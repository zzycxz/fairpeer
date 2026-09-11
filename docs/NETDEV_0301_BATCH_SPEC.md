# NETDEV 0.3.1 批次规格 —— 排查遗留清仓（批次 A/B）与待裁决项

- 日期：2026-09-12
- 来源：两轮专项排查的遗留台账（"5 轮 × 3 子任务主题排查" + "5 轮 × 3 子任务逐行精读"，全部发现已修或记录；本 spec 收录**记录未修**部分并规格化）
- 基线：commit `7e00e3a2`（feat(netdev) 0.3.0 已提交，35 文件）
- 关联：`FDE_AIINFRA_OPS_GAP_SPEC.md`（承接分期）、`NETDEV_SPEC_V2.md`（§1.4 不变量/§10 UI 契约，全文适用）、`GPU_TODO.md`（批次 C 的 dogfooding 门槛载体）、`CHANGELOG.md [0.3.0]`
- 分工：批次 A 无设计分歧，做完即清；批次 B 每项给出方案与**推荐**；§四 待裁决项需负责人拍板后才动。

---

## 一、批次 A：卫生清仓（无设计分歧，做完即清）

### A1. prevUptimes 加锁
- **问题**：`alert.go:104` 的 `prevUptimes` 是裸 map，5 处裸访问（:141/:152/:192/:221 及清理段）。`evaluateAlerts` 目前是单飞轮询 goroutine，正确性靠约定；但它是导出方法 `PollHealthOnce` 的一部分，测试直接改写该 map，一旦出现并发评估即 data race（CI race 通道可旗标）。相邻的 `alertStreaks` 已有 `alertStreaksMu`，纪律不对称（逐行精读 R3 P3-3）。
- **修法**：新增 `prevUptimesMu`，提供 `prevUptime(name) int64` / `setPrevUptime(name string, sec int64)`（sec>0 才写，沿用"失败轮不覆写基线"语义）/ `prunePrevUptimes(seen map[string]bool)` 三个 helper，替换全部裸访问。
- **落点**：`internal/netdev/alert.go`。
- **验收**：`go test -race ./internal/netdev/ -run TestGPUAlert -count=1` 通过；行为无变化。

### A2. 备份调度失败静默
- **问题**：`desktop/netdev_app.go` startBackupScheduler 的 `RunBackup` 失败无 else——"定时备份到底跑没跑"无处可答（逐行精读 R3 #5）。巡检调度器已有 `stamp.Note = err.Error()` 先例。
- **修法**：补 `else { slog.Warn("scheduled netdev backup failed", "err", err) }`；可选再落一条 `Kind:"backup"` 的 ScheduleStamp（与巡检合规卡同构）。
- **落点**：`desktop/netdev_app.go` startBackupScheduler。
- **验收**：代码审阅 + 人为制造失败（改错密钥）日志出现。

### A3. 向导"已保存待验证"态
- **问题**：AlertSetupWizard 保存成功而通知测试失败时，catch 只 setErr，UI 停在带「取消」按钮的步骤 3——但规则/轮询/出口**已落库**，「取消」暗示什么都没保存；`testFail` 分支不可达（setStep(4) 只在测试成功后调用）为死代码（逐行精读 R4 P3-7）。
- **修法**：`SetNetDevSettings` 成功即 `setSaved(true)`；步骤 3 的放弃按钮在 saved 态改文案「稍后验证」且 onClose 不清提示；删除/改写 `testFail` 死分支。
- **落点**：`desktop/frontend/src/components/netdev/AlertSetupWizard.tsx:85-88/181/192`。
- **验收**：mock 下让 NetDevNotifyTest 抛错，UI 显示"已保存，测试失败可稍后重试"，无误导按钮。

### A4. 回退失败路径缺 EndedAt 与报告
- **问题**：`CutoverRollback` 失败分支（`cutover.go` "回退失败（已回滚 %d/%d…"）置 failed 后直接 return——无 `EndedAt`、无 PostSnapshot/对比报告；failed 终态 run 永远缺收尾件（逐行精读 R2 P3-10 附注）。
- **修法**：失败分支补 `now := time.Now(); c.EndedAt = &now`，并照成功路径调 `cutoverFinishReport`（其锁内合并逻辑已保证不改已落盘的 failed 状态，仅补 PostSnapshot/Report）。
- **落点**：`internal/netdev/cutover.go` CutoverRollback 失败分支。
- **验收**：构造回退失败（引用已删提案），断言 `EndedAt != nil` 且 `Report != ""`。

