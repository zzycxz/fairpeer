# 三台能力矩阵与运维域边界 —— FDE / AI Infra 承载思考

- 日期：2026-09-11
- 状态：方向定稿（产品思考纪要；运维台落地分期见 §7）
- 关联：`NETDEV_SPEC_V2.md`（运维台规格）、`COWORK_IMPLEMENTATION_PLAN.md`（办公台）、`CODING_WORKFLOW.md`（编码台工作流）、`DEV_COWORK_TOOL_COMPARISON.md`（历史工具对比，头部已标过时，工具面以 `internal/boot/boot.go` 为准）

---

## 0. 结论摘要

1. **FDE 与 AI infra 在运维场景里是同一个域的两种姿态**，不是两个平行域。运维台（netdev profile）以"受控现场运维"内核统一承载，用**模式预设**区分前台，而不是复制两套界面。
2. 三台分工一句话：**运维台操作既有系统（看 / 改 / 验 / 收 / 报），办公台替人操作软件与信息流（CUA），编码台造新东西（写代码）。**
3. 边界原则：凡是要"造新东西、写代码、管人管钱、改模型"的，都不归运维台——FDE 手里那部分"写胶水代码"归编码台，"给客户整理交付物"归办公台。
4. 明确拒绝清单（§6.3）：训练调优、模型研发、客户关系管理、商务容量决策不进任何一台。

---

## 1. 名词：FDE 与 AI Infra

**FDE（Forward Deployed Engineer，前置部署工程师）**：Palantir 造的岗位，AI 时代的标配。工作是人/会话进入**别人的环境**，把产品跑起来：部署、升级、数据对接、现场排障、把问题带着证据带回后方研发。环境通常是客户机房或专有云——受限网络、异构硬件、没人说得清的存量配置。

**AI infra（AI 基础设施工程师）**：运维**自己公司的**训练/推理底座——GPU 集群健康与容量、训练任务调度与恢复、推理服务部署与 SLO、驱动/CUDA/NCCL 这一层的日常运维。

### 1.1 为什么运维场景几乎统一（五轴同构）

把两者的岗位描述剥掉业务外壳，剩下的运维内核是同一个东西：

| 轴 | 两者共同的形态 |
|---|---|
| 对象 | Linux 主机 + GPU + 容器/k8s + 一点网络；接入都是 SSH / HTTP API。是"服务器与集群运维"，不是"交换机运维" |
| 动作循环 | 看（巡检/探测）→ 改（部署/变更/配置）→ 验（健康检查/语义验证）→ 收（日志/诊断包/证据）→ 报（交接/升级/复盘） |
| 约束 | 操作的都不是"自己随便折腾的机器"：FDE 怕弄坏客户的东西，infra 怕打断训练任务和线上服务 → 都需要变更安全（授权/审批/回退）、审计留痕、凭据管控 |
| 异常处理 | 出事（GPU XID / 服务起不来 / 任务卡死）→ 分诊 → 修或带证据升级。triage → fix → escalate with evidence，同一流程 |
| 工作形态 | runbook/清单驱动的远程操作，全程录制（FDE 录给客户合规看，infra 录给复盘看） |

### 1.2 真正的区别：不在"做什么"，在"姿态"

| 轴 | AI infra 姿态 | FDE 姿态 |
|---|---|---|
| 节奏 | 持续运营：7×24 监控、值班、容量规划 | 项目制脉冲：一次驻场/一次升级/一次救火，做完交接走人 |
| 对环境的已知性 | **已知环境**：有清单、有基线，核心问题是"偏离基线的漂移" | **未知环境**：第一接触，核心问题是"快速把图景建立起来" |
| 风险方向 | 可用性/爆炸半径（SRE 味）：SLO、快速回滚 | 授权与证据（合规味）：信封、录制、每个动作可解释 |
| 前台工具 | 大屏、告警、时序趋势 | 预检清单、发现、诊断包、交接报告 |
| 规模 | 一个舰队（成百上千卡，告警要去重聚合） | 一次几台到几十台主机 |
| 数据保留 | 长期时序（趋势、容量推算） | engagement 范围内的快照（带走切片，不带全量） |

