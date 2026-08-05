---
name: ppt-auto
description: 使用PPT模板生成演示文稿。根据用户主题，通过SVG路径或模板填充方式生成专业PPT。支持动画、过渡效果、图表(SVG)。
runAs: subagent
effort: high
allowed-tools: bash, read_file, write_file, edit_file, grep, todo_write, image_understand, image_generate, web_search, web_fetch
---

# PPT 生成 Skill

根据用户主题和PPT模板，生成专业演示文稿，输出为 `.pptx` 文件。

## ⚠️ 前置条件：Python 3.10+

本 skill 需要系统已安装 **Python 3.10+**（不再携带嵌入式运行时，跨平台通用）。

**首次使用前，请安装依赖：**

- macOS / Linux：`bash <skill_dir>/setup_python.sh`
- Windows：双击或运行 `<skill_dir>\setup_python.bat`

脚本会执行 `pip install -r <skill_dir>/requirements.txt` 安装 python-pptx、Pillow、lxml 等依赖（纯 Python，无需 Office）。

> 若系统中没有 `python3` 命令，请先安装 Python 3.10+：
> - macOS：`brew install python`
> - Ubuntu/Debian：`sudo apt install python3 python3-pip`
> - Windows：从 https://www.python.org/downloads/ 安装并勾选 "Add to PATH"

## 你会收到什么 / 要产出什么

- **输入**：一个主题（如"企业数字化转型方案"），可选附带源文档（PDF/DOCX 等）和页数/风格要求。
- **输出**：`<project_dir>/exports/*.pptx`（+ 可选 PDF），每页带演讲者备注。
- **全程约 12–15 步**，建议用 `todo_write` 跟踪进度（尤其多页 PPT）。

## 路径占位符

下文命令中 `<skill_dir>` 和 `<project_dir>` 是占位符，调用时替换为实际绝对路径：

- `<skill_dir>`：本 skill 的安装目录（即 `SKILL.md` 所在目录）。
- `<project_dir>`：Step 4 `init` 创建的工作目录（`init` 会打印它的绝对路径）。后续所有文件（SVG、备注、导出）都在它下面。**在 Step 4 init 之前，`<project_dir>` 不存在**——需要它的步骤都在 init 之后。

> **Windows 提示**：下文统一用 `python3`。若 Windows 上 `python3` 命令不存在（python.org 的安装包只提供 `python.exe`），把 `python3` 换成 `python` 即可。

## 功能降级路线图（环境不满足时的处理策略）

任何单项功能缺失，主流程仍继续；降级时必须明确告知用户跳过了哪项功能及原因。

| 所需环境 | 涉及步骤 | 降级策略 |
|---------|---------|----------|
| PowerPoint / WPS（COM 接口） | Step 6 背景图导出、Step 13a 截图、Step 14 PDF 导出 | 跳过该步骤；Step 6 失败时 SVG 用纯色背景(`colors.background`)代替模板背景，并告知用户 |
| Pillow（Python 库） | Step 6 颜色提取 → `dynamic_style.json` | 跳过颜色提取，`dynamic_style.json` 不生成，继续沿用 `template_config.json` |
| 网络连接 | Step 2 联网搜索 | 跳过搜索，仅依赖用户提供的源文档和主题 |

## 两条生成路线

**默认走路线 A（SVG 路线）**，除非用户明确要求"直接用模板填充/不改版式"才走路线 B：

- **路线 A（SVG 路线，Step 1–15）**：逐页生成 SVG → 转成 PPTX。布局完全自由，推荐用于从零设计。`svg_to_pptx.py` 用纯 Python（python-pptx），**不需要安装 Office**。
- **路线 B（模板填充，见末节）**：直接在原生 PPTX 模板的占位符里填内容（`template_fill_pptx.py`）。保留模板原有版式，适合"模板已经很完美，只想换文字/数据"。

---

## 路线 A：SVG 生成工作流

### Step 1: 提取源内容（如有文档）

如果用户提供了文档，先提取内容。注意此时尚未 init 项目，输出路径用一个临时位置（Step 4 init 后再移入 `<project_dir>/sources/`）：

```bash
python3 <skill_dir>/scripts/extract_content.py <file1.pdf> <file2.docx> source_content.json
```

