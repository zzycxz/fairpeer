---
name: ppt-auto
description: 使用PPT模板生成演示文稿。根据用户主题，通过SVG路径生成专业PPT，输出.pptx文件。
runAs: subagent
effort: high
allowed-tools: bash, read_file, write_file, edit_file, grep, todo_write, web_search, web_fetch
---

# PPT 生成 Skill

根据用户主题生成专业演示文稿，输出 `.pptx` 文件。默认无动画、无过渡（用户明确要求时才加，参数见 `references/animations.md`）。

## 前置条件

需要 **Python 3.10+**。首次使用安装依赖：
- macOS/Linux：`bash <skill_dir>/setup_python.sh`
- Windows：`<skill_dir>\setup_python.bat`

依赖纯 Python（python-pptx/Pillow/lxml），**不需要 Office**。

## todo 管理规则（严格遵守，否则任务无法完成）

1. Step 4 init 前，用 `todo_write` 创建所有步骤
2. **每完成一步立即标 completed**（不要批量更新）
3. 某步失败无法完成时，标 completed 并在结果里说明原因
4. **最终答案前，确认 todo_write 里没有任何 in_progress 项**——否则系统会判定任务未完成并阻止输出

## 输入与输出

- **输入**：主题（如"企业数字化转型"），可选源文档 + 页数/风格要求
- **输出**：`<project_dir>/exports/*.pptx`，每页带演讲者备注

## 路径占位符

- `<skill_dir>`：本 SKILL.md 所在目录
- `<project_dir>`：Step 4 init 创建的工作目录（init 前不存在）

> Windows 上 `python3` 若不存在，换成 `python`。

## 生成路线

默认走**路线 A（SVG 路线）**，除非用户要求"直接模板填充"才走路线 B。

---

## 路线 A：SVG 生成（15 步）

### Step 0: 确定模板（关键！）

扫描 `<skill_dir>/templates/` 目录下的 `.pptx` 文件（排除 `default.pptx`）：

```bash
ls <skill_dir>/templates/*.pptx
```

- **有且仅有 1 个模板** → 直接使用，记录路径
- **有多个模板** → 询问用户"检测到以下模板：xxx、yyy，使用哪个？"
- **没有模板**（只有 default.pptx）→ 无模板模式：SVG 自己画背景色

**如果有模板，先提取配色**：

```bash
python3 <skill_dir>/scripts/extract_template_colors.py <template.pptx> <skill_dir>/template_config.json
```

这会从模板提取颜色方案，更新 `template_config.json`。

**⚠️ SVG 背景规则（极其重要）**：

- **有模板时**：禁止画全屏不透明背景矩形（会完全遮住模板背景）。卡片、内容块可以用半透明色（如 `rgba(0,0,0,0.3)`）做局部背景，让模板背景透出来同时增强内容可读性。就像在 PPT 里给文字加一个半透明蒙版，而不是盖一层纯色。
- **无模板时**：画一个全屏 `<rect width="1280" height="720" fill="#背景色"/>` 作为背景。

这条规则直接影响 Step 5-8 的 SVG 生成。在生成 SVG 前必须确定是否有模板。

### Step 1: 提取源文档（如有）

```bash
python3 <skill_dir>/scripts/extract_content.py <file.pdf> source_content.json
```
支持 PDF/DOCX/XLSX/CSV/TXT/MD。init 后挪到 `<project_dir>/sources/`。

### Step 2: 联网搜索（如需要）

```
web_search(query="<主题相关关键词>")
```

### Step 3: 写内容大纲

规划每页：页面类型（封面/目录/内容/结尾）、标题、要点（3-6 条）、布局选型（参考 `references/layout_templates.md`）。

**内容必须来自用户主题和素材，不要套固定结构。**

### Step 4: 初始化项目

```bash
python3 <skill_dir>/scripts/project_manager.py init <project_name> --format ppt169
```

init 创建目录结构，后续文件路径：

| 目录 | 用途 |
|------|------|
| `svg_output/` | 逐页 SVG（`slide_01.svg`…） |
| `notes/` | 演讲者备注 |
| `exports/` | 最终 PPTX |
| `backgrounds/` | 模板背景图 |

**init 后立刻把大纲写入 `<project_dir>/design_spec.md`**（完整性校验必需）。

### Step 5: 读取配置（只读一次）

```bash
read_file <skill_dir>/template_config.json
```

`template_config.json` 是**配色、字号、布局规则的唯一事实源**。check_svg.py 按它校验。牢记其中约束，贯穿后续所有 SVG 生成。

