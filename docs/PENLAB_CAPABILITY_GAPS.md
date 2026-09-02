# 渗透靶场场景能力评估与补全清单

> 场景定义：入口为 Web 漏洞（如 PHP RCE）→ 拿到主机权限 → 网络侦察 → 内网探活（同 C 段优先、B 段有限）→ 存活主机端口/指纹识别 → 内网漏洞识别 → 修复。整体是「识别 → 修复 → 复核」闭环；探测深度默认 2 层（打下一层主机后做一轮内网探活），开 3 层 = 再争取拿下一层并继续探活。
>
> 评估基线：feat/mindmap-read-loop 分支，2026-09-02。所有结论均给出代码/文档出处。

---

## 一、能力现状总表

| 场景环节 | 能力 | 状态 | 出处 |
|---|---|---|---|
| 网络侦察（拿权后先看网络） | Precheck 只读三表（接口/路由/ARP）→ 网段分级 | ✅ 具备 | `internal/netdev/layerdiscover.go` |
| 内网探活（C 段优先） | 计划卡：小网段默认勾选、中等网段人工确认、大网段不默认扫 | ✅ 具备 | `layerdiscover.go`（DiscoverPlan/PlanStep） |
| 内网探活（执行） | TCP 管理面端口探测经跳板，限速 50/s（封顶 256）、每主机 800ms 抖动、缓存 24h | ✅ 具备 | `internal/netdev/discover.go:35-95` |
| 探测深度控制 | `max_hops` 默认 2、夹 1..4；层账本 + layerGuard 拒绝超层 | ✅ 具备（与"默认 2 层"建议一致） | `internal/config/netdev.go:317`、`discoveryrun.go:172` |
| 拿下一层后继续 | 人工晋升机制：晋升设备成为下一 vantage；只接受有 vault 凭证的纳管设备 | ✅ 具备（"拿下"动作本身由人完成，产品管每层侦察） | `layerdiscover.go` 头注（不变量 8） |
| 端口/指纹识别 | 端口集 22/23/161/443/830 + banner（F1）+ SNMP sysDescr（F2）+ HTTP 指纹（F3，opt-in） | ✅ 具备（端口集偏管理面，Web 靶机覆盖窄） | `discover.go:56-57`、`internal/config/netdev.go:294-309` |
| 内网漏洞识别 | ① 基线核查（配置类弱点）② 弱口令核查（engagement 限时，尝试次数硬顶）③ CVE feed 匹配 | ✅ 具备（通道偏被动） | `baseline.go`、`assess.go`、`cve.go` |
| 影响面分析 | 攻击路径推演：暴露 Finding × 邻接边 → 波及面（纯本地，标「推演」） | ✅ 具备 | `attackpath.go` |
| 修复 | 提案路径：起草（含回滚）→ 行内批准 → 先备份后执行 → 可回退，agent 无执行权 | ✅ 具备 | `proposal.go` |
| 复核 | 提案观察期（默认 30 分钟，劣化触发 Finding）+ 定时基线 + Finding DedupKey 自动 resolve | ✅ 具备 | `proposal.go:109`、`finding.go:46-51` |
| Web 靶机（DVWA 类） | 浏览器工作台：交互/录制/技能/确定性执行/定时值守 | ✅ 具备 | `docs/browser-ops-guide.md` |
| 案例沉淀 | 安全工作台：案例/IOC/CVE 视图/CaseBundle 导出 | ✅ 具备 | `SecWorkbench.tsx` |
| 授权与审计 | engagement 信封（攻限时）、全程审计、紧急停止 | ✅ 具备 | `assess.go:28-39` |

**结论：防御侧闭环（识别→修复→复核）与分层侦察骨架已具备；缺口集中在"黑盒递进"和"主动识别"两侧。**

---

## 二、问题清单与修复意见

按优先级排序（P0 阻塞场景 / P1 显著削弱 / P2 体验补全）。

### P0-1 黑盒递进时 scopes 白名单无法扩展 ✅ 已落地（2026-09-02）

- **问题**：每轮探测的 CIDR 必须落在 `[netdev.discovery] scopes` 静态白名单内，越界拒绝且该护栏永不可关（`discover.go` scopeAllows）。白盒靶场可预先配齐各层网段；黑盒场景拿下一层后 Precheck 发现的内网网段不在 scopes 内，探测直接被拒，递进中断。
- **落地方案**：Precheck 计划卡就地标识越界网段，「加入探测范围」人工确认后经 `NetDevDiscoverExtendScopes` 桥扩展——`ExtendScopesCandidates`（discover.go）校验 CIDR 并对已被现有 scope 覆盖的候选去重，`applyConfigOnly` 落盘，逐条 guardrail 审计 + 状态历史快照。护栏由人扩展、永不绕过。

### P0-2 B 段（/16）全量探活与 UDP/ICMP 探测缺 netprobe ✅ 短期方案已落地（2026-09-02）

- **问题**：tunnel 模式单 CIDR 硬顶 4096 台（/20），超过报错指引 "use netprobe"（`discover.go:416-417`）；但 netprobe 二进制是 P3 后期阶段，**未实现**。UDP/ICMP 探测同样缺失。B 段目前只能人工拆成 16 个 /20 分次跑。
- **落地方案（短期）**：`buildPlan` 自动把超限网段拆成 /20 计划步（`splitForTunnel`，layerdiscover.go）——继承父网段 class 与 default（medium/large 仍需显式勾选），拆块数 >16（父网段宽于 /16）不拆、保持整体 default-off。B 段以内网段计划卡直接可用。
- **落地方案（中期 netprobe 编排，2026-09-02）**：产品侧编排就绪（`netprobeorch.go`）——编排自家的 `cmd/netprobe` 二进制（用户构建并 `netprobe_path` 指定，通常拷到跳板机执行），闸门与 nmap 同级（engagement + scopes），主机预算走 `max_hosts_per_job`（默认 65536 = 一个 /16，不占 tunnel 的 4096 上限），支持 ICMP 存活（需二进制以特权运行）；存活主机回填待确认区（仅 ICMP 存活的主机为无端口行）。agent 工具 `netdev_netprobe`。UDP 探测仍留后续。

