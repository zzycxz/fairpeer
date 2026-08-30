# 工作区 Git 进展面板 SPEC（G1–G3 修复 + 第二轮 G5–G7/F1–F4 + 停车场）

> **版本**: v1.2 | **日期**: 2026-08-30 | **状态**: 全部落地——G1–G3、G5–G7、F1–F4
> 完成；go 双模块 build/vet/test + 前端 tsc/vite/43 测试全绿
>
> **v1.2**: G3 分支切换落地（Popover + 内置确认 + 三连刷新；远程/运行中/待审批
> 禁用）。**与 v1.1 方案的一处偏差**：「会话改动非空」从硬禁用降为确认步骤内
> 的警告文案——硬禁用在活跃会话里几乎永久生效，会让功能不可用；脏树拒绝切换
> 由 git 本身兜底。F2 防回归闸以 vitest 落地（`locale-parity.test.ts`：键集
> 一致 + zh 值非英文句，白名单 7 键），已挂入 npm test / test:all。
>
> **v1.1**: 运行实例走查后新增第二轮批次——G5（子代理检查点补洞）、G6（改动条目
> diff 视图，接已有 `CheckpointDiffForTab` 桥）、G7（TodoPanel 完成态自动折叠）、
> F1–F4 体验修复（tt 泄漏 / zh.ts ndv.* 翻译 / notice 去重翻译 / 发现清理）。
> F2 为**发布阻塞项**：当前源码 zh.ts 有 243 个 `ndv.*` 键为英文，旧构建二进制
> 尚显示中文，一旦重建即整体回归英文。→ 已修复（243 键全部译出，en/zh 键集
> 3568 条完全一致）。
>
> **来源**: 对照 VS Code 智能体 Git 面板（分支 + 增删行统计 + 「进程 3/5」任务清单）的差距复盘。
> 桌面端编码模式现状：改动页签有会话改动 + git 状态徽标 + 提交历史（可展开 diff），但
> **分支名后端已取、前端丢弃**；**增删行统计 CLI 有、桌面无**；**分支切换 API 全就绪、无 UI**。
>
> **术语**: 「改动页签」= WorkspacePanel 的 changed 视图（`viewMode === "changed"`）；
> 「CLI」= `internal/cli` TUI；「远程 tab」= WSL/SSH 远程工作区会话。

---

## §0 范围

**做**（三批，均为「接线」而非「新能力」——后端/CLI 侧数据全部现成）：

| 批次 | 内容 | 优先级 | 一句话 |
|---|---|---|---|
| G1 | 分支名显示 | P0 | `gitBranch` 已随 `WorkspaceChanges` 返回，前端接出来 |
| G2 | +N/-N/?N 增删统计 | P0 | 搬 CLI 的 numstat 逻辑进桌面后端 + 远程 host，改动页签头部展示 |
| G3 | 分支切换下拉 | P1 | `GitBranches`/`GitCheckout` 桥两端就绪，补交互（含确认与刷新） |

**不做**（§5 停车场，写明启动条件）：
- G4 任务清单 × git 整合面板（截图那种「分支+变更量+进程 3/5」同栏视图）
- TodoPanel 布局改造（移出对话区/侧栏化）
- 远程 tab 的分支切换
- 提交级增删统计（commit 详情里加 +/-）

**安全不变量（每条方案自检）**：
1. 新增 git 调用一律只读（numstat/log/branch --format），沿用 `workspaceGit` 的
   console-hidden + `core.fsmonitor=false` + `maintenance.auto=false` 探针模式（#3906）。
2. `GitCheckout` 是本 spec 唯一写路径：必须二次确认；`state.running` / 有未读审批 /
   改动文件在会话中产生时禁用（防止 agent 正在写文件时分支被换）。
3. 探针可失败但不可拖慢面板：numstat 超时/失败时统计位留空，分支名与文件列表照常返回。

---

## §1 G1 分支名显示（P0）

### 现状断链

- 后端 `WorkspaceChanges()` 已填 `GitBranch`（本地：`workspace_changes.go:38`；
  远程：`remote_app.go:321`，detached 时标 `(detached)`）。
- 前端 `WorkspacePanel.loadWorkspaceChanges`（`WorkspacePanel.tsx:257`）只取
  `result.files`，**其余字段全部丢弃**——这是断链的唯一点。

