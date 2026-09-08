# fairpeer 编码·办公·运维 能力补齐规格书

> 基于 codex 源码研究 + fairpeer 三轮源码审计（9 个子代理交叉验证）生成。
> 日期：2026-09-07 | 状态：Draft
>
> **2026-09-08 修订**：全部事实断言经代码逐条复核，修正过时基线（旧版 "72 个工具"
> 抄自 2026-07-05 的 DEV_COWORK_TOOL_COMPARISON.md，早于 profile 分区注册）、
> 删除不存在的工具名（xlsx_edit / mindmap_read / ppt_create）、撤回与现状冲突的
> 方案（Spec-3.2/3.3、Spec-4 从零建 PTY、Spec-4 WebSocket）。Spec-3 核心已实施落地。

---

## 总览

| # | 不足项 | 严重度 | codex 参考 | 改进方案 | 状态 |
|---|--------|--------|-----------|---------|------|
| 1 | 工具卡片渲染低覆盖 | 中 | HistoryCell trait + 按工具类型分组件 | 分层 ToolCard 体系 | **两批已实施**（批 1：bash/办公写/email/netdev_probe；批 2：BrowserActionCard 全量 33 工具 + 标准模式 Exploring 只读分组。read/edit/write 专卡经评估不做——generic 管线 + tools.ts 摘要已覆盖，专卡冗余） |
| 2 | 对话内无全文搜索 | 中 | codex 也没有（仅有 Ctrl+T overlay） | 超越 codex：Ctrl+F 搜索 + 高亮 | **已实施**（Phase 2：用户消息/通知 mark 高亮 + 跳转闪烁；Markdown/工具输出的高亮穿透为 Phase 3） |
| 3 | 证据链不认办公工具 | 高 | codex 无此概念（update_plan 不验证） | 名单扩展 + out_path 提取 | **已实施**（netdev 台账对接另立 Spec） |
| 4 | 主终端未接 PTY | 中 | portable-pty + ProcessHandle 抽象 | 接线既有 ConPTY + 补 Unix 存根 | **已实施** |
| 5 | Item 事件前端未消费 | 低 | SQ/EQ + TurnItem tagged enum + delta | 双 wire.go 补 event.Item 映射 | **已实施**（含 resumed/expert_collab 两个同源断流修复；渲染迁移 Phase 2） |
| 6 | MCP 通知被丢弃 | 中 | LoggingClientHandler（仅日志） | 日志 + tools/list_changed 自动刷新 | **已实施**（含注册表热换） |

工具面基线（profile 分区注册，`internal/boot/boot.go:632-853`）：全库实现 ~125 个工具；
dev 注册 ~37（基础 24 + meta 9 + 探索/评审包装 4）；cowork ~82–94（browser 21、
screen 7、window 5、schedule 6、email 3、calendar 1、im_send 1、rag 6 可选、
document 9、expert 2，平台/RAG 相关）；netdev ~52–54（netdev_* 25 注册 / 24 可见，
trustdomain 与 RAG 各 2 个条件项）。前端 `toolCards.tsx` 注册表 5 项（39 行），
`ToolCard.tsx` 321 行兜底，`lib/tools.ts`（206 行）另有按工具的标题/diff 启发式——
新卡片体系须与后者合并而非并行。

---

## Spec-1：分层 ToolCard 渲染体系

### 问题

`toolCards.tsx` 注册表仅 5 项（web_search + 4 netdev_*），未注册工具显示为原始 JSON。
注意 `netdev_discover` 卡片对应的工具已收敛为 `netdev_probe` 的弃用别名
（`netdev/tools.go:632` tunnel 通道注册，`probetool.go:349` probeAliasTool），
卡片应保留至别名移除，但不要再新增对旧名的引用。

### codex 参考

codex 采用 per-tool-type 的 `HistoryCell` trait：
- `ExecCell`：shell 命令，带状态指示器（绿勾/红叉/旋转圈）、bash 语法高亮、头尾截断 + 省略行
- `McpToolCallCell`：`server.tool(args)` 格式，cyan 高亮
- `WebSearchCell`：搜索/打开/查找三种动作区分
- `PatchHistoryCell`：diff 摘要
- `Exploring` 模式：只读操作（Read/List/Search）分组折叠，树形缩进

### 方案

#### 1.1 扩展 ToolCardSpec 为分层注册

