#!/usr/bin/env python3
"""Build common page types from a compact page-spec JSON — the model writes
~300 tokens of JSON per page instead of ~5K tokens of SVG.

Usage:
    python build_page_skeleton.py <pages.json> --project <project_dir> [--config <template_config.json>]

pages.json shape:
    {"pages": [
      {"type": "cover",   "title": "...", "subtitle": "...", "footer": "2026-08"},
      {"type": "toc",     "title": "目录", "items": ["章节一", "章节二", ...]},
      {"type": "section", "title": "章节标题", "number": "02", "lead": "可选导语"},
      {"type": "cards",   "title": "页标题", "lead": "可选导语",
       "items": [{"icon": "tabler-outline/server", "head": "卡片标题",
                  "lines": ["要点一", "要点二"]}, ...]},          # 2-4 张卡
      {"type": "columns", "title": "页标题",
       "columns": [{"icon": "...", "head": "栏标题", "lines": [...]}, {...}]},
      {"type": "bullets", "title": "页标题", "lead": "可选导语", "items": ["要点", ...]},
      {"type": "ending",  "title": "谢谢观看", "footer": "联系方式"},
      {"out": "slide_07.svg", "fonts": {"title": 26, "card_title": 18, "body": 14}}   # 可选覆盖
    ]}

Mechanics: colors/fonts/background rules come from template_config.json (the
same source check_svg enforces) — has_template pages draw NO full-screen
background (the PPTX master shows through), cards use the config's semi-
transparent card_bg, icons are <use data-icon> placeholders (embed via
svg_finalize/embed_icons.py afterwards). Text is CJK-aware wrapped; lines that
cannot fit a card are dropped and reported in the JSON summary — fix the spec,
not the SVG. Generated pages still go through batch_check; the model may
edit_file tweaks on top.
"""

from __future__ import annotations

import argparse
import json
import os
import sys

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

CANVAS_W, CANVAS_H = 1280, 720
MARGIN_X = 50
TITLE_Y = 56
CONTENT_TOP = 180

PAGE_TYPES = ("cover", "toc", "section", "cards", "columns", "bullets", "ending")


def cjk_w(ch):
    return 1.0 if ord(ch) > 0x2E80 else 0.55


def text_width(s, fs):
    return sum(cjk_w(c) for c in s) * fs


def wrap_text(s, fs, max_units):
    """Greedy wrap, CJK-aware. Returns lines each ≤ max_units wide."""
    lines, cur, cur_w = [], "", 0.0
    for ch in s:
        w = cjk_w(ch)
        if cur_w + w > max_units and cur:
            lines.append(cur.rstrip())
            cur, cur_w = "", 0.0
            if ch == " ":
                continue
        cur += ch
        cur_w += w
    if cur:
        lines.append(cur)
    return lines or [""]


def esc(s):
    return (str(s).replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
            .replace('"', "&quot;"))


def load_style(config_path):
    cfg = {}
    for cand in (config_path,
                 os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
                              "template_config.json")):
        try:
            with open(cand, "r", encoding="utf-8") as f:
                cfg = json.load(f)
            break
        except (OSError, ValueError, TypeError):
            pass
    colors = cfg.get("colors") or {}
    fonts = cfg.get("fonts") or {}
    return {
        "background": colors.get("background") or "#FFFFFF",
        "background_type": colors.get("background_type") or "",
        "accent": colors.get("accent") or "#4472C4",
        "text": colors.get("text") or "#1A1A1A",
        "muted": colors.get("text_secondary") or colors.get("muted") or "#666666",
        "line": colors.get("line") or "rgba(0,0,0,0.15)",
        "card_bg": colors.get("card_bg") or "rgba(255,255,255,0.75)",
        "font": fonts.get("family") or '"Microsoft YaHei", sans-serif',
        "has_template": colors.get("background_type") in ("image", "solid"),
    }


class Page:
    def __init__(self, style, fonts_override):
        self.s = style
        f = {"title": 26, "card_title": 18, "body": 14}
        f.update(fonts_override or {})
        self.f = f
        self.el = []
        self.dropped = 0

    def text(self, x, y, content, size, color, anchor="start", weight=""):
        w = ' font-weight="%s"' % weight if weight else ""
        self.el.append(
            '<text x="%d" y="%d" font-family="%s" font-size="%d" fill="%s" '
            'text-anchor="%s"%s>%s</text>'
            % (x, y, esc(self.s["font"]), size, color, anchor, w, esc(content)))

    def icon(self, lib_name, x, y, size, color):
        if not lib_name:
            return
        name = lib_name if "/" in lib_name else "tabler-outline/" + lib_name
        self.el.append(
            '<use data-icon="%s" x="%d" y="%d" width="%d" height="%d" fill="%s"/>'
            % (esc(name), x, y, size, size, color))

    def card(self, x, y, w, h, rx=10):
        self.el.append(
            '<rect x="%d" y="%d" width="%d" height="%d" rx="%d" fill="%s" '
            'stroke="%s" stroke-width="1"/>'
            % (x, y, w, h, rx, self.s["card_bg"], self.s["accent"]))

    def title_band(self, title, lead=""):
        self.el.append('<rect x="%d" y="%d" width="5" height="30" fill="%s"/>'
                       % (MARGIN_X, TITLE_Y - 24, self.s["accent"]))
        self.text(MARGIN_X + 16, TITLE_Y, title, self.f["title"], self.s["text"], weight="bold")
        if lead:
            # baseline 96 lands inside check_svg's content area (y≥90) so pages
            # with a lead always cover the top density zone
            for i, ln in enumerate(wrap_text(lead, 15, (CANVAS_W - 2 * MARGIN_X - 16) / 15.0)):
                self.text(MARGIN_X + 16, 96 + i * 22, ln, 15, self.s["muted"])

    def background(self):
        if not self.s["has_template"]:
            self.el.insert(0, '<rect width="%d" height="%d" fill="%s"/>'
                           % (CANVAS_W, CANVAS_H, self.s["background"]))

    def svg(self):
        self.background()
        return ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d">\n'
                % (CANVAS_W, CANVAS_H)) + "\n".join(self.el) + "\n</svg>\n"


