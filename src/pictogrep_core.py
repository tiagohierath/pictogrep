from pathlib import Path
import json
import logging
import os
import shlex
import shutil
import subprocess
import tempfile
import time

BASE = Path(__file__).resolve().parents[1]
VERSION = "0.2.0"
DATA_DIR = BASE / "data"
EMBEDDINGS_PATH = DATA_DIR / "embeddings.npy"
METADATA_PATH = DATA_DIR / "metadata.json"
INDEX_STATE_PATH = DATA_DIR / "index-state.json"
COLLECTIONS_DIR = BASE / "collections"
OPTIMIZED_DIR = DATA_DIR / "optimized-images"
WEEK_SECONDS = 7 * 24 * 60 * 60
DAY_SECONDS = 24 * 60 * 60
IMAGE_EXTENSIONS = (".jpg", ".jpeg", ".png", ".webp")
MODEL_NAME = os.environ.get("PICTOGREP_MODEL", "ViT-B-32")
PRETRAINED = os.environ.get("PICTOGREP_PRETRAINED", "laion2b_s34b_b79k")

_model = None
_tokenizer = None


def quiet_hf_warnings():
    if os.environ.get("PICTOGREP_VERBOSE"):
        return
    logging.getLogger("huggingface_hub").setLevel(logging.ERROR)
    logging.getLogger("huggingface_hub.utils._http").setLevel(logging.ERROR)


def available_index(base=BASE):
    return (base / "data" / "embeddings.npy").exists() and (base / "data" / "metadata.json").exists()


def env_int(name, default):
    try:
        return int(os.environ.get(name, default))
    except (TypeError, ValueError):
        return default


MAINTENANCE_SECONDS = max(0, env_int("PICTOGREP_MAINTENANCE_SECONDS", DAY_SECONDS))