一个对称的说法：infra 是"**长期盯着一群已知设备**"，FDE 是"**短期盯透一群未知设备**"。前者重基线与告警，后者重发现与取证。

---

## 2. 核心判断：一个运维域，两种模式预设

运维台不做两个平行工作台，而是同一个运维台的两个**模式预设**（mode preset）。底层（设备清单、连接池、提案、审计、终端、日志、报告）完全共用，模式只影响：

| 模式影响面 | FDE 模式 | AI infra 模式 |
|---|---|---|
| 前台默认视图 | 预检 → 部署 → 验证 → 诊断包 → 交接 一条线 | 大屏 + 告警 + 时序趋势 |
| 时序保留 | engagement 窗口切片 | 长期保留（14 天滚动现值，规模化再延） |
| 告警 | 现场即时（单机阈值） | 聚合去重（舰队级） |
| 写档默认 | sealed + 授权信封 | confirm（变更窗口内） |
| 规模预期 | 几台~几十台 | 舰队 |

理由：智算中心割网络正是 FDE 典型场景，两者经常同时出现；复制两套 profile 会把提案管线、写授权、审计链、终端全部复制一遍，维护成本翻倍且割裂现场体验。

---

## 3. 运维台（netdev profile）能做的

### 3.1 共用内核（现状已有，两种模式共用）

| 能力 | 载体 | 说明 |
|---|---|---|
| 多协议接入 | `internal/netdev/transport/`、`driver/` | SSH（堡垒 Via 链/TOFU/legacy 算法）、NETCONF、SNMP v2c/v3、Redfish GET-only、串口 console；主机类：linux/windows/vmware/esxi，kind=docker/k8s/firewall GET-only API 面 |
| 命令安全面 | `tools.go`、各驱动读表 | read/write/dangerous 三分类表；非 read 一律拒绝并审计；每轮命令预算；全程脱敏 |
| 写授权三档 | `writeauth.go` | sealed（默认，写必须走提案）/ confirm（审批卡 + 120s 超时即拒）/ auto（实验室）；设备 override 只许收紧 |
| 提案管线 | `proposal.go` | agent 只能起草（netdev_propose），人才能 approve/reject/execute/rollback；组 policy 双确认 + 变更窗口 |
| 巡检/分诊/基线 | `inspection`、`triage.go`、`baseline` | 全网巡检、单机分诊电池（异常直接立 Finding）、配置基线核查 |
| 配置备份金库 | `backup.go`、`golden.go` | 版本/diff/git 镜像、golden 漂移 |
| 割接 runbook | `cutover.go` + `CutoverView` | 步骤流水线（已批准变更/只读命令）、语义验证门（Expect 持续 SustainSec）、回退决策点、前后基线快照与对比报告 |
| 发现与纳管 | `discovered.go`、`layerdiscover.go` | TCP probe / SSH 隧道分层发现 / nmap 编排 / netprobe / drawio+vsdx 拓扑导入；待确认区 + promote |
| 可观测 | `health.go`、`series.go`、`alert*.go` | SNMP 健康轮询（cpu/mem/uptime/接口）、JSONL 时序（14 天滚动）、告警规则与队列、syslog/trap UDP 接收 |
| 日志工作台 | `LogWorkbench`、log 家族 | 日志源探测/读取/流式 follow/IOC 搜索 |
| 人工通道 | `humantty.go`、`sftp`、`OBB` | 人工 PTY 直达（人可以、agent 不可以，全程录制脱敏）、SFTP 只读下载、带外启动器 |
| 报告家族 | briefing 家族 | 每日晨报/周报/交接班报告（LLM 合成）、凭证清单 |
| 大屏五屏 | `DashShell` | overview/chain/cutover/discovery/exposure，投影轮播、审计 ticker |
| 安全核查 | CVE 匹配、弱口令、暴露面、攻击路径、蓝队 | 防御向；主动扫描档受授权信封门控 |
| 治理 | 审计 hash 链、状态历史回退、迁移导入导出、评估授权信封 | engagement_id/expiry/approver |

### 3.2 FDE 模式

