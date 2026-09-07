# fairpeer 运维界面（netdev）用户体验审计报告

> **版本**：**v-final（定稿）**（2026-09-07 00:58）——六轮审计+通宵迭代后定稿；夜间定时轮次转为复读校验（只追加校验日志，不再改版）。v2 = 高危复核，v3 = 第五轮四维+小组件，v4 = H25-H29 主审直验 + 缺口优先级矩阵。
> **审计约束**：本审计全程**只读**，未修改、未创建、未删除任何源码文件；本文件（spec.md）是唯一产出物。
> **审计对象**：桌面端运维界面（netdev 模块）——`desktop/frontend/src/components/netdev/`（30 组件）、`desktop/frontend/src/layouts/NetDevLayout.tsx`、桥接层 `lib/bridge.ts`、后端 `desktop/netdev_app.go` / `desktop/netdev_dash_app.go`、引擎 `internal/netdev/`。
> **方法**：五轮审计（R1 静态完整性 → R2 前后端接线 → R3 规划对照+大文件深读 → R4 四类用户旅程走查 → R5 四维新视角+小组件补漏），加通宵迭代校验（全部 29 条高危经主审直接 Read 核验）；另有独立的代码级审查报告见 `CODE_REVIEW_20260907.md`（与本报告互补）。

---

## 1. 总体结论

运维界面**骨架质量高于同类产品平均水平**：只读密封+提案闸+审计哈希链的写入安全模型、事实/推演分区纪律、深链文化、键盘直达习惯都是真功夫。真正的问题集中在四条结构性裂缝和一批"深夜故障时刻最依赖的最后一公里"：

1. **反馈层失序**——成功/错误/loading 至少 7 条并存通道，成功会进红色报错横幅，失败会静默；
2. **交互基元停在 span**——341 个 `role="button"` 的 span 不可键盘聚焦、不可禁用，是可访问性与防重复点击两类问题的共同根因；
3. **格式化无统一层**——时间/字节格式化散落各处，已产生真实的时区 bug（UTC 当本地）；
4. **护栏的时间语义缺口**——变更窗口只在批准瞬间校验，批准永久有效，人工终端完全绕过护栏。

另有一批单点高严重度断点：割接大屏无任何决策按钮、回滚失败后备份内容 UI 不可达、终端假在线、告警"测试成功"不代表送达、CVE 粗匹配数字进大屏、总览空态在大屏形态是死路等。

**问题统计（v-final）**：高危 29（第 2 节汇总表，H1-H29 全部经主审直接 Read 核验）· 旅程/维度明细 106 条（第 3 节）· 附录 A 代码卫生 27 条；另有用户需要缺口 27 项（第 5 节，含优先级矩阵：P1×11 / P2×11 / P3×5）、成熟实践差距 9 大类。

---

## 2. 高优先级问题汇总（修复应最优先）

> **v2 复核声明**：H1-H24 已于 00:30-00:45 逐条用 Read/grep 对照当前代码复核，全部命中、零行号偏差，统计类断言精确复现（role="button" span = 341、netdev 域 tabIndex = 0、`setErr(String(e))` NetDevLayout 38 处 / NetDevSection 14 处）。复核中新采到的补充证据已并入对应条目。
> **v3 复核声明**：H25-H29 已于 01:15 由主审直接 Read 核验坐实——proposal.go:753-754/908-909 的 `Command: cmd` 与 tools.go:350-353 的 `Redact(cmd)` 注释形成直接对照；调度轮 netdev_app.go:843 确实绕过 ：1524-1528 的运行标志检查；TemplateCard render/apply 均取当前 vars（预览不 pin）；VulnScanPanel:112-113 同步派发两事件而 SecWorkbench 懒挂载（NetDevLayout:1645 `secBenchEverOpened &&`）时序成立。