支持格式：PDF、DOCX、XLSX、CSV、TXT、MD。init 完成后把产物挪到 `<project_dir>/sources/`。

### Step 2: 联网搜索（如需要）

如果用户主题需要最新数据：

```bash
web_search(query="<根据用户主题组织搜索关键词，如：XX行业 2024 最新趋势/数据/政策>")
```

### Step 3: 写内容大纲（构思 + 落盘）

**在生成 SVG 之前，先规划好每页的内容大纲。这一步分两段：先构思，init 项目后落盘成文件。**

根据主题、源文档、搜索结果，规划每页的内容大纲。**大纲内容必须来自用户给的具体主题和素材，不要套固定结构。**

每页大纲包含：
- 页面类型（封面/目录/内容/结尾）
- 页面标题
- 内容要点（3-6 条）
- 布局建议（表格/时间线/卡片/对比等）——参考 `references/layout_templates.md` 的布局选型

**构思完成后，在 Step 4 init 项目之后，把大纲写入 `<project_dir>/design_spec.md`**（项目根目录）。这一步**必须落盘**——项目的验证逻辑（`project_manager.py validate`）会检查该文件是否存在，缺失会警告 "Missing design specification file"；且生成中途你要反复回看大纲保证每页贴合规划，不落盘容易跑偏。

### Step 4: 初始化项目

```bash
python3 <skill_dir>/scripts/project_manager.py init <project_name> --format ppt169
```

init 会创建标准项目目录，后续所有步骤的文件都落到对应子目录，**路径不要写错**：

| 目录 | 用途 | 由哪一步写入 |
|------|------|--------------|
| `svg_output/` | 逐页 SVG 源文件（`slide_01.svg`…） | Step 8 生成 SVG |
| `notes/` | 演讲者备注 | Step 11 |
| `exports/` | 最终 PPTX / PDF | Step 12、Step 14 |
| `backgrounds/` | 模板导出的背景图 | Step 6 |
| `previews/` | PPTX 截图（仅 validate 模式） | Step 13a |
| `sources/` | 源文档 | Step 1（可选） |

> `svg_to_pptx.py` 默认从 `svg_output/` 读 SVG、从 `notes/` 读备注、输出到 `exports/`。若 SVG 没写到 `svg_output/`，转换会读不到。

**init 完成后，立刻把 Step 3 构思的大纲写入 `<project_dir>/design_spec.md`**（项目根目录的 markdown 文件）。这是后续生成 SVG 时的内容蓝图，也是项目完整性校验的必需文件。

### Step 5: 读取配置（单一事实源，只读一次）

**在任务开始时，读取一次 `template_config.json`，并把其中约束牢记在心、贯穿后续所有 SVG 生成：**

```bash
read_file <skill_dir>/template_config.json
```

**`template_config.json` 是配色、字号、布局规则、内容密度的唯一事实源。** 生成每一页 SVG 时严格遵守其中字段——`check_svg.py`（Step 10）会按这份配置检查，自行发挥的配色会通不过。关键字段：`colors`（hex 值）、`fonts`、`rules`（forbidden 元素/字数/对齐/留白/`background_rules`/`content_density`）、`default_prompt`。

> 下方 Step 7/8 会把这些约束展开成生成时可直接套用的具体值，方便逐页生成 SVG 时参考——但若与 config 有出入，**以 `template_config.json` 为准**（config 是检查器实际校验的依据）。

### 颜色三层优先级（从低到高，精确合并，非整体替换）

| 层级 | 来源 | 说明 |
|------|------|------|
| 1（基线） | `template_config.json` | 始终有效，提供完整颜色基础（含 `card_bg`、`text_muted`、`line` 等全部字段） |
| 2（模板提取） | `dynamic_style.json`（若存在） | **只覆盖**其中实际存在的字段（目前为 `primary`、`secondary`、`accent`）；未出现的字段继续沿用层级 1 |
| 3（用户输入） | 用户自然语言（如"用绿色主色"） | 只覆盖用户明确提及的字段；其余继续沿用层级 2 合并结果 |

> **关键规则**：高层级只覆盖它实际指定的字段，不影响未提及的字段。最终合并后的完整颜色表才是生成 SVG 时的唯一参照。**严禁在三层之外凭空捏造任何颜色。**

### Step 6: 分析模板（导出背景图及样式）

