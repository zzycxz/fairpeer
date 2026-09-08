# fairpeer 编码·办公·运维 能力补齐规格书

> 基于 codex 源码研究 + fairpeer 三轮源码审计（9 个子代理交叉验证）生成。
> 日期：2026-09-07 | 状态：Draft

---

## 总览

| # | 不足项 | 严重度 | codex 参考 | 改进方案 |
|---|--------|--------|-----------|---------|
| 1 | 工具卡片渲染低填充 | 中 | HistoryCell trait + 按工具类型分组件 | 分层 ToolCard 体系 |
| 2 | 对话内无全文搜索 | 中 | codex 也没有（仅有 Ctrl+T overlay） | 超越 codex：Ctrl+F 搜索 + 高亮 |
| 3 | 证据链不认办公工具 | 高 | update_plan + ParsedCommand 分类 | 扩展 isWriterTool + 办公证据路径 |
| 4 | 主终端非 PTY | 中 | portable-pty + ProcessHandle 抽象 | 双轨终端（pipe + PTY） |
| 5 | Item 事件前端未消费 | 低 | SQ/EQ + TurnItem tagged enum + delta | wire 层补 event.Item 映射 |
| 6 | MCP 通知被丢弃 | 中 | LoggingClientHandler（仅日志） | 日志 + tools/list_changed 自动刷新 |

---

## Spec-1：分层 ToolCard 渲染体系

### 问题

`toolCards.tsx` 注册表仅 5 项（web_search + 4 netdev_*），72 个工具共用单一 `ToolCard.tsx`（322 行），未注册工具显示为原始 JSON。

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
  category?: 'read' | 'write' | 'search' | 'shell' | 'office' | 'netdev'
  profile?: ProfileKind[]  // 限定在哪些 profile 下生效
}

const registry: Record<string, ToolCardSpec> = {
  // --- 编码 (dev) ---
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

  // --- 办公 (cowork) ---
  doc_write:    { category: 'office', body: DocWriteCard },
  doc_read:     { category: 'office', body: DocReadCard },
  csv_write:    { category: 'office', body: CsvWriteCard },
  xlsx_write:   { category: 'office', body: XlsxWriteCard },
  xlsx_query:   { category: 'office', body: XlsxQueryCard },
  mindmap_create: { category: 'office', body: MindmapCard },
  ppt_create:   { category: 'office', body: PptCard },
  email_send:   { category: 'office', body: EmailCard },

  // --- 运维 (netdev) ---
  netdev_exec:     { category: 'netdev', forceOpen: true, noQuiet: true },
  netdev_netconf:  { category: 'netdev', forceOpen: true, noQuiet: true },
  netdev_discover: { category: 'netdev', forceOpen: true, noQuiet: true },
  netdev_baseline: { category: 'netdev', forceOpen: true, noQuiet: true },
}
```

#### 1.2 新增专用卡片组件

| 组件 | 文件 | 功能 |
|------|------|------|
| `ReadFileCard` | `toolcards/ReadFileCard.tsx` | 语法高亮预览，行号，可折叠 |
| `WriteFileCard` | `toolcards/WriteFileCard.tsx` | 文件路径 + 行数变化摘要 |
| `EditFileCard` | `toolcards/EditFileCard.tsx` | 内联 UnifiedDiff 预览（已有管线） |
| `PatchCard` | `toolcards/PatchCard.tsx` | 多文件 diff 摘要 + 展开详情 |
| `ShellCard` | `toolcards/ShellCard.tsx` | 状态指示器 + 头尾截断 + 展开全文 |
| `GrepCard` | `toolcards/GrepCard.tsx` | 匹配数 + 前 N 条结果高亮 |
| `DocWriteCard` | `toolcards/DocWriteCard.tsx` | 文档路径 + 字数变化 + 格式预览 |
| `XlsxWriteCard` | `toolcards/XlsxWriteCard.tsx` | 表格名 + 行数变化 |
| `EmailCard` | `toolcards/EmailCard.tsx` | 收件人 + 主题 + 摘要 |

#### 1.3 Exploring 分组模式

借鉴 codex 的 `is_exploring_call`，将连续的只读工具（`read_file`、`ls`、`grep`、`glob`、`web_fetch`、`doc_read`、`csv_read`、`xlsx_read`）折叠为一个 "探索中" 摘要行：

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

Transcript.tsx（1303 行）无搜索功能。长对话中无法定位特定内容。

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

当前 `SearchSessions` 仅搜索当前 profile 目录。增强方案：

```go
// search.go — 新增跨 profile 搜索
func SearchAllProfiles(userDir, query string) []SearchHit {
    var all []SearchHit
    for _, profile := range []string{"", "cowork", "netdev"} {
        dir := sessionDirFor(userDir, profile)
        hits := SearchSessions(dir, query)
        for i := range hits {
            hits[i].Profile = profile  // 标注来源 profile
        }
        all = append(all, hits...)
    }
    // 按修改时间排序
    sort.Slice(all, func(i, j int) bool {
        return all[i].ModTime.After(all[j].ModTime)
    })
    return all
}
```

---

## Spec-3：证据链认办公工具

### 问题

`evidence.go:570-577` 中 `isWriterTool` 仅列出 7 个编码工具，缺失 `doc_write`、`csv_write`、`xlsx_write`、`mindmap_create`、`doc_convert`。导致 `complete_step` 在办公/运维 profile 中无法用 `diff`/`files` 证据验证步骤。

### codex 参考

codex 无 "evidence chains" 概念。其 `ParsedCommand` 分类（Read/ListFiles/Search/Unknown）仅用于 UI 分组。`update_plan` 工具用 `StepStatus`（Pending/InProgress/Completed）做步骤跟踪，不涉及工具分类。

### 方案

#### 3.1 扩展 isWriterTool / isReaderTool

```go
// evidence.go

