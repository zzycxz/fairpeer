#!/usr/bin/env python3
"""extract_template_colors.py — 从 PPTX 模板提取配色方案，写入 template_config.json

混合提取策略（先结构后视觉，参考主流 PPT 工具的标准流程）：
  1. 读 theme1.xml 配色方案（accent1-6 / dk1 / lt1 等）→ 主题色
  2. 扫 slide master / layout / slide 的 solidFill + schemeClr → 实际使用色
  3. 检测全屏 blipFill 图片背景 → 抽图用 PIL 算真实背景色（视觉兜底）
     这一步对"图片驱动模板"至关重要：纯靠 XML 启发式会把 theme 的 dk2
     误判成背景色（见 commit 历史）。

用法: python3 extract_template_colors.py <template.pptx> <template_config.json>
"""

import sys
import os
import json
import zipfile
from pathlib import Path

# Fix Windows console encoding (GBK → UTF-8) so Chinese template names and
# color output don't crash with UnicodeEncodeError.
try:
    from console_encoding import configure_utf8_stdio
    configure_utf8_stdio()
except ImportError:
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')


# 16:9 全屏尺寸阈值（EMU）。13.33in × 7.5in = 12192000 × 6858000。
# 容差 300000 EMU ≈ 0.33in，兼容不同 dpi 的模板。
_FULL_SCREEN_W = 12192000
_FULL_SCREEN_H = 6858000
_FULL_SCREEN_TOL = 400000

# 13.33in × 7.5in @ 96dpi 的像素尺寸，用于 layout 判断
_SLIDE_W_PX = 1280
_SLIDE_H_PX = 720


def _is_full_screen_bg(x, y, cx, cy):
    """判断一个 pic 元素是否覆盖整张幻灯片（全屏背景图）。"""
    at_origin = x < 200000 and y < 200000
    full_w = abs(cx - _FULL_SCREEN_W) < _FULL_SCREEN_TOL
    full_h = abs(cy - _FULL_SCREEN_H) < _FULL_SCREEN_TOL
    # 也兼容 4:3（10in × 7.5in = 9144000 × 6858000）
    full_w_43 = abs(cx - 9144000) < _FULL_SCREEN_TOL
    return at_origin and (full_w or full_w_43) and full_h


def _pil_average_color(image_bytes):
    """用 PIL 算图片的平均色和主色，返回 (avg_hex, [top_hexes])。"""
    try:
        from PIL import Image
        import io as _io
        img = Image.open(_io.BytesIO(image_bytes)).convert("RGB")
        img.thumbnail((200, 200))
        # 平均色（用 numpy 加速，无 numpy 则逐像素）
        try:
            import numpy as np
            arr = np.asarray(img)
            r, g, b = int(arr[:, :, 0].mean()), int(arr[:, :, 1].mean()), int(arr[:, :, 2].mean())
        except ImportError:
            px = list(img.getdata())
            n = len(px)
            r = sum(p[0] for p in px) // n
            g = sum(p[1] for p in px) // n
            b = sum(p[2] for p in px) // n
        avg_hex = f"{r:02X}{g:02X}{b:02X}"
        # 主色聚类（量化到 16 色）
        q_img = img.quantize(colors=16)
        palette = q_img.getpalette()
        counts = q_img.getcolors()
        if counts and palette:
            counts.sort(reverse=True, key=lambda x: x[0])
            top_hexes = []
            for count, idx in counts:
                pr, pg, pb = palette[idx * 3], palette[idx * 3 + 1], palette[idx * 3 + 2]
                # 跳过接近纯白/纯黑的（背景之外的元素常用）
                if pr > 245 and pg > 245 and pb > 245:
                    continue
                if pr < 15 and pg < 15 and pb < 15:
                    continue
                hex_c = f"{pr:02X}{pg:02X}{pb:02X}"
                if hex_c not in top_hexes:
                    top_hexes.append(hex_c)
                if len(top_hexes) >= 4:
                    break
            return avg_hex, top_hexes
        return avg_hex, []
    except ImportError:
        return None, []
    except Exception:
        return None, []


def _brightness(hex_val):
    r = int(hex_val[:2], 16)
    g = int(hex_val[2:4], 16)
    b = int(hex_val[4:6], 16)
    return (r + g + b) / 3