### P1-1 服务级主动漏洞探测缺失 ✅ nmap 编排 v1 已落地（2026-09-02）

- **问题**：识别通道全部偏被动/半主动（配置基线、CVE 情报匹配、次数硬顶的弱口令核查），没有服务级探测（nmap 编排、版本级服务指纹、已知漏洞主动验证）。
- **落地方案（v1）**：`nmaporch.go` 编排用户自备的 nmap（`nmap_path` 配置或 PATH；缺失报安装指引——产品编排外部工具，绝不内置扫描引擎）。闸门：engagement 信封 → scopes 白名单 → 单 CIDR ≤4096 主机。结果（product/version）回填待确认区（Parsed 直载指纹），纳管即接 CVE 匹配闭环；agent 工具 `netdev_nmap` + 发现弹窗「nmap 服务探测」按钮双入口。
- **v1 未含（后续按需）**：UDP/ SYN 扫描（需 root/netprobe）、脚本扫描（--script）、主动漏洞验证（PoC 编排属红队批）。

### P1-2 CVE 匹配精度受制于手工资产字段 ✅ 已落地（2026-09-02）

- **问题**：CVE 匹配是对设备手工录入的 `Vendor + OS + Model` 做子串匹配（`cve.go:76`），字段缺失即漏报，发现侧指纹结果（banner/SNMP/HTTP）不回填资产。
- **落地方案**：`NetDevPromoteForm` 增 `model` 字段——纳管时前端把 banner/HTTP 指纹浓缩成 `product + version` 预填设备 Model（待确认区行内同步展示指纹徽标），CVE 匹配的文本面从纳管第一天起可用；设备表单仍可人工修改。

### P1-3 漏洞利用入口无能力（有意缺位，需明确决策）

- **问题**：PHP RCE 等利用、payload 执行、在线爆破在当前批次**不存在于 boot 级**（`NETDEV_SPEC_V2.md:618`，红队批暂缓）。
- **影响**：靶场里"打点拿权"完全靠人工；产品只接管拿权后每层的标准化侦察。
- **修复意见**：**靶场阶段维持现状**——人工打点 + 打下后纳管（存 vault 凭证）+ 晋升为 vantage，这个分工在教学场景反而是优点（证据链完整、每层跳板可审计）。若未来要产品化利用链，按附录 C 三要素（engagement 信封 + profile 显式配置 + 重启生效）立项，不建议现在开。
- **工作量估计**：0（当前）；红队批立项另议。

### P2-1 Finding → 修复提案无一键转化 ✅ 已落地（2026-09-02）

- **问题**：Finding 带修复建议，但转提案靠 agent 读后重新起草，界面无「转修复提案」入口。
- **落地方案**：FindingRow 未解决态新增「转修复提案」按钮——起草提示词带发现 id/标题/设备跳对话，走既有 netdev_propose 人工审批流。

### P2-2 弱口令核查缺 UI 入口 ✅ 已落地（2026-09-02，巡检家族菜单）

- **问题**：后端 `NetDevWeakCredCheck` 桥已就绪但无 UI（`NETDEV_SPEC_V2.md:23` 明确标注"仅缺 UI"），目前只能对话调用 `netdev_assess`。
- **落地方案**：左栏「巡检家族」菜单（网络巡检/主机体检/基线核查/**弱口令核查 basic 档/strong 档**），全网入口与对话调用同一后端预算与审计。

### P2-3 提案观察期不复跑关联核查 ✅ 已落地（2026-09-02）

- **问题**：观察期只对比健康基线（ifDown 计数），不复跑产生该 Finding 的具体核查；复核靠全局定时基线兜底，不是提案驱动。
- **落地方案**：① 基线发现接入 `Source` 生命周期（`baseline:<rule>`）——重复核查更新同一告警不堆积，规则不再命中且全部受检设备已复核时自动 resolve（定向复核不误恢复未复核设备的告警）；② 提案全部步骤成功进入观察期时自动对涉及设备跑 `RunBaselineFor` 复核，结果入审计。

---

## 三、建议落地顺序

1. **第一批（靶场开跑前，约 1 周内）**：P0-1 动态 scope → P0-2 短期 /20 自动拆分 → P1-2 指纹回填。做完后黑盒 2~3 层递进可完整演练。
2. **第二批（体验补全，随手做）**：P2-1 / P2-2 / P2-3，把「识别→修复→复核」闭环的接缝焊死。
3. **单独立项**：P1-1 nmap 编排（服务级探测）；netprobe（B 段全量 + UDP/ICMP）。两者都挂 engagement 授权模型。
4. **明确不做（现阶段）**：P1-3 漏洞利用链——保持人工打点、产品管侦察与修复的产品边界。

## 四、靶场即时可用的操作路径（现状即可跑）

 scopes 预配各层网段 → 入口靶机纳管（vault 凭证）→ 作为 vantage 跑 Precheck → 计划卡确认 → 探活+指纹 → 待确认区线索纳管 → 人工确认晋升 vantage → 第 2 层 Precheck/探活 → 产出进 Finding → CVE 匹配 + 基线核查 + （engagement 内）弱口令核查 → 提案修复 → 观察期 → 定时基线复核 → Finding 自动 resolve → 安全工作台案例沉淀。