### A5. inspection/backup 调度器缺退出路径
- **问题**：briefing 调度器有 `case <-a.ctx.Done(): return`，inspection/backup 的 Sleep 无——app 关闭后循环仍会醒来在收尾期尝试设备 I/O（逐行精读 R3 #6）。
- **修法**：两个循环的 `time.Sleep(d)` 改 `select { case <-time.After(d): case <-a.ctx.Done(): return }`。
- **落点**：`desktop/netdev_app.go` 两个调度器。
- **验收**：代码审阅；进程退出语义对齐 briefing。

### A6. 浏览器 mock 的 Cutover 状态机与真实后端相悖
- **问题**：mock `CutoverStart` 返回 `status:"hold"`（真实只有 running/precheck-failed）；`CutoverContinue` 直接翻 done+伪造 report（真实是 running + runner 接管）；`CutoverSkip` **三道闸全缺**（空 reason 不拒、不校验步状态、cursor 不推进、步不标 skipped）。浏览器 dev 演示的分支在真机永不触发/时序不符（逐行精读 R4 #3；2026-09-12 曾修复后因共享文件被并行回写覆盖而丢失，本批次重新落地，见批次 A 收尾注记）。
- **修法**：Start → `status:"running"`（如需演示决策点，在 Cutovers 预置一条 hold 态 run）；Continue → `running` + 清 hold_note；Skip 补 `steps[cursor].status=="skipped"`、`cursor++`、非 failed/gating 拒绝。
- **落点**：`desktop/frontend/src/lib/bridge.ts` mock 区。
- **验收**：浏览器 dev 下 start→continue→skip 的演示态与真机语义一致。

### A6+. 收尾注记（2026-09-12）
- 共享文件覆盖事件：`bridge.ts` 曾在提交前被并行整文件回写，本批次的 mock Skip 校验随之丢失（HEAD `7e00e3a2` 中不存在）——2026-09-12 已重新落地（reason/状态/步三道闸 + cursor 推进）。**教训：每次并行批次落地后、提交前，对本批次全部共享文件跑一遍标记清单核对**（本会话已用标记 grep 法执行）。
- 其余本批次前端编辑经标记盘点全部存活（metricDefaults/valueAsNumber/notifySMTPPassword×7/stoppingRef/noticeTimer/estopTitle/dock 全量目录 等）。

**批次 A 验收门禁**：`go build ./internal/... ./cmd/... && cd desktop && go build ./...`、`go test ./internal/netdev/ ./internal/config/ -count=1`、`go test ./... -count=1`（desktop 模块）、`npx tsc --noEmit` 全绿；行为差异仅限上述各项。

---

## 二、批次 B：设计取舍类（每项含方案与推荐）

### B1. 跳过原因输入：`window.prompt` 有跨平台静默失效风险
- **问题**：WKWebView（macOS）无 `window.prompt` 实现，返回 null → CutoverView 的跳过操作**静默无反应**，用户以为已跳过；且违背本模块自定规范（NetDevLayout 注释明言弃用原生对话框，已有 ConfirmModal/useConfirm）。
- **方案 A（推荐）**：hold 面板内联一行输入（`mem-input` 样式 + 跳过按钮读 state）——改动局部、无组件契约变更。
- **方案 B**：`useConfirm`/ConfirmModal 扩展可选 `input` 字段——通用但动公共组件。
- **验收**：macOS 下跳过流程可用；空原因仍被拦（前端 + 后端双层）。