FDE 的本质是"带着授权信封去客户现场：探环境 → 交付 → 排障 → 带走证据"。零件几乎全在，缺的是组装：

| FDE 工作 | 复用什么 | 状态 |
|---|---|---|
| 客户授权边界 | 评估授权信封（engagement_id/expiry/approver） | ✅ 已有（FDE 是它的第二用例，语义完全吻合） |
| 环境图景建立 | 发现/待确认区/promote、nmap 编排 | ✅ 已有 |
| 现场快速分诊 | `triage.go` 电池（含 GPU 三表） | ✅ 已有 |
| 现场操作留证 | HumanTTY 录制 + 审计链 | ✅ 已有 |
| 案件打包 | `CaseBundle` | ✅ 已有 |
| 现场交接 | `HandoffReport`（LLM 合成交接班报告） | ✅ 已有 |
| 环境预检 | 新增 readiness 电池：驱动/CUDA/容器运行时/内核参数/端口连通/磁盘水位（复用 triage 框架 + hosts 读表） | 🔨 P1 |
| 部署/升级流程 | 部署 runbook 模板（复用 CutoverRun 引擎 + CutoverView UI：预检门→步骤→验证→回退点→前后报告） | 🔨 P1 |
| 诊断包一键带回 | CaseBundle 扩展：日志切片 + 配置 + triage 报告 + 时序切片，脱敏打包 | 🔨 P1 |
| 前台聚合 | **作废（2026-09-12，见 gap spec §五）**：第四主区工作台违反 NETDEV_SPEC_V2 §10.1「工作台 ≤3」不变量；FDE 流程改走主区割接形态（CutoverView）+ AuditProject 状态机模式（DeliveryProject，见 gap spec 盲点 #10）+ dock 发现页承载 | ⛔ 作废 |

### 3.3 AI Infra 模式

现状是"网络运维视角的浅层 GPU 接入"：设备 GPU 标记 + 徽标、nvidia-smi/npu-smi（昇腾）只读白名单、分诊 GPU 三表（XID>0 立案、≥85℃ 告警）。

> **2026-09-12 进度更新**：下表 P0 的采集器/时序/告警/XID 分级已在 `internal/netdev/gpuhealth.go` + `alert.go` + `series.go` 落地（XID 证据主源换内核日志 journalctl，真机校准留 dogfooding）；智算大屏与设备卡 GPU sparkline 未做。实现细节与审查结论见 `FDE_AIINFRA_OPS_GAP_SPEC.md`。

| AI infra 工作 | 复用什么 | 状态 |
|---|---|---|
| GPU 主机只读诊断 | hosts 读表 + 分诊三表 | ✅ 已有 |
| GPU 指标时序 | GPU 采集器：GPU=true 主机经 SSH 周期 `nvidia-smi --query-gpu` CSV（昇腾 `npu-smi`），落 `series.go` | ✅ 已实现（2026-09-12） |
| GPU 告警 | `alert.go` 规则引擎：gpu.xid/gpu.temp/gpu.mem_pct/gpu.count 四指标 + for_rounds 防抖；XID→Finding 分级（journalctl 证据源） | ✅ 已实现（2026-09-12） |
| 智算大屏 | DashShell 第六屏：卡×指标利用率矩阵热图、XID 事件流、温度分布；投影轮播自动带上 | 🔨 P0（数据面已就绪） |
| 设备模型 | role 枚举扩展 gpu-node/inference（功能暂由 d.GPU bool 覆盖） | 🔨 P2 降档（随 kind=gpu-host 立项） |
| 推理服务面 | kind=k8s 上发现推理工作负载（Deployment/vLLM `/metrics`），套 `kubeapi.go` 已验证的 GET-only 模式 | 🔨 P2 |
| 深层 GPU 诊断 | NVLink/PCIe 拓扑、`dcgmi diag`、进程级 GPU 占用 | 🔨 P2 |

### 3.4 运维台不做的事

