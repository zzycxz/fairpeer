# NETDEV 缺口总台账 spec —— 目前不足的地方（全量收敛 · 分批修）

- 日期：2026-09-13
- 定位：**0.2.3/0.2.4 落地之后的全部已知不足**的唯一收敛台账。每条 = 来源 + 现状证据 + 影响 + 修法 + 验收 + 批次。修完一条勾一条；新发现一律先进本台账再排批。
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
| **E1** | Timeline `rejected` kind 无前端消费 | 后端已产出（timeline.go D5），但 LogWorkbench:83 `KIND_LABEL` 只有 change/finding/event、:164 的 rel 过滤会把 rejected 条目**整体滤掉**——D5 的"另立被拒操作过滤器"只做了后端一半 | 被拒写命令在时间关联轴上不可见，"谁尝试改了什么"的排查视角缺失 | KIND_LABEL 补 `rejected` 键（zh/en）；rel 过滤器补 rejected 开关；分类图标用护栏红点 | 在 LogWorkbench 时间轴可见被拒条目并可过滤 |
| **E2** | preset_key 后端无唯一性校验 | ValidateNetDev 仅查字符集（netdev.go preset_key 段）；两条规则同 preset_key 后端放行——前端 merged 去重已做，但 TOML 手工编辑或旧客户端可造成同 key 双规则 | 同 key 双规则使向导按 key 替换语义失效（一次替换一条，另一条残留） | ValidateNetDev alert 段：已见 preset_key 非空且重复 → 报错（对齐 name 查重的既有范式） | 同 key 双规则被加载/保存拒绝 |
| **E3** | 部署面读表增补 4 命令 | hosts.go 读表无 sha256sum / find / python3（--version）/ curl -s（本机 GET）——部署 runbook 的权重对账/清单/探活步会被分类器拒（MODEL_DEPLOY_SPEC §3.1-1） | 部署 runbook 的只读检查段跑不全 | linux 读表增补：`sha256sum`、`find`、`python3 --version`、`curl -s http://127.0.0.1`（前缀，限本机探活语义） | 四命令经 execSealed 走通且分类为 read；部署蓝本 20 步的只读段全通 |
| **E4** | 机型能力档案最小实现（accel_profiles） | 设计已在 ACCEL_SPEC §8.3（字段/消费方已定），代码未做 | 部署参数（TP≤卡数、模型显存 vs 机型显存）无校验数据源，全凭人工 | `[[netdev.accel_profiles]]` 配置段 + 部署建议校验（告警不硬失败）+ GpuBoard readiness 徽标消费 | H20 档案下声明 TP=8×不匹配卡数 → 建议性告警可见 |
| **E5** | GPU 值班 runbook 模板（文档级） | XID 怎么查/显存泄漏怎么查/NCCL hang 怎么查——散在调研报告（DEPLOY_SPEC §2.2 八簇失败模式表），未沉淀为值班手册 | 值班靠人记忆，排查路径不一致 | 把 DEPLOY_SPEC §2.2 八簇改写为《GPU 值班排查手册》（docs/，每簇：现象→只读检测→修复分类→升级路径） | 手册评审入库；值班培训可用 |

### 批次 F —— 0.3.x（需设计或较大改动）

