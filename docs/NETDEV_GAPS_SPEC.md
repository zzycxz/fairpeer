# NETDEV 缺口总台账 spec —— 目前不足的地方（全量收敛 · 分批修）

- 日期：2026-09-13
- 定位：**0.2.3/0.2.4 落地之后的全部已知不足**的唯一收敛台账。编排与节奏见 `NETDEV_ROADMAP_SPEC.md`（版本线统一 0.2.x：全部工作归属 0.2.5 的七个子批 + 触发池 + 收益对照）。每条 = 来源 + 现状证据 + 影响 + 修法 + 验收 + 批次。修完一条勾一条；新发现一律先进本台账再排批。
- 已修内容**不重复收录**（十轮主题/逐行排查 + 两轮机型调研的已修项见各来源 spec 的修订记录：`NETDEV_0204_BATCH_SPEC.md`、`NETDEV_ACCEL_MATRIX_SPEC.md`、`NETDEV_MODEL_DEPLOY_SPEC.md`、`FDE_AIINFRA_OPS_GAP_SPEC.md`）。
- 批次划分：**E = 0.2.5 候选**（小改全清）｜**F = 0.3.x**（需设计或较大改动）｜**G = dogfooding**（真机门槛）｜**H = 域外/拒绝**（防 scope creep，声明不做）。

---

## 一、缺口来源

| 来源 | 内容 | 本台账条目 |
|---|---|---|
| 十轮代码排查（两轮×5，各 3 子任务） | 记录未修的 P2/P3 + 收尾注记 | G-A1、G-A2、G-C3 |
| 工作流八场景评估 | 场景 1/2/5/6/7 的支持度缺口 | G-A3、G-B1/B2/B3/B4、G-D 若干 |
| 模型部署面调研（NETDEV_MODEL_DEPLOY_SPEC） | D-1 读表增补/模板库、D-2 /metrics 观测 | G-A5、G-B1、G-B3 |
| 异构硬件调研（NETDEV_ACCEL_MATRIX_SPEC + 机型线谱两路） | M-1/M-2、机型能力档案、错误码 catalog | G-A4、G-B4/B5、G-C2 |
| 0.2.4 批次 spec（NETDEV_0204_BATCH_SPEC） | 批次 C dogfooding、B9 降噪 | G-C 全组、G-B6 |

---

## 二、缺口总台账

### 批次 E —— 0.2.5 候选（小改全清，每条 ≤半天）

