# FDE / AI Infra 运维承接力调研报告（三轮调研 + 缺口规格）

- 日期：2026-09-11
- 进度更新（2026-09-12，五轮排查后）：§4.0 与 §4.1-1/2/3/4 已实现并经 5 轮×3 子任务排查修复——下文 §2.1 的"GPU 指标采集 ❌/告警枚举 🟡"两行与盲点 #1/#3/#4 已被实现消化（采集/标签化/告警开面落地，行号亦漂移）， 本文其余判据仍有效；XID 证据主源换内核日志（见 §4.1-1 实现注记）。
- 修订：2026-09-11 第二轮复审修正五处——estop 语义改 hold、P0 标注为范围提案（尊重 GPU_TODO dogfooding 门槛）、分工线表述收敛并补反向边界、AuditProject 改为复用模式不扩类型、新增盲点 16/17 与 AI infra 定位声明（详见 §五修订记录）
- 性质：调研纪要 + 缺口规格（对 `NETDEV_SPEC_V2.md`、`GPU_TODO.md`、`PROFILE_CAPABILITY_MATRIX.md` 的承接与修正）
- 后续批次：0.3.0 落地后的排查遗留清仓与待裁决项规格化见 `NETDEV_0204_BATCH_SPEC.md`（批次 A 卫生清仓 / 批次 B 设计取舍 / 批次 C dogfooding）
- 调研方法：
  - **轮 1（业界对标 × 2）**：FDE 运维侧能力清单 64 条（33 基线/31 进阶，来源：Palantir/OpenAI/Anthropic JD、Troubleshoot.sh/Replicated/sos report、SRE 书籍等）；AI infra 运维侧能力清单 77 条（约 40 条 baseline，来源：NVIDIA DCGM/XID/NVSentinel/Mission Control 文档、vLLM/Triton/NCCL 文档、Meta/ByteDance 论文、Google SRE）。
  - **轮 2（代码承接映射）**：本仓库 15 个引擎内部精读（告警/时序/采集/割接/提案/Job/分诊/kube-docker API/案例包/信封/日志/通知/审计/急停/设备模型），逐项对照业界清单，给出能力边界与扩展点（附行号）。
  - **轮 3（综合设计）**：盲点综合、承接判定、修改方案、不变量核对（本文档）。
  - **复审（同日追加）**：对本文承重判断逐条压测，修正五处（见 §五修订记录）。

---

## 一、为什么写进代码：可靠流程 + 安全限制（论点论证）

> 用户论点：把 FDE / AI infra 运维能力写进代码，一是给出可靠的流程，二是方便做安全限制。本轮调研的结论是：**这个论点与本仓库已验证的架构哲学完全一致；在本产品的架构哲学（人在环、写路径唯一）下，这是正确的分工线。**

### 1.1 可靠流程：概率性组件不能承载确定性流程

LLM 是概率性的——同一段提示词，两次执行给出不同的步骤顺序、不同的判断、偶尔漏一步。运维流程的本质要求恰恰相反：**同输入 → 同步骤 → 同门 → 同产物**，可测试、可重放、可审计、可验收。所以流程的"骨架"必须在代码里：

- 现有架构已验证此模式：割接引擎把「Word 文档 + 对讲机」变成状态机（步骤 → 语义门 → 决策点 → 前后快照 → 报告），提案管线把「口头答应」变成 draft→approved→executing→watching 的状态机。`cutover.go`、`proposal.go` 的存在本身就是论点的证明。
- 本轮调研的新证据：Anthropic FDE 岗位描述把「**codify 可重复部署模式**」（把可重复的部署模式编码化）写进 JD 原文；Palantir 把 FDE 工作产品化为 AI FDE 与 Apollo（部署约束声明 + 跨安全域自动分发）——业界头部公司的方向就是"部署模式代码化"。
- 由此推出的产品判断：**cutover runbook 模板库是 FDE 能力的核心缺口**（提案侧有模板，runbook 侧没有——每次现场手工起草，模式无法沉淀复用）。

### 1.2 安全限制：不变量必须放 harness 层，不能放 prompt 层

提示词可被注入、可被幻觉绕过；代码路径不能。本仓库的安全模型已经是这个哲学（`NETDEV_SPEC_V2 §1.4` 设计不变量）：命令四分类是驱动读表（代码）而非提示词约束；写路径只有提案（代码状态机 + 人闸）而非"请 AI 小心"；预算/信封是硬闸而非自觉。**本轮调研发现的所有新能力，都必须继续走这个模式**：

