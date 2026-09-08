# netdev OpStep 台账 × complete_step 证据链对接规格书

> 从 FAIRPEER_CODEX_GAP_SPEC Spec-3 拆出（2026-09-08 复核裁决：netdev 配置变更
> 不走文件证据——`netdev_exec`/`netdev_netconf` 无本地 path，塞进 isWriterTool
> 产生不出路径回执）。日期：2026-09-08 | 状态：**已实施（同日）**
>
> As-built 与草案的差异：跨轮查询未走"controller 注入"，而是 builtin 直接
> `netdev.ListOpSteps`（netdev 不 import builtin，无环；非 netdev profile 下
> 台账目录为空，device: 引用自然拒绝）；桥接在 `appendOpStep(ctx, …)` 内经
> ctx 取证据 Ledger（desktop 多标签各有 Ledger，全局回调会串台）。
> 前端 device: 徽标渲染未包含在本批（卡片当前按普通路径显示）。

---

## 背景与现状

- **证据链**（`internal/evidence` + `builtin/completestep.go`）：`complete_step`
  的 `diff`/`files` 证据是**回执制**——验证"引用的路径有成功的写入/读取回执"，
  不做内容 diff。回执由 `ReceiptFromToolCall` 从工具调用的 path 参数派生。
- **OpStep 操作台账**（`internal/netdev/writeauth.go:42`）：每条写步（直写/提案步/
  回退本身）一行——`ID/At/Actor/Device/Command/Status(ok|device-error|failure)/
  Turn/PreID/PostID/DiffSummary/Error/RollbackTo/Link`，`Manager.appendOpStep` 落盘、
  `ListOpSteps(device, limit)` 查询，**已按 Turn 锚定**。
- **断点**：`verification` 证据已认 `netdev_exec` 命令（`HasSuccessfulCommand`，
  evidence.go 与 bash 同列），但"配置变更本身做成了什么"（pre/post diff、落在哪台
  设备）无法作为证据被签核——模型只能退回 `manual`。

## 方案

### 1. 伪路径约定：`device:<name>`

`complete_step` 证据的 `paths` 支持设备引用 `"device:SW-01"`。验证分支：
路径带 `device:` 前缀 → 走 OpStep 台账核对，不查文件回执。

### 2. 写路径桥接：appendOpStep → 证据台账

`Manager.appendOpStep` 落账后，经回调（controller 注入，同 `EmitExpertCollab`
模式）把 OpStep 映射进证据 `Ledger`：

```
Receipt{
  ToolName: "netdev_opstep",
  Success:  Status == "ok",
  Command:  Command,
  Paths:    []string{"device:" + Device},
  Write:    true,
}
```

轮内验证直接命中回执；跨轮回退用 `ListOpSteps` 按 `Turn` 锚定 + `Status`
核对（`PathsProvenInSession` 的台账版对应物）。

### 3. complete_step 验证语义

- `diff` 证据引用 `device:X`：本轮（或跨轮回退：台账任意轮）存在 `Device==X`、
  `Status==ok`、`DiffSummary` 非空的 OpStep 行 → 通过；否则拒绝并列出本轮台账行
  （复用 `receiptHint` 的"实际发生过什么"提示模式）。
- `device-error`/`failure` 行**不算**证据（与失败回执不计入的既有语义一致），
  拒绝信息提示先修复或改走 `manual` 说明。
- 前端 `evidence.kind=diff` 的卡片渲染 `device:` 路径时显示设备徽标 + 台账
  DiffSummary（复用 netdev 台账视图样式）。

### 4. 明确不做

- `netdev_exec`/`netdev_netconf` 仍**不进** `isWriterTool`（无本地 path，加入
  产生不出回执——2026-09-08 复核结论维持）。
- 不引入 `verifyCommandFromSession` 之外的命令宽松匹配；台账 `Command` 精确
  对齐 `CommandMatches` 既有语义即可。

## 实施清单（估 2 天）

1. `evidence`：`Receipt` 无需改结构（伪路径即可）；新增 `HasSuccessfulOpStep(
   device string)` + 跨轮 `ListOpSteps` 回退（netdev 侧提供只读查询，避免
   evidence 反向依赖 netdev——经 controller 注入查询函数）。
2. `netdev/writeauth.go`：`appendOpStep` 尾部触发 `onOpStep` 回调（Manager
   字段，controller 装配时注入）。
3. `completestep.go`：`diff`/`files` 分支识别 `device:` 前缀走台账验证；
   拒绝提示带台账行摘要。
4. `boot.go`：netdev profile 装配回调 + 查询注入。
5. 测试：直写→回执→diff 证据通过；failure 行不算证据；跨轮（Turn 锚定）
   回退；`device:` 与文件路径混引；非 netdev profile 不受影响（无回调 =
   现行为）。

## 验收

- cowork/dev 行为零变化（无 OpStep 回调，`device:` 路径按普通路径拒绝）。
- netdev 会话中：`complete_step` 引用 `device:X` 且台账有本轮 ok 行 →
  host-verified；引用未写过的设备 → 拒绝并列出本轮台账。
