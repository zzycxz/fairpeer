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
      {"type": "kpi_row", "title": "指标页",
       "items": [{"num": "98.7%", "label": "客户满意度", "delta": "+2.1%"}, ...]},
      {"type": "composite", "title": "建设成效", "lead": "可选导语",
       "blocks": [
         {"region": [50, 104, 1180, 130], "type": "kpi_row",
          "items": [{"num": "98.7%", "label": "核心指标"}]},
         {"region": [50, 254, 760, 416], "type": "panel",
          "head": "重点工作", "lines": ["...", "..."]},
         {"region": [830, 254, 400, 416], "type": "bullets",
          "items": ["要点一", "要点二"]}]},
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

Layout model (2026-09 rework): every content builder renders inside a content
box (x, y, w, h) at TRUE font sizes — full-page types get the whole canvas
content area, composite blocks get their `region` rectangle. No linear
transform: a half-height region gets a compact native layout, not a squashed
full-page layout. Color slots: `brand` for structure (title band, strokes,
table headers, KPI numbers), `accent` for emphasis (deltas, warnings).
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

try:
    from text_utils import cjk_char_units as cjk_w, estimate_text_width as text_width
except ImportError:
    def cjk_w(ch):
        return 1.0 if ord(ch) > 0x2E80 else 0.55

    def text_width(s, fs):
        return sum(cjk_w(c) for c in s) * fs

CANVAS_W, CANVAS_H = 1280, 720
MARGIN_X = 50
TITLE_Y = 46          # slim chrome: title baseline (was 56)
LEAD_Y = 92           # lead baseline — stays inside check_svg's content area (y≥90)
CONTENT_TOP = 104     # content starts higher than the old 150/180
CONTENT_BOTTOM = CANVAS_H - 50

PAGE_TYPES = ("cover", "toc", "section", "cards", "columns", "bullets",
              "kpi_row", "panel", "composite", "ending")
# Types renderable as a composite block (content-only, no page title band).
BLOCK_TYPES = ("cards", "columns", "bullets", "kpi_row", "panel")


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
    font_sizes = cfg.get("font_sizes") or {}
    accent = colors.get("accent") or "#4472C4"
    return {
        "background": colors.get("background") or "#FFFFFF",
        "background_type": colors.get("background_type") or "",
        "brand": colors.get("brand") or accent,
        "accent": accent,
        "text": colors.get("text") or "#1A1A1A",
        "muted": colors.get("text_secondary") or colors.get("muted") or "#666666",
        "line": colors.get("line") or "rgba(0,0,0,0.15)",
        "card_bg": colors.get("card_bg") or "rgba(255,255,255,0.75)",
        "font": fonts.get("family") or '"Microsoft YaHei", sans-serif',
        "has_template": colors.get("background_type") in ("image", "solid"),
        # D-02: 结构化默认字号——替代 Page 里的硬编码；autofit 在其上覆盖
        "font_sizes": {k: font_sizes.get(k) for k in ("title", "card_title", "body")
                       if isinstance(font_sizes.get(k), (int, float))},
    }


class Page:
    def __init__(self, style, fonts_override, autofit=None):
        self.s = style
        # 字号优先级（低→高）：内置兜底 < config font_sizes（D-02）
        # < --autofit 结果（S-01）< pages.json 每页 fonts
        f = {"title": 26, "card_title": 18, "body": 14}
        f.update(style.get("font_sizes") or {})
        if autofit:
            f.update({k: autofit[k] for k in f if isinstance(autofit.get(k), (int, float))})
        f.update(fonts_override or {})
        self.f = f
        self.el = []
        self.dropped = 0
        self.meta = {}   # D-03: 嵌入 <metadata><ppt-auto .../></metadata>
        self.region = None  # 块页（整文件即一个块）：[x, y, w, h]
        self.block_reports = []  # composite 各块的生成摘要

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
            % (x, y, w, h, rx, self.s["card_bg"], self.s["brand"]))

    def title_band(self, title, lead=""):
        """Page-level slim chrome: brand bar + title at y=46, optional lead at
        y=92 (inside the checker's content area). Returns the lead line count
        so content builders can push their top below a multi-line lead."""
        self.el.append('<rect x="%d" y="%d" width="5" height="26" fill="%s"/>'
                       % (MARGIN_X, TITLE_Y - 21, self.s["brand"]))
        self.text(MARGIN_X + 16, TITLE_Y, title, self.f["title"], self.s["text"], weight="bold")
        if not lead:
            return 0
        max_units = (CANVAS_W - 2 * MARGIN_X - 16) / 15.0
        lines = wrap_text(lead, 15, max_units)
        for i, ln in enumerate(lines):
            self.text(MARGIN_X + 16, LEAD_Y + i * 22, ln, 15, self.s["muted"])
        return len(lines)

    def background(self):
        if not self.s["has_template"]:
            self.el.insert(0, '<rect width="%d" height="%d" fill="%s"/>'
                           % (CANVAS_W, CANVAS_H, self.s["background"]))

    def svg(self):
        head = ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d">\n'
                % (CANVAS_W, CANVAS_H))
        meta = ""
        if self.meta:  # D-03: check_svg 与后续工具优先读它判断页型
            attrs = " ".join('%s="%s"' % (k.replace("_", "-"), esc(str(v)))
                             for k, v in self.meta.items())
            meta = "<metadata><ppt-auto %s/></metadata>\n" % attrs
        if self.region:
            # 块页：内容已按 region 的绝对坐标排版（真实字号，无缩放变换），
            # 不画背景——整页背景由拼装方统一处理。
            return head + meta + "\n".join(self.el) + "\n</svg>\n"
        self.background()
        return head + meta + "\n".join(self.el) + "\n</svg>\n"