### 方案

1. state 从 `WorkspaceChangeView[] | null` 升级为保存完整 `WorkspaceChangesView | null`
   （分支名、统计位 G2 共用）。
2. 改动页签头部（`workspace-change-scope__head` 同层，无会话改动时置于
   `workspace-git-history` 顶部）新增一条摘要行：
   `⟪branch⟫ · N 个文件 · 来自会话/git`；`gitAvailable === false` 时显示现有文案
   `workspace.gitUnavailable`。
3. 分支名超长截断（CSS `max-width` + ellipsis，title 提示全名）；detached 样式弱化
   （黄点前缀，与 CLI `gitStatus.render` 的语义一致）。

### 验收

- 本地 git 仓库：改动页签可见当前分支；`git checkout` 其他分支后点刷新，分支名随之更新。
- 远程 WSL tab：同样可见（含 detached 标注）。
- 非 git 目录：不渲染摘要行，显示 `gitUnavailable` 文案，无 console 报错。
- `gitBranch` 为空串（detached 且 rev-parse 失败）时整行降级为仅文件计数。

---

## §2 G2 增删行统计（P0）

### 现状断链

- CLI 已有完整实现：`git diff --numstat HEAD` → `parseGitNumstat` 汇总
  `Added/Removed`（`internal/cli/gitstatus.go:65,87`），porcelain 计 untracked，
  渲染 `repo@branch (+N -N ?N)`（绿/红/黄）。
- 桌面后端 `WorkspaceChanges` 只跑 porcelain，无 numstat；远程 host `git/status`
  （`internal/remotehost/fs.go:206`）同样只有 porcelain + branch。
- 桌面前端类型 `WorkspaceChangesView`（`types.ts:513`）无统计字段。

### 方案

1. **桌面后端**（`desktop/workspace_changes.go`）：
   - `WorkspaceChangesView` 增 `Added/Removed/Untracked int`（omitempty）。
   - 新增 `workspaceGitNumstat(base)`：`git diff --numstat HEAD --`，解析逻辑直接
     搬 `parseGitNumstat`（`internal/cli/gitstatus.go:87`，处理二进制 `-` 列），
     在 desktop 包内建同名小函数并配单测（CLI 侧保持不动）。
   - untracked 计数复用已取的 porcelain 结果（`??` 前缀条目数），不发第二次调用。
   - numstat 失败：三字段零值，`GitAvailable` 不受影响（不变量 3）。
2. **远程 host**（`internal/remotehost/fs.go gitStatus`）：`GitStatusResult` 增
   `Added/Removed int`，host 侧多跑一次 `diff --numstat HEAD`；`remote_app.go`
   透传。untracked 由现有 Entries 统计。
3. **前端**（`WorkspacePanel.tsx` + `workbench.css` + locales）：
   - 摘要行（G1）追加统计段：`+N`（绿 `--ok`）/ `-N`（红 `--danger`）/ `?N`
     （黄，仅 >0 时显示），零值隐藏；tabular-nums，与 CLI 配色语义对齐。
   - mock（`bridge.ts:3675` 附近）补字段，保证演示环境一致。

### 验收

- 本地仓库改文件后：`+/-` 与 `git diff --numstat HEAD` 手算一致；untracked 文件计 `?N`。
- 二进制改动（图片）不炸解析（`-` 列跳过）。
- 远程 tab 统计与本地 tab 行为一致。
- `internal/cli` 现有测试不动仍绿；desktop 新增 numstat 解析单测（含空串/二进制/多行）。

---

## §3 G3 分支切换下拉（P1）

### 现状断链

- `GitBranches()`（`workspace_changes.go:220`）/ `GitCheckout()`（`:236`）Go 侧已实现，
  `bridge.ts:323` 已声明，**前端零调用**。

### 方案

1. 摘要行的分支名（G1）升级为下拉触发器（仅本地 tab；远程 tab 保持纯文本——
   远程无 checkout 通道，不在本 spec 补）。
2. 点开：`AnchoredPopover`（复用现有组件，参照 WorkspacePanel 内 recent 菜单的用法）
   列 `GitBranches()` 结果，当前分支高亮 + check 图标；顶部过滤框（分支多于 12 个时出现）。
