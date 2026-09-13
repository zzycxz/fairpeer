# NETDEV 异构智算硬件适配规格 —— 多厂商加速卡的采集/部署/观测（调研三轮 + 架构裁决）

- 日期：2026-09-13
- 来源：三轮专项调研（① 华为昇腾生态 39 条；② 燧原/昆仑芯/平头哥 30 条；③ 跨硬件编排架构三模式对比，全部带官方来源）+ 本仓源码硬件耦合点自查（5 项）
- 关联：`NETDEV_MODEL_DEPLOY_SPEC.md`（模型部署面，硬件无关编排蓝本）、`NETDEV_0204_BATCH_SPEC.md`、`FDE_AIINFRA_OPS_GAP_SPEC.md` §4.1、`GPU_TODO.md`
- 回答的问题：**智算服务器异构（NVIDIA/昇腾/燧原/昆仑芯/平头哥）× 跨设备部署 × vLLM/SGLang 多引擎——我们需要什么能力？一个能力通杀还是分而治之？**

---

## 〇、结论先行

> **编排与治理面通杀，采集与命令面分治（per-vendor 薄驱动）。**
> 割接/部署 runbook 引擎、提案审批、审计、门、回退、GpuBoard 展示、拒绝清单——全部硬件无关，一套通杀（已验证：本会话的部署 spec 即硬件无关编排蓝本）。厂商差异压进**"加速卡类型"薄驱动**（采集命令 + 解析器 + 错误码 catalog），走本仓已验证的 vendor 驱动扩展模式（与 netdev/driver 的 huawei-vrp/cisco/zte/h3c 结构同构——业界同构先例：HAMi 的 per-vendor device plugin 子仓 + 主仓统一面，CNCF Incubating）。
> **引擎支持（vLLM/SGLang 跨硬件）不需要我们做**：各硬件插件（vllm-ascend / vllm-gcu / vLLM-Kunlun / sgl-kernel-npu）由引擎/芯片厂商生态提供，我们承接的是其上的部署编排、健康观测与安全治理。

---

## 一、调研来源与可信度

| 轮 | 主题 | 产出 | 代表来源 |
|---|---|---|---|
| 1 | 华为昇腾生态（910B/A3/300I Duo） | 39 条 + 昇腾 vs NVIDIA 差异 Top10 | vllm-ascend（vllm-project 官方插件）、hiascend.com、MindIE-LLM、Ascend Hub |
| 2 | 燧原/昆仑芯/平头哥 | 30 条 + 三厂商对照表 | EnflameTechnology/vllm-gcu、baidu/vLLM-Kunlun、project-hami.io、kunlunxin.com |
| 3 | 跨硬件编排架构 | 三模式对比（vLLM 插件架构/SGLang 对照/K8s device plugin+DRA/HAMi/GPUStack/一体机） | kubernetes.io、Project-HAMi（CNCF Incubating）、gpustack、vllm-plugin-system 文档 |

---

## 二、五厂商软件栈对照总表（采集与部署面）

