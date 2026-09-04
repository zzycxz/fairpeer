# 申报书缺口 TODO（代码会话执行版）

> 只保留能在代码会话里实际完成的事项；真机实测/录屏/掐表/材料归档等线下人工
> 事项见文末一行备注，不进任务列表。智算相关见 `GPU_TODO.md`。
> 日期：2026-09-04（v2，按"做不到的就不做"原则裁剪）

---

## 任务 T1：备份 → 恢复提案闭环（补"回退"的后半截）✅ 2026-09-04 完成

现状：`internal/netdev/backup.go` 备份库已实现（不可变版本 + unified diff +
`backup_interval` 定时 + 割接/金标联动），缺的"从备份版本起草恢复提案"通路已落地：

- [x] 按设备读指定版本的接口：`GetBackupText`（版本内容 → 脱敏文本）已有；
      新增 `netdev_backup` agent 工具（list / read / **diff-current**，
      `internal/netdev/backup.go`）与 `Manager.BackupDiffCurrent`（版本 vs 现拉
      running-config，当前侧走密封 Exec：分类器/预算/脱敏/审计全适用）
- [x] 恢复入口：设备卡「备份」页签版本列表 →「起草恢复提案」按钮，注入指令
      让 agent 对比差异后走 `netdev_propose`（新增 `restore_from` 字段记录来源
      版本 ID，`ValidateRestoreFrom` 校验版本在库且设备与步骤一致）
  - 红线保持：恢复是写操作，AI 只起草（`restore_from` 提案），人整份审批后才执行
- [x] 前端：BackupTimeline / BackupHistory 两处入口 + 提案卡显示
      "↩ 恢复提案 · 来源版本"；审计行 `draft … restore-from <id>` 可回放
- 验收路径：见 `docs/NETDEV_DEMO_SCRIPTS.md` 场景二第 4-6 步

## 任务 T2：RunningConfigCommand 驱动扩覆盖

现状：`internal/netdev/baseline.go:30` 仅 huawei-vrp / cisco-ios / zte-zxr10，
其余设备 RunBackup 静默跳过。

- [ ] 防火墙：靶场真机验证华为 USG（VRP 命令族）是否被 huawei-vrp 顺带覆盖；
      需要新厂商（如 fortigate `show full-configuration`）时按现场实际设备加
- [ ] redfish BMC：增加配置导出映射（Redfish 标准接口），走 API 型驱动路径，
      注意输出同样过脱敏器后入备份库
- [ ] ESXi：备份形态是 bundle 文件下载，与 CLI 回显不同——**停车场，不硬塞**
- [ ] 中间件配置文件（nginx.conf 等）：注意 `srvconf.go` 已有平行实现
      （linux 白名单路径快照 → diff → 漂移 → restore-verify 演练，NETDEV_SPEC_V2
      §7.3）；若需统一入 backup.go 版本库再评估，勿重复造
- 约束：新驱动/新命令必须先进只读分类器表，再接备份库

## 任务 T3：态势感知联动处置技能（P0-B2）

- [ ] 用浏览器「录制」页签录态势感知控制台的封禁/处置流程 → 参数化（IP 等变量）→ 固化为技能
- [ ] **安全设计红线**：封禁是写操作，技能只做到"填好参数停在确认页、人工点最终执行"
- 验收：一句话"封禁 x.x.x.x" → 技能自动填参 → 人工确认 → 态势感知侧生效

## 任务 T4：晨报闭环拼装（P1-A）——主链路已落地，仅剩附件增强

- [x] 定时巡检（inspection_interval）+ 态势感知告警轮询 → DailyBriefing
      （`desktop/netdev_app.go` startBriefingScheduler/startInspectionScheduler）
      → IM/邮件出口（`internal/netdev/notify.go`：webhook/飞书/钉钉/企微/SMTP/
      内嵌 IM 网关，`briefing_push_time` 控制每日推送）——2026-09-04 核实：接线已完成
- [ ] 巡检结果可选汇总为 PPT/Excel 附件（复用办公能力 cowork/PPT 模块；
      `NotifyPushText` 目前只发纯文本，需加附件通道）
- 验收：次日晨报自动推送，含夜间巡检 Finding 与告警评判汇总

## 任务 T5：故障定界演示脚本包（P0-A 的代码侧）✅ 2026-09-04 完成

真跑需要靶场设备，代码会话只准备"弹药"：

- [x] 预置 2 个场景的指令包/提示词：①某 IP 失联 → `netdev_locate`（ARP/MAC/DHCP
      并发）→ 定位接入设备端口 → 拓扑标红 → Finding 落证据；②变更后异常 →
      变更-故障时间轴关联 → 锁定嫌疑变更（备份版本 diff 即证据）→ 恢复提案起草
- [x] 每个场景写一页"演示剧本"（步骤、预期输出、讲解词要点）
- 交付：`docs/NETDEV_DEMO_SCRIPTS.md`（含通用前置、录屏取景清单）

## 任务 T6：git 审计链路核实（P0-E）✅ 2026-09-04 完成

- [x] 阅读代码确认申报书"步骤进入 git 审计"对应的具体机制：实际为三条——
      ①工作区 git 面板（`desktop/workspace_changes.go`，只读探针 + GitCheckout
      唯一写路径）②netdev 审计 JSONL + SHA-256 哈希链（`internal/netdev/audit.go`，
      VerifyAuditChain 整链可校验）③trust domain 锚定（`internal/netdev/auditanchor.go`）。
      **结论：设备操作步骤不进 git commit，申报书该句需要换措辞**
- [x] 产出：`docs/NETDEV_AUDIT_CHAIN.md`（机制名 + 关键代码位置 + 截图取景 +
      申报书替换措辞）
- 线下（不进代码会话）：按该文档取景说明截图归档；现场演示"篡改一行 → 链断"

## 任务 T7：LinkPeer README 徽章修正 ✅ 2026-09-04 完成

- [x] LINKPEER.md 徽章 planning(M0) → in progress（灰色 → 绿色）；路线图表加
      状态列（Go 桌面侧 M0–M3 完成，移动端仓库 M4+ 进行中）；快速开始警示更新
- [x] 同批修正过时口径：`internal/mobilebridge/doc.go` 的 M0-spike 注释、
      `docs/MOBILE_CLIENT_PLAN.md` 状态行

---

## 优先级建议（2026-09-04 更新）

已完成：T6 → T1 → T5 → T7。剩余：T4 的 PPT/Excel 附件（可选增强，不急）
→ T3（新技能开发，需真实态势感知控制台环境）→ T2（按现场设备厂商裁剪范围，
真机依赖）。

## 线下人工（不进代码会话，一行备查）

真机实测（LinkPeer/USG 防火墙/BMC）、演示录屏归档、人工 vs 平台耗时掐表、
申报材料整理——由团队线下完成，代码会话不处理。