| 不做的事 | 理由 | 归宿 |
|---|---|---|
| 训练侧调优（看 loss 曲线诊断模型、调超参、NCCL 调到算法层） | ML 工程，不是运维。运维台只看"NCCL 超时是不是网络/驱动问题"，不看"loss 为什么不降" | 不在本产品 |
| 模型研发/评测/量化蒸馏决策 | 纯 ML | 不在本产品 |
| Prompt 工程 / 模型微调实施 | FDE 常兼职做，但那是工程/咨询工作 | 编码台（写）+ 办公台（文档） |
| 写数据对接胶水代码 | 工程实施，不是操作既有系统 | **编码台** |
| 数据工程（建数据管道） | 数据工程的活 | 编码台 |
| 平台建设（给算法同学造内部平台/抽象层） | "造工具"≠"用工具操作" | 编码台 |
| 容量商务决策（采购谈判、GPU 小时预算分配） | 商务决策。运维台只提供利用率事实 | 不在本产品 |
| 客户关系（期望管理、培训、workshop） | CRM/售前。办公台可辅助产出文档邮件，但不管理客户关系 | 不在本产品 |
| 破坏性安全操作 | 产品红线；蓝队核查是防御向 | 无人做（拒绝） |

---

## 4. 办公台（cowork profile）能做的

定位：**Computer-Use Agent（CUA）**——像人一样操作图形界面、文档、邮件、知识库、整个桌面（`internal/config/profile.go` coworkDefaultPromptAddon）。工具桶 = Universal + Office（`COWORK_TOOL_SLIMDOWN.md` 三桶分解）。

| 能力 | 载体 | 说明 |
|---|---|---|
| 浏览器自动化 | `browser-auto` 技能 + `internal/browseruse`（Python sidecar，HTTP+SSE） | 优先站点技能，browser-auto 兜底；共享浏览器给 agent + 面板镜像 |
| 桌面 GUI 操作 | `desktop-auto` | WPS/Excel/原生对话框等桌面应用 |
| PPT 生产线 | `ppt-auto`（内置技能，SVG→PPTX 纯 Python）+ `internal/ppttemplate` 模板 | fast/validate 模式（validate 三轮返工）；用户可丢模板进目录 |
| 邮件 | `email-auto` + SMTP/IMAP 多账号加密存储 | 收发搜索；无头模式发送需许可 |
| Office 文档读写 | `document-auto` + `internal/docconv` | markitdown + PaddleOCR PDF OCR，集中管理转换脚本 |
| 日历/任务 | `internal/calendar`（ICS/rrule/节假日/提醒）+ `CalendarTaskPanel` | 事件动作编译（ActionPrompt）仅人工 UI 可建（安全设计） |
| 定时任务 | `internal/scheduler` | cron + 自然语言时间；输出投递 toast/email/file；错过补偿 |
| 知识库 | `internal/rag` | FTS5 全文 + 结构化实体 + 可选 embedding rerank；深抽取管线（断点续传）；Obsidian；实体图谱、思维导图 |
| 专家团队 | `internal/experts` | 多专家多轮讨论 + 综合（Orchestrator），内置 6 团队（方案评审/头脑风暴/文档撰写/数据分析/翻译校对…） |
| 截图解题 / 语音 | 截图热键→VLM、STT 模型 | cowork 设置面 |

### 办公台不做的事

| 不做的事 | 理由 |
|---|---|
| 生产代码工程 | 无 multi_edit/LSP/codegraph（工具桶不含 Coding 桶）；写代码归编码台 |
| 服务器/设备运维操作 | netdev 工具硬密封（boot.go 按注册表隔离），办公注册表根本不含 |
| 绕过许可的静默邮件/外发 | 无头发送需显式许可；输出路由人工可见 |

---

## 5. 编码台（dev profile）能做的

定位：ZCode/Claude-Code 风格编码 agent，默认 profile（标准 AppChrome 布局）。工具桶 = Universal + Coding。