| 维度 | NVIDIA | 昇腾 Ascend | 燧原 Enflame | 昆仑芯 Kunlunxin | 平头哥 T-Head |
|---|---|---|---|---|---|
| 诊断 CLI | nvidia-smi | npu-smi info（-t usages/memory/health） | efsmi（-dmon/-q/-ptopo）；虚拟化 gim-smi（私有） | xpu-smi（+ topo -m） | **无公开 CLI**（自用+阿里云 PAI） |
| 深度诊断 | DCGM diag | ascend-dmi -dg | TopsRider 工具链 | xpu-smi + XDR 排障 | 未公开 |
| 运行时/栈 | CUDA | CANN（四层锁版本：驱动-CANN-torch_npu-引擎） | TopsRider（.run 包/官方镜像） | XRE（.run 包）+ XDR | SAIL（2026-07 开源，真武系列） |
| 通信库 | NCCL | HCCL（ranktable 组网） | ECCL | BKCL/XCCL/XPU Link；混训 BCCL | 未公开 |
| 推理引擎 | vLLM/TRT-LLM/SGLang | **vllm-ascend**（推荐）/ MindIE | **vllm-gcu**（0.11.0 fork）/ TopsInference | **vLLM-Kunlun 插件**（RFC #11162）/ FastDeploy | 无公开（PAI 视角替代） |
| vLLM 生态位 | 核心内建 | OOT 插件（vllm-project 官方，production） | fork 适配（独立仓库，Qwen/DeepSeek/GLM 全系） | 插件（baidu/vLLM-Kunlun，25+ 模型） | 无 |
| K8s resource | nvidia.com/gpu | huawei.com/NPU（+Volcano/MindX DL） | enflame.com/gcu·vgcu（私有分发，HAMi 可集成） | kunlunxin.com/xpu（1/2/4/8 卡 NUMA 感知；HAMi vXPU） | 无公开 device plugin |
| 容器挂载 | --gpus all | --device /dev/davinci0..7 + driver/dcmi/npu-smi 卷，或 Ascend Docker Runtime | --privileged 或 Container Toolkit 注入 /dev/gcu*；TOPS_VISIBLE_DEVICES | --privileged + --device=/dev/xpu0..7 + /dev/xpuctrl | 不适用 |
| 模型源 | HF/NGC | 魔乐 modelers.cn（昇腾原生 W8A8 优先）+ ModelScope | 官方镜像 + TopsCompressor 量化（GPTQ/AWQ/W8A16，无 FP8） | 百度专属 pip 源 + Paddle 系 | 不适用 |
| 量化 | FP8/AWQ/GPTQ | W8A8/W4A8（msmodelslim 转换） | GPTQ/AWQ/W8A16/INT8 KV | 模型矩阵按 P600-P900 标注 | 不适用 |
| 生态开放度 | 基准 | 高（开源全家桶 MindCluster） | 低（封闭，.run/私有 plugin） | 中（vLLM-Kunlun 开源，plugin 私有） | 最低（含光 800 无公开栈；真武 M890+SAIL 2026-07 才开源） |

关键事实：① 昇腾"四层锁版本"（驱动-CANN-torch_npu-引擎）是生产事故第一来源；② 燧原 vllm-gcu 是 0.11.0 fork（非插件），跟版成本高；③ 昆仑芯走 RFC #11162 标准插件（装标准 vLLM + vllm-kunlun 即插即用，但依赖百度专属 pip 源）；④ 平头哥含光 800 封闭自用，公开资料不足以纳入生产承接面（真武 M890 开源后重评）。

---

## 三、源码耦合点自查（5 项，逐项给结论）

| # | 位置 | 现状 | 对异构的影响 | 结论 |
|---|---|---|---|---|
| 1 | gpuhealth.go:269 采集判定 `d.GPU && d.Vendor=="linux"` + gpuQueryCmd/gpuXidCmd 硬编码 nvidia-smi | NVIDIA 专用 | 昇腾/燧原/昆仑芯主机采不到 | **M-1 主改点**：加速卡薄驱动（§四） |
| 2 | driver/hosts.go:73-74 读表：nvidia-smi 全形态 + npu-smi info/info -t 已在 | 昇腾只读半就绪 | 燧原/昆仑芯 CLI（efsmi/xpu-smi）缺 | M-1 按厂商补读表 |
| 3 | triage.go:62 电池已含 npu-smi info | 昇腾体检半就绪 | 仅缺解析 | M-1 顺带 |
| 4 | config vendor 枚举 `"huawei"` 已被网络设备（VRP）占用 | 命名冲突 | 硬件厂商身份**不能**新增 vendor 枚举值 | 独立 `accel` 维度（§四） |
| 5 | gpudash.go/GpuBoardView：XID 目录（NVIDIA 专属错误码体系）、85°C 色档、字段名 | XID 流/徽标 NVIDIA 语义 | 异构厂商错误码不同 | 错误码字段泛化 `ErrorCode/CodeKind`（§四） |

