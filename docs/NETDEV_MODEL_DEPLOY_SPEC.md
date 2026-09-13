# NETDEV 模型部署面规格 —— 智算场景大模型推理服务的受控部署（调研 + 分期）

- 日期：2026-09-12
- 来源：两路专项 web 调研（部署形态矩阵 49 条 / vLLM 主线 runbook 20 步 + 22 条失败模式，全部带官方来源）+ 本仓现有能力约束核实（file-upload 200MB 上限、提案 cli 步骤走 runUnclassified 私有写路径、linux 读表白名单覆盖度）
- 关联：`NETDEV_0204_BATCH_SPEC.md`（上一批次）、`FDE_AIINFRA_OPS_GAP_SPEC.md` §4.1-3（推理服务面 P2）、`GPU_TODO.md`（dogfooding 载体）
- 回答的问题：**智算场景"部署一个大模型服务"，我们的运维台怎么承接？大模型确实不用单机 ollama 那类方式——生产形态是 vLLM/SGLang/TRT-LLM/NIM 的多卡/多机服务，本 spec 给出全套映射。**

---

## 一、调研结论 A：部署形态全景（提炼）

### 1.1 引擎定位（生产视角）

| 引擎 | 一句话定位 | 生产形态 |
|---|---|---|
| **vLLM** | 生态最广、K8s/量化/多模态兼容最好的默认选择 | pip 直装+systemd / 官方容器 / K8s Helm |
| SGLang | 前缀缓存+低延迟见长，Agent 负载占优 | 单机 `--tp 8` / 多机 `--dist-init-addr` |
| TensorRT-LLM | NVIDIA 极致性能；engine build 与 GPU 强绑定，运维摩擦最大 | NGC 容器 + `trtllm-serve`（无裸机 pip 主线） |
| TGI | HF 官方，生态动能已让位 vLLM 兼容路线 | docker `--num-shard 8` |
| NVIDIA NIM | "模型即容器"企业路线（NGC 目录，权重自动预取缓存，支持 air-gap） | docker / NIM Operator（K8s CRD） |

### 1.2 关键启动参数（运维台参数白名单的依据）

`--tensor-parallel-size`（张量并行卡数）、`--pipeline-parallel-size`、`--gpu-memory-utilization`（0–1，默认 0.9）、`--max-model-len`（省显存第一旋钮）、`--served-model-name`（API 暴露名）、`--quantization fp8|awq|gptq`、`--port`、`--distributed-executor-backend ray|mpirun`、环境变量 `HF_ENDPOINT`（镜像站）、`HF_HUB_OFFLINE=1`（离线强制）。

### 1.3 平台层选择

| 场景 | 推荐路线 | 权重分发 | 验证面 |
|---|---|---|---|
| 单机单卡（7B/8B） | vLLM pip 直装 + systemd | hf CLI 下载 + sha256 对账 | /health → /v1/models → 冒烟 → /metrics |
| 单机 8 卡（70B FP16/405B FP8） | Docker `vllm/vllm-openai` + TP8（`--ipc=host` 必需） | 中央存储/NFS 或本地盘+摆渡 | 同上 + `vllm bench serve` 基线 |
| 多机（>8 卡） | K8s（Helm/KServe LLMInferenceService + GPU Operator）；已有 Ray 用 Ray Serve；HPC 用 srun+Ray | PVC/NFS 共享或分发任务 | 同上 + DCGM/ray status |
| air-gap 智算中心 | 内网 Harbor + `/data/models` 本地路径 serve；NIM 预热缓存模式 | 中转区→摆渡→sha256 强校验→只读挂载 | /health、/v1/models、本地 benchmark |
| 企业"模型即容器"标准化 | NIM / NIM Operator | NGC 预取→NIM_CACHE_PATH | `/v1/health/ready` + /v1/models |

显存匹配速查：FP16≈2GB/B 参数、INT8≈1、INT4≈0.5，另加 15–20% KV cache；70B FP16≈TP4–8×80G、70B AWQ≈1×80G、405B FP8≈8×80G 单机。

---

## 二、调研结论 B：标准部署序列与失败模式（vLLM 主线）

### 2.1 步骤序列（20 步，只读/变更已标注——这正是编排原语的划分）

