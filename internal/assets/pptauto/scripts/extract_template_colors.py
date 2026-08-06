#!/usr/bin/env python3
"""extract_template_colors.py — 从 PPTX 模板提取配色方案，写入 template_config.json

纯 python-pptx 实现（不依赖 Office COM）。扫描模板的 slide master / slide layout
里的 solidFill 颜色和图片，推断主题色、背景色、强调色，更新 template_config.json。

用法: python3 extract_template_colors.py <template.pptx> <template_config.json>
"""

import sys
import os
import json
from pathlib import Path

# Fix Windows console encoding (GBK → UTF-8) so Chinese template names and
# color output don't crash with UnicodeEncodeError.
try:
    from console_encoding import configure_utf8_stdio
    configure_utf8_stdio()
except ImportError:
    # Fallback: force UTF-8 directly if console_encoding module not on path.
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

def extract_colors_from_pptx(pptx_path):
    """从 PPTX 模板提取配色方案 + 最佳 layout 索引，返回 dict。"""
    from pptx import Presentation
    from lxml import etree

    NS = {
        'p': 'http://schemas.openxmlformats.org/presentationml/2006/main',
        'a': 'http://schemas.openxmlformats.org/drawingml/2006/main',
        'r': 'http://schemas.openxmlformats.org/officeDocument/2006/relationships',
    }

    prs = Presentation(str(pptx_path))
    all_colors = {}  # hex -> count

    def collect_colors(xml_element):
        """递归收集 srgbClr 颜色值。"""
        for clr in xml_element.findall('.//a:srgbClr', NS):
            val = clr.get('val')
            if val:
                val = val.upper()
                if val in ('000000', 'FFFFFF', '00000000'):
                    continue
                all_colors[val] = all_colors.get(val, 0) + 1

    def count_shapes(xml_element):
        """统计 shapes 数量（区分内容页 vs 封面页）。"""
        sps = xml_element.findall('.//p:sp', NS)
        pics = xml_element.findall('.//p:pic', NS)
        return len(sps) + len(pics)

    # 扫描 slide master
    for master in prs.slide_masters:
        collect_colors(master.element)

    # 分析每个 layout：收集颜色 + 统计 shapes + 记录图片数
    layout_info = []
    for li, layout in enumerate(prs.slide_layouts):
        layout_colors = {}
        for clr in layout.element.findall('.//a:srgbClr', NS):
            val = clr.get('val')
            if val:
                val = val.upper()
                if val not in ('000000', 'FFFFFF', '00000000'):
                    layout_colors[val] = layout_colors.get(val, 0) + 1
        shape_count = count_shapes(layout.element)
        image_count = len(layout.element.findall('.//a:blip', NS))
        layout_info.append({
            'index': li,
            'name': layout.name,
            'colors': layout_colors,
            'shapes': shape_count,
            'images': image_count,
        })
        collect_colors(layout.element)

    # 扫描 slides
    for slide in prs.slides:
        collect_colors(slide.element)

    if not all_colors:
        return None

    # 选最佳 content layout：shapes 最多（内容页比封面页 shapes 多），
    # 或有图片的 layout（通常内容页有背景图片）。
    # 排除名字明显是封面/结尾的 layout。
    cover_names = {'封面', '标题', 'cover', 'title slide', 'title'}
    ending_names = {'结尾', '致谢', 'ending', 'thank', 'back'}

    best_layout_index = 0
    best_score = -1
    for info in layout_info:
        name_lower = info['name'].lower()
        # 封面/结尾页降分
        penalty = 0
        if any(kw in name_lower for kw in cover_names):
            penalty = 5
        if any(kw in name_lower for kw in ending_names):
            penalty = 5
        # 内容页加分：shapes 多 + 有图片
        score = info['shapes'] + info['images'] * 2 - penalty
        if score > best_score:
            best_score = score
            best_layout_index = info['index']

    # 按出现频率排序
    sorted_colors = sorted(all_colors.items(), key=lambda x: -x[1])

    # 分析颜色
    def brightness(hex_val):
        r = int(hex_val[:2], 16)
        g = int(hex_val[2:4], 16)
        b = int(hex_val[4:6], 16)
        return (r + g + b) / 3

    def saturation(hex_val):
        r = int(hex_val[:2], 16) / 255
        g = int(hex_val[2:4], 16) / 255
        b = int(hex_val[4:6], 16) / 255
        mx, mn = max(r, g, b), min(r, g, b)
        return mx - mn if mx > 0 else 0

    darkest = min(sorted_colors, key=lambda x: brightness(x[0]))
    most_saturated = max(sorted_colors, key=lambda x: saturation(x[0]))

    bg_hex = darkest[0]
    bg_brightness = brightness(bg_hex)
    is_dark = bg_brightness < 128

    colors = {}
    colors['background'] = f"#{bg_hex}"
    if is_dark:
        colors['primary'] = '#FFFFFF'
        colors['text'] = '#FFFFFF'
        colors['secondary'] = '#A0A0A0'
        colors['secondary_light'] = '#2A2A2A'
        colors['text_muted'] = '#888888'
        colors['text_secondary'] = '#A0A0A0'
        colors['card_bg'] = '#1E1E1E'
        colors['line'] = '#333333'
    else:
        colors['primary'] = '#1A1A1A'
        colors['text'] = '#1A1A1A'
        colors['secondary'] = '#666666'
        colors['secondary_light'] = '#E8E8E8'
        colors['text_muted'] = '#999999'
        colors['text_secondary'] = '#666666'
        colors['card_bg'] = '#F5F5F5'
        colors['line'] = '#E0E0E0'

    colors['accent'] = f"#{most_saturated[0]}"
    colors['white'] = '#FFFFFF'

    extracted_palette = [f"#{c}" for c, _ in sorted_colors[:6]]

    # 记录 layout 分析结果
    layout_summary = [{
        'index': info['index'],
        'name': info['name'],
        'shapes': info['shapes'],
        'images': info['images'],
        'is_cover': any(kw in info['name'].lower() for kw in cover_names),
        'is_ending': any(kw in info['name'].lower() for kw in ending_names),
    } for info in layout_info]

    return {
        'colors': colors,
        'is_dark_theme': is_dark,
        'extracted_palette': extracted_palette,
        'template_name': Path(pptx_path).stem,
        'best_layout_index': best_layout_index,
        'layouts': layout_summary,
    }

    if not all_colors:
        return None

    # 按出现频率排序
    sorted_colors = sorted(all_colors.items(), key=lambda x: -x[1])

    # 分析颜色：区分深色/浅色、找强调色
    def brightness(hex_val):
        r = int(hex_val[:2], 16)
        g = int(hex_val[2:4], 16)
        b = int(hex_val[4:6], 16)
        return (r + g + b) / 3

    def to_hex(r, g, b):
        return f"#{r:02X}{g:02X}{b:02X}"

    # 找最深色作为 background 候选
    darkest = min(sorted_colors, key=lambda x: brightness(x[0]))
    # 找最亮且非白的作为 text 候选
    lightest_non_white = max(
        [c for c in sorted_colors if brightness(c[0]) > 100],
        key=lambda x: brightness(x[0]),
        default=None,
    )
    # 找饱和度最高的作为 accent
    def saturation(hex_val):
        r = int(hex_val[:2], 16) / 255
        g = int(hex_val[2:4], 16) / 255
        b = int(hex_val[4:6], 16) / 255
        mx, mn = max(r, g, b), min(r, g, b)
        return mx - mn if mx > 0 else 0

    most_saturated = max(sorted_colors, key=lambda x: saturation(x[0]))

    bg_hex = darkest[0]
    bg_brightness = brightness(bg_hex)
    is_dark = bg_brightness < 128

    # 构建配色方案
    colors = {}
    colors['background'] = f"#{bg_hex}"
    if is_dark:
        colors['primary'] = '#FFFFFF'
        colors['text'] = '#FFFFFF'
        colors['secondary'] = '#A0A0A0'
        colors['secondary_light'] = '#2A2A2A'
        colors['text_muted'] = '#888888'
        colors['text_secondary'] = '#A0A0A0'
        colors['card_bg'] = '#1E1E1E'
        colors['line'] = '#333333'
    else:
        colors['primary'] = '#1A1A1A'
        colors['text'] = '#1A1A1A'
        colors['secondary'] = '#666666'
        colors['secondary_light'] = '#E8E8E8'
        colors['text_muted'] = '#999999'
        colors['text_secondary'] = '#666666'
        colors['card_bg'] = '#F5F5F5'
        colors['line'] = '#E0E0E0'

    colors['accent'] = f"#{most_saturated[0]}"
    colors['white'] = '#FFFFFF'

    # 收集提取到的所有颜色（给 agent 参考）
    extracted_palette = [f"#{c}" for c, _ in sorted_colors[:6]]

    return {
        'colors': colors,
        'is_dark_theme': is_dark,
        'extracted_palette': extracted_palette,
        'template_name': Path(pptx_path).stem,
    }