另：early-return 的三个 early-return 分支与拒绝清单不受影响；本仓 `netdev/driver` 的 `Driver{Key/Classify/...}` 扩展点与 HAMi per-vendor 子仓模式同构，是既有验证过的扩展路径。

---

## 四、架构设计：加速卡薄驱动（分治采集）+ 通杀编排

### 4.1 新维度：`accel`（加速卡类型），不动 vendor 枚举

```toml
[[netdev.devices]]
name = "ascend-a2-01"
vendor = "linux"        # 服务器 OS 语义（不动 huawei=网络设备 的既有占用）
gpu = true
accel = "ascend"        # 新字段：nvidia(默认) | ascend | enflame | kunlunxin | cambricon | ""(未指定→按探测)
```

- 采集驱动按 `accel` 分发；缺省 `""` → 探测（nvidia-smi 存在→nvidia；npu-smi 存在→ascend；以此类推）。
- **vendor 枚举不动**：`huawei` 已是网络设备语义，服务器是 vendor=linux + accel=ascend，两维度正交。

### 4.2 加速卡薄驱动接口（每厂商一个 Go 文件，归一到现有 GPUCard）

```go
// internal/netdev/accel/ — per-vendor thin drivers
type AccelDriver interface {
    Key() string                                    // nvidia | ascend | enflame | kunlunxin | cambricon
    Inventory(ctx, device) []GPUCard                // 归一：index/name/temp/util/mem/…
    HealthEvents(ctx, device) []AccelEvent          // 厂商错误码事件（归一前）→ catalog 分级
    Version(ctx, device) (driver, runtime, error)   // nvidia-smi 头部 / npu-smi 头部+CANN version.info / topsinfo / xpu-smi
}
```

- 输出归一到现有 vendor-neutral `GPUCard`/`GPUBoardDevice` 结构，**新增 `ErrorCode int` + `ErrorCodeKind string`**（XID ↔ 昇腾错误码 ↔ 燧原错误码），替代 XID 专属语义；severity 分级查各厂商 catalog 表（对齐既有 XID severe 设计，真机校准随 dogfooding）。
- 每厂商命令进各自读表白名单（nvidia-smi/journalctl 已在；npu-smi info 已在；enflame-smi/xpu-smi 按客户硬件补）。
- **密封/审计/脱敏/预算零改动**——薄驱动仍走 execSealed。

### 4.3 编排与展示通杀（已验证）

- 部署 runbook 引擎（割接形态）、提案审批、审计、语义门、回退矩阵——**硬件无关**。昇腾 910B 部署 Qwen 的 runbook 骨架（调研轮 1 §六 9 步）与 NVIDIA 版逐段同构，仅命令内容不同：步骤内容差异属**模板层**（vLLM 昇腾模板 vs NVIDIA 模板），不属引擎层。
- GpuBoard：矩阵/色档/XID 流的字段结构硬件无关；色档阈值 85/70°C 对 HBM 同样适用；`ErrorCodeKind` 徽标替代 XID 徽标。
- 引擎支持（vLLM-ascend/vllm-gcu/vLLM-Kunlun/SGLang-npu）：**借力各引擎/芯片生态的既有插件**，我们不开发引擎适配；运维台的价值是把这些异构引擎的"部署 runbook + 健康观测 + 变更治理"统一到一套流程。

### 4.4 明确不做（业界已分工）

- 算力切分/调度统一（HAMi/K8s device plugin/Volcano 领域）；
- 各厂商 CLI 的统一进程包装（只做 per-vendor 薄驱动 + 统一结构体，不收敛 CLI 本身）；
- 引擎硬件插件开发（vllm-ascend 等由引擎/芯片生态维护，我们消费）；
- CUDA shim/兼容层（TopsCloud 类，厂商域）。

---

## 五、分期