| 段 | 步骤 | 类型 | 命令/动作 |
|---|---|---|---|
| A 前置 | 1-7 | **只读×7** | nvidia-smi（驱动/CUDA 上限/显存/残留进程/compute_cap）、python3 --version、df -h、ss -ltnp 端口、curl -sI 源可达 |
| B 环境 | 8-10 | 变更×3 | venv 创建、pip install vllm（`--torch-backend=auto`）、hf download 权重（HF_ENDPOINT 镜像、断点续传） |
| C 校验 | 11-12 | **只读×2** | 文件清单 vs Hub tree API、sha256sum vs lfs.oid |
| D 启动 | 13-15 | 变更×3 | systemd unit（`TimeoutStartSec=1800`/`LimitNOFILE=65536`/`KillMode=control-group` 三件套必配）、daemon-reload、enable --now |
| E 验证 | 16-19 | **只读×4** | journalctl 关键行、curl /health + /v1/models、OpenAI 冒烟、vllm bench serve 基线 |
| F 回退 | 20 | 变更×1 | stop → nvidia-smi 确认显存释放 → 切旧 venv/旧权重 → start → 重跑 E 段 |

### 2.2 失败模式 Top（22 条提炼为 8 簇，全部可用只读命令检测）

| 簇 | 现象 | 只读检测 | 修复分类 |
|---|---|---|---|
| 驱动/CUDA 错配 | driver too old / CUDA insufficient / no kernel image | nvidia-smi（CUDA 上限 vs wheel 版本） | 升级驱动或换 wheel |
| 显存 | 启动即 OOM / 够用仍 OOM（预分配被误解）/ 运行期 OOM | nvidia-smi --query-compute-apps（残留进程）；日志 KV cache 行 | 清残留/调 gpu-memory-utilization/max-model-len/加 TP/量化 |
| 并行初始化 | TP≠卡数即退、NCCL init 失败 | nvidia-smi -L \| wc -l；df -h /dev/shm；NCCL_DEBUG=INFO | shm 加大/TP 改配置/升级 |
| 权重 | 下载中断、safetensors 损坏、磁盘写满 | ls/find 清单、sha256sum 对账、df -h | 断点续传/重下分片/扩容 |
| 端口/防火墙 | address already in use；本机通远程不通 | ss -ltnp；firewall-cmd --list-ports | 停旧/改 port/放行 |
| systemd 三坑 | start timeout（默认 90s<加载 10+ 分钟）、KillMode 不当显存僵尸、LimitNOFILE 过低 ZMQ 崩 | journalctl -u vllm；nvidia-smi compute-apps；/proc/PID/limits | TimeoutStartSec=1800/KillMode=control-group/LimitNOFILE=65536 |
| 引擎×模型不匹配 | architecture not supported、量化 kernel 不支持 | config.json architectures + quantization_config 对照 | 升级/换产物 |
| 假 hang | 加载 10+ 分钟疑似挂死 | `--load-format dummy` 对照定位 | 换本地盘（存储 I/O） |

---

## 三、承接设计：我们的运维台怎么"部署模型"

### 3.1 核心结论（三个映射，零新增写路径）

1. **只读段 → 读表检查步**：A/C/E 段 11 个只读命令中，`nvidia-smi`（全形态）、`df`、`ss`、`systemctl status/is-active/cat`、`journalctl`、`dmesg`、`curl -I` **已在 linux 读表白名单**。缺 4 个：`sha256sum`（权重对账）、`find /data/models`（清单）、`python3 --version`、`curl -s http://127.0.0.1:<port>/health`（GET 探活——现有仅 `curl -I` HEAD）。→ **D-1 读表增补**。
2. **变更段 → 提案 cli 步骤**：venv/pip/权重下载脚本/写 unit/daemon-reload/enable 全部是普通 shell 命令，走提案 `cli` 步骤的 `runUnclassified` 私有写路径（绕分类器但逐条审计、首败冻结、可回滚）——**不需要新增任何写路径**。权重下载属长时命令，建议包成脚本文件（file-upload 上传脚本 + cli 执行）以复用断点续传。
3. **验证段 → 语义门**：`curl /health` 200 + `/v1/models` 包含声明名（`--served-model-name`），正是割接引擎 `Gate{Command, Expect, SustainSec}` 的直接表达——`Expect: '"object":"list"'`、SustainSec 60（大模型加载 10+ 分钟 → gate TimeoutSec 需 1800 级）。
4. **整个部署 = 一条割接 runbook**：前置检查段（只读步）→ 变更步（提案引用）→ 语义门 → 决策点（值班看基线压测再放流量）→ 回退段（stop/切旧路径/start 重验）。割接引擎的全部既有能力（总倒计时、预注册、急停 hold、首败冻结、回退矩阵）直接继承。

### 3.2 大权重：登记-校验-分发分离（file-upload 200MB 上限保持不变）

模型权重几十~几百 GB，**不经运维台搬运**（SSH 通道既不稳也不该承载）。三段式：