def _saturation(hex_val):
    r = int(hex_val[:2], 16) / 255
    g = int(hex_val[2:4], 16) / 255
    b = int(hex_val[4:6], 16) / 255
    mx, mn = max(r, g, b), min(r, g, b)
    return mx - mn if mx > 0 else 0


def extract_colors_from_pptx(pptx_path, bg_image_out=None):
    """从 PPTX 模板提取配色方案 + 背景图，返回 dict。

    三层提取：
      ① theme1.xml 配色方案（accent1-6 等）
      ② master/layout/slide 的 solidFill + schemeClr
      ③ 全屏 blipFill 图片背景 → PIL 算真实背景色（视觉兜底）
         + 若 bg_image_out 指定，把原始背景图字节写到该路径
    """
    from pptx import Presentation
    from lxml import etree

    NS = {
        'p': 'http://schemas.openxmlformats.org/presentationml/2006/main',
        'a': 'http://schemas.openxmlformats.org/drawingml/2006/main',
        'r': 'http://schemas.openxmlformats.org/officeDocument/2006/relationships',
    }

    prs = Presentation(str(pptx_path))
    all_colors = {}  # hex -> count（出现频率，用于排序强调色）

    # --- 1. 读 theme.xml 的配色方案（accent1-6 是模板真正的主题色）---
    scheme_map = {}  # scheme name → hex
    try:
        with zipfile.ZipFile(str(pptx_path)) as zf:
            theme_xml = etree.fromstring(zf.read('ppt/theme/theme1.xml'))
            clr_scheme = theme_xml.find('.//a:clrScheme', NS)
            if clr_scheme is not None:
                for child in clr_scheme:
                    tag = child.tag.split('}')[-1]
                    srgb = child.find('a:srgbClr', NS)
                    sys_clr = child.find('a:sysClr', NS)
                    hex_val = None
                    if srgb is not None:
                        hex_val = srgb.get('val', '').upper()
                    elif sys_clr is not None:
                        hex_val = sys_clr.get('lastClr', '').upper()
                    if hex_val:
                        scheme_map[tag] = hex_val
                        if hex_val not in ('000000', 'FFFFFF', '00000000'):
                            all_colors[hex_val] = all_colors.get(hex_val, 0) + 5
    except Exception:
        pass

    def resolve_scheme_clr(val):
        if not val:
            return None
        v = val.lower()
        if v in scheme_map:
            return scheme_map[v]
        aliases = {'tx1': 'dk1', 'bg1': 'lt1', 'tx2': 'dk2', 'bg2': 'lt2'}
        if v in aliases and aliases[v] in scheme_map:
            return scheme_map[aliases[v]]
        return None

    def collect_colors(xml_element):
        for clr in xml_element.findall('.//a:srgbClr', NS):
            val = clr.get('val')
            if val:
                val = val.upper()
                if val not in ('000000', 'FFFFFF', '00000000'):
                    all_colors[val] = all_colors.get(val, 0) + 1
        for clr in xml_element.findall('.//a:schemeClr', NS):
            resolved = resolve_scheme_clr(clr.get('val'))
            if resolved and resolved not in ('000000', 'FFFFFF', '00000000'):
                all_colors[resolved] = all_colors.get(resolved, 0) + 1

    # --- 2. 扫描 slide master / layout / slide 的 solidFill ---
    for master in prs.slide_masters:
        collect_colors(master.element)

    # --- 3. 检测全屏图片背景 ---
    image_bg_info = None  # (avg_hex, [top_hexes]) 或 None

    with zipfile.ZipFile(str(pptx_path)) as zf:
        names = zf.namelist()

        def read_image_for_rid(rels_xml_root, rid, base_dir):
            """从 rels 找到 rid 对应的图片，返回 (图片字节, 扩展名) 或 (None, None)。"""
            for rel in rels_xml_root:
                if rel.get('Id') == rid:
                    tgt = rel.get('Target', '')
                    if 'image' not in tgt.lower():
                        return None, None
                    # tgt 相对路径：../media/image1.png
                    parts = tgt.replace('\\', '/').split('/')
                    # 去掉开头的 ..，拼到 ppt/ 下
                    clean = [p for p in parts if p and p != '..' and p != '.']
                    img_path = 'ppt/' + '/'.join(clean)
                    if img_path in names:
                        # 取扩展名（png/jpg/jpeg/gif）
                        ext = os.path.splitext(tgt)[1].lstrip('.').lower() or 'png'
                        return zf.read(img_path), ext
            return None, None

        def find_bg_in_part(part, part_type):
            """扫描一个 master/layout 的全屏 blipFill 图片，返回 (bytes, ext) 或 None。"""
            pics = part.element.findall('.//p:pic', NS)
            for pic in pics:
                xfrm = pic.find('.//p:spPr/a:xfrm', NS)
                if xfrm is None:
                    xfrm = pic.find('.//a:xfrm', NS)
                blip = pic.find('.//p:blipFill/a:blip', NS)
                if blip is None:
                    blip = pic.find('.//a:blipFill/a:blip', NS)
                if xfrm is None or blip is None:
                    continue
                off = xfrm.find('a:off', NS)
                ext_elem = xfrm.find('a:ext', NS)
                if off is None or ext_elem is None:
                    continue
                x, y = int(off.get('x', 0) or 0), int(off.get('y', 0) or 0)
                cx, cy = int(ext_elem.get('cx', 0) or 0), int(ext_elem.get('cy', 0) or 0)
                if not _is_full_screen_bg(x, y, cx, cy):
                    continue
                rid = blip.get('{http://schemas.openxmlformats.org/officeDocument/2006/relationships}embed')
                if not rid:
                    continue
                partname = str(part.part.partname) if hasattr(part, 'part') else str(part.partname)
                rels_name = os.path.basename(partname) + '.rels'
                rels_dir = os.path.dirname(partname).lstrip('/')
                rels_path = f'{rels_dir}/_rels/{rels_name}'
                if rels_path not in names:
                    continue
                rels_root = etree.fromstring(zf.read(rels_path))
                img_bytes, img_ext = read_image_for_rid(rels_root, rid, os.path.dirname(str(partname)))
                if img_bytes:
                    return img_bytes, img_ext
            return None

        # 先扫 slide masters（背景图常在 master 上），再扫 layouts
        bg_img_bytes = None
        bg_img_ext = 'png'
        for master in prs.slide_masters:
            result = find_bg_in_part(master, 'master')
            if result:
                bg_img_bytes, bg_img_ext = result
                break
        if bg_img_bytes is None:
            for layout in prs.slide_layouts:
                result = find_bg_in_part(layout, 'layout')
                if result:
                    bg_img_bytes, bg_img_ext = result
                    break

        # 如果找到了背景图，算配色 + 写出文件
        if bg_img_bytes:
            avg_hex, top_hexes = _pil_average_color(bg_img_bytes)
            if avg_hex:
                image_bg_info = (avg_hex, top_hexes or [])
                if avg_hex not in ('000000', 'FFFFFF'):
                    all_colors[avg_hex] = all_colors.get(avg_hex, 0) + 3
                for h in (top_hexes or []):
                    if h not in ('000000', 'FFFFFF'):
                        all_colors[h] = all_colors.get(h, 0) + 2
            # 写背景图到指定路径
            if bg_image_out is not None:
                bg_image_out = Path(bg_image_out)
                bg_image_out.parent.mkdir(parents=True, exist_ok=True)
                # 确保扩展名匹配
                out_path = bg_image_out.with_suffix(f'.{bg_img_ext}')
                out_path.write_bytes(bg_img_bytes)

        for layout in prs.slide_layouts:
            collect_colors(layout.element)

        for slide in prs.slides:
            collect_colors(slide.element)

    if not all_colors and image_bg_info is None:
        return None

    sorted_colors = sorted(all_colors.items(), key=lambda x: -x[1])

    # --- 4. 决定背景色：优先用图片视觉结果，否则用最深色 ---
    bg_hex = None
    has_image_background = image_bg_info is not None

    if image_bg_info is not None:
        # 图片背景：用 PIL 算出的平均色作为背景色
        bg_hex = image_bg_info[0]
        # 图片主色（排除背景后）补充进调色板候选
        img_top = [h for h in image_bg_info[1]
                   if h != bg_hex and h not in ('000000', 'FFFFFF')]
        for h in img_top:
            if h not in [c for c, _ in sorted_colors[:6]]:
                sorted_colors.append((h, 1))
        sorted_colors.sort(key=lambda x: -x[1])
    elif sorted_colors:
        # 纯色背景模板：用最深色
        bg_hex = min(sorted_colors, key=lambda x: _brightness(x[0]))[0]

    if bg_hex is None:
        return None

    is_dark = _brightness(bg_hex) < 128

    # --- 5. 构建 colors 方案 ---
    colors = {}
    colors['background'] = f"#{bg_hex}"
    if has_image_background:
        colors['background_type'] = 'image'
    else:
        colors['background_type'] = 'solid'

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

    # accent：在前 N 个高频色里选饱和度最高、且不是背景色的。
    # 只按饱和度选会选到一次性杂色（频率1的高饱和像素），所以限制候选范围。
    accent_candidates = [(c, cnt) for c, cnt in sorted_colors[:8] if c != bg_hex]
    if accent_candidates:
        # 综合饱和度与频率：饱和度为主，频率作微弱加权（log 抑制极端频率）
        import math
        def accent_score(c_cnt):
            c, cnt = c_cnt
            return _saturation(c) * (1 + math.log(max(cnt, 1)) * 0.1)
        most_saturated = max(accent_candidates, key=accent_score)[0]
        colors['accent'] = f"#{most_saturated}"
    else:
        colors['accent'] = '#4472C4'  # 兜底

    colors['white'] = '#FFFFFF'

    extracted_palette = [f"#{c}" for c, _ in sorted_colors[:6]]

    return {
        'colors': colors,
        'is_dark_theme': is_dark,
        'has_image_background': has_image_background,
        'background_image_ext': bg_img_ext if bg_img_bytes else None,
        'extracted_palette': extracted_palette,
        'template_name': Path(pptx_path).stem,
    }


