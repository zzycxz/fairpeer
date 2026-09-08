# 蓝队漏洞排查工具能力提升 Spec（BLUETEAM_SKILL_SPEC v1）

> 日期：2026-09-06。
> 输入：对 GitHub 开源渗透/蓝队生态的系统性排查——**125 库次（去重约 120 个）、
> 38 轮检索**，五个切片并行（AI-agent skills 集合 / 后渗透与横向 / 网络发现与侦察 /
> 提权与主机清单 / 蓝队 IR 与攻防模拟），完整筛查表见附录 A。
> 关联文档：NETDEV_SEGMENT_DISCOVERY_SPEC.md（网段发现 v1，本文 §4 升级为 v1.1）、
> REDTEAM_BATCH_DECISION.md（红线，本文全部设计在红线内）、SKILL_ARCHITECTURE_SPEC.md
> （技能体系层，本文不重复其结论）。

## 0. 结论摘要

1. **方法论被外部验证**：我们"读表优先、假设驱动、微采样验证"的纪律在开源生态里有
   工业级先例——Netdisco（读 SNMP/CDP/ARP/FDB 建拓扑，零 ping sweep）、BloodHound/
   SharpHound（读 LDAP 目录替代扫网段）、fscan（从本机 IP 推 C 段、两级收敛）、
   linpeas 默认档全本地。方向不动，差距在**工程化程度**。
2. **两个真实场景的能力缺口**：原 netdev-vulnscan 只覆盖"在管设备清单"这一种形态；
   缺（a）**拿到一台主机权限后的分层纵深排查**（靶场分工=人工打点、产品管侦察，
   见 REDTEAM_BATCH_DECISION 背景）；缺（b）**只给一个入口 IP 时的快速网段收敛**
   （避免 A 类私网盲扫）。两者已落为 `netdev-seccheck-auto` 的主机/网段两个入口
   形态（ORCHESTRATION 收敛后；0.2.2 落地，§3/§4 是其内容设计）。
3. **skill 写法有一套业界收敛的解剖结构**（yaklang/hack-skills、Anthropic-Cybersecurity-
   Skills、atomic-red-team、Sigma、Velociraptor artifacts 五方互证）：触发式 description →
   When to Use/Not → Prerequisites → 分步 Workflow（每步=命令+期望输出+失败分支）→
   Verification 关卡 → Output 契约 → Guardrails → 升级路由。本文 §5 定版为我们的
   skill 写法规范；两个 -auto body 已按此骨架断言式重写并过预算 lint（0.2.2）。
4. **知识外置**是头部项目的共同选择（nuclei 模板、GTFOBins YAML、SecLists 字典、
   linpeas↔HackTricks 分离）：把网段先验、凭据存放点清单、指纹→职能→动作映射做成
   数据文件，SKILL.md 只写引擎逻辑——知识可独立更新、可被审计、可社区化。
   （0.2.2 已落地：segment-priors / credential-spots / host-risk-checks 三表 +
   `netdev_knowledge` 加载通道，哈希入审计链。注意第三张落的是 host-risk-checks
   ——H2 本机风险的支撑表；本文原规划的"指纹→职能→动作"映射表未落，转批 3。）
5. 红线一律不变：不做利用、不做爆破、主动探测留在评估信封后、scopes 永不可关。

## 1. 研究方法与筛选结果

五路并行调研，每路 6-11 轮 web 检索 + GitHub API/README 精读核实路径与星数：

| 切片 | 排查库数 | 重点借鉴 | 关键收获 |
|---|---|---|---|
| AI-agent skills 集合 | 26 | yaklang/hack-skills、mukul975/Anthropic-Cybersecurity-Skills、trailofbits/skills | skill 解剖结构、三层路由、渐进披露控 token |
| 后渗透/横向 | 21 | GhostPack/Seatbelt、SpecterOps/BloodHound、AlessandroZ/LaZagne、nicocha30/ligolo-ng | 拿下主机后的第一小时零发包顺序、L0-L4 层推进 |
| 网络发现/侦察 | 28 | netdisco/netdisco、shadow1ng/fscan、nmap、masscan、projectdiscovery/nuclei | 读表建拓扑先例、大网段工程手段、模板外置 |
| 提权/主机清单 | 28 | PEASS-ng、HackTricks、GTFOBins、LOLBAS、linux-smart-enumeration | 清单条目结构、结果驱动升档、攻防同条目 |
| 蓝队 IR/攻防模拟 | 24 | atomic-red-team、SigmaHQ/sigma、Velociraptor artifacts、CERT SocGen IRM、ThreatHunter-Playbook、Bypass007/Emergency-Response-Notes | 能力/动作/检验/调查四类单元范式 |

（判断为"重点借鉴/借鉴/一般/索引/跳过"的完整逐库表见附录 A。）

## 2. 八条设计原则（从 120 个库中提炼）

每条注明来源库与在本平台的落点。

**P1 读优先，"目录/表项即地图"替代"扫描即发现"。**
来源：SharpHound（LDAP 拉全目录、零网段扫描）、Netdisco（SNMP 读 ipRouteTable+CDP/
LLDP+ARP+FDB 输出全网拓扑）、Certipy（纯目录读枚举 ADCS 误配）、osquery（把 OS 当
关系库查询）。落点：两个新技能卡的第一步一律是"读"——读平台存量、读在管设备表项、
读入口主机本机状态；发包只出现在验证关卡之后。

**P2 分层推进，扩半径由证据触发。**
来源：后渗透框架收敛顺序（身份上下文→网络位置→凭据存放点→本机提权→目录读图→
定点移动）；ligolo-ng/chisel 只对"已证实的具体目标"打洞；NetExec 单目标认证回显成功
才扩半径。落点：§3 的 H0-H5 层模型 + 每层"入场证据"前置条件；§4 的证据阶梯。