| 段 | 谁做 | 运维台做什么 |
|---|---|---|
| 分发 | 客户通道（hf download/ModelScope/内网 HTTP/NFS/摆渡） | 提供脚本模板（file-upload 上传下载脚本 ≤200MB + cli 执行，断点续传在脚本内） |
| 校验 | 运维台只读步 | `sha256sum` 清单 vs 登记的 manifest 对账（读表增补后即只读检查步）；不一致=Finding |
| 登记 | 运维台 | 权重目录路径 + manifest 哈希记入部署 runbook/提案描述，`--model` 指向本地路径 + `HF_HUB_OFFLINE=1` |

### 3.3 分期

| 期 | 内容 | 性质 |
|---|---|---|
| **D-0（零代码，现在可用）** | 部署 runbook = 割接 runbook 手工编排：§2.1 的 20 步直接翻成 CutoverRun（只读步/提案引用/门/决策点）；本 spec §二 即编排蓝本 | 流程复用 |
| **D-1（小代码，随 0.2.5）** | ① 读表增补 4 命令（sha256sum / find / python3 --version / curl -s http://127.0.0.1 前缀）；② 部署 runbook 进 runbook 模板库（**前置 F1a：cutover 侧模板库机制待建**——提案侧 template.go 模式可参照）；③ vLLM 部署参数模板（TP/port/gpu-mem-util/model-path 白名单化） | 编排质量 |
| **D-2（P2 推理服务面，随 GAP_SPEC §4.1-3 合并）** | `/metrics` 抓取进 series：`vllm:kv_cache_usage_perc / num_preemptions / TTFT / ITL / generation_tokens`；告警规则枚举加 `infer.queue_depth / infer.ttft / infer.preemptions`；GpuBoard 加"推理服务"区（模型名/健康/TTFT 基线） | 观测闭环 |

### 3.4 拒绝清单（部署面同样适用）

| 不做 | 理由/归属 |
|---|---|
| 模型训练/微调/量化产物制作 | 训练框架域；运维台只加载现成产物 |
| HF 镜像站/权重仓库服务本体 | 平台基础设施；运维台只做消费端登记与校验 |
| ollama 类单机玩具运行时管理 | 非智算生产形态；vLLM/SGLang/NIM 才是目标形态 |
| K8s GPU Operator/KServe 集群安装 | K8s 平台域；K8s 路线下运维台只做 k8s-apply 提案的审批面 |
| 多机 Ray 集群搭建 | 部署多机大模型的**前置平台**归客户/平台团队；运维台承接其上的服务部署与观测 |

---

## 四、不变量核对

- **§1.4 写路径唯一**：部署变更全部走提案 cli 步骤（runUnclassified 逐条审计、首败冻结、组策略、回滚）；只读检查走读表白名单；零新增写路径。
- **gate 语义**：`/health`+`/v1/models` 验证门 = 割接语义门的直接实例；TimeoutStartSec 场景已由 B2 批次的 gateTimeout 夹制（sustain>timeout 自动放大）覆盖。
- **大文件**：file-upload 200MB 上限维持；权重分发在台外，台内只做登记与只读校验。
- **§10 UI**：部署 runbook 走主区割接形态 + 模板库，无新增工作台。

## 五、验收（分期）

- **D-0 验收**：用 §2.1 蓝本在测试 GPU 机手工编排一条部署 runbook 走通（或以预检红灯→override→执行→决策点的演练形态）。
- **D-1 验收**：读表增补后 4 命令从"教读表"变内建；模板库出现"vLLM 单机部署"模板；dry-run 渲染无分类器拒绝。
- **D-2 验收**：GpuBoard 出现推理服务区；`infer.*` 告警规则可在设置页配置。


---

## 四·补、部署模板分层组合模型与标准部署流程（2026-09-13 补，回应"三条模板限制发挥"）

### 4.1 分层组合（模板 = 骨架 × 引擎 × 硬件 × 变体，非平铺清单）

| 层 | 内容 | 数量 | 说明 |
|---|---|---|---|
| L-骨架 | 通用部署 20 步（§2.1 蓝本） | 1 | 只读检查→环境→权重→校验→启动→验证→决策点→回退 |
| L-引擎 | vLLM-systemd / vLLM-Docker / vLLM-K8s(Helm) / SGLang / MindIE / vLLM-Kunlun / vllm-gcu / NIM | 8 | 命令与参数差异层（引擎生态全部现成，我们只做编排模板） |
| L-硬件 | accel_profiles 机型档案 | 数据 | **不建独立硬件模板**——{{tp}}/{{quant}}/{{mem_limit}} 由档案填缺省（H20 96G 与 910B 64G 的差异在数据不在模板） |
| L-变体 | 单机 / 多机(head-worker 序) / **版本升级蓝绿** / air-gap | 3-4 | 升级蓝绿：旧服务保活→新实例并行→门验证→决策点切流→回收旧实例 |