```typescript
// toolCards.tsx — 新增 profile 感知 + 分类渲染
interface ToolCardSpec {
  body?: (item: TranscriptItem) => React.ReactNode | undefined
  forceOpen?: boolean
  noQuiet?: boolean
  category?: 'read' | 'write' | 'search' | 'shell' | 'office' | 'browser' | 'netdev'
  profile?: ProfileKind[]  // 限定在哪些 profile 下生效
}

const registry: Record<string, ToolCardSpec> = {
  // --- 编码 (dev)：基础 24 + meta 9 ---
  read_file:    { category: 'read',   body: ReadFileCard },
  write_file:   { category: 'write',  body: WriteFileCard },
  edit_file:    { category: 'write',  body: EditFileCard },
  multi_edit:   { category: 'write',  body: MultiEditCard },
  bash:         { category: 'shell',  body: ShellCard },
  grep:         { category: 'search', body: GrepCard },
  glob:         { category: 'search', body: GlobCard },
  apply_patch:  { category: 'write',  body: PatchCard },
  web_search:   { category: 'search', body: WebSearchCard },
  web_fetch:    { category: 'read',   body: WebFetchCard },

  // --- 办公 (cowork)：document 9 张专用卡，其余按类聚合 ---
  doc_write:      { category: 'office',   body: DocWriteCard },
  doc_read:       { category: 'office',   body: DocReadCard },
  doc_convert:    { category: 'office',   body: DocConvertCard },
  csv_write:      { category: 'office',   body: CsvWriteCard },
  csv_read:       { category: 'office',   body: CsvReadCard },
  xlsx_write:     { category: 'office',   body: XlsxWriteCard },
  xlsx_read:      { category: 'office',   body: XlsxReadCard },
  xlsx_query:     { category: 'office',   body: XlsxQueryCard },
  mindmap_create: { category: 'office',   body: MindmapCard },
  // cowork 的大头是浏览器/桌面自动化（21 browser_* + 7 screen_* + 5 window_*），
  // 一个按动作分组的 BrowserActionCard 吃下大多数，比逐个注册更实际：
  'browser_*':  { category: 'browser', body: BrowserActionCard }, // 动作名+目标+截图态
  'screen_*':   { category: 'browser', body: BrowserActionCard },
  'window_*':   { category: 'browser', body: BrowserActionCard },
  email_send:   { category: 'office',   body: EmailCard },
  email_read:   { category: 'office',   body: EmailCard },
  // schedule_*/calendar/im_send/rag_*/expert_team_*：走 category 兜底样式即可

  // --- 运维 (netdev)：沿用现有 forceOpen/noQuiet，netdev_discover 为弃用别名保留 ---
  netdev_exec:     { category: 'netdev', forceOpen: true, noQuiet: true },
  netdev_netconf:  { category: 'netdev', forceOpen: true, noQuiet: true },
  netdev_probe:    { category: 'netdev', forceOpen: true, noQuiet: true },
  netdev_baseline: { category: 'netdev', forceOpen: true, noQuiet: true },
  netdev_discover: { category: 'netdev', forceOpen: true, noQuiet: true }, // 弃用别名
}
```

（`'browser_*'` 通配注册是新语法，落地时实现为前缀匹配；无对应工具的条目不得
进入注册表——旧版草稿中的 xlsx_edit / mindmap_read / ppt_create 均不存在：
读思维导图走 doc_read，PPT 由 ppt-auto 技能驱动 WPS，无 ppt_* 工具。）

#### 1.2 新增专用卡片组件

| 组件 | 文件 | 功能 |
|------|------|------|
| `ReadFileCard` | `toolcards/ReadFileCard.tsx` | 语法高亮预览，行号，可折叠 |
| `WriteFileCard` | `toolcards/WriteFileCard.tsx` | 文件路径 + 行数变化摘要 |
| `EditFileCard` | `toolcards/EditFileCard.tsx` | 内联 UnifiedDiff 预览（tools.ts 已有管线，迁移复用） |
| `PatchCard` | `toolcards/PatchCard.tsx` | 多文件 diff 摘要 + 展开详情 |
| `ShellCard` | `toolcards/ShellCard.tsx` | 状态指示器 + 头尾截断 + 展开全文 |
| `GrepCard` | `toolcards/GrepCard.tsx` | 匹配数 + 前 N 条结果高亮 |
| `DocWriteCard` | `toolcards/DocWriteCard.tsx` | 文档路径 + 字数/段落变化 + 格式 |
| `XlsxWriteCard` | `toolcards/XlsxWriteCard.tsx` | 工作簿 + sheet 行数变化 |
| `BrowserActionCard` | `toolcards/BrowserActionCard.tsx` | 动作（click/type/scroll…）+ 目标元素 + 截图态 |
| `EmailCard` | `toolcards/EmailCard.tsx` | 收件人 + 主题 + 摘要 |

