#!/usr/bin/env python3
"""Batch fix + check for every slide in a project — one interpreter, one bash
round trip.

Replaces the per-page loop (fix_svg.py page → check_svg.py page → repeat).
fix_svg and check_svg run in-process. Output contract (S-19): ONE JSON object
on stdout for the agent, human-readable per-page reports on stderr, exit codes
frozen (0 = OK/WARN-only, 2 = any ERROR — op_gate keys on these).

Usage:
    python3 batch_check.py <project_dir> [--config <template_config.json>] [--fix-legacy]

--fix-legacy (S-10): also fix+check legacy NN_type.svg files. fix_svg rewrites
files IN PLACE, so legacy files are never touched without this explicit opt-in
— without the flag they are only listed in the JSON as skipped_legacy.
"""
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import check_svg as check_mod  # noqa: E402  (needs the sys.path tweak above)
import fix_svg as fix_mod  # noqa: E402
from project_utils import find_page_svgs  # noqa: E402


def main():
    args = sys.argv[1:]
    if not args:
        print("Usage: python batch_check.py <project_dir> [--config <template_config.json>] [--fix-legacy]",
              file=sys.stderr)
        sys.exit(1)
    project_dir = Path(args[0])
    config_path = None
    if "--config" in args:
        idx = args.index("--config")
        if idx + 1 < len(args):
            config_path = args[idx + 1]
    fix_legacy = "--fix-legacy" in args
    if config_path is None:
        config_path = str(Path(__file__).resolve().parent.parent / "template_config.json")

    svg_dir = project_dir / "svg_output"
    new_pages = sorted(svg_dir.glob("slide_*.svg"))
    all_pages = find_page_svgs(svg_dir)
    new_set = set(new_pages)
    legacy_pages = [p for p in all_pages if p not in new_set]
    pages = list(new_pages) + (legacy_pages if fix_legacy else [])
    skipped_legacy = [p.name for p in legacy_pages if not fix_legacy]
    if not pages:
        print(json.dumps({"status": "error",
                          "message": f"no slide_*.svg found under {svg_dir}"}))
        sys.exit(1)

    config = check_mod.load_config(config_path, str(pages[0]))
    mode = config.get("mode", "fast")

    results = {}
    failed = []
    for p in pages:
        try:
            fix_mod.fix_svg(str(p), str(p))
        except Exception as exc:  # noqa: BLE001
            print(f"=== {p.name} ===", file=sys.stderr)
            print(f"  [ERROR] fix_svg crashed: {exc}", file=sys.stderr)
            results[p.name] = {"errors": [f"fix_svg crashed: {exc}"], "warnings": []}
            failed.append(p.name)
            continue
        res = check_mod.run_check(str(p), config, mode)
        print(f"=== {p.name} ===", file=sys.stderr)
        for e in res["errors"]:
            print(f"  [ERROR] {e}", file=sys.stderr)
        for w in res["warnings"]:
            print(f"  [WARN] {w}", file=sys.stderr)
        if not res["errors"] and not res["warnings"]:
            print("  [OK]", file=sys.stderr)
        results[p.name] = {"errors": res["errors"], "warnings": res["warnings"]}
        if res["exit"] != 0:
            failed.append(p.name)

    print(json.dumps({
        "status": "error" if failed else "ok",
        "pages": len(pages),
        "mode": mode,
        "failed": failed,
        "skipped_legacy": skipped_legacy,
        "results": results,
    }, ensure_ascii=False))
    sys.exit(2 if failed else 0)


if __name__ == "__main__":
    main()