def atomic_write_json(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = None
    try:
        with tempfile.NamedTemporaryFile(
            "w",
            encoding="utf-8",
            dir=path.parent,
            prefix=f".{path.name}.",
            suffix=".tmp",
            delete=False,
        ) as fh:
            tmp = Path(fh.name)
            json.dump(data, fh, indent=2)
            fh.write("\n")
        os.replace(tmp, path)
    finally:
        if tmp and tmp.exists():
            tmp.unlink()


def atomic_save_npy(path, array):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = None
    try:
        with tempfile.NamedTemporaryFile(
            "wb",
            dir=path.parent,
            prefix=f".{path.name}.",
            suffix=".tmp",
            delete=False,
        ) as fh:
            tmp = Path(fh.name)
            import numpy as np

            np.save(fh, array)
        os.replace(tmp, path)
    finally:
        if tmp and tmp.exists():
            tmp.unlink()


def index_state(base=BASE):
    path = base / "data" / "index-state.json"
    if not path.exists():
        return {}
    with path.open() as fh:
        return json.load(fh)


def write_index_state(state, base=BASE):
    atomic_write_json(base / "data" / "index-state.json", state)


def remembered_sources(base=BASE):
    sources = index_state(base).get("sources", [])
    if sources:
        return sources
    metadata = base / "data" / "metadata.json"
    if not metadata.exists():
        return []
    try:
        with metadata.open() as fh:
            paths = [str(Path(path).expanduser().resolve()) for path in json.load(fh)]
        common = Path(os.path.commonpath(paths)) if paths else None
        if common and common.is_file():
            common = common.parent
        return [str(common)] if common and common.exists() else []
    except (OSError, ValueError, json.JSONDecodeError):
        return []


def index_is_due(base=BASE, now=None):
    if not available_index(base):
        return True
    updated = index_state(base).get("indexed_at", 0)
    return not updated or (now or time.time()) - updated >= WEEK_SECONDS


def index_maintenance_is_due(base=BASE, now=None):
    if not available_index(base):
        return False
    maintained = index_state(base).get("maintained_at", 0)
    return not maintained or (now or time.time()) - maintained >= MAINTENANCE_SECONDS


def metadata_key(path):
    try:
        return str(Path(path).expanduser().resolve())
    except (OSError, RuntimeError):
        return str(Path(path).expanduser().absolute())


def duplicate_metadata_count(metadata):
    seen = set()
    duplicates = 0
    for path in metadata:
        key = metadata_key(path)
        if key in seen:
            duplicates += 1
            continue
        seen.add(key)
    return duplicates


def dedupe_index(base=BASE, now=None):
    if not available_index(base):
        return {
            "available": False,
            "changed": False,
            "removed": 0,
            "kept": 0,
            "total": 0,
        }

    import numpy as np

    embeddings_path = base / "data" / "embeddings.npy"
    metadata_path = base / "data" / "metadata.json"
    embeddings = np.load(embeddings_path)
    with metadata_path.open() as fh:
        metadata = json.load(fh)

    if embeddings.ndim == 0 or embeddings.shape[0] != len(metadata):
        return {
            "available": True,
            "changed": False,
            "removed": 0,
            "kept": len(metadata),
            "total": len(metadata),
            "error": (
                "index metadata and embeddings are out of sync "
                f"({len(metadata)} paths, {embeddings.shape[0] if embeddings.ndim else 0} embeddings)"
            ),
        }

    seen = set()
    keep = []
    for i, path in enumerate(metadata):
        key = metadata_key(path)
        if key in seen:
            continue
        seen.add(key)
        keep.append(i)

    now = now or time.time()
    state = index_state(base)
    state["maintained_at"] = now

    removed = len(metadata) - len(keep)
    if not removed:
        write_index_state(state, base)
        return {
            "available": True,
            "changed": False,
            "removed": 0,
            "kept": len(metadata),
            "total": len(metadata),
        }

    compact_metadata = [metadata[i] for i in keep]
    compact_embeddings = embeddings[keep]
    atomic_save_npy(embeddings_path, compact_embeddings)
    atomic_write_json(metadata_path, compact_metadata)
    write_index_state(state, base)
    return {
        "available": True,
        "changed": True,
        "removed": removed,
        "kept": len(compact_metadata),
        "total": len(metadata),
    }


def index_stats(base=BASE):
    if not available_index(base):
        return None
    with (base / "data" / "metadata.json").open() as fh:
        metadata = json.load(fh)
    return {
        "count": len(metadata),
        "embeddings": str(base / "data" / "embeddings.npy"),
        "metadata": str(base / "data" / "metadata.json"),
        "sources": remembered_sources(base),
        "due": index_is_due(base),
        "maintenance_due": index_maintenance_is_due(base),
        "duplicates": duplicate_metadata_count(metadata),
    }


def image_files(folder):
    root = Path(folder).expanduser().resolve()
    if root.is_file():
        return [root] if root.suffix.lower() in IMAGE_EXTENSIONS else []
    skipped = {".git", ".venv", "venv", "__pycache__", "node_modules", "data", "storyboards", "collections"}
    paths = []
    for directory, dirs, files in os.walk(root):
        dirs[:] = [name for name in dirs if name not in skipped]
        paths.extend(Path(directory) / name for name in files if Path(name).suffix.lower() in IMAGE_EXTENSIONS)
    return sorted(paths)


def collection_names():
    if not COLLECTIONS_DIR.exists():
        return []
    return sorted(path.name for path in COLLECTIONS_DIR.iterdir() if path.is_dir())


def collection_images(name):
    if name not in collection_names():
        raise ValueError(f"unknown tag: {name}")
    folder = COLLECTIONS_DIR / name
    return sorted(
        path.resolve()
        for path in folder.iterdir()
        if path.is_file() and path.suffix.lower() in IMAGE_EXTENSIONS
    )


def tags_for_image(path):
    path = Path(path).expanduser().resolve()
    return [name for name in collection_names() if path in set(collection_images(name))]


def find_milklily_project(start=None):
    directory = Path(start or Path.cwd()).expanduser().resolve()
    if directory.is_file():
        directory = directory.parent
    while directory != directory.parent:
        if (directory / "milklily.conf").is_file():
            return directory
        directory = directory.parent
    if (directory / "milklily.conf").is_file():
        return directory
    return None


def load_text_model():
    global _model, _tokenizer
    if _model is None or _tokenizer is None:
        quiet_hf_warnings()
        import open_clip

        _model, _, _ = open_clip.create_model_and_transforms(
            MODEL_NAME,
            pretrained=PRETRAINED,
        )
        _model.eval()
        _tokenizer = open_clip.get_tokenizer(MODEL_NAME)
    return _model, _tokenizer


def encode_text(query):
    import torch

    model, tokenizer = load_text_model()
    text = tokenizer([query])
    with torch.no_grad():
        vector = model.encode_text(text)
    vector /= vector.norm(dim=-1, keepdim=True)
    return vector.cpu().numpy()[0]


def load_index(base=BASE):
    import numpy as np

    embeddings_path = base / "data" / "embeddings.npy"
    metadata_path = base / "data" / "metadata.json"
    if not embeddings_path.exists() or not metadata_path.exists():
        raise FileNotFoundError(
            "No Pictogrep index found. Run: pictogrep index /path/to/images"
        )
    embeddings = np.load(embeddings_path)
    with metadata_path.open() as fh:
        metadata = json.load(fh)
    if embeddings.ndim == 0 or embeddings.shape[0] != len(metadata):
        raise RuntimeError(
            "Pictogrep index metadata and embeddings are out of sync. "
            "Run: pictogrep index /path/to/images"
        )
    return embeddings, metadata


def search(query, limit=50, base=BASE):
    import numpy as np

    query = query.strip()
    if not query:
        return []
    embeddings, metadata = load_index(base)
    vector = encode_text(query)
    scores = embeddings @ vector
    top = np.argsort(scores)[::-1][:limit]
    return [
        {
            "score": float(scores[i]),
            "path": metadata[i],
            "name": Path(metadata[i]).name,
        }
        for i in top
    ]


def choose_viewer():
    configured = os.environ.get("PICTOGREP_VIEWER")
    if configured:
        return shlex.split(configured)
    if shutil.which("mpv"):
        return ["mpv", "--image-display-duration=3"]
    if shutil.which("xdg-open"):
        return ["xdg-open"]
    if shutil.which("gio"):
        return ["gio", "open"]
    raise RuntimeError("No viewer found. Install mpv or set PICTOGREP_VIEWER.")


def single_file_opener(viewer):
    name = Path(viewer[0]).name if viewer else ""
    return name in {"xdg-open", "open"} or viewer[:2] == ["gio", "open"]


def open_files(files, wait=True):
    files = [str(f) for f in files if f]
    if not files:
        return
    viewer = choose_viewer()
    if single_file_opener(viewer):
        if len(files) > 1:
            raise RuntimeError(
                "Multiple-image slideshow needs mpv or PICTOGREP_VIEWER set to a viewer that accepts many files."
            )
        subprocess.Popen(viewer + files, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        return
    if not wait:
        subprocess.Popen(viewer + files, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        return
    subprocess.run(viewer + files, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def reveal_file(file):
    path = Path(file).expanduser().resolve()
    target = path.parent if path.exists() else path
    opener = None
    for candidate in (["xdg-open"], ["gio", "open"], ["open"]):
        if shutil.which(candidate[0]):
            opener = candidate
            break
    if not opener:
        raise RuntimeError("No folder opener found. Install xdg-open or gio.")
    subprocess.Popen(opener + [str(target)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def copy_text(text):
    commands = (
        ["wl-copy"],
        ["xclip", "-selection", "clipboard"],
        ["xsel", "--clipboard", "--input"],
        ["pbcopy"],
    )
    for command in commands:
        if shutil.which(command[0]):
            subprocess.run(command, input=text.encode(), check=True)
            return
    raise RuntimeError("No clipboard command found. Install wl-clipboard, xclip, or xsel.")