### B2. 门失败 hold 上「继续」对 proposal 步是必败循环 ⚠️ 待裁决
- **问题**：门失败时提案已执行（watching/partial），HoldNote 给"跳过/回退/终止"但 UI 在 hold 态固定渲染[继续]——按继续 → `ExecuteProposal` 拒绝非 approved → 步 failed → 再次 hold，循环到按跳过为止。
- **方案 A**：后端拒绝——Continue 对 proposal 步且提案非 approved 时直接报错"提案步请用跳过/回退"。改动最小，但砍掉"直接再试"的合法场景（提案从未执行的 direct 段不受影响）。
- **方案 B（推荐）**：Continue 对已执行完（watching/done）的 proposal 步**只重验门**——不重发提案，跑 `gateWait` 直到 sustain 满足/超时。语义 = "变更已在设备上，我只重新验证"。需要给 CutoverStep 增加派生态（如 `reverify`），改动集中在 `cutoverExecStep`。
- **验收**：急停→继续 主恢复路径不再出现假门失败循环；direct 步行为不变。

### B3. 回退 break 级联与 failed 提案白名单
- **问题**：倒序回退中任一候选失败即 break——更早的已落盘提案不再回退；且 `RollbackProposal` 只接受 partial/done/watching，**status=failed 的提案（上次回退失败的产物）永远回不动**，尽管其已落地前缀仍可按 `AppliedCmds` 回滚（其自身文档承认）。
- **修法**：(a) `RollbackProposal` 前置纳入 `ProposalFailed`（其步级 `!s.Applied && AppliedCmds==0 → continue` 闸保证只回已落地前缀，安全论证不变）；(b) CutoverRollback 失败不再整体 break——改为逐项汇总 `unresolved []string`，HoldNote 列出"已回滚 N / 未回滚 M（M 的清单）"，run 仍置 failed 但人工接管的范围从"全部"缩小到 M。
- **落点**：`internal/netdev/proposal.go` RollbackProposal 前置；`internal/netdev/cutover.go` 回退循环。
- **验收**：构造"提案 A 回滚失败 + 提案 B 可回滚"，断言 B 被回滚、HoldNote 列出 A；netdev 全量测试绿。

### B4. CutoverStart 落盘→launch 窗口被误判 interrupted
- **问题**：Start 在落盘 running 后、注册 runner 前有含 AppendAudit 的间隙；并发 `ListCutovers`（大屏/estop 高频调）的孤儿扫描此时查不到活 runner，会把新 run 翻成 interrupted（逐行精读 R2 P3-8；estop 高频调用使窗口被放大）。
- **修法（推荐）**：CutoverStart 先 `cutoverRuns[id] = handle`（预注册，cancel 为空操作即可）再落盘再 launch 覆盖；recover 的"存在即活"判定自然放行。注意 precheck-failed 不 launch 的路径要在返回前清理 handle。
- **验收**：并发压测 `ListCutovers` × `CutoverStart` 数十次，无 interrupted 误判（可用 `go test -race` + 计数断言）。

### B5. 向导规则的稳定 preset key
- **问题**：向导落库的是 `t(r.name)` 译文并以其为去重键——换语言（或文案改版）重跑向导会产生同名不同字的重复规则，双份通知。
- **修法（推荐）**：`NetDevAlertRule` 增加可选 `preset_key string toml:"preset_key"`（附录 B-10 白名单字符集）；向导写入 key、merged 按 key 去重；设置页编辑器不暴露该字段。轻量替代（不加字段）：向导合并时同时按"当前译文 + 两个 locale 的预设译文"匹配——脆弱，不推荐。
- **验收**：切换语言重跑向导不产生重复；旧配置（无 key）第一次重跑向导仍会追加一次（一次性迁移，可在 merged 时按译文兜底一次）。

### B6. err/notice 来源分离
- **问题**：NetdevTitleBar 的 err state 被设置加载失败与急停失败两个不相干来源写入：急停成功会"顺手"清掉设置加载失败的错误；反之 err 存在时急停成功的绿 notice 被抑制（逐行精读 R4 P3-2）。
- **修法**：stop 走独立 `stopNotice/stopErr` state（渲染槽位可共用，状态不共用）。
- **验收**：设置加载失败的错误不因急停成功而消失。