func isWriterTool(name string) bool {
    switch name {
    // 编码
    case "write_file", "edit_file", "multi_edit", "move_file",
         "notebook_edit", "delete_range", "delete_symbol",
         "apply_patch":
        return true
    // 办公
    case "doc_write", "csv_write", "xlsx_write", "xlsx_edit",
         "mindmap_create", "doc_convert", "ppt_create",
         "email_send":
        return true
    // 运维
    case "netdev_exec", "netdev_netconf":
        return true
    default:
        return false
    }
}

func isReaderTool(name string) bool {
    switch name {
    // 编码
    case "read_file", "ls", "grep", "glob", "web_fetch", "web_search":
        return true
    // 办公
    case "doc_read", "csv_read", "xlsx_read", "xlsx_query",
         "mindmap_read", "email_read":
        return true
    // 运维
    case "netdev_discover", "netdev_baseline":
        return true
    default:
        return false
    }
}
```

#### 3.2 扩展 extractPaths 支持办公工具参数

```go
// evidence.go — extractPaths 新增办公工具路径提取

case "doc_write", "doc_read", "doc_convert":
    // 办公文档工具使用 "path" 或 "file_path" 参数
    if p, ok := args["path"].(string); ok && p != "" {
        paths = append(paths, p)
    }
    if p, ok := args["file_path"].(string); ok && p != "" {
        paths = append(paths, p)
    }

case "csv_write", "csv_read":
    if p, ok := args["path"].(string); ok && p != "" {
        paths = append(paths, p)
    }

case "xlsx_write", "xlsx_read", "xlsx_query", "xlsx_edit":
    if p, ok := args["path"].(string); ok && p != "" {
        paths = append(paths, p)
    }

case "mindmap_create", "mindmap_read":
    if p, ok := args["path"].(string); ok && p != "" {
        paths = append(paths, p)
    }

case "ppt_create":
    if p, ok := args["path"].(string); ok && p != "" {
        paths = append(paths, p)
    }
```

#### 3.3 complete_step 办公证据增强

办公工具的 `diff` 证据不适用（二进制格式无法行级 diff），改用 `files` 证据类型验证：

```go
// completestep.go — 办公文件验证逻辑
func (t *CompleteStepTool) verifyOfficeFile(path string) bool {
    // 1. 文件存在性检查
    info, err := os.Stat(path)
    if err != nil { return false }

    // 2. 修改时间在当前 turn 时间窗口内
    turnStart := t.agent.TurnStartTime()
    if info.ModTime().Before(turnStart) { return false }

    // 3. 文件非空
    if info.Size() == 0 { return false }

    return true
}
```

---

## Spec-4：双轨终端（pipe + PTY）

### 问题

主终端（TerminalPanel）仅支持一次性 `RunShell` 执行，无 PTY，无法运行交互式程序。PTY 终端（DeviceTerminal）仅限 netdev 界面。

### codex 参考

codex 使用 `portable-pty` crate 实现跨平台 PTY：
- `spawn_pty_process`：交互式 PTY 会话
- `spawn_pipe_process`：非交互式管道执行
- `ProcessHandle`：统一抽象（stdin writer、stdout reader、exit code、kill、resize）
- 进程组管理：setsid、PGID kill、parent death signal

### 方案

#### 4.1 Go 端双轨执行器

```go
// internal/shell/executor.go

type ExecMode int
const (
    ExecModePipe ExecMode = iota  // 非交互，捕获输出
    ExecModePTY                   // 交互，PTY 分配
)

type ExecConfig struct {
    Mode    ExecMode
    Command string
    Cwd     string
    Env     []string
    Rows    uint16  // PTY 行数
    Cols    uint16  // PTY 列数
}