| 能力 | 载体 | 说明 |
|---|---|---|
| 编码主循环 | `control.Controller` + 三路径分流 | 直答（低风险问句）/ 单次执行 / Plan Mode（autoPlanScore 自动或 Shift+Tab）；≥3 task 进 compose 循环 Implement→Verify→Review |
| 完成签核 | Evidence-Backed | complete_step 必须带证据 |
| 时光倒流 | 检查点 + `/rewind` | |
| 交互终端 | `TerminalPanel`/`TerminalSession`（ConPTY + xterm.js） | Ctrl+\` 呼出，最多 8 页签；one-shot 走 RunShell |
| 远程开发主机 | `remote_host_manager.go` | wsl / docker / ssh / server 四种传输；断线重连、按 transcript 重挂会话 |
| 代码理解 | `internal/lsp`、`internal/codegraph` | LSP（definition/references/hover/diagnostics）；tree-sitter + SQLite 代码图谱 |
| 工程编辑 | multi_edit、research 子代理 | dev 独有 |
| 扩展体系 | MCP 插件（stdio/HTTP/SSE）、skill（inline/subagent）、hook（PreToolUse/PostToolUse/UserPromptSubmit/Stop） | 项目级配置需信任 |
| 记忆 | Dream 7 天沉淀、profile 隔离记忆 | |

### 编码台不做的事

| 不做的事 | 理由 |
|---|---|
| Office 文档生产线 | PPT/邮件/日历工具不在 dev 桶，归办公台 |
| 运维设备操作 | netdev 工具集不在 dev 注册表（硬密封）；设备终端/SFTP 经运维台 |
| 完整浏览器自动化 | 浏览器归属办公（用户定稿 2026-09-06）；dev 无完整浏览器工具组 |

---

## 6. 三台边界原则与协作

### 6.1 划界一句话

> **运维台 = 让既有系统可靠地跑（监控、接入、安全地变更、诊断、恢复、交代清楚）；办公台 = 替人操作软件与信息流；编码台 = 造新东西。**
> 凡是要"造新东西、写代码、管人管钱、改模型"的，都不归运维台。

### 6.2 FDE 用户的跨 profile 工作流（双/三 profile 是产品优势）

一个 FDE 在同一个 app 里按阶段切台：

| 阶段 | 用哪台 | 做什么 |
|---|---|---|
| 进场 | 运维台（FDE 模式） | 授权信封 → 发现 → readiness 预检 |
| 交付 | 运维台 | 部署 runbook（预检门→步骤→验证→回退点） |
| 对接定制 | **编码台** | 写数据对接胶水代码 / 客户环境定制脚本 |
| 排障 | 运维台 | 分诊 + 诊断包一键收集 |
| 离场 | 运维台 → **办公台** | 交接报告（运维台）→ 给客户发交付邮件、整理现场汇报 PPT、沉淀知识库（办公台） |

### 6.3 产品级拒绝清单（任何台都不做）

| 事项 | 说明 |
|---|---|
| 训练调优 / 模型研发 / 评测 / 量化蒸馏决策 | ML 工程与科研，不是本产品的层 |
| 客户关系管理（CRM） | 办公台辅助产出文档与邮件，但不管理客户关系 |
| 商务容量决策 | 运维台提供利用率事实，不做采购与预算分配 |
| 破坏性/攻击性安全操作 | 产品红线；安全能力保持防御向（蓝队核查） |

---

## 7. 运维台落地分期

| 期 | 内容 | 性质 |
|---|---|---|
| **P0** | GPU 采集器进 series（`internal/netdev/` 新增 gpuhealth.go）+ GPU 告警规则 + DashShell 第六屏智算大屏 + 设备 role 扩展（**2026-09-12**：采集器/告警已实现见 §3.3；大屏未做；role 扩展降档 P2 随 kind=gpu-host 立项） | 纯增量，不碰现有网络功能；做完界面立刻"能看智算"，FDE 模式同时受益（驻场看 GPU 现场时序切片） |
| **P1** | FDE 交付件（非工作台——承载面见 §3.2 作废注记）：readiness 电池、部署 runbook 模板、诊断包导出 | 复用 triage/CutoverRun/CaseBundle 骨架 |
| **P2** | 推理服务面（k8s/vLLM GET-only）+ 模式预设正式化（前台/时序保留/告警聚合/写档默认按模式切换）+ 对外文案泛化（netdev→运维台，绑定方法名不动） | 域包机制如果 P2 时仍然不疼，可以继续不做 |
