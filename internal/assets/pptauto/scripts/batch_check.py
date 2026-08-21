#!/usr/bin/env python3
"""Batch fix + check for every slide in a project — one interpreter, one bash
round trip.

Replaces the per-page loop (fix_svg.py page → check_svg.py page → repeat),
which cost two agent round trips and two interpreter startups per slide. fix_svg
and check_svg are imported in-process here; check_svg prints its usual per-page
report itself.

Usage:
    python3 batch_check.py <project_dir> [--config <template_config.json>]

Exit code 2 if ANY page has ERROR-level issues (fix those pages, re-run);
0 when all pages are OK or WARN-only. The tail summary lists the failing pages.
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import check_svg as check_mod  # noqa: E402  (needs the sys.path tweak above)
import fix_svg as fix_mod  # noqa: E402


def main():
    args = sys.argv[1:]
    if not args:
        print("Usage: python batch_check.py <project_dir> [--config <template_config.json>]")
        sys.exit(1)
    project_dir = Path(args[0])
    config_path = None
    if "--config" in args:
        idx = args.index("--config")
        if idx + 1 < len(args):
            config_path = args[idx + 1]
    if config_path is None:
        config_path = str(Path(__file__).resolve().parent.parent / "template_config.json")

    svg_dir = project_dir / "svg_output"
    pages = sorted(svg_dir.glob("slide_*.svg"))
    if not pages:
        print(f"no slide_*.svg found under {svg_dir}")
        sys.exit(1)

    config = check_mod.load_config(config_path, str(pages[0]))
    mode = config.get("mode", "fast")

    failed = []
    for p in pages:
        try:
            fix_mod.fix_svg(str(p), str(p))
        except Exception as exc:
            print(f"=== {p.name} ===")
            print(f"  [ERROR] fix_svg crashed: {exc}")
            failed.append(p.name)
            continue
        rc = check_mod.check_svg(str(p), config, mode)
        if rc != 0:
            failed.append(p.name)

    print("=== batch summary ===")
    print(f"pages: {len(pages)}, mode: {mode}")
    if failed:
        print(f"ERROR pages (must fix + re-check): {', '.join(failed)}")
        sys.exit(2)
    print("all pages OK (or WARN-only)")


if __name__ == "__main__":
    main()
