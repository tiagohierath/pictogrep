import argparse
from functools import lru_cache
import json
import mimetypes
from pathlib import Path
import shutil
import sys
import threading
import time
from urllib.parse import parse_qs, quote, unquote, urlparse
from urllib.request import urlopen
import webbrowser

from build_index import build_index, default_optimization_settings
from manage_collections import collection_name, create_collection, link_image
from pictogrep_core import (
    BASE,
    COLLECTIONS_DIR,
    IMAGE_EXTENSIONS,
    MODEL_NAME,
    PRETRAINED,
    VERSION,
    choose_viewer,
    collection_images,
    collection_names,
    find_milklily_project,
    image_files,
    index_is_due,
    index_state,
    index_stats,
    remembered_sources,
    search as clip_search,
    tags_for_image,
)
from storyboard import (
    DEFAULT_PORT,
    StoryboardHandler,
    StoryboardServer,
    collect_images,
    image_record,
    page_html as storyboard_page_html,
)


WEB_DIR = BASE / "web"
LIBRARY_DIR = BASE / "library"
MAX_UPLOAD_BYTES = 100 * 1024 * 1024


@lru_cache(maxsize=20_000)
def cached_image_size(path_value, mtime_ns, file_size):
    del mtime_ns, file_size
    try:
        from PIL import Image

        with Image.open(path_value) as image:
            width, height = image.size
            orientation = image.getexif().get(274)
            if orientation in {5, 6, 7, 8}:
                width, height = height, width
            return width, height
    except Exception:
        return 0, 0


def image_dimensions(path):
    try:
        stat = Path(path).stat()
        return cached_image_size(str(path), stat.st_mtime_ns, stat.st_size)
    except OSError:
        return 0, 0


def safe_upload_name(value):
    name = Path(value or "image").name
    stem = "".join(char if char.isalnum() or char in "._- " else "-" for char in Path(name).stem)
    stem = "-".join(stem.split()).strip(".-")[:100] or "image"
    suffix = Path(name).suffix.lower()
    if suffix not in IMAGE_EXTENSIONS:
        raise ValueError("unsupported image type")
    return stem + suffix


def unique_path(folder, name):
    target = folder / name
    stem = target.stem
    suffix = target.suffix
    number = 2
    while target.exists():
        target = folder / f"{stem}-{number}{suffix}"
        number += 1
    return target


def board_records(out_dir):
    if not out_dir.exists():
        return []
    boards = []
    for path in out_dir.iterdir():
        if not path.is_file() or path.suffix.lower() not in IMAGE_EXTENSIONS:
            continue
        metadata = {}
        sidecar = path.with_suffix(".json")
        if sidecar.is_file():
            try:
                loaded = json.loads(sidecar.read_text(encoding="utf-8"))
                if isinstance(loaded, dict):
                    metadata = loaded
            except (OSError, json.JSONDecodeError):
                pass
        boards.append(
            {
                "name": path.name,
                "url": "/board/" + quote(path.name),
                "mtime": int(path.stat().st_mtime),
                "source": metadata.get("source", ""),
                "aspect": metadata.get("aspect", ""),
                "query": metadata.get("query", ""),
                "tags": metadata.get("tags", []),
            }
        )
    return sorted(boards, key=lambda board: board["mtime"], reverse=True)


def image_tag_map():
    tagged = {}
    for name in collection_names():
        for path in collection_images(name):
            tagged.setdefault(path.resolve(), []).append(name)
    return tagged


def path_is_inside(path, folder):
    try:
        Path(path).resolve().relative_to(Path(folder).resolve())
        return True
    except ValueError:
        return False


def existing_app_url(port, path="/"):
    base = f"http://127.0.0.1:{port}"
    try:
        with urlopen(base + "/api/app/state", timeout=0.35) as response:
            state = json.load(response)
        if state.get("version"):
            return base + path
    except Exception:
        pass
    return None