**P3 知识外置，引擎与数据分离。**
来源：nuclei（模板=数据，引擎通用，社区独立贡献）、GTFOBins（YAML 条目+CI 校验）、
SecLists/onesixtyone（工具只吃字典文件路径）、linpeas↔HackTricks（工具与判定标准
文档分离）。落点：§5.2 三张外置表；skill 正文不再堆知识，只写调度与判定逻辑。

**P4 分支决策树，不做线性脚本。**
来源：HackTricks 条目前提门控（"If you can X"开头，命中才进入）、LSE 渐进披露
（默认档无果→升 -l1→升 -l2，结果驱动）、linpeas 噪声预算档位（默认/-a 深/-s 隐身）、
命令内 `||` fallback 单行编码环境差异。落点：§5.4 环境分支表达；两个新技能卡按
"观察→判定→行动→加固"四段式写条目。

**P5 每步带验证关卡：期望输出+失败分支。**
来源：yaklang/hack-skills 每步自带命令+期望输出；Anthropic-Cybersecurity-Skills 正文
固定四段（When to Use/Prerequisites/Workflow/Verification）；Atomic test 的
"预期结果写在 description 里"；Sigma 的 selections+condition+falsepositives。
落点：§5.1 骨架把 Verification 定为必备段；§4 假设-验证表升级为 Sigma 式三元组
（判据/误报回退/等级）。

**P6 攻防同条目：每条"看见 X 试 Y"同时写"如何确认 Y 被利用/未利用"。**
来源：LOLBAS 条目同时带攻击配方+Sigma 检测+IOC；Seatbelt 官方定位 offensive+
defensive 双视角。落点：蓝队排查卡的每条检查项都带"阳性判据+阴性说明（误报回退）"，
排查动作与加固建议（netdev_finding 的 fix 字段）成对出现。

**P7 三层路由+渐进披露，控 token。**
来源：yaklang 三层加载（28 行纯路由入口→6 个 category→101 个深 playbook，loader
只见入口）；Anthropic-Cybersecurity-Skills 的 frontmatter ~30 token 可全库扫描、正文
500-2000 token 按需加载。落点：§5.3 我们的蓝队技能树定为一个入口卡+三个场景
playbook+OS/协议深卡；入口卡 ≤300 token。

**P8 输出即契约：结构化 findings+框架映射。**
来源：Claude-BugHunter（"最终产物=可提交报告"内化为输出契约）；Anthropic-Cybersecurity-
Skills 的 frontmatter 携带 MITRE ATT&CK/D3FEND 映射（805/817 个 skill 已映射），
供平台侧审计报表。落点：netdev_finding 已是输出契约（severity=影响×暴露、fix 结构化、
source 分流视图）——保留；新增可选：技能卡 frontmatter 加 `mitre_attack` 字段，
蓝队核查视图可按战术折叠展示。

## 3. 场景一：拿到一台主机 → 分层纵深排查（seccheck-auto 主机入口形态，H0-H5）

### 3.1 现状缺口

靶场与评估的真实分工是"人工打点、产品管侦察与修复"（REDTEAM_BATCH_DECISION），
但现有 netdev-vulnscan 的入口是 netdev_devices 在管清单——它回答"这批设备有什么
漏洞"，不回答"从这台已拿到的主机出发，能确认多深的失陷面"。真实环境多样（有无域、
有无纳管、OS 各异），要求技能是决策树不是流水线。

### 3.2 层模型 H0-H5（每层=入场证据+最小动作+验证关卡+产出）

第一小时的顺序对外部收敛结论（Seatbelt 网络组全本地读、LaZagne 凭据存放点清单、
SharpUp 零横向本地检查）做蓝队化改写——**全部动作在只读密封内**：

| 层 | 名称 | 入场证据（无则不开层） | 最小动作（全零发包） | 验证关卡 | 产出 |
|---|---|---|---|---|---|
| H0 | 本机身份与网络位置 | 一条可执行通道 | whoami /all（或 id）、ip addr|ipconfig /all（DNS=DC 线索）、ip route|route print、arp -n、ss -tlnp|netstat -ano、hostname/resolv.conf | 命令有输出且互相印证（如网关同时出现在 route 与 arp） | 身份卡：OS/补丁水平/所处段/网关/邻居初表 |
| H1 | 凭据暴露面巡检 | H0 完成 | 按 LaZagne 类目盘点存放点：WiFi 明文、GPP、PS 历史、浏览器保存、SSH key、云凭据文件、缓存凭据（LSASS 可达性只判不取） | 每个存放点=找到文件/键即阳性，找不到=阴性记录（不是失败） | 凭据暴露清单（finding：credential 类，fix=credential/procedure） |
| H2 | 本机风险检查 | H0 完成 | linpeas/Seatbelt 分组检查卡蓝队化：SUID/服务路径/计划任务/补丁水平（uname、Get-HotFix）→ 只列风险不利用 | 逐项"阳性判据+误报回退"（P6），启发式项标注"绿≠安全" | 本机风险 findings（fix=upgrade/config） |
| H3 | 邻接层 | H0 的 arp/netstat 出现邻居 | 邻居 IP×开放端口与 netdev_devices 清单/待确认区对账；在管邻居直接读表；已知凭据复用只做"一次认证回显"级验证（信封内） | 对账命中数≥1 或认证回显成功才继续；0 命中且无凭据→停在 H3，报告即止 | 邻接可达图（mermaid，对齐运维模式的路径图纪律） |
| H4 | 域/目录层 | H0 的 DNS 指向 DC 或 H1 拿到任一域凭据 | 目录即地图：一次 LDAP/SNMP 级读拿全图（域内：目录查询；纯网络侧：netdev_topology 的 CDP/LLDP+路由表），取关键路径不漫游 | 图生成成功且关键节点（DC/运维跳板/认证服务）可定位 | 纵深地图：主机→邻居→域/核心的分层图 |
| H5 | 跨段 | H4 图中出现段外具体目标 | 跨段探测只在评估信封内编排（netprobe 网络位置模式），目标=图中的具体地址，绝不展开成段扫 | 与 §4 同一闸门（≥2 活/用户确认） | 跨段路径与目标清单 |