`DocReadCard`/`CsvReadCard`/`XlsxQueryCard`/`MindmapCard`/`DocConvertCard` 在 1.1
注册表中被引用，实现上可先复用 `DocWriteCard`/`GrepCard` 的骨架。

#### 1.3 Exploring 分组模式

借鉴 codex 的 `is_exploring_call`，将连续的只读工具（`read_file`、`ls`、`grep`、
`glob`、`web_fetch`、`doc_read`、`csv_read`、`xlsx_read`、`xlsx_query`）折叠为一个
"探索中" 摘要行：

```
📖 探索中 — 读取 src/main.go, src/util.go | 搜索 "handleError" | 列出 src/
```

展开后逐条显示。

#### 1.4 ShellCard 头尾截断

```
$ npm run build
✓ Built in 3.2s
  dist/index.js     45.2 kB
  dist/index.css    12.8 kB
  ... +127 lines (Ctrl+T 查看完整输出)
```

---

## Spec-2：对话内全文搜索

### 问题

Transcript.tsx（1302 行）无搜索功能。长对话中无法定位特定内容。

### codex 参考

codex **也没有**对话内搜索。仅有：
- Ctrl+R：输入历史反向搜索
- Ctrl+T：transcript overlay（仅浏览，无搜索）
- Ctrl+F：仅用于 agents overview 列表过滤

### 方案：超越 codex，实现 Ctrl+F 搜索

#### 2.1 搜索栏组件

```typescript
// components/TranscriptSearch.tsx
interface TranscriptSearchProps {
  items: TranscriptItem[]       // 全量对话条目
  onJumpTo: (itemId: string) => void
}

// 功能：
// - Ctrl+F 唤起搜索栏（固定在 Transcript 顶部）
// - 增量搜索，250ms 防抖
// - 匹配计数 "3/17"
// - Enter/Shift+Enter 上下跳转
// - Esc 关闭
// - 匹配文本高亮（mark 标签）
```

#### 2.2 搜索范围

| Profile | 搜索内容 |
|---------|---------|
| dev | 用户消息、AI 回复、工具 args/output、reasoning |
| cowork | 同上 + 文档预览文本 |
| netdev | 同上 + 设备输出、配置 diff |

#### 2.3 实现要点

```typescript
// Transcript.tsx 新增
const [searchOpen, setSearchOpen] = useState(false)
const [searchQuery, setSearchQuery] = useState('')
const [matchIds, setMatchIds] = useState<string[]>([])
const [matchIndex, setMatchIndex] = useState(0)

// Ctrl+F 快捷键
useEffect(() => {
  const handler = (e: KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
      e.preventDefault()
      setSearchOpen(true)
    }
  }
  window.addEventListener('keydown', handler)
  return () => window.removeEventListener('keydown', handler)
}, [])

// 搜索逻辑：遍历 items，匹配 text/reasoning/output 中的子串
// 返回匹配的 item id 列表
// 自动滚动到当前匹配项 + 高亮
```

#### 2.4 跨会话搜索增强

当前 `SearchSessions`（`internal/agent/search.go:40`）经 controller 只搜当前
profile 目录（`internal/control/controller.go:2742` 传 `c.sessionDir`）。增强方案：

```go
// 跨 profile 搜索：复用 config.SessionDirFor（config.go:2256）——
// dev/默认落在顶层 sessions，cowork/netdev 落在 sessions/<key> 子目录
func SearchAllProfiles(query string) []SearchHit {
    var all []SearchHit
    for _, profile := range []string{config.ProfileDev, config.ProfileCowork, config.ProfileNetDev} {
        hits := agent.SearchSessions(config.SessionDirFor(profile), query)
        for i := range hits {
            hits[i].Profile = profile  // 标注来源 profile
        }
        all = append(all, hits...)
    }
    sort.Slice(all, func(i, j int) bool {
        return all[i].ModTime.After(all[j].ModTime)
    })
    return all
}
```

---

