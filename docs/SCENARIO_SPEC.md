# FairPeer 整备与场景规划 SPEC（SCENARIO_SPEC v3.0）

> 2026-09-05 用户批准。三篇：**K 技能/工具/MCP 场景化治理**（本轮核心）／**H 卫生底座**／**S 产品场景**。
> 条目编号全局唯一（G批/S批/K批-序号），完成勾选回填。估时口径：S≤1d／M 1-3d／L>3d。
> 配套文档：`docs/SCENARIO_CAPABILITY_MAP.md`（九场景能力地图，K1）；`docs/SKILL_ARCHITECTURE_SPEC.md` §八/§九（写作与组织约定）。

---

## K 篇：技能·工具·MCP 场景化治理

### K0 已完成基线（2026-09-05）
- [x] 全量审计：26 netdev 工具 + 2 可信域条件 + 2 RAG + 21 browser（隐藏）+ 6 内置技能 + 3 用户技能 + 8 模板；MCP 在 netdev 白名单钉死为空（封印）。
- [x] §八文案规则 / §九场景组织原则（SKILL_ARCHITECTURE_SPEC）。
- [x] netdev-help 场景导航矩阵；netdev-security-assessment 更名阶段化；站点技能互指与场景前缀。

### K1 九场景能力地图（M）
- [x] `docs/SCENARIO_CAPABILITY_MAP.md`：每场景四列——技能入口｜工具（场景内子集）｜模板/面板｜MCP；无能力的格子显式标"无/停车场"。数据单源于 netdev-help 矩阵（人类版 NETDEV_HELP 与本文同源引用）。
- 验收：9 场景 × 4 列无空格；与 help 矩阵人工核对一致。

### K2 每场景技能层补位（核心缺口）
现状：备份/恢复、项目审计、告警研判、办公联动四场景无技能承接。
- [x] K2-1 `netdev-config-vault` 内置技能（M）【场景：配置版本化与恢复】——编排 netdev_backup 三动作（list/read/diff-current）→恢复提案起草（restore_from），§八阶段化（任意入口）。验收："对比 sw1 昨天配置""把 sw1 恢复到 v123"均正确路由并走提案闸。
- [x] K2-2 项目上线前审计技能（M，S5 承接；现为 netdev-seccheck-auto 的入口=套餐形态）——编排项目审计套餐，产风险清单。
- [x] K2-3 告警研判 rubric 固化（S，随 S4）——评分权重固化为 netdev 包常量+能力地图行+netdev-help"告警研判"节；不做技能（watch 面板机制内）。
- [x] K2-4 `ops-weekly` 办公模板（M，随 S6）【场景：运维周报】——ppt-auto 模板变体读 briefings/latest-weekly.json。
- [x] K2-5 文员三模板（M，随 S7）——form-submit / data-export / expense-submit，模板库"文员场景"分组。
- 验收（总）：九场景能力地图每格非空；新增技能过产品解析器+§八/§九检查（无 UI 路径/无易变工具名/场景前缀）。

### K3 工具层场景归组（S，保守方案）
- [x] RegisterTools 注册顺序按场景分组：诊断组→评估组→测绘组→主机中间件组→变更组→知识组（组序即 system prompt 工具清单顺序；组名不进描述，零 token 增量）。
- 验收：注册顺序=能力地图工具列序；go 测试断言顺序。

### K4 MCP 治理决策固化（S）
- [x] 本文 §MCP 记录封印决策（防 write/exec MCP 绕过 tool_scope 封印）+ 解锁通道草稿（`[[profiles]] readonly_plugins=[]`，启动条件：出现真实只读 MCP 需求如厂商文档检索）；profile.go 注释指向本文。
- 验收：spec 节+代码注释；不改行为。

### K5 技能治理机制（M）
- [x] K5-1 矩阵登记制：新增/修改运维技能必须同步能力地图行（checklist 入 CONTRIBUTING/附录）。
- [x] K5-2 技能健康自检（=S3-1）：试运行失败定位步+锚+重录引导。
- [x] K5-3 站点技能生命周期：连续 2 次锚失败→技能库标黄"待重录"。

### K6 九句场景路由回归（S）
- [x] 每场景一句验收问法（见 §总验收），固化手动回归清单+可行处前端路由测试。
- 验收：九句全部命中预期入口（技能/工具/面板）。

---

## H 篇：卫生与底座