```bash
python3 <skill_dir>/scripts/analyze_template.py <template.pptx> <project_dir>/backgrounds/
```

> ⚠️ 此脚本通过 COM 调用 **PowerPoint 或 WPS** 导出每页背景图，并**自动分析背景像素**提取出 `<project_dir>/dynamic_style.json` (包含模板的主色调等约束)。若机器上两者都未安装，会报错——此时回退为不使用模板背景（SVG 用纯白底 + config 配色），并告知用户"未检测到 Office/WPS，已用纯色背景代替模板背景"。

### Step 7: 规划页面布局

根据 Step 3 的大纲，为每页选定布局类型。版式要多样，同一版式不超过总页数的 1/5。

**布局选型参考 `references/layout_templates.md`**——它给出每种布局的坐标规格（封面、2×2卡片网格、时间线、左右对比、表格、仪表盘等）。规划时就对照它选，不要等到生成时才找。

模板使用原则（来自 `template_config.json` 的 `template_usage`）：背景保持原样、保留 logo、遵循配置配色、布局可调整但须商务克制。

### Step 8: 逐页生成 SVG

逐页生成 SVG，每页写入 `<project_dir>/svg_output/slide_01.svg`、`slide_02.svg`…（文件名按页序编号，`svg_to_pptx` 按此顺序合成）。每页 SVG 都必须：

1. 背景图作为第一个 `<image>` 元素
2. viewBox 固定为 `"0 0 1280 720"`
3. 字体：读取并遵循配置中的 `fonts.family`
4. 配色：**严格读取 `template_config.json`（或 `dynamic_style.json`，若存在）中的 `colors` 字段**。不得凭空捏造颜色。
5. 文字颜色：遵循配置要求；注意文字颜色需与背景色形成高对比度，不要在深色底上放大量深色正文，也不要在浅色底上放浅色正文。
6. 框体、图标、装饰线：优先使用配置中的 primary 和 secondary 颜色。强调色 (accent) **只用于**关键数据、按钮、警告标记。
7. 颜色数量限制：**严格读取配置中的 `rules.color_limits.max_per_page`**。不得超过限制数量（不含黑白灰），以防画面花哨。整体配色数量不得超过配置中提取的颜色。
8. 布局避让：文字必须避开模板背景上的固有装饰图形，绝对**不能压字**或产生位置冲突。
9. 同类元素必须对齐，间距均匀，不要参差不齐。
10. 同一层级字号一致：遵循配置要求（默认标题 26px，卡片 18px，正文 14px）。
11. **禁止** `filter`/`feDropShadow`/`pattern`/`mask`/`foreignObject`
12. 背景图保持原样，不加遮罩、渐变叠加或半透明层。
13. 输出纯 SVG 代码，不要 markdown 代码块。

> **动态样式合并说明（精确合并，非整体替换）**：如果在 Step 6 生成了 `<project_dir>/dynamic_style.json`，则：
> - **只覆盖** `dynamic_style.json` 中实际存在的字段（目前是 `colors.primary`、`colors.secondary`、`colors.accent`）。
> - `dynamic_style.json` 中未出现的字段（如 `card_bg`、`text_muted`、`line`、`background`），**继续沿用 `template_config.json` 的值，绝不得自行填充或忽略**。
> - 合并后的完整颜色表才是生成 SVG 时的唯一依据。**严禁在此之外臆造任何颜色**——包括看起来合理的颜色也不行。

**通用排版补充约束**：

- **内容充实**：内容页尽量填满内容区域（y=90~620），但必须保证留白和对齐。**封面和结尾页不受此约束**。
- **色彩克制**：强调色视觉面积不得超过 5%，不得用于整块卡片背景、整页标题底。
- **背景避让**：如果背景图已经有强烈的图案，文字区域应使用纯色的底板隔开，以确保文字可读性。
- 封面只叠加标题和副标题；结尾页只叠加致谢文字，不加卡片/图表/色块。

**视觉元素来源：**

| 需求 | 方式 |
|---|---|
| 封面主视觉图 / 内容页配图 | `image_generate` 工具 |
| 图表/流程图 | 引用 `templates/charts/` 模板（见下方资源；`charts_index.json` 含选型规则） |
| 图标 | 引用 `templates/icons/` 库 |
| 装饰元素 | 直接写 SVG 代码 |