class PictogrepHandler(StoryboardHandler):
    server_version = "Pictogrep/1.0"

    def parsed_json(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length > 2 * 1024 * 1024:
            raise ValueError("request is too large")
        return json.loads(self.rfile.read(length) or b"{}")

    def send_file(self, path, content_type=None, include_body=True):
        path = Path(path)
        if not path.is_file():
            self.send_error(404)
            return
        content_type = content_type or mimetypes.guess_type(path.name)[0] or "application/octet-stream"
        self.send_response(200)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(path.stat().st_size))
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        if include_body:
            with path.open("rb") as fh:
                shutil.copyfileobj(fh, self.wfile)

    def app_image_record(self, path, score=None, tag_map=None):
        path = Path(path).expanduser().resolve()
        record = image_record(self.server.id_for_path(path), path)
        width, height = image_dimensions(path)
        record.update(
            {
                "path": str(path),
                "tags": (tag_map or {}).get(path, []) if tag_map is not None else tags_for_image(path),
                "width": width,
                "height": height,
            }
        )
        if score is not None:
            record["score"] = score
        return record

    def app_state(self):
        stats = index_stats()
        try:
            viewer = " ".join(choose_viewer())
        except Exception as exc:
            viewer = f"Unavailable: {exc}"
        return {
            "version": VERSION,
            "model": MODEL_NAME,
            "pretrained": PRETRAINED,
            "index": stats,
            "indexJob": self.server.job_snapshot(),
            "sources": remembered_sources(),
            "tags": [
                {"name": name, "count": len(collection_images(name))}
                for name in collection_names()
            ],
            "boards": len(board_records(self.server.out_dir)),
            "paths": {
                "home": str(BASE),
                "library": str(LIBRARY_DIR),
                "boards": str(self.server.out_dir),
                "tags": str(COLLECTIONS_DIR),
            },
            "viewer": viewer,
        }

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/":
            self.send_file(WEB_DIR / "index.html", "text/html; charset=utf-8")
            return
        if parsed.path == "/practice":
            body = storyboard_page_html().encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if parsed.path.startswith("/assets/"):
            name = Path(unquote(parsed.path.split("/assets/", 1)[1])).name
            self.send_file(WEB_DIR / name)
            return
        if parsed.path == "/api/app/state":
            self.send_json(self.app_state())
            return
        if parsed.path == "/api/app/images":
            params = parse_qs(parsed.query)
            tag = params.get("tag", [""])[0]
            source = params.get("source", [""])[0]
            mode = params.get("mode", ["recent"])[0]
            try:
                count = min(500, max(1, int(params.get("count", ["120"])[0])))
                paths = collection_images(tag) if tag else list(self.server.paths)
                if source:
                    paths = [path for path in paths if path_is_inside(path, source)]
            except (ValueError, TypeError) as exc:
                self.send_json({"ok": False, "error": str(exc)}, status=400)
                return
            if mode == "recent":
                paths = sorted(paths, key=lambda path: path.stat().st_mtime if path.exists() else 0, reverse=True)
            tag_map = image_tag_map()
            records = [self.app_image_record(path, tag_map=tag_map) for path in paths[:count] if path.exists()]
            self.send_json({"ok": True, "images": records, "total": len(paths)})
            return
        if parsed.path == "/api/app/folders":
            folders = []
            for source in remembered_sources():
                path = Path(source).expanduser().resolve()
                paths = [image for image in self.server.paths if path_is_inside(image, path)]
                paths.sort(key=lambda image: image.stat().st_mtime if image.exists() else 0, reverse=True)
                folders.append(
                    {
                        "kind": "source",
                        "name": path.name or str(path),
                        "value": str(path),
                        "count": len(paths),
                        "images": [self.app_image_record(image, tag_map={}) for image in paths[:4] if image.exists()],
                    }
                )
            for name in collection_names():
                paths = collection_images(name)
                paths.sort(key=lambda image: image.stat().st_mtime if image.exists() else 0, reverse=True)
                folders.append(
                    {
                        "kind": "tag",
                        "name": name,
                        "value": name,
                        "count": len(paths),
                        "images": [self.app_image_record(image, tag_map={}) for image in paths[:4] if image.exists()],
                    }
                )
            self.send_json({"ok": True, "folders": folders})
            return
        if parsed.path == "/api/app/search":
            params = parse_qs(parsed.query)
            query = params.get("q", [""])[0].strip()
            tag = params.get("tag", [""])[0]
            source = params.get("source", [""])[0]
            try:
                limit = min(200, max(1, int(params.get("limit", ["80"])[0])))
                tagged = {path.resolve() for path in collection_images(tag)} if tag else None
                results = clip_search(query, limit=limit) if query else []
                images = []
                tag_map = image_tag_map()
                for result in results:
                    path = Path(result["path"]).expanduser().resolve()
                    if not path.exists() or (tagged is not None and path not in tagged):
                        continue
                    if source and not path_is_inside(path, source):
                        continue
                    images.append(self.app_image_record(path, result["score"], tag_map))
                self.send_json({"ok": True, "images": images, "query": query})
            except Exception as exc:
                self.send_json({"ok": False, "error": str(exc)}, status=400)
            return
        if parsed.path == "/api/app/boards":
            self.send_json({"ok": True, "boards": board_records(self.server.out_dir)})
            return
        if parsed.path.startswith("/board/"):
            name = Path(unquote(parsed.path.split("/board/", 1)[1])).name
            self.send_file(self.server.out_dir / name)
            return
        super().do_GET()

    def do_HEAD(self):
        parsed = urlparse(self.path)
        if parsed.path == "/":
            self.send_file(WEB_DIR / "index.html", "text/html; charset=utf-8", include_body=False)
            return
        if parsed.path.startswith("/assets/"):
            name = Path(unquote(parsed.path.split("/assets/", 1)[1])).name
            self.send_file(WEB_DIR / name, include_body=False)
            return
        if parsed.path.startswith("/board/"):
            name = Path(unquote(parsed.path.split("/board/", 1)[1])).name
            self.send_file(self.server.out_dir / name, include_body=False)
            return
        super().do_HEAD()

    def do_POST(self):
        parsed = urlparse(self.path)
        if parsed.path == "/api/app/index":
            try:
                data = self.parsed_json()
                requested = data.get("folders") or ([] if not data.get("folder") else [data["folder"]])
                if data.get("includeLibrary"):
                    requested.append(str(LIBRARY_DIR))
                folders = self.server.resolve_index_folders(requested)
                if not folders:
                    raise ValueError("Add an image folder first.")
                if not self.server.start_index(folders):
                    self.send_json({"ok": False, "error": "An index job is already running."}, status=409)
                    return
                self.send_json({"ok": True, "folders": folders}, status=202)
            except (ValueError, TypeError) as exc:
                self.send_json({"ok": False, "error": str(exc)}, status=400)
            return
        if parsed.path == "/api/app/upload":
            length = int(self.headers.get("Content-Length", "0"))
            if length <= 0 or length > MAX_UPLOAD_BYTES:
                self.send_json({"ok": False, "error": "Image must be between 1 byte and 100 MB."}, status=400)
                return
            try:
                params = parse_qs(parsed.query)
                name = safe_upload_name(params.get("name", [""])[0])
                LIBRARY_DIR.mkdir(parents=True, exist_ok=True)
                target = unique_path(LIBRARY_DIR, name)
                remaining = length
                with target.open("wb") as fh:
                    while remaining:
                        chunk = self.rfile.read(min(1024 * 1024, remaining))
                        if not chunk:
                            raise ValueError("upload ended early")
                        fh.write(chunk)
                        remaining -= len(chunk)
                from PIL import Image

                with Image.open(target) as image:
                    image.verify()
                folder = params.get("folder", [""])[0].strip()
                if folder:
                    link_image(create_collection(folder), target)
                self.send_json({"ok": True, "name": target.name, "path": str(target), "folder": folder})
            except Exception as exc:
                if "target" in locals():
                    target.unlink(missing_ok=True)
                self.send_json({"ok": False, "error": str(exc)}, status=400)
            return
        if parsed.path == "/api/app/tags":
            try:
                data = self.parsed_json()
                action = data.get("action", "add")
                tag = collection_name(data.get("tag", ""))
                folder = create_collection(tag)
                if action == "create":
                    self.send_json({"ok": True, "tag": tag})
                    return
                if action == "fill":
                    prompt = str(data.get("prompt", "")).strip()
                    if not prompt:
                        raise ValueError("AI prompt cannot be empty")
                    limit = min(200, max(1, int(data.get("limit", 50))))
                    results = clip_search(prompt, limit=limit)
                    added = sum(link_image(folder, result["path"]) for result in results)
                    self.send_json(
                        {
                            "ok": True,
                            "tag": tag,
                            "prompt": prompt,
                            "added": added,
                            "matches": len(results),
                        }
                    )
                    return
                image_id = int(data["imageId"])
                source = self.server.paths[image_id].resolve()
                if action == "add":
                    added = link_image(folder, source)
                    self.send_json({"ok": True, "tag": tag, "added": added})
                    return
                if action == "remove":
                    removed = False
                    for existing in folder.iterdir():
                        if existing.is_symlink() and existing.resolve() == source:
                            existing.unlink()
                            removed = True
                    self.send_json({"ok": True, "tag": tag, "removed": removed})
                    return
                raise ValueError("unknown tag action")
            except (FileNotFoundError, KeyError, IndexError, RuntimeError, TypeError, ValueError) as exc:
                self.send_json({"ok": False, "error": str(exc)}, status=400)
            return
        super().do_POST()