**深度计**：技能收尾必报"本次推进到 H几、各层证据计数、未开层与原因"——
"排查尽量多的层数"被量化为每层的开/停判定，而不是主观描述。

### 3.3 接入形态与工具缺口

- 入口主机已纳管 → 全程 netdev_exec/netdev_snmp（现有工具面够）。
- 入口主机未纳管（靶场常态）→ 产出"人工执行卡"（按 H0-H2 分组的命令清单），
  用户回贴输出后由 agent 关联分析、立案——netdev_finding 的 evidence 字段已能承载
  （批 3 可加一个轻量"排查卡回填"动作降低摩擦，见 §6）。
- H1/H2 的检查项清单外置为数据表（§5.2），OS 分支按 §5.4 表达。

## 4. 场景二：入口 IP → 网段快速收敛（SEGMENT_DISCOVERY_SPEC v1 → v1.1）

v1 的方法论全部保留，本节吸收外部证据后定版，并记两处修订。

### 4.1 证据阶梯（定版）

```
                        ┌────────────────────────┐
                        │    输入：一个入口 IP     │
                        └───────────┬────────────┘
                                    ▼
 ═══════ 证据阶梯：按包数成本逐级上升，不跳级；每一级内部跑同一个循环 ═══════

  L0 平台存量查询        0 包 · 0 凭证 · 毫秒级
  │   同段设备地址(netdev_devices) · topology 邻接 · 存量配置(DHCP池/ACL/静态路由)
  │   └→ 兼职"可用性预言机"：顺手回答 L1/L2 到底有没有得用
  ▼
  L1 入口主机本体        0 包 · 1 次执行（若已持访问权）
  │   ip addr→掩码(假设升为事实!) · ip route→静态路由(隐藏核心段) · arp -n→现成邻居
  │   v6 快路：ping ff02::1 一包全段应答（v6 别套 v4 流程）
  ▼
  L2 在管设备读表        0 包 · 只读密封（display/show 前缀，免评估信封）
  │   顺序是硬的：路由表(直连段全集=整站地图) → 目标段ARP(活体台账) → MAC(端口规模)
  │   ※ ARP 只有直连段才完整；非直连段 ARP 稀疏 ≠ 空段
  ▼
  L3 定点指纹            个位数包 · 只打已有证据指向的地址
  │   网关候选 ping 读 TTL：64=Linux直连 · 128=Windows · 127=有NAT/L3 · 63=隔一跳
  │   banner/证书SAN→主机名→命名规律 · PTR 批查 · traceroute 定上游
  ▼
  L4 微采样              十几个包 · 抽 4-6 点
  │   点位按信息量排：.1 → 基础设施区抽一(.2-.19) → .100 → .254 → 随机
  │   ※ 密度探针而非活性检查：命中的"分布形状"直接读出段角色
  ▼
  ╔═ 验证闸门 ═════════════════════════════════════════════════╗
  ║ ≥2 活 = 通过，段入地图                                      ║
  ║  0 活 = 标"证据不足"即止，绝不重扫                           ║
  ║ 下探任何新段 = 必须有一行"通过"记录 + 证据链交用户确认        ║
  ╚════════════════════════════════════════════════════════════╝
  ▼
  L5 已验证段内全扫      ≤256 主机 · 评估信封 + scopes + 用户确认
  │   netprobe / nmap 只准吃本流程产出的"已验证段"，不吃裸 CIDR
  ✗  永不到达：10/8 类无界扫 —— 1677 万地址不是"慢"，是方向错误
```

循环（每级内部原样运转）：读信息→立假设→最小验证→记入段地图草稿；不通过回信息面。
记录/下探分离：新段"入账"免费，"发包"过闸门。出口：段地图（段×证据源×置信度×角色），
角色即核查队列序：边界 > DMZ > 生产（稀疏固定 IP）> 基础设施（.2-.19）> 办公（DHCP 池，跳过）。

### 4.2 外部证据带来的确认与修订

- **确认（不动）**：读表优先有工业先例（Netdisco：SNMP ipRouteTable+CDP/LLDP+ARP+FDB
  建拓扑，零 ping sweep——v1 的"读表一条命令"路线就是蓝队版 Netdisco）；两级收敛是
  工具界惯例（naabu→nmap、fscan 先探活再服务扫描），v1 的"微采样后才放大"与之同构；
  netdiscover fast 模式只采 .1/.254 类网关地址=我们的"网关先验"有先例。
- **修订一（吸收 fscan）**：L1 增加"从本机 IP 推 C 段"为缺省行为——入口 IP 的
  /24（掩码未知时）自动成为第一个候选段并进入 L4 采样，不再要求显式推理步骤。
- **修订二（吸收 nuclei/模板外置）**：网关先验（.1/.254/.249 次序）、采样方案
  （点位选择器/探针/≥2 活阈值/放大动作）、段职能指纹→动作映射（445+88=域段、
  大量打印机 OUI=办公段、SNMP community 命中=可读表深挖）全部外置为数据表（§5.2），
  skill 正文只写阶梯调度逻辑。调研确认"网段边界抽点+N 点置信度判定"没有现成开源
  实现——这是本流程的差异化点，数据表化之后可独立演进。

### 4.3 工具咬合（0.2.2 更新：测绘三合一）

L2 走 netdev_exec 只读密封（display/show=read 已验证）；L3/L4/L5 的发包统一走
`netdev_probe(cidr, depth, mode)`（discover/nmap/netprobe 的收敛面：depth=L3 定点
指纹 / L4 微采样 / L5 已验证段全扫，mode=auto 自动回退引擎；scopes 预检硬拒——
出界零发包）；L5 输入只准是"已验证段"；
段地图落 netdev_finding，蓝队核查视图按角色排序消费（对齐 MITRE T1018→T1046 链序：
先列主机、后扫服务）。