| # | 缺口 | 现状/证据 | 修法方向 | 依赖 |
|---|---|---|---|---|
| **F1a** | **cutover runbook 模板库机制**（开工审计修正：F1 原称"模板机制已就绪"不实——0204 批次 B 无此条目；template.go 是提案侧模板，cutover 侧模板库从未建过） | cutover runbook 每次手工起草（逐行精读轮 2 确认）；提案侧 template.go 的"模板=步骤+{{var}}+持久化+dry-run 渲染"模式可参照不可复用 | 照提案模板模式建 CutoverTemplate（骨架=只读步/提案引用/门/决策点+变量，save/render/dry-run/apply 生成 CutoverRun 草稿） | ~3-4 天 |
| **F1b** | 部署 runbook 三条模板（D-1 主体） | 依赖 F1a 机制 | 「vLLM 单机」「vLLM 多机」「昇腾 vllm-ascend」入库，参数 `{{tp}}/{{model_path}}/{{port}}` 由机型档案缺省填入 | E3/E4 + F1a |
| **F2** | 权重登记-校验-分发落地 | 三段式设计在 MODEL_DEPLOY_SPEC §3.2（分发走客户通道；台内只做脚本上传+cli 执行+sha256 对账）；脚本模板与对账步未固化 | 下载脚本模板 + sha256 对账检查步进 runbook 模板；E3 的 sha256sum 读表是前置 | E3 |
| **F3** | 推理指标面（D-2） | `/metrics` 抓取（vllm:kv_cache_usage_perc/num_preemptions/TTFT/ITL/generation_tokens）未做；告警枚举无 infer.*；GpuBoard 无服务层区 | GET 抓取通道（同 GPU 采集薄驱动模式，端点=推理服务而非加速卡）→ series `infer.*` + 告警枚举 + GpuBoard 服务区 | 无（可独立做） |
| **F4** | M-1 异构最小承接 | 设计在 ACCEL_SPEC §四（accel 维度/npu-smi 薄驱动/GPUBoard ErrorCodeKind 泛化/昇腾模板）；代码未做 | accel 配置维度 + npu-smi 驱动三方法归一 GPUCard + GPUBoard ErrorCodeKind 徽标 | 真机验收依赖批次 C |
| **F5** | M-2 燧原/昆仑芯驱动 + 错误码 catalog | 依赖客户硬件盘点；efsmi/xpu-smi 读表与解析各一套 | 按客户采购清单排期 | 客户硬件 |
| **F6** | 审计/实况降噪（B9） | GPU 轮询每主机每轮 2-3 条审计行 + live 事件；审计链完整性优先所以不能简单不打 | 设计项：internal 调用方 live 聚合为一条/轮 + 审计按 class 检索视图 | dogfooding 反馈立项 |
| **F7** | Timeline 事件面扩展（P3 级） | rejected kind 落地后，"尝试改了什么"视角仍缺字段级 detail 富化（现有 Detail=Class） | 视消费反馈决定 | 反馈 |
| **F8** | **node-set 步骤类型**（大规模部署 Path B 前置） | CutoverStep.Device 是单设备字符串（cutover.go:94），"在 N 台并行执行 X"无法表达；100 节点部署 = 1000+ 串行步 ≈ 1-2h 纯编排 | 步骤目标支持设备组（group/label 选择器）+ 展开为并行子任务；执行汇总进步骤状态 | **立项条件：真实客户出现裸机大军团（>16 节点无 K8s）场景**；否则走 Path A（K8s 吸收复杂度，k8s-apply 一步触达） |
| **F9** | **并行执行器 + quorum 失败策略** | runner 单游标严格串行（cutover.go:731）+ 首败冻结——大规模下一次网络抖动冻结全网发布 | node-set 步骤的并发执行器（上限可配）；失败策略=继续其他/隔离失败节点/按比例(quorum)决定继续或中止，策略入提案人审 | 同 F8 触发条件 |
| **F10** | **权重分发进度跟踪** | 100 节点各自拉 140GB 的进度/断点续传状态无承载结构 | 分发任务实体：per-node 进度（脚本输出解析或文件探针）+ 汇总面板；复用 Job 引擎的步骤状态机 | 同 F8 触发条件 |
| **F11** | **series 分区/sqlite 化（R6 解冻，触发条件已量化）** | JSONL 单文件全扫描：100 节点×14 天 ≈ 3.4GB/6860 万行；SeriesRead 每查一台全扫、GpuBoard 构建=100 次全扫、CleanupSeries 整文件重写——**~20-30 节点开始退化，100 节点检查面不可用** | 按设备分片文件（`series/<device>.jsonl`，零新依赖快速解）或 sqlite 化（spec §5.3 原案）；迁移读端 | **>30 GPU 节点即触发（与 Path A/B 无关的硬伤）** |
| **F12** | **并发参数化 + 巡检并发化** | gpuPollConcurrency=8 硬编码（100 节点≈56s/轮刚好打满 60s 间隔）；全网巡检纯串行（inspect.go 平 for 循环，100 节点 20-35 分钟/轮） | 两者改信号量并发 + 配置化上限（对齐 healthPollConcurrency=64 先例）；巡检串行→并发需保进度回调线程安全 | >30 节点即触发（与 F11 同批） |
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