class PictogrepServer(StoryboardServer):
    def __init__(self, address, paths, out_dir):
        super().__init__(address, PictogrepHandler, paths, out_dir)
        self.job_lock = threading.Lock()
        self.index_job = {
            "state": "idle",
            "current": 0,
            "total": 0,
            "message": "Ready",
            "updatedAt": time.time(),
        }

    def job_snapshot(self):
        with self.job_lock:
            return dict(self.index_job)

    def update_job(self, **values):
        with self.job_lock:
            self.index_job.update(values)
            self.index_job["updatedAt"] = time.time()

    def replace_paths(self, paths):
        self.paths = list(paths)
        self.path_ids = {path.resolve(): i for i, path in enumerate(self.paths)}

    def resolve_index_folders(self, requested):
        combined = list(remembered_sources())
        combined.extend(str(value).strip() for value in requested if str(value).strip())
        folders = []
        seen = set()
        for value in combined:
            path = Path(value).expanduser().resolve()
            if path in seen:
                continue
            if not path.exists():
                raise ValueError(f"Folder does not exist: {path}")
            if not path.is_dir():
                raise ValueError(f"Not a folder: {path}")
            seen.add(path)
            folders.append(str(path))
        return folders

    def start_index(self, folders):
        with self.job_lock:
            if self.index_job.get("state") == "running":
                return False
            self.index_job = {
                "state": "running",
                "current": 0,
                "total": 0,
                "message": "Scanning image folders…",
                "updatedAt": time.time(),
            }

        def progress(event):
            phase = event.get("phase", "indexing")
            current = event.get("current", 0)
            total = event.get("total", 0)
            if phase == "scanned":
                message = f"Found {total} images. Loading the search model…"
            elif phase == "indexing":
                name = Path(event.get("file", "")).name
                message = f"Indexing {current} of {total}: {name}"
            else:
                message = event.get("message", "Updating the library…")
            self.update_job(current=current, total=total, message=message)

        def worker():
            try:
                build_index(
                    folders,
                    progress=False,
                    optimization_settings=default_optimization_settings(),
                    progress_callback=progress,
                )
                self.replace_paths(collect_images())
                self.update_job(
                    state="complete",
                    current=len(self.paths),
                    total=len(self.paths),
                    message=f"Library ready: {len(self.paths)} indexed images.",
                )
            except BaseException as exc:
                self.update_job(state="error", message=str(exc))

        threading.Thread(target=worker, name="pictogrep-index", daemon=True).start()
        return True