| 反面（禁止） | 正面（要求） |
|---|---|
| 提示词约定"agent 你记得定期看 nvidia-smi" | GPU 采集进轮询调度器（`health.go` 兄弟通道），指标落 series |
| 让 agent 自由解释告警 | 告警规则是 TOML 结构 + 代码校验（`config/netdev.go:779-803`），新指标必须先进 `ruleMetricValue` 封闭枚举 |
| "AI 觉得客户环境没问题就可以部署" | 部署 = runbook 实例 + precheck 电池 + 语义门 + 人工决策点（`cutover.go` 状态机） |
| 诊断包让 agent 自由组织 | 诊断包是固定收集器 + SHA-256 manifest（`§4.7` 规格），脱敏在收集层强制 |

### 1.3 分工线：什么留给 LLM，什么必须代码化

- **读 = 代码白名单**（哪些命令能跑，读表说了算）；
- **写 = 代码状态机 + 人闸**（提案/割接，AI 只能起草，按按钮的永远是人的手——"AI 的手永远慢一步"）；
- **判断 = LLM 带证据**（"这段输出说明什么"、"XID 43 该怎么处置"——结论可以由模型给，但结论必须挂命令输出证据、走 Finding/案例结构，且异常立案阈值由代码定义）。

一句话：**流程骨架与安全边界代码化，语义判断模型化且永远带证据。**

**反向边界（复审补充，同样重要）**：代码化只适用于**可重复流程**。探索性现场诊断（"服务为什么起不来"）的分支是开放的，不适合硬编码成电池或 runbook——那正是对话式 agent 的价值区。不要从本文推出"一切都要进 runbook"的结论：**可重复的进代码，探索性的留给对话**。triage 电池（代码骨架 + 代码定义的异常阈值）与对话分诊（开放分支 + 证据链）并存，就是这个边界的现成示范。

---

## 二、承接映射总表

标记：✅ 已承接（现有实现直接覆盖）/ 🟡 半承接（骨架在，缺关键件）/ ❌ 缺失（需新建）/ ⛔ 拒绝（明确不做，注明归属）。

### 2.1 AI infra 侧（对照轮 1 清单 #1-77）