| # | 问题 | 位置 | 来源 |
|---|------|------|------|
| H1 | **`fairpeer:netdev-dash` 写侧事件推送整体断链**：后端约 30 个写入点经 Wails `EventsEmit` 发事件，前端 6 组件却监听 DOM 事件（Wails 不派发 DOM 事件，前端也无桥接）。dock 总览挂载后数据永不更新；bench 大屏靠 60s 兜底轮询，提案执行/割接推进最坏延迟 1 分钟 | 后端 `netdev_dash_app.go:505-511` 及约 30 个调用点；前端 `DashShell.tsx:90`、`OverviewPanel.tsx:79`、`ChainBoard.tsx:73`、三个 BoardView | R2 |
| H2 | **运维侧栏没有"新建会话"入口**：App 传入 `onNewSession/onPickProject/onAddProject` 三个 props，NetDevLayout 解构列表未包含，零使用（半截接线） | `App.tsx:3583-3595`、`NetDevLayout.tsx:366,371-372` | R1 |
| H3 | **大屏（DashShell）总览空态"去添加设备"是死路**：`onJump({tab:"settings"})` 在 dash 形态兜底 `openDockTabFn("settings")`，但 "settings" 不在 DockTab 枚举，dock 整体空白用户被困。且快照拉取失败静默（`DashShell.tsx:62`），空态越易出现 | `OverviewPanel.tsx:96`、`NetDevLayout.tsx:1655-1672` | R4D |
| H4 | **设备清单无搜索/过滤/排序/组折叠**：500+ 台场景只能肉眼扫；组头无 onClick（SPEC §10.5 自己承诺"分组可折叠且记住状态"）。`/` 快捷键聚焦的唯一输入框是 IP 定位而非清单过滤 | `NetDevLayout.tsx:2048-2060、711-718` | R4A/R4D |
| H5 | **全网发现零进度零取消**：直连模式同步 await，/24 网段可能数分钟，只有按钮文案变化；无已扫描数、无耗时、无取消、无双击重入保护（runDiscover 无 in-flight 守卫） | `NetDevLayout.tsx:1214-1239、1788` | R4A |
| H6 | **发现白名单拒绝直接抛英文 Go error 无落点**：`setErr(String(e))` 直出整句英文术语（CIDR/scopes/guardrail），没有"去设置加白"按钮，新手卡死在横幅前 | `internal/netdev/discover.go:68` → `NetDevLayout.tsx:1234-1235` | R4A |
| H7 | **变更窗口护栏只挡批准不挡执行**：`parseChangeWindow().contains(now)` 全仓唯一调用点在 ApproveProposal；批准后任何时候可执行，CutoverStart 也不查窗口——"周四 22-24 点变更"形同虚设 | `internal/netdev/proposal.go:524-544`（唯一执行点）、`cutover.go:219` | R4B |
| H8 | **批准永久有效、无失效检测**：ExecuteProposal 只查 status==approved，不校验批准时长、不比对设备配置是否已偏离；唯一执行前检查（who/quser 在线人员）对网络设备 CLI 直接 skip | `proposal.go:638-640、981-1016` | R4B |
| H9 | **提案起草后人工无法修改任何一步**：桥接只有 Approve/Reject/Delete/Execute/Rollback，无编辑保存桥。改错一个端口号只能驳回重来 | `desktop/netdev_app.go:1568-1627`、`proposal.go` | R4B |
| H10 | **dock 侧审批看不到 SQL/YAML 内容**：确认框用 `commands.join("; ")` 拼接，对 sql-migration/k8s-apply/file-upload/cert-replace 四类结构化步骤显示空白；类型感知的 stepSummary 只在设置页版本使用 | `ProposalCenter.tsx:64-66`（对比 `:180`） | R4B |
| H11 | **割接大屏纯只读，没有任何决策按钮**：继续/回退只在对话区 CutoverView，终止在 running 横幅；而失败 IM 深链文本却是"点开直达割接大屏"——凌晨被叫醒的工程师落地后无按钮可按 | `CutoverBoardView.tsx:74-79`（唯一动作=预检放行）、`cutover.go:612-614` | R4B |
| H12 | **回滚失败后，恢复所需的备份内容 UI 不可达**：Note 说"备份在提案里"，前端对 `s.Backup` 只显示长度（"备份已存档 N 字节"），无查看/复制/导出入口 | `proposal.go:918-925`、`ProposalCenter.tsx:280` | R4B |
| H13 | **凭证轮换走提案会把新口令明文落盘**：无 credential/secret 型步骤，改口令只能写成 cli 命令字符串，明文进提案 JSON、审批框、审计与状态历史快照——secrets.enc.json 的加密纪律在提案路径失效 | `assess.go:122`、`proposal.go`（步骤类型清单） | R4C |
| H14 | **告警"发送测试成功"不代表送达**：webhook 发送 fire-and-forget，失败只 slog.Warn，向导第 4 步照样绿"测试成功"；全通知链无重试、无送达状态、无发送历史 UI——第一晚告警可全部静默丢失 | `AlertSetupWizard.tsx:85-87`、`netdev_app.go:2974-2987`、`notify.go:293-303` | R4C |
| H15 | **CVE 扫荡注释声称去重、实际每次堆积新卡**：`SaveRollingFinding` 设计存在但调用的是 `SaveFinding`（每次新 ID 新文件）；跑 7 天堆 7 张雷同卡，只能靠"清空全部发现"连真实告警一起清 | `cve.go:355-357、398`、`finding.go:249-260` | R4C |
| H16 | **暴露大屏 CVE-C/H 数字不可信**：匹配是厂商+OS+型号字符串 Contains，无版本比对，一台普通 Linux 主机会命中 feed 里所有 linux 内核 critical CVE；且大屏无任何可信度提示 | `cve.go:337-352、248-265、392`、`dashboards.go:982-996` | R4C |
| H17 | **终端"会话结束"是死状态（假在线）**：`exited` 仅初始化置 false，全文件无 setExited(true)；设备踢下线（VTY 超时/ACL 断开）后仍显示"在线 ● 录制中"，敲的字进黑洞 | `DeviceTerminal.tsx:21、79` | R4A |
| H18 | **没有"全网一键备份"**：备份仅单设备（NetDevRunBackup(device)），割接前想备份 50 台只能逐台点；也无备份覆盖率视图（多少台从未备份/超 7 天） | `NetDevLayout.tsx:2771-2774` | R4A |
| H19 | **定时任务在运维界面无法创建，空态指引指错地方形成死循环**：JobsPanel 空态让去"设置→运维→定时任务"，但那里只有巡检/备份频率；Create/Update/Pause/ResumeScheduledTask 桥已存在但仅办公侧日历在用 | `JobsPanel.tsx:81-86`、`NetDevSection.tsx:508-564`、`bridge.ts:802-810` | R4A |
| H20 | **失败的 Runbook 作业无法重跑**：failed/aborted 行无任何操作按钮；列表 slice(0,8) 硬顶，第 9 个以后永不可见 | `JobsPanel.tsx:190-222` | R4A |
| H21 | **总览"立即巡检"kick 失败静默**：`.catch(() => {})`——模块未启用/后端异常时按钮既不报错也不转圈，死按钮。✓已复核（v2 补充：同款静默 kick 在 `NetDevLayout.tsx:2699` 审计空态的巡检按钮处再现） | `OverviewPanel.tsx:69-71`、`NetDevLayout.tsx:2699` | R4A |
| H22 | **时间格式化无统一层且 UTC/本地两种口径并存**：SecWorkbench 有正确的 fmtEntryTime（注释自证 UTC+8 偏 8 小时问题），但裸 ISO 切片遍布 6+ 处；实锤 bug：审计页"今日读/写数"用 UTC 日期与 `a.time.slice` 比对，每天 0-8 点把今日算成昨天 | `NetDevLayout.tsx:1384-1385`；裸切片 `JobsPanel.tsx:102`、`ChainBoard.tsx:202`、`DashShell.tsx:188`、`ProposalCenter.tsx:289-291`、`OverviewPanel.tsx:338-339` | R4D |
| H23 | **键盘用户几乎无法操作**：341 个 `role="button"` span 无 tabIndex（netdev 域 tabIndex 0 命中）；大屏切换 chips、投影/暂停、bench 切换条、设备行、折叠标题行全部键盘不可达；弹层无 Esc 关闭/焦点圈禁，全局 Esc 处理器不感知弹层（dash 下按 Esc 切走 bench 而弹框留在原地） | `DashShell.tsx:144-167`、`NetDevLayout.tsx:1622-1629、1684-1794、722-726`、`NetDevSection.tsx:1373-1389` | R4D |
| H24 | **错误文案直接抛 Go error**：`setErr(String(e))` 在 NetDevLayout 38 处、NetDevSection 14 处等，英文含包路径的错误串直出横幅，无错误码化、无"下一步"引导 | 全仓普遍（实锤样例 H6） | R4A/R4D |
| H25 | **提案写路径审计不脱敏，新旧密码明文落盘并随导出外带**：执行/回滚直接 `Command: cmd` 原文入审计（对比 tools.go:353 常规命令已过 `Redact`）；`password cipher 新密码`、`snmp-server community` 类命令恰恰最常带凭据；audit.jsonl 又被 ExportState 的 audit_tail 带进导出包、被周报读取。NETCONF 同病（`netconf.go:49,133` 存原文）。修复是一行改动：`Command: Redact(cmd)` | `internal/netdev/proposal.go:753-754、908-909`、`netconf.go:49,133`、`selfexport.go:65-66` | R5A |
| H26 | **大列表零虚拟化 + 3449 行单体组件 30s 全量重渲染**：netdev 30 组件无一处虚拟化（全前端仅 VirtualMenu.tsx 用了 react-virtual）；`reload()` 每 30s 一次 set 五个大数组（设置全量/findings/200 审计/提案/割接），整棵布局树换引用全量 re-render，无 React.memo。500+ 台或 2000 行日志时周期性 CPU 尖峰、输入掉帧 | `NetDevLayout.tsx:734、1365-1371、2049-2067`、`LogPanel.tsx:16,626` | R5A |
| H27 | **调度巡检无防重入，可双轮并发轰同一批设备**：手工入口查 `inspState.Running`（netdev_app.go:1523-1533），但调度循环不查该标志直接跑（:841）；定时 tick 落在手工巡检进行中（巡检 5 分钟上限、间隔可配 <30m）即两轮全量电池并发——VTY 预算被打满、连接互挤、Finding 重复立案。备份调度器同样不查重（:886-905） | `desktop/netdev_app.go:823-880、1523-1533` | R5A |
| H28 | **模板"预览 ≠ 应用"：人审的渲染结果与落库内容可能不一致**：人审的是 `render()` dry-run 结果，`apply()` 却在后端用当前 vars 重新渲染——改了变量不重新预览直接应用，生成与签核内容不同的变更草稿，且无"预览已过期"标记。破掉了本组件"人审 N 份渲染结果"的安全设计 | `TemplateCard.tsx:45-67` | R5B |
| H29 | **首次"建案例"事件静默丢失**：VulnScanPanel 先派发 `fairpeer:netdev-case` 再打开安全工作台，而 SecWorkbench 是懒挂载（首次使用时监听者尚不存在）——案例没建成、无任何提示，用户只被切到一个空工作台 | `VulnScanPanel.tsx:111-114`、`NetDevLayout.tsx:485、1645` | R5B |

---

## 3. 用户旅程发现明细

（每条格式：位置 · 用户故事 · 严重度。来源标注 R1/R2/R3=前三轮，R4A/R4B/R4C/R4D=第四轮旅程走查，R5A/R5B=第五轮。**口径**：高/中/低三档，"中偏高/中低"为档内细分；标注"✓已复核"表示主审已直接对照代码核验该条。）

### 3.1 第一天上手

- `NetDevLayout.tsx:1954-1981 vs 2037-2047、3022-3047` · 总览空态与"三步快速上手"是两套且不同页签，引导藏在右侧 dock「网络」页签里，不自己翻永远看不到 · 中
- `NetDevLayout.tsx:3023-3041` · 三步引导只有第 1 步可点，第 2/3 步（TOFU 测试、快捷诊断）是纯文本无落点 · 中
- `NetDevSection.tsx:69、224-231` · 全局"启用运维模块"主开关无任何引导，加完设备功能不生效不知卡在哪 · 中
- `NetDevSection.tsx:272` · SSH 导入候选 slice(0,12) 静默截断，无"还有 N 台"提示 · 低
- `NetDevSection.tsx:820-826` · SNMP 仅支持 v2c，选不到 v3（user/auth/priv）——等保场景刚需 · 中

### 3.2 设备纳管

- `NetDevLayout.tsx:1909-1938` · 迁移导入向导埋在「历史」页签一排报告按钮的末尾，在「设备」「设置」都找不到 · 高
- `NetDevSection.tsx:896-920` · "测试连接"会先把填了一半的表单落盘（Save first so TestConnection can find the device），无任何"已保存"告知 · 中
- `NetDevSection.tsx:751、900` · 地址字段无格式校验，`10.1.0.11:22` 或带空格能保存成功 · 低
- 批量加设备只有"发现→纳管（骨架无凭据）"一条路，CSV 批量导入被裁决入停车场——初次上量 80 台场景无解 · 中
- 无常驻"添加设备"入口：设备列表卡只有定位/期望对比/发现三按钮；侧栏"管理"菜单仅在 projects.length>0 时渲染，新装机用户看不到 · 中