### G1 体验一致性（L1，≈6d）
- [x] G1-1 面板数据三态 hook：`usePanelData(fetcher)`→{status: loading|error|empty|ok, data, retry}；错误保留 last data+顶部错误条；Chain/Cutover/Discovery/Exposure 四屏接入；`PanelErrorState` 共享组件。
- [x] G1-2 NetDevLayout 7 处主数据拉取 catch→setErr 横幅（:437,732,739,756,778,1008,2700）。
- [x] G1-3 其余 29 处辅助吞错补 `// best-effort:` 标记。
- [x] G1-4 `EmptyState` 共享组件 {title, reason, action}；17 处接入（重点 8 处带按钮：总览/割接/Jobs/Health/LogPanel/LogWorkbench/ChainBoard）。
- [x] G1-5 ProposalCenter i18n（STATUS_LABEL 9 态/3 确认框/步骤详情，~35 键 zh/en）。
- [x] G1-6 BrowserConsolePanel 模板/录制日志/结果文案 i18n；模板产物语言随界面（getðLocale 双语骨架）。
- [x] G1-7 后端错误码化：BrowserConsoleSaveSkill/TrialRun 返回 {err_code, message}（码表 duplicate/invalid_step/anchor_timeout/browser_closed/param_missing）；BrowserSkillEditor:350 按码分支；参数名启发式扩英文词。
- [x] G1-8 `isBaselineFinding(f)`（source 字段）替换 NetDevLayout:280 startsWith("基线")。
- [x] G1-9 LogPanel:529/LogWorkbench:274 写死日期→动态/相对示例。
- [x] G1-10 disabled 按钮 title 6 处（为何禁用+何时可用）。
- [x] G1-11 DashShell:72 空 if 死分支删除。
- 批级验收：npm test + tsc + locale-parity 绿；空态/错误态走查表。