def update_template_config(config_path, extracted):
    """用提取的配色更新 template_config.json。"""
    with open(config_path, 'r', encoding='utf-8') as f:
        config = json.load(f)

    # 覆盖 colors 段
    old_colors = config.get('colors', {})
    new_colors = extracted['colors']

    # 合并：新值覆盖旧值，旧值里新值没有的保留
    for k, v in new_colors.items():
        old_colors[k] = v

    config['colors'] = old_colors

    # 记录模板来源
    if '_template' not in config:
        config['_template'] = {}
    config['_template']['name'] = extracted['template_name']
    config['_template']['is_dark'] = extracted['is_dark_theme']
    config['_template']['palette'] = extracted['extracted_palette']
    config['_template']['best_layout_index'] = extracted['best_layout_index']
    config['_template']['layouts'] = extracted['layouts']

    with open(config_path, 'w', encoding='utf-8') as f:
        json.dump(config, f, ensure_ascii=False, indent=2)

    return old_colors


def main():
    if len(sys.argv) < 3:
        print("用法: python3 extract_template_colors.py <template.pptx> <template_config.json>")
        sys.exit(1)

    pptx_path = Path(sys.argv[1])
    config_path = Path(sys.argv[2])

    if not pptx_path.exists():
        print(f"错误: 模板文件不存在: {pptx_path}")
        sys.exit(1)

    if not config_path.exists():
        print(f"错误: 配置文件不存在: {config_path}")
        sys.exit(1)

    extracted = extract_colors_from_pptx(pptx_path)
    if extracted is None:
        print(f"警告: 未能从模板 {pptx_path.name} 提取配色（模板可能使用纯图片背景），保留现有配色")
        sys.exit(0)

    colors = update_template_config(config_path, extracted)
    print(f"已从模板 {pptx_path.name} 提取配色并更新 {config_path.name}:")
    print(f"  background: {colors.get('background')}")
    print(f"  accent:     {colors.get('accent')}")
    print(f"  primary:    {colors.get('primary')}")
    print(f"  text:       {colors.get('text')}")
    print(f"  深色主题:   {extracted['is_dark_theme']}")
    print(f"  提取调色板: {extracted['extracted_palette']}")


if __name__ == '__main__':
    main()