| 期 | 内容 | 验收 |
|---|---|---|
| **M-0（零代码）** | 昇腾 910B 部署蓝本：调研轮 1 §六 9 步骨架（npu-smi 前置检查→容器/venv→W8A8 权重→vllm-ascend/MindIE 启动→/v1/models 验证）手工编排为割接 runbook；GPU_TODO P1 演示一并覆盖 | 演示环境走通一条昇腾部署 runbook |
| **M-1（异构最小承接，约 3-4 天）** | ① `accel` 配置维度 + 驱动分发骨架；② **昇腾 npu-smi 薄驱动**（Inventory/Health/Version 三方法归一 GPUCard；`info -t memory` 解析 HBM；health 列→ErrorCode）；③ 读表增补 enflame-smi/xpu-smi（按客户硬件）；④ GPUBoard/GpuBoardView 泛化（ErrorCodeKind 徽标）；⑤ 昇腾部署 runbook 模板进模板库 | 测试环境（如可得 910B）采集出矩阵；无硬件则以 fixture 测试 + 命令白名单审阅验收 |
| **M-2（按客户硬件盘点排期）** | **寒武纪 MLU 驱动（cnmon≈nvidia-smi：温度/功耗/算力/显存；vllm-mlu 官方插件非 fork、DeepSeek-V4 Day0；k8s `cambricon.com/mlu`；torch_mlu 走 PrivateUse1）——注意：错误码完整表在 CNMon 手册登录墙后，Finding 分级先依赖 cnmon 健康/ECC 计数 + device-plugin 健康上报（gated 项：开发者社区账号/商务渠道获取）**、燧原 efsmi 驱动、昆仑芯 xpu-smi 驱动、各家错误码 catalog 精化、推理指标 /metrics 各厂商端点（D-2 合并）、平头哥（真武 SAIL 开源后重评） | 按客户采购清单；寒武纪优先级=用户点名 |
| **M-3（其余四家，2026-09-13 深度实勘后从"远期菜单"升格为分级菜单）** | **摩尔线程（production：vllm-musa 最活跃，v0.28-dev+V1 引擎+Day0 模型镜像，push 调研当日；mthreads.com/gpu；MT GPU Operator）**、**沐曦（production：vLLM-metax 官方插件月度对齐主线 0.24，C500 64G/C600 144G FP8；MXMACA 类 CUDA 全栈）**、海光（production 但生态封闭：hy-smi（ROCm 系，勘误：非 dcu-smi）；无开源 vLLM 仓，DTK 镜像分发+dcu-inference-cookbook）、天数智芯（early+：ixsmi 公开、推理栈闭源 IxFormer，但 Deep-Spark 开源 K8s 全家桶意外地全；iluvatar.ai/gpu；勘误：产品线为天垓/智铠，"倚天 710"是平头哥 CPU 系张冠李戴） | 客户硬件出现即立项；接入模式同 M-2 薄驱动（各家 smi 全部≈nvidia-smi 形态，映射成本低） |
| **拒绝（长期）** | §4.4 四项不做 | — |

---

## 六、不变量核对

- §1.4 写路径唯一：accel 驱动全部只读（采集/解析），零新增写路径；部署变更仍全部走提案 cli 步骤。
- §10 UI：GpuBoard 已是大屏 chip 内部视图，accel 维度只改行内容与徽标，无新增界面元素数量。
- 脱敏：npu-smi/efsmi/xpu-smi 输出走同一 Redact 上游链。
- vendor 枚举不动：accel 独立维度，huawei（网络）与 ascend（智算）正交。
- estop 红线：异构采集不触碰任何执行链。

## 七、调研来源摘要

