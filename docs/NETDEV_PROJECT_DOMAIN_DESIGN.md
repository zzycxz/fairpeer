# 项目安全域与跨实例协同：开工设计（K1+K2）

- 日期：2026-09-14（0.2.5 批⑤/⑦开工准入件）
- 地位：GAPS 台账批次 K 的两件设计成文——K1（G-P1 三件设计）、K2（G-L1 通道选型）。
  K1 三件中的"F3 阈值映射表"已在批④交付（`docs/NETDEV_INFER_METRICS.md`）。
- 读者：G-P1/G-L1 开工前必读；J4/J5 裁决人。
- 代码事实基线（PROJECT_SCENARIO_SPEC §八 V1-V3）：项目=纯前端视图分组；
  guardrails 全局单份；提案无 project 字段；confirm2=无身份布尔位；
  AnchorAudit=本域锚；linkpeersignal=配对信令（无数据面）。

---

## 一、K1-① 会话项目上下文载体（G-P1 管道设计）

**问题**：activeProject 是前端 localStorage 态，后端零项目上下文——guardrail/提案/发现全部无法感知"当前在哪个项目里干活"。

**设计：per-session 后端态，一次写入两处。**

1. **载体**：Manager 已有的会话上下文（TurnBegin 侧）挂 `project string`。
   新 binding `NetDevSetSessionProject(sessionID, project)`——前端项目切换器
   在写本地 store 的**同一动作**里调用；后端会话态成为唯一权威运行时事实，
   前端 store 降为 UI 缓存。
2. **接线三点**：
   - guardrailCheck：读会话 project → 合并项目级 allow/deny（项目只许收紧
     于全局，WRITE_AUTHZ §4.1 同款单向纪律）；
   - 提案：草稿创建时盖 `project` 字段（不可事后改）——ExecuteProposal 对
     跨项目目标步骤拒执行（审计 refusal），这是"域"从视图变安全语义的关键；
   - 发现/巡检：目标集按会话 project 的 groups 过滤（项目外设备按 J4 裁决
     处理，见 §三）。
3. **不做**：per-tab 多项目并行（一期单会话单项目——值班切换是串行动作，
   并行域引入的跨域泄漏面远大于收益）；项目级凭据隔离（凭据在 secret store，
   本就不分项目）。

**验收**：切换项目后发起的写请求命中目标项目外设备 → 拒绝 + 审计 refusal
含项目名；提案携带创建时会话的 project 字段且不可变。

## 二、K1-② confirmers 身份模型

**问题**：`ApproveProposal(id, confirm2 bool)` 是无身份布尔位——confirmers
名单没有可校验的对象。

**设计：自报名为基线，trustdomain 开启时自动升格签名验证（J5 折中）。**

1. `ApproveProposal(id, confirm2 bool, operator string)`：operator=批准人
   自报名（桌面无用户体系，自报是诚实下限）；审计记 operator。
2. trustdomain 已启用 → 上面的 operator 必须携带该成员密钥签名（请求体
   签名或 challenge-response，G-L1 的 peer 身份验证同机制复用）；名单匹配
   签名身份，自报不可绕过。
3. confirmers 名单语义不变：空 = 任意人工（现状）；非空 = 名单内才有效。
4. 跨实例四眼（G-L1）：对端确认卡携带 peer 签名身份——两把锁"一人本机、
   一人远端"自然成立。

## 三、K1-③ NetDevProject schema 迁移 + J4/J5 裁决框

**迁移（D1 软降级口径）**：新增字段 type/allow/deny/policy/confirmers 全部
缺省安全值（type=generic、allow/deny 空=passthrough、policy 继承全局、
confirmers 空=任意人工）；旧 config 加载通过 + `NetDevWarnings` 提示未配置
项目域。无迁移脚本——字段级缺省即迁移。

**J4 项目外设备可见性（推荐：只读+拒操作）**
- A 只读+拒操作：视图全量保留、操作越域拒绝+审计。✔ 值班需要跨域可见性
  （故障不分项目）；拒绝动作本身是审计信号；实现=guardrail/提案层过滤，
  视图层零改动。
- B 完全隐藏：视图也滤。✘ 巡检/分诊口径断裂；"看不见的故障"是运维反模式。
- **推荐 A**。

**J5 confirmers 绑身份（推荐：折中）**
- A 绑 trustdomain 签名：强，但强制 trustdomain 前置（未开域的站点四眼
  降级为自报）。
- B 纯自报：零依赖，但名单可被冒名。
- **推荐折中**：自报基线 + trustdomain 存在时自动升格（见 §二.2）——
  两档都成立，站点按自身基建选。

## 四、K2 G-L1 通道选型（relay vs P2P）

**问题**：四眼跨实例确认需要新数据通道（V3 修正：linkpeersignal 只有配对
信令，无消息路由）。对方 Ed25519 公钥配对时已在手。

| | 方案 A：signal 服务加 relay 端点 | 方案 B：配对后 P2P 直连 |
|---|---|---|
| 路径 | 确认请求经对端 linkpeer 服务中转（/pair/relay 消息路由，端到端加密，relay 不解密） | 双方局域网直连（公钥在手，NaCl 加密） |
| 跨网段（FDE 现场 ↔ 总部，主场景） | ✔ 天然支持 | ✘ 防火墙/NAT 常不可达 |
| 可用性 | relay 在线依赖（单点） | 无中转单点 |
| 消息语义 | 离线暂存友好（确认类消息小、对延迟不敏感） | 需自行处理不可达重试 |
| 实现面 | linkpeersignal 加一个消息类型 + 暂存队列 | 可达性探测 + 连接管理 + 加密层 |

**推荐：A relay 为主，B 作为同网段长优化（后置）**——四眼的主场景恰是
跨网段（现场起草、远端确认），relay 天然覆盖而 P2P 恰好覆盖不了；确认
消息小且可暂存，relay 的可用性单点可用"消息重试 + 到达回执"补偿；
端到端加密使 relay 不成为信任单点（中转只见到密文）。

**原型验证步骤（半天+）**：①配对双方经 relay 互发签名心跳（公钥已在手，
验证签名链）；②确认请求消息体（提案摘要 hash + operator 签名）往返 +
对端确认回执；③relay 宕机时的暂存补投语义验证。③项通过后 G-L1 开工。

**L2 审计互锚**：同用该通道（锚记录=另一种消息类型），选型结论直接复用。

---

## 五、遗留决策（需要拍板才能进实现）

| # | 决策 | 推荐 | 影响 |
|---|---|---|---|
| J4 | 项目外设备可见性 | 只读+拒操作（A） | G-P1 视图层工作量 |
| J5 | confirmers 身份强度 | 折中：自报+trustdomain 升格 | G-P1 审批链实现 |
| K2 落地 | relay vs P2P | relay 为主 | G-L1/G-L2/G-L4 全部数据通道 |