| 业界能力簇（条目号） | 判定 | 现状与差距 |
|---|---|---|
| GPU 指标采集（#1 DCGM/nvidia-smi 进监控） | ❌ | **最大硬缺口**。health 轮询只覆盖 `SNMP != nil` 的设备（`health.go:163-166`），GPU 主机基本无 SNMP；triage 里的 nvidia-smi 三表是一次性按需，不进轮询、不落时序 |
| XID 采集告警 + 分级处置（#2/#3） | 🟡 | triage 有 XID>0 立案（`triage.go:183-208`），但是一次性的、只在人触发分诊时；无持续监听、无 XID catalog 分级（contained vs 硬件故障）、无自动证据收集建议 |
| ECC/温度/掉卡监测（#7/#8） | 🟡 | triage 解析温度（≥85℃、只看第一卡）与 XID；无 DBE/row-remapping 规则、无掉卡（卡数变化）检测——卡数检测依赖周期采集 |
| dcgmi diag / health / NVLink / ibstat（#5/#6/#10/#11） | ❌ | 命令不在读表；**现成的承接机制是教读表** `[netdev.extra_read]`（`tools.go:154-170`，只放宽可读面、永不授权写）+ 设备卡一键教学（`NetDevAddExtraRead`）。`GPU_TODO P1-2` 已规划 fabric 体检 preset |
| nvidia-bug-report 采集（#4） | 🟡 | SFTP 只读下载通道在；无"严重 XID → 建议收集 bug-report"的 Finding 处置联动 |
| 驱动/CUDA 版本矩阵（#9/#21） | 🟡 | triage 采版本号；`§4.8` 已规格"离线升级兼容矩阵"（已装版本 vs 升级包要求 → 差异报告）但未实现 |
| 遥测集中化/日志关联（#13） | ✅ | 日志工作台（多源合并时间线）+ 时间关联层（变更-故障并排、实体 360°，R5）是强项 |
| 告警分级/去噪（#64/#65） | 🟡 | 告警引擎只有 5 个封闭 metric（reachable/if_down_count/uptime_reset/flap_count/if_down_above_p90，`alert.go:40-100`）、int64 标量、无持续时间子句、规则仅 TOML 无 UI。**AI 专属规则集（XID 突增/ECC DBE/preemption/queue depth）一条都加不进去**——需开枚举 |
| symptom vs cause 分级、误报学习（#64） | ✅ | Finding severity 分级 + 同类聚合 + 误报学习（7 天 TTL、3 次降级，`alertqueue.go`）直接对应 SRE 告警去噪实践 |
| 变更审批 + 灰度 + 回滚（#51） | ✅✅ | **王牌**：提案状态机（人审/首败冻结/只回已落地前缀）+ 组变更窗口硬闸 + 批量模板逐台 dry-run diff + 执行前 who/quser 在线检查 + 30 分钟观察期 watching 劣化立案 |
| 危险操作卡点"重启=杀训练"（#52） | 🟡 | dangerous 分类 + confirm2 + 在线人员检查已有；**缺 GPU 版检查**：GPU 主机执行前探测训练/推理进程在跑（nvidia-smi 进程表，只读）——`who/quser` 的 GPU 类比，一行读命令挡住最常见事故 |
| 多租户/namespace 隔离（#54） | 🟡 | k8s namespace 白名单逐请求强制（`kubeapi.go:190-201`）✅；配额仲裁/干扰监控归调度器域 ⛔ |
| 操作审计（#55/#46/#47） | ✅✅ | hash 链审计（不存原始输出）+ OpStep 台账 + 人工终端全程录制脱敏——正好命中业界"供应商会话级审计/PSM 录制"硬要求 |
| 故障节点 cordon/drain/reset 自动治愈（#12） | ⛔/🟡 | 自动治愈归集群自愈系统（NVSentinel 类）；我们能承接的是**人批准的提案式 drain/reset**（k8s-apply 提案 + resourceVersion 回滚） |
| 训练任务：checkpoint/抢占/队列/gang/优先级（#23-27） | ⛔ | 调度器与训练框架领域（Slurm/K8s/Volcano/Kueue）。我们只做只读观测（队列深度/占用进 series） |
| NCCL hang / rank 定位（#28/#29） | 🟡 | NCCL_DEBUG 日志可经 log_paths 接入 follow/搜索（多机 fan-out 搜索 ✅）；无 Flight Recorder、无 rank 拓扑知识——日志观测承接，框架级 trace ⛔ |
| loss NaN 检测（#30） | 🟡→易 | **漂亮承接点**：syslog 接收器已有"坏模式 10 分钟节流自动立案"机制（`syslogrecv.go`），推广到 file: 日志源的 follow 流即可 grep NaN/timeout 立 Finding——纯复用，无新协议 |
| 推理服务指标 TTFT/TPOT/queue/KV/preemption（#37-41） | ❌ | 需要 vLLM/Triton `/metrics` GET 抓取通道 + series 标签化（见盲点 #3）+ 告警枚举扩展。`§8.4` 停车场已列（vLLM/Triton 指标） |
| 金丝雀发布/版本回滚（#43/#44） | 🟡 | k8s-apply 提案（19 种 Kind 白名单、apply 前钉 resourceVersion、观察期一键回滚提案）**恰好是金丝雀发布的受控执行形态**；缺的是推理负载发现的观测面 |
| 性能基线/压测（#42） | ⛔/🟡 | 主动压测需独立授权面（信封档）；baseline 档只做"压测结果登记对比" |
| 模型热更新/autoscaling/scale-to-zero（#45-48） | ⛔ | 模型平台领域。部署动作的安全执行是我们的，服务治理策略不是 |
| 事件响应方法论（#61-66） | ✅ | 无责复盘=案例+时间线+CaseBundle；升级链=critical 15 分钟未 ack 自动升级（`escalate.go`）；告警挂 runbook 缺机制（见盲点 #13） |
| agent 辅助诊断（#68） | ✅ | 产品本体就是带证据链的运维 agent——这是相对 NVSentinel/Mission Control 的差异化 |

> **定位声明（复审补充）**：本产品的哲学是「AI 的手永远慢一步、人在环」。这在 FDE 是优势；在千卡舰队规模会成为瓶颈（每个 XID 都人工审批不现实，业界解法是 NVSentinel 类自动治愈，已列 ⛔）。因此**本产品在 AI infra 的真实定位是「观测 + 证据 + 受控变更」，不是全托管运维台**——舰队级自动治愈与调度仲裁归集群侧系统，我们不越界，也不必越界。