def update_template_config(config_path, extracted):
    """用提取的配色更新 template_config.json。"""
    with open(config_path, 'r', encoding='utf-8') as f:
        config = json.load(f)

    old_colors = config.get('colors', {})
    new_colors = extracted['colors']

    for k, v in new_colors.items():
        old_colors[k] = v

    config['colors'] = old_colors

    if '_template' not in config:
        config['_template'] = {}
    config['_template']['name'] = extracted['template_name']
    config['_template']['is_dark'] = extracted['is_dark_theme']
    config['_template']['has_image_background'] = extracted.get('has_image_background', False)
    config['_template']['palette'] = extracted['extracted_palette']

    with open(config_path, 'w', encoding='utf-8') as f:
        json.dump(config, f, ensure_ascii=False, indent=2)

    return old_colors


def main():
    if len(sys.argv) < 3:
        print("用法: python3 extract_template_colors.py <template.pptx> <template_config.json> [bg_image_output]")
        sys.exit(1)

    pptx_path = Path(sys.argv[1])
    config_path = Path(sys.argv[2])
    bg_image_out = Path(sys.argv[3]) if len(sys.argv) >= 4 else None

    if not pptx_path.exists():
        print(f"错误: 模板文件不存在: {pptx_path}")
        sys.exit(1)

    if not config_path.exists():
        print(f"错误: 配置文件不存在: {config_path}")
        sys.exit(1)

    extracted = extract_colors_from_pptx(pptx_path, bg_image_out=bg_image_out)
    if extracted is None:
        print(f"警告: 未能从模板 {pptx_path.name} 提取配色，保留现有配色")
        sys.exit(0)

    colors = update_template_config(config_path, extracted)
    print(f"已从模板 {pptx_path.name} 提取配色并更新 {config_path.name}:")
    print(f"  background:      {colors.get('background')} ({colors.get('background_type', 'solid')})")
    print(f"  accent:          {colors.get('accent')}")
    print(f"  primary:         {colors.get('primary')}")
    print(f"  text:            {colors.get('text')}")
    print(f"  深色主题:        {extracted['is_dark_theme']}")
    print(f"  图片背景:        {extracted.get('has_image_background', False)}")
    if extracted.get('background_image_ext'):
        print(f"  背景图:          已提取 (.{extracted['background_image_ext']})")
    print(f"  提取调色板:      {extracted['extracted_palette']}")


if __name__ == '__main__':
    main()