type ProcessHandle struct {
    stdin   io.WriteCloser
    stdout  <-chan []byte
    stderr  <-chan []byte
    exitCh  <-chan int
    kill    func()
    resize  func(rows, cols uint16)
}
```

#### 4.2 PTY 实现（跨平台）

```go
// internal/shell/pty_unix.go (go:build !windows)
// 使用 os/exec + syscall.Setsid + pty.Open()

// internal/shell/pty_windows.go (go:build windows)
// 使用 ConPTY API (CreatePseudoConsole)
```

#### 4.3 前端终端升级

```typescript
// TerminalPanel.tsx — 支持两种模式

interface TerminalTab {
  id: string
  mode: 'pipe' | 'pty'
  title: string
  device?: string  // netdev 设备名
}

// pipe 模式：保留现有一次性执行
// pty 模式：xterm.js + WebSocket 双向流
```

#### 4.4 分 Profile 终端策略

| Profile | 默认模式 | 可切换 |
|---------|---------|--------|
| dev | pipe（一次性） | 可切换到 PTY（vim, top 等） |
| cowork | pipe（一次性） | 可切换到 PTY |
| netdev | pipe + DeviceTerminal | 设备终端默认 PTY |

---

## Spec-5：wire 层补 event.Item 映射

### 问题

后端 `ItemAdapter` 正确生成 `ItemEvent`（started/delta/completed），但 `wire.go` 的 `kindNames` 和 `toWire()` 未映射 `event.Item`，前端 types.ts/reducer 无 "item" 事件类型。事件在序列化边界被静默丢弃。

### codex 参考

codex 的 `EventMsg` 是 tagged enum（`#[serde(tag = "type")]`），包含：
- `ItemStarted` / `ItemCompleted`：包裹 `TurnItem` payload
- Delta 事件：`AgentMessageContentDelta`、`ReasoningContentDelta`、`PlanDelta`
- 每个 delta 携带 `(thread_id, turn_id, item_id, delta)` 四元组
- TUI 直接消费 canonical item model

### 方案

#### 5.1 wire.go 补映射

```go
// wire.go — kindNames 补充
kindNames[event.Item] = "item"

// wireEvent 补充字段
type wireEvent struct {
    // ... existing fields ...
    Item *wireItemEvent `json:"item,omitempty"`
}

type wireItemEvent struct {
    ID        string `json:"id"`
    Kind      string `json:"kind"`       // "text" | "tool_call" | "reasoning" | ...
    Phase     string `json:"phase"`      // "started" | "delta" | "completed"
    Delta     string `json:"delta,omitempty"`
    Content   string `json:"content,omitempty"`
    ParentID  string `json:"parentId,omitempty"`
}

// toWire 补充 case
case event.Item:
    wire.Item = &wireItemEvent{
        ID:    e.Item.ID,
        Kind:  string(e.Item.Kind),
        Phase: string(e.Item.Phase),
        Delta: e.Item.Delta,
        Content: e.Item.Content,
        ParentID: e.Item.ParentID,
    }
```

#### 5.2 前端 types.ts 补类型

```typescript
// types.ts
interface WireItemEvent {
  id: string
  kind: 'text' | 'tool_call' | 'reasoning' | 'notice' | 'approval'
  phase: 'started' | 'delta' | 'completed'
  delta?: string
  content?: string
  parentId?: string
}

// EventKind 新增
type EventKind = ... | 'item'

// WireEvent 新增
interface WireEvent {
  // ... existing fields ...
  item?: WireItemEvent
}
```

#### 5.3 前端 reducer 处理

```typescript
// useController.ts — applyEvent 新增 case
case 'item': {
  if (!e.item) return s
  const { id, kind, phase, delta, content, parentId } = e.item
  // 更新对应 item 的状态
  return updateItemInTranscript(s, id, { kind, phase, delta, content, parentId })
}
```

#### 5.4 迁移策略

**Phase 1**（当前 spec）：wire 层补映射，前端消费 item 事件更新现有 UI
**Phase 2**（未来）：前端逐步从 legacy 事件迁移到 item 事件，最终移除 legacy 路径

---

## Spec-6：MCP 通知处理增强

### 问题

三种 MCP 传输层（stdio/SSE/HTTP）全部丢弃非 progressToken 通知。`notifications/tools/list_changed` 等关键通知被静默忽略。

### codex 参考

codex 用 `LoggingClientHandler` 实现 `rmcp::ClientHandler`，所有通知仅日志记录，不触发自动刷新。工具刷新需手动触发 `RefreshMcpServers`。codex 有 `McpToolCatalogCache`（LRU + TTL 30 分钟 + generation-based fetch）。

### 方案

#### 6.1 stdio 传输层通知路由