## 5. Skill 写法规范（定版）

综合 yaklang/hack-skills（结构工程最佳）、Anthropic-Cybersecurity-Skills（蓝队映射
最全）、atomic-red-team（动作单元）、Sigma（检验单元）、Velociraptor（能力单元）、
IRM/Emergency-Response-Notes（手册范式）五方互证。

### 5.1 必备骨架（八段，顺序固定）

```markdown
---
name: netdev-xxx            # kebab-case
description: >              # 触发式：Use when… 枚举场景词（决定被选中的命中率）
  蓝队网段收敛（运维）: 拿到一个入口 IP 需要快速画出所在段与邻接段时使用。
  触发词: 网段、探测、这个 IP 接在哪、内网摸底、子网。零发包优先，发包过评估信封。
mitre_attack: [T1018, T1046]   # 可选，蓝队核查视图按战术折叠
---
# 标题
## When to Use / When NOT   # 触发与让位（如: 单台已知设备故障→netdev-playbook）
## Prerequisites            # 工具+权限+信封要求，逐条可检查（"评估信封未开→只输出建议"）
## Workflow                 # 编号步骤；每步 = 动作(命令模板) + 期望输出样例 + 失败分支
## Verification             # 检验关卡：判据(阳性) + 误报回退(阴性≠失败) + 等级/升档条件
## Output                   # 输出契约：落哪些 netdev_finding、source 取值、深度计/完成度报告
## Guardrails               # 红线（不利用/不爆破/信封不绕/拒绝不重试）
## Escalate                 # 升级路由：命中什么信号 → 转哪个技能/交用户决策
```

对照现状：netdev-vulnscan 已有纪律/红线/闭环（相当于 Guardrails/Output 就位），
缺 When-NOT、每步期望输出样例、显式 Verification 段——按 §5.5 映射补齐。

### 5.2 知识外置（0.2.2 落地：机制全落 + 三表，第四张转批 3）

知识=数据文件（YAML，`internal/netdev/knowledge/` embed→释放→`user-knowledge/`
同 id 覆盖，升级不冲掉用户改动；schema 校验入 CI；`netdev_knowledge` 工具加载，
内容哈希入审计链——机制按 ORCHESTRATION §3.5-D 全部落地）。表状态：

1. **网段先验表 ✅**（segment-priors.yaml，服务场景二）：网关候选次序、采样点位
   选择器（sample_points）、min_alive 阈值、TTL 解读。
2. **凭据存放点巡检表 ✅**（credential-spots.yaml，服务场景一 H1）：类目 × OS ×
   检查点（路径/键/命令模板）× 阳性判据——LaZagne 类目清单的蓝队化。
3. **主机风险检查表 ✅**（host-risk-checks.yaml，服务场景一 H2）：linpeas/Seatbelt
   分组检查的蓝队化条目（阳性判据+误报回退成对，P6）。
4. **指纹→段职能→动作映射表 ⬜（批 3）**：445+88→域段→优先核查队列；打印机 OUI
   密集→办公段→跳过；SNMP community 命中→可读表→转 L2 深挖。

外置的好处即 nuclei/GTFOBins 已验证的：知识独立更新、可审计、可测试（YAML 可
schema 校验）、未来可开放用户自扩。

### 5.3 三层路由与 token 预算

> **修订（2026-09-07）**：`NETDEV_SKILL_ORCHESTRATION_SPEC.md` v1 已立项——
> 入口卡 `netdev-blue` **不再单独设立**（常驻路由由主循环 addon 路由表承担，
> 场景导航并入 `netdev-help`）；`netdev-vulnscan` 改造并更名为
> `netdev-seccheck-auto`（编排子代理，吸收 netdev-audit-project）；
> **foothold 与 segment-discover 亦不再单独立卡**——作为 seccheck-auto 的
> 两个入口形态落地（主机纵深 H0-H5 / 网段收敛 L0-L5，阶梯表外置知识数据，
> 信封闸门交互留主循环）。
> 本节树形图按该 spec §4 终态理解。

```
netdev-seccheck-auto（编排子代理；常驻路由=主循环 addon 路由表 + netdev-help）
 ├─ 入口=清单：单机闭环核查（←vulnscan+audit-project）
 ├─ 入口=主机：分层纵深 H0-H5（§3）
 └─ 入口=网段：证据阶梯 L0-L5（§4；L3-L5 走 netdev_probe depth 分档）
      └─ 深卡按需（linux/windows 专卡、域环境专卡——各自 ≤2000 token）
```

加载纪律对齐 yaklang：loader 常驻只有入口卡；playbook 由路由选中后整卡进上下文；
深卡只在 Workflow 显式路由时加载。

### 5.4 环境分支的表达（真实环境多样性）

- **前提门控句式**：每个条目以"若你已有 X"开头（HackTricks 惯例），不满足→跳过并
  标 N/A，不算失败。
- **OS 决策树小节**：H0/H1/H2 的命令按 linux/windows/网络设备 三列并排，先判 OS
  再走列（对齐现有 vulnscan 的三列写法）。
- **单行 fallback**：兼容性差异写成 `cmd_a || cmd_b`（LSE/linpeas 惯例）——但注意
  平台约束：netdev_exec 单条命令无管道，fallback 落在"期望输出样例"的变体行，不落
  命令行。
- **结果驱动升档**：默认档（零发包）无果→按档位表升档（每档标注噪声/信封要求），
  禁止跳档（LSE 惯例）。

### 5.5 现有技能改造映射

> 修订（2026-09-07，随 ORCHESTRATION v1）：vulnscan 并入 seccheck-auto、
> playbook/diag 三卡并入 diag-auto——本表补齐项随合并 body 落地（ORCHESTRATION
> P1"按 §5.1 八段骨架断言式重写"），不再单独改旧卡。