- 昇腾：vllm-ascend（vllm-project 官方插件，production）、hiascend.com（npu-smi/CANN/MindIE/ModelZoo）、Ascend/mind-cluster、Ascend Docker Runtime、魔乐 modelers.cn、msit/msmodelslim。
- 燧原：EnflameTechnology/vllm-gcu（0.11.0 fork，Qwen/DeepSeek/GLM 全系）、TopsRider/efsmi/gim-smi、enflame.com/gcu·vgcu、ECCL。
- 昆仑芯：baidu/vLLM-Kunlun（RFC #11162 插件，25+ 模型）、xpu-smi/XRE/BKCL/XDR、kunlunxin.com/xpu、FastDeploy P800。
- 平头哥：含光 800 封闭自用（无公开栈）、真武 M890+SAIL 开源（2026-07）、阿里云 PAI 替代视角。
- 编排架构：vLLM plugin system（entry_points/Platform/WorkerBase）、SGLang platforms、K8s device plugin→DRA、HAMi（CNCF Incubating，per-vendor 子仓+主仓统一面）、GPUStack（9 类加速器统一纳管）、Atlas 800/TopsRider 一体机专属栈实例。


---

## 八、机型线谱与机型能力档案（补充：2026-09-13 两路机型调研）

> 回答"不同型号有不同配置，我们确定能支持吗"。结论：**能支持，但"支持"必须分层定义，且机型差异在同厂商内的代际间就存在（燧原 G3→G4、昆仑芯 P800→M100），不能按厂商粒度建模。** 解法是第八节的"机型能力档案"：结构化承载差异，展示/校验/建议全部数据驱动。

### 8.1 机型线谱摘要（在售/量产主力，完整表见调研轮 1/2 报告）

| 厂商 | 机型 | 卡数/形态 | 单卡显存 | 部署要点 |
|---|---|---|---|---|
| NVIDIA | H100/H200 SXM | 8卡 NVLink 全互联 | 80/141G HBM3e | TP 上限 8；vLLM 原生 |
| NVIDIA | **H20（特供）** | 8卡 | 96G/141G HBM3 | **高带宽低算力特例**：量化+大 KV 为主要手段 |
| NVIDIA | A100/A800 | 8卡（A800 特供：NVLink 降 400GB/s） | 40/80G | 唯一 MIG 存量主力 |
| NVIDIA | L40S/L20/4090D | 1-8卡 PCIe **无 NVLink** | 48G/48G/24G | TP>2 受 PCIe 限制；单卡量化推理为主 |
| NVIDIA | B200/GB200 NVL72 | 机柜级（72 GPU 单一 NVLink 域） | 180G HBM3e | 部署单元是机柜不是服务器 |
| 昇腾 | Atlas 800I A2 | 8×910B 整机 | 64G HBM | vllm-ascend **完整支持**（最成熟主力） |
| 昇腾 | Atlas 800I A3 | 8×910C 整机，可组 384 卡超节点 | HBM3e | 支持（独立 A3 kernels，特性演进中） |
| 昇腾 | Atlas 300I Duo | **第三方 x86 插卡**，双芯 | 48/96G LPDDR4X | vllm-ascend **Experimental**（固定 v0.10.0rc1） |
| 燧原 | 云燧 S60（G3） | 8卡 x86 插卡 | 48G HBM2e（第三方预估） | vllm-gcu 三条版本线；**无 FP8**；DeepSeek 仅 AWQ |
| 燧原 | L600（G4） | 模组，128 卡全互联 | 144G HBM | 原生 FP8；vllm-gcu 矩阵**未列** |
| 昆仑芯 | P800（Kunlun3） | 8卡 OAM | 96G HBM3 | vLLM-Kunlun **唯一列名硬件**；单机 8 卡 W8A8 跑 671B |
| 昆仑芯 | M100/M300（四代） | 超节点 | 96G HBM3 | 2026 量产；vLLM-Kunlun 未列入 |
| 平头哥 | 含光 800 / 真武 810E/M890 | 阿里云内 | — | **不外卖**；以阿里云 PAI 视角替代承接评估 |
| 寒武纪 | MLU370-X8 / MLU590（2026 放量）/ 690（媒体口径） | 8卡；590 OAM/PCIe5 | 48G LPDDR5 / **96G HBM2e**（勘误：非 32G） | vllm-mlu 官方插件（v0.11.2-dev，**落后主线 10+ 版**）；cnmon≈nvidia-smi；**错误码手册登录墙（gated）** |

### 8.2 机型差异的 12 个维度（同厂商内代际间即存在，非跨厂商才有）