### 2.2 FDE 侧（对照轮 1 清单 #1-64）

| 业界能力簇（条目号） | 判定 | 现状与差距 |
|---|---|---|
| 进场 discovery（#1/#3） | ✅ | 发现/待确认区/promote、triage 电池、拓扑、清单——环境图景建立是强项 |
| 部署执行（#4/#23） | 🟡 | 提案（file-upload/cert-replace/sql-migration/k8s-apply）+ 割接 runbook（precheck/步骤/门/决策点/前后快照/报告）结构完全对业界 runbook 标准；**缺 runbook 模板库**（见盲点 #5） |
| codify 可重复部署模式（#5） | ❌ | runbook 每次手工起草（`netdev_app.go:2823`，持久化 `cutovers/C<day>-N.json`），模板机制只在提案侧——**FDE 核心诉求的直接缺口** |
| 环境预检 readiness（#18-21） | 🟡 | triage 框架现成、加电池成本低（照 `gpuTriageBattery` 模式：Go 切片 + `RunJobSync`，超时/预算/审计白得）；`§4.8` prereq 电池（内核参数/端口/证书/磁盘/依赖/权限探测）已规格未实现 |
| UAT 验收/签字放行（#7/#8） | 🟡 | **AuditProject 提供了状态机范本**：项目=设备清单+联系人+上线窗口+checklist 套餐 → 风险清单 open\|fixed\|accepted → **全绿才放行**（`auditproject.go`）。复审修正：复用其「清单+全绿放行」引擎模式做独立类型（详见盲点 #10） |
| 客户合规导出（#50） | ❌→规格在 | `§4.8`"审计记录+提案 diff+回滚凭证一键导出 PDF——「我们在你环境里做了什么」的自证文件"未实现 |
| 诊断包（#32-37 sos/troubleshoot 三段式） | 🟡 | CaseBundle 现为**单个 markdown**（复盘 + ≤30 Finding + ≤30 变更审计行，`cases.go:152-219`）；`§4.7` 承诺的 **zip + 逐文件 SHA-256 manifest** 未实现；collect→redact 两段有，analyze 段弱。上游脱敏链完整 ✅ |
| 制品校验/离线部署（#14-16/#27） | 🟡 | file-upload 有 sha256 校验但 **≤200MB 上限挡住镜像/模型分发**（几 GB 起步）；正确姿势见盲点 #8 |
| 回滚预案（#25/#26） | ✅✅ | 逆向计划只回已落地前缀 + sql-migration 的 down 脚本必填（缺则不可提交）+ 回退决策点人工按钮——业界最佳实践逐条命中 |
| 配置漂移（#29） | ✅ | golden 基线漂移 + SrvConf 跨环境 diff（dev/staging/prod 同路径对比）|
| 授权边界（#40-44） | ✅✅ | 提案审批 + 组变更窗口 + 写档三档（override 只许收紧）+ 评估信封（engagement_id/scopes/expires/approver）——变更管理链完整 |
| 会话录制/行为审计（#46/#47） | ✅✅ | HumanTTY 录制 + 命令级 hash 链审计——FDE 离场可自证 |
| 诊断排障 SOP（#39） | ✅ | 分诊电池 + 入侵排查向导 + 日志工作台 |
| 交接 handoff（#52/#55） | ✅ | HandoffReport LLM 合成 + 案例永久保留 + 周报/晨报族 |
| 经验回流（#10-12/#57） | 🟡 | 案例可导出、报告可归档；模式回流缺 runbook 模板这个载体（同盲点 #5） |
| 干系人/CAB/CRM（#2/#42） | ⛔ | 非运维域；confirm2 双人审批是简化 CAB，够用 |

---

## 三、盲点清单（本轮新发现，前两轮文档未覆盖）