### 3.3 日常巡检

- `NetDevSection.tsx:510-515` · 巡检周期是自由文本（"如 1h/30m"），格式错保存不报错；无"下轮巡检时间"回显 · 中
- `OverviewPanel.tsx:256` · 巡检电池写死（version/cpu/memory/interface），不能按分组差异化排期、不能自定义巡检项 · 中
- `OverviewPanel.tsx:238-251` · 简报只落盘 briefings/ 目录，无导出 PDF/Excel/发邮件按钮，界面内看不到文件路径 · 中
- `OverviewPanel.tsx:242-245` · 生成简报失败静默：catch 里 setBriefState("")，状态文字直接消失 · 中

### 3.4 配置管理

- `NetDevLayout.tsx:2313-2327` · 没有"当前运行 vs 最新备份"的一键 diff；变更后也不自动触发备份留档 · 中
- `NetDevLayout.tsx:3109` · 备份失败信息是写死英文占位 `"[SYS] BACKUP FAILED: ACCESS DENIED OR DEVICE ERROR"`，分不清凭据错/ACL 拦/设备宕 · 中
- `NetDevLayout.tsx:2071+2328` · 同一选中设备同屏渲染 BackupTimeline 与 BackupHistory 两套重复备份面板（各有独有功能但叠加易困惑） · 中（R1）
- `SrvConfCard.tsx:37-43` · 空态提示写 TOML 键名 `config_paths`，无"去设置"按钮，死路 · 低
- `NetDevLayout.tsx:756-768` · 备份覆盖率不可见，只有全网最近一次备份时间一行 · 中

### 3.5 手工操作与终端

- `DeviceTerminal.tsx:65-70` · 断线无重连、会话不可恢复：卸载即 Stop，误关面板/应用重启=全新 PTY 重走登录 · 中
- 人工终端无危险命令软确认（护栏仅对 agent 生效是明示设计，但护栏设置页没有任何一行告诉用户"这些护栏不覆盖人工终端"） · 中
- `DeviceTerminal.tsx:13-15、45-51` · 终端固定 120x30，小窗裁切（注释自认 no fit addon） · 低
- `ManualPanel.tsx:9-19` · 「手册」页签实为文档，真正手工入口在别处，命名误导 · 低
- 正面：全程录制+审计哈希链+只读回放；标题栏常驻急停。

### 3.6 日志排查

- `LogWorkbench.tsx:107`、`LogPanel.tsx:83-95` · 日志源选择不持久化，每天排查重新手加，无"保存的源视图" · 中
- `LogWorkbench.tsx:141-186` · 多源合并时间线无 follow 模式（单源 LogPanel 反而有），故障进行时只能反复手点刷新 · 中
- `LogPanel.tsx:75`、`LogWorkbench.tsx:102-105` · 网络设备（交换机/路由器）进不了日志工作台（hosts 过滤仅 linux/windows/vmware/docker/k8s），核心交换机 logbuffer 历史无法回溯 · 中
- `LogWorkbench.tsx:344-386` · 跨设备 IOC 搜索命中不可导出 · 低
- `LogPanel.tsx:542`、`LogWorkbench.tsx:279、349` · 时间参数全自由文本，填"昨天"无报错也无结果 · 低

### 3.7 定时任务

- `JobsPanel.tsx:87-110` · 任务行无启用/停用开关（Pause/Resume 桥未被 netdev UI 调用） · 中
- `JobsPanel.tsx:187-189` · Runbook 空态不说明作业从哪来（要靠对话让 agent 发起） · 中
- `JobsPanel.tsx:104` · 失败详情 slice(0,120) 无展开入口 · 低

### 3.8 变更提案与审批

- `ProposalCenter.tsx:63-66` · 审批时看不到"现在长什么样"：只列将执行的命令，无当前配置片段/前后 diff；备份执行时才抓 · 中
- `proposal_steps.go:36` · 无风险评级/影响面字段，唯一风险信号是英文危险动词正则；`no ip route`/`no acl` 等网络最常用语法不在表内，不标危险 · 中
- `proposal.go:548` · 批准无理由、审批人硬编码 local-user、无第二审批人/职责分离（StateActorIM 常量定义了但全仓未使用）——批准与执行可同一秒同一只手 · 中（对照 CAB 实践为高）
- `ProposalCenter.tsx:70、186` · UI 恒传 confirm2=true，后端 ProposalNeedsConfirm2 强制闸从桌面路径永不触发；危险与普通变更摩擦相同 · 中
- `NetDevLayout.tsx:3286-3287` · 展开行只展示 commands/rollback，同 H10 的信息保真问题 · 中

### 3.9 割接执行与回滚

- `CutoverView.tsx:299`、`cutover.go:278-321` · 窗口期只能"从现在起 N 分钟"，不能预约（"20 点备好 runbook、23 点自动开始"做不到） · 中
- 无复制历史计划入口；BuildCutoverBoard("") 只取活跃或最新一次；例行月度割接每次从零填 gate · 中
- `CutoverView.tsx:246`、`cutover.go:264-274、721` · gate 设备默认值对 SQL 类提案是必败陷阱（db_source 名不在设备清单，到 gateWait 才报错）；expect 正则无即时校验，写错要等启动才暴露 · 中（v2 复核：行号 247→246 修正）
- `desktop/netdev_app.go:1611-1625` · 脱离割接直接执行提案：同步桥 10 分钟硬超时，无进度无取消，命令挂死只能等超时被切 partial · 中偏高
- `cutover.go:211-214` · 应用崩溃/重启后割接永久停 running：runner 注册表在内存，无类似提案 recoverStaleExecuting 的恢复逻辑 · 中
- `proposal.go:602-606`、`ProposalCenter.tsx:125` · failed（回滚失败）状态的提案可被普通确认框删除——恢复契约随记录消失 · 中偏高
- `cutover.go:501-527` · 回滚完成后不重跑 gate 验证（不确认"真的回到原状"） · 中
- `tools.go:269-283` · allowed_groups 护栏会泄漏到割接预检和 gate（都走 Manager.Exec）——为 AI 设的护栏误伤人工割接自动化 · 中
- `CutoverView.tsx:250` · 步骤 est_sec 硬编码 60；步骤顺序=勾选顺序无拖拽排序 · 低（v2 复核：行号 252→250 修正）
- `CutoverBoardView.tsx:105` · 大屏 gate 徽标裸英文术语，对非 AI 背景网管偏 developer 化 · 低

### 3.10 安全响应

- `dashboards.go:958-978`、`attackpath.go:137、77` · "critical" KPI 混入告警噪声：矩阵统计全部未 resolved Finding（含 alert:/syslog: 告警），一次"设备不可达"critical 告警会成为攻击路径推演起点——把运维故障当安全暴露 · 中
- `tools.go:991-996` · 对话核查结果无后端去重，重跑一轮新增一套平行卡片，无"已修复/仍在/新增"机器比对 · 中
- VulnScanPanel · 扫描无进度/覆盖率（"已完成 3/17 台"），大清单下只能盯着对话框等 · 中
- `NetDevLayout.tsx:3213-3216`、`alertqueue.go:194-197` · 误报按钮对 CVE 类发现是"假动作"：误报学习只对 syslog:/trap: 前缀生效，标完下轮照样再生成 · 中
- `cve.go:21-28、41-67` · CVE feed 无日期/时效/清空能力，示例 feed 一旦导入永久参与匹配 · 低
- 无应急遏制一键动作（封禁 IP/隔离主机模板），全靠对话框自由描述——链路合规但应急 MTTC 不可控 · 中偏高
- `SecWorkbench.tsx:430-433` · 工单关闭只需点一下状态徽标，无结论/根因/处置人必填 · 中
- `SecWorkbench.tsx:222-234、329-350` · IOC 跨设备关联串不起来：向导命中逐条独立、无按值聚合；SPEC §10.6 承诺的命中行"转 IOC"按钮未实现，台账行无"搜"按钮，三处界面间手动拼图 · 中
- `dashboards.go:186-415`、`ChainBoard.tsx:111-116` · 正面：单 finding 进链体验顺畅，hover 邻域/深链齐备；但链无 IOC 维度，同一 IOC 在 3 台设备上是 3 个不相连的结论节点 · 中

### 3.11 通知与升级