def build_cover(p, spec):
    p.text(640, 300, spec.get("title", ""), 42, p.s["text"], "middle", "bold")
    p.el.append('<rect x="590" y="330" width="100" height="3" fill="%s"/>' % p.s["accent"])
    if spec.get("subtitle"):
        p.text(640, 372, spec["subtitle"], 20, p.s["muted"], "middle")
    if spec.get("footer"):
        p.text(640, 645, spec["footer"], 14, p.s["muted"], "middle")


def build_toc(p, spec):
    p.title_band(spec.get("title", "目录"))
    items = spec.get("items") or []
    top, bottom = CONTENT_TOP - 10, 610
    step = min(80, (bottom - top) / max(len(items), 1))
    fs = 18 if step >= 46 else 16
    for i, it in enumerate(items):
        y = top + i * step + step / 2 + 6
        p.text(MARGIN_X + 8, y, "%02d" % (i + 1), fs + 2, p.s["accent"], weight="bold")
        p.text(MARGIN_X + 62, y, str(it), fs, p.s["text"])
        p.el.append('<line x1="%d" y1="%.0f" x2="%d" y2="%.0f" stroke="%s" stroke-width="1"/>'
                    % (MARGIN_X + 62, y + 14, CANVAS_W - MARGIN_X, y + 14, p.s["line"]))


def build_section(p, spec):
    if spec.get("number"):
        p.text(640, 210, spec["number"], 110, p.s["accent"], "middle", "bold")
    p.text(640, 400, spec.get("title", ""), 34, p.s["text"], "middle", "bold")
    if spec.get("lead"):
        p.text(640, 452, spec["lead"], 16, p.s["muted"], "middle")
    p.el.append('<rect x="540" y="592" width="200" height="3" fill="%s"/>' % p.s["accent"])


def build_cards(p, spec):
    p.title_band(spec.get("title", ""), spec.get("lead", ""))
    items = spec.get("items") or []
    k = max(2, min(4, len(items))) if items else 2
    gap = 24
    card_w = (CANVAS_W - 2 * MARGIN_X - (k - 1) * gap) / k
    top, bottom = 150, CANVAS_H - 50
    for i in range(k):
        x = MARGIN_X + i * (card_w + gap)
        p.card(x, top, card_w, bottom - top)
        # thin accent underline near the card foot: ties the card visually and
        # puts an element in the bottom density zone (config wants all 4 zones)
        p.el.append('<rect x="%d" y="598" width="%d" height="2" fill="%s"/>'
                    % (x + 20, card_w - 40, p.s["accent"]))
        it = items[i] if i < len(items) else {}
        inner = x + 22
        if it.get("icon"):
            p.icon(it["icon"], inner, top + 24, 40, p.s["accent"])
            y = top + 70
        else:
            y = top + 52
        p.text(inner, y, it.get("head", ""), p.f["card_title"], p.s["text"], weight="bold")
        y += 30
        max_units = (card_w - 44) / float(p.f["body"])
        room = (bottom - 24 - y) / 22
        lines = []
        for raw in it.get("lines") or []:
            for ln in wrap_text(str(raw), p.f["body"], max_units):
                lines.append(ln)
        if len(lines) > room:
            p.dropped += len(lines) - int(room)
            lines = lines[: int(room)]
        # adaptive spacing: few lines spread deeper into the card (validate-mode
        # vertical-coverage wants content across the whole canvas)
        step = max(22, min(44, (bottom - 30 - y) / max(len(lines), 1)))
        for ln in lines:
            p.text(inner, y, ln, p.f["body"], p.s["muted"])
            y += step


