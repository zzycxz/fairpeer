# GPU 值班排查手册

- 日期：2026-09-13（批次 E5 / 0.2.5 批①）
- 来源：NETDEV_MODEL_DEPLOY_SPEC §2.2 八簇失败模式表（22 条提炼，全部可用只读命令检测）+ 本仓 GPU 采集/告警/XID 通道（gpuhealth.go / alert.go / gpudash.go）
- 用途：值班培训与当班速查。每簇四段：**现象 → 只读检测 → 修复分类 → 升级路径**。
- 纪律：手册中所有检测命令均在 linux 读表白名单内（或走 extra_read 逐命令授予）；修复动作凡属变更一律走提案/割接，**值班不当班改设备**。
- 命令出处约定：`nvidia-smi` 全只读形态、`npu-smi info`、`ibstat`、`chronyc tracking` 等已进读表（0.2.5 批①）；`journalctl`/`ss`/`df`/`systemctl status` 为存量读表。

---

## 簇 0 · 硬件健康（XID / 温度 / 显存水位）——fairpeer 特有通道

**现象**：节点掉卡、训练中断报 `CUDA error`、大屏温度/显存红色、XID 事件流立案。

**只读检测**（优先用界面，CLI 兜底）：
1. **智算大屏**（DashShell 第六屏）：全卡温度矩阵（>85°C 红）、显存水位、XIDMax 徽标；卡片标题旁的机型档案徽标显示 readiness/特供标注。
2. **XID 事件流**：活动 Finding 给出码值 + journalctl 证据行。severe 分级表（gpuhealth `gpuXidSevere`）：**63/64/74/79/80/81/92/93/94/95/8/48 → critical**（双位_err 级硬件事件），其余 warning。
3. CLI 复核：`nvidia-smi -q`（ECC/重试/墜落页）、`journalctl -k | grep -i xid`（证据源）。
4. 告警规则面：`gpu.xid` / `gpu.temp` / `gpu.mem_pct` 规则已配置 for_rounds 防抖，夜班窗口静默由 alerts 配置管。

**修复分类**：
- XID 79（GPU 掉卡/总线）→ **硬件簇**：不负接线、不热插拔判断，直接升级。
- XID 48/63/64（双位 ECC/页退役）→ 记录 + 排期维护窗口；非 critical 不当场动。
- XID 92/93/94/95（ECC 致命/单比特不可恢复）→ 立即隔离该卡/节点（走提案改调度标签，不 ssh 手改）。
- 温度红线 → 查机房空调/风道（物理），软件侧降频不建议值班做。

**升级路径**：critical XID 立刻； ECC 双位当日； 温度 30 分钟不降→机房。

---

## 簇 1 · 驱动 / CUDA 错配

**现象**：启动即退：`driver too old` / `CUDA insufficient` / `no kernel image is available`。

**只读检测**：`nvidia-smi`（右上角 Driver/CUDA Version = 驱动支持的 **上限**）；对照部署 runbook 声明的 wheel（vLLM 版本 × CUDA 版本矩阵）；昇腾对应 `npu-smi info`（驱动/固件版本列）。

**修复分类**：升级驱动（变更，提案+维护窗口）或 换 wheel（变更，重装容器/venv）。

**升级路径**：值班记录证据（两条命令输出）→ 转部署负责人；不当场升驱动。

---

## 簇 2 · 显存（OOM / 泄漏 / 残留进程）

**现象**：启动即 OOM；"显存明明够用仍 OOM"（vLLM 预分配语义被误解）；运行期逐步 OOM（泄漏）。

**只读检测**：
1. `nvidia-smi --query-compute-apps=pid,process_name,used_memory --format=csv`：**残留进程**（上一次崩溃没清干净）是最常见成因。
2. `nvidia-smi --query-gpu=memory.used,memory.total --format=csv`：水位对照大屏 series（`gpu.<i>.mem_pct` 24h 趋势——爬坡型=泄漏，阶跃型=新进程）。
3. 服务日志 `journalctl -u <svc> | grep -i "kv cache"`：实际分配量 vs 预期。

**修复分类**：清残留（kill 残留 PID=变更，提案）；调 `gpu-memory-utilization` / `max-model-len` / 加 TP / 量化降档（变更，重启服务）。

**升级路径**：值班先定位并截图 → 部署负责人改参；泄漏复现两轮以上按缺陷立案。

---

## 簇 3 · 并行初始化（TP / NCCL init 失败）

**现象**：TP≠卡数即退；`NCCL init failed` / 卡在 `NCCL INFO` 刷屏不动。

**只读检测**：
1. `nvidia-smi -L | wc -l`：实卡数 vs 声明 TP（大屏卡片数即实况；档案偏差告警会提示卡数对不上）。
2. `df -h /dev/shm`：容器 shm 64MB 默认值是 NCCL init 失败经典成因。
3. `ibstat`：互联口状态（Down/Init 端口 = 通信域不完整）。

