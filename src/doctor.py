import importlib.util
import shutil
import sys

from bildkasten_core import COLLECTIONS_DIR, choose_viewer, index_stats


MODULES = ["numpy", "torch", "open_clip", "PIL"]


def main():
    ok = True
    print("Bildkasten doctor")
    print(f"Python: {sys.executable}")

    for module in MODULES:
        found = importlib.util.find_spec(module) is not None
        print(f"{module}: {'ok' if found else 'missing'}")
        ok = ok and found

    stats = index_stats()
    if stats:
        print(f"index: ok ({stats['count']} images)")
        print(f"sources: {len(stats['sources'])} remembered folder(s)")
        if not stats["sources"]:
            print("  run: bildkasten index /path/to/images to enable weekly refresh")
        print("refresh: due" if stats["due"] else "refresh: weekly schedule is current")
    else:
        print("index: missing (run: bildkasten index /path/to/images)")
        ok = False

    try:
        print("viewer:", " ".join(choose_viewer()))
    except Exception as exc:
        print(f"viewer: missing ({exc})")
        ok = False

    print("mpv:", "ok" if shutil.which("mpv") else "not found")
    collections = len([path for path in COLLECTIONS_DIR.iterdir() if path.is_dir()]) if COLLECTIONS_DIR.exists() else 0
    print(f"collections: {collections}")
    return 0 if ok else 1


if __name__ == "__main__":
    raise SystemExit(main())
