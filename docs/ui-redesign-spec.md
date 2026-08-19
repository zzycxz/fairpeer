# 界面改版 Spec(对标 OpenWorker 的视觉与结构升级)

> **状态**:规划中(视觉方向待定稿;阶段 A 可先行,不依赖方向选择)
> **基线**:分支 `feat/mindmap-read-loop` 工作区(2026-08-18)。前端 `desktop/frontend/src` 约 76,500 行 / 57 个组件;`styles.css` 26,258 行
> **范围**:视觉系统收敛、聊天区排版、Composer 重排、工具调用聚合、向导/设置改版、侧栏会话化、Cowork 面板统一。**不含**任何后端/协议改动
> **上游参考**:`Swarm-OS/openworker` GUI(Tauri + React,设计语言与本 spec 的对标来源);视觉预览稿 `ui-mockups/index.html`(5 个方向,浏览器直接打开)
> **红线**:**功能零缺失**——见 §5 保全矩阵;所有现有功能入口改版后必须仍可达

---

## 一、目标与红线

### 1.1 目标
1. **观感焕新**:疏朗、克制、有签名细节;消灭"同一界面 3~4 种圆角、两套气质、颜色随主题漂移"的杂感。
2. **系统收敛**:一代 token(圆角/间距/动效),消除硬编码与内联样式;26k 行 `styles.css` 拆分为目录。
3. **维护性**:巨型组件(SettingsPanel 5,342 行 / Composer 2,229 行 / App.tsx 3,305 行)拆分,纯搬移不改逻辑。
4. **结构补课**:侧栏从"纯文件树"升级为"会话为主、文件为辅",对齐"打开即回到工作"的体验。

### 1.2 红线:功能零缺失
- 定义:**改版前存在的每一个用户可触达的功能入口,改版后仍可达**。允许换形态(气泡→眉标、按钮→菜单项),不允许消失。
- 约束 1:任何被收纳的功能,从主界面出发 **≤ 2 次点击** 可达;快捷键数量**只增不减**;右键菜单**只增不减**。
- 约束 2:如实施中发现某功能"建议删除"(如确属死代码),必须单独提项、经确认后在矩阵中标记为 `移除(经确认)`,禁止静默删除。
- 约束 3:§5 矩阵是验收交付物的一部分——每阶段结束逐项勾检,PR 描述附勾检结果。

### 1.3 保留资产(不动的底座)
hot/warm/cold 三层渲染预算(Transcript)、z-index 全 token 化 + CI 守卫、6 theme_style × 亮暗/auto 主题框架、3 档显示密度 × 4 档字号 × 4 字体预设、中英 i18n(2,067 key 对齐)、`prefers-reduced-motion` 覆盖、AnchoredPopover 统一弹出层、TabBar 拖拽排序、乐观 UI 模式。**不迁 Tailwind、不引 UI 组件库、不用 backdrop-filter**(Linux WebKitGTK 约束,`styles.css:1-10`)。

### 1.4 后端边界(零后端改动的依据,已逐项核实)
本 spec 全部阶段只改 `desktop/frontend/src` 与前端构建脚本。所有新 UI 消费的数据/操作均为**现成 API**:
| 新 UI | 消费的现成接口 |
|---|---|
| 侧栏会话列表(分组/年龄/重命名/删除) | `ListSessions`(SessionMeta 含时间戳)/ `RenameSession` / `DeleteSession`(bridge.ts:186-194) |
| 向导供应商卡墙 | `GetProviderTemplates` / `ProbeVendorKey` / `SetupProvider` / `FetchProviderModels`(含本地 keyless,已于模型接入批次完成) |
| Composer 用量填充条 | ContextPanel / useController 既有数据源 |
| TurnGroup 工具聚合 | 既有工具调用事件流(前端聚合,不改协议) |

**唯一例外**:会话"置顶"需要后端会话元数据加 pin 字段——**v1 不做**,列入 backlog 待后端单独排期;侧栏列表首期仅"最近分组 + 重命名 + 两步删除"。

---

## 二、设计规范(定稿决策)