| # | 缺口 | 现状证据 | 影响 | 修法 | 验收 |
|---|---|---|---|---|---|
| **E1** ✅已修（54d9c409） | Timeline `rejected` kind 无前端消费 | 后端已产出（timeline.go D5），但 LogWorkbench:83 `KIND_LABEL` 只有 change/finding/event、:164 的 rel 过滤会把 rejected 条目**整体滤掉**——D5 的"另立被拒操作过滤器"只做了后端一半 | 被拒写命令在时间关联轴上不可见，"谁尝试改了什么"的排查视角缺失 | KIND_LABEL 补 `rejected` 键（zh/en）；rel 过滤器补 rejected 开关；分类图标用护栏红点 | 在 LogWorkbench 时间轴可见被拒条目并可过滤 |
| **E2** ✅已修（54d9c409） | preset_key 后端无唯一性校验 | ValidateNetDev 仅查字符集（netdev.go preset_key 段）；两条规则同 preset_key 后端放行——前端 merged 去重已做，但 TOML 手工编辑或旧客户端可造成同 key 双规则 | 同 key 双规则使向导按 key 替换语义失效（一次替换一条，另一条残留） | ValidateNetDev alert 段：已见 preset_key 非空且重复 → 报错（对齐 name 查重的既有范式） | 同 key 双规则被加载/保存拒绝 |
| **E3** ✅已修（2026-09-13，安全子集收窄） | 原计划 4 命令中 **find/curl 经边界语义核实不可内建**：prefixMatches 空格边界允许尾参——`find -exec/-delete` 是写原语、`curl -o/-T/第二 URL` 是写/外联原语 | 安全子集已入读表：`sha256sum`（纯哈希）、`ls`/`ls -l`（纯列目录）、`python3 --version`（早退语义）；find/curl 走 extra_read 教读表逐命令授予 | 分类测试绿；部署蓝本权重对账/清单步内建可用 |
| **E4** ✅已修（dce7e91b） | 机型能力档案最小实现（accel_profiles） | 设计已在 ACCEL_SPEC §8.3（字段/消费方已定），代码未做 | 部署参数（TP≤卡数、模型显存 vs 机型显存）无校验数据源，全凭人工 | `[[netdev.accel_profiles]]` 配置段 + 部署建议校验（告警不硬失败）+ GpuBoard readiness 徽标消费 | H20 档案下声明 TP=8×不匹配卡数 → 建议性告警可见 |
| **E5** ✅已修（aa89b515） | GPU 值班 runbook 模板（文档级） | XID 怎么查/显存泄漏怎么查/NCCL hang 怎么查——散在调研报告（DEPLOY_SPEC §2.2 八簇失败模式表），未沉淀为值班手册 | 值班靠人记忆，排查路径不一致 | 把 DEPLOY_SPEC §2.2 八簇改写为《GPU 值班排查手册》（docs/，每簇：现象→只读检测→修复分类→升级路径） | 手册评审入库；值班培训可用 |
| **E6** ✅已修（5635d98d） | **curl 前缀的尾参通道**（E3 修理过程中发现的既有安全缺口） | 读表既有 `curl -I ` 前缀：空格边界允许追加 `-o`（写文件）/`-T`（上传）/第二 URL——注释声称"HEAD-only 无数据通道"但前缀模型不约束尾参 | curl 类前缀收紧为"URL 后无尾参"的专用校验（logPathReadOverride 同款旁路范式），或引入结构化 http-check 步骤类型替代裸 curl | 恶意/注入场景无法借 curl 读表项获得写原语 |
| **E7** ✅已修（dce7e91b） | **模型能力档案（model card，部署校验的模型侧数据源；2026-09-13 实勘 45 条校准）** | E4 机型档案只覆盖机器侧；模型侧无数据源（"70B FP16 140G"原为 spec 硬编码例子） | `[[netdev.model_cards]]`：name/params_B(总/激活，MoE 双值)/quant 档×**硬件族枚举**（同一模型不同卡主流量化档不同：H 系=FP8 原生、910B=W8A8（950 前无 FP8）、P800=W8A8C16、A 系/4090=BF16+AWQ）/显存估算（BF16≈2GB/B 规则已实勘确认）/kv 余量/多模态视觉塔增量。**内置初始集（实勘主流矩阵）**：Qwen2.5 全档+Qwen3 MoE 系（30B-A3B=智算中心单机标配/235B-A22B/Next-80B）+DeepSeek V3/R1（671B；昇腾官方口径 BF16≥4 台 A2、W8A8≥2 台）+**R1-Distill 蒸馏系（政企一体机主力，1.5-70B）**+GLM-4.5/-Air（106B 轻量档）/4.6+MiniMax-M1（456B，8×H800 可部署）；VLM（Qwen-VL/InternVL）进档案；Kimi K2（1T，官方最小 16×H200）标注"多走 API 少自建"；Llama 标注"占比下降，作蒸馏底座存量" | 声明 DeepSeek-671B BF16 而目标 2×8 卡 64G → 校验告警（需 4 台）；Qwen3-30B W8A8 vs 910B 单卡 → 通过 |
| **E8** | 深度诊断工具的读表策略（2026-09-13 核查补） | ascend-dmi（昇腾）/dcgmi diag（NVIDIA）未在读表：`-dg`/diag 是**带宽压测类负载**非纯读——裸进读表会给 agent 施压硬件的通道 | 裁决：留教读表（用户逐命令授予）或提案档（作为受控压测步骤）；版本查询形态（ascend-dmi -v）可进读表 | spec 记档即可，随 M-1 顺带 |
| **E9** ✅已修（d30258a7） | 互联/时钟只读检查命令读表（FULL_CHAIN P8 补） | ibstat/ibqueryerrors/perfquery/chrony tracking 不在读表——互联验收只读步被分类器拒 | linux 读表增补四命令（纯读：端口状态/错误计数/时钟偏差）；**ib_write_bw/nccl-tests/gpu-burn/fio 是流量负载类**按 E8 裁决走提案档 | 四命令分类 read；F15 只读步全通 |

