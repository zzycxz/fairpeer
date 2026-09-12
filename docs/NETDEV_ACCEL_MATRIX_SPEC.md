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
accel = "ascend"        # 新字段：nvidia(默认) | ascend | enflame | kunlunxin | ""(未指定→按探测)
```

- 采集驱动按 `accel` 分发；缺省 `""` → 探测（nvidia-smi 存在→nvidia；npu-smi 存在→ascend；以此类推）。
- **vendor 枚举不动**：`huawei` 已是网络设备语义，服务器是 vendor=linux + accel=ascend，两维度正交。

### 4.2 加速卡薄驱动接口（每厂商一个 Go 文件，归一到现有 GPUCard）

```go
// internal/netdev/accel/ — per-vendor thin drivers
type AccelDriver interface {
    Key() string                                    // nvidia | ascend | enflame | kunlunxin
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
| **M-2（按客户硬件盘点排期）** | 燧原 efsmi 驱动、昆仑芯 xpu-smi 驱动、各家错误码 catalog 精化、推理指标 /metrics 各厂商端点（D-2 合并）、平头哥（真武 SAIL 开源后重评） | 按客户采购清单 |
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