1. **GPU 没有采集通道**（最大硬缺口）。health 轮询 SNMP-only；`DeviceHealth` 已留扩展字段（`health.go:28-33`）；正确挂法是兄弟 goroutine + 每 tick 重读配置（照 inspection/backup/briefing 三个调度器先例，`netdev_app.go:841/897/930`）。
2. **estop 盲区**：`NetDevEmergencyStop`（`desktop/netdev_app.go:1596`）杀连接/人工终端/发现任务，但 **running 的 Job/Cutover/Proposal 不受影响**——客户现场按下急停后部署 runbook 还在跑，是安全事故剧本。**正确语义是 hold 而非 abort（复审修正）**：`CutoverAbort` 只 cancel 运行器并把 pending 步骤标 skipped，**不回退任何已执行变更**（`cutover.go:676,691-699`，HoldNote 仅「人工终止」）——直接 abort 会把设备留在半割接状态，恰是引擎设计要防的事故态。急停应把 running cutover 置于现有 Hold 状态并引导人到回退决策点（继续/回退/终止由人按），abort 仍是独立的人工动作。
3. **series 无标签体系**：`SeriesPoint{Device, Metric, Value}`（`series.go:22-31`）——GPU 多卡、多容器、多推理实例都要靠 metric 名编码（`gpu0_mem_used` 是坏味道）。spec §5.3 本来就定了 `labels_json` schema，实现时走了 JSONL 捷径。**标签化是 GPU/推理面的前置依赖**。
4. **告警规则系统太窄**：5 个封闭 metric、int64、无 duration、无 UI。业界 baseline 的"显存 >95% 持续 5 分钟"这类规则表达不了。duration 语义可复用割接门的 SustainSec 设计。
5. **cutover 无模板库**：提案侧 Template（步骤+{{var}}+逐台 dry-run）模式成熟，runbook 侧每次手工起草。FDE 的"部署模式代码化"缺载体。
6. **CaseBundle 与 §4.7 规格有落差**：实现是单 markdown，规格承诺 zip + SHA-256 manifest；且无自动分析段（troubleshoot.sh 的 analyzers 模式）。
7. **§4.8 FDE 交付件规格了但未实现**：验收报告、合规导出 PDF、权限预检、兼容矩阵——FDE baseline 里价值最高的四件，规格已经写好，是实现问题不是设计问题。
8. **file-upload 200MB 上限挡住镜像/模型分发**。正确姿势不是无脑放开（SSH 通道不稳），而是**登记-校验-分发分离**：大制品走客户自己的通道（scp/rsync/仓库），我们做 manifest 登记 + sha256 前置校验（已有机制）+ 小文件提案上传。与 §4.8"离线部署完整性"同一清单机制。
9. **k8s 只读面 7 种资源**（version/nodes/pods/events/deployments/pod/podlog，`kubeapi.go:204-229`）：推理服务面需要 services/endpoints（发现推理端点）、statefulsets、pvc（模型存储水位）、hpa（扩缩状态）——switch 加 case 即可，防 SSRF/裁剪/密封框架全现成。
10. **AuditProject 是 FDE 验收门的状态机范本**（本轮最重要发现之一）：项目+窗口+checklist+风险清单+全绿放行。**复审修正：复用其状态机模式，不扩旧类型**——安全审计（防御性检查清单：基线/漏洞/暴露面）与 UAT 验收（业务功能验证）领域语义不同，塞同一类型会混淆概念；应做独立类型（如 DeliveryProject），共享「项目+窗口+清单+全绿放行」的引擎模式。
11. **观察期 watching 是金丝雀发布的验证器**：提案 done→watching(30min)→closed，持续对比健康基线、劣化立案、一键回滚提案（人批准）——这正好是业界 #43 金丝雀发布的"发布后自动验证"，FDE 部署和 infra 发版共用，零新增。
12. **"GPU 版 who"卡点**：提案执行前 who/quser 在线人员检查（`proposal.go:1035-1067`）的类比——GPU 主机执行前探测 `nvidia-smi --query-compute-apps`（只读、读表内），有训练/推理进程在跑则暂停并列出。一行读命令挡住"重启杀掉别人训练"的最常见事故（业界 #52）。
13. **告警挂 runbook**：业界 #66 每条告警应挂处置 runbook。承接：Finding 卡加"一键诊断"动作 = 跑预设分诊电池（Job 引擎现成），诊断报告自动回填 Finding 证据。
14. **loss NaN / NCCL timeout 告警零协议成本**：把 syslogrecv 的坏模式节流立案机制推广到 file: 日志源 follow——训练日志目录进 log_paths 后即可 NaN/timeout 立 Finding。这是"训练侧观测"里唯一属于运维的部分，且纯复用。
15. **XID 知识库进 RAG**（GPU_TODO P1-1 已提）：XID catalog 分级（contained → 应用重启/GPU reset；74/79/81 → 隔离送修）做成检索知识 + Finding 处置建议——模型判断带证据的正确用法（§1.3 分工线）。
16. **Windows GPU 主机拿不到 GPU 诊断**（复审补充）：`triage.go:98` 限定 `d.GPU && vendor==linux` 才追加 GPU 档——Windows 推理主机（`nvidia-smi` 同样可用）无 GPU 分诊段；GPU 采集通道规划同样以 linux 为先，Windows 随后补。
17. **时序 14 天保留不够做容量趋势**（复审补充）：AI infra 容量规划要月级对比；两模式的数据保留差异（FDE 窗口切片 vs infra 月级）应进保留策略设计，P0 标签化时一并定 schema，避免二次迁移。