| 技能（终态名） | 已就位 | 补齐（对应骨架段） |
|---|---|---|
| netdev-seccheck-auto（←vulnscan+audit-project） | Guardrails/Output/单机闭环 | When-NOT；第 1-4 步各加期望输出样例+失败分支；显式 Verification 段（候选→只读验证的判据表已有，提为独立段）；frontmatter 加 mitre_attack（✅ 以上已随 0.2.2 断言式重写落地）；扩段确认落委托侧（✅ 2026-09-08 addon 行已补） |
| netdev-security-assessment（用户文件技能） | 阶段化裁剪 | 阶段间加 Verification 关卡（现按顺序推进，改为"上阶段通过记录才开下阶段"）；测绘阶段输入收窄为段地图产出；入口语义随 seccheck-auto 终态对齐 |
| netdev-diag-auto（←playbook+diag 三卡） | 读序矩阵、证据落 finding | 不动内容——合并时按 ORCHESTRATION §3.2 搬节，形态即本表的样板 |
| netdev-help | 场景导航矩阵 | 加入口形态路由矩阵（在管清单/一台主机/一个裸 IP/一条告警 → 对应入口或 SecWorkbench 案例；ORCHESTRATION §3.3） |

## 6. 落地批次

> 修订（2026-09-07，按 ORCHESTRATION v1 重排）：foothold / segment-discover
> 不再单独立卡，作为 `netdev-seccheck-auto` 的两个入口形态落地（其 §4 终态）；
> 技能注册/更名/别名/合同校验在 ORCHESTRATION P1/P2，本表只列蓝队侧内容项。

**批 1（蓝队内容，随 ORCHESTRATION P1/P3）**：seccheck-auto body 三入口内容——
清单核查节（vulnscan 现有闭环按 §5.5 补齐后并入）+ 主机纵深节（§3 H0-H5+深度计）
+ 网段收敛节（§4 阶梯；L3-L5 调 `netdev_probe` 的 depth 分档）；netdev-help 入口
形态路由矩阵。扩段确认硬步骤（§8③）随网段节在案。
验收：每节过 §5.1 八段检查单；红线段逐字保留。
**状态：✅ 落地（0.2.2 最小切片 + 2026-09-08 尾差清零：addon 主机/网段委托行、
扩半径委托侧纪律、help 矩阵两处同步、守护测试 TestNetdevAddonEntryRoutingAndRadiusDiscipline）。**

**批 1.5（工作流接线，小改，§8 新增）**：segmap 段地图工件（source=`segmap` 的
finding，蓝队页卡按 source 分组渲染）；整轮案例开卷约定（每轮自动开/续 SecWorkbench
案例，轮末 新增/仍在/已修复 diff 钉入时间线）。
**状态：⬜ 未动工（代码无 segmap 痕迹、无轮次案例约定）。**

**批 2（知识外置，随 ORCHESTRATION P3-D）**：三张数据表（§5.2）入库——机制按
ORCHESTRATION §3.5-D（embedded `knowledge/` 释放 + `user-knowledge/` 同 id 覆盖 +
schema 校验 + 内容哈希入审计链），本 spec 定内容。
验收：表改动不需改 body；YAML 校验入 CI。
**状态：✅ 已落地（0.2.2：机制+segment-priors/credential-spots/host-risk-checks
三表；原规划第三张"职能映射"表转批 3，见 §5.2-4）。**

**批 3（工具缺口，按需立项）**：未纳管主机的"排查卡回填"轻量通道（用户贴输出→
结构化入 finding）；采样判读下沉为 `netdev_probe` 输出的注解字段；知识回填约定
（§8⑤：轮内确认判据先落 user-knowledge/，验证后再上游化）；指纹→段职能→动作
映射表（§5.2-4）。
验收：不引入任何写路径与主动探测能力，仅编排与解析。
**状态：⬜ 未动工。**

## 7. 红线（不变，全部批次适用）

- 不做利用性/破坏性验证（POC/EXP、爆破、溢出、畸形报文）——REDTEAM_BATCH_DECISION
  未立项前无任何例外。
- 主动探测（`netdev_probe`——discover/nmap/netprobe 的收敛面）一律信封+scopes
  后置；scopes 永不可关、预检出界零发包；拒绝不重试、不换写法。
- 凭据巡检只判"存在与暴露"，不取值、不破解、不外传；LSASS 只判可达性。
- 每条结论必须有只读证据（netdev_finding evidence），无证据不下结论；
  扩半径（新段/新层）必须有"通过"的检验记录，跨段还需用户确认。

## 8. 工作流与模式演进（2026-09-07 增补，经代码现状核实）

蓝队模式**不换骨**：密封、信封、scopes、finding 骨架与运维同构且正确，不开新
profile。要改的是工作流形态——从"一次漏洞核查工具"到"以地图为骨架、假设驱动、
分层推进的排查工作流"。五项流程改动全部对照现有代码与 ORCHESTRATION v1 终态后
裁决，按"复用现有原语优先、不新建大面"执行：

