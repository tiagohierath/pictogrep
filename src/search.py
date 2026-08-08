import argparse
import sys

from pictogrep_core import open_files, search


def main(argv=None):
    parser = argparse.ArgumentParser(
        description="Search a Pictogrep image index with natural language."
    )
    parser.add_argument("query", nargs="*", help="visual search text")
    parser.add_argument("-n", "--limit", type=int, default=50, help="number of results")
    parser.add_argument("--print", action="store_true", help="print results instead of opening them")
    parser.add_argument("--no-open", action="store_true", help="search without opening a viewer")
    args = parser.parse_args(argv)

    query = " ".join(args.query).strip()
    if not query:
        parser.print_help()
        return 2

    try:
        results = search(query, limit=args.limit)
    except FileNotFoundError as exc:
        print(exc, file=sys.stderr)
        return 1
    except (ImportError, ModuleNotFoundError) as exc:
        print(f"Dependency error: {exc}", file=sys.stderr)
        print("Run: pictogrep setup", file=sys.stderr)
        return 1

    for item in results:
        print(f"{item['score']:.3f}\t{item['path']}")
    sys.stdout.flush()

    if not args.print and not args.no_open:
        open_files([item["path"] for item in results])
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
