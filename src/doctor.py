import importlib
import shutil
import sys

from pictogrep_core import COLLECTIONS_DIR, choose_viewer, index_state, index_stats


MODULES = ["numpy", "torch", "open_clip", "PIL"]


def main():
    ok = True
    print("Pictogrep doctor")
    print(f"Python: {sys.executable}")

    for module in MODULES:
        try:
            importlib.import_module(module)
            print(f"{module}: ok")
        except Exception as exc:
            print(f"{module}: missing ({exc})")
            ok = False

    stats = index_stats()
    if stats:
        print(f"index: ok ({stats['count']} images)")
        print(f"sources: {len(stats['sources'])} remembered folder(s)")
        if not stats["sources"]:
            print("  run: pictogrep index /path/to/images to enable weekly refresh")
        print("refresh: due" if stats["due"] else "refresh: weekly schedule is current")
        print("maintenance: due" if stats["maintenance_due"] else "maintenance: current")
        print(f"duplicates: {stats['duplicates']}")
        state = index_state()
        optimization = state.get("optimization", {})
        if not optimization:
            print("optimization: pending default webp refresh")
        elif optimization.get("enabled"):
            print(
                "optimization: webp "
                f"max-side={optimization.get('max_side')} "
                f"quality={optimization.get('quality')}"
            )
        else:
            print("optimization: disabled")
    else:
        print("index: missing (run: pictogrep index /path/to/images)")
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