- `notify.go:133-153` · 风暴汇总可能吞掉 critical：窗口内后到告警只 count++，汇总标题取窗口内第一条——info 先进窗则 critical 被折叠成 info 标题 · 中
- `alert.go:91`、`notify.go:99-154` · 告警风暴抑制缺口：source 键含设备名，100 台同时断电=100 个不同 source 各发各的；SPEC 附录 B-11 承诺的"全局限速+熔断"未检索到实现 · 中
- `config/netdev.go:21-35`、`notify.go`、`escalate.go` · night_window/night_min 只作用于办公侧态势告警，netdev 自身 Finding 通知与升级链完全不看夜间窗口，且该配置无 UI 入口——"夜里只叫醒我 critical"对运维告警不生效 · 中
- `alert.go:34-67` · 规则粒度浅：五 metric 硬编码，无 per-rule 静默时段/持续时间/迟滞条件（阈值抖动反复触发-恢复-再触发） · 中
- `escalate.go:17、72-76` · 升级链过简：固定 15 分钟、仅 critical、每条只升一次、升到全部出口并集；"ack 了却没修"之后没人再催 · 中
- 告警恢复（自动 resolve）会再触发通知，但消息文本不标注"已恢复"，值班无法区分新告警与恢复通知 · 低
- `AlertSetupWizard.tsx:66-71` · 规则按翻译后名字合并去重，切语言后重跑向导生成第二套同名规则双份告警 · 低

### 3.12 留痕复盘与合规

- `NetDevLayout.tsx:2701`、`netdev_app.go:1673-1676` · 审计 UI 上限 100 条、桥上限 500，无任何过滤/搜索/导出——"查上周谁改了配置"无解 · 中偏高
- 审计"谁"的粒度只有 local-user/agent/system，无 OS 账号/IM 身份 · 中
- `internal/netdev/auditproject.go` · 审计项目与变更记录零关联：proposal/cutover 关键字零命中，修复不生成提案链接、割接完成不反馈项目红绿灯 · 中
- `CutoverBoardView` · 无历史 run 选择器，旧割接的对比报告无法从 UI 再次调出导出 · 中
- `baseline.go:60-71` · 基线核查仅 10 条明文协议规则，无等保 2.0/CIS 条款映射；所有报告仅 Markdown 无 PDF/签字件 · 中
- 无复盘度量（MTTD/MTTR/误报率/噪声排行），告警疲劳无法量化治理 · 低
- `StateHistoryPanel.tsx:36-39` · 无条件 2.5s 轮询不判可见性，页面挂后台也打桥 · 低

### 3.13 性能与响应感知（第五轮 A 维）

- 高：见 H26（零虚拟化 + 单体全量重渲染）。
- `StateHistoryPanel.tsx:37`（2.5s）与 `NetDevLayout.tsx:539`（5s）对同一后端接口 `NetDevStateEvents` 双通道轮询，历史页签打开时同一数据 7.5s 内至少被拉 4 次（`:533` 注释自认分工但 IO 重复） · 中
- 除 DashShell（:73-82 有 visibilitychange，唯一正确示范）外，**所有轮询均无可见性门控**：2.5s/3s/5s/30s 在窗口最小化时照跑——离开工位一晚上 StateHistoryPanel 轮询 1.4 万次 · 中
- 重复请求三连：tab 切换 `NetDevOverview` 连发（`NetDevLayout.tsx:742、757` + OverviewPanel 内部自拉）；事件通道 + 30s `reload()` 全量 + 操作后 `await reload()` 三重刷新，单次"批准提案"= 调用 + 5 接口全量 + 事件触发 · 中
- `NetDevDiscover` 120s 上限、`NetDevDiscoverResume` **ctx 上限 4 小时**（WallSec 默认 14400）同步桥调用，期间只有 busy 布尔，无进度无取消，关窗即前功尽弃（有 checkpoint 但 UI 不提示） · 中
- 正面：巡检已任务化（kick + `netdev:inspection` 进度事件，`netdev_app.go:1495-1498`），是其余长操作该抄的模板；所有定时器卸载清理合格，无泄漏 · 低

### 3.14 数据安全泄漏面（第五轮 B 维）

正面确认（架构级）：设备密码/SNMP v3/API token/kubeconfig 全部进加密密钥库（AES-256-GCM + DPAPI/Keychain，0600/0700，v1→v2 透明迁移）；终端录制只录输出流 + `Redact()` 脱敏后落盘；常规命令审计已中心化脱敏（tools.go:353）；凭证盘点报告只报布尔不报明文。以下是旁路：

- **提案执行/回滚命令明文入审计**（H25，本节最重）。
- 发现用 SNMP community 是**明文 TOML 字段**且 config.toml 以 **0644** 落盘（`internal/config/netdev.go:344`、`config.go:2407`）——多用户机器上其他本地账户可读；且 config 本身携带完整拓扑台账（设备地址/用户名/跳板链/DB 主机）= 资产地图世界可读 · 中
- 人工终端录制**无保留期、无清理 API**：只有 List/Read 只读接口（humantty.go:357-405），敏感会话证据无限累积；含密命令经设备回显进输出流时，`Redact` 正则族覆盖不到自由文本裸密码；内存 8MB 缓冲存未脱敏原文 · 中
- `audit_retention` 配置是**安慰剂字段**：仅回显（netdev_app.go:684-685），全包无 prune/rotate 实现；审计按设计只追加（防篡改链）→ 与 30s 全量重读叠加 · 中
- 晨报/提案生成的 LLM 出口是**云端**：提示词含网络名+漏洞清单+提案意图全文+攻击路径图（netdev_app.go:1399-1417），敏感运营情报随晨报离机，无可关开关（凭据本身不外发，工具输出侧脱敏扎实） · 中
- 通知外发内容不可配脱敏：Finding 标题+设备清单+detail 500 字符发往第三方 IM SaaS；附件场景还会把文件路径清单追加进 IM 正文（notify.go:250-252） · 中
- 简报目录 **0755/文件 0644**（briefing.go:56、143、156；06:04 复读校验修正原行号 51/140-147），内容含风险清单+网络名，与全模块 0700/0600 纪律相悖 · 中
- srvconf 快照**不脱敏**整份远端配置（srvconf.go:138-158，应用配置常含 DB 密码行）——humantty/备份都过 Redact，唯独这里不过 · 中
- webhook 允许明文 http URL 无校验（postWebhookJSON）；detail 500 字节截断会切碎 UTF-8 产生乱码尾巴（notify.go:179-180） · 低

### 3.15 升级与配置兼容（第五轮 C 维）

- **无版本化字段迁移机制**：`ConfigVersion` 只在一次性搬场时写入、加载路径零消费者；字段改名靠 BurntSushi 静默忽略——旧字段无声丢弃、新默认值生效，用户无感知 · 中
- **设置保存破坏半径大**：`SetNetDevSettings` 用前端视图整体重建 `[netdev]` 段（netdev_app.go:541-560），未覆盖字段靠十几处手写 if 回填（:566-685）——新增 TOML 字段忘了登记，用户点一次"保存设置"即被静默抹掉 · 中
- localStorage 有一次性 seed 机制（NetDevLayout.tsx:448-469，做法正确），但键不区分站点（多站点共享一套页签布局）；用户手动关完全部页签后 overview 永不回填（:466，`stored2.length` 为假） · 低
- 数据文件前后兼容靠 Go 零值（findings/proposals/audit 旧文件新代码可读，有真实兼容案例）；但**新文件+旧代码（降级安装）未考虑**，watching 等新状态落 default 分支行为未定义 · 低

### 3.16 并发使用（第五轮 D 维）

- **SharedManager 的 cfg 指针无锁热替换**：每次桥接调用 `config.Load()` 后换指针（tools.go:56-66），运行中路径无锁直读 `m.cfg`——巡检/提案进行中改设置，任务在步与步之间看到新配置（设备被删→"vanished"、组策略收紧→后半段被新护栏拒绝），产生难复现的"有时拒绝"；严格说是 Go 内存模型下的数据竞争 · 中偏高
- **IM bot ack 与桌面操作竞态丢更新**：Finding 状态迁移是"无锁读全列表→改内存→整文件写"（alertqueue.go:101-125），IM 侧 `/netdev ack` 与桌面 resolve 并发时后写者覆盖先写者 · 中
- 长任务不 pin 配置快照："设置改动即时生效"与 4 小时发现/10 分钟提案的长事务一致性之间无产品化说明 · 中
- 办公侧日历任务与运维侧共用 SharedManager 连接池与 VTY 预算，无优先级/排队，冲突时靠 max_sessions_per_device 硬拒其中一方 · 中
- 提案互斥总体合格（同 id 重复执行/回滚/删除均被正确拒绝）；残余：执行流逐步 SaveProposal 与 watch goroutine 交错理论上可把 closed 覆盖回 watching（时序极窄的无事务 RMW 家族） · 低
- 正面：提案 ID 序号跨重启从磁盘重播防冲突（proposal.go:299-336，注释记录了历史事故） · —