3. 选择非当前分支 → `InlineConfirmButton`/确认小弹层（文案含目标分支名）→ `GitCheckout(branch)`
   → 成功后重拉 `WorkspaceChanges` + `WorkspaceGitHistory` + 文件树（`loadDir("")`），
   摘要行分支名更新；失败弹 `banner` 错误（git 原始输出截断展示）。
4. **禁用态**（不变量 2）：`state.running` 为真或存在待审批项时触发器置灰并
   提示原因；远程 tab 不渲染下拉（后端 `GitBranches`/`GitCheckout` 双重拒绝，
   `workspace_changes.go`）。「会话改动非空」为确认步骤内的警告而非硬禁用
   （v1.2 偏差，见头部）。

### 验收

- 本地 tab：切换分支后分支名/改动列表/提交历史三者一致刷新；取消确认无副作用。
- 禁用态三种场景各验证一次（跑任务中/待审批/有会话改动）。
- 远程 tab：分支名无下拉箭头、不可点。
- git checkout 冲突失败（脏工作区）：错误可见、面板不白屏。

---

## §4 改动清单

| 文件 | 动作 |
|---|---|
| `desktop/app.go`（WorkspaceChangesView） | +3 统计字段 |
| `desktop/workspace_changes.go` | +`workspaceGitNumstat`（含单测文件 `workspace_changes_numstat_test.go`） |
| `internal/remotehost/fs.go` | `GitStatusResult` +2 字段，host 跑 numstat |
| `desktop/remote_app.go` | 透传统计字段 |
| `desktop/frontend/src/lib/types.ts` | `WorkspaceChangesView` +3 字段 |
| `desktop/frontend/src/lib/bridge.ts` | mock 补字段 |
| `desktop/frontend/src/components/WorkspacePanel.tsx` | 保存完整 view；摘要行；分支下拉+确认+禁用态 |
| `desktop/frontend/src/components/App.tsx` | 向 WorkspacePanel 传 running/approval/hasChanges |
| `desktop/frontend/src/styles/workbench.css` | 摘要行、统计色、下拉样式 |
| `desktop/frontend/src/locales/{zh,en}.ts` | 新增约 8 条文案 |

建议提交切分：G1+G2 一个 commit（数据链路一体），G3 一个 commit。

## §5 停车场（启动条件触发再立项）

| 项 | 启动条件 |
|---|---|
| G4 任务清单 × git 整合面板 | 出现「任务 ↔ 提交」关联的真实诉求（如按 todo 项归组 commit）；需先在内核 evidence 侧建模关联，纯前端拼装无数据源 |
| TodoPanel 布局改造（移出对话区） | G7 落地后仍成为痛点时；现有缓解（手动收起/✕ 关闭/新 todo_write 唤起）先观察 |
| 远程分支切换 | 远程 host 协议增加写通道时一并设计（当前远程只读探针是刻意的） |
| 提交级 +/- 统计 | `GitCommitDetailView` 消费侧提出需求时，`show --numstat` 一行可得 |
| 影子 git（编码） | 用户提出跨会话持久历史诉求，或 G4 立项时一并做 |
| 配置历史 git（运维） | golden/backup 单版本覆盖的痛点被确认后立项（state/golden → 版本库 + 时间线 UI） |

## §6 第二轮批次（v1.1 新增）

走查来源：2026-08-30 运行实例检查（运维欢迎页 + 编码会话 + 改动页签 + 状态目录核对）。

### G5 子代理检查点补洞（P1）

**现状**：`SetPreEditHook` 仅 controller 挂给主 agent（controller.go:399）；
`RunSubAgentWithSession` 新建的子 agent（task.go:492）无钩子 → 技能子代理的
文件写操作不进检查点、不可 rewind——办公模式（document-auto/ppt-auto 等大量
走 run_skill）编辑安全网缺失。

**方案**：`agent.Options` 增加 `PreEditHook func(diff.Change)`，`New` 时安装；
controller 构造 TaskTool 时把主循环钩子传入，子代理与主 agent 共写同一
checkpoint store（Store 并发安全，Snapshot 按轮去重已处理并发首触）。

**验收**：办公模式经 document-auto 写文件后，改动页签出现会话来源条目；
rewind 可恢复；`go test ./internal/agent/... ./internal/control/...` 绿。

### G6 改动条目 diff 视图（P1）