```go
// transport_stdio.go — readLoop 改造

case "notifications/tools/list_changed":
    // 通知上层刷新工具列表
    s.onNotification(Notification{
        Type:     NotifToolListChanged,
        ServerID: s.serverID,
    })
    continue

case "notifications/resources/updated":
    if params, ok := msg.Params.(map[string]interface{}); ok {
        uri, _ := params["uri"].(string)
        s.onNotification(Notification{
            Type:     NotifResourceUpdated,
            ServerID: s.serverID,
            URI:      uri,
        })
    }
    continue

case "notifications/prompts/list_changed":
    s.onNotification(Notification{
        Type:     NotifPromptListChanged,
        ServerID: s.serverID,
    })
    continue

// 其余通知仍 drop，但记录 debug 日志
default:
    slog.Debug("dropped MCP notification", "method", method, "server", s.serverID)
    continue
```

#### 6.2 SSE/HTTP 传输层同步改造

```go
// transport_sse.go — dispatch 改造
if env.ID == nil {
    // 通知消息，尝试解析 method
    if env.Method != "" {
        s.onNotification(parseNotification(env))
    }
    return
}

// transport_http.go — readSSEResponse 改造
if resp.ID == nil {
    // 通知消息，记录并继续扫描
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
    toolCache  *ToolCatalogCache
    mu         sync.RWMutex
}

func (h *NotificationHandler) Handle(n Notification) {
    switch n.Type {
    case NotifToolListChanged:
        // 标记缓存为脏，下次工具列表查询时自动刷新
        h.toolCache.Invalidate(n.ServerID)
        slog.Info("MCP tool list changed, cache invalidated", "server", n.ServerID)

    case NotifResourceUpdated:
        // 通知 agent 层资源已更新（可选：自动重新读取）
        slog.Info("MCP resource updated", "server", n.ServerID, "uri", n.URI)

    case NotifPromptListChanged:
        h.toolCache.InvalidatePrompts(n.ServerID)
        slog.Info("MCP prompt list changed", "server", n.ServerID)
    }
}
```

#### 6.4 工具缓存（借鉴 codex McpToolCatalogCache）

```go
// internal/plugin/tool_cache.go

type ToolCatalogCache struct {
    entries    map[string]*CacheEntry  // serverID -> entry
    ttl        time.Duration           // 默认 30 分钟
    maxEntries int                     // 默认 30
    mu         sync.RWMutex
}

type CacheEntry struct {
    Tools      []Tool
    FetchedAt  time.Time
    Generation uint64
    Dirty      bool  // 通知标记为脏
}

func (c *ToolCatalogCache) Get(serverID string) ([]Tool, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    entry, ok := c.entries[serverID]
    if !ok || entry.Dirty || time.Since(entry.FetchedAt) > c.ttl {
        return nil, false  // 需要重新获取
    }
    return entry.Tools, true
}

func (c *ToolCatalogCache) Invalidate(serverID string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    if entry, ok := c.entries[serverID]; ok {
        entry.Dirty = true
    }
}
```

---

## 实施优先级

### P0 — 必须修复（影响核心功能）

| Spec | 工作量 | 说明 |
|------|--------|------|
| Spec-3 | 2 天 | 证据链认办公工具，否则 cowork/netdev 的 complete_step 形同虚设 |

### P1 — 重要改进（影响用户体验）

| Spec | 工作量 | 说明 |
|------|--------|------|
| Spec-1 | 5 天 | 分层 ToolCard 渲染，三个界面都受益 |
| Spec-2 | 3 天 | 对话内搜索，超越 codex |
| Spec-4 | 5 天 | PTY 终端，编码和运维场景刚需 |

### P2 — 架构优化（技术债清理）

| Spec | 工作量 | 说明 |
|------|--------|------|
| Spec-5 | 3 天 | Item 事件前端消费，为未来增量渲染打基础 |
| Spec-6 | 2 天 | MCP 通知处理，动态 MCP 服务器场景需要 |

**总工作量估算：~20 天**

---

## 与 codex 的差距变化

| 能力维度 | 当前状态 | 实施后 |
|---------|---------|--------|
| 工具渲染 | ⚠️ 5/72 工具有专属 UI | ✅ 全量覆盖 |
| 对话搜索 | ❌ 无 | ✅ Ctrl+F + 高亮（超越 codex） |
| 办公证据 | ❌ complete_step 失效 | ✅ 三 profile 全支持 |
| 终端交互 | ⚠️ 仅一次性 | ✅ pipe + PTY 双轨 |
| 事件架构 | ⚠️ 后端有、前端无 | ✅ 端到端贯通 |
| MCP 兼容 | ⚠️ 通知丢弃 | ✅ 日志 + 缓存失效 |

实施后 fairpeer 在**对话搜索**方面将超越 codex（codex 没有），在**办公能力**方面补齐证据链（codex 无此概念），其余维度对齐或接近 codex 水平。
