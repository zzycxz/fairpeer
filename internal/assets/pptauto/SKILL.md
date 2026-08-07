---
name: ppt-auto
description: 使用PPT模板生成演示文稿。根据用户主题，通过SVG路径生成专业PPT，输出.pptx文件。
runAs: subagent
effort: high
allowed-tools: bash, read_file, write_file, edit_file, grep, todo_write, complete_step, web_search, web_fetch
---

# PPT 生成 Skill

根据用户主题生成专业演示文稿，输出 `.pptx` 文件。默认无动画、无过渡（用户明确要求时才加，参数见 `references/animations.md`）。

## 前置条件

需要 **Python 3.10+**。首次使用安装依赖：
- macOS/Linux：`bash <skill_dir>/setup_python.sh`
- Windows：`<skill_dir>\setup_python.bat`

依赖纯 Python（python-pptx/Pillow/lxml），**不需要 Office**。

## todo 管理规则（严格遵守，否则任务无法完成）

1. Step 0 开始前，用 `todo_write` 创建所有步骤
2. **每完成一步，必须使用 `complete_step` 提交证据**（系统会自动将该步标记为 completed 并将下一步置为 in_progress，**不要手动用 `todo_write` 去改状态**）
3. 某步失败无法完成时，也使用 `complete_step` 提交结果并在证据里说明原因
4. **最终答案前，确认所有步骤都已通过 `complete_step` 完成**——否则系统会判定任务未完成并阻止输出

### complete_step 证据格式（严格遵守，否则报错）

`evidence` 必须是**数组**，每项含 `kind`（verification/diff/files/manual）+ `summary`，以及可选的 `command`/`paths`。

**关键：`command` 字段必须和你实际跑的 bash 命令完全一致**（系统会拿它去匹配 bash 执行记录）。不一致会报 "no matching successful bash receipt"。如果拿不准命令原文，用 `kind: "manual"` 代替。

正确示例：
```json
{
  "step": "Step 0: 提取模板配色",
  "result": "已提取模板配色并更新 template_config.json",
  "evidence": [
    {"kind": "verification", "summary": "extract_template_colors 成功，background=#EDF8FC", "command": "python \"C:\\Users\\13852\\.fairpeer\\skills\\ppt-auto\\scripts\\extract_template_colors.py\" \"C:\\Users\\13852\\.fairpeer\\ppt-template.pptx\" \"C:\\Users\\13852\\.fairpeer\\skills\\ppt-auto\\template_config.json\""}
  ]
}
```

简化方案（拿不准命令原文时用 manual）：
```json
{
  "step": "Step 5: 写大纲",
  "result": "大纲已写入 design_spec.md",
  "evidence": [
    {"kind": "files", "summary": "design_spec.md 已创建", "paths": ["<project_dir>/design_spec.md"]}
  ]
}
```

## 输入与输出

- **输入**：主题（如"企业数字化转型"），可选源文档 + 页数/风格要求
- **输出**：`<project_dir>/exports/*.pptx`

## 路径占位符

- `<skill_dir>`：本 SKILL.md 所在目录
- `<project_dir>`：Step 4 init 创建的工作目录（init 前不存在）

> Windows 上 `python3` 若不存在，换成 `python`。

## 生成路线

默认走**路线 A（SVG 路线）**，除非用户要求"直接模板填充"才走路线 B。

---

## 路线 A：SVG 生成（8 步）

### Step 0: 提取模板配色（关键！）

检查固定位置的模板文件：

```bash
ls ~/.fairpeer/ppt-template.pptx 2>/dev/null
```

- **文件存在** → 有模板：提取配色（用于内容着色）。模板的背景/装饰/logo 由 PPTX 继承自动透出，SVG 不需要画背景
- **文件不存在** → 无模板：SVG 自己画背景色

**如果有模板，提取配色**（纯 Python + PIL，不依赖 Office）：

```bash
python3 <skill_dir>/scripts/extract_template_colors.py ~/.fairpeer/ppt-template.pptx <skill_dir>/template_config.json
```