| # | 改动 | 判决 | 现状依据（已核实） | 怎么改 |
|---|---|---|---|---|
| ① 入口路由前置 | **已被 ORCHESTRATION 覆盖** | ORCHESTRATION §3.5-A：body 入口识别节置顶（≤300 字符）+ `入口=清单\|主机\|网段` 显式前缀 + help 路由矩阵 + 主循环 addon 路由行 | 无需重复设计；本 spec 批 1 只供内容（help 矩阵行、两个入口节 body） |
| ② 地图一等公民 | **轻改：工件先行，UI 缓建** | VulnScanPanel 是 findings 点列表（DISPLAY_CAP=50，按 source 过滤），无段地图工件与队列视图；但 finding.source 已是分流机制（vulnscan/cve:*） | 段地图落 **finding 工件**（source=`segmap`，title=CIDR，detail=证据源×置信度×角色×队列位次）；蓝队页卡按 source 加分组渲染即可。独立地图视图等真实排查跑过 2-3 轮再立项 | 批 1.5 |
| ③ 扩半径确认闸门 | **已落地（2026-09-08 委托侧清零）** | seccheck-auto 是一趟跑完的子代理，**无法中途问用户**——"段中途确认"结构性不可行。实际闸门三层：`netdev_probe` scopes 预检硬拒（出界零发包）+ L4 验证闸门（min_alive，body"下探新段须有通过记录"）+ **委托侧放行** | 委托侧纪律已写进主循环 addon：入口=网段 只有两种放行（用户对话已给范围＝已授权；裸 IP 则主循环先零发包收敛候选段、列给用户点头后才委托），绝不无范围甩裸 IP；禁用 seccheck 时委托行随剪（pruneSkillRoutingRows），守护测试钉住 | 批 1 ✅ |
| ④ 轮次账本 | **轻改：家已存在** | SecWorkbench 案例+时间线+IOC+CaseBundle 复盘导出已存在；vulnscan"复查注明"纪律散在 detail 首行；VulnScanPanel 已有 fairpeer:netdev-case 开案例事件 | 每轮核查自动开/续一个案例：入口形态+地图版本+队列完成度+深度计进时间线；轮末把 新增/仍在/已修复 diff 作为一条 triage 条目钉入——轮与轮之间可对照，不再从头对表 | 批 1.5 |
| ⑤ 知识反哺闭环 | **缓：管道已由 ORCHESTRATION 建好** | ORCHESTRATION §3.5-D：`knowledge/` 释放 + `user-knowledge/` 同 id 覆盖 + 内容哈希入审计链——回填管道天然存在，缺的只是约定 | 批 2 落表后加约定：轮内确认的段职能指纹/误报判据先写 user-knowledge/（升级不冲掉、哈希可溯源），验证过再上游化进内置表；此前先在轮次案例 note 人工沉淀 | 批 3 |

**裁决要点**：③④ 的"新交互"都不新建——待确认区与案例时间线就是为"人放行/
人复盘"设计的原语，蓝队流程把自己接到这两个原语上，比造新面便宜且一致。② 的
重 UI（独立地图视图）明确缓建：先用工件+分组渲染验证地图数据形态，避免为没跑过
的流程固化界面。

**不建议做的**（重申）：开蓝队专属 profile（无新工具面，纯增维护成本）；追攻击
自动化（REDTEAM_BATCH_DECISION 未立项）；全自动闭环（每轮产出是"可决策的地图"，
空段/证据不足即止，比多扫两段更专业）。

## 附录 A：库筛查总表（125 库次，判断：★=重点借鉴 / 借鉴 / 一般 / 索引 / 跳过）

### A1 AI-agent skills 集合（26）
| 库 | Star≈ | 判断 | 理由 |
|---|---|---|---|
| yaklang/hack-skills | 2.1k | ★ | 三层路由+每步验证+护栏段，skill 工程最佳 |
| mukul975/Anthropic-Cybersecurity-Skills | 32.3k | ★ | 817 skills 映射 ATT&CK/D3FEND，蓝队视角最全 |
| trailofbits/skills | 7.0k | ★ | 安全厂维护，"结论可复现+每步验证命令" |
| elementalsouls/Claude-BugHunter | 4.3k | ★ | 输出=报告契约的范本 |
| 0xSteph/pentest-ai-agents | 2.2k | ★ | 50 个 subagent 的 domain+触发词+输出物三要素 |
| BagelHole/DevOps-Security-Agent-Skills | 1.1k | ★ | DevOps/合规知识库，贴近运维蓝队 |
| frendysanusi/claude-pentest-skills | 35 | ★ | scope 强制+OWASP 方法+6 道质量门 |
| anthropics/skills | 175k | 借鉴 | 官方 frontmatter/渐进披露权威 |
| zhaoxuya520/reverse-skill | 34.8k | 借鉴 | 逆向+授权渗透热库，终端可用 |
| DaoYiSec/SecSkills | 976 | 借鉴 | 中文安全 skills 聚合 |
| Hi-FullHouse/CyberSecurity-Skills | 539 | 借鉴 | 以 PTES/NIST800-115 为骨架的方法论对齐 |
| crazyMarky/pentest-skills | 303 | 借鉴 | 知识/脚本/参考三层拆分 |
| trilwu/secskills | 135 | 借鉴 | "编码专家判断而非套壳扫描器"，防御向 |
| vxcontrol/pentagi | 22.4k | 参考 | 多 agent 编排可学，skill 写法不可学 |
| VoltAgent/awesome-claude-code-subagents | 24.9k | 参考 | security 分区角色定义可抄 |
| GreyDGL/PentestGPT | 15.2k | 参考 | 任务分解思想可借，代码偏旧 |
| aliasrobotics/CAI | 9.8k | 参考 | agent 类型学清晰 |
| 0x4m4/hexstrike-ai | 11.6k | 参考 | 工具 MCP 桥接层范本 |
| ipa-lab/hackingBuddyGPT | 1.2k | 参考 | 轻量研究框架 |
| straylabs-ai/deadend-cli | 304 | 观察 | 验证驱动 agentic CLI |
| Batman0506/openclaw-sec-skills | 325 | 一般 | 条目多深度一般 |
| ComposioHQ/awesome-claude-skills | 74.6k | 索引 | 通用清单 |
| EvanThomasLuke/Awesome-AI-Hacking-Agents | 660 | 索引 | 扩展发现用 |
| VoltAgent/awesome-agent-skills | 大 | 索引 | 跨平台注册表 |
| joe-shenouda/awesome-cyber-skills | 4.7k | 跳过 | 靶场清单非 agent skills |
| Stickman230/claude-pentest | 99 | 跳过 | 强绑 Kali MCP，轻知识 |