---

## 四、修改方案（分期与落点）

### 4.0 独立安全修复（先行，与 GPU 承接无关）

| # | 改动 | 落点 | 要点 |
|---|---|---|---|
| 1 | estop 统一（**hold 语义**） | `desktop/netdev_app.go:1596` + cutover Hold / Job Pause / 提案冻结桥 | 急停 = 杀连接 + **把 running cutover 置于 Hold（不是 abort——Abort 不回退已执行变更，会把设备留在半割接态，见盲点 #2）并引导人到回退决策点** + pause running jobs + 冻结 executing 提案（在步骤间安全：提案本有首败冻结/partial 语义）；全部入审计。abort 仍是独立人工动作。这是安全缺陷修复，不属 AI infra 承接面，建议最先做 |

### 4.1 P0 —— AI infra 最小承接面（**范围提案**；对齐 GPU_TODO P1-1，修正前版 PROFILE_CAPABILITY_MATRIX §7）

> **启动门槛（复审补充）**：`GPU_TODO.md` 既定裁决是「先跑其 P0 两条路径（教读表纳管 + k8s 只读）两周 dogfooding 产出痛点清单，再定原生数据面范围」。本 P0 清单是**范围提案**，不是对既定门槛的覆盖——启动顺序仍以 dogfooding 痛点清单为准；痛点确认后可直接取用下表，砍掉不被痛点支撑的行。

| # | 改动 | 落点 | 要点 |
|---|---|---|---|
| 1 | GPU 采集通道 | 新增 `internal/netdev/gpuhealth.go`；`DeviceHealth` 加 GPU 段；`netdev_app.go` 挂兄弟调度器 | GPU=true 主机经 SSH 跑 `nvidia-smi --query-gpu=... --format=csv`（昇腾 `npu-smi` 仅在读表白名单，采集器未接——随 dogfooding）,命令进驱动读表（§1.4 第七条不变量：先映射分类器再放行，只读）；复用连接池/预算/脱敏；linux 先行，Windows GPU 主机随后（盲点 #16）。**实现注记（2026-09-12 审查后）**：实际落点为 `PollHealthOnce` 内联追加段（同节拍、单次告警评估，优于兄弟调度器原案）；轮询走 `execSealed(internal)`——跳过 per-turn 护栏但保留分类/审计/脱敏；GPU 扫描 8 并发 + 每设备 90s 超时；XID 证据主源为内核日志 `journalctl -k -g Xid`（`nvidia-smi -q` 在真实驱动上疑似无 Xid 段——**真机校准列入 dogfooding**），失败落 -q 兜底 |
| 2 | series 标签化 | `series.go`（JSONL 行加 labels）+ `metrics.go`（SQLite 加列，已有 ALTER 迁移先例） | 最小做法：metric 命名规范 `gpu.<index>.<metric>` + labels 字段并存；读取端按 (device, metric 前缀) 聚合；schema 一并定保留策略分级（盲点 #17）。**实现注记**：labels/命名已落地（series.go），SQLite 列与保留分级未做 |
| 3 | 告警引擎开面 | `alert.go` ruleMetricValue/ruleTitle + `config/netdev.go:131-146,779-803` | 新指标进封闭枚举：`gpu.xid / gpu.temp / gpu.mem_pct / gpu.count`（`gpu.mem_used` 只落时序不做告警阈值——阈值语义由 `gpu.mem_pct` 承担；`infer.queue_depth` 随 P2 推理指标抓取一起进枚举，见 §4.3-2）；op 支持 float；加 `for_rounds`（连续 N 轮成立才立案，复用割接 sustain 语义） |
| 4 | XID → Finding（分级） | 采集器解析 + `triage.go` 分析器 + XID 知识库进 RAG | XID>0 立 Finding 必带命令输出证据；catalog 分级决定 severity 与处置建议（含 nvidia-bug-report 收集建议）；同节点多卡 XID 聚合为一条（防误换好卡） |
| 5 | 前端 | 设备卡 GPU sparkline（时序已通）；DashShell 第六屏（智算：卡×指标热图/XID 事件流/温度分布） | ✅ 已落地（2026-09-12，GpuBoardView + 设备卡温度 sparkline；真机数据验收留 dogfooding）。DashShell 是合法大屏 chip，不违反 §10.1「工作台 ≤3」不变量 |

