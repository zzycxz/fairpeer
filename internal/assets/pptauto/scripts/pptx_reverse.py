#!/usr/bin/env python3
"""CLI entry: convert a .pptx file to per-slide SVG (vendored from ppt-master, MIT).

Usage:
    python pptx_reverse.py <pptx_file> [-o <output_dir>] [--embed-images]
                           [--inheritance-mode {both,layered,flat}]

Reads OOXML directly and emits shape-level SVG without PowerPoint/PDF rendering.
Output: <output_dir>/svg-flat/slide_*.svg (self-contained preview slides),
svg/ (layered masters/layouts) and assets/ (extracted media).

Upstream: https://github.com/hugohe3/ppt-master (MIT, Copyright (c) 2025-2026 Hugo He),
vendored under vendor/pptmaster/ with svg_to_pptx imports renamed to
svg_to_pptx_lib to avoid colliding with this skill's own converter.
"""
from __future__ import annotations

import sys
from pathlib import Path
from xml.etree import ElementTree as ET

_here = Path(__file__).resolve().parent
sys.path.insert(0, str(_here / "vendor" / "pptmaster"))
sys.path.insert(0, str(_here))

from console_encoding import configure_utf8_stdio  # noqa: E402
configure_utf8_stdio()

from pptx_to_svg import convert_pptx_to_svg  # noqa: E402
from pptx_to_svg.converter import ConvertOptions  # noqa: E402


def main() -> int:
    import argparse
    ap = argparse.ArgumentParser(description="Convert PPTX to per-slide SVG (OOXML direct).")
    ap.add_argument("pptx_file")
    ap.add_argument("-o", "--output", default=None)
    ap.add_argument("--media-subdir", default="assets")
    ap.add_argument("--embed-images", action="store_true")
    ap.add_argument("--keep-hidden", action="store_true")
    ap.add_argument("--inheritance-mode", choices=("both", "layered", "flat"), default="both")
    args = ap.parse_args()

    pptx = Path(args.pptx_file).expanduser().resolve()
    if not pptx.exists():
        print("Error: file does not exist: %s" % pptx, file=sys.stderr)
        return 1
    out = Path(args.output).expanduser().resolve() if args.output else pptx.with_name(pptx.stem + "_pptx_to_svg")
    options = ConvertOptions(media_subdir=args.media_subdir, embed_images=args.embed_images,
                              keep_hidden=args.keep_hidden, inheritance_mode=args.inheritance_mode)
    try:
        result = convert_pptx_to_svg(pptx, out, options)
    except Exception as exc:  # noqa: BLE001 — surface a clean error, keep exit 1
        print("Error: PPTX-to-SVG conversion failed: %s" % exc, file=sys.stderr)
        return 1

    slides = getattr(result, "flat_slides", None) or getattr(result, "slides", [])
    placeholders = 0
    for a in slides:
        try:
            root = ET.fromstring(a.svg)
            placeholders += sum(1 for e in root.iter()
                                if (e.get("data-pptx-fallback-kind") or e.get("data-pptx-visual-status")) == "placeholder")
        except ET.ParseError:
            pass
    print("slides=%d placeholders=%d out=%s" % (len(slides), placeholders, out))
    return 0


if __name__ == "__main__":
    sys.exit(main())