提取后 `template_config.json` 被更新：
- `colors`：模板真实配色（background/accent/text 等），用于内容着色
- `fonts.family`：模板字体 + 跨平台降级链（如 `"等线", "Microsoft YaHei", "PingFang SC", sans-serif`），SVG 文字用此字体

**视觉配色（可选，优先级最高）**：若 `~/.fairpeer/ppt-template-style.json` 存在（由设置面板选模板时后台自动生成），其配色优先于 `extract_template_colors.py` 的结果。

### Step 1: 提取源文档（如有）

```bash
python3 <skill_dir>/scripts/extract_content.py <file.pdf> source_content.json
```
支持 PDF/DOCX/XLSX/CSV/TXT/MD。init 后挪到 `<project_dir>/sources/`。

### Step 2: 联网搜索（如需要）

```
web_search(query="<主题相关关键词>")
```

### Step 3: 读取配置（关键！必须在写大纲之前读）

```bash
read_file <skill_dir>/template_config.json
```

`template_config.json` 是**配色、字号、布局规则的唯一事实源**。牢记其中的 `colors`（background/accent/text 等）——后续写大纲、生成 SVG 时**只能用这些颜色，禁止凭主题名推断品牌色**。

若有 `~/.fairpeer/ppt-template-style.json`（视觉提取），也读它——其配色优先级最高。

### 颜色规则（唯一来源）

| 层级 | 来源 | 说明 |
|------|------|------|
| 基线 | `template_config.json` | 始终有效，提供全部颜色字段 |
| 视觉提取（最高） | `~/.fairpeer/ppt-template-style.json`（若存在） | 设置面板选模板时由视觉模型自动生成；覆盖 background/accent/text 等关键字段 |
| 用户输入 | 自然语言（"用绿色主色"） | 只覆盖明确提及的字段 |

**⚠️ 严禁凭空捏造颜色。严禁凭主题名推断品牌色（如"中国移动"≠ 自己编蓝色）。配色只能从已读的 config 取。**

### Step 4: 初始化项目

```bash
python3 <skill_dir>/scripts/project_manager.py init <project_name> --format ppt169
```

init 创建目录结构：

| 目录 | 用途 |
|------|------|
| `svg_output/` | 逐页 SVG（`slide_01.svg`…） |
| `notes/` | 演讲者备注（用户要求时才写） |
| `exports/` | 最终 PPTX |
| `images/` | 图片资源 |

### Step 5: 写大纲 + design_spec

规划每页：页面类型（封面/目录/内容/结尾）、标题、要点（3-6 条）、布局选型（参考 `references/layout_templates.md`）。

**内容必须来自用户主题和素材，不要套固定结构。**

**⚠️ 配色方案必须直接引用 Step 3 读到的 config colors 值，不得自创。**

把大纲写入 `<project_dir>/design_spec.md`（完整性校验必需）。

### Step 6: 逐页生成 SVG

每页写入 `<project_dir>/svg_output/slide_01.svg`…。

每页 SVG 必须：

1. **背景**（取决于 Step 0 是否有模板）：
   - **有模板**（`~/.fairpeer/ppt-template.pptx` 存在）：**不要画任何全屏背景**（不要 rect、不要 image）。模板的背景/渐变/装饰/logo 会通过 PPTX layout 继承自动透出。SVG 只画内容（卡片、文字、图标）。画全屏 rect 会盖住模板背景！check_svg.py 会报错。
   - **无模板**：每页第一个元素是全屏 `<rect width="1280" height="720" fill="#背景色"/>`（`colors.background`）。
2. viewBox 固定 `"0 0 1280 720"`
3. 配色严格读 config，不得凭空捏造
4. **卡片用半透明背景**（`rgba()` 格式），让模板背景透出来——**不要用纯色不透明填充**（如 `#F5F5F5`），那样会像色块贴上去。深浅自动适配：浅色背景用 `rgba(255,255,255,0.7~0.85)` 或主题色的 `rgba(R,G,B,0.08~0.12)`；深色背景用 `rgba(255,255,255,0.08~0.15)`。卡片可加细描边（`stroke` 主题色 + `stroke-width="1"`）增强层次感。`colors.card_bg` 已给出适配当前背景的半透明值，直接用。
5. 强调色只用于关键数据/按钮/警告（面积 ≤5%）
6. 文字与背景高对比度，避免压字/溢出
7. 同类元素对齐、间距均匀、同层级字号一致（标题 26px/卡片 18px/正文 14px）
8. 禁止 filter/pattern/mask/foreignObject
9. 纯 SVG 代码，不要 markdown 代码块
10. 封面只叠加标题副标题；结尾页只加致谢文字