## Spec-3：证据链认办公工具（核心已实施）

### 问题（复核后修正）

`evidence.go` 的 `isWriterTool` 仅列 7 个编码工具，漏了 `apply_patch`（真实写工具）
与 5 个办公写工具（`doc_write`、`csv_write`、`xlsx_write`、`doc_convert`、
`mindmap_create`，均带 `path` 参数、`ReadOnly()=false`）。后果按路径拆开看：

- 轮内（Ledger 回执，`agent.go:1897/1902` 以 `t.ReadOnly()` 记录）：办公工具
  Read/Write 均为 false，`diff` 与 `files` 证据全部失败；
- 跨轮回退（`PathsProvenInSession`，`evidence.go:511` 硬编码 `readOnly=true`）：
  任何带 path 的调用都算读，所以 `files` 证据**部分场景已经能通过**，`diff`
  （要求 Write）仍失败。

原稿 "complete_step 在办公/运维 profile 中无法用 diff/files 证据验证" 对 `files`
有所夸大；真实断点是以 `diff` 验证办公产出。

### 机制澄清（原稿两处误判）

1. **原 3.2（扩展 extractPaths 按工具取 path）删除**：`extractPaths`
   （`evidence.go:588`）本就与工具无关，统一提取 `path/file_path/notebook_path/
   source_path/destination_path/paths/file_paths` 标量与列表键，办公工具的路径
   一直在被提取。唯一缺口是 `doc_convert` 的输出参数 `out_path` 不在键表中。
2. **原 3.3（verifyOfficeFile 二进制验证）不需要**：`complete_step` 的 `diff`
   证据**不做文本 diff**——它只验证"引用的路径存在成功的写入回执"
   （`completestep.go` verifyStepEvidence → `Ledger.HasSuccessfulWrite`）。不存在
   "二进制格式无法行级 diff" 的问题，也就不需要 mtime/turn 窗口校验
   （原稿引用的 `t.agent.TurnStartTime()` 全库不存在）。

### 已实施（2026-09-08）

- `isWriterTool` += `apply_patch`、`doc_write`、`csv_write`、`xlsx_write`、
  `doc_convert`、`mindmap_create`；
- `isReaderTool` += `doc_read`、`csv_read`、`xlsx_read`、`xlsx_query`（跨轮回退
  以此名单判定读语义）；
- `extractPaths` 键表 += `out_path`（doc_convert 产物文件可被引用为证据）；
- 测试：`evidence_test.go`（办公写/读回执、doc_convert 双路径、email_send 不产生
  路径回执、会话回退）、`completestep_test.go`（办公 diff/files 轮内与跨轮验证）。
  `go test ./internal/evidence/... ./internal/tool/... ./internal/agent/... ./internal/boot/...` 全绿。

### 明确不做 / 另立

- **无 path 参数的工具不进名单**：`email_send`/`email_read`（邮箱域）加入
  isWriterTool 产生不出任何路径回执，无意义；
- **netdev 不走文件证据**：`netdev_exec`/`netdev_netconf` 改的是远端设备、无本地
  路径，塞进 isWriterTool 同样产生不出回执。现状是 `verification` 证据已认
  `netdev_exec`（`HasSuccessfulCommand`，`evidence.go:101` 与 bash 同列）；配置
  变更的 sign-off 对接 netdev 已有的 OpStep 操作台账——**已拆出并实施
  `docs/NETDEV_OPSTEP_EVIDENCE_SPEC.md`**（伪路径 `device:<name>` +
  appendOpStep→回执桥接 + complete_step 台账验证，2026-09-08 落地）。

---

## Spec-4：主终端接 PTY（改造为"接线"任务）

### 问题（复核后修正）

主终端（TerminalPanel v1）仅一次性 `RunShell`（前端 `TerminalPanel.tsx:203`
本地输入框，后端 `desktop/app.go:1393` → `control/controller.go:1347` one-shot
exec）。**但 PTY 的端到端代码大部分已经存在、只是没接线**：

- 后端：`desktop/pty_windows.go`（ConPTY）完整的 `ptySession/ptyManager` 与
  Wails 绑定 `PTYCreate/PTYCreateForTab/PTYWrite/PTYRead/PTYResize/PTYKill/
  PTYAlive`，文件头自述 "TerminalPanel v2 的后端"；
- 前端：`components/TerminalSession.tsx`（xterm.js 消费上述绑定）已写好但
  **未在任何地方挂载（死代码）**；`lib/bridge.ts:197-215` 已有声明；
