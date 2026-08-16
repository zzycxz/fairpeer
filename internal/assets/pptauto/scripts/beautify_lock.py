#!/usr/bin/env python3
"""Beautify content lock: extract per-page text from SVGs, then mechanically
verify a redesigned deck lost nothing.

Usage:
    # 1) extract the content lock from the REVERSED original slides:
    python beautify_lock.py extract <reverse_svgflat_dir> --out content_lock.json
    # 2) after redesign, check the NEW slides against the lock:
    python beautify_lock.py check <new_svg_dir> --lock content_lock.json

WHY: Beautify's contract is "keep wording, page count, page order 1:1 — only
the layout changes". Relying on the LLM's discipline for that contract failed
everywhere else in this pipeline (colors, tables, steps); this script is the
mechanical guarantee. The skill MUST run `check` and pass before export.

Matching model: text is compared whitespace-normalized (CJK text is often
re-flowed and re-split across elements during redesign, so per-element equality
is too strict). Each ORIGINAL text unit must appear as a substring of the new
page's concatenated text (both stripped of all whitespace). Added text is
reported separately (allowed but visible — additions like new labels should be
deliberate). Images are counted per page as a soft signal (not enforced).

Exit codes: extract always 0; check exits 2 when any page is missing text
(the skill treats this as a must-fix), 0 otherwise. Report is JSON on stdout.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from pathlib import Path
from xml.etree import ElementTree as ET

try:
    from console_encoding import configure_utf8_stdio
except ImportError:
    _here = os.path.dirname(os.path.abspath(__file__))
    if _here not in sys.path:
        sys.path.insert(0, _here)
    try:
        from console_encoding import configure_utf8_stdio
    except ImportError:
        configure_utf8_stdio = None

if configure_utf8_stdio is not None:
    configure_utf8_stdio()

SVG_TEXT_NS = "{http://www.w3.org/2000/svg}text"
SVG_IMAGE = "{http://www.w3.org/2000/svg}image"


def _hidden(el, parent):
    """True when el or any ancestor carries visibility=hidden / display=none —
    reversed slides duplicate text into hidden geometry copies; counting those
    lets a redesign 'pass' by keeping the hidden twin of a dropped visible text."""
    seen = set()
    while el is not None and id(el) not in seen:
        seen.add(id(el))
        if (el.get("visibility") == "hidden" or el.get("display") == "none"):
            return True
        el = parent.get(el)
    return False


def page_units(svg_path: Path):
    """All visible text units + image count from one SVG (tspans concatenated)."""
    try:
        root = ET.parse(svg_path).getroot()
    except ET.ParseError:
        return [], 0
    parent = {c: p for p in root.iter() for c in p}
    texts, images = [], 0
    for el in root.iter():
        if el.tag == SVG_TEXT_NS:
            if _hidden(el, parent):
                continue
            parts = [el.text or ""] + [(t.text or "") for t in el.iter("{http://www.w3.org/2000/svg}tspan")]
            unit = "".join(parts)
            unit = re.sub(r"\s+", "", unit)
            if unit:
                texts.append(unit)
        elif el.tag == SVG_IMAGE:
            images += 1
    return texts, images


def slide_number(name: str):
    m = re.search(r"(\d+)", Path(name).stem)
    return int(m.group(1)) if m else 0


def collect(svg_dir: Path):
    pages = {}
    for f in sorted(svg_dir.glob("*.svg")):
        n = slide_number(f.name)
        if n:
            units, images = page_units(f)
            pages[n] = {"units": units, "images": images,
                        "joined": "".join(units)}
    return pages


def cmd_extract(args):
    pages = collect(Path(args.svg_dir))
    lock = {"pages": {str(n): {"units": p["units"], "images": p["images"]}
                      for n, p in sorted(pages.items())}}
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(lock, ensure_ascii=False, indent=1), encoding="utf-8")
    total = sum(len(p["units"]) for p in pages.values())
    print(json.dumps({"lock": str(out), "pages": len(pages), "text_units": total}))
    return 0


def cmd_check(args):
    lock = json.loads(Path(args.lock).read_text(encoding="utf-8"))
    lock_pages = {int(k): v for k, v in lock.get("pages", {}).items()}
    new_pages = collect(Path(args.svg_dir))
    report, bad = [], 0
    for n, orig in sorted(lock_pages.items()):
        new = new_pages.get(n)
        if new is None:
            report.append({"page": n, "ok": False, "missing": ["<整页缺失>"]})
            bad += 1
            continue
        joined = new["joined"]
        # Multiplicity-aware matching: pages legitimately repeat identical units
        # (e.g. two "100%" cells); dropping one copy must NOT pass silently.
        # Exact multiset deficit first, then substring/occurrence fallback so
        # re-split text (one box -> several elements) still satisfies the lock.
        from collections import Counter
        oc, nc = Counter(orig["units"]), Counter(new["units"])
        missing = []
        for u, c_o in oc.items():
            deficit = c_o - nc.get(u, 0)
            if deficit <= 0:
                continue
            # Substring tolerance only for units long enough to be distinctive
            # (>=3 chars): a 1-2 char unit like a page number "5" occurs inside
            # dates/percentages and would be "satisfied" by unrelated text.
            if len(u) >= 3:
                occ = joined.count(u)  # non-overlapping occurrences incl. re-split
                if occ >= c_o:
                    continue
                deficit = min(deficit, c_o - occ)
            missing.extend([u] * deficit)
        # additions: new units not covered by any original unit (substring the
        # other way is noisy when text was re-split; use unit-level containment)
        orig_joined = "".join(orig["units"])
        added = [u for u in new["units"] if u not in orig_joined]
        ok = not missing
        if not ok:
            bad += 1
        report.append({"page": n, "ok": ok,
                       "missing": [m[:60] for m in missing[:10]],
                       "added": [a[:60] for a in added[:10]],
                       "images_orig": orig["images"], "images_new": new["images"]})
    extra_pages = sorted(set(new_pages) - set(lock_pages))
    summary = {"ok": bad == 0 and not extra_pages, "pages_checked": len(lock_pages),
               "pages_missing_text": bad, "extra_pages": extra_pages, "pages": report}
    print(json.dumps(summary, ensure_ascii=False, indent=1))
    return 0 if summary["ok"] else 2


def main():
    ap = argparse.ArgumentParser(description="Beautify content lock (extract/check).")
    sub = ap.add_subparsers(dest="cmd", required=True)
    e = sub.add_parser("extract", help="build content_lock.json from reversed original slides")
    e.add_argument("svg_dir", help="svg-flat/ directory from pptx_reverse.py")
    e.add_argument("--out", default="content_lock.json")
    c = sub.add_parser("check", help="verify redesigned slides against the lock")
    c.add_argument("svg_dir", help="new svg_output/ directory")
    c.add_argument("--lock", default="content_lock.json")
    args = ap.parse_args()
    return cmd_extract(args) if args.cmd == "extract" else cmd_check(args)


if __name__ == "__main__":
    sys.exit(main())