### 批次 F —— 0.3.x（需设计或较大改动）

| # | 缺口 | 现状/证据 | 修法方向 | 依赖 |
|---|---|---|---|---|
| **F1a** ✅已修（本批：runbooktpl.go——save/render/apply/extract + 种子骨架，见 CHANGELOG 批②条目） | **cutover runbook 模板库机制**（开工审计修正：F1 原称"模板机制已就绪"不实——0204 批次 B 无此条目；template.go 是提案侧模板，cutover 侧模板库从未建过） | cutover runbook 每次手工起草（逐行精读轮 2 确认）；提案侧 template.go 的"模板=步骤+{{var}}+持久化+dry-run 渲染"模式可参照不可复用 | 照提案模板模式建 CutoverTemplate（骨架=只读步/提案引用/门/决策点+变量，save/render/dry-run/apply 生成 CutoverRun 草稿） | ~3-4 天 |
| **F1b** ✅内容库已入库（e202af94：模型侧三变体+运维侧四变体+F13 四组件，内置库 12 条种子；蓝绿×流量编排原语条目注记在模板 Notes；③b 骨架×引擎档×8 的组合矩阵=后续按需组合） | **部署模板体系（分层组合，非平铺清单）**（用户质询后重定义：原"三条模板"形态限制发挥） | 依赖 F1a 机制 | 四层组合：①基础骨架×1（通用 20 步：前置→环境→权重→校验→启动→验证→决策点→回退）②引擎档×8（vLLM-systemd/Docker/K8s-Helm、SGLang、MindIE、vLLM-Kunlun、vllm-gcu、NIM 容器——参数与命令差异层）③硬件绑定（**不建独立模板**：{{tp}}/{{quant}}/{{mem_limit}} 由 accel_profiles 机型档案填缺省）④规模/操作变体×**7**（单机、多机 head-worker 序、**版本升级蓝绿**、**DP 扩副本**（不停机，可全自动；HPA 指标=KV cache 利用率/排队深度而非 CPU）、**TP 重排=蓝绿换队**（实勘校准：TP degree 启动时固定，改 TP 必全量重启——新队拉起→切流→旧队下线；MoE Elastic EP 例外可运行时弹性）、**故障节点替换**（ECC 阈值自动化边界实勘：correctable>10 次/时→drain、反复 uncorrectable→cordon+隔离、物理换卡必人工——GPU 故障占训练中断约 58%，高频流程）、**服务下线**（idle 判定→摘流→删 endpoint→权重归档——业界无统一 runbook，结构化即增量；"确认无人再用"必人工审批））；⑤**流量编排原语**（实勘洞察：金丝雀/灰度/模型回滚本质同一机制=双版本+流量百分比，参照 KServe 三角色 revision 状态机建模；双版本显存翻倍→成本授权人工）。种子集 ~10-12 条（组合的常用交点），其余按需组合渲染或"runbook 另存为模板"生成 | E3/E4 + F1a |
| **F1c** ✅出口②已修（ExtractRunbookTemplate + binding；出口①模板=数据天然成立；出口③ agent 起草=对话面，机制侧 Save/Preview binding 已就绪） | 模板生态三出口（防"模板限制发挥"） | ①模板=数据非代码上限（TOML/JSON 可自由扩充）②**runbook→模板抽取**（跑通一次的部署可另存为模板，现场经验沉淀）③**agent 起草**（DEPLOY_SPEC 蓝本对 agent 可读：对话中"给这台 910B 部署 Qwen"→agent 按蓝本+机型档案起草 runbook→人审——模板管重复场景，对话管新情况） | F1a |
| **F2** | 权重登记-校验-分发落地 | 三段式设计在 MODEL_DEPLOY_SPEC §3.2（分发走客户通道；台内只做脚本上传+cli 执行+sha256 对账）；脚本模板与对账步未固化 | 下载脚本模板 + sha256 对账检查步进 runbook 模板；E3 的 sha256sum 读表是前置 | E3 |
| **F3** ✅已修（批④：infermetrics.go——抓取通道+series infer.*+告警枚举 7 项+GpuBoard 服务层区；K3 表见 docs/NETDEV_INFER_METRICS.md） | 推理指标面（D-2） | `/metrics` 抓取（vllm:kv_cache_usage_perc/num_preemptions/TTFT/ITL/generation_tokens）未做；告警枚举无 infer.*；GpuBoard 无服务层区 | GET 抓取通道（同 GPU 采集薄驱动模式，端点=推理服务而非加速卡）→ series `infer.*` + 告警枚举 + GpuBoard 服务区 | 无（可独立做） |
| **F4** ✅已修（批⑥：device.accel 维度 + accel=ascend 分发 pollAscendHealth（npu-smi info 两行一芯 fixture 解析、Health 非 OK→ErrorCode/npu-health 归一、逐格容错 note 通道）+ GPUCard/GPUBoardCard ErrorCode·ErrorCodeKind 归一列 + 大屏卡级 ERR 徽标 + RBB-deploy-vllm-ascend 模板 + CANN 版本读表前缀；catalog 分级与探测式缺省挂真机 G-C2） | M-1 异构最小承接 | 设计在 ACCEL_SPEC §四（accel 维度/npu-smi 薄驱动/GPUBoard ErrorCodeKind 泛化/昇腾模板）；代码未做 | accel 配置维度 + npu-smi 驱动三方法归一 GPUCard + GPUBoard ErrorCodeKind 徽标 | 真机验收依赖批次 C |
| **F5** | M-2 燧原/昆仑芯驱动 + 错误码 catalog | 依赖客户硬件盘点；efsmi/xpu-smi 读表与解析各一套 | 按客户采购清单排期 | 客户硬件 |
| **F6** | 审计/实况降噪（B9） | GPU 轮询每主机每轮 2-3 条审计行 + live 事件；审计链完整性优先所以不能简单不打 | 设计项：internal 调用方 live 聚合为一条/轮 + 审计按 class 检索视图 | dogfooding 反馈立项 |
| **F7** | Timeline 事件面扩展（P3 级） | rejected kind 落地后，"尝试改了什么"视角仍缺字段级 detail 富化（现有 Detail=Class） | 视消费反馈决定 | 反馈 |
| **F8** | **node-set 步骤类型**（大规模部署 Path B 前置） | CutoverStep.Device 是单设备字符串（cutover.go:94），"在 N 台并行执行 X"无法表达；100 节点部署 = 1000+ 串行步 ≈ 1-2h 纯编排 | 步骤目标支持设备组（group/label 选择器）+ 展开为并行子任务；执行汇总进步骤状态 | **立项条件：真实客户出现裸机大军团（>16 节点无 K8s）场景**；否则走 Path A（K8s 吸收复杂度，k8s-apply 一步触达） |
| **F9** | **并行执行器 + quorum 失败策略** | runner 单游标严格串行（cutover.go:731）+ 首败冻结——大规模下一次网络抖动冻结全网发布 | node-set 步骤的并发执行器（上限可配）；失败策略=继续其他/隔离失败节点/按比例(quorum)决定继续或中止，策略入提案人审 | 同 F8 触发条件 |
| **F10** | **权重分发进度跟踪** | 100 节点各自拉 140GB 的进度/断点续传状态无承载结构 | 分发任务实体：per-node 进度（脚本输出解析或文件探针）+ 汇总面板；复用 Job 引擎的步骤状态机 | 同 F8 触发条件 |
| **F11** ✅已修（ce4af8dd） | **series 分区/sqlite 化（R6 解冻，触发条件已量化）** | JSONL 单文件全扫描：100 节点×14 天 ≈ 3.4GB/6860 万行；SeriesRead 每查一台全扫、GpuBoard 构建=100 次全扫、CleanupSeries 整文件重写——**~20-30 节点开始退化，100 节点检查面不可用** | 按设备分片文件（`series/<device>.jsonl`，零新依赖快速解）或 sqlite 化（spec §5.3 原案）；迁移读端 | **>30 GPU 节点即触发（与 Path A/B 无关的硬伤）** |
| **F12** ✅已修（703a0058） | **并发参数化 + 巡检并发化** | gpuPollConcurrency=8 硬编码（100 节点≈56s/轮刚好打满 60s 间隔）；全网巡检纯串行（inspect.go 平 for 循环，100 节点 20-35 分钟/轮） | 两者改信号量并发 + 配置化上限（对齐 healthPollConcurrency=64 先例）；巡检串行→并发需保进度回调线程安全 | >30 节点即触发（与 F11 同批） |
| **F13** | **AI 平台组件部署模板包**（2026-09-13 两路调研新增：AI-native 企业栈的组件全是 K8s/Docker 部署件，F1 模板体系从模型服务自然扩展到平台组件） | F1 模板现仅覆盖推理引擎档 | LiteLLM 网关/Milvus 或 pgvector/Dify 或 Coze/Langfuse 四条组件部署 runbook 模板（K8s 路径 k8s-apply 审批）；配套只读检查步（/metrics 探活）与升级回滚变体 | 企业从试点→平台化（阶段 2-3）的建平台流程可被本台编排 |
| **F14** ✅已修（批④：parsePromText 通用 Prometheus 文本解析——引擎无关，映射按前缀择行，现收 vllm:*） | **F3 /metrics 抓取泛化**（同调研：LiteLLM/Kueue/Milvus/护栏服务全部暴露 Prometheus 格式 /metrics——F3 的抓取设计不必限定 vLLM） | F3 现按推理引擎设计 | F3 实现时抓取器做成通用 Prometheus 文本解析（端点登记制，J2 已裁），指标名前缀区分（vllm:*/litellm:*/自定义）；告警枚举随端点类型 | 一套采集通道覆盖网关/队列/向量库指标 |
| **F15** | **集群验收 runbook 模板**（FULL_CHAIN 核心产出：验收判定线是数据资产） | 判定线散在调研（busbw 机内≥80%/跨机≥92% 且 verify=0、gpu-burn OK/FAULTY、fio 基线、烤机时长按规模换算、YD/T 6961-2026 行标）无处承载 | 七步验收模板：dcgmi diag→gpu-burn→单机 NCCL→IB→多机 NCCL（判定线内置）→fio→E2E 压测（SLO 决策点）；随批②机制落地 | 测试环境走通一次七步验收 |
| **F16** | Redfish 批量写操作面（P1 补） | 现状 redfish 仅 GET；批量改 BMC IP/固件升级/BIOS 基线是建设期高频 | 提案编排 Redfish 写（POST/PATCH 白名单）或域外声明——**待裁决 J6** | 可批量设置 BMC IP 演示或域外入库 |
| **G-P1** | **项目安全域**（PROJECT_SCENARIO_SPEC §二+§七 V1/V2） | 项目现状=纯视图分组（activeProject 是前端 localStorage 态，后端零项目上下文）；提案无 project 字段；confirm2 无身份概念 | 会话项目上下文管道（前端→后端会话态→guardrail/提案/发现）+ 提案补 project 字段 + NetDevProject 升级（type/allow/deny/policy/confirmers+身份定义）+ 三段式表单 | 0.3.x 主体，**~1.5-2 周（V1 上调）** |
| **G-P2** | 项目化模板联动（同上 §五） | 模板无项目类型过滤 | 部署模板按项目类型缺省 | 小 |
| **G-L1** | **四眼跨实例 confirm2**（§四 L1+§七 V3） | confirm2 仅同机布尔位；linkpeersignal 只有 /pair/* 三端点、无 relay/数据面 | 新数据通道（signal 加 relay 端点或配对后 P2P 直连，对方 Ed25519 公钥可用）+ 确认请求消息 + 对端确认卡 | P-3a，**可行性中（V3 下调），~2 周** |
| **G-L2** | **审计链 peer 互锚**（L2+V3） | AnchorAudit 锚定目标=本节点 trust domain 链（非跨机器） | peer 间锚记录提交路径 + 定期互锚调度 | P-3a |
| **G-L3** | 签名证据包（L3） | 诊断包无签名 | trustdomain 私钥签名+验签 | P-3b |
| **G-L4** | 态势共享（L4） | linkpeersignal 仅信令无数据面 | 探索 | P-3c |

### 批次 G —— dogfooding（真机门槛，GPU_TODO 载体）

| # | 事项 | 门槛 |
|---|---|---|
| G-C1 | XID 证据源真机校准（journalctl -g 可用性/-q 兜底形态/severe 分级表对照 catalog） | 910B/NVIDIA 真机 |
| G-C2 | 昇腾部署蓝本演练（910B 蓝本 9 步）+ M-1 驱动验收 | 昇腾真机 |
| G-C3 | vllm-ascend readiness 真机验证（910b/A3/310p 三档 × W8A8） | 昇腾真机 |
| G-C4 | GPU_TODO P0-1/2/3 演示材料（录屏/截图归档） | 真机 |
| G-C5 | 机型能力档案数据录入与校准（13+ SKU 表逐行对真机规格页） | 各机型到场 |

### 批次 H —— 域外/拒绝（声明不做，防 scope creep）

用户账号体系/RBAC 细粒度 ACL 矩阵（单机桌面无用户体系，两把锁+可选项哲学）｜算力使用权限分配（K8s RBAC/配额面）｜集群资源池划分（调度域）｜RMA 工单流（客户 ITSM 域）｜容量采购决策（商务域；运维台只供给利用率事实）｜K8s GPU Operator/集群安装（平台域）｜模型训练/微调/量化制作（训练框架域）｜HF 镜像站/权重仓库服务本体（基础设施域）｜算力切分调度（HAMi/device plugin 域）｜推理服务灰度/autoscale 策略（推理平台域）｜CRM/客户关系（非软件域）。

---

## 二·补、批次 K —— 开工前设计项（各主体批次的准入门槛）

| # | 设计项 | 是谁的前置 | 量级 |
|---|---|---|---|
| K1 | G-P1 三件设计：会话项目上下文载体（倾向 per-tab 后端态）/ confirmers 身份模型（自报+可选 trustdomain 签名）/ NetDevProject schema 迁移（D1 软降级）——详见 PROJECT_SCENARIO_SPEC §八 | G-P1（0.3.x 主体） | 半天成文 |
| K2 | G-L1 通道选型：signal 服务加 relay 端点 vs 配对后 P2P 直连（对方 Ed25519 公钥可用）——V3 修正后从"信令现成"降为"需新数据通道" | G-L1/G-L4（P-3a） | 半天+原型验证 |
| K3 ✅已修（批④：docs/NETDEV_INFER_METRICS.md——七项映射+建议阈值起步+派生语义纪律） | F3 指标-阈值映射表：vllm:* 指标（kv_cache_usage_perc/num_preemptions/TTFT/ITL/generation_tokens）→ infer.* 告警枚举与默认阈值——spec 完成度审计确认缺这张表 | F3（推理指标面） | 半天 |

## 二·补、批次 U —— 用户侧待办（非代码，列此使台账完整）

| # | 事项 | 说明 |
|---|---|---|
| U1 | 推送分支 | 本地领先远端 71+ 提交（0.2.3~0.2.5 三版内容）；推送触发 CI 权威门禁（race 通道/6 平台构建——本地仅覆盖 Windows） |
| U2 | 版本切割 | [Unreleased] 三批（智算大屏/卫生清仓/tools 修复）切 0.2.6 标题；desktop/wails.json productVersion 0.2.3 → 对齐实际（0.2.5 已发内容） |
| U3 | 裁决 J4/J5 | J4 项目外设备可见性（倾向只读+拒操作）/ J5 confirmers 绑身份（倾向绑）——卡 G-P1 开工 |
| U4 | 真机到位 | G 批全部（XID 校准/昇腾演练/readiness 验证/演示材料/机型档案校准）+ M-1 驱动验收依赖 |
| U5 | 并行批次协调 | 共享文件整写覆盖已发生两次（mock Skip/CHANGELOG 条目）；按 0204 spec A6+ 标记清单核对法在每次并行落地后核对 |

## 三、工作流八场景 × 缺口映射（评估结论存档）

| 场景 | 支持度 | 未闭环项（→台账编号） |
|---|---|---|
| 1 日常巡检值班 | ✅ | E5（值班手册） |
| 2 模型上线/升级 | 🟡 | F1（模板库）、F2（权重三段式）、E4（机型档案校验） |
| 3 故障处理 | ✅ | F4（非 NVIDIA 采集，M-1）、F3（引擎层日志，P2） |
| 4 变更管理 | ✅✅ | — |
| 5 推理服务运维 | ❌→🟡 | F3（/metrics 指标面，P2） |
| 6 容量与资源管理 | ❌半 | 观测已有（series）；建议/配额 = 域外（G-D） |
| 7 硬件生命周期 | 🟡 | F4（异构纳管）、RMA 流=域外 |
| 8 报告复盘 | ✅ | GPU 专项段落随 Finding 自然进入；可选 GPU 周报（P3，按反馈） |

---

## 四、待裁决项

| # | 事项 | 选项 | 当前倾向 |
|---|---|---|---|
| J1 | E2 preset_key 唯一性：硬失败 vs 加载警告 | 硬失败（对齐 name 查重）/ 警告 | **硬失败**（对齐 name 查重范式，双规则同 key 必然是编辑错误） |
| J2 | F3 推理指标抓取的端点发现方式 | 手工登记（设置页）/ k8s 服务注解自动发现 | 先手工登记（与设备纳管同范式），自动发现随 P2 |
| J3 | E4 机型档案的录入界面 | TOML 手工（与现有一致）/ 设置页表单 | 先 TOML + example 注释，表单随反馈 |

---

## 五、总验收门禁（每批次通用）

build（internal/cmd/desktop）｜vet｜netdev/config 全量测试｜desktop 三包测试｜tsc｜vite build｜定向 race（Estop/Cutover/GPU 族）。批次 E 另加：Timeline rejected 条目的人工可见性检查。


---

## 六、大规模贴合度评估（2026-09-13 补，回答"大集群全场景贴合吗"）

**结论：≤16 节点贴合（现有引擎 + E/F 批）；100+ 节点裸机军团结构性不贴合（F8-F10），但业界正解是 K8s 吸收复杂度（Path A）而非我们补齐编排并行性；检查面两个量化硬伤（F11/F12）与路径无关，>30 节点即触发。**

三面 × 规模量化：

| 面 | ≤16 节点 | ~30-50 节点 | 100+ 节点 |
|---|---|---|---|
| 检查 | ✅ | 🟡（F11/F12 触发线） | ❌ 未修 F11/F12 则不可用；修后可测 |
| 修改（单服务参数） | ✅ | ✅ | ✅ |
| 修改（舰队配置） | ✅（串行可忍） | 🟡 10-15min/轮 | 🟡（同左，勉强） |
| 部署（裸机直部署） | ✅（模板 + E3/E4 后） | 🟡 编排开销显性化 | ❌ 需 F8-F10（触发条件：真实场景） |
| 部署（K8s 路径） | ✅ | ✅ | ✅（k8s-apply 一步触达，调度器负责 fan-out） |

路径裁决建议：**Path A（K8s/Helm/KServe/NIM Operator manifest 为大集群部署对象）为正解**，与业界分工一致（调度域归 K8s/HAMi，变更治理/验证/回滚归本台）；Path B（F8-F10 裸机军团引擎改造）仅在真实客户场景出现时立项。超节点形态（Atlas A3/天池/GB200 NVL72）随机带厂商集群软件，归 Path A 同类。