### 4.2 P1 —— FDE 交付件实现（对齐 §4.8 已有规格，不是新设计）

| # | 改动 | 落点 | 要点 |
|---|---|---|---|
| 1 | readiness 电池 | `triage.go` 照 `gpuTriageBattery` 模式加切片 + 分析器 | 内核参数/端口占用/磁盘水位/依赖版本/GPU 驱动-CUDA 对照/受限账户能力探测（全只读）；异常立 Finding |
| 2 | runbook 模板库 | `cutover.go` + 复用 `template.go` 模式 | 模板 = 步骤+门+变量；实例化走变量渲染 + dry-run 预览；FDE 部署模式从此可沉淀复用（§1.1 论点落点） |
| 3 | 部署验收门 | **复用 `auditproject.go` 的「项目+窗口+清单+全绿放行」状态机模式，独立 DeliveryProject 类型（复审修正：不扩 AuditProject——安全审计与 UAT 验收语义不同）** | readiness 结果 + UAT 登记 → 风险清单 open\|fixed\|accepted → 全绿放行 |
| 4 | 诊断包升级 | `cases.go` CaseBundle → zip + SHA-256 manifest（复用 §5.6 迁移向导同一清单机制）+ 自动分析段（triage 结论/体检异常进 manifest 头部） | 对齐 sos report/troubleshoot 三段式（collect→redact→analyze） |
| 5 | 合规导出 PDF | 审计 + 提案 diff + 回滚凭证 → PDF | 「我们在你环境里做了什么」自证文件；导出过脱敏器 |
| 6 | GPU 版执行前检查 | `proposal.go:1035-1067` 在线检查扩展 | GPU 主机加 `nvidia-smi --query-compute-apps` 探测，有任务在跑 → 暂停列出，人确认才继续 |

### 4.3 P2 —— 推理服务面（对齐 §8.4 停车场；启动条件：dogfooding 痛点清单）

| # | 改动 | 落点 | 要点 |
|---|---|---|---|
| 1 | k8s 只读面扩资源 | `kubeapi.go:204-229` switch 加 case | services/endpoints/statefulsets/pvc/hpa；密封/裁剪/namespace 白名单全现成 |
| 2 | 推理指标抓取 | 新 GET-only API 通道（vLLM/Triton `/metrics`）→ series | TTFT/TPOT/queue/KV/preemption 进 labels 化时序 + P0-3 的告警枚举 |
| 3 | 发布验证器 | 零新增 | k8s-apply 提案 + watching 观察期即金丝雀发布的受控执行与自动验证 |
| 4 | 训练日志坏模式立案 | syslogrecv 机制推广到 file: 源 follow | NaN/timeout/CUDA OOM → Finding；step 对齐另立（GPU_TODO P1-2，不并入） |
| 5 | 制品登记校验面 | manifest 机制复用（§4.7/§4.8/§5.6 同一机制） | 大制品登记 + sha256 校验，不做大文件搬运；file-upload 上限维持 200MB 不放开 |

### 4.4 明确拒绝清单（任何台都不做，每条注明归属）

| 拒绝项 | 归属 |
|---|---|
| 调度器域：gang scheduling/队列配额仲裁/优先级抢占/autoscaling/scale-to-zero/拓扑感知调度 | K8s + Volcano/Kueue/Run:ai；我们只读观测队列深度与配额水位 |
| 训练框架域：checkpoint 策略/断点续训/弹性训练/loss 自动回滚/MFU 调优 | 训练框架（Megatron/DeepSpeed/torchft）；我们做观测+证据+提案式干预 |
| 模型平台域：模型 registry 管理面/评测/热更新 API/服务治理策略 | 推理平台（KServe/NIM/Triton 管理面）；我们管"部署这个动作"的安全执行 |
| 舰队级自动治愈（自动 cordon/drain/reset） | 集群自愈系统（NVSentinel 类）；我们只做人批准的提案式 drain/reset（见 §2.1 定位声明） |
| 供应链域：镜像仓库/镜像签名/CUDA 基础镜像白名单 | Harbor/供应链系统；我们做制品登记与校验 |
| CRM/干系人/商务容量 | 非运维域 |