**现状**：非 git 项目（如 fairbox）改动页签只有文件名 + 提示词标签，点开是
文件当前内容预览，**看不到"这轮改了什么"**。而 `CheckpointDiffForTab`
（app.go:1948，bridge.ts:258 已声明）能返回任意轮次的 per-file diff——数据
通路现成，前端没接。

**方案**：改动条目（会话来源）增加「查看改动」入口或直接点条目展开：
取该文件 `turns` 的最后一轮 → `CheckpointDiffForTab(tabId, turn)` → 过滤
该 path → CodeViewer 渲染 diff（复用 git 历史展开的渲染）。无 diff 数据
（早于检查点保留窗口）时降级为现有行为（预览当前内容）。

**验收**：非 git 项目会话改动条目可看到统一 diff；git 项目同样可用；
远端会话（CheckpointDiff 走 remotehost）不炸。

### G7 TodoPanel 完成态自动折叠（P1）

**现状**：任务 5/5 100% 后面板仍展开（5 条划线条目约 150px 占据对话区）。

**方案**：`useEffect` 监听 done/total——全完成后自动 `setOpen(false)`；
新 todo_write（新列表 id）进来时恢复展开。用户手动展开/收起仍优先。

**验收**：任务完成 → 自动折叠为一行；新任务列表 → 重新展开；无闪烁抖动。

### F1 tt("ndv.gs.footer") JSX 泄漏（P0，一行）

NetDevLayout.tsx:2506 漏 `{}`，界面上直接渲染函数调用文本。修复：加花括号。

### F2 zh.ts ndv.* 英文键补翻译（P0，发布阻塞）

**现状**：zh.ts 3555 条中 248 条值为英文长句，243 条属 `ndv.*`（运维模块）。
运行中旧构建尚显示中文；**用当前源码重建即整体回归英文**。

**方案**：按 en.ts 语义逐条翻译为简体中文 UI 文案（保留专有名词/命令名/
品牌名原样）；补 CI 防回归：zh 值不得为「>15 字符的纯 ASCII 小写起始句」
（现有误判白名单：mock.*/preview.* 4+1 条与既有技术字符串）。

### F3 后端 notice 去重 + 翻译（P1）

**现状**：resume 会话触发 `c.notice("resumed session with … still active")`
（controller.go:2422），英文原文入对话流且多次 resume 重复刷屏（实测 4 条）。

**方案**（不动 notice 协议）：
1. Transcript 渲染时合并**相邻同文本** notice 为一条 +「×N」标注；
2. NoticeCard 增加常见后端文案的本地化映射表（本批覆盖 resumed session…、
   compacted、context cleared、new session 四条），未命中映射原样显示。

### F4 发现列表清理能力（P0）

**现状**：实测「发现 113」徽标全部为 8/27 开发测试产物（notify-test-warn、
设备"x"），设备清单 0 台；finding.go 只有 Save/List，**无 Dismiss/Clear API、
无 UI 出口**——测试垃圾永久挂顶。

**方案**：
1. 后端：`internal/netdev/finding.go` 增 `DismissFinding(id)`（删单文件）、
   `ClearFindings()`（清空目录）；desktop 桥 `NetDevFindingDismiss(id)` /
   `NetDevFindingsClear()` + bridge 声明/mock。
2. 前端：发现页签条目加 ×（复用 discovered 主机的交互语法）；页签头加
   「清空」（InlineConfirmButton 二次确认，i18n 文案含条数）。
3. 一致性提示：设备 0 台且发现 >0 时，页签头显示「来自历史运行」角标说明。

**验收**：删除/清空后徽标即时更新；重启不复活；清空有二次确认；
`go test ./internal/netdev/...` 绿。

## §7 测试与回归

- Go：`go build ./... && go vet ./...`；desktop numstat 单测；internal/cli 既有测试不动；
  internal/netdev 新增 Dismiss/Clear 单测；internal/agent 补子代理钩子用例。
- 前端：`tsc` + `vite build` + 既有 tsx 测试（若 WorkspacePanel/TodoPanel/Transcript
  有快照需更新）；zh.ts 翻译后跑 i18n 完整性检查（en/zh 键集一致）。
- 手工冒烟：本地/远程 × git/非 git × running/静止 × 三模式，过一遍 §1–§3、§6 验收。
