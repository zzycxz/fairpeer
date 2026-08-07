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

1. Step 4 init 前，用 `todo_write` 创建所有步骤
2. **每完成一步，必须使用 `complete_step` 提交证据**（系统会自动将该步标记为 completed 并将下一步置为 in_progress，**不要手动用 `todo_write` 去改状态**）
3. 某步失败无法完成时，也使用 `complete_step` 提交结果并在证据里说明原因
4. **最终答案前，确认所有步骤都已通过 `complete_step` 完成**——否则系统会判定任务未完成并阻止输出

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

### Step 0: 提取模板配色 + 背景图（关键！）

检查固定位置的模板文件：

```bash
ls ~/.fairpeer/ppt-template.pptx 2>/dev/null
```

- **文件存在** → 有模板：提取配色 + 背景图
- **文件不存在** → 无模板：用 `template_config.json` 的默认配色

**如果有模板，提取配色 + 背景图**（纯 Python + PIL，不依赖 Office）：

```bash
python3 <skill_dir>/scripts/extract_template_colors.py ~/.fairpeer/ppt-template.pptx <skill_dir>/template_config.json ~/.fairpeer/ppt-template-bg.png
```

第三个参数是背景图输出路径。提取后：
- `template_config.json` 的 `colors` 被更新为模板真实配色
- `~/.fairpeer/ppt-template-bg.png` 是模板的全屏背景图（若有图片背景）

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
| 模板提取 | `dynamic_style.json`（若存在） | 整组 colors 覆盖基线；由 Step 6 生成（仅装了 Office 时） |
| 用户输入 | 自然语言（"用绿色主色"） | 只覆盖明确提及的字段 |

**⚠️ 严禁凭空捏造颜色。严禁凭主题名推断品牌色（如"中国移动"≠ 自己编蓝色）。配色只能从已读的 config 取。**

### Step 4: 写内容大纲

规划每页：页面类型（封面/目录/内容/结尾）、标题、要点（3-6 条）、布局选型（参考 `references/layout_templates.md`）。

**内容必须来自用户主题和素材，不要套固定结构。**

**⚠️ 配色方案必须直接引用 Step 3 读到的 config colors 值，不得自创。**

### Step 5: 初始化项目

```bash
python3 <skill_dir>/scripts/project_manager.py init <project_name> --format ppt169
```

init 创建目录结构，后续文件路径：

| 目录 | 用途 |
|------|------|
| `svg_output/` | 逐页 SVG（`slide_01.svg`…） |
| `notes/` | 演讲者备注 |
| `exports/` | 最终 PPTX |
| `images/` | 图片资源（含模板背景图） |

**init 后把大纲写入 `<project_dir>/design_spec.md`**（完整性校验必需）。

**若有模板背景图，复制到项目 images/**：
```bash
cp ~/.fairpeer/ppt-template-bg.png <project_dir>/images/bg_template.png 2>/dev/null
```

### Step 6: 分析模板（可选，需 Office）

```bash
python3 <skill_dir>/scripts/analyze_template.py ~/.fairpeer/ppt-template.pptx <project_dir>/backgrounds/
```

需 PowerPoint/WPS（COM 自动化）。**若报错（未装 Office/comtypes），将该步标 completed 并继续**——Step 0 的纯 Python 配色提取已足够。此步用 Office 渲染模板截图做更精细的 PIL 配色量化，写入 `<project_dir>/dynamic_style.json`（仅覆盖 colors）。通常可跳过。

### Step 7: 规划页面布局

根据大纲为每页选布局，版式要多样（同一版式不超过总页数 1/5）。对照 `references/layout_templates.md` 选型。

### Step 8: 逐页生成 SVG

每页写入 `<project_dir>/svg_output/slide_01.svg`…。每页 SVG 必须：

1. **背景**（三选一，取决于 Step 0/5 的结果）：
   - **有模板背景图**（`images/bg_template.png` 存在）：第一个元素是 `<image href="images/bg_template.png" x="0" y="0" width="1280" height="720" preserveAspectRatio="xMidYMid slice"/>`——直接铺模板背景图，100% 还原
   - **无模板**：第一个元素是全屏 `<rect width="1280" height="720" fill="#背景色"/>`（`colors.background`）
   - **用户要求重新设计背景**：可用 rect/渐变自创，不引用模板图
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
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir>
```

转换器从**空白 Presentation** 开始，把每页 SVG 转成原生 DrawingML 形状/文字（**可编辑**）。SVG 里的 `<image>` 会被嵌入 PPTX 的 media。`--template` 参数已废弃（no-op），无需传。

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