### 2.1 视觉方向(二选一,见 ui-mockups)
| 方案 | 亮色默认 | 暗色默认 | 备注 |
|---|---|---|---|
| **推荐:品牌延续** | 方向 3「炉温」(暖纸 #f7f4ee + 陶土橙) | 方向 4「余烬」(现暗色收敛重制,保留橙) | 品牌资产延续,cowork 模式协调 |
| 备选:OW 原味 | 方向 1「曙光」(冷灰 + 钴蓝) | 方向 2「夜航」 | 最接近 OpenWorker |
| 附加 | 方向 5「车床」做成第 7 个 theme_style(紧凑工程师风),非默认 | | 服务重度 dev 用户 |

组件骨架(两案通用):768px 阅读列、中性 `--solid` 用户气泡(右下小角 14/14/4/14)、10px 大写眉标、TurnGroup 工具折叠、单行控制栏 Composer、上下文用量填充条、"回到最新"悬浮药丸、零阴影克制(仅浮层投影)。

### 2.2 Token 定稿
| Token | 值 | 存量映射(601 处 `border-radius` 按值批改) |
|---|---|---|
| `--radius-sm` | 6px(控件/输入) | 3px、4px(共 387 处) |
| `--radius-md` | 10px(卡片) | 5px、6px、8px(共 53 处) |
| `--radius-lg` | 14px(气泡/模态) | 10px、11px、12px、14px(共 9 处) |
| `--radius-pill` | 999px | 999px(70 处,仅换写法) |
| `--space-1..6` | 4/8/12/16/24px | 仅改版触及区域强制;存量不清洗 |
| `--dur-fast/base/slow` | 120/180/280ms + 统一 easing | 168 处 transition、71 处 animation 收敛(阶段 A) |
| `--solid` | 每 theme_style 定义(暗 ≈#2e3138 / 亮 ≈#e9ebf0) | 用户气泡专用中性底 |
| `--maxw` | 940 → **768px** | 阅读列;聊天最小宽约束同步重算 |

### 2.3 明确不做
玻璃模糊顶栏(WebKitGTK)、color-mix 关键路径、macOS overlay 标题栏、Tailwind 迁移、砍任何 cowork 面板、换品牌主色、移动端适配。

---

## 三、现状基线(量化,2026-08-18 核准)

| 指标 | 数值 |
|---|---|
| styles.css | 26,258 行,约 3,210 个选择器,占前端源码约 1/3 |
| `border-radius` 硬编码 | 601 处(4px×242、3px×145、999px×70、6px×26、5px×26…);token `var(--radius*)` 仅 9 处 |
| 内联样式绕主题 | CoworkDock 61、ImportModal 35、TemplateSelect 34、CapabilitiesPanel 31、SettingsPanel 27、OnboardingOverlay 13;cowork 硬编码 `#3b82f6/#a855f7/#eab308` 等 |
| 动效 | 168 处 transition / 71 处 animation / 约 30 个 keyframes,时长缓动各写各的 |
| i18n 泄漏 | Composer 历史会话菜单、ModelSwitcher"月之暗面"、ConfirmModal 默认按钮、TabBar"专家会话"fallback 等 8 处 |
| 巨型组件 | SettingsPanel 5,342 / bridge.ts 3,741 / App.tsx 3,305 / CapabilitiesPanel 2,304 / Composer 2,229 |
| **容器自适应缺失** | ① `--maxw` 固定 940px + 固定 32px 内边距,且 940/920/760 三处覆盖并存——侧栏拖宽后对话列只贴边铺满,不随容器收窄;② `--chat-min-width`(JS 传 400/560)CSS **零消费**(死变量),"对话最小宽"实际不存在;③ 侧栏拖宽钳制仅 `clamp(200, clientX, 480)` 看视口,不感知右侧 dock——dock(428px)开着时侧栏仍可拖到 480,对话区可被挤至 ~300px 无任何防护(`App.tsx:110-117,1885-1915`、`styles.css:1683-1684,225,2604`) |

---

## 四、阶段计划(每阶段 = 独立 PR,可单独回滚)

### 阶段 A:地基——token 收敛与样式清偿(2~3 天)
1. `scripts/normalize-radius.mjs`:按 §2.2 映射表批改 601 处圆角(dry-run 报告 → 写入),并挂入 `npm run build` 作守卫(禁止新增字面圆角,白名单例外)。
2. 动效 token:`--dur-*` + easing 变量定义,过渡类收敛(新代码强制,存量动画文件整体归档到 `animations.css`)。
3. 内联样式清偿:§3 所列 6 个组件约 200 处 → BEM 类 + token;重点消灭 cowork 硬编码色。
4. `styles.css` 拆分为 `src/styles/` 目录:`tokens / base / chrome / chat / composer / settings / cowork / panels / modals / animations`(**按原顺序切分**,构建产物规则数 diff 校验);`check-css-syntax.mjs` 改为遍历目录。
5. i18n 泄漏修复(§3 清单 8 处)。
- **验收**:build 三守卫全绿;关键界面截图与改前逐像素对比(此阶段观感变化仅圆角统一级别)。

### 阶段 B:主界面骨架——聊天区 + 侧栏会话化 + TabBar(2~3 天)
1. 阅读列 768px;消息间距 18→13px;去 transcript 网格纹理(`styles.css:2592` 自标 "easy revert")。
2. 用户气泡 `--solid` + 右下小角 + 78% 宽;助手消息 10px 大写眉标(assistant/子代理名/角色,i18n)。
3. 复制/时间进**零高度 hover meta 条**;Fork/摘要/回退(含二次确认)保留在下拉。
4. **侧栏会话化**:上半区 = 会话列表(最近分组、紧凑年龄、hover ⋮ 菜单:重命名/删除两步确认;置顶待后端支持,见 §1.4),下半区 = ProjectTree 可折叠;HistoryPanel 保留为"全部会话"管理弹窗(⌘K / 侧栏"查看全部"入口)。Cowork 侧栏分组(新建任务/日历/专家团/知识库)全保留,统一为同一导航语言。
5. TabBar 视觉减负:激活态卡片化、非激活纯文字 + hover 出关闭;拖拽排序/右键批量关闭不动。
6. topicbar 简化为"标题 + 事实副标题(model · 工作区)"形态;重命名、变更按钮、命令面板按钮保留。
7. **容器自适应改造**(修复"侧栏/历史区拖宽后对话不跟随"的问题):
   - `.transcript` 设 `container-type: inline-size`;对话列规则改为 `max-width: min(768px, 100%)` + 水平内边距 `clamp(16px, 6cqw, 48px)`——列宽随 pane 实际宽度按比例收窄,窄 pane 自动加大相对边距(对齐 OpenWorker `padding: 8%` 的行为);composer(`.composer-wrap`)消费同一规则,消灭 940/920/760 三处 `--maxw` 覆盖。
   - **侧栏钳制升级**:`clampSidebarWidth` 从"仅看视口"改为联合约束 `min(clientX, 视口宽 − 右侧 dock 占宽 − CHAT_MIN_WIDTH)`;右侧 dock 拖宽上限同样受"对话区 ≥ CHAT_MIN_WIDTH"约束——两侧面板此消彼长,对话区永不被挤穿。
   - 死变量处置:CSS 删除未消费的 `--chat-min-width` 定义(`styles.css:1684,17065`),以 JS 钳制为唯一事实源;或反向让 grid 列真正消费它——二选一,禁止继续"定义了但没用"。
   - 验证场景:侧栏拖至 480 / 右 dock 开至最大 / 两者同时,对话列均保持可读且无横向滚动。
- **涉及**:`App.tsx`、`Transcript.tsx`、`Message.tsx`、`ProjectTree.tsx`(加折叠容器不改内部)、`HistoryPanel.tsx`(加入口)、`TabBar.tsx`、`AppChrome.tsx`、chat.css/chrome.css。

### 阶段 C:Composer 重排(2 天)
结构:附件 chip 条 → textarea → 单行控制栏(左:`＋附件 / 模式 / 审批`;右:`用量 chip / 模型 / 发送`)。发送钮状态化(空=描边、有文=实心、运行=Stop)。
- 用量 chip 内嵌 12px 上下文填充条 + 按模型账单弹层(数据源 ContextPanel/useController 现成)。
- **全部现有控件保留**(§5 矩阵 C 组逐项):低频项(意图菜单/Plan/Goal/Effort/Knowledge)收进 `More`,默认固定可pin;SlidersMenu/ArgMenu/VirtualMenu/语音/拖拽/长粘贴折叠/拖拽调高/shell 提示符全保留。
- `Composer.tsx` 拆为 `composer/` 目录(ComposerCard / AttachmentRow / ParamBar / UsageChip / VoiceButton / menus…)。

### 阶段 D:工具调用聚合层(2 天)
TurnGroup:连续工具区间折叠为一行摘要头("N 步 · 读取 2 个文件 · ✓ 2 已批准 · 6.4s"),展开人类语化步骤(i18n 动词表)+ `原始日志` 链接;审批徽章三态 pill(已批准/已拒绝/自动允许);等待审批卡 dock 在 composer 上方。
ToolCard 明细卡(diff/子代理/shell 截断输出)保留为展开态;hot/warm/cold 渲染预算不动。

### 阶段 E:向导 + 设置改版(3~4 天)
1. vendor lobe-icons(MIT)子集(≥26 家,含国内厂商)→ `src/assets/providers/*.svg` + 浅色底牌容器。
2. OnboardingOverlay:固定 600×560 模态、进度圆点、两列供应商卡墙(logo + 名称 + 状态行"✓ 已连接 · 2h 前用过 / 未配置 / 无需密钥");key 内嵌"✓ 已验证"pill;**三步流程、本地免 key 跳步、同步模型、跳过按钮全部保留**。
3. SettingsPanel:5,342 行拆为 `settings/` 14 个页面文件(**纯搬移不改逻辑**);形态改全页面 + 左 sub-nav(208px)+ 居中 max-w-3xl 卡片流;14 个分区一个不少。
4. Models 设置页复用向导卡墙组件(同源防漂移)。

### 阶段 F:状态、反馈与打磨(1~2 天)
骨架屏原语(设置页/CoworkDock/历史弹窗三处先用,替换纯文字加载态);Toast 底部 5s 排水条;空状态 3 张模板任务卡(技能/连接器就绪色点);命令面板行内 `⌘1-9` kbd + `?` 快捷键 cheatsheet(快捷键只增不减);通知徽章体系(jobs 进行中/审批等待/邮件未读统一);StartupSplash 与新视觉对齐;ConfirmModal i18n + 破坏性红色样式;滚动条按平台统一样式;右侧 dock 双轨(dev workbench 与 cowork dock)统一骨架。

### 阶段 G:Cowork 面板统一(2~3 天)
CoworkDock 四标签内容卡片化 + 空态 + 状态点;专家团群聊(CollabStream)与 ExpertPanel 气泡对齐新语言;日历任务面板;RagPanel 3D 图谱主题联动(保留 3D,2D 力导向为可选视图不替换);PreferencePanel / TemplateSelect / DocPreview / AttachmentViewer 对齐卡片原语。

### Backlog(不排期,随阶段顺带)
代码块头部三段式(语言/复制/折叠)+ diff 配色主题联动;Markdown 表格斑马纹/粘性表头;右键菜单扩展到侧栏/dock/消息;窄窗口降级顺序(侧栏归零→dock 隐藏→按钮浮出,规则与 §4-B7 的联合钳制衔接);Windows/Linux 平台细节。

---

## 五、功能保全矩阵(红线落地;验收时逐项勾检)

图例:去处 = 改版后的位置;`形态` 列为允许的变化;验证 = S 截图对比 / I 交互勾检 / E e2e(gui-test 流程)。

### A. 顶栏与标签(AppChrome / TabBar)
| 现有功能 | 去处 | 形态 | 验证 |
|---|---|---|---|
| 标签切换 / 关闭 / + 新建 | 原位 | 非激活态 hover 出关闭 | I |
| 拖拽排序(含落点指示) | 原位 | 不变 | I |
| 右键批量关闭菜单 | 原位 | 不变 | I |
| dev/cowork 分段切换 | 原位 | 胶囊样式重绘 | S+I |
| 侧栏开关 / 右面板开关 / 搜索按钮(Win/Linux) | 原位 | 图标不变 | I |
| Windows 预览窗控 / macOS 红绿灯 | 原位 | 平台分支不动 | I |

### B. 侧栏(dev / cowork)
| 现有功能 | 去处 | 形态 | 验证 |
|---|---|---|---|
| 新建会话 / 新建任务按钮 | 原位(首行) | split 主按钮 | I |
| ProjectTree 文件树(1,070 行,含全部交互) | 侧栏下半区 | 外包折叠容器,**内部不改** | I+E |
| 编码偏好 / 办公偏好入口 | 侧栏底部账户区 | 归组 | I |
| cowork 侧栏:日历任务 / 专家团 / 知识库入口 | 原位 | 统一导航语言 | I |
| 会话历史(HistoryPanel 列表 + 预览) | **双入口**:侧栏新增会话列表(最近分组)+ HistoryPanel 保留为全量管理 | 新增不删除(置顶列 backlog,§1.4) | I+E |
| 侧栏拖拽调宽(200~480)+ **拖至左缘收起**(drag-to-edge hide) | 原位 | 钳制升级为联合约束(§4-B7) | I |
| 键盘调宽(←/→ ±16px)| 原位 | 同上钳制 | I |
| 右侧 dock 拖宽 / 最大化(`layout--workspace-maximized`) | 原位 | 上限受"对话 ≥ CHAT_MIN_WIDTH"约束 | I |

### C. Composer(逐控件,最敏感区)
| 现有功能 | 去处 | 形态 | 验证 |
|---|---|---|---|
| 运行状态条 + 暂停/停止 | composer 顶行 | 原样 | I |
| 附件 chip:图片/文件/@工作区/历史会话引用(含移除) | 文本区上方 chip 行 | 原样 | I |
| 长粘贴折叠块 | 原位 | 原样 | I |
| 拖拽调高(104~360px) | 原位 | 原样 | I |
| shell 模式提示符(`›`/`$`) | dev 保留 | 原样 | I |
| 语音麦克风 | 控制栏右侧 | 原样 | I |
| 发送(含运行中变停止) | 控制栏右端圆钮 | 状态化 | I |
| 意图菜单(Sliders)/ Plan / Goal / 审批三段滑块 / EffortSwitcher / KnowledgeSwitcher / More | 控制栏左侧 + More 菜单;**可 pin 到一级** | 收纳 ≤2 击可达 | I |
| ModelSwitcher(≤5 平铺 / >5 级联悬停) | 控制栏右侧 | 原样 | I |
| SlashMenu / ArgMenu / VirtualMenu(@文件、历史会话搜索) | 原位 | 样式对齐 | I |
| 拖文件进入高亮 | 原位 | accent 光环 | I |
| token 用量(原 StatusBar/ContextPanel 数据) | 控制栏用量 chip(填充条 + 账单弹层) | 新增呈现 | I |

### D. 消息区与内容渲染
| 现有功能 | 去处 | 形态 | 验证 |
|---|---|---|---|
| 用户附件展示(图/文件/文件夹)、发送失败态、IM 来源卡片 | 原位 | 原样 | S+I |
| reasoning 折叠(思考中/完成) | 原位 | 竖线块对齐新语言 | I |
| 复制 / Fork / 摘要 / 回退(含二次确认) | hover meta 条 + 下拉 | 复制进 hover 条 | I |
| ToolCard:折叠/diff/子代理递归/shell 10 行截断/产出图片/Ctrl+B | TurnGroup 展开态明细 | 外包聚合层 | I+E |
| Markdown:KaTeX / mermaid / CodeViewer(maxHeight/展开)/ 外链系统打开 | 原位 | 代码块头部升级 | S+I |
| 问题导航锚点 / jump-bar | 药丸 + 锚点保留 | 换形态 | I |
| hot/warm/cold 三层渲染 | **不动** | — | E |

### E. 右侧面板与其他
| 现有功能 | 去处 | 形态 | 验证 |
|---|---|---|---|
| dev dock:Files / Context / Changed;ContextPanel 仪表/已读/变更 | 原位 | 统一 dock 骨架 | I |
| CoworkDock:今日/邮件/文件/概览;RAG 集合/实体/文件/提取 | 原位 | 卡片化,标签不减 | I+E |
| MemoryPanel / CapabilitiesPanel(MCP/Skills 市场) | 原位(设置页内嵌关系不变) | 样式对齐 | I |
| CommandPalette / StatusBar(token、缓存命中、jobs popover) | 原位;StatusBar 在 cowork 简化为可展开,**dev 全量保留** | 收纳不删除 | I |
| StartupSplash / Welcome 空态示例(dev 4 条 / cowork 6 条) | 原位 | 视觉对齐 + 任务卡化 | S+I |

### F. 设置与向导
| 现有功能 | 去处 | 形态 | 验证 |
|---|---|---|---|
| 设置 14 个分区(general…updates)全部字段 | 全页面 + sub-nav,**一区不少一字段不丢** | 纯搬移 | E+I |
| 模型抓取 / RegistryBox / Provider 卡片 / keySet 徽章 / 外观(6 主题×亮暗/字号/字体/密度) | 原位 | Models 页卡墙化 | I |
| Onboarding 三步 / 本地免 key 跳步 / 同步模型 / 跳过 | 原位 | 卡墙换皮 | I+E |

### G. 系统级
| 现有功能 | 去处 | 形态 | 验证 |
|---|---|---|---|
| 6 theme_style × 亮/暗/auto、3 密度、4 字号、4 字体预设 | 原位 | 新增「车床」为第 7 风格 | E |
| 中英 i18n / prefers-reduced-motion / z-index 守卫 | 原位(泄漏修复) | 只增不减 | E |

---

## 六、验收与回归

1. **每阶段**:`npm run build`(css 语法 + z-index + 新增圆角守卫)+ `tsc` 通过;`gui-test-screenshots` 流程对关键界面截图,与上一阶段对比;§5 矩阵对应分组逐项勾检,PR 描述附结果。
2. **容器自适应专项**(阶段 B):三组拖拽场景截图——侧栏 480 / 右 dock 最大 / 两者同开——对话列均居中、边距随宽收窄、无横向滚动、无内容裁切;`.transcript` 列宽在 pane 600→1400px 间连续拖动时平滑跟随。
2. **每阶段(Go 侧无关,但防误伤)**:desktop Go 测试失败集与本分支基线(14 个已知 WIP 失败)比对,不多不少。
3. **最终验收**:`6 主题 × 亮暗 × 3 密度` 截图矩阵抽查;硬编码扫描(圆角/颜色/内联样式)为零增量;快捷键清单只增不减;§5 全矩阵勾检完成。
4. **回滚**:每阶段独立 PR,revert 即回滚;阶段 A 的守卫脚本保留在被 revert 的提交里也可独立恢复。

---

## 七、风险与对策

| 风险 | 对策 |
|---|---|
| 26k CSS 拆分级联差异 | 按原顺序切分;构建产物规则数 diff;截图对比 |
| 圆角统一改变观感 | 脚本 dry-run 报告人工抽查后再写入 |
| SettingsPanel/Composer 拆分引入行为回归 | 纯搬移原则;拆分 PR 与样式 PR 分离;e2e + 勾检 |
| 侧栏会话化与 ProjectTree 争高 | 会话区 max-height + 滚动;文件树折叠态记忆 |
| 收纳造成"功能找不到" | ≤2 击红线 + `?` cheatsheet + More 可 pin |
| WebKitGTK 兼容 | 零新运行时依赖(logo 为静态 SVG);禁 backdrop-filter/color-mix |

---

## 八、里程碑汇总

| 阶段 | 工作量 | 依赖 |
|---|---|---|
| A 地基 | 2~3 天 | 无(可立即开工,不依赖视觉方向定稿) |
| B 主界面骨架 | 2~3 天 | A |
| C Composer | 2 天 | A |
| D 工具聚合 | 2 天 | A |
| E 向导+设置 | 3~4 天 | A |
| F 反馈打磨 | 1~2 天 | B/C |
| G Cowork 面板 | 2~3 天 | A |
| **合计** | **约 14~19 个工作日** | C/D/E/G 可并行 |