### A2 后渗透/横向（21）
| 库 | Star≈ | 判断 | 理由 |
|---|---|---|---|
| GhostPack/Seatbelt | 4.7k | ★ | 攻防双视角主机体检，分组命令=现成分层检查单 |
| AlessandroZ/LaZagne | 11k | ★ | 凭据存放点类目清单→蓝队暴露面巡检表 |
| SpecterOps/BloodHound | 3.4k | ★ | "读目录而非扫网段"教科书，图=纵深地图 |
| nicocha30/ligolo-ng | ~5k | ★ | 跨段层标准做法，半径可控（只回连一个出口） |
| fortra/impacket | 16.1k | 借鉴 | 凭据→单点执行的最小流量模型 |
| Pennyw0rth/NetExec | 5.8k | 借鉴 | 凭据单目标验证后扩半径的纪律 |
| SpecterOps/SharpHound | ~2k | 借鉴 | LDAP+SAMR 采集，零网段扫描 |
| GhostPack/Rubeus | ~4.2k | 借鉴 | Kerberos 证据链验证器 |
| GhostPack/SharpUp | ~1.3k | 借鉴 | 零横向流量的本机检查 |
| gentilkiwi/mimikatz | 21.8k | 借鉴 | 凭据存放点清单定义者（蓝队只判不取） |
| jpillora/chisel | 16.5k | 借鉴 | 按"已证实目标"打洞 |
| Dec0ne/KrbRelayUp | ~1.5k | 借鉴 | 先提权再横向的决策点 |
| NetSPI/PowerUpSQL | ~2.5k | 借鉴 | 链接服务器图纵深，垂直范例 |
| NetSPI/MicroBurst | ~1.8k | 借鉴 | 云控制面读枚举=跨段思想云版 |
| ly4k/Certipy | ~7k | 借鉴 | 纯目录读枚举 ADCS 误配 |
| PowerShellMafia/PowerSploit | 13.1k | 半跳过 | 已归档；PowerView LDAP 思路仍可读 |
| BC-SECURITY/Empire | 5.3k | 跳过 | C2 编排，对排查顺序贡献少 |
| its-a-feature/Mythic | ~8k | 跳过 | 通信层架构，非排查方法论 |
| GhostPack/SafetyKatz | ~1.3k | 跳过 | 加载壳 |
| OmerYa/Invisi-Shell | ~1.5k | 跳过 | 对抗检测向 |
| hashcat/hashcat | ~19k | 跳过 | 离线破解，凭据后处理 |

### A3 网络发现/侦察（28）
| 库 | Star≈ | 判断 | 理由 |
|---|---|---|---|
| netdisco/netdisco | 907 | ★ | 读 SNMP/CDP/LLDP/ARP/FDB 建拓扑零 sweep——读表先例 |
| shadow1ng/fscan | 14.5k | ★ | 本机 IP 推 C 段+两级收敛+指纹→模块联动 |
| robertdavidgraham/masscan | 26k | 借鉴 | 令牌桶限速+随机化目标，超大空间工程范本 |
| nmap/nmap | 13.5k | 借鉴 | 分层探针组合+限速调度 |
| projectdiscovery/nuclei | 31k | 借鉴 | 模板外置标杆（引擎/数据分离） |
| royhills/arp-scan | 1.3k | 借鉴 | --localnet 本段+OUI 指纹 |
| netdiscover-scanner/netdiscover | 398 | 借鉴 | fast 模式=网关先验采样先例 |
| schweikert/fping | 1.2k | 借鉴 | 微采样最小载体 |
| projectdiscovery/naabu | 6.2k | 借鉴 | 两级收敛：快探活→深扫 |
| projectdiscovery/httpx | 10.4k | 借鉴 | 去重+并发+标准化输出流水线 |
| bettercap/bettercap | 19.9k | 借鉴 | net.recon 被动解析=旁路收集 |
| trailofbits/onesixtyone | 726 | 借鉴 | SNMP community+外置字典→读表入口 |
| zmap/zmap | 6.4k | 借鉴 | 单包无状态+去重聚合 |
| lcvvvv/kscan | 4.3k | 借鉴 | 中文生态指纹→动作映射完整 |
| MJL85/natlas | - | 借鉴 | SNMP/CDP 自动画拓扑轻量版 |
| darkoperator/dnsrecon | 3.1k | 一般 | 反向解析反推命名，辅助证据 |
| bee-san/RustScan | 20.4k | 一般 | 二段式无新意 |
| lgandx/Responder | 6.6k | 一般 | 被动收集可借鉴，攻向为主 |
| snmp-check / SECFORCE/SNMP-Brute | -/333 | 一般 | 被 onesixtyone+SecLists 覆盖 |
| scottpeterman/secure_cartography | - | 一般 | SSH 读邻居同思路，偏重 |
| projectdiscovery/subfinder | 14.4k | 一般 | 外部 OSINT 对内网收敛借鉴有限 |
| laramies/theHarvester | 17.3k | 跳过 | 外部 OSINT |
| aboul3la/Sublist3r | 11k | 跳过 | 同上且停更 |
| dirkjanm/mitm6 | 1.9k | 跳过 | DHCPv6 攻击向 |
| TideSec/TscanPlus | 4.3k | 跳过 | 仅二进制不开源 |
| ffffffff0x/f8x | 2.2k | 跳过 | 环境部署工具 |
| 0xInfection/TIDoS-Framework | 1.9k | 跳过 | Web 攻击框架 |

