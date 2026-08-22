# 4-1/4-2/5-2/5-7 架构设计规格书

> 日期：2026-08-22 | 状态：设计定稿，待实施
> 前置：M0–M3 全部完成（47/51），本文档覆盖剩余 4 项的实现规格。

---

## 一、4-1 事件 Item 化（Event Model Restructuring）

### 目标
把 18 种扁平 event.Kind 重组为 `item_started / item_delta / item_completed` 三层模型，前端从"翻译事件"变为"渲染 item"。这是 mobile bridge / 远程接入的战略前置。

### 新事件模型

```go
// ItemKind identifies what a timeline item IS (not what happened to it).
type ItemKind string

const (
    ItemUserMessage      ItemKind = "user_message"
    ItemAgentMessage     ItemKind = "agent_message"
    ItemReasoning        ItemKind = "reasoning"
    ItemToolCall         ItemKind = "tool_call"
    ItemApproval         ItemKind = "approval"
    ItemCompaction       ItemKind = "compaction"
    ItemNotice           ItemKind = "notice"
    ItemTurnSummary      ItemKind = "turn_summary"
)

// ItemEvent is the wire form of a timeline item transition.
type ItemEvent struct {
    Kind    string      `json:"kind"`    // "item_started" | "item_delta" | "item_completed"
    ItemID  string      `json:"item_id"` // stable across all three phases
    ItemKind ItemKind   `json:"item_kind"`
    Delta   string      `json:"delta,omitempty"` // item_delta only
    Item    interface{} `json:"item,omitempty"`  // full item on started/completed
}
```

### 迁移策略（两步走）

**Step 1**（可独立发版）：agent 在发旧 Kind 的同时发 ItemEvent，前端可选择性消费。旧事件继续工作，零破坏。

**Step 2**：前端 reducer 切换到 ItemEvent 消费，旧事件标记 deprecated。mobile bridge 直接消费 ItemEvent，跳过旧模型。

### 影响面

| 层 | 变更 |
|----|------|
| agent.go 发射点 | 在 Emit() 前包一层 itemAdapter（~200 行新增） |
| desktop/wire.go | wireEvent 增加 items 字段 |
| useController.ts | 新增 item_started/delta/completed case |
| present sidecar | 记录 ItemEvent 而非旧 Kind（4-2 的前置） |
| serve/bot/ACP/TUI | 可选择性忽略 ItemEvent（向后兼容） |

---

## 二、4-2 快照 + 增量协议（Snapshot + Delta）

### 目标
替代 present sidecar 的 100-turn 上限全量重放。任意长度会话秒开。

### 协议

```
SessionSnapshot {
    session_id     string
    revision       int      // bumps on compaction/rewrite
    items          []Item   // full item list at snapshot time
}

DeltaEvent {
    session_id     string
    from_revision  int
    to_revision    int
    operations     []Operation  // append/update/remove
}
```

### 存储布局

```
<session-dir>/
  <session-id>.jsonl          // 模型上下文（已有）
  <session-id>.present.jsonl  // 全量 item 日志（替代现有 sidecar）
  <session-id>.snapshot.json  // 定期快照（每 50 items 或 30s）
```

### 加载流程

1. 读 snapshot.json → 恢复到 revision N
2. 读 present.jsonl 尾部 → apply deltas N→HEAD
3. 若 snapshot 缺失 → 从 present.jsonl 头全量重放（降级为现有行为）

### 快照触发

- items 数量增量 ≥ 50
- 距上次快照 ≥ 30s 且有增量
- compaction 完成后（revision 变更）

---

## 三、5-2 崩溃 Turn 恢复

### 前置
4-1 的 ItemEvent 模型（恢复需要知道 turn 边界和 item 状态）。

### 设计

```
CrashRecovery {
    session_id    string
    turn_id       string
    started_at    time.Time
    items         []ItemEvent  // items emitted before the crash
    last_usage    *Usage
}
```

- `crash_recovery.json` 写在 session-dir 下，turn 开始时创建，turn 完成时删除
- 应用启动时扫描所有 session-dir 的 crash_recovery.json
- 恢复选项：继续 turn（重新注入 steer/recovery prompt）/ 丢弃 / 查看
- 桌面启动时弹恢复面板（可选）

---

## 四、5-7 权限决策粒度

### 设计

在 ApprovalRequest 事件中增加可选的 `Decision[]`：

```go
type Decision struct {
    Label       string   // "Allow for this file"
    Scope       string   // "once" | "session" | "always" | "path:<glob>" | "host:<pattern>"
    Restrictions *Restrictions
}

type Restrictions struct {
    AllowedPaths []string // glob patterns
    DeniedPaths  []string
    AllowedHosts []string // for network
}
```

- 默认 3 档（once/session/always）不变——不增加普通用户认知负担
- 高级用户通过设置启用"细粒度决策"，审批卡多出 path/host 选项
- permission.Rule 增加 `PathPattern` 和 `HostPattern` 字段
- Gate.Evaluate 匹配时检查路径/主机模式

---

## 五、实施顺序建议

```
4-1 Step 1（双轨发射，零破坏）→ 4-1 Step 2（前端切换）→ 4-2（快照协议）→ 5-2（崩溃恢复）→ 5-7（权限粒度）
```

每步独立可发版。4-1 Step 1 是后续所有项的地基，建议最先。
