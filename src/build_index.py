import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import time

from pictogrep_core import (
    BASE,
    COLLECTIONS_DIR,
    DATA_DIR,
    MODEL_NAME,
    OPTIMIZED_DIR,
    PRETRAINED,
    atomic_write_json,
    dedupe_index,
    env_int,
    image_files,
    index_maintenance_is_due,
    index_is_due,
    index_state,
    remembered_sources,
    write_index_state,
)


OPTIMIZED_MANIFEST_PATH = OPTIMIZED_DIR / "manifest.json"
OPTIMIZATION_VERSION = 1
DEFAULT_OPTIMIZE_MAX_SIDE = 1920
DEFAULT_OPTIMIZE_MIN_BYTES = 512 * 1024
DEFAULT_WEBP_QUALITY = 82


def env_flag(name, default=True):
    value = os.environ.get(name)
    if value is None:
        return default
    return value.strip().lower() not in {"0", "false", "no", "off"}


def default_optimization_settings():
    return {
        "version": OPTIMIZATION_VERSION,
        "enabled": env_flag("PICTOGREP_OPTIMIZE_IMAGES", True),
        "format": "webp",
        "max_side": max(1, env_int("PICTOGREP_OPTIMIZE_MAX_SIDE", DEFAULT_OPTIMIZE_MAX_SIDE)),
        "min_bytes": max(0, env_int("PICTOGREP_OPTIMIZE_MIN_BYTES", DEFAULT_OPTIMIZE_MIN_BYTES)),
        "quality": min(100, max(1, env_int("PICTOGREP_WEBP_QUALITY", DEFAULT_WEBP_QUALITY))),
    }


def resolve_optimization_settings(args):
    settings = default_optimization_settings()
    saved = index_state().get("optimization") if getattr(args, "refresh", False) else None
    if isinstance(saved, dict) and saved:
        for key in ("enabled", "format", "max_side", "min_bytes", "quality", "version"):
            if key in saved:
                settings[key] = saved[key]
    if getattr(args, "no_optimize", False):
        settings["enabled"] = False
    if getattr(args, "optimize_max_side", None) is not None:
        settings["max_side"] = max(1, args.optimize_max_side)
    if getattr(args, "optimize_min_bytes", None) is not None:
        settings["min_bytes"] = max(0, args.optimize_min_bytes)
    if getattr(args, "webp_quality", None) is not None:
        settings["quality"] = min(100, max(1, args.webp_quality))
    settings["version"] = OPTIMIZATION_VERSION
    settings["format"] = "webp"
    return settings


def optimization_settings_are_stale(settings):
    return index_state().get("optimization") != settings


def load_optimized_manifest():
    if not OPTIMIZED_MANIFEST_PATH.exists():
        return {}
    try:
        with OPTIMIZED_MANIFEST_PATH.open() as fh:
            data = json.load(fh)
        return data if isinstance(data, dict) else {}
    except (OSError, json.JSONDecodeError):
        return {}


def save_optimized_manifest(manifest):
    atomic_write_json(OPTIMIZED_MANIFEST_PATH, manifest)


def safe_stem(path):
    clean = re.sub(r"[^a-zA-Z0-9._-]+", "-", Path(path).stem).strip(".-")
    return clean[:80] or "image"


def optimized_key(source):
    return hashlib.sha256(str(source).encode("utf-8")).hexdigest()[:20]


def source_fingerprint(source, settings):
    stat = source.stat()
    return {
        "source": str(source),
        "source_size": stat.st_size,
        "source_mtime_ns": stat.st_mtime_ns,
        "version": settings["version"],
        "format": settings["format"],
        "max_side": settings["max_side"],
        "min_bytes": settings["min_bytes"],
        "quality": settings["quality"],
    }


def manifest_record_is_current(record, fingerprint):
    if not isinstance(record, dict):
        return False
    for key, value in fingerprint.items():
        if record.get(key) != value:
            return False
    status = record.get("status")
    if status == "skipped":
        return True
    if status == "optimized":
        output = Path(record.get("output", ""))
        return output.is_file()
    return False


def should_optimize(source, image, settings):
    if not settings["enabled"]:
        return False
    try:
        source_size = source.stat().st_size
    except OSError:
        source_size = 0
    return source_size >= settings["min_bytes"] or max(image.size) > settings["max_side"]


def webp_mode(image):
    if image.mode in {"RGBA", "LA"} or image.info.get("transparency") is not None:
        return "RGBA"
    return "RGB"


def optimized_image_path(source):
    return OPTIMIZED_DIR / f"{optimized_key(source)}-{safe_stem(source)}.webp"


def is_managed_optimized_path(path):
    try:
        Path(path).expanduser().resolve().relative_to(OPTIMIZED_DIR.resolve())
        return True
    except ValueError:
        return False