**修复分类**：shm 加大（变更）；TP 改配置（变更）；互联口 Down（硬件/网络簇，升级）。

**升级路径**：NCCL hang 连续 2 轮采样无进展 → 按簇 0 查互联 + 升级网络值班。

---

## 簇 4 · 权重完整性

**现象**：加载报 safetensors 损坏/缺分片；`No such file`；磁盘写满。

**只读检测**：
1. `ls -l /data/models/<name>/`：分片清单 vs manifest（应有 `.safetensors` 全序号 + 2 个 json）。
2. `sha256sum` 对账：与部署 runbook 登记的 manifest 哈希逐一比对（运维台校验步可自动跑）。
3. `df -h /data`：下载通道把盘写满是常见伴生成因。

**修复分类**：断点续传/重下分片（客户通道，运维台只提供脚本模板——权重不经运维台搬运）；扩容（变更）。

**升级路径**：哈希不一致 → **停用该副本**（提案摘流量）再重下，不带病上线。

---

## 簇 5 · 端口 / 防火墙

**现象**：`address already in use`；本机 curl 通、远程不通。

**只读检测**：
1. `ss -ltnp 'sport = :8000'`：旧进程没退干净占着端口。
2. `curl -I http://127.0.0.1:8000/health`：本机探活（HEAD-only 读表）。
3. `firewall-cmd --list-ports`（或 `iptables -L -n` 读形态）：服务端口未放行。

**修复分类**：停旧进程/改 port（变更）；放行端口（变更，防火墙规则走提案——那是数通侧本职）。

**升级路径**：跨区不通且本机正常 → 网络值班接手（tracepath 分段定位）。

---

## 簇 6 · systemd 三坑（服务托管部署专属）

**现象**：start 超时（默认 90s < 大模型加载 10+ 分钟）；stop 后显存仍被占（KillMode 不当成僵尸）；服务反复崩（LimitNOFILE 过低 ZMQ 崩）。

**只读检测**：
1. `journalctl -u <svc> -n 100`：超时/崩溃证据。
2. `systemctl show <svc> -p TimeoutStartUSec,KillMode,LimitNOFILE`：三个关键值一眼对错。
3. `systemctl status <svc>`：主 PID 与存活态。

**修复分类**：三个修复全部在 unit 文件里——`TimeoutStartSec=1800` / `KillMode=control-group` / `LimitNOFILE=65536`，属**变更走提案**（改 unit → daemon-reload → restart，割接 runbook 编排，首败冻结可回退）。

**升级路径**：无——这是手册里最"自助"的一簇，值班提提案即可。

---

## 簇 7 · 引擎 × 模型不匹配

**现象**：`architecture not supported`；`quantization kernel not supported`；引擎启动但输出乱码/空转。

**只读检测**：
1. `cat /data/models/<name>/config.json`：`architectures` + `quantization_config` 字段。
2. 对照机型档案（智算大屏徽标 readiness）与模型档案（[[netdev.model_cards]] 校验行——H 系=FP8 原生、910B=W8A8、P800=W8A8C16、A 系/4090=BF16+AWQ；跨族量化档无实勘依据会出建议性告警）。
3. `python3 --version` + venv 内 `pip list | grep vllm`：引擎版本 × 模型架构支持矩阵。

**修复分类**：升级引擎 / 换模型产物（变更）；量化档跨族（回到部署校验，重新配对）。

**升级路径**：引擎生态问题转平台组；本台职责是**阻止未验证组合静默上线**，不替引擎厂商背书。

---

## 簇 8 · 假 hang（存储 I/O 瓶颈）

**现象**：加载 10+ 分钟疑似挂死（大屏利用率 0%、显存缓慢增长——其实在慢慢读盘）。

**只读检测**：
1. `iostat -x 2 3`：`%util` 打满 + 极低读吞吐 = 存储瓶颈实锤。
2. 对照实验：同权重 `--load-format dummy`（不读权重）秒级起 → 排除引擎/网络，锁定存储。
3. `df -h <权重路径>`：NFS 挂载 vs 本地盘（假 hang 大户是权重放 NFS）。

**修复分类**：权重挪本地盘（变更，重排目录）；存储侧扩容/换协议（升级存储值班）。

**升级路径**：确认存储瓶颈后转存储值班；值班不要在 hang 疑似中反复 kill 重启（每次重启都从头读盘，越 kill 越慢）。

---

## 值班动作纪律（全簇通用）

1. **先大屏后 CLI**：智算大屏/健康面板是第一现场；CLI 只做复核取证据。
2. **只读在线、变更离场**：检测命令全在读表；修复动作走提案/割接 runbook，急停（E-STOP）后按大屏提示回决策点处理，不绕过 hold。
3. **证据入档**：每一步检测输出随 Finding/审计留存（hash 链审计是合规承重产物，别嫌密）。
4. **升级要带四件套**：现象截图、检测命令输出、已排除项、影响面（哪些卡/哪些模型/哪些用户）。