### Step 9: 修复 SVG（XML 合法性）

`fix_svg.py` 处理**单个文件**，对 `svg_output/` 下每一页都跑一次（输入和输出可以是同一个文件，就地修复）：

```bash
python3 <skill_dir>/scripts/fix_svg.py <project_dir>/svg_output/slide_01.svg <project_dir>/svg_output/slide_01.svg
# 对 slide_02.svg、slide_03.svg … 重复
```

### Step 10: 质量检查（根据模式决定是否运行）

`check_svg.py` 同样是**单文件**，对每页跑一次：

```bash
python3 <skill_dir>/scripts/check_svg.py <project_dir>/svg_output/slide_01.svg --config <skill_dir>/template_config.json --mode <fast|validate>
# 对每页重复
```

模式由用户在设置页选择（`template_config.json` 的 `mode` 字段）：**`fast`（默认）**跳过本步直接进 Step 11；**`validate`**逐页检查，有问题的页用 `edit_file` 改 SVG 后重查，最多 3 轮。

### Step 11: 生成演讲者备注

```bash
python3 <skill_dir>/scripts/save_notes.py <project_dir> <notes_json>
```

备注规则：
- 每页 2-5 句话，是演讲者会说的话
- 不要重复页面上的文字，而是解释和补充
- 开头可以用过渡句，结尾可以用总结句

### Step 12: 转换 PPTX

```bash
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir>
```

此步用 python-pptx（纯 Python），**不需要 Office**。

### Step 13: 视觉质量检查（仅 validate 模式）

**仅在 `mode=validate` 时执行。快速模式跳过。**

**Step 13a: 导出截图**（需要 PowerPoint/WPS）

```bash
python3 <skill_dir>/scripts/export_previews.py <project_dir>
```

> ⚠️ 此脚本通过 COM 调用 PowerPoint/WPS 截图。未安装则跳过本步并告知用户。

**Step 13b: 逐页检查**

对每页截图调用 `image_understand`：

```
image_understand(
  image="<project_dir>/previews/slide_03.png",
  prompt="请逐项严格检查这个PPT页面，每项只能回答 是 / 否 / 看不清：
1. 文字是否压住了背景的边框或装饰线？
2. 文字是否超出了其所在框体的边界？
3. 同类元素（如多个卡片）是否对齐整齐（左对齐或居中对齐）？
4. 整体颜色数量是否超过4种（不含黑白灰）？
5. 文字与背景的对比是否清晰可读（不存在深色字压深色底、浅色字压浅色底）？
6. 如果这是结尾页，是否只有致谢文字（无多余卡片/图表/色块）？不是结尾页则回答 不适用。

⚠️ 客观性规则（必须遵守）：
- 如果图片模糊或无法确认某项，必须回答「看不清」，禁止猜测或乐观推断。
- 只有全部适用项均明确回答「是」，总结才能写：通过。
- 任何一项回答「否」或「看不清」，总结必须写：需改进，并逐项列明问题。
- 严禁在未能确认的情况下给出通过结论。"
)
```

**Step 13c: 修正问题**

如果发现问题：记录问题页面 → 用 `edit_file` 只改该页 SVG（不要整体重写）→ 修复 + 检查 + 转换 → 再次检查，直到通过。

### Step 14: 导出 PDF（可选，需要 PowerPoint/WPS）

```bash
python3 <skill_dir>/scripts/export_pdf.py <input.pptx> <output.pdf>
```

> ⚠️ 此脚本通过 COM 调用 PowerPoint/WPS。未安装则跳过并告知用户。

### Step 15: 用户反馈处理

- **解析反馈**：如"第3页太空"→增内容密度；"换布局"→换布局类型重生成该页；"颜色不好看"→调整（仍须在 config 配色范围内）
- **只改有问题的页**：用 `edit_file` 改单页 SVG，其他页保持不变
- **确认**：修正后询问"已修正第X页，您看看是否满意？"，最多 3 轮

### 动画、过渡与旁白（`svg_to_pptx.py` 进阶参数）

`svg_to_pptx.py` 支持页面过渡、元素入场动画和演讲旁白，全部通过命令行参数控制。下面列出与默认 Step 12 转换命令搭配的进阶开关。