### 3.17 小组件旅程补漏（第五轮 B 维，高危两项已入列 H28/H29）

- AlertSetupWizard：`saveAndTest` 把保存与测试包进同一 try（:63-93）——测试失败时设置其实已落盘，用户无法区分"没保存"还是"没送达"；step4 的"测试失败"分支是不可达死代码（step 4 只在测试成功后进入，:85-87、179-180）；出口不查 URL/邮箱格式，间隔 <10 静默钳到 10 · 中
- ImportWizardCard：新设备默认全选进合并集（:21-23），"先审后选"缺失；应用失败无"已应用/失败多少"区分、重按可能重复合并（:35-46）；"凭证不迁"告知出现在文件已选之后 · 中
- TemplateCard：新建模板不重置 targets（:102、116-124），第二个模板静默带上上次勾的设备；已保存模板看不到本体、不可编辑改名只能删除重建（:141-168）；name/commands 全空可存、重名不查 · 中
- SrvConfCard：路径输入框实为"只可追加"（:83，前缀失配即弹回 roots[0]），placeholder 暗示可自由填写；快照成功无反馈；对比不标 old/new 方向 · 中
- StateHistoryPanel：恢复确认框只给文件**数量**不给路径清单（:124-132）；"恢复=撤销该事件及其后所有变更"的语义无行内说明，newest-first 列表下多步误回滚风险真实；回滚覆盖 config.toml 后已打开的设置页不刷新，用户可能把旧值重新保存回去 · 中
- HealthPanel：`netdev:health` 事件只替换已存在设备（:81-88），新纳入设备必须手动刷新，制造"数据是全的"错觉；导出 CSV 比屏上少水位与历史 · 中低
- proposalStepFormat：default 分支把一切未知/未来新增步骤类型当 CLI 串渲染（:35-36），后端加新类型时审批框信息失真 · 中低
- usePanelData：deps 变化重载时不清 data——切换案例后立即渲染**上一个查询**的结果（:31-55，ChainBoard 以 `[sel, findingID]` 为 deps），无 stale 标记，可能把旧调查链当新案例的 · 中
- ChainBoard：拖拽无点击阈值无 setPointerCapture（:136-149，起手 1px 也算拖拽但 pointerup 后节点 onClick 仍触发误深链）；手动缩放/拖一次后无"复位视图"按钮，切案例后 tx/ty 残留新图可能整个停在视口外只能盲拖找回（:56、80-95）；缩放原点固定 (0,0) 越缩越偏 · 中
- VulnScanPanel："开始核查"只填提示词不发送，语义落差；sweep 完成无跳转该 finding 的链接 · 低
- ManualPanel/TopoIcon/PanelStates：滚动位置跨文档残留无锚点搜索；未知设备 role 静默落"？"框且与"未纳管"同图形；PanelStates 无新问题 · 低

---

## 4. 横切结构性问题（根因级）

1. **反馈 7 通道并存且语义不稳**：toast / okMsg 绿横幅 / opsResult 中性卡 / setErr 红横幅（含 7+ 处成功进红横幅：`NetDevLayout.tsx:975、1918、1946、2156、2558、2574`、`HealthPanel.tsx:144`）/ 内联 note / 按钮文案替换 / usePanelData 三态（仅 4 个大屏在用）。loading 样式 4 种。成功与错误在用户眼里不可区分。
2. **span role=button 基元**：341 处不可聚焦不可禁用——防重复点击（发现/扫描/备份 busy 中仍可点、无 in-flight 守卫）与键盘可达性两类问题的共同根因。正面孤例：总览"立即巡检"用原生 `<button disabled>`（`OverviewPanel.tsx:252-259`），说明正确做法已知未推广。
3. **格式化无统一层**：时间（H22 时区实锤 bug）、字节（`{bytes}B` 无 KB/MB 进位，`zh.ts:2918` 等，formatBytes 全仓 0 个）。
4. **错误直出**：`String(e)` 模式几十处（H24），与 H6 的"无下一步引导"叠加，构成新手的主要流失点。
5. **长操作无进度体系**：全网发现/nmap/全量备份/基线/CVE 扫荡全部"busy 文案+await 整段返回"；对照"网络巡检"已任务化（kick+事件进度）——同一模块两种成熟度。
6. **usePanelData/PanelErrorState 只有 4 个消费者**：dock 里健康/任务/状态历史/日志面板各自 setErr 裸奔。
7. **跨形态切丢现场**：`App.tsx:3581` 条件渲染，切到 dev/办公再切回，bench、选中设备、dock 页签、cutoverId、dashScreen 全部重置（SPEC §10.2 承诺的是形态内不丢，形态间丢了）。
8. **主题 token 违反**：`NetDevLayout.tsx:146-148` 立规"Severity colors ride the THEME TOKENS — never literal hexes"，但 `ChainBoard.tsx:15-18、165-166、186-191` 六组字面 hex，浅色主题下调查链屏发灰。内联 style 泛滥（NetDevLayout 165 处、NetDevSection 124 处）与 netdev.css 成套 ndv-* 类并行。z-index 双轨（css 已统一变量，三个内联弹层用魔数 50/60）。emoji 与 lucide 双图标体系并存。
9. **术语双义**：「发现」双义（页签=findings，大屏第五屏=discovery）；「网络」页签 vs「设备列表」侧栏按钮打开同一处；bench「日志」与 dock「日志」同名不同物；深链层残留旧名重定向 `vulnscan→findings / topology→devices / state→audit / jobs→live`（`NetDevLayout.tsx:595`）。
10. **英文界面中文残留（新位点）**：`NetDevLayout.tsx:279` 深链过滤用中文标题前缀 `f.title.startsWith("基线")`（英文 UI 永远匹配不到，静默空列表）；`App.tsx:2849-2856` 命令面板 6 条 netdev 条目硬编码中文；`NetDevLayout.tsx:2593` composer 提示词嵌中文；`BrowserConsolePanel.tsx:206` 录制步骤文案中文。裸枚举直出新位点：审计表 `{a.status}`、割接 `:2668`、总览风险条与事件徽标（`OverviewPanel.tsx:118-120、272、280`）。

---

## 5. 用户需要缺口清单（对照成熟网管/SOC 实践，逐项有代码证据）

**网管侧**（对照 eSight/SolarWinds/PRTG 基线）：
1. 设备清单搜索/过滤/列排序/组折叠/批量操作（SPEC 自己承认单层表不可规模化）
2. CSV/Excel 批量导入设备（停车场裁决 vs 真实迁移场景）
3. 备份运营视图：全网一键备份、覆盖率报表、变更前自动留档钩子、备份成功/失败通知
4. 巡检排期差异化（按分组/设备）与可自定义巡检项；下轮巡检时间回显
5. 报表定投：定时生成+SMTP 投递给非技术干系人（现简报只落盘）
6. 运维界面内定时任务 CRUD + 概念正名（JobsPanel 与设置页两个"定时任务"指鹿为马）
7. 失败作业重试入口
8. 日志源持久化视图、工作台 follow 模式、网络设备历史日志接入
9. SNMPv3
10. 终端韧性：PTY 退出检测、断线重连、可选危险命令软确认
11. 配置即时核查："当前运行 vs 最新备份"一键 diff + 变更后自动快照
12. 全局搜索收进设备/资源（Ctrl+K 面板无设备类目）；命令面板 netdev 覆盖严重不足（仅 logs+5 屏，安全工作台/体检/导出/巡检全缺席，违背 SPEC §10.7"全部新入口进面板"）
13. 拓扑图交互（缩放/拖拽/右键菜单——ChainBoard 已有 Ctrl+滚轮+拖拽，同模块两套图两套交互）
14. 快捷键注册表与帮助页（`r`/`/` 快捷键全 UI 无露出）

**变更管理侧**（对照 CAB/freeze window/灰度实践）：
15. 多人审批与职责分离、审批理由留档、审批队列/超时升级
16. 变更窗口真正约束执行 + 全局冻结日历/一键封网
17. 批准 TTL + 执行前"提案基线 vs 现网"自动 diff
18. 回滚工具箱：备份内容查看/导出、回滚后自动重跑 gate 验证
19. 灰度分批（ring/批间 gate）与 dry-run 预演、厂商 commit-confirm 集成
20. 变更↔工单/项目关联、命令级审计导出（CSV/MD）、执行进度事件+取消桥+stale-running 恢复