### 颜色规则（唯一来源）

| 层级 | 来源 | 说明 |
|------|------|------|
| 基线 | `template_config.json` | 始终有效，提供全部颜色字段 |
| 模板提取 | `dynamic_style.json`（若存在） | 只覆盖 primary/secondary/accent，其余沿用基线 |
| 用户输入 | 自然语言（"用绿色主色"） | 只覆盖明确提及的字段 |

**严禁在三层之外凭空捏造颜色。**

### Step 6: 分析模板

```bash
python3 <skill_dir>/scripts/analyze_template.py <template.pptx> <project_dir>/backgrounds/
```

需 PowerPoint/WPS（COM）。未安装则跳过，SVG 用纯色背景代替，告知用户。

### Step 7: 规划页面布局

根据大纲为每页选布局，版式要多样（同一版式不超过总页数 1/5）。对照 `references/layout_templates.md` 选型。

### Step 8: 逐页生成 SVG

每页写入 `<project_dir>/svg_output/slide_01.svg`…。每页 SVG 必须：

1. 背景图作为第一个 `<image>` 元素
2. viewBox 固定 `"0 0 1280 720"`
3. 配色严格读 config，不得凭空捏造
4. 强调色只用于关键数据/按钮/警告（面积 ≤5%）
5. 文字与背景高对比度，避免压字/溢出
6. 同类元素对齐、间距均匀、同层级字号一致（标题 26px/卡片 18px/正文 14px）
7. 禁止 filter/pattern/mask/foreignObject
8. 纯 SVG 代码，不要 markdown 代码块
9. 封面只叠加标题副标题；结尾页只加致谢文字

### Step 9: 修复 SVG XML

```bash
python3 <skill_dir>/scripts/fix_svg.py <project_dir>/svg_output/slide_01.svg <project_dir>/svg_output/slide_01.svg
# 对每页重复
```

### Step 10: 质量检查（根据模式）

```bash
python3 <skill_dir>/scripts/check_svg.py <project_dir>/svg_output/slide_01.svg --config <skill_dir>/template_config.json --mode <fast|validate>
```

- `fast`（默认）：跳过本步
- `validate`：逐页检查，有问题用 edit_file 修后重查（最多 3 轮）

### Step 11: 演讲者备注

```bash
python3 <skill_dir>/scripts/save_notes.py <project_dir> <notes_json>
```

每页 2-5 句，补充解释而非重复页面文字。

### Step 12: 转换 PPTX

```bash
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir> --template <skill_dir>/templates/<模板名>.pptx
```

**必须在 Step 0 确定了模板时传 `--template` 参数**，否则输出为白底，模板背景丢失。模板路径就是 Step 0 扫描到的文件。

纯 Python（python-pptx），**不需要 Office**。默认无动画无过渡。

> 如需动画/过渡/旁白，参数见 `references/animations.md`。

### Step 13: 视觉检查（仅 validate 模式）

**13a** 导出截图（需 PowerPoint/WPS）：
```bash
python3 <skill_dir>/scripts/export_previews.py <project_dir>
```

**13b** 逐页检查：对每页截图用 `image_understand` 检查文字压边/溢出/对齐/配色/可读性。

**13c** 修正：发现问题用 `edit_file` 改单页 SVG → 修复 → 转换 → 重查。

### Step 14: 导出 PDF（可选）

```bash
python3 <skill_dir>/scripts/export_pdf.py <input.pptx> <output.pdf>
```

需 PowerPoint/WPS，未安装则跳过。

### Step 15: 用户反馈处理

- 只改有问题的页（edit_file 改单页 SVG）
- 修正后确认，最多 3 轮

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
| project_manager.py init | 纯 Python |
| fix_svg.py | 纯 Python |
| check_svg.py | 纯 Python |
| svg_to_pptx.py | python-pptx（纯 Python） |
| save_notes.py | 纯 Python |
| analyze_template.py | **需 PowerPoint/WPS** |
| export_previews.py | **需 PowerPoint/WPS** |
| export_pdf.py | **需 PowerPoint/WPS** |

## 资源

| 资源 | 用途 |
|------|------|
| templates/charts/ | 图表/流程图（含选型规则 charts_index.json） |
| templates/icons/ | 5 个图标库 |
| references/layout_templates.md | 布局坐标规格 |
| references/animations.md | 动画/过渡/旁白参数 |
| references/error_handling.md | 排错参考 |