> ⚠️ **动画/过渡 XML 写入依赖可选的 `pptx_animations` 模块**。脚本在缺失该模块时会优雅降级（`try/except ImportError`），此时下列参数仍会被校验，但生成的 PPTX **不会包含动画/过渡 XML**。若发现参数已传但 PPT 里没有动效，即属此情况。安装该模块后即可写入完整动画/过渡。

#### 过渡效果（`-t/--transition`，页面切换）

| 取值 | 效果 |
|------|------|
| `fade` | 淡入淡出（默认） |
| `push` | 推入 |
| `wipe` | 擦除 |
| `split` | 分割 |
| `strips` | 条纹 |
| `cover` | 覆盖 |
| `random` | 随机 |
| `none` | 无过渡 |

配套：`--transition-duration 0.5`（过渡时长，秒，默认 0.5）。

#### 元素入场动画（`-a/--animation`，仅 native shapes 模式）

> 默认是 `none`（**动画默认关闭**）——设计上避免"AI 生成的演示文稿里元素自动逐个弹入"这种刻板印象。需要时显式开启。

| 取值 | 效果 |
|------|------|
| `fade` | 淡入 |
| `fly` | 飞入 |
| `zoom` | 缩放 |
| `appear` | 出现 |
| `auto` | 按 SVG group id 自动映射（chart→wipe，card-/step-/pillar-→fly，title/takeaway→fade；图片类 id 在 zoom/dissolve/circle/box/diamond/wheel 中轮换；未匹配 id 轮换 fade/wipe/fly/zoom） |
| `mixed` | 轮换 legacy 16 效果池（按 group 顺序） |
| `random` | 从 legacy 效果池随机采样 |
| `none` | 无元素动画（默认） |

动画参数：
- `--animation-duration 0.4` — 单元素入场时长（秒，默认 0.4）
- `--animation-trigger <mode>` — 开始方式，对应 PowerPoint 的 Start 下拉：
  - `on-click` — 每点一次进入一个元素
  - `with-previous` — 所有元素随幻灯片进入同时开始
  - `after-previous` — **默认**，上一个之后级联进入；间隔由 `--animation-stagger` 控制
- `--animation-stagger 0.5` — `after-previous` 模式下元素间延迟（秒，默认 0.5；其他模式忽略）
- `--animation-config <path>` — 每页/每对象的精细动画覆盖配置，默认读取 `<project>/animations.json`（存在时）

#### 自动翻页（`--auto-advance`）

- `--auto-advance 3.0` — 幻灯片自动翻页间隔（秒）；默认手动翻页。配合旁白时长自动设置时尤其有用。

#### 旁白 / 录音（narration）

PPT-auto 支持把演讲录音嵌入幻灯片，用于"录制式"演示或视频导出：

- `--recorded-narration <dir>` — **完整录制模式**：目录内每页幻灯片需有一个 m4a/mp3/wav 文件（按 SVG 文件名或页码匹配）。会：
  - 保留演讲备注（启用时）
  - 嵌入每页音频
  - 按音频时长设置每页自动翻页时间，便于视频导出选"录制时间和旁白"
  - **拒绝 on-click 动画**（与自动翻页冲突）；改用 `after-previous` 或 `with-previous`
- `--narration-audio-dir <dir>` — **低级音频嵌入**：嵌入匹配到的文件，允许部分匹配；仅当不需要完整录制时间轴时使用
- `--use-narration-timings` — 按旁白音频时长设置幻灯片自动翻页时间
- `--narration-padding 0.5` — 每段旁白结束后追加的秒数再翻页（默认 0.5）

#### 其它常用进阶参数

| 参数 | 作用 |
|------|------|
| `-o/--output <path>` | 显式输出路径（默认 `exports/*.pptx`，并备份到 `backup/<ts>/`） |
| `-s/--source <name>` | SVG 源目录：`output`=svg_output（默认 native）、`final`=svg_final（legacy），或自定义子目录名 |
| `-f/--format <name>` | 画布格式（见 CANVAS_FORMATS） |
| `--only native\|legacy` | 只生成一种版本（native 可编辑形状 / legacy SVG 图片） |
| `--svg-snapshot` | 额外输出 SVG 渲染快照 pptx，与 native 并排放在 exports/ |
| `--no-compat` | 关闭 Office 兼容模式（纯 SVG，仅 Office 2019+ 支持） |
| `--no-notes` | 关闭演讲备注嵌入（默认开启） |
| `--no-image-optimize` | 关闭栅格图优化，嵌入原图字节 |
| `--conversion-trace` | 在 native pptx 旁写出诊断 JSON（`<output>.trace.json`） |
| `--cache-dir <dir>` / `--no-cache` / `--keep-cache` | SVG→PNG 渲染缓存控制 |
| `--workers <n>` | 并行 worker 数 |