def default_box(lead_lines=0):
    """Full-page content box below the (slim) title chrome."""
    top = CONTENT_TOP if lead_lines <= 1 else LEAD_Y + (lead_lines - 1) * 22 + 26
    return (MARGIN_X, top, CANVAS_W - 2 * MARGIN_X, CONTENT_BOTTOM - top)


def build_cover(p, spec, box):
    p.text(640, 300, spec.get("title", ""), 42, p.s["text"], "middle", "bold")
    p.el.append('<rect x="590" y="330" width="100" height="3" fill="%s"/>' % p.s["brand"])
    if spec.get("subtitle"):
        p.text(640, 372, spec["subtitle"], 20, p.s["muted"], "middle")
    if spec.get("footer"):
        p.text(640, 645, spec["footer"], 14, p.s["muted"], "middle")


def build_toc(p, spec, box):
    x, y, w, h = box
    items = spec.get("items") or []
    step = min(80, h / max(len(items), 1))
    fs = 18 if step >= 46 else 16
    for i, it in enumerate(items):
        iy = y + i * step + step / 2 + 6
        p.text(x + 8, iy, "%02d" % (i + 1), fs + 2, p.s["brand"], weight="bold")
        p.text(x + 62, iy, str(it), fs, p.s["text"])
        p.el.append('<line x1="%d" y1="%.0f" x2="%d" y2="%.0f" stroke="%s" stroke-width="1"/>'
                    % (x + 62, iy + 14, x + w, iy + 14, p.s["line"]))


def build_section(p, spec, box):
    if spec.get("number"):
        p.text(640, 210, spec["number"], 110, p.s["brand"], "middle", "bold")
    p.text(640, 400, spec.get("title", ""), 34, p.s["text"], "middle", "bold")
    if spec.get("lead"):
        p.text(640, 452, spec["lead"], 16, p.s["muted"], "middle")
    p.el.append('<rect x="540" y="592" width="200" height="3" fill="%s"/>' % p.s["brand"])