### 4.5 不变量核对（本方案对 §1.4/§10 的遵守）

- **新数据面默认只读**：GPU 采集命令、dcgmi/ibstat preset、推理 /metrics 抓取——全部先映射分类器四类，写能力零新增；教读表机制只放宽可读面。
- **写路径唯一形态 = 结构化提案**：cordon/drain/reset 类操作若承接，只以提案步骤存在（k8s-apply 白名单内或新增 Kind 白名条目），confirm2 覆盖危险动词。
- **UI 不变量**：dock 页签目录恒 11、主区工作台 ≤3、徽标代替常驻——智算大屏走 DashShell 内部新屏（合法 chip），FDE 流程走主区割接形态（CutoverView）+ 安全工作台视图 + dock 发现页，不新增第四工作台。
- **脱敏全覆盖**：新采集/新 bundle/新导出全部过既有 `Redact/RedactCounted` 上游链。
- **estop 语义不变量（复审新增）**：任何统一急停不得绕过割接的回退决策点——hold + 人决策，不自动 abort、不自动回滚。

---

## 五、给前版文档的修正

- `PROFILE_CAPABILITY_MATRIX.md` §3.3 的"GPU 采集器进 series"方案，落点细化为本文 §4.1（采集通道挂法、标签化前置、告警枚举开面三件事缺一不可）。
- 前版 §3.2「前台聚合」（第四工作台）提法**作废**——违反 §10.1 工作台 ≤3 不变量；FDE 流程的承载面是：主区割接形态（部署 runbook 执行）+ 验收门（DeliveryProject，复用 AuditProject 状态机模式）+ dock 发现页（预检/诊断包入口）。
- 前版 P2"模式预设正式化"保留，但排在 P0/P1 之后；本轮结论是：FDE 与 AI infra 的差异主要落在**前台预设与数据保留策略**，引擎层两者共用率经映射验证 >80%，无需拆分。

**修订记录（2026-09-11 复审，同日第二轮）**：
1. **estop 语义修正**：原 §4.1-5"急停 = abort running cutover"经代码验证为危险建议（`CutoverAbort` 不回退已执行变更，cutover.go:676,691-699）——改为 hold + 引导人工决策（§4.0，并从 AI infra P0 拆出为独立安全修复）；
2. **P0 定性为范围提案**：原表述跳过了 `GPU_TODO.md` 既定的 dogfooding 启动门槛，现明确"启动顺序以痛点清单为准"（§4.1 头注）；
3. **分工线表述收敛**：删去"唯一正确"，限定为"在本产品架构哲学下正确"，并补反向边界——可重复流程进代码，探索性诊断留给对话（§1、§1.3）；
4. **AuditProject 复用方式修正**：由"加 deployment 阶段"改为"复用状态机模式、独立 DeliveryProject 类型"（盲点 #10、§4.2-3）；
5. **新增盲点 16/17 与定位声明**：Windows GPU 主机无 GPU 档（triage.go:98）、14 天保留不够容量趋势（§3）；产品在 AI infra 的定位是「观测+证据+受控变更」而非全托管运维台（§2.1 表后注）。

## 六、关键来源（摘要）

- FDE 侧：Troubleshoot.sh/Replicated support bundle（collect→redact→analyze 三段式）、Red Hat sos report、NVIDIA NIM air-gap 部署、SRE 书籍 postmortem/runbook、Palantir Apollo 与 AI FDE、OpenAI/Anthropic FDE 岗位描述（codify 部署模式）。
- AI infra 侧：NVIDIA DCGM/XID catalog/GPU Debug Guidelines/NVSentinel/Mission Control、vLLM/Triton metrics 文档、NCCL troubleshooting、Meta Llama 3 可靠性论文（arXiv 2410.21680）、ByteDance MegaScale（arXiv 2402.15627）、Google SRE Book（告警分级/变更管理/无责复盘）。
- 全部 141 条带 URL 的完整清单见调研轮 1 产出（会话记录）；本文 §2 映射表按簇引用其条目号。