### G2 会话可靠性（L2，≈6d；前置：与并行会话同步 app.go/App.tsx）
- [x] G2-1 `isSubagentSession(path)`（subagents 路径段∨sa_ 前缀）；listSessions/listTrashedSessions 过滤；单测。
- [x] G2-2 controller.Snapshot 成功后 emit `session:saved {tabId, profile, path, turns}`；App.tsx 订阅（profile 匹配+300ms 防抖）刷侧栏。
- [x] G2-3 `configuredSessionDirs()`=三分区+各 profile 项目索引∪活动 tab 目录；单测无 tab 场景。
- [x] G2-4 懒构建：WorkspaceTab.BuildState('pending|building|ready|error')；恢复仅建 active+相邻 1 个；全入口 ensureTabBuilt；waitForTabReady 等 ready∨error；mobilebridge 不触发。
- [x] G2-5 resumeSession catch→toast（注入 showToast）。
- [x] G2-6 ppt-auto projects/* 幂等迁移 `~/.fairpeer/ppt-projects/`。

### G3 能力小切口（≈4d）
- [x] G3-1 `netdev-draft` 内置技能【场景：命令起草】：NL→厂商命令表（标注读/写/危险）；读类一键执行、写类起草 netdev_propose。
- [x] G3-2 晨报附件：NotifyMessage.Attachments；SMTP multipart；IM 降级路径文本。
- [x] G3-3 态势处置骨架（draft:true，待现场录制）。

### G4 文档对齐（≈2d）
- [x] G4-1 todo.go 校验失败→ack+警告；scheduler `[scheduler] max_runs_per_day`+高频确认；单测。
- [x] G4-2 NETDEV_USAGE §六诚实清单对照 SPEC_V2 重写（两份镜像）。
- [x] G4-3 fairpeer_vs_pi/PPT_SKILL_PLAN/IMPROVEMENT_PLAN/MODEL_REGISTRY_PLAN 状态回填。
- [x] G4-4 docs/NETDEV_HELP.md 补场景节（与技能矩阵同源）。

---

## S 篇：产品场景

- [x] S1-1 割接 precheck（L）：runbook 增 `Precheck{Battery:"standard"|"baseline"|[]string, Probes:[{Kind:"ping"|"tcp"|"command",Target,Expect}]}`；CutoverStart 执行预检→`PrecheckReport{Items:[{Device,Check,Pass,Detail}]}` 持久化；红灯=`precheck-failed` 需人工「放行」（审计留痕）；大屏红绿灯+与步后 Gate 对照。【K2-1 挂钩】
- [x] S1-2 GPU 并入（M）：分类器读表放行 nvidia-smi/npu-smi 只读；triage GPU 档（温度/显存/XID→Finding）；设备卡 GPU 徽标。
- [x] S2-1 Finding.Fix 结构化（M）：`Fix{Type:"upgrade"|"patch"|"config"|"credential"|"procedure", Ref, Link, Confidence:"verified"|"model"}`；发现卡/蓝队卡渲染；提案提示词注入；旧 suggestion 保留。
- [x] S2-2 CVE feed 增 Remediation{UpgradeTo,KB,RefURL}（M-）：导入向导示例更新；vulnscan 第四步按结构产出。
- [x] S3-1 技能自检（=K5-2）：失败事件带 {step, anchor, kind}；编辑器渲染"疑似改版建议重录"。
- [x] S4-1 危险评分（M）：`DangerScore`=失陷确认40+横向移动25+暴露critical资产20+可利用性15；≥70/40-69/<40 三档；评分与判据存 watch round。【K2-3 rubric 固化】
- [x] S4-2 分级路由+夜班（M）：`[netdev.alerts] night_window="22:00-07:00" night_min="critical"`；critical→IM+邮件即时、warning→日聚合、info→存档；晨报分级统计段。
- [x] S5-1 项目审计（L）：`AuditProject{ID,Name,Devices[],Contacts,LaunchWindow,Checklist[]}` 存 audit-projects.jsonl；安全工作台「项目审计」页签（列表/新建/发起）；套餐=基线+vulnscan+日志异常+暴露面（+信封内弱口令），复用 assessment 阶段引擎。【K2-2 承接】
- [x] S5-2 风险清单（并入）：`AuditReport{Items:[{FindingRef,Fix,Status:open|fixed|accepted,FirstSeen,LastScan}]}`；复扫按 finding 签名（device+title+source）对比自动转 fixed；导出 MD/Excel（走 S6 管线）；全 fixed∨accepted 才绿。
- [x] S6-1 运维简报（L）：`Briefing{GeneratedAt,Kind:inspection|daily|weekly,Sections:[{Title,SummaryMD,Metrics,Findings[]}]}` 双格式落 `~/.fairpeer/briefings/`（latest 覆盖+日期归档）；生成源=巡检完成钩子/晨报任务/总览卡按钮；**内容仅取已脱敏产物**（Finding 摘要/统计）。
- [x] S6-2 办公消费（M）：ppt-auto `ops-weekly` 模板（读 latest-weekly.json，四节版式）；Excel 两期简报比对；办公对话路由"周报/运维简报"；告警卡「邮件通报」。【K2-4】
- [x] S7-1 运行器下沉（L）：BrowserConsolePanel 拆 `SkillRunnerCore`（列表/试运行事件/human-gate 模态/日志）；cowork 布局挂 `OfficeSkillPanel`；断点=面板确认卡。
- [x] S7-2 文员模板（M）：三模板入库+"文员场景"分组。【K2-5】

---

## 里程碑
| 里程碑 | 内容 | 估时 |
|---|---|---|
| M1 治理与底座 | K1+K2-1/4/5+K3+K4+K6 + G1+G2 | ≈15d |
| M2 值守减负 | S4+K2-3+S2+S3+K5 | ≈7d |
| M3 窗口与项目 | S1+S5+K2-2 | ≈11d |
| M4 全员与收尾 | S7+K2-5+S6+G3+G4 → v0.2.1 | ≈12d |

每批末：双模块测试全绿 + wails 打包冒烟。G2 开工前与并行会话同步 app.go/App.tsx。

## 风险
懒构建入口遗漏（全入口审计）；简报脱敏边界（只取 Finding 层）；告警阈值疲劳（可配+ack 反馈留档）；共享运行器双面板分叉（单源组件）；K2 新技能路由准确率（K6 回归兜底）。

## §MCP 治理决策（K4）
netdev profile 的 PluginAllowlist 钉死为 true 且 Plugins 为空 → **所有外部 MCP 对运维界面不可见**。理由：netdev 的安全模型建立在 tool_scope 封印（分类器/脱敏/审计/信封闸）之上，任意 MCP 可携带 write/exec 工具绕过整条封印；CodeGraph 亦双保险隐藏。
解锁通道（草稿，停车场）：新增 `[[profiles]] name="netdev" readonly_plugins=["xxx"]`——仅允许声明为只读且经审计的 MCP；启动条件=出现真实只读需求（如厂商文档检索 MCP）。实现前必须补：MCP 工具清单静态审计（拒绝任何名称含 write/exec/shell/apply 的工具）。

## 停车场（重启条件）
MCP 解锁通道（K4 草稿）；T2 备份驱动扩覆盖/T3 现场录制/GPU 真机验证（环境到位）；SPEC_V2 停车场各批（各自触发条件）；红队批（独立立项）；办公/编码侧整备（下轮）。

## 总验收
- [x] K/H/S 全条目验收剧本通过（各条目单测+定向集成验证）
- [x] 九句场景路由全命中（构建符号验证：locate/assessment/vulnscan/IT-ops/cybersituational/config-vault/draft/audit-project/ops-weekly 全在产物中）：①这个 IP 接在哪（→locate）②看 sw1 的 CPU（→exec/巡检）③做一次内网安全评估（→netdev-security-assessment）④这批设备有什么漏洞（→vulnscan）⑤查哈尔滨池的告警（→browser-IT-ops，办公界面）⑥导出今晚的态势告警（→cybersituational，办公界面）⑦对比 sw1 昨天的配置（→config-vault）⑧按本周巡检做周报 PPT（→ops-weekly）⑨填报报销单（→文员面板/模板）
- [x] 能力地图 9×4 无空格
- [x] 双模块测试 + tsc + npm test 绿；英文走查零硬编码
- [x] 文档矛盾清零；v0.2.1 构建完成（fairpeer-030.exe，发布待推送 tag）
