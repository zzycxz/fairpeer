# 智算设备纳管 TODO（申报书对齐版）

> 背景：作品申报书已声明"智算纳管"能力（三条当前路径 + 后续原生 GPU 数据面）。
> 本清单的目标：把申报书里写的话变成真的，并留齐演示/证明材料。
> 参考：`NETDEV_SPEC_V2.md` §8.4（GPU 数据面·停车场）、§2.1（kind 判别式）、§1.4（设计不变量：新数据面默认只读，先映射分类器四类再放行）。

---

## P0 —— 让"当前能力"三条成立（申报书演示所需，优先完成）

### P0-1 GPU 服务器主机模式纳管 + 读表授权
- [ ] 准备一台真实 GPU 服务器（NVIDIA 或昇腾），SSH 可达
- [ ] 以 kind=linux 录入 `[[netdev.devices]]`（省略 kind 时按 vendor 推断）
- [ ] 对话式触发 `nvidia-smi` → 预期被只读分类器首次拒绝（这是设计行为，演示时是亮点不是事故）
- [ ] 设备卡「允许此命令」一键写入 `[netdev.extra_read]` 教读表
- [ ] 再跑对话式巡检：`nvidia-smi`、`nvidia-smi -q`（ECC/温度/MIG）出结果并解读
- [ ] 昇腾路径：`npu-smi info` 走同一教读表机制
- [ ] 产出：演示录屏/截图 1 套（归入申报材料）
- 验收：一次对话完成"查看卡状态 → 异常解读"，全程留审计记录

### P0-2 智算集群 K8s 只读纳管（有条件则做）
- [ ] 拿到智算集群 kubeconfig，凭证入密钥库（SecretKindKubeconfig，TOML 只存引用名）
- [ ] kind=k8s 设备录入，验证节点 / GPU pod / 资源水位只读查询
- [ ] 产出：截图 1 套
- 代码参考：`internal/netdev/kubeapi.go`、`internal/netdev/apitools.go`

### P0-3 浏览器 skill：智算管理平台巡检
- [ ] 选定智算管理平台 Web 控制台（内网可达）
- [ ] 参照态势感知巡检 skill 的制作流程，录一个"智算平台巡检"技能
- [ ] 验证链路：一键巡检 → 结果页卡 →（可选）Excel/PPT 报告
- [ ] 产出：配置过程 + 运行录屏 1 套

---

## P1 —— 原生 gpu-host 数据面（独立立项，规格 §8.4）

前置门槛（规格启动条件）：自有 GPU 集群 dogfooding 产出真实痛点清单——先跑 P0 两周再定范围。

### P1-1 最小可用面：DCGM XID → Finding
- [ ] 痛点清单：基于 P0-1/P0-2 dogfooding 记录高频诉求
- [ ] kind=gpu-host 判别式落地（`internal/config/netdev.go`，沿用 §2.1 模式）
- [ ] 采集面：`nvidia-smi -q` / DCGM（XID / ECC / NVLink / MIG 字段）
- [ ] XID 错误解析 → `netdev_finding`（source=gpu，必带命令输出证据）
- [ ] 只读密封：新命令全部映射分类器四类（read/write/dangerous/unknown），写路径不存在
- 验收：注入/回放一次 XID 错误，自动落 Finding 并推送通知

### P1-2 后续菜单（按痛点优先级排队，不承诺全部）
- [ ] Fabric 体检 preset：`ibstat` / `nccl-tests` 只读预设
- [ ] 训练日志按 step 对齐（logsource 扩展）
- [ ] Checkpoint 治理（保留策略 / 完整性 / 跨节点取回，复用 SFTP 通道）
- [ ] 推理服务面：vLLM / Triton 指标
- [ ] 节点横向对比（rank 速度 / 温度 / ECC）

---

## P2 —— 算力底座（模型接入，"自主可控"叙事配套）
- [ ] `fairpeer.toml` 增加集团/省内智算推理服务 provider（OpenAI 兼容端点）
- [ ] 验证运维 profile 下模型调用走自有算力
- 备注：演示可选，答辩讲到"模型底座跑在移动自有智算上"时需要这张图

---

## 演示材料归档
- [ ] 全部截图/录屏统一归档（建议 `docs/assets/` 或申报材料目录），文件名含场景号（如 `zhisuan-p0-1-巡检.mp4`）
