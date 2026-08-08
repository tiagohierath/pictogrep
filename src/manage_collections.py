import argparse
from pathlib import Path
import re
import sys

from pictogrep_core import COLLECTIONS_DIR, IMAGE_EXTENSIONS, available_index, collection_images, find_milklily_project, search


def collection_name(name):
    clean = re.sub(r"[^a-z0-9]+", "-", name.lower()).strip("-")
    if not clean:
        raise ValueError("tag name must contain letters or numbers")
    return clean


def collection_path(name):
    return COLLECTIONS_DIR / collection_name(name)


def create_collection(name):
    path = collection_path(name)
    path.mkdir(parents=True, exist_ok=True)
    return path


def link_image(folder, source):
    source = Path(source).expanduser().resolve()
    if not source.is_file() or source.suffix.lower() not in IMAGE_EXTENSIONS:
        raise ValueError(f"not a supported image: {source}")
    for existing in folder.iterdir():
        if existing.is_symlink() and existing.resolve() == source:
            return False
    target = folder / source.name
    stem, suffix = source.stem, source.suffix
    number = 2
    while target.exists() or target.is_symlink():
        target = folder / f"{stem}-{number}{suffix}"
        number += 1
    target.symlink_to(source)
    return True


def command_create(args):
    print(create_collection(args.name))
    return 0


def command_list(args):
    if not COLLECTIONS_DIR.exists():
        return 0
    for folder in sorted(path for path in COLLECTIONS_DIR.iterdir() if path.is_dir()):
        count = sum(1 for path in folder.iterdir() if path.is_file() or path.is_symlink())
        print(f"{folder.name}\t{count} images\t{folder}")
    return 0


def command_add(args):
    folder = create_collection(args.name)
    added = 0
    for image in args.images:
        added += link_image(folder, image)
    print(f"{folder}: added {added} image(s)")
    return 0


def command_fill(args):
    if not available_index():
        print("No index found. Run: pictogrep index /path/to/images", file=sys.stderr)
        return 1
    folder = create_collection(args.name)
    results = search(" ".join(args.query), limit=args.limit)
    added = sum(link_image(folder, result["path"]) for result in results)
    print(f"{folder}: added {added} of {len(results)} CLIP matches")
    return 0


def command_send(args):
    project = find_milklily_project()
    if not project:
        print("Not inside a Milklily project (no milklily.conf found).", file=sys.stderr)
        return 2
    tag = collection_name(args.tag)
    sources = collection_images(tag)
    destination = project / "refs" / "visual" / "pictogrep" / tag
    destination.mkdir(parents=True, exist_ok=True)
    added = sum(link_image(destination, source) for source in sources)
    print(f"{destination}: linked {added} of {len(sources)} tagged image(s)")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="Create editable Pictogrep image tags backed by symlink folders.")
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create", help="create an empty editable image tag")
    create.add_argument("name")
    create.set_defaults(func=command_create)
    listing = commands.add_parser("list", help="list image tags")
    listing.set_defaults(func=command_list)
    add = commands.add_parser("add", help="tag images with symlinks; originals stay in place")
    add.add_argument("name")
    add.add_argument("images", nargs="+")
    add.set_defaults(func=command_add)
    fill = commands.add_parser("fill", help="use CLIP search to tag matching images")
    fill.add_argument("name")
    fill.add_argument("query", nargs="+")
    fill.add_argument("--limit", type=int, default=30)
    fill.set_defaults(func=command_fill)
    send = commands.add_parser("send", help="link one tag's images into the current Milklily project")
    send.add_argument("tag")
    send.set_defaults(func=command_send)
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except ValueError as exc:
        print(exc, file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