### B7. healthMap 按清单修剪
- **问题**：NetDevLayout 的 healthMap 只增不减——设备删除/改名后幽灵设备继续计入健康点与 down 计数，直到 Layout 重挂载（逐行精读 R4 P3-7）。
- **修法**：settings reload 成功后按 `settings.devices` 名单修剪 healthMap。
- **验收**：删除设备后健康徽标计数即时回落。

### B8. CleanupSeries 流式化
- **问题**：现实现 `os.ReadFile` 整文件入内存 + `kept` 再拼一份——GPU 通道接入后 series.jsonl 增长放大（逐行精读 R3 P3-9）。
- **修法**：改 scanner 逐行读 + 临时文件 + rename（复用 fileutil.AtomicWriteFile 模式）；保留策略不变。
- **验收**：大文件（>50MB）清理时内存占用有界；行为等价（新旧行混读测试）。

### B9. 后台轮询的审计/实况降噪（spec 级，另立设计）
- **问题**：GPU 轮询每主机每轮 2-3 条 hash 链审计行 + live 事件；10 主机 @60s ≈ 4320 行/天，审计文件（合规承重产物）被稀释、实况面板刷屏。
- **方向**（需要小设计，不直接做）：internal 轮询审计**不降频**（审计链完整性优先），但 live 面板对 internal 调用方聚合为一条/轮；另可给审计增加按 class 的检索视图。立项条件：dogfooding 反馈噪声真疼。

---

## 三、批次 C（引用，不在本 spec 展开）

dogfooding 门槛项全部以 `GPU_TODO.md` 为载体：XID 证据源真机校准（journalctl -g 可用性 / -q 兜底形态 / severe 分级表对照 catalog）、GPU_TODO P0-1/2/3 真机演示材料、DashShell 智算大屏 + 设备卡 GPU sparkline（数据面已就绪，纯渲染，建议配真机数据验收）。

---

## 四、待裁决项（拍板后才动）

| # | 事项 | 选项 | 推荐 |
|---|---|---|---|
| D1 | config 校验补强：SNMP 块（version/community/v3 三件套）、protocols 白名单、alert 值域、proxy_jump 环、DBSource.Via 对照 hops | (a) 硬失败——口径清晰但既有坏配置会整份拒绝启动；(b) 软降级——加载通过 + 启动日志/可见 Finding | **(b) 软降级**：坏通道进 LastError/NoProbe，不绑架整份配置 |
| D2 | B2 的「继续」语义 | A 后端拒绝 / B 只重验门 | **B**（恢复路径价值高） |
| D3 | flap_count 口径：SNMP 面抖动（GPU 通道在线）是否计 flap | 计入（现状，保守）/ 豁免 GPUOnly 主机 | 保持现状 + 在告警文案注明口径 |
| D4 | 确认框单例互斥时 E-STOP 静默 no-op | 保持（安全侧失败）/ no-op 时 toast 提示 | 保持 + estop 按钮 stopping 态已缓解 |
| D5 | Timeline「变更」轴是否过滤 `Status=AuditRefused` 的写命令 | 过滤（轴更纯）/ 保留（尝试也算事件） | 过滤，另立"被拒操作"过滤器 |

---

## 五、不变量核对（本 spec 全部条目适用）

- §1.4 七条：新行为零新增写路径；全部改动是收窄/修正方向。
- §10 UI：不新增 dock 页签/主区工作台；CutoverView 内部按钮可见性调整属既有面板。
- 脱敏：无新数据出口。
- estop 红线：任何改动不得绕过割接的回退决策点。

## 六、批次验收门禁（A/B 通用）

1. `go build ./internal/... ./cmd/...` + desktop 模块 `go build ./...`
2. `go vet ./internal/netdev/ ./internal/config/` + desktop 模块 vet
3. `go test ./internal/netdev/ ./internal/config/ -count=1` + desktop 模块 `go test ./... -count=1`
4. `go test -race ./internal/netdev/ -run "TestEstop|TestCutover|TestGPU|TestDisabledRule" -count=1`（CI race 通道 / w64devkit 本地）
5. `npx tsc --noEmit` + `npx vite build`
6. 涉及 CHANGELOG：条目进 `[Unreleased]`，随下一小版本切割。