- 缺口：仅 Windows 构建（无 pty_unix.go 存根），且 TerminalPanel 未提供入口。

设备侧对照：DeviceTerminal（netdev）已通过 `NetDevHumanTTY*`
（`internal/netdev/humantty.go`，SSH 伪终端 + 全程录制）实现 PTY，仅在
netdev 界面的设备标签页渲染。

### codex 参考

codex 使用 `portable-pty` crate 实现跨平台 PTY：
- `spawn_pty_process` / `spawn_pipe_process` 双轨
- `ProcessHandle` 统一抽象（stdin writer、stdout reader、exit code、kill、resize）
- 进程组管理：setsid、PGID kill、parent death signal

### 方案（从"新建执行器"改为"接线 + 跨平台收尾"）

#### 4.1 剩余工作清单

1. **TerminalPanel v2 接线**：本地标签页挂载既有 `TerminalSession.tsx`，
   走既有 `PTYCreateForTab`（已支持本地 cmd.exe / wsl / docker exec / ssh），
   保留 v1 输入框作为 pipe 模式或直接替换；
2. **非 Windows 存根**：新增 `desktop/pty_stub.go`（`//go:build !windows`），
   PTYCreate 返回明确错误并在前端降级为 pipe 一次性执行（codex 的双轨思路，
   但复用既有 RunShell，不需要新的 internal/shell 执行器抽象）；
3. **模式切换 UI**：标签页 `mode: 'pipe' | 'pty'`。

#### 4.2 分 Profile 终端策略

| Profile | 默认模式 | 可切换 |
|---------|---------|--------|
| dev | pipe（一次性） | 可切换到 PTY（vim, top 等） |
| cowork | pipe（一次性） | 可切换到 PTY |
| netdev | pipe + DeviceTerminal | 设备终端默认 PTY（已有） |

**撤回原稿的 "xterm.js + WebSocket 双向流"**：与现有 Wails 绑定（轮询 PTYRead）
架构冲突，桌面单机场景没有必要引入 WebSocket。工作量 5 天 → **~2 天**。

---

## Spec-5：wire 层补 event.Item 映射

### 问题（复核确认）

后端 `ItemAdapter`（`internal/event/itemadapter.go:84`，`agent.go:776` 挂载为
sink 包装）正确生成 `Item` 事件（started/delta/completed），但**两处** wire 层
均未映射：`internal/serve/wire.go`（kindNames:143-163 无 Item、wireEvent:10-26
无字段、toWire:179-256 无 case）与 `desktop/wire.go`（kindNames:152-172、
wireEvent:17-31、toWire:188-269 同样缺失；`desktop/tabs.go:551` toWireTab 包装
toWire，Wails EventsEmit 与移动网桥转发都走它）。事件实际以 `{"kind":""}` 空帧
发出，被前端 `useController.ts` 的 `default: return s`（:732）忽略——效果等价于
丢弃。前端 `types.ts` 的 EventKind 联合亦无 `"item"`。目前 event.Item 在 event
包之外零消费，本 Spec 纯为增量渲染铺路。

### codex 参考

codex 的 `EventMsg` 是 tagged enum（`#[serde(tag = "type")]`），包含：
- `ItemStarted` / `ItemCompleted`：包裹 `TurnItem` payload
- Delta 事件：`AgentMessageContentDelta`、`ReasoningContentDelta`、`PlanDelta`
- 每个 delta 携带 `(thread_id, turn_id, item_id, delta)` 四元组
- TUI 直接消费 canonical item model

### 方案

#### 5.1 两处 wire.go 同步补映射

```go
// internal/serve/wire.go 与 desktop/wire.go 各自：
kindNames[event.Item] = "item"

type wireEvent struct {
    // ... existing fields ...
    Item *wireItemEvent `json:"item,omitempty"`
}

type wireItemEvent struct {
    ID       string `json:"id"`
    Kind     string `json:"kind"`       // "text" | "tool_call" | "reasoning" | ...
    Phase    string `json:"phase"`      // "started" | "delta" | "completed"
    Delta    string `json:"delta,omitempty"`
    Content  string `json:"content,omitempty"`
    ParentID string `json:"parentId,omitempty"`
}

// toWire 补充 case
case event.Item:
    wire.Item = &wireItemEvent{ /* ... */ }
```