def optimize_image(source, manifest, settings):
    from PIL import Image, ImageOps

    source = Path(source).expanduser().resolve()
    if is_managed_optimized_path(source):
        return source, "reused"
    if not settings["enabled"]:
        return source, "disabled"

    key = optimized_key(source)
    fingerprint = source_fingerprint(source, settings)
    record = manifest.get(key)
    if manifest_record_is_current(record, fingerprint):
        if record.get("status") == "optimized":
            return Path(record["output"]), "reused"
        return source, "skipped"

    with Image.open(source) as original:
        image = ImageOps.exif_transpose(original)
        original_size = image.size
        if not should_optimize(source, image, settings):
            manifest[key] = {
                **fingerprint,
                "status": "skipped",
                "reason": "below-threshold",
                "width": original_size[0],
                "height": original_size[1],
            }
            return source, "skipped"

        image = image.convert(webp_mode(image))
        image.thumbnail(
            (settings["max_side"], settings["max_side"]),
            Image.Resampling.LANCZOS,
        )
        output = optimized_image_path(source)
        output.parent.mkdir(parents=True, exist_ok=True)
        tmp = None
        try:
            with tempfile.NamedTemporaryFile(
                "wb",
                dir=output.parent,
                prefix=f".{output.name}.",
                suffix=".tmp",
                delete=False,
            ) as fh:
                tmp = Path(fh.name)
            image.save(
                tmp,
                format="WEBP",
                quality=settings["quality"],
                method=6,
            )
            optimized_size = tmp.stat().st_size
            source_size = source.stat().st_size
            resized = image.size != original_size
            if optimized_size >= source_size and not resized:
                tmp.unlink(missing_ok=True)
                manifest[key] = {
                    **fingerprint,
                    "status": "skipped",
                    "reason": "not-smaller",
                    "width": original_size[0],
                    "height": original_size[1],
                }
                return source, "skipped"
            os.replace(tmp, output)
        finally:
            if tmp:
                tmp.unlink(missing_ok=True)

    manifest[key] = {
        **fingerprint,
        "status": "optimized",
        "output": str(output),
        "source_width": original_size[0],
        "source_height": original_size[1],
        "width": image.size[0],
        "height": image.size[1],
        "optimized_size": output.stat().st_size,
    }
    return output, "optimized"


def prune_unused_optimized_files(manifest, used_paths):
    used = {str(Path(path).expanduser().resolve()) for path in used_paths}
    removed = 0
    for key, record in list(manifest.items()):
        if not isinstance(record, dict) or record.get("status") != "optimized":
            continue
        output = record.get("output")
        if not output:
            continue
        try:
            resolved = str(Path(output).expanduser().resolve())
        except (OSError, RuntimeError):
            resolved = output
        if resolved in used:
            continue
        try:
            Path(output).unlink(missing_ok=True)
            removed += 1
        except OSError:
            pass
        del manifest[key]
    return removed


def prepare_image_for_index(file, manifest, settings):
    try:
        return optimize_image(file, manifest, settings)
    except Exception:
        return file, "failed"


