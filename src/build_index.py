import argparse
import json
from pathlib import Path
import sys
import time

from pictogrep_core import (
    BASE,
    COLLECTIONS_DIR,
    DATA_DIR,
    INDEX_STATE_PATH,
    MODEL_NAME,
    PRETRAINED,
    image_files,
    index_is_due,
    remembered_sources,
)


def build_index(folders, remember=True, progress=True):
    import numpy as np
    import open_clip
    import torch
    from PIL import Image

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

    model, _, preprocess = open_clip.create_model_and_transforms(
        MODEL_NAME,
        pretrained=PRETRAINED,
    )
    model.eval()

    if progress:
        print(f"Found {len(files)} images in {len(scan_roots)} folder(s)")
    embeddings = []
    metadata = []

    for i, file in enumerate(files, start=1):
        try:
            image = preprocess(Image.open(file).convert("RGB")).unsqueeze(0)
            with torch.no_grad():
                vector = model.encode_image(image)
            vector /= vector.norm(dim=-1, keepdim=True)
            embeddings.append(vector.cpu().numpy()[0])
            metadata.append(str(file))
            if progress:
                print(f"{i}/{len(files)} {file}")
        except Exception as exc:
            if progress:
                print(f"Failed: {file} {exc}")

    DATA_DIR.mkdir(exist_ok=True)
    np.save(DATA_DIR / "embeddings.npy", np.array(embeddings))
    with (DATA_DIR / "metadata.json").open("w") as fh:
        json.dump(metadata, fh, indent=2)
    if remember:
        with INDEX_STATE_PATH.open("w") as fh:
            json.dump({"indexed_at": time.time(), "sources": [str(root) for root in roots]}, fh, indent=2)
    if progress:
        print(f"Saved {len(metadata)} images to {DATA_DIR}")


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Build a Pictogrep CLIP index from a folder of images."
    )
    parser.add_argument("folders", nargs="*", help="one or more image folders")
    parser.add_argument("--refresh", action="store_true", help="rebuild remembered folders only when the weekly refresh is due")
    parser.add_argument("--force", action="store_true", help="with --refresh, rebuild even when the index is fresh")
    parser.add_argument("--quiet-if-fresh", action="store_true", help="print nothing when no refresh is needed")
    args = parser.parse_args(argv)
    if args.refresh:
        folders = remembered_sources()
        if not folders:
            if not args.quiet_if_fresh:
                print("No remembered folders yet. Run: pictogrep index /path/to/images")
            return 0
        if not args.force and not index_is_due():
            if not args.quiet_if_fresh:
                print("Index is fresh; weekly refresh is not due yet.")
            return 0
    else:
        folders = args.folders or [str(BASE / "images")]
    try:
        build_index(folders, progress=not args.refresh)
    except (ImportError, ModuleNotFoundError) as exc:
        print(f"Dependency error: {exc}", file=sys.stderr)
        print("Run: pictogrep setup", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