#### 5.2 前端 types.ts 补类型

```typescript
interface WireItemEvent {
  id: string
  kind: 'text' | 'tool_call' | 'reasoning' | 'notice' | 'approval'
  phase: 'started' | 'delta' | 'completed'
  delta?: string
  content?: string
  parentId?: string
}
type EventKind = ... | 'item'
interface WireEvent { /* ... */ item?: WireItemEvent }
```

#### 5.3 前端 reducer 处理

```typescript
// useController.ts — applyEvent 新增 case
case 'item': {
  if (!e.item) return s
  const { id, kind, phase, delta, content, parentId } = e.item
  return updateItemInTranscript(s, id, { kind, phase, delta, content, parentId })
}
```

#### 5.4 迁移策略

**Phase 1**（本 spec）：双 wire 层补映射，前端消费 item 事件更新现有 UI；
**Phase 2**（未来）：前端逐步从 legacy 事件迁移到 item 事件，最终移除 legacy 路径。

---

## Spec-6：MCP 通知处理增强

### 问题（复核确认，一处措辞修正）

三种传输层丢弃通知，其中 **SSE 连 progress 通知都不处理**（原稿 "全部丢弃非
progressToken 通知" 对 SSE 不准确）：

- stdio：`transport_stdio.go` readLoop（:440-496）仅处理 elicitation 与按
  progressToken 路由的 progress（:466-481），其余通知在 :482 丢弃（:44-47 结构体
  注释自述 "其他服务端通知仍然被丢弃"）；
- SSE：`transport_sse.go` dispatch（:136-155）对 ID==nil 一律忽略（:145-146），
  无任何 progress 处理；
- HTTP：`transport_http.go` readSSEResponse（:174-227）跳过所有与调用响应 ID
  不匹配的帧（:195-196）。

`notifications/tools/list_changed` 在 internal/plugin 中零引用；无 onNotification
回调。已有缓存 `internal/plugin/cache.go`（:48 cacheVersion、:95
LoadCachedSchema，按配置指纹 expectedHash 失效）**只认指纹、不认 list_changed**。

### codex 参考

codex 用 `LoggingClientHandler` 实现 `rmcp::ClientHandler`，所有通知仅日志记录，
不触发自动刷新。工具刷新需手动 `RefreshMcpServers`。codex 另有
`McpToolCatalogCache`（LRU + TTL 30 分钟 + generation-based fetch）。

### 方案

#### 6.1 stdio 传输层通知路由

```go
// transport_stdio.go — readLoop 改造
case "notifications/tools/list_changed":
    s.onNotification(Notification{Type: NotifToolListChanged, ServerID: s.serverID})
    continue
case "notifications/resources/updated":
    if params, ok := msg.Params.(map[string]interface{}); ok {
        uri, _ := params["uri"].(string)
        s.onNotification(Notification{Type: NotifResourceUpdated, ServerID: s.serverID, URI: uri})
    }
    continue
case "notifications/prompts/list_changed":
    s.onNotification(Notification{Type: NotifPromptListChanged, ServerID: s.serverID})
    continue
default:
    slog.Debug("dropped MCP notification", "method", method, "server", s.serverID)
    continue
```

#### 6.2 SSE/HTTP 传输层同步改造（SSE 顺带补 progress 路由）

```go
// transport_sse.go — dispatch：ID==nil 时解析 method 分发通知
if env.ID == nil {
    if env.Method != "" {
        s.onNotification(parseNotification(env))
    }
    return
}

// transport_http.go — readSSEResponse 同型改造
if resp.ID == nil {
    if resp.Method != "" {
        s.onNotification(parseNotification(resp))
    }
    continue
}
```

#### 6.3 通知处理器

```go
// internal/plugin/notification_handler.go
type NotificationHandler struct {
    toolCache *ToolCatalogCache
    mu        sync.RWMutex
}

func (h *NotificationHandler) Handle(n Notification) {
    switch n.Type {
    case NotifToolListChanged:
        h.toolCache.Invalidate(n.ServerID)
        slog.Info("MCP tool list changed, cache invalidated", "server", n.ServerID)
    case NotifResourceUpdated:
        slog.Info("MCP resource updated", "server", n.ServerID, "uri", n.URI)
    case NotifPromptListChanged:
        h.toolCache.InvalidatePrompts(n.ServerID)
        slog.Info("MCP prompt list changed", "server", n.ServerID)
    }
}
```