def build_columns(p, spec):
    p.title_band(spec.get("title", ""), spec.get("lead", ""))
    cols = (spec.get("columns") or [])[:2] or [{}, {}]
    gap = 24
    w = (CANVAS_W - 2 * MARGIN_X - gap) / 2
    top, bottom = 150, CANVAS_H - 50
    for i, col in enumerate(cols):
        x = MARGIN_X + i * (w + gap)
        p.card(x, top, w, bottom - top)
        p.el.append('<rect x="%d" y="598" width="%d" height="2" fill="%s"/>'
                    % (x + 24, w - 48, p.s["accent"]))
        inner = x + 24
        if col.get("icon"):
            p.icon(col["icon"], inner, top + 22, 36, p.s["accent"])
            y = top + 66
        else:
            y = top + 50
        p.text(inner, y, col.get("head", ""), p.f["card_title"] + 2, p.s["text"], weight="bold")
        y += 32
        max_units = (w - 60) / float(p.f["body"] + 1)
        room = (bottom - 24 - y) / 24
        lines = []
        for raw in col.get("lines") or []:
            for ln in wrap_text("• " + str(raw), p.f["body"] + 1, max_units):
                lines.append(ln)
        if len(lines) > room:
            p.dropped += len(lines) - int(room)
            lines = lines[: int(room)]
        step = max(24, min(44, (bottom - 30 - y) / max(len(lines), 1)))
        for ln in lines:
            p.text(inner, y, ln, p.f["body"] + 1, p.s["muted"])
            y += step


def build_bullets(p, spec):
    p.title_band(spec.get("title", ""), spec.get("lead", ""))
    items = spec.get("items") or []
    fs = p.f["body"] + 2
    base_step = fs + 14
    # pre-wrap to know total height, then stretch inter-item gaps so few items
    # still spread down the page (vertical coverage)
    max_units = (CANVAS_W - 2 * MARGIN_X - 40) / float(fs)
    wrapped = [wrap_text(str(it), fs, max_units) for it in items]
    total_h = sum(len(w) * base_step for w in wrapped) + 6 * (len(wrapped) - 1 if wrapped else 0)
    slack = max(0.0, (610 - 186) - total_h)
    extra = slack / max(len(wrapped), 1) if wrapped else 0
    y = 186
    for lines in wrapped:
        p.el.append('<circle cx="%d" cy="%.0f" r="3.5" fill="%s"/>'
                    % (MARGIN_X + 8, y - 5, p.s["accent"]))
        for ln in lines:
            p.text(MARGIN_X + 26, y, ln, fs, p.s["text"])
            y += base_step
        y += 6 + min(extra, 90)
        if y > CANVAS_H - 40:
            p.dropped += len(wrapped) - (wrapped.index(lines) + 1)
            break


def build_ending(p, spec):
    p.text(640, 340, spec.get("title", "谢谢观看"), 38, p.s["text"], "middle", "bold")
    if spec.get("subtitle"):
        p.text(640, 382, spec["subtitle"], 18, p.s["muted"], "middle")
    if spec.get("footer"):
        p.text(640, 645 if spec.get("subtitle") else 390, spec["footer"], 15, p.s["muted"], "middle")


BUILDERS = {
    "cover": build_cover, "toc": build_toc, "section": build_section,
    "cards": build_cards, "columns": build_columns,
    "bullets": build_bullets, "ending": build_ending,
}


def main():
    ap = argparse.ArgumentParser(description="Build common page types from a compact page-spec JSON.")
    ap.add_argument("pages_json", help="pages.json file (or '-' for stdin)")
    ap.add_argument("--project", required=True, help="ppt-auto project dir (writes svg_output/)")
    ap.add_argument("--config", default=None, help="template_config.json (default: skill dir)")
    args = ap.parse_args()

    raw = sys.stdin.read() if args.pages_json == "-" else open(args.pages_json, "r", encoding="utf-8").read()
    doc = json.loads(raw)
    specs = doc["pages"] if isinstance(doc, dict) else doc
    style = load_style(args.config)

    svg_dir = os.path.join(args.project, "svg_output")
    os.makedirs(svg_dir, exist_ok=True)

    out_pages = []
    for i, spec in enumerate(specs, start=1):
        ptype = spec.get("type", "")
        if ptype not in BUILDERS:
            out_pages.append({"index": i, "type": ptype, "error": "unknown type (skipped)"})
            continue
        p = Page(style, spec.get("fonts"))
        BUILDERS[ptype](p, spec)
        # Default name carries the type suffix: keeps slide_* sort order (and
        # svg_to_pptx/QA pairing) while check_svg's filename heuristics exempt
        # cover/ending pages from density checks, as they do for hand-drawn ones.
        name = spec.get("out") or "slide_%02d_%s.svg" % (i, ptype)
        path = os.path.join(svg_dir, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(p.svg())
        entry = {"index": i, "type": ptype, "out": name}
        n_texts = sum(1 for e in p.el if e.startswith("<text"))
        if ptype in ("cover", "ending") and n_texts < 3:
            entry["warning"] = "sparse page has <3 texts (checker floor even for cover/ending) — add subtitle/footer"
        if p.dropped:
            entry["lines_dropped"] = p.dropped
            entry["warning"] = "spec too long for the layout — trim lines/items and regenerate"
        out_pages.append(entry)

    print(json.dumps({"pages": out_pages, "svg_dir": svg_dir,
                      "has_template": style["has_template"]}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