**SOC 侧**（对照 CVSS 优先级/SLA/playbook 实践）：
21. CVSS 分值+版本级比对+资产关键性加权（设备无 owner/关键性属性），替代粗匹配进大屏
22. Finding SLA 时限与超期升级（现仅 created_at，critical 15min 一次性升级）
23. 结构化处置 playbook（封禁/隔离/改密——secret 型提案步骤堵明文落盘）
24. 告警运营：全局限速熔断、送达率监控/发送历史、per-rule 维护窗口、恢复通知标注
25. 威胁情报运营：feed 时效/增量/清空、IOC 富化与生命周期、命中"转 IOC"
26. 修复验证闭环：主发现队列"复测"动作、cve:sweep 收敛跟踪
27. 合规映射（等保/CIS 条款级差距）与正式报告（PDF/签字件）、复盘度量（MTTD/MTTR/误报率）

**已确认非缺口**（免下轮误报）：暗色主题（themes.css 全局已有）、多语言切换、命令面板本体、紧急停止、投影模式、L3 设置页、审计链徽标、数据导出/导入（功能存在但入口太深+凭证不迁）。

**缺口优先级矩阵（v4 深化）**：P1=应进近期批次，P2=规划排期，P3=需产品决策/远期；工作量 S≤1天 / M≈2-5天 / L>1周。

| # | 缺口 | 优先级 | 工作量 | 依据速记 |
|---|------|--------|--------|----------|
| 1 | 设备清单搜索/过滤/列排序/组折叠/批量操作 | P1 | M | H4；500 台场景失效 |
| 2 | CSV/Excel 批量导入设备 | P2 | M | 停车场裁决需重议；迁移场景真实 |
| 3 | 备份运营视图（全网备份/覆盖率/前置钩子/通知） | P1 | M | H18 |
| 4 | 巡检排期差异化+自定义巡检项+下轮回显 | P2 | M | 单全局开关+固定电池 |
| 5 | 报表定投（定时生成+SMTP 投递） | P2 | M | 简报只落盘；消费链路绕办公侧 |
| 6 | 运维界面内定时任务 CRUD+概念正名 | P1 | S | H19；桥已备好（bridge.ts:802-810） |
| 7 | 失败作业重跑 | P1 | S | H20 |
| 8 | 日志源持久化/工作台 follow/网络设备历史日志 | P2 | M | 日常排查每天重复劳动 |
| 9 | SNMPv3 | P2 | M | 等保刚需；协议栈工作量大 |
| 10 | 终端韧性（退出检测/重连/可选软确认） | P1 | S-M | H17 假在线 |
| 11 | 配置即时核查（运行 vs 最新备份 diff+变更后自动快照） | P2 | S-M | diff 基建已有（BackupTimeline） |
| 12 | 全局搜索收设备+命令面板 netdev 覆盖 | P2 | S | 面板本体已有，加类目即可 |
| 13 | 拓扑交互（缩放/拖拽/右键） | P3 | M | ChainBoard 交互可移植 |
| 14 | 快捷键注册表+帮助页 | P3 | S | `r`/`/` 全 UI 无露出 |
| 15 | 多人审批/职责分离 | P3 | L | 单机产品定位，需产品决策 |
| 16 | 变更窗口约束执行+冻结日历 | P1 | S | H7；ExecuteProposal/CutoverStart 加校验即 S |
| 17 | 批准 TTL+执行前配置漂移 diff | P1 | M | H8 |
| 18 | 回滚工具箱（备份查看/导出/回滚后重跑 gate） | P1 | S-M | H12 |
| 19 | 灰度分批/dry-run/commit-confirm | P3 | L | 架构级原语 |
| 20 | 变更↔工单关联；审计导出；执行进度+取消+stale 恢复 | P1 | M | 审计导出为 P1·S；stale-running 恢复对照提案已有 recoverStaleExecuting |
| 21 | CVSS 分值+版本级比对+资产关键性加权 | P2 | L | feed 无版本区间结构，需 feed 格式升级 |
| 22 | Finding SLA 时限+超期升级 | P2 | M | 现仅 critical 15min 一次 |
| 23 | 处置 playbook（封禁/隔离/改密）+secret 步骤 | P1 | M | H13 明文落盘是安全问题 |
| 24 | 告警运营（全局限速熔断/送达率/维护窗口/恢复标注） | P1 | M | H14 相关；夜间窗口已有配置未接线 |
| 25 | 情报运营（feed 时效/增量/清空、IOC 富化、命中转 IOC） | P2 | S-M | cve.go 结构性补字段 |
| 26 | 修复验证闭环（复测动作/收敛跟踪） | P2 | M | 项目审计签名机制可复用到主队列 |
| 27 | 合规条款映射+正式报告（PDF）+复盘度量 | P3 | L | 等保场景当前基本不可用 |

---

## 6. 做得好、应保持的

- 只读密封+唯一写路径=已批准提案+首败冻结+回滚随变更起草+前后快照对比报告+审计哈希链可视校验——写入安全模型在同类工具里罕见地扎实
- 事实/推演分区与"推演"角标纪律（`dashboards.go:902`、ExposureBoardView 三处）
- TOFU 主机密钥确认、redfish/snmp 设备主动隐藏必败测试按钮
- 巡检任务化（kick 即返+事件进度）——应作为其他长操作的范式推广
- Esc 层级退出链、`o`/`Alt+1..5`/`r`/`/` 快捷键、`?dock=`/`?bench=` 深链文化
- 误报学习（syslog/trap 域）、评估信封三要素闸门、弱口令"不可达不误判已修复"复核语义
- 通知文本带 fairpeer:// 深链与 IM ack 回传
- StateHistoryPanel 的"本地记录回滚 ≠ 设备回滚"分层声明诚实
- 日志源探测一键枚举真实服务/容器/文件、错误行染色+等级过滤
- usePanelData 四态 + PanelStates 组件本身设计优秀（问题是覆盖面）

---

## 7. 修复路线图建议（按批次）

**批次一（止血，1-2 天）**：
0. 提案审计脱敏一行修复：`proposal.go:754、909` 的 `Command: cmd` 改为 `Command: Redact(cmd)`（H25——一行改动消灭明文密码落盘+导出外带链；netconf.go:49,133 同改）
1. netdev-dash 事件桥接（bridge.ts 加 EventsOn→DOM 转发，一处改动修复 H1）
2. 大屏空态死路 H3 + OverviewPanel 巡检 kick 静默 H21
3. 终端假在线 H17（exited 状态接线）
4. UTC 今日统计 bug H22（统一 fmtTime 工具）
5. dock 审批 SQL 内容 H10（复用设置页 stepSummary）

**批次二（安全网，一周）**：
6. 变更窗口约束执行 + 批准 TTL + 执行前配置漂移警告（H7/H8）
7. 割接大屏决策按钮（继续/回退/终止上大屏，H11）
8. 回滚失败备份查看/导出 + failed 提案禁删（H12）
9. secret 型提案步骤（H13）
10. 告警测试真实性 + 发送历史 UI（H14）
11. CVE 扫荡改 SaveRollingFinding（H15）
12. 调度巡检/备份防重入守卫（H27）；首次建案例事件时序修正（先挂载再派发或改直调，H29）；模板 apply 前重渲染比对（H28）；config.toml 收紧 0600（3.14）

**批次三（体验基建，两周）**：
12. 设备清单过滤/排序/折叠（H4）+ 全网备份（H18）+ 失败作业重跑（H20）+ 定时任务正名与 CRUD（H19）
13. span→button 基元替换（防重复点击+键盘可达，H23）、错误文案中文化与"下一步"引导（H24/H6）
14. 发现任务化（复用巡检 kick+事件范式，H5）
15. 提案步骤编辑（H9）+ 风险评级字段
16. usePanelData 推广到全部 dock 面板
17. 性能：轮询可见性门控（对照 DashShell 已有正确写法）+ 大列表虚拟化 + 单体组件拆分与 memo（H26）；录制保留期与清理 API、audit_retention 落地（3.14/3.15）

**批次四（缺口补齐，按需排期）**：第 5 节缺口清单按用户反馈排优先级；建议先做报表定投、审计导出、日志源持久化、SNMPv3。

---

## 附录 A：代码卫生级问题清单（低优先级明细）