### A4 提权/主机清单（28）
| 库 | Star≈ | 判断 | 理由 |
|---|---|---|---|
| peass-ng/PEASS-ng | 20.4k | ★ | 章节化+严重度×置信度着色+默认档全本地 |
| HackTricks-wiki/hacktricks | 12.2k | ★ | 条目="前提→枚举→判定→PoC→加固"四段式 |
| GTFOBins/GTFOBins.github.io | 13.6k | ★ | 条件→效果五元组 YAML+CI 校验 |
| LOLBAS-Project/LOLBAS | 8.8k | ★ | 攻击配方+Sigma 检测同条目（攻防同条目） |
| diego-treitos/linux-smart-enumeration | ~7k | ★ | l0/l1/l2 结果驱动升档教科书 |
| CISOfy/lynis | 16.3k | 借鉴 | 测试 ID+硬化指数+合规映射 |
| mzet-/linux-exploit-suggester | ~4.5k | 借鉴 | uname→CVE 决策表 |
| a13xp0p0v/kconfig-hardened-check | ~1.5k | 借鉴 | "应有值→实际值→建议" schema |
| jtesta/ssh-audit | 2k+ | 借鉴 | CVE/CWE+fail/warn/info 分级范本 |
| testssl.sh | ~8k | 借鉴 | 严重度标注+退出码编码 |
| ffffffff0x/1earn | ~4.8k | 借鉴 | 中文知识库章节化（已归档） |
| 1135/notes | 低 | 借鉴 | 中文提权 checklist 骨架清晰 |
| PowerSploit PowerUp | ~12k | 借鉴 | Invoke-AllChecks 逐项独立输出 |
| diego-treitos 系列/其他 | - | 一般 | LinEnum 线性无分级；les-2/SharpUp/BeRoot 覆盖窄 |
| GDSSecurity/Windows-Exploit-Suggester | ~3.5k | 跳过 | 已归档 |
| luke-goddard/enumy | ~1k | 跳过 | 无清单组织创新 |
| CIS-CAT | - | 跳过 | 闭源 |
| xiaoy-sec/Pentest_Note | ~2k | 一般 | 命令速查无判定 |
| guchangan1/All-Defense-Tool 等 5 个聚合库 | ~3k | 跳过 | 武器库堆叠 |
| nixawk/pentest-wiki | ~4k | 跳过 | 被 HackTricks 替代 |

### A5 蓝队 IR/攻防模拟（24）
| 库 | Star≈ | 判断 | 理由 |
|---|---|---|---|
| redcanaryco/atomic-red-team | ~11k | ★ | 一步=参数+前置检查+命令+预期结果+清理 |
| SigmaHQ/sigma (+spec) | ~9k+3k | ★ | selections+condition+falsepositives=检验机制标准 |
| Velocidex/velociraptor (+artifacts) | ~11k | ★ | artifact=能力单元最佳范式（前提+查询+输出列） |
| certsocietegenerale/IRM | ~2.4k | ★ | 按症状分册：步骤+raw log+误报，最接近我们的卡 |
| OTRF/ThreatHunter-Playbook | ~4k | ★ | 假设→所需数据→查询→判据，直觉编码化范本 |
| Bypass007/Emergency-Response-Notes | 5.5k | ★ | 中文排查手册事实标准，检查点清单制 |
| center-for-threat-informed-defense/attack-flow | ~800 | 借鉴 | action/condition/asset 图=排查链模型 |
| Yamato-Security/hayabusa | ~7k | 借鉴 | 4000+规则分类可复用 |
| meirwah/awesome-incident-response | 9.4k | 索引 | playbook 样本库入口 |
| GuardSIght battle cards | 收录 | 借鉴 | 场景→动作→升级路径卡片式 |
| osquery/osquery | ~20k | 借鉴 | "把 OS 当关系库查询"零发包范式 |
| Just-Hack-For-Fun/Linux-IR-COOKBOOK | ~1k | 借鉴 | Linux 应急步骤化 |
| m-sec-org/d-eyes 等 3 个 | 小 | 一般 | 检查项→脚本实现参考 |
| 0x4d31/awesome-threat-detection、mthcht/ThreatHunting-Keywords | ~4k/~1k | 素材 | 狩猎关键词补充 |
| apache/caldera | 7.2k | 一般 | ability 单元概念可借 |
| WithSecureLabs/chainsaw | ~3.8k | 一般 | 快速分诊思路 |
| AWS IR runbook / Counteractive | 收录 | 一般 | runbook 结构可抄，粒度粗 |
| BlueTeamNote / Blue-Team 等 | 小 | 一般 | 交叉校验检查点 |
| PagerDuty IR docs | 收录 | 跳过 | 流程管理非技术排查 |
| wazuh/wazuh | ~12k | 跳过 | 平台过重 |
| cugu/awesome-forensics、KAPE | ~5k | 跳过 | 工具目录非范式来源 |

## 附录 B：关键参考链接

- [yaklang/hack-skills](https://github.com/yaklang/hack-skills) · [skills.sh 页面](https://www.skills.sh/yaklang/hack-skills/)
- [mukul975/Anthropic-Cybersecurity-Skills](https://github.com/mukul975/Anthropic-Cybersecurity-Skills)
- [GhostPack/Seatbelt](https://github.com/GhostPack/Seatbelt) · [SpecterOps/BloodHound](https://github.com/SpecterOps/BloodHound) · [AlessandroZ/LaZagne](https://github.com/AlessandroZ/LaZagne)
- [netdisco/netdisco](https://github.com/netdisco/netdisco) · [shadow1ng/fscan](https://github.com/shadow1ng/fscan) · [projectdiscovery/nuclei](https://github.com/projectdiscovery/nuclei)
- [peass-ng/PEASS-ng](https://github.com/peass-ng/PEASS-ng) · [HackTricks](https://github.com/HackTricks-wiki/hacktricks) · [GTFOBins](https://github.com/GTFOBins/GTFOBins.github.io) · [LOLBAS](https://github.com/LOLBAS-Project/LOLBAS)
- [atomic-red-team](https://github.com/redcanaryco/atomic-red-team) · [SigmaHQ/sigma](https://github.com/SigmaHQ/sigma) · [velociraptor](https://github.com/Velocidex/velociraptor) · [CERT SocGen IRM](https://github.com/certsocietegenerale/IRM) · [ThreatHunter-Playbook](https://github.com/OTRF/ThreatHunter-Playbook) · [Emergency-Response-Notes](https://github.com/Bypass007/Emergency-Response-Notes)