① 互联拓扑决定 TP 上限（SXM 全互联/NVL 成对/无 NVLink/HCCS/超节点）；② 显存容量与类型跨代差（48G→144G）；③ 特供 SKU 算力裁剪（H20/A800/4090D/L20）；④ 精度支持代际差（S60 无 FP8、P800 无 FP8、M100 才有）；⑤ 引擎 readiness 按卡型三分支（昇腾 910b/A3/310p）；⑥ 引擎版本×软件栈成对升级（vllm-gcu 每版绑 TopsRider 最低版）；⑦ 图捕获/PD 分离按卡型可用；⑧ 8 卡服务器与超节点两代形态并存；⑨ 插卡 x86 vs OAM/整机柜交付；⑩ 互联协议私有且各代不同；⑪ 模型支持矩阵=卡型×量化×特性三维查表；⑫ 商业模式决定可得性（含光/真武不外卖）。

### 8.3 设计：机型能力档案（Machine Profile，数据驱动，不写死）

**原则**：采集与展示对机型自适应（已验证：GpuBoard 按采集实况渲染，卡数/显存天然自适应）；**部署建议与校验需要结构化的机型档案**——这是当前唯一真空（config 仅有自由文本 model 字段）。

```toml
# 机型能力档案（厂商×SKU 一行；运维设置内维护，数据来源=官方规格页+真机校准）
[[netdev.accel_profiles]]
accel   = "nvidia"            # nvidia | ascend | enflame | kunlunxin | cambricon
sku     = "H20"               # 机型/SKU 名（匹配 device.model 前缀或 accel_model）
cards   = 8                   # 常见卡数（部署建议用）
vram_gb = 96                  # 单卡显存
mem_type = "HBM3"
interconnect = "nvlink4"      # nvlink4 | nvlink-pair | pcie | hccs | supernode | eccl | xpu-link
special = "cn-market"         # 特供/裁剪标注（可选）
readiness = "production"      # 引擎 readiness 档：production | new | experimental | unknown（数据来源=引擎生态矩阵）
notes   = "高带宽低算力：量化+大 KV 为主要手段"
```

**消费方**（全部数据驱动，零 per-SKU 代码）：
1. **部署建议校验**（D-2 起）：runbook/提案声明 `tensor_parallel_size=8` 而目标机型 `cards=4` → 建议性校验告警；声明模型 70B FP16（140G）而 `cards×vram_gb=192G` → 通过（带 KV 余量提示）。
2. **GpuBoard 徽标**：readiness 档（experimental/特供）显式可见，不按架构推断。
3. **模板参数渲染**：runbook 模板 `{{tp}}` `{{model_path}}` 由档案缺省填入。

### 8.4 支持度承诺分层（对"确定可以支持吗"的精确回答）

| 层级 | 内容 | 承诺范围 |
|---|---|---|
| L1 采集观测 | 健康/温度/显存/利用率采集上板 | **全厂商全机型**（薄驱动归一；缺失厂商按客户硬件排期，见 §五 M-2） |
| L2 编排治理 | runbook/提案/审计/回退/急停 | **硬件无关**（割接引擎天然不感知机型） |
| L3 部署建议 | TP/显存/量化适配建议 | **按机型能力档案**——档案是数据，新机型=新档案行，无需发版 |
| L4 引擎兼容保证 | "这型号跑这引擎一定行" | **不承诺**（引擎生态域）；readiness 作为档案数据展示（vllm-ascend 按 910b/A3/310p 三档、vllm-gcu 仅 S60、vLLM-Kunlun 仅 P800），最终以真机验证为准（dogfooding） |

诚实声明：L4 是引擎生态的动态矩阵（vllm-ascend 按 A2/A3/300I 三条 kernels 分支、vllm-gcu 矩阵仅覆盖 S60、vLLM-Kunlun 仅 P800 列名），运维台的职责是把 readiness **作为数据展示并阻止未验证组合静默上线**，而非替引擎厂商背书。