- `NetDevLayout.tsx:3242` 恒空死表达式 `{f.suggestion ? "" : ""}`
- `NetDevLayout.tsx:48-49` 标题栏取回 networkName 从不显示（死状态）
- `NetDevLayout.tsx:188-194` K8S_QUICK "Pods"/"Deployments" 未走 i18n（靠缺键回退显示英文）
- `NetDevLayout.tsx:2642` `tt(...).replace("· ","")` 字符串修补翻译
- `NetDevLayout.tsx:3121-3131` showDiff 兜底参数顺序与 BackupTimeline 相反（不可达分支）
- `NetDevLayout.tsx:2976-2981` 评估流程卡"漏洞"步骤 ok:false 恒假永不亮绿
- `NetDevLayout.tsx:1573-1582` 大段注释掉的侧栏入口说明（决策记录，建议归档文档）
- zh.ts:4234/en.ts `ndv.cut.st.precheckFailed` 驼峰死键；`ndv.tab.context/devices/audit` 旧键残留
- `ChainBoard.tsx:201` timeline 条目 role="button" 无 onClick（role 撒谎）
- `SecWorkbench.tsx:151-173` 一键示例案例为设计内演示数据，建议水印更醒目；`:323` map 回调 shadow 翻译函数 t；`:493` 缺键兜底语义脆弱
- `proposalStepFormat.ts:10-13、30、46` 硬编码中文（"SQL 迁移"等）；`BrowserSkillEditor.tsx:98` 时间快捷片硬编码中文且耦合后端中文解析
- Browser 三件套文件在 netdev/ 目录但已被办公侧独占引用，注释过时（"运维 dock 的浏览器 tab"）
- `AuditProjectPanel.tsx:107-116` 新建项目无名称必填校验
- bridge.ts:681-683 三个幽灵声明（BrowserConsoleBack/Forward/ExtractTable 后端无实现）
- `BrowserConsolePanel.tsx:78` log 死状态缓冲（15+ 处写入永不渲染）；`:156` benchActive 死初值；`:1562-1564` watch picked 竞态；`:1283-1287` onStarted 吞错后跳转；`:1811-1814` K5-3 计数键错位；`:1360-1372` SMTP 默认账户不预选；`:1066-1068` 保活间隔写死 5；`:117-131` 空 URL 静默 return；动作记录仅组件内存
- `BrowserSkillEditor.tsx:376-392` 覆盖保存错误逃逸 unhandled rejection；`:278-291` 会话级失败显示"第 0 步失败"；`:240-248` 试运行下载分析写死 alerts 档；`:803-907` stepFields 缺 switch_tab/screenshot 分支；`:785` trialRunning 事件丢失则永久 disabled
- 后端显式能力边界（有兜底报错，非静默）：串口 follow 不支持流式（logfollow.go:89，Windows-only）、sql-migration 仅 mysql/postgres/mssql（proposal.go:399）、SSH keyboard-interactive 不支持（auth.go:358）
- 串口控制台仅 Windows：`NetDevSerialPorts` 在 mac/linux 返回空，设置页无平台提示
- OverviewPanel 空态按钮"跑一次网络发现"只跳日志页签（`OverviewPanel.tsx:98`）
- `CutoverView.tsx:22-28` STATUS_LABEL 缺 precheck-failed（可达状态显示裸枚举）
- `DiscoveryBoardView.tsx:76` 层级行把假设备名 L0/L1 传给定位框
- NetDevSection 行内删除两套持久化语义（项目/规则/DB 源删除不点保存静默回滚，`:483/:503/:665/:685` vs `:303/:324`）；SMTP 密码草稿随表单选择性丢失（`:1143` 等 vs `:907/951/1028/1089`）；测试通知成功无任何反馈（`:86-96`）；改名无重名校验（`:946-948` 等）；syslog 状态用未保存草稿端口显示（`:130`）
- JobsPanel/发现列表/命中明细/端口事件多处 slice 硬顶（`:190`、`NetDevLayout.tsx:2590、2794`、`SecWorkbench.tsx:228`、`DiscoveryBoardView.tsx:107`）
- EmptyState 共享组件全仓仅 1 处使用（OverviewPanel.tsx:93），spec 宣称 17 处与实况不符；NetDevSection 各列表空态为纯文字无动作按钮
- `OverviewPanel.tsx:226-228` 巡检行 nowrap+hidden 静默截断上轮结果标题且无 title 兜底
- `prefers-reduced-motion` 4 处覆盖，`ndv-brc-pulse` 脉冲动画不在降级名单；role="menu" 无键盘导航无 aria-expanded
- 事件断链第二处：`fairpeer:netdev-cve` 有监听（SecWorkbench.tsx:110）无派发

## 附录 B：文档与实现漂移

- NETDEV_SPEC_V2.md:461 说 dock 目录恒 11、:605 UI 契约说 12，实际 TABS 9 项（合并后文档未修订）
- Canary/蜜罐提案：SPEC_V2 §4.9 写"随 R5"且头部宣称 R1-R5 全部落地，但 internal/netdev 无任何 canary 代码；裁决表 #10 又写"延停车场"——两份文档自相矛盾
- 资产报表并入周报（COMPLETION_SPEC §6 #12）标"⬜ 未验证"至今
- 真机冒烟清单（终端双设备并开、SFTP>50MB、割接倒计时全程、带外深链三平台、批量模板、导入向导换机往返）多批次标"待做"无打勾记录
- SPEC §10.6 log_search 命中"转 IOC"、§10.5 分组可折叠记住状态、附录 B-11 全局限速熔断——均有承诺无实现
- UDP 探测/SYN 扫描/--script 为知情欠账（PENLAB_CAPABILITY_GAPS.md 明示"后续按需"）
- 红队批 R1-R3 为结论性"暂不立项"（非欠账，记录以免下轮误报）

## 附录 C：审计轮次记录

| 轮次 | 时间 | 角度 | 关键产出 |
|------|------|------|----------|
| R1 | 09-06 | 前端静态完整性 | 22 项（无 TODO/死按钮/缺翻译键；发现 props 死接线、红横幅承载成功等） |
| R2 | 09-06 | 前后端接线 | netdev-dash 事件断链（H1）、4 个无 UI 入口的后端能力、平台差异、事件配对全核对 |
| R3 | 09-06 | 规划对照+三 大文件深读 | 文档漂移、NetDevSection 删除/SMTP 草稿语义、BrowserSkillEditor 边缘路径 |
| R4 | 09-07 00:20-00:40 | 四类用户旅程走查（日常运维/变更割接/安全响应/信息架构） | 本版主体：H3-H24 及旅程明细、缺口清单、成熟实践差距 |
| R5A | 09-07 00:33-00:46 | 新视角四维：性能感知/数据安全泄漏面/升级配置兼容/并发使用 | H25-H27 + 3.13-3.16 节 + 泄漏面清单表 |
| R5B | 09-07 00:33-00:46 | 中危抽样复核 + 小组件旅程补漏 | 18/20 复核命中（2 处行号修正）+ H28-H29 + 3.17 节 |
| 定稿 | 09-07 00:58 | 通读去重、口径统一、统计核对、行号抽查 | v-final；29 条高危全部主审直验 |
| 夜间轮 | 09-07 01:00-06:00 | 定时复读校验（只追加日志，不改版本） | 见迭代日志追加 |

## 迭代日志