def main(argv=None):
    parser = argparse.ArgumentParser(description="Open the Pictogrep local web app.")
    parser.add_argument("folder", nargs="?", help="optional image folder for the practice studio")
    parser.add_argument("--out", help="output folder for PNG boards")
    parser.add_argument("--project", action="store_true", help="use the current Milklily project")
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument("--no-open", action="store_true", help="do not open the browser automatically")
    parser.add_argument("--page", choices=("app", "practice"), default="app")
    args = parser.parse_args(argv)

    folder = args.folder
    out = args.out
    if args.project:
        project = find_milklily_project()
        if not project:
            parser.error("--project needs a Milklily project (no milklily.conf found)")
        if folder is None:
            candidate = project / "refs" / "visual"
            if image_files(candidate):
                folder = str(candidate)
        if out is None:
            out = str(project / "storyboards" / "inbox")

    paths = collect_images(folder)
    out_dir = Path(out or BASE / "storyboards").expanduser().resolve()
    out_dir.mkdir(parents=True, exist_ok=True)
    try:
        server = PictogrepServer(("127.0.0.1", args.port), paths, out_dir)
    except OSError as exc:
        page_path = "/practice" if args.page == "practice" else "/"
        existing = existing_app_url(args.port, page_path)
        if existing and folder is None and out is None and not args.project:
            print(f"Pictogrep is already running: {existing}")
            if not args.no_open:
                webbrowser.open(existing)
            return 0
        if args.port != DEFAULT_PORT or exc.errno != 98:
            parser.error(f"could not start Pictogrep on port {args.port}: {exc}")
        server = PictogrepServer(("127.0.0.1", 0), paths, out_dir)
        print(f"Port {args.port} is already in use; using port {server.server_port} instead.")

    if index_is_due() and remembered_sources():
        server.start_index(server.resolve_index_folders([]))

    path = "/practice" if args.page == "practice" else "/"
    url = f"http://127.0.0.1:{server.server_port}{path}"
    print(f"Pictogrep: {url}")
    print(f"{len(paths)} indexed images available. Press Ctrl+C to stop.")
    if not args.no_open:
        webbrowser.open(url)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nPictogrep stopped.")
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
