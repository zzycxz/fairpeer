# -*- coding: utf-8 -*-
"""
模板分析脚本：导出 PPT 模板每页为 PNG 背景图。
支持 PowerPoint 和 WPS。

用法: python analyze_template.py <template.pptx> <output_dir>

输出:
  output_dir/slide_01.png  (封面)
  output_dir/slide_02.png  (内容页)
  output_dir/slide_03.png  (结尾)
"""
import sys, os
import json

def extract_colors(image_path, max_colors=4):
    try:
        from PIL import Image
        img = Image.open(image_path).convert("RGB")
        img.thumbnail((200, 200))
        q_img = img.quantize(colors=16)
        palette = q_img.getpalette()
        counts = q_img.getcolors()
        if not counts or not palette: return []
        
        counts.sort(reverse=True, key=lambda x: x[0])
        hex_colors = []
        for count, idx in counts:
            r = palette[idx*3]
            g = palette[idx*3+1]
            b = palette[idx*3+2]
            # Skip pure white/black as main theme colors
            if r > 240 and g > 240 and b > 240: continue
            if r < 15 and g < 15 and b < 15: continue
            hex_c = f"#{r:02x}{g:02x}{b:02x}"
            if hex_c not in hex_colors:
                hex_colors.append(hex_c)
            if len(hex_colors) >= max_colors:
                break
        return hex_colors
    except ImportError:
        print("Pillow not installed, skipping color extraction.")
        return []
    except Exception as e:
        print(f"Color extraction failed: {e}")
        return []

def write_dynamic_style(bg_cover, bg_content, project_dir):
    # Determine max_colors from template_config.json if possible
    max_extracted = 4
    max_per_page = 3
    config_path = os.path.join(os.path.dirname(__file__), "..", "template_config.json")
    try:
        with open(config_path, "r", encoding="utf-8") as f:
            cfg = json.load(f)
            max_extracted = cfg.get("rules", {}).get("color_limits", {}).get("max_extracted", 4)
            max_per_page = cfg.get("rules", {}).get("color_limits", {}).get("max_per_page", 3)
    except Exception:
        pass

    colors = []
    if bg_content and os.path.exists(bg_content):
        colors = extract_colors(bg_content, max_extracted)
    if not colors and bg_cover and os.path.exists(bg_cover):
        colors = extract_colors(bg_cover, max_extracted)
        
    if not colors: return
    
    style = {
        "colors": {
            "primary": colors[0] if len(colors) > 0 else "#ffffff",
            "secondary": colors[1] if len(colors) > 1 else "#a0a0a0",
            "accent": colors[2] if len(colors) > 2 else "#f28b50"
        },
        "rules": {
            "color_usage": f"严格遵守配置的色彩，单页最多允许使用 {max_per_page} 种颜色，防花哨。基于实际视觉提取，严禁臆想。",
            "color_limits": {
                "max_extracted": max_extracted,
                "max_per_page": max_per_page
            }
        }
    }
    out = os.path.join(project_dir, "dynamic_style.json")
    with open(out, 'w', encoding='utf-8') as f:
        json.dump(style, f, ensure_ascii=False, indent=2)
    print(f"  Extracted dynamic style to {out}")

def detect_office_app():
    """检测本机安装的办公软件，返回 COM 对象名称"""
    try:
        import comtypes.client
        # 尝试 PowerPoint
        try:
            app = comtypes.client.CreateObject("PowerPoint.Application")
            app.Quit()
            return "PowerPoint.Application"
        except Exception:
            pass
        # 尝试 WPS (KWPS.Application 或 WPS.Application)
        for name in ["KWPS.Application", "WPS.Application"]:
            try:
                app = comtypes.client.CreateObject(name)
                app.Quit()
                return name
            except Exception:
                pass
        # WPS 兼容模式（注册为 PowerPoint.Application）
        try:
            app = comtypes.client.CreateObject("PowerPoint.Application")
            app.Quit()
            return "PowerPoint.Application"
        except Exception:
            pass
    except ImportError:
        pass
    return None


def analyze_template(template_path, output_dir):
    """分析模板，导出背景图"""
    try:
        import comtypes.client
    except ImportError:
        print("ERROR: comtypes not installed. Run: pip install comtypes")
        sys.exit(1)

    app_name = detect_office_app()
    if not app_name:
        print("ERROR: No office application found. Need PowerPoint or WPS.")
        sys.exit(1)

    print(f"Using: {app_name}")
    os.makedirs(output_dir, exist_ok=True)

    powerpoint = comtypes.client.CreateObject(app_name)
    powerpoint.Visible = 1
    prs = powerpoint.Presentations.Open(os.path.abspath(template_path), WithWindow=False)

    slide_count = prs.Slides.Count
    print(f"Template: {os.path.basename(template_path)} ({slide_count} slides)")

    backgrounds = {}
    for i in range(1, slide_count + 1):
        slide = prs.Slides(i)
        out_path = os.path.join(output_dir, f"slide_{i:02d}.png")
        slide.Export(out_path, 'PNG', 1280, 720)
        print(f"  Slide {i} -> {out_path}")
        backgrounds[i] = out_path

    prs.Close()
    powerpoint.Quit()

    # 映射角色
    if slide_count >= 3:
        result = {
            "cover": backgrounds[1],
            "content": backgrounds[2],
            "ending": backgrounds[slide_count],
            "slide_count": slide_count,
        }
    elif slide_count == 2:
        result = {
            "cover": backgrounds[1],
            "content": backgrounds[2],
            "ending": backgrounds[2],
            "slide_count": slide_count,
        }
    else:
        result = {
            "cover": backgrounds[1],
            "content": backgrounds[1],
            "ending": backgrounds[1],
            "slide_count": slide_count,
        }

    # 复制为标准名称
    import shutil
    for role, src in result.items():
        if role in ("cover", "content", "ending"):
            dst = os.path.join(output_dir, f"bg_{role}.png")
            if src != dst:
                shutil.copy2(src, dst)
                print(f"  {role}: {dst}")

    print(f"\nDone. Backgrounds in {output_dir}")
    
    # Output dynamic style to the project dir (parent of backgrounds)
    project_dir = os.path.dirname(os.path.normpath(output_dir))
    write_dynamic_style(
        os.path.join(output_dir, "bg_cover.png"),
        os.path.join(output_dir, "bg_content.png"),
        project_dir
    )
    
    return result


if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("Usage: python analyze_template.py <template.pptx> <output_dir>")
        sys.exit(1)
    # Normalize paths for Windows
    template = os.path.normpath(sys.argv[1])
    output = os.path.normpath(sys.argv[2])
    analyze_template(template, output)