def build_cards(p, spec, box):
    x, y, w, h = box
    items = spec.get("items") or []
    k = max(2, min(4, len(items))) if items else 2
    # 窄盒子里放不下 k 列就降列数加行（复合布局的侧栏常见）
    cols = k
    while cols > 1 and w / cols < 240:
        cols -= 1
    rows = max(1, (k + cols - 1) // cols)
    gap = 18 if h < 300 else 24
    card_w = (w - (cols - 1) * gap) / cols
    card_h = (h - (rows - 1) * gap) / rows
    compact = card_h < 150 or card_w < 320
    head_fs = p.f["card_title"] - (2 if compact else 0)
    body_fs = p.f["body"] - (1 if compact else 0)
    for i in range(k):
        cx = x + (i % cols) * (card_w + gap)
        cy = y + (i // cols) * (card_h + gap)
        p.card(cx, cy, card_w, card_h, rx=8 if compact else 10)
        it = items[i] if i < len(items) else {}
        inner = cx + 18
        if it.get("icon"):
            isz = 30 if compact else 40
            p.icon(it["icon"], inner, cy + 16, isz, p.s["brand"])
            ty = cy + (16 + isz + 14 if compact else 24 + 40 + 12)
        else:
            ty = cy + (34 if compact else 48)
        p.text(inner, ty, it.get("head", ""), head_fs, p.s["text"], weight="bold")
        ty += 24 if compact else 30
        max_units = (card_w - 36) / float(body_fs)
        room = (cy + card_h - 14 - ty) / (20 if compact else 22)
        lines = []
        for raw in it.get("lines") or []:
            for ln in wrap_text(str(raw), body_fs, max_units):
                lines.append(ln)
        if len(lines) > room:
            p.dropped += len(lines) - int(room)
            lines = lines[: int(room)]
        step = max(20, min(44, (cy + card_h - 12 - ty) / max(len(lines), 1)))
        for ln in lines:
            p.text(inner, ty, ln, body_fs, p.s["muted"])
            ty += step


def build_columns(p, spec, box):
    x, y, w, h = box
    cols = (spec.get("columns") or [])[:2] or [{}, {}]
    gap = 24
    cw = (w - gap) / 2
    compact = h < 300 or cw < 420
    head_fs = p.f["card_title"] + (0 if compact else 2)
    body_fs = p.f["body"] + (0 if compact else 1)
    for i, col in enumerate(cols):
        cx = x + i * (cw + gap)
        p.card(cx, y, cw, h, rx=8 if compact else 10)
        inner = cx + 22
        if col.get("icon"):
            isz = 30 if compact else 36
            p.icon(col["icon"], inner, y + 18, isz, p.s["brand"])
            ty = y + 18 + isz + 14
        else:
            ty = y + (40 if compact else 46)
        p.text(inner, ty, col.get("head", ""), head_fs, p.s["text"], weight="bold")
        ty += 26 if compact else 32
        max_units = (cw - 50) / float(body_fs)
        room = (y + h - 16 - ty) / (22 if compact else 24)
        lines = []
        for raw in col.get("lines") or []:
            for ln in wrap_text("• " + str(raw), body_fs, max_units):
                lines.append(ln)
        if len(lines) > room:
            p.dropped += len(lines) - int(room)
            lines = lines[: int(room)]
        step = max(20, min(44, (y + h - 14 - ty) / max(len(lines), 1)))
        for ln in lines:
            p.text(inner, ty, ln, body_fs, p.s["muted"])
            ty += step


def build_bullets(p, spec, box):
    x, y, w, h = box
    items = spec.get("items") or []
    fs = p.f["body"] + (0 if h < 300 else 2)
    base_step = fs + 12
    # pre-wrap to know total height, then stretch inter-item gaps so few items
    # still spread down the box (vertical coverage)
    max_units = (w - 30) / float(fs)
    wrapped = [wrap_text(str(it), fs, max_units) for it in items]
    total_h = sum(len(w) * base_step for w in wrapped) + 6 * (len(wrapped) - 1 if wrapped else 0)
    slack = max(0.0, h - total_h)
    extra = slack / max(len(wrapped), 1) if wrapped else 0
    cy = y
    for lines in wrapped:
        p.el.append('<circle cx="%d" cy="%.0f" r="3.5" fill="%s"/>'
                    % (x + 8, cy - 5, p.s["brand"]))
        for ln in lines:
            p.text(x + 26, cy, ln, fs, p.s["text"])
            cy += base_step
        cy += 6 + min(extra, 60)
        if cy > y + h:
            p.dropped += len(wrapped) - (wrapped.index(lines) + 1)
            break


def build_kpi_row(p, spec, box):
    """3-6 个紧凑数字卡：大数字（brand）+ 标签 + 可选 delta（accent）。
    作为 composite 的顶部条带最常用（高 ~120-140px）。"""
    x, y, w, h = box
    items = spec.get("items") or []
    n = max(2, min(6, len(items))) if items else 3
    gap = 14
    cw = (w - (n - 1) * gap) / n
    num_fs = max(20, min(44, int(h * 0.38)))
    lab_fs = max(12, min(16, int(h * 0.13)))
    for i in range(n):
        cx = x + i * (cw + gap)
        p.card(cx, y, cw, h, rx=8)
        it = items[i] if i < len(items) else {}
        cxm = cx + cw / 2
        p.text(cxm, y + h * 0.52, str(it.get("num", "")), num_fs, p.s["brand"],
               "middle", "bold")
        p.text(cxm, y + h * 0.78, str(it.get("label", "")), lab_fs, p.s["muted"], "middle")
        if it.get("delta"):
            p.text(cxm, y + h - 8, str(it["delta"]), lab_fs, p.s["accent"], "middle")


def build_panel(p, spec, box):
    """通用面板块：卡片内 head + 要点行。适合复合布局的侧栏/次区块。"""
    x, y, w, h = box
    p.card(x, y, w, h, rx=8 if h < 260 else 10)
    compact = h < 220 or w < 420
    head_fs = p.f["card_title"] - (2 if compact else 0)
    body_fs = p.f["body"] - (1 if compact else 0)
    inner = x + 20
    ty = y + (30 if compact else 40)
    if spec.get("icon"):
        isz = 28 if compact else 34
        p.icon(spec["icon"], inner, ty - isz + 6, isz, p.s["brand"])
        p.text(inner + isz + 10, ty, spec.get("head", ""), head_fs, p.s["text"], weight="bold")
    else:
        p.text(inner, ty, spec.get("head", ""), head_fs, p.s["text"], weight="bold")
    # head 下细线
    p.el.append('<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1"/>'
                % (inner, ty + 8, x + w - 20, ty + 8, p.s["line"]))
    ty += 26 if compact else 32
    max_units = (w - 44) / float(body_fs)
    room = (y + h - 14 - ty) / (20 if compact else 22)
    lines = []
    for raw in spec.get("lines") or []:
        for ln in wrap_text("• " + str(raw), body_fs, max_units):
            lines.append(ln)
    if len(lines) > room:
        p.dropped += len(lines) - int(room)
        lines = lines[: int(room)]
    step = max(20, min(40, (y + h - 12 - ty) / max(len(lines), 1)))
    for ln in lines:
        p.text(inner, ty, ln, body_fs, p.s["muted"])
        ty += step


def build_ending(p, spec, box):
    p.text(640, 340, spec.get("title", "谢谢观看"), 38, p.s["text"], "middle", "bold")
    if spec.get("subtitle"):
        p.text(640, 382, spec["subtitle"], 18, p.s["muted"], "middle")
    if spec.get("footer"):
        p.text(640, 645 if spec.get("subtitle") else 390, spec["footer"], 15, p.s["muted"], "middle")


def build_composite(p, spec, box):
    """一页多块：title band + blocks[].region 内各自原生排版（真实字号，
    无缩放），一次调用生成整页——替代旧的分块生成+手拼 <g>。"""
    lead_lines = p.title_band(spec.get("title", ""), spec.get("lead", ""))
    blocks = spec.get("blocks") or []
    regions = []
    for b in blocks:
        region = b.get("region")
        btype = b.get("type", "")
        if (not isinstance(region, (list, tuple)) or len(region) != 4
                or btype not in BLOCK_TYPES):
            p.block_reports.append({"type": btype, "error": "bad region or non-block type (skipped)"})
            continue
        try:
            bx, by, bw, bh = [float(v) for v in region]
        except (TypeError, ValueError):
            p.block_reports.append({"type": btype, "error": "non-numeric region (skipped)"})
            continue
        sub = {k: v for k, v in b.items() if k not in ("region", "type")}
        before_dropped = p.dropped
        BUILDERS[btype](p, sub, (bx, by, bw, bh))
        rep = {"type": btype, "region": [bx, by, bw, bh]}
        if p.dropped > before_dropped:
            rep["lines_dropped"] = p.dropped - before_dropped
        p.block_reports.append(rep)
        regions.append((bx, by, bw, bh))
    # 粗略重叠检测（矩形相交>2px 容差）——提示而非阻塞
    for i in range(len(regions)):
        for j in range(i + 1, len(regions)):
            ax, ay, aw, ah = regions[i]
            bx, by, bw, bh = regions[j]
            if ax < bx + bw - 2 and bx < ax + aw - 2 and ay < by + bh - 2 and by < ay + ah - 2:
                p.block_reports.append({"warning": f"blocks {i + 1} and {j + 1} overlap"})


BUILDERS = {
    "cover": build_cover, "toc": build_toc, "section": build_section,
    "cards": build_cards, "columns": build_columns,
    "bullets": build_bullets, "kpi_row": build_kpi_row, "panel": build_panel,
    "composite": build_composite, "ending": build_ending,
}

# 整页渲染时自带 title band 的类型（block 视图不含 band）
FULLPAGE_WITH_BAND = ("toc", "cards", "columns", "bullets", "kpi_row", "composite")


def main():
    ap = argparse.ArgumentParser(description="Build common page types from a compact page-spec JSON.")
    ap.add_argument("pages_json", help="pages.json file (or '-' for stdin)")
    ap.add_argument("--project", required=True, help="ppt-auto project dir (writes svg_output/)")
    ap.add_argument("--config", default=None, help="template_config.json (default: skill dir)")
    ap.add_argument("--autofit", default=None,
                    help="autofit_fontsize.py 输出 JSON 路径（S-01：覆盖 config font_sizes；"
                         "pages.json 每页 fonts 仍最高优先）。Step 5 生成："
                         "autofit_fontsize.py ... > <project>/output/autofit_result.json")
    ap.add_argument("--region", default=None,
                    help="块页模式：x,y,w,h——整份输出作为单个块在该区域内原生排版"
                         "（真实字号，不缩放、不画背景）；pages.json 每页可用 "
                         "\"region\": [x,y,w,h] 单独指定")
    args = ap.parse_args()

    raw = sys.stdin.read() if args.pages_json == "-" else open(args.pages_json, "r", encoding="utf-8").read()
    doc = json.loads(raw)
    specs = doc["pages"] if isinstance(doc, dict) else doc
    style = load_style(args.config)

    autofit_data = None
    if args.autofit:
        try:
            with open(args.autofit, "r", encoding="utf-8") as f:
                autofit_data = json.load(f)
        except (OSError, ValueError) as e:
            print(f"[build_page_skeleton] WARN: cannot read --autofit {args.autofit}: {e}",
                  file=sys.stderr)

    cli_region = None
    if args.region:
        try:
            vals = [float(v) for v in args.region.split(",")]
            if len(vals) == 4 and all(v >= 0 for v in vals):
                cli_region = vals
            else:
                raise ValueError("need x,y,w,h")
        except ValueError as e:
            print(f"[build_page_skeleton] WARN: bad --region {args.region!r} ({e}); ignored",
                  file=sys.stderr)

    svg_dir = os.path.join(args.project, "svg_output")
    os.makedirs(svg_dir, exist_ok=True)

    out_pages = []
    for i, spec in enumerate(specs, start=1):
        ptype = spec.get("type", "")
        if ptype not in BUILDERS:
            out_pages.append({"index": i, "type": ptype, "error": "unknown type (skipped)"})
            continue
        p = Page(style, spec.get("fonts"), autofit=autofit_data)
        p.meta = {"page_type": ptype, "slide_number": i}  # D-03
        region = spec.get("region") or cli_region
        if isinstance(region, (list, tuple)) and len(region) == 4:
            try:
                p.region = [float(v) for v in region]  # 块页：原生排版于该区域
                p.meta["region"] = 1  # 拼装块标记：check_svg 豁免整页内容底线
            except (TypeError, ValueError):
                pass

        if p.region:
            # 块页：无 title band，内容直接落 region（真实字号）
            if ptype in BLOCK_TYPES:
                bx, by, bw, bh = p.region
                BUILDERS[ptype](p, spec, (bx, by, bw, bh))
            else:
                out_pages.append({"index": i, "type": ptype,
                                  "error": f"type {ptype} cannot render as a region block"})
                continue
        elif ptype == "composite":
            BUILDERS[ptype](p, spec, None)
        elif ptype in FULLPAGE_WITH_BAND:
            lead = spec.get("lead", "") if ptype != "toc" else ""
            n_lead = p.title_band(spec.get("title", ""), lead)
            BUILDERS[ptype](p, spec, default_box(n_lead))
        else:  # cover / section / ending：整页居中式，无 band
            BUILDERS[ptype](p, spec, None)

        # Default name carries the type suffix: keeps slide_* sort order (and
        # svg_to_pptx/QA pairing) while check_svg's filename heuristics exempt
        # cover/ending pages from density checks, as they do for hand-drawn ones.
        name = spec.get("out") or "slide_%02d_%s.svg" % (i, ptype)
        path = os.path.join(svg_dir, name)
        with open(path, "w", encoding="utf-8") as f:
            f.write(p.svg())
        entry = {"index": i, "type": ptype, "out": name}
        if p.region:
            entry["region"] = p.region
        if p.block_reports:
            entry["blocks"] = p.block_reports
        n_texts = sum(1 for e in p.el if e.startswith("<text"))
        if ptype in ("cover", "ending", "section") and n_texts < 3:
            entry["warning"] = "sparse page has <3 texts (checker floor even for cover/section/ending) — add subtitle/lead/footer"
        if p.dropped:
            entry["lines_dropped"] = p.dropped
            entry["warning"] = "spec too long for the layout — trim lines/items and regenerate"
        out_pages.append(entry)

    print(json.dumps({"pages": out_pages, "svg_dir": svg_dir,
                      "has_template": style["has_template"]}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