种子集取常用交点 ~10-12 条；全矩阵靠组合渲染，不靠人工穷举。

### 4.2 标准部署流程走查（"怎么进行部署"的完整答案）

1. **建项目**：智算项目圈定设备组+地址域（G-P1 后含白名单/操作档）；
2. **选模板**：引擎档×变体组合，机型档案自动填 `{{tp}}/{{model_path}}/{{port}}` 缺省；dry-run 渲染全量预览（人审的是渲染结果）；
3. **权重三段式**（§3.2）：脚本下载（客户通道/hf/ModelScope）→ sha256 只读对账步 → manifest 登记；
4. **生成割接 runbook**：只读检查步（E3 四命令后全通）+ 变更步（提案引用：venv/pip/unit/enable）+ 验证门（`/health`+`/v1/models` 声明名比对，SustainSec 60）+ 决策点（基线压测后放流量）+ 回退段；
5. **审批**：两把锁（proposal+confirm2）；高危可四眼跨实例（G-L1）；
6. **执行**：总倒计时+语义门+急停可用；失败首败冻结/回退矩阵；
7. **观察期**：30 分钟健康对比，劣化立案+一键回滚提案；
8. **收尾**：前后对比报告+审计链+（可选）签名证据包。

**大规模出口**（>16 节点）：K8s 路径 = 同一流程但第 4 步的 runbook 退化为"一份 Helm/KServe manifest 的 k8s-apply 审批 + 集群级验证门"——fan-out 归调度器。**新情况出口**：无模板可套时走对话，agent 按蓝本+档案起草 runbook 人审（F1c③）。


---

## 五、全链支持度清单（联通→部署→开放服务，2026-09-13 补）

从服务器通电到 API 服务的 5 阶段 19 步全景（✅ 现成 10 / 🟡 已排期缺口 5 / ⛔ 域外但含审批·观测接缝 4-5）：

| 阶段 | 步骤 | 支持度 | 承接/缺口 |
|---|---|---|---|
| 一 物理/带外 | 1 上架加电 | ⛔ 机房工程 | — |
| | 2 BMC 联通状态 | 🟡 | redfish GET-only 纳管已有；BMC 变更域外 |
| | 3 固件升级 | 🟡 | 提案编排 ✅；固件包分发同权重三段式 |
| 二 联通/纳管 | 4 管理网联通（IP/SSH/堡垒） | ✅✅ | 数通本行：Via/TOFU/发现/promote |
| | 5 参数网组网（RoCE/IB/存储网） | ✅ | 数通引擎：交换机提案+轮询+割接 |
| | 6 OS 就绪检查 | ✅ | readiness 电池（E 批后全通） |
| 三 驱动栈 | 7 卡驱动/固件 | ✅ | 提案 cli 编排 + Version() 采集 |
| | 8 运行时配套（CUDA/CANN/…四层锁版本） | 🟡 | 安排案 ✅；配套校验=E4 机型档案数据 |
| | 9 容器运行时 | ✅/⛔ | 单机=提案；K8s 集群安装=平台域 |
| 四 集群化 | 10 互联验证（NCCL/HCCL test） | 🟡 | 只读步可编排；读表增补（GPU_TODO P1-2） |
| | 11 集群软件（Ray/MindCluster/K8s） | ⛔/✅审批面 | 厂商交付；其上 manifest=k8s-apply |
| | 12 共享存储挂载 | ✅/⛔ | 挂载=提案；存储系统=域外 |
| 五 部署/开放 | 13 权重分发 | 🟡 | F2 三段式 |
| | 14 引擎部署 | 🟡 | F1a/b/c 分层模板 |
| | 15 服务验证 | ✅ | 语义门/SustainSec/压测决策点 |
| | 16 网关/LB/证书 | ✅ | SrvConf+cert-replace（数通本行） |
| | 17 鉴权参数 | ✅ | 编排+secret store/脱敏；租户体系域外 |
| | 18 上线观察 | 🟡 | **F3 推理指标面（服务开放后唯一硬缺口）** |
| | 19 值班闭环 | ✅ | 已建成（告警/巡检/变更/回退/报告） |

关键结构判断：步骤 4-9 与 14-19 是连续覆盖带（同一引擎吃掉"组网→驱动"和"部署→开放"两头——两头本质都是网络工程）；10-12 靠审批面缝合；F3 落地后全链闭环。
