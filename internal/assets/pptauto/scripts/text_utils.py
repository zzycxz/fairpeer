#!/usr/bin/env python3
"""Shared text/color measurement helpers for ppt-auto scripts (S-05③/S-18).

Single home for the CJK-aware width estimate (check_svg overflow shadow +
build_page_skeleton wrapping must agree) and the color normalizer
(svg_quality_checker spec_lock drift must not false-flag short hex or named
colors). Keep this module dependency-free so every script can import it.
"""
from __future__ import annotations

# Common named colors — enough for drift checking; not a full CSS table.
_NAMED_COLORS = {
    "white": "#FFFFFF", "black": "#000000", "red": "#FF0000",
    "green": "#008000", "blue": "#0000FF", "yellow": "#FFFF00",
    "orange": "#FFA500", "purple": "#800080", "gray": "#808080",
    "grey": "#808080", "silver": "#C0C0C0", "navy": "#000080",
    "teal": "#008080", "aqua": "#00FFFF", "cyan": "#00FFFF",
    "fuchsia": "#FF00FF", "magenta": "#FF00FF", "lime": "#00FF00",
    "maroon": "#800000", "olive": "#808000",
}


def cjk_char_units(ch: str) -> float:
    """CJK 字符约占 1em、Latin 约 0.55em——骨架换行与溢出检查共用同一套。"""
    return 1.0 if ord(ch) > 0x2E80 else 0.55


def estimate_text_width(text: str, font_size: float) -> float:
    """CJK 感知的文字宽度估算（S-05）。旧的 len*0.6 对中文偏小（漏报溢出）、
    对英文偏大（误报溢出）。"""
    return sum(cjk_char_units(c) for c in text) * font_size


def normalize_color(c: str) -> str:
    """归一化颜色字符串以便比较（S-18）：

    - "#FFF" → "#FFFFFF"（短 hex 展开）
    - "#RRGGBBAA" → "#RRGGBB"（丢 alpha——漂移检查关心色相）
    - 命名颜色（white/black/…）→ 标准 hex
    - 其余原样大写返回；非字符串/空值返回空串
    """
    if not isinstance(c, str):
        return ""
    c = c.strip()
    low = c.lower()
    if low in _NAMED_COLORS:
        return _NAMED_COLORS[low]
    if c.startswith("#"):
        body = c[1:]
        if len(body) == 3 and all(ch in "0123456789abcdefABCDEF" for ch in body):
            return "#" + "".join(ch * 2 for ch in body).upper()
        if len(body) in (6, 8):
            return ("#" + body[:6]).upper()
    return c.upper()