- v1（00:30）：四轮成果合并成稿。
- v2（00:35）：第 1 场·证据复核——H1-H24 逐条对照代码全部命中（含统计复现：341 span 按钮/tabIndex 0/38+14 处 String(e)）；补充 H21 同款证据一处（NetDevLayout.tsx:2699）；确认 H9 桥接层确实无提案编辑方法（netdev_app.go:1568-1630 仅 Approve/Reject/Delete/Execute/Rollback）、H13 确认 proposal.go 无 credential/secret 步骤类型、H14 确认 NotifyTest 走 fire-and-forget（notify.go:293-303 仅 slog.Warn）。
- v3（00:47）：第 2/3 场合并执行——(a) 20 条中危抽样复核：18 条精确命中，2 处行号修正（CutoverView 247→246、252→250）；(b) 新视角四维审计新增 高危×3（H25 提案审计明文、H26 零虚拟化+单体重渲染、H27 调度巡检无防重入）+ 小组件补漏 高危×2（H28 模板预览≠应用、H29 首次建案例事件丢失），新增 3.13-3.17 五节共约 40 条中低危（含数据安全泄漏面清单表）；(c) 路线图更新：批次一新增第 0 项（Redact 一行修复），批次二/三各扩充。
- v4（00:53）：H25-H29 主审直接 Read 核验全部坐实（含 tools.go:350-353 Redact 对照、调度轮绕过运行标志、懒挂载时序）；第 4 场前半提前完成——第 5 节 27 条缺口逐项标注优先级+工作量，形成缺口优先级矩阵。
- v4.1（00:56）：定稿前通读校验——第 4 节横切问题行号抽查全命中（红横幅 6 处 ：975/:1918/:1946/:2156/:2558/:2574、主题军规 :146-148、ChainBoard hex :15-18、zh.ts:4234 死键）；修正 v4 日志中的矩阵统计笔误（实为 P1×11/P2×11/P3×5；工作量 S×5、S-M×4、M×14、L×4）；修正日志时间戳为真实时间。
- **v-final（00:58，定稿）**：全文去重与口径统一完成（严重度图例补入第 3 节、方法轮次更新为五轮+迭代校验）；第 1 节统计与实际条目数核对一致（高危表 29 行 ✓、第 3 节 106 条 ✓、附录 A 27 条 ✓、缺口 27 项 ✓）；29 条高危全部经主审直接核验。定稿后夜间定时轮次转为复读校验，只追加日志不改版本。
- 复读校验轮 01:52 场（02:35 主会话代执行——定时轮 00:52/01:52 两轮均正常触发（runCount 已计）但未产生任何写入，疑似无人值守会话缺写权限，此后各轮由主会话代执行并如实标注）：抽查附录 A/第 3 节共 8 条——`NetDevLayout.tsx:3242` 恒空表达式、`:48` 死状态、`:191-192` K8S_QUICK 硬编码标签、`:2642` replace 修补、`ChainBoard.tsx:201` role="button" 无 onClick、`DiscoveryBoardView.tsx:76` L0/L1 假设备名、`CutoverView.tsx:22-28` 缺 precheck-failed 映射、`BrowserSkillEditor.tsx:98` 中文时间片——**全部命中，无偏差**；spec 保持 v-final。
- 复读校验轮 02:52 场（03:05 主会话代执行，定时轮仍无写入）：抽查第 3 节共 8 条——`NetDevSection.tsx:69` 默认 enabled:false、`:130` syslog 状态依赖草稿端口、`:1143` 跳板保存不含 SMTP 密码草稿（`{ ...view, hops }`，对照 907/951/1028/1089 四处显式携带）、`OverviewPanel.tsx:243-245` 简报失败静默清空、`NetDevLayout.tsx:2900/2952` 另两处独立 NetDevFindings 拉取、`LogPanel.tsx:75` hosts 过滤排除网络设备、`HealthPanel.tsx:84` 事件合并只替换已有设备（新设备不加入）、`SecWorkbench.tsx:226-231` 命中 slice(0,10) 逐条钉时间线无按值聚合——**全部命中，无偏差**；spec 保持 v-final。
- 复读校验轮 03:52 场（04:00 主会话代执行，定时轮连续三轮无写入）：抽查第 3 节/小组件共 8 条——`NetDevSection.tsx:907/910/919` 测试连接先落盘（:907 恰好印证设备保存携带 notifySMTPPassword、与 :1143 形成精确对照）、`ImportWizardCard.tsx:21-23` 新设备默认全选、`TemplateCard.tsx:102/116-124` 新建模板仅重置 draft 不重置共享 targets、`SrvConfCard.tsx:83` 路径输入只可追加、`StateHistoryPanel.tsx:127` 恢复确认只给文件数量、`JobsPanel.tsx:84` 定时任务空态指向设置、`DashShell.tsx:188` 裸 ISO 切片、`JobsPanel.tsx:102` 时间 slice(5,16)——**全部命中，无偏差**；spec 保持 v-final。
- 复读校验轮 04:52 场（04:55 主会话代执行，定时轮连续四轮无写入；本轮后定时任务 5 次配额用尽）：抽查附录 A/第 3/4 节共 8 条——`NetDevSection.tsx:943-948` 改名按原匹配替换、对新名不查重（注释自证设计）、`:86-96` 测试通知成功仅 setErr("")、`NetDevLayout.tsx:1939` 交接报告仅渲染无导出、`SecWorkbench.tsx:359-365` CVE feed 粘贴+示例填充、`AlertSetupWizard.tsx:96-100` 步骤校验存在（正面印证）、`bridge.ts:681-683` 三条幽灵声明在列、`ChainBoard.tsx:123` 案例下拉 uuid 噪声、`OverviewPanel.tsx:314` jump("findings","baseline") 依赖中文标题前缀匹配——**全部命中，无偏差**；spec 保持 v-final。
- **收口复核（06:01，审计窗口结束）**：按约定时间到 06:00 收口。终检结果——① spec.md 头部仍为 **v-final（定稿）**，全夜未被任何会话改动版本与结构；② 夜间 4 轮复读校验（01:52/02:52/03:52/04:52 场，因定时会话无写入权限由主会话代执行）共抽查 32 条引用，**32/32 全部命中、零偏差**；③ 源码零改动复核通过：git 未跟踪文件 28 个（本会话仅新增 spec.md，其余 27 个会话开始前已存在），全部 tracked 源码文件保持只读；④ 定时任务 5/5 次配额正常耗尽并自动关闭。**审计全程结束，本报告为最终交付物。**
- 复读校验轮 04:52 场补录（06:03，末班定时会话迟滞触发）：到场即确认 v-final 与上方收口复核在位，本场景职责（缺口深化+定稿）已由主会话提前完成（缺口矩阵=v4、定稿=00:58），无需重做；另独立抽证第 5 节缺口矩阵 5 项"确实没有"论断——CSV 批量导入设备 / SNMPv3 / formatBytes 工具 / 设备清单过滤输入 / 审计段导出按钮，grep 全部 0 命中，矩阵结论成立。不改版本、不改结构，spec 保持 v-final。**末班轮结束，全夜 5 个定时场次闭环。**
- 复读校验轮（06:05，收班迟滞轮）：抽查第 3.14 泄漏面与设置项共 8 条——`netconf.go:49/:133` NETCONF 审计原文、`selfexport.go:65-66` audit_tail 导出、`config.go:2407` 配置 0644 落盘、`briefing.go` 简报目录 0755、`srvconf.go:138-148` 快照存 Text 原文（目录 0700）、`LogPanel.tsx:542` since 自由文本、`NetDevSection.tsx:820-826` SNMP 仅 v2c——7 条精确命中，1 处行号修正（briefing.go MkdirAll 0755 实为 ：56、0644 写入实为 ：143/:156，已就地更正）；spec 保持 v-final。
- 复读校验轮（06:07，收班迟滞轮）：抽查第 3/4 节前端杂项共 8 条——`OverviewPanel.tsx:118/:120` 风险条裸英文 critical/warning、`:272/:280` 事件徽标裸枚举、`App.tsx:2849-2855` 命令面板 6 条 netdev 条目硬编码中文（同时正面印证"仅 logs+5 屏"覆盖面论断）、`NetDevLayout.tsx:595` 旧名重定向表、`:1573-1582` 侧栏注释块（有日期有理由的决策记录）、`ChainBoard.tsx:165-166` 边线/标签字面 hex `#8b93a1`、`BrowserConsolePanel.tsx:78` 死 log 缓冲、`proposalStepFormat.ts:30/:35-36/:46` "⚠ 缺失"/default 当 CLI/"N 字节 YAML"——**全部命中，无偏差**；spec 保持 v-final。
- 复读校验轮（06:08，收班迟滞轮）：抽查后端行为类论断共 8 条——`escalate.go:17` EscalationTimeout=15min、`:69-76` escalatedIDs 每条仅升一次、`notify.go:143-149` 汇总闭包捕获首条 Finding（critical 后到只 count++ 被折叠）、`alertqueue.go:103-104` transitionFinding 无锁 ListFindings 读改写、`tools.go:56-63` SharedManager 的 `shared.cfg = cfg` 换锚点、`inspect.go:24-26` RunInspectionProgress 无重入守卫、`NetDevLayout.tsx:2668` 割接列表非 running/hold 渲染裸 `c.status`、`OverviewPanel.tsx:252-259` 原生 `<button disabled>` 正面孤例——**全部命中，无偏差**；spec 保持 v-final。
- 复读校验轮（06:09，收班迟滞轮）：抽查安全声明与正面项共 8 条——`config/netdev.go:344` SnmpCommunity 明文 TOML 字段、`discover.go:256` 使用点、`humantty.go:190-197` 录制内存缓冲存未脱敏原文（8MB 上限）、`:319-320` 审计写入录制文件路径、`:331-340` 落盘前 `Redact(ansi.Strip())` + 0700/0600（正面印证 B2-1）、`store.go:140/148` 密钥库 0700/0600（正面印证 B1-1）、`NetDevLayout.tsx:3213-3215` 误报按钮作用于 active/ack 全部发现（3.10"假动作"论断的入口实证）、`JobsPanel.tsx:139-144` 仅活跃时 3s 轮询——**全部命中，无偏差**；spec 保持 v-final。