def build_index(
    folders,
    remember=True,
    progress=True,
    optimization_settings=None,
    progress_callback=None,
):
    import numpy as np
    import open_clip
    import torch
    from PIL import Image

    optimization_settings = optimization_settings or default_optimization_settings()
    roots = [Path(folder).expanduser().resolve() for folder in folders]
    scan_roots = list(roots)
    if COLLECTIONS_DIR.exists():
        scan_roots.append(COLLECTIONS_DIR)
    files = []
    seen = set()
    for root in scan_roots:
        if not root.exists():
            if progress:
                print(f"Skipping missing folder: {root}")
            continue
        for file in image_files(root):
            resolved = file.resolve()
            if resolved in seen:
                continue
            seen.add(resolved)
            files.append(resolved)
    if not files:
        raise SystemExit("No images found in: " + ", ".join(map(str, roots)))

    if progress_callback:
        progress_callback({"phase": "scanned", "current": 0, "total": len(files)})

    model, _, preprocess = open_clip.create_model_and_transforms(
        MODEL_NAME,
        pretrained=PRETRAINED,
    )
    model.eval()

    if progress:
        print(f"Found {len(files)} images in {len(scan_roots)} folder(s)")
    embeddings = []
    metadata = []
    used_index_paths = []
    manifest = load_optimized_manifest()
    optimize_counts = {"optimized": 0, "reused": 0, "skipped": 0, "disabled": 0, "failed": 0}
    indexed_seen = set()

    for i, file in enumerate(files, start=1):
        if progress_callback:
            progress_callback(
                {
                    "phase": "indexing",
                    "current": i,
                    "total": len(files),
                    "file": str(file),
                }
            )
        index_file, optimize_status = prepare_image_for_index(file, manifest, optimization_settings)
        optimize_counts[optimize_status] = optimize_counts.get(optimize_status, 0) + 1
        resolved_index_file = index_file.resolve()
        if resolved_index_file in indexed_seen:
            continue
        indexed_seen.add(resolved_index_file)
        try:
            with Image.open(index_file) as opened:
                image = preprocess(opened.convert("RGB")).unsqueeze(0)
            with torch.no_grad():
                vector = model.encode_image(image)
            vector /= vector.norm(dim=-1, keepdim=True)
            embeddings.append(vector.cpu().numpy()[0])
            metadata.append(str(file))
            used_index_paths.append(str(index_file))
            if progress:
                suffix = f" -> {index_file}" if index_file != file else ""
                print(f"{i}/{len(files)} {file}{suffix}")
        except Exception as exc:
            if progress:
                print(f"Failed: {file} {exc}")

    DATA_DIR.mkdir(exist_ok=True)
    np.save(DATA_DIR / "embeddings.npy", np.array(embeddings))
    with (DATA_DIR / "metadata.json").open("w") as fh:
        json.dump(metadata, fh, indent=2)
    save_optimized_manifest(manifest)
    pruned = prune_unused_optimized_files(manifest, used_index_paths)
    if pruned:
        save_optimized_manifest(manifest)
    if remember:
        state = index_state()
        state.update(
            {
                "indexed_at": time.time(),
                "sources": [str(root) for root in roots],
                "optimization": optimization_settings,
                "optimized_images": {
                    "optimized": optimize_counts.get("optimized", 0),
                    "reused": optimize_counts.get("reused", 0),
                    "skipped": optimize_counts.get("skipped", 0),
                    "failed": optimize_counts.get("failed", 0),
                    "pruned": pruned,
                },
            }
        )
        write_index_state(state)
        dedupe_index()
    if progress:
        print(f"Saved {len(metadata)} images to {DATA_DIR}")
        if optimization_settings["enabled"]:
            print(
                "Optimized images: "
                f"{optimize_counts.get('optimized', 0)} new, "
                f"{optimize_counts.get('reused', 0)} reused, "
                f"{optimize_counts.get('skipped', 0)} skipped"
            )


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Build a Pictogrep CLIP index from a folder of images."
    )
    parser.add_argument("folders", nargs="*", help="one or more image folders")
    parser.add_argument("--refresh", action="store_true", help="rebuild remembered folders only when the weekly refresh is due")
    parser.add_argument("--force", action="store_true", help="with --refresh, rebuild even when the index is fresh")
    parser.add_argument("--quiet-if-fresh", action="store_true", help="print nothing when no refresh is needed")
    parser.add_argument("--no-optimize", action="store_true", help="do not create managed WebP copies before indexing")
    parser.add_argument("--optimize-max-side", type=int, help=f"largest side for managed WebP copies (default: {DEFAULT_OPTIMIZE_MAX_SIDE})")
    parser.add_argument("--optimize-min-bytes", type=int, help=f"only optimize files at or above this size (default: {DEFAULT_OPTIMIZE_MIN_BYTES})")
    parser.add_argument("--webp-quality", type=int, help=f"managed WebP quality, 1-100 (default: {DEFAULT_WEBP_QUALITY})")
    args = parser.parse_args(argv)
    optimization_settings = resolve_optimization_settings(args)
    if args.refresh:
        folders = remembered_sources()
        if not folders:
            if index_maintenance_is_due():
                dedupe_index()
            if not args.quiet_if_fresh:
                print("No remembered folders yet. Run: pictogrep index /path/to/images")
            return 0
        # Searches call this command before every launch.  Optimization setting
        # changes can require re-encoding the entire image library, so they must
        # not turn that otherwise cheap refresh check into a surprise rebuild.
        # A scheduled refresh, --force, or an explicit `pictogrep index` still
        # applies the current optimization settings.
        if not args.force and not index_is_due():
            if index_maintenance_is_due():
                result = dedupe_index()
                if not args.quiet_if_fresh and result.get("removed"):
                    print(f"Removed {result['removed']} duplicate index entries.")
            if not args.quiet_if_fresh:
                print("Index is fresh; weekly refresh is not due yet.")
            return 0
    else:
        folders = args.folders or [str(BASE / "images")]
    try:
        build_index(folders, progress=not args.refresh, optimization_settings=optimization_settings)
    except (ImportError, ModuleNotFoundError) as exc:
        print(f"Dependency error: {exc}", file=sys.stderr)
        print("Run: pictogrep setup", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