#### 6.4 工具缓存：并入既有 cache.go，不另起炉灶

原稿的 `ToolCatalogCache` 是全新并行缓存，与 `cache.go`（配置指纹失效的
schema 缓存）功能重叠。改为**扩展 cache.go**：在既有 CacheEntry 上加
`Dirty bool` 与 `Generation uint64`，`Invalidate(serverID)` 置脏，`Get` 命中条件
从 "指纹一致" 放宽为 "指纹一致 && 未置脏 && 未超 TTL"。TTL/LRU 上限借鉴 codex
（30 分钟 / 30 条）。

---

## 实施优先级（2026-09-08 修订）

### P0 — 必须修复（影响核心功能）

| Spec | 工作量 | 说明 |
|------|--------|------|
| Spec-3 | **已实施** | 名单扩展 + out_path 提取 + 测试已落地；净残留仅 netdev 台账对接（另立 Spec） |

### P1 — 重要改进（影响用户体验）

| Spec | 工作量 | 说明 |
|------|--------|------|
| Spec-4 | **~2 天** | 接线既有 ConPTY + TerminalSession（后端/前端均已存在，仅挂载 + 非 Windows 存根），提前到 P1 首位 |
| Spec-1 | ~5 天 | 分层 ToolCard；cowork 以 BrowserActionCard 覆盖 browser/screen/window 大头 |
| Spec-2 | ~3 天 | 对话内搜索，超越 codex |

### P2 — 架构优化（技术债清理）

| Spec | 工作量 | 说明 |
|------|--------|------|
| Spec-5 | ~3 天 | 双 wire.go 补 Item 映射，为增量渲染打基础 |
| Spec-6 | ~2 天 | MCP 通知处理，动态 MCP 服务器场景需要 |

**总工作量估算：~15 天**（原 ~20 天；Spec-3 落地、Spec-4 改为接线任务）

---

## 与 codex 的差距变化

| 能力维度 | 当前状态 | 实施后 |
|---------|---------|--------|
| 工具渲染 | ⚠️ 5 项专属 UI / 全库 ~125 工具 | ✅ 分类卡片 + 类聚合，三界面主路径覆盖 |
| 对话搜索 | ❌ 无 | ✅ Ctrl+F + 高亮（超越 codex） |
| 办公证据 | ✅ **已修复**（2026-09-08） | 写/读名单 + out_path 提取落地 |
| 终端交互 | ⚠️ 仅一次性（ConPTY 后端已建未接线） | ✅ pipe + PTY 双轨（接线即得） |
| 事件架构 | ⚠️ 后端有、双 wire 层无 | ✅ 端到端贯通 |
| MCP 兼容 | ⚠️ 通知丢弃 | ✅ 日志 + 缓存失效（并入 cache.go） |

实施后 fairpeer 在**对话搜索**方面将超越 codex（codex 没有），在**办公证据链**
方面已补齐（codex 无此概念），其余维度对齐或接近 codex 水平。

---

## 修订记录

- **2026-09-08（晚）**：Spec-2/4/5/6 全量落地、Spec-1 首批落地（bash/办公写/email/
  netdev_probe 卡片），状态表同步；实现中顺手修了三个既有断流/卫生问题——
  httpTransport 通知回调与请求周期的互斥自锁、browser-mirror/dash-jump-filter
  两个过期测试 fixture、test:all 脚本对 5 个 vitest 风格测试的重复 tsx 执行。
- **2026-09-08**：逐条代码复核后修订——基线数字按 profile 分区注册更正
  （弃用 "72 工具"，源自过时的 DEV_COWORK_TOOL_COMPARISON.md 2026-07-05）；
  删除不存在工具名 xlsx_edit/mindmap_read/ppt_create；Spec-3 拆出已实施部分
  （名单 + out_path + 测试）、撤回 3.2（extractPaths 本就工具无关）与 3.3
  （diff 证据是回执制，无文本 diff）、netdev 改判为对接 OpStep 台账另立 Spec；
  Spec-4 从"从零建双轨执行器 + WebSocket"改为"接线既有 ConPTY/TerminalSession
  + 非 Windows 存根"（5 天→2 天，提前至 P1 首位）；Spec-5 明确两处 wire.go
  （internal/serve 与 desktop）；Spec-6 修正 SSE 连 progress 都丢的措辞、
  工具缓存改为并入既有 cache.go。