#### 完整示例

```bash
# 带过渡和级联动画
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir> \
  -t push --transition-duration 1.0 \
  -a auto --animation-trigger after-previous --animation-stagger 0.4

# 录制式旁白（每页一个音频文件，自动设翻页时间）
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir> --recorded-narration audio

# 自动翻页、关闭动画
python3 <skill_dir>/scripts/svg_to_pptx.py <project_dir> -a none --auto-advance 5.0
```

**图表支持：**
图表需要先生成为 SVG，然后包含在 PPT 中。使用 `image_generate` 工具生成图表 SVG，或使用 Python 的 matplotlib 生成。

---

## 路线 B：模板填充工作流（不经 SVG）

当用户明确要求"直接用模板填充/不改版式"时，用 `template_fill_pptx.py` 直接在原生 PPTX 模板的占位符里填内容：

```
analyze（分析模板结构）→ scaffold（搭内容骨架）→ check-plan（校验计划）→ apply（填充）→ validate（校验）
```

与路线 A 二选一，不要混用。详细参数见 `template_fill_pptx.py --help`。

---

## 用户输入覆盖

用户自然语言输入覆盖 `template_config.json` 中的默认值：

| 用户说 | 覆盖内容 |
|--------|----------|
| "用XX模板" | template |
| "做10页" | pages |
| "深色风格" | style + colors |
| "用绿色主色" | colors.primary |
| "要有表格" | content_requirements 追加 |
| "快速模式" | mode = fast |
| "校验模式" | mode = validate |

---

## 脚本说明

| 脚本 | 功能 | 依赖 |
|------|------|------|
| extract_content.py | 提取源文档内容（PDF/DOCX/XLSX/CSV/TXT）→ JSON | 纯 Python |
| project_manager.py init | 初始化项目目录结构（**推荐**）。⚠️ `import-sources` 子命令依赖未打包的脚本，本 skill 内不可用，导入源文件请改用 `extract_content.py` | 纯 Python |
| fix_svg.py | 修复 SVG XML 错误 | 纯 Python |
| check_svg.py | SVG 质量检查，支持 `--config <json> --mode fast\|validate`（按 config 校验） | 纯 Python |
| svg_to_pptx.py | `svg_output/` + `notes/` → `exports/*.pptx` | python-pptx（纯 Python） |
| save_notes.py | 保存演讲者备注到 `notes/` | 纯 Python |
| svg_quality_checker.py | SVG 质量检查（详细版，支持 `--template-mode`/`--format`） | 纯 Python |
| analyze_template.py | 模板分析，COM 导出每页背景图 | **需 PowerPoint 或 WPS** |
| export_previews.py | 从 `exports/*.pptx` 导出每页 PNG 截图 | **需 PowerPoint 或 WPS** |
| export_pdf.py | PPTX → PDF 导出 | **需 PowerPoint 或 WPS** |
| template_fill_pptx.py | **路线 B 专用**：不经 SVG，直接填充原生 PPTX 模板 | 见脚本内说明 |

---

## 资源

| 资源 | 数量 | 用途 |
|------|------|------|
| templates/charts/ | 23 个图表 SVG + `charts_index.json`（含 71 个图表的选型规则） | 图表/流程图，可直接引用 |
| templates/icons/ | 5 个图标库（chunk-filled / phosphor-duotone / simple-icons / tabler-filled / tabler-outline） | 图标，可直接引用 |
| templates/visual-styles/ | 19 种视觉风格 | 设计参考 |
| templates/modes/ | 6 种叙事模式 | 内容组织参考 |
| references/layout_templates.md | 多种布局坐标规格 | Step 7 规划时对照选型 |
| references/error_handling.md | 错误处理指引 | 排错参考 |
