"""
SVG 修复脚本：修复模型生成的 SVG 中的常见错误。

用法: python fix_svg.py <input_svg> <output_svg>

修复内容：
1. 去掉 markdown 代码块标记
2. 修复嵌套 SVG（模型输出格式文本）
3. 截断到 </svg>
4. 去掉不支持的元素（filter/pattern/mask）并清理其引用：
   - mask/filter 引用 → 直接移除属性（元素不再被遮罩/滤镜，渲染安全）
   - pattern 的 fill/stroke 引用 → 替换为安全底色（删除属性会让元素变黑块）
5. 修复常见 XML 错误（<br>、路径数据里的 ·、未转义 &、未闭合 text/tspan）
6. 兜底：截断到最后一个可解析位置（有损，打 WARN 记录丢弃行数）
"""
import json
import os
import re
import sys
import xml.etree.ElementTree as ET

_FALLBACK_FILL = None


def _fallback_fill():
    """Safe replacement fill for removed pattern references: the skill config's
    card_bg (semi-transparent white) when readable, else plain white. Deleting
    the attribute instead would render the element BLACK (SVG default fill),
    so a replacement value is mandatory (S-13)."""
    global _FALLBACK_FILL
    if _FALLBACK_FILL is None:
        _FALLBACK_FILL = "#FFFFFF"
        cfg_path = os.path.join(
            os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
            "template_config.json")
        try:
            import json
            with open(cfg_path, "r", encoding="utf-8") as f:
                card = (json.load(f).get("colors") or {}).get("card_bg")
            if card:
                _FALLBACK_FILL = card
        except (OSError, ValueError):
            pass
    return _FALLBACK_FILL


def _parses(content):
    try:
        ET.fromstring(content)
        return True
    except ET.ParseError:
        return False


def _remove_unsupported(content):
    """Drop filter/pattern/mask elements (paired AND self-closing) and clean
    their url(#id) references. Gradient ids are untouched — they use the same
    url(#...) syntax, so id sets are collected before any removal (S-13)."""
    id_sets = {}
    for tag in ("filter", "pattern", "mask"):
        id_sets[tag] = set(re.findall(r'<%s\b[^>]*\bid=["\']([^"\']+)["\']' % tag, content))
        content = re.sub(r'<%s\b[^>]*/>' % tag, '', content)
        content = re.sub(r'<%s\b[^>]*>.*?</%s>' % (tag, tag), '', content,
                         flags=re.DOTALL)
    content = re.sub(r'<defs>\s*</defs>', '', content)

    # mask/filter references: remove the attribute (element renders unmasked)
    for tag in ("filter", "mask"):
        for eid in id_sets[tag]:
            content = re.sub(r'\s*%s="url\(#%s\)"' % (tag, re.escape(eid)), '', content)
    # pattern fill/stroke references: replace with a safe color, never delete
    for attr in ("fill", "stroke"):
        for eid in id_sets["pattern"]:
            content = re.sub(r'%s="url\(#%s\)"' % (attr, re.escape(eid)),
                             '%s="%s"' % (attr, _fallback_fill()), content)
    return content


def _escape_amp(content):
    """Escape bare & (leave existing entities and numeric refs alone)."""
    return re.sub(r'&(?!(?:amp|lt|gt|quot|apos|#\d+|#x[0-9a-fA-F]+);)', '&amp;', content)


def _close_unclosed_tags(content):
    """Append the missing </tspan>/</text> closers before </svg> so a prefix
    with unclosed text elements can still parse (S-07)."""
    for tag in ("tspan", "text"):
        opens = len(re.findall(r'<%s\b[^>]*(?<!/)>' % tag, content))
        closes = len(re.findall(r'</%s>' % tag, content))
        if opens > closes:
            content = content.replace(
                "</svg>", ("</%s>" % tag) * (opens - closes) + "</svg>", 1)
    return content


def fix_svg(input_path, output_path):
    """修复并落盘。返回结果 dict（S-19：stdout JSON 由 CLI 打印，
    WARN 走 stderr，退出码恒 0——修复失败会抛异常）。"""
    with open(input_path, "r", encoding="utf-8") as f:
        content = f.read()
    truncated = False
    dropped_lines = 0

    # 1. 去掉 markdown 代码块
    if "```" in content:
        svg_start = content.find("<svg")
        if svg_start >= 0:
            content = content[svg_start:]
        else:
            for part in content.split("```"):
                if "<svg" in part:
                    content = part[part.find("<svg"):]
                    break

    # 2. 修复嵌套 SVG（模型输出格式文本如 "Final SVG code:"）
    first_svg = content.find("<svg")
    second_svg = content.find("<svg", first_svg + 1) if first_svg >= 0 else -1
    if second_svg > 0:
        content = content[second_svg:]

    # 3. 截断到 </svg>
    end_idx = content.find("</svg>")
    if end_idx >= 0:
        content = content[:end_idx + 6]
    else:
        content = content.rstrip() + "\n</svg>"

    # 4. 去掉不支持的元素 + 清理引用
    content = _remove_unsupported(content)

    # 5. 常见 XML 修复
    content = content.replace("<br>", "<br/>")
    content = content.replace("<br />", "<br/>")
    # · 只在路径数据里当小数点用（某些编辑器），文本内容里的 · 必须保留（S-14）
    content = re.sub(r'(d="[^"]*)·', lambda m: m.group(1) + '.', content)

    # 6. 针对性解析修复（无损优先，S-07）——每步成功即停
    if not _parses(content):
        # 6a. 裸 & 转义
        escaped = _escape_amp(content)
        if _parses(escaped):
            content = escaped
    if not _parses(content):
        # 6b. 补闭合未闭合的 <text>/<tspan>
        fixed = _close_unclosed_tags(content)
        if _parses(fixed):
            content = fixed

    # 7. 兜底截断（有损）——先转义 &，再从尾向头找最长可解析前缀；每次
    #    尝试都先补齐未闭合标签，否则一个未闭合 <text> 会连累它之前所有
    #    可解析的前缀。（不用 rfind("/>") 贪心捷径：它只会命中最后一个自
    #    闭合标签，把其后所有内容整体丢掉。）
    if not _parses(content):
        content = _escape_amp(content)
    if not _parses(content):
        original_lines = content.count("\n") + 1
        candidate = None
        lines = content.split("\n")
        for i in range(len(lines) - 1, 0, -1):
            trial = _close_unclosed_tags("\n".join(lines[:i]) + "\n</svg>")
            if _parses(trial):
                candidate = trial
                break
        if candidate is not None:
            dropped = original_lines - (candidate.count("\n") + 1)
            print("[fix_svg] WARN: 无法修复的 XML 错误，兜底截断丢弃了 %d 行内容" % dropped,
                  file=sys.stderr)
            content = candidate
            truncated = True
            dropped_lines = dropped

    with open(output_path, "w", encoding="utf-8") as f:
        f.write(content)

    return {
        "status": "ok",
        "file": output_path,
        "chars": len(content),
        "truncated": truncated,
        "dropped_lines": dropped_lines,
    }


if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("Usage: python fix_svg.py <input_svg> <output_svg>", file=sys.stderr)
        sys.exit(1)
    info = fix_svg(sys.argv[1], sys.argv[2])
    print("Fixed: %s (%d chars)" % (info["file"], info["chars"]), file=sys.stderr)
    print(json.dumps(info, ensure_ascii=False))