**每页生成后，自动跑修复 + 检查**：
```bash
# 修复常见 XML 错误（markdown 标记、未闭合标签等）
python3 <skill_dir>/scripts/fix_svg.py <project_dir>/svg_output/slide_01.svg <project_dir>/svg_output/slide_01.svg
# 质量检查（默认 fast 模式：XML/背景/禁止元素——快）
python3 <skill_dir>/scripts/check_svg.py <project_dir>/svg_output/slide_01.svg --config <skill_dir>/template_config.json --mode fast
```

**模式选择**（用户说"校验模式"时才用 validate）：
- `fast`（默认）：只跑基础检查（XML 格式、背景规则、禁止元素）——快，出稿用
- `validate`：全量检查（额外检查密度/溢出/重叠/覆盖/对齐）——慢，最终交付用。validate 的 WARN 是建议性的，不阻止流程

check_svg 报 ERROR（exit code 2）时必须修正后重查。WARN 不阻止流程。

> **演讲者备注**：用户明确要求时才做——在 `notes/slide_NN.md` 写每页 2-5 句备注，svg_to_pptx 会自动读取嵌入。默认不生成。

### Step 7: 转换 PPTX

```bash
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir>
```

转换器会**自动检测** `~/.fairpeer/ppt-template.pptx`：有模板时打开模板、清空已有 slides、用模板的 layout 添加新 slide（模板的背景/master/装饰通过继承保留），把每页 SVG 转成原生 DrawingML 形状/文字（**可编辑**）叠加在模板背景上。无模板时用空白 Presentation。无需手动传参。

若 `notes/` 目录有备注文件（用户要求时才生成），转换器会自动读取嵌入。

纯 Python（python-pptx），**不需要 Office**。默认无动画无过渡。

> 如需动画/过渡/旁白，参数见 `references/animations.md`。

---

**生成完成后**：把 `<project_dir>/exports/*.pptx` 交付给用户。如用户要求修改，用 edit_file 改对应页 SVG → 重跑 Step 7。

> 导出 PDF 需额外安装 PowerPoint/WPS，用户明确要求时才做：`python3 <skill_dir>/scripts/export_pdf.py <input.pptx> <output.pdf>`

---

## 路线 B：模板填充（不经 SVG）

用户明确要求"直接模板填充"时用 `template_fill_pptx.py`。与路线 A 二选一，不混用。详细参数见脚本 `--help`。

---

## 用户输入覆盖

| 用户说 | 覆盖 |
|--------|------|
| "用XX模板" | template |
| "做10页" | pages |
| "深色风格" / "用绿色主色" | style / colors |
| "快速模式" / "校验模式" | mode = fast / validate |
| "要动画" | 参考 references/animations.md |

---

## 脚本一览

| 脚本 | 依赖 |
|------|------|
| extract_content.py | 纯 Python |
| extract_template_colors.py | 纯 Python（需 Pillow） |
| project_manager.py init | 纯 Python |
| fix_svg.py | 纯 Python |
| check_svg.py | 纯 Python |
| svg_to_pptx.py | python-pptx（纯 Python） |
| template_fill_pptx.py | python-pptx（纯 Python） |
| export_pdf.py | **需 PowerPoint/WPS** |

## 资源

| 资源 | 用途 |
|------|------|
| templates/charts/ | 图表/流程图（含选型规则 charts_index.json） |
| templates/icons/ | 5 个图标库 |
| references/layout_templates.md | 布局坐标规格 |
| references/animations.md | 动画/过渡/旁白参数 |
| references/error_handling.md | 排错参考 |
