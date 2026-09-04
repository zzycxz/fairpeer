# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### fix(browser): stable: 判稳重写——首 token 前绝不放行（browser-IT-ops 抓不到慢回答的根因）

用户实测 browser-IT-ops 查哈尔滨池告警：AI 助手回答需 30-60 秒，第五步 `stable:` 等待在回答出现前就放行、第六步 extract 抓空，技能只返回流程日志。原实现（自适应静默版）仍有三个提前放行洞，本批把 `stable:` 重写为就绪态状态机（`internal/tool/builtin/browser.go`）：

- **三个洞**：① 目标元素缺失走「静态短内容 30s 兜底」——恰好短于 30-60s 的首 token 延迟，30s 整点放行；② 空块挂载（签名 none→1:0:0）也算「变化」，2s 静默即放行；③ 「正在…」类短占位文案同样吃 30s 档
- **新语义**：首 token 之前一律不判稳——目标缺失、空块、占位文案（正在/请稍/思考/生成/加载/分析/检索/处理/等待/loading/thinking/generating/analyzing 前缀）、纯点符（…/···/....）、`aria-busy` 均视为未就绪，只有超时能结束等待；「变化」必须是有内容的就绪态变化；流式判稳保持自适应静默 2-8s；静态兜底两档——充实内容（≥40 字或有渲染子节点）10s、短静态文案 30s→**90s**（盖过 30-60s 首 token 窗口，未识别的占位文案也拖不过首 token 到达）
- 验证：嵌入 JS 从 Go 源提取后经 node 虚拟时钟跑 9 场景沙盒（慢首 token/缺失/空块/未识别占位/静态两档/快速流式/aria-busy）全过；`TestConsoleWaitStableSemantics` 占位 4s/26s 不放行继续通过；`TestConsoleWaitStableStaticFallback` 静态块改为 ≥40 字钉 10s 档（短文案 90s 契约由沙盒覆盖）；builtin 全包测试通过；工具描述与两份 browser-ops-guide 镜像、browser-it-ops SKILL.md 第 5 步注意事项同步新语义

### feat(netdev): 备份→恢复提案闭环（申报书 T1）+ 审计链路/演示剧本/LinkPeer 状态文档批次

申报书缺口 TODO（`docs/APPLICATION_GAPS_TODO.md`）代码会话批次，T1/T5/T6/T7 四项落地：

- **备份→恢复提案闭环（T1）**：新增 `netdev_backup` agent 工具（`internal/netdev/backup.go`）——list/read/diff-current 三动作，`diff-current` 把库存版本与现拉 running-config 做 unified diff，当前侧走密封 Exec（分类器/预算/脱敏/审计全适用）；`netdev_propose` 新增 `restore_from` 字段（`ValidateRestoreFrom` 校验版本在库且设备与步骤一致），审计行记 `draft … restore-from <id>` 可回放；前端 BackupTimeline/BackupHistory 两处版本列表加「起草恢复提案」按钮（注入起草提示词 + 跳实况页签），提案卡显示"↩ 恢复提案 · 来源版本"。红线不变：AI 只起草，人整份审批后才执行。`TestBackupDiffCurrentAndRestoreFrom` 守护
- **git 审计链路核实（T6）**：新增 `docs/NETDEV_AUDIT_CHAIN.md`——查清申报书"步骤进入 git 审计"实际对应三条机制（工作区 git 面板只读探针 / netdev 审计 JSONL+SHA-256 哈希链 / trust domain 锚定），**设备操作步骤不进 git commit**，附申报书替换措辞与截图取景说明
- **故障定界演示脚本包（T5）**：新增 `docs/NETDEV_DEMO_SCRIPTS.md`——IP 失联定位（netdev_locate 并发→端口核查→拓扑标红→Finding 落证据）与变更-故障时间轴关联（Timeline 同轴→备份双版本 diff→恢复提案起草）两个场景的指令包、预期链路、讲解词要点与录屏取景清单
- **LinkPeer 状态修正（T7）**：LINKPEER.md 徽章 planning(M0)→in progress（灰→绿）+ 路线图表加状态列（Go 桌面侧 M0–M3 完成、移动端仓库 M4+ 进行中）+ 快速开始警示更新；同批修 `internal/mobilebridge/doc.go` 过时的 M0-spike 注释与 `docs/MOBILE_CLIENT_PLAN.md` 状态行
- prompt addon（`profile_netdev_addon.go`）工具清单补 netdev_backup 与恢复起草纪律；APPLICATION_GAPS_TODO 勾选校准（T4 主链路核实为已接线，仅剩 PPT/Excel 附件）
- 验证：go build internal+desktop 通过；netdev 备份测试、frontend tsc、locale-parity 通过

### fix(skill): 运维界面技能体系审计修复批次——白名单漂移/浏览器技能打架治理/用户技能域折叠

用户提出五问审计运维界面技能与工具（找全、描述边界、适配性、暴露收紧、专用 vs 通用浏览器技能打架）。逐项取证确认后成批修复：

- **白名单漂移（红测试转绿）**：`builtinBuiltinSkillNames` 漏 `netdev-vulnscan`——仓库自带的 `TestBuiltinSkillNamesCoverCodeBuiltins` 本就在失败，该技能因此绕过 dev/cowork 白名单出现在编码/办公索引。补名册 + 注释更新
- **browser-auto 进 netdev 白名单**（用户方向 2026-09-04 定稿「启用为通用兜底」）：运维页签恢复通用浏览器能力，散文模板（site-console/page-patrol）的 `run_skill("browser-auto")` 委派链路复活；硬封印下其子代理拿不到 bash/写文件，兜底只有浏览器读写。`TestNetDevWhitelistsBrowserAuto` 钉住该决策
- **专用/通用打架治理**：cowork 路由表（原 "Any browser task → browser-auto" 一刀切）、netdev 路由表（新增浏览器行）、browser-auto 描述三处一致写明「站点专用浏览器技能优先，browser-auto 是通用兜底」——用户建的发票/车票/监控专用技能不再被通用兜底压过
- **用户技能按 `domain:` 域折叠**：Profile 新增 `SkillDomains`（dev=`["code"]` 哨兵、cowork=`["browser-ops"]`、netdev=`["browser-ops","netdev"]`）；skill.Skill 新增 `Domain` 字段解析 frontmatter。声明域的用户技能在域不匹配的 profile 索引中折叠（省预算防误路由，run_skill 仍可调——与出厂白名单的硬禁用是两道闸），无域标记永不折叠。动机：netdev-assess（domain: netdev）曾出现在办公/编码索引里而那边没有 netdev_* 工具。`TestBuildSkillDomainFolding` 守护
- **散文模板三连修**（skillTemplates.ts）：补 `domain: browser-ops`（原保存后从面板列表消失）+ `draft: true`（原绕过唤醒生命周期、保存即全 profile 生效）；siem-watch 补运行环境说明（定时任务在办公 profile 触发工具齐备；运维页签手动运行缺 email_send，可改 im_send）；三处步骤编号错引修正（stream-query ×2、form-submit ×1）
- **面板防坑**：`BrowserConsoleListSkills` 改显示 frontmatter 注册名（核心库规则校验，允许大写）——修「面板显示的名字对话调不动」的精神分裂；`BrowserConsoleSaveSkill` 拒绝 >600 带秒缀的 wait 超时写法（`50000s` 被双端解析器按毫秒读成 50 秒，实测坑过 browser-it-ops 抓不到慢回答）；编辑器描述字段加「索引每行约可见前 100 字、关键词前置」提示（中英）
- **工具描述修正**：browser_wait 条件枚举补 `download`（实现早有、描述漏了）；netdev_discover 的 "use the probe" 消歧为明确指名 netdev_netprobe 并注明 nmap/netprobe 的授权信封门槛差异
- **boot.go 注释更新**：browser 工具注册分支的过时表述改为双路径实况（browser-auto 子代理 + browser-flow 内核执行器）
- **文档同步**：SKILL_ARCHITECTURE_SPEC 三分域表补全运维技能行（诊断卡系列 + browser-auto）与名册计数（21 项），新增域折叠/优先级两条实施记录；browser-ops-guide（docs 与前端两份镜像）修正 stable 判稳表述（自适应 2-8 秒）并新增超时写法规则（≤600s，>600 带秒缀会被按毫秒读且保存拒绝）
- 验证：go test boot/config/skill/netdev/tool.builtin/desktop 六包通过；frontend skill-doc 模板守卫 101/101、locale-parity、tsc 干净

### refactor(netdev): 侧栏八标签极简化——对齐办公/编码底部导航样式（用户定稿）

用户复看后定稿：左下角与办公/编码侧栏视觉统一，全部修饰性元素撤除：

- **对齐**：立即巡检从分裂按钮（主区+▾ 24px 溢出）还原为整行单按钮——它此前把该行拆成两截，是八标签无法对齐的直接原因；单击直跑只读网络巡检不变
- **▾ 溢出菜单删除**：主机分诊/基线/弱口令×2 不再占侧栏。能力不丢——设备卡有单机主机分诊入口，基线在总览场景卡与发现空态，弱口令/全网分诊走对话（netdev_assess / netdev_triage，信封与预算闸门管着）
- **大屏红点删除**：风险状态信号归总览风险卡（数字+蓝队拆分行）与主区大屏 chip，侧栏不再携带；此前加的计数 tooltip 一并撤除
- **历史 (0) 删除**：今日审计数从标签里拿掉（提案的待决策计数保留——它是可操作的队列数字，用户未提）
- 连带清理：inspMenuOpen/triageBusy/weakBusy 状态、runTriageAll/runWeakCredAll 回调（noUnusedLocals 下必须删）、中英各 17 个孤儿 locale key
- 验证：frontend tsc + 全量 vitest 43 项通过

### feat(netdev): 总览接入蓝队核查存量——风险卡来源拆分 + 事件卡聚合行（深链蓝队透镜）

用户反馈「蓝队核查出一堆问题，总览看不到」。核查现状：风险数字（critical/warning/加权分）本就计入蓝队条目，但事件流只收 syslog/trap/alert——存量不刷流是对的（总览定位"正在发生"，蓝队结果是"已知存量"，全量倒进去会告警疲劳），缺的是一个聚合出口。方案取此前评估的 a+b 双做：

- **后端**（`netdev_dash_app.go`）：`NetDevOvRisk` 增 `vuln_critical/vuln_warning/vuln_open`——统计循环按蓝队透镜同规则（source 前缀 `vulnscan*`/`cve:*`）拆分未闭环计数
- **风险卡**：critical/warning/info 行下补一行「蓝队核查 {c}C · {w}W」（仅 >0 显示），点击深链发现中心
- **事件卡**：头部补聚合行「蓝队核查 {n} 条未闭环 →」（sev 徽标随有无 critical 变色），点击同深链
- **深链语义**：`findingMatchesJump` 新增 `vuln` 过滤器（vulnscan*/cve:*，与发现中心蓝队核查筛选同规则；无 source 的发现不误匹配）
- 测试：dash-jump-filter 补 vuln 四分支断言；desktop go build + frontend tsc + dash-boards/locale-parity 通过

### fix(provider): 网关瞬断伪装的 400 改走重试 + 报错不再误导（用户实测：发 netdev-assess prompt 直接失败）

用户在对话框发首条评估指令即失败，报「Malformed request (HTTP 400): the request body was rejected. This is likely a bug」。抓包看 body 实为小米 MiMo 网关的 `{"param":"Connection prematurely closed BEFORE response"}`——网关到它上游的连接断了，是服务端瞬时故障套了 400 的壳，与请求内容无关：

- **重试层**（`provider/retry.go`）：新增 `IsTransientGatewayBody`——400 的 body 命中中继断连签名（prematurely closed / connection reset）即按可重试处理，走既有退避循环（至多 10 次）；真正的参数错误 400 仍立即失败不重试
- **报错文案**（`control/errmsg.go` + i18n 中英）：该类错误改映射到新文案「网关瞬断 (HTTP 400)：模型网关与其上游的连接中断，你的请求本身没有问题。已退避重试；仍失败时重发一次即可」；`providerBodyReason` 补解析 `param` 字段（MiMo 把真实原因放这，message 只有笼统的 "Request failed"）
- 测试：`TestSendWithRetryRetriesGatewayTransient400`（识别 + 重试恢复 + 真 400 不误判）、errmsg 补网关瞬断分支断言；provider/control/i18n 三包通过
- 顺带发现（未处理）：app.log 里有 `signal_disconnect dial tcp 127.0.0.1:8080 拒连` 的持续重试噪音，与本次无关，像是 bot 信使桥接的本地服务没起

### fix(netdev): 大屏红点常亮根治（单测夹具泄漏真实状态目录）+「立即巡检」单击直跑

用户报告的两个左下角问题（2026-09-04），一个查出来是测试泄漏、一个是交互设计：

- **① 大屏红点一直亮——根因不在 UI**：红点=「存在未闭环 critical」本属设计信号，但三个单测没重定向状态目录，`SaveFinding` 把夹具直接写进用户真实 `%APPDATA%\fairpeer\netdev\findings\`——escalate_test（esc-\* 四条，含两条 critical active）、locate_notify_test（notify-test-\* 两条）、trap_tunnel_test（合成 trap `[trap] link-down @ sw-1`，真实配置里根本没有 sw-1）。每次 `go test` 都刷新一批假告警，红点永不熄。修复：三处测试补 `findingsDirOverr = t.TempDir()` 隔离（对齐包内其余测试的既有模式）；清理已泄漏的 8 条垃圾（含两条 trap）；全量跑测后对状态目录做前后快照 diff 确认零新增
- **② 风险点可解释**：色点补计数 tooltip（红=「N 条未闭环 critical」/橙=「N 条未闭环警告」，提示去发现中心处理即熄灭），侧栏「大屏」按钮与主区大屏 chip 两处同享；顺带把 warning 计数从内联 filter 提为 `openWarn` 变量
- **③「立即巡检」不再弹窗**：改分裂按钮——主区单击直跑只读网络巡检（默认动作，安全无副作用）；主机分诊/基线/弱口令×2 收进右侧 ▾ 溢出菜单（它们有登录/写副作用或字典依赖，必须显式选择，且此处是其唯一电池入口；总览场景卡的对话式入口不变）
- **④ 八按钮全量审计（用户追问"不止大屏"）**：逐个核过左下角 8 项的徽标与弹窗——设备列表/浏览器/偏好干净；历史是标签内联计数 (N) 非徽标；大屏色点已由 ② 治理；巡检已由 ③ 治理；**安全工作台摘掉 findingsHot 红点**——该活动信号属于「发现中心」dock 页签（访问即清），本按钮开的是案例/IOC 工作台展示不了新发现，挂着就是一枚点了不灭的假红点；发现中心页签自带同款热点（findingsHot||vulnHot），信号不丢，顺手删除失去引用的 `.ndv-nav__hotdot` 死样式。**提案按钮计数徽标补 tooltip**（待决策提案数，处理即减——徽标本就是可自清的队列计数，保留）；hotTabs 清除时机补一档：用户正盯着的页签上到达的新事件不再留点（原来只在切页时清，页签内到达会留一枚死角）
- 验证：internal/netdev 全量 go test 通过 + 状态目录快照零泄漏；frontend tsc + dash-boards/dash-jump-filter/locale-parity vitest 通过

### refactor(netdev): 右侧 dock 页签合并 13→11——发现中心吸收蓝队核查、历史吸收状态历史

用户审视右边栏后的两处合并（其余组合评估过不建议动：健康/总览粒度不同、设备/拓扑信息密度都高）：

- **① 发现 + 蓝队核查 → 一个「发现中心」**：顶部来源筛选片（全部 / 蓝队核查 / CVE / 告警，按 source 前缀 vulnscan|cve: / cve: / alert:|syslog:|trap 过滤）；选「蓝队核查」时显示评估流程步骤卡 + 蓝队核查实时落卡（对话扫描滚动结果保留），其余透镜走常规发现列表（聚合/深链过滤不变）。热点与自动聚焦合并到发现页签（新蓝队发现点亮发现页签并定位到蓝队核查透镜）；旧 vulnscan 深链/存储页签重定向
- **② 审计 + 状态历史 → 一个「历史」页签**：命令审计（含链校验徽章/今日统计）在上、配置状态与恢复（StateHistoryPanel）在下；可回退事件数徽标转移到历史页签；旧 state 路由重定向
- **左下角导航栏（用户审视）**：补「浏览器」常驻入口（运维浏览器是四大工作台之一，此前只有主区 chip 可进）；「审计」按钮随页签合并改标「历史 (N)」；**收敛到 8 项**——去掉「拓扑」纯视图捷径（dock 页签与"+"目录仍可达，是视图捷径里重复度最高的）；**修「巡检」点不动**：侧栏容器 overflow:hidden 会把向右弹出的菜单整体裁掉（用户实测点了没反应），改为向上、在栏内展开
- 验证：frontend tsc + vitest 12 项通过（未打包，按新节奏攒批）

### feat(netdev): 评估流程的控制补 UI、引导成体系——授权信封卡片/状态徽标/步骤卡/对话技能

上一批结论：评估链"控制完整、引导断链"（授权信封 TOML-only、状态不可见、无流程向导）。本批补齐，长任务按步骤交付、每步自带验收：

- **授权信封进设置页**（后端）：NetDevSettingsView 增 assessment（form-owned；nil 旧负载保持存量、全空=关闭信封）；ValidateNetDev 补信封校验（部分填写拒绝、expires 必须 YYYY-MM-DD；过期是合法保存态——运行闸门拒绝使用，历史可留）。回环测试扩展信封字段 + TestAssessmentEnvelopeValidation 四分支
- **设置页「评估授权信封」卡片**（护栏与读表页签顶部）：编号/有效期/批准人/授权范围，实时显示剩 N 天或已过期
- **标题栏状态徽标**：有信封显示 `🛡 ENG-xxx · N天`（绿）/过期（红），点击直达设置；无信封不占位
- **评估流程步骤卡**（用户复测后调整位置：总览→蓝队核查页卡顶部，可折叠）：授权→测绘→纳管→漏洞→弱口令→攻击路径→报告 七步，状态由真实数据推导（信封/待确认区条数/设备数/发现数），每步直达入口（设置/发现弹窗/设备/发现中心/对话指令/安全工作台）；它是引导不是闸门——闸门仍在后端
- **对话侧调用方法**：用户级技能 `~/.fairpeer/skills/netdev-assess/SKILL.md`——描述触发即按七步走，每步先查前置（信封缺失停下引导），红线写死（只测授权范围/发现不登录/不主动利用/全程审计），allowed-tools 与实际工具名核对
- 验证：config/netdev 测试（信封回环+四分支校验）+ desktop go build + frontend tsc + vitest 12 项通过；wails 打包

### feat(netdev): 蓝队漏洞核查——对话驱动技能 + 右侧「蓝队核查」页卡实时结果

用户方向：漏洞知识在模型权重里，产品该做的是指纹发现——对话里发起核查，结果实时落运维界面右侧页卡：

- **技能**：内置 `netdev-vulnscan`（inline，过程在对话可见）：指纹发现（服务器包清单/补丁级，网络设备版本）→ 候选（`netdev_cve_match` feed 匹配 + 模型知识标注"须验证"）→ 只读验证（版本区间比对）→ 证据立案 `netdev_finding`（source=vulnscan）。红线写进技能正文：不做利用、主动扫描须评估信封不代开；挂入 netdev profile 白名单与委派表
- **新工具 `netdev_cve_match`**（只读本地）：把 CVE feed 匹配从 UI 桥暴露给 agent；无 feed/无交集返回引导文案而非报错
- **Linux 读表补软件清单动词**：`dpkg -l`/`dpkg-query`/`rpm -qa`/`rpm -q`/`pip(list|freeze)`/`java -version`/`openssl version`/`ssh -V`/`nginx -v`/`apache2ctl -v`/`httpd -v`——纯查询形态，安装/卸载动词仍拒绝；Windows 侧 `get-` 前缀本就放行（Get-HotFix/Get-ItemProperty）
- **实时推送**：netdev 包级 Finding 观察者（SetFindingObserver，异步 recover，只在保存成功后触发）→ desktop `netdev:finding-saved` Wails 事件（搭 live forwarding 便车装一次）→ 前端 vulnScanState 模块 store（browserMirror 模式，环 100 条、同 ID 滚动覆盖）
- **页卡**：DockTab 新增 `vulnscan`（待决策组，ShieldCheck），老安装一次性补种；新发现点亮热点 + 首次到达自动开 dock 聚焦（每次挂载至多一次）；VulnScanPanel 合并磁盘种子×实时尾部，行内动作：建案例/跳发现/插修复提案指令；「发起核查」按钮把示例指令插进对话框；CVE 扫查按钮同发现页
- 验证：driver/tools/netdev/skill/config go 测试 + finding observer 三态单测（成功触发/失败静默/panic 隔离）+ frontend tsc + vuln-scan-state（20 断言）+ locale-parity + guides-drift 通过

### feat(netdev): 串口控制台（COM 口）——USB 转串口线直连交换机 Console

用户任务：交换机 Console 口接入。原生 Windows API 实现（golang.org/x/sys 的 CreateFile/DCB/CommTimeouts，**零第三方依赖**——proxy.golang.org 被墙、镜像拉不到 go.bug.st/serial，索性手写）：

- **串口层**：console_windows.go——`\\.\COMn` 打开、8N1 无流控、20ms 间隔超时读（空闲返回 0,nil 由读循环消化）；COM 口枚举读注册表 SERIALCOMM（USB 转串口即插即见）；非 Windows 构建 stub
- **会话复用**：OpenConsoleSession 构造 stdin=串口线、缓冲=读 goroutine 的 Session——提示符检测/分页关闭/回显剥离/脱敏/审计/操作实况与 SSH 会话**完全同一套引擎**；Session.Close 加 nil-ssh 守卫；无主机密钥/凭证：物理接入即授权，但只读分类器照常密封每条命令
- **配置**：`console_port`/`console_baud`（0=>9600）；校验——端口名纯 token、CLI vendor only（redfish/snmp/docker/k8s/firewall 拒绝）、配串口时管理地址可留空（Console 设备往往还不知道 IP）
- **接线**：Manager.connect/TestConnection 分支走串口；E-STOP/Close/空闲回收统一 closeConn（nil client 守卫）；流式跟随暂不支持串口（明确报错，一次性读取可用）
- **UI**：设备表单新增「串口控制台（COM 口）+ 波特率」字段（CLI vendor 显示），COM 口 datalist 来自本机枚举；设备列表地址列显示 🔌COM3 标记；地址/串口至少填一项的新校验文案
- 验证：netdev 全量（201s，含 console 新测试：nil-ssh Close、校验六分支）+ config 全量 + desktop go build + frontend tsc + vitest 通过

### feat(netdev): 日志源探测——「这台机器上到底有哪些日志」一键枚举

用户痛点：连上 SSH 后不知道里面有哪些日志可读，也不该要求用户记住发行版文件名。探测按钮用三条纯只读表命令（`systemctl list-units --type=service --state=running` / `ls -lh /var/log` / `docker ps`）在设备的既有密封会话上枚举真实存在的日志源，**不是**网络发现——不涉及 scope 白名单、不产生设备会话之外的流量，分类器/预算/脱敏/审计全部照旧：

- **后端**：`netdev.LogSourceProbe`（probelog.go）——运行中服务→journal: 候选、容器→docker: 候选、/var/log 常规文件（过滤轮转归档 .gz/.1/-日期后缀、目录、符号链接）→file: 候选（带大小）；docker 不存在记为提示而非错误。三条解析器 fixtures 测试（systemctl 表头/legend、ls 轮转过滤、docker 表 NAMES 列）通过
- **桥接**：`App.NetDevProbeLogSources`（90s 超时）+ bridge 接口/dev mock
- **前端**：日志页源栏新增「探测日志源」按钮（仅 Linux 主机显示），结果按 运行中服务 / 容器 / 日志文件 三组渲染为 chip，点击候选即切换为当前日志源（journal:nginx / docker:web / file:/var/log/messages），每组上限 24/30 条折叠计数
- **应用日志目录**（追问补全）：探测追加扫描常见应用日志根（`ls -d /opt/*/logs /usr/local/*/logs /srv/*/logs /data/logs …`——`*` 不在 ShellMetachars 清单内，由远端 shell 在 ls 内展开，无法链命令），每个目录（≤8）再 `ls -lh` 列文件；ProbeFile 带 allowed 标记（logPathAllowed 对设备白名单判定）。白名单外的文件 chip 虚线样式，点击即把其目录一键登记进设备的 log_paths（同一人工配置管线 SetNetDevSettings）并选为当前源——发现应用日志→登记→读取一气呵成，不再要求用户先去设置页手工填路径
- **两个独立按钮**（用户要求）：日志页标题栏新增「🧭 网络发现」chip——一键打开子网发现弹窗（选探测起点→预检→计划卡→隧道探测，与设备页签入口同源同逻辑，含中断续跑检测）；「探测日志源」从源栏挪到动作行首位成为与 筛选/读取/跟随 平级的独立按钮（有探测结果时高亮）
- **「转为发现」按钮**（连接日志页与总览）：原始日志行不会自动进总览——总览聚合的是发现（findings）。新增 App.NetDevFileFinding（人工专属入口，非智能体工具）：把日志页当前视图（含等级过滤）的行提交为一条 Finding（证据脱敏、上限 200 行、severity 随等级过滤自动定级），立即出现在发现中心/总览并进入告警生命周期、通知出口与每日晨报；保存后 dashEmit 刷新总览
- 验证：go 测试（parseLsFiles 含应用目录 allowed=false 用例）+ desktop go build + frontend tsc + vitest（locale-parity/dash-boards）通过

### feat(netdev): `system:main` 系统日志源——发行版无关，Linux 设备默认

用户反馈：不一定点得对设备是哪个 Linux（centos 还是 debian 决定了系统日志文件名），「只要能获取一个系统日志就可以」。落地：

- **后端**：logsource.go/logfollow.go 新增 `system:` 源——整本 systemd journal（`journalctl [-n N --since X]` / 跟随 `journalctl -f`），所有 systemd 发行版通用，与文件名（syslog vs messages）无关；journalctl 本就在 linux 只读表内，密封路径不变。netdev_log_read 工具描述与 schema 同步（提示模型系统日志优先用 system:main）
- **前端**：日志页/日志工作台的源类型新增「系统日志（全 journal）」且为 Linux 设备的**默认**源（选到 Linux 主机自动切过去），空态快捷片也加了系统日志一键入口；文件源默认值仍按 OS 家系给（上一条已做），作为系统日志之外的手选路径
- **导出带时间选择**（用户建议）：日志页「导出」变为可展开选项——当前显示 N 行 / 最近 1h / 6h / 24h / 今天 0 点以来；时间段导出按所选范围重新走一次密封读取（tail 拉满 1000 行、套用当前过滤词）直接落文件（`netdev-logs-<设备>-<类型>-<范围>.txt`），不要求先加载进查看器。时间选项只对 file/journal/system 源开放（syslog 聚合与 DB 快查询无 since 概念）
- 测试：composeLogCommand/composeLogFollowCommand 新增 system 正反例（含 marker 带元字符拒绝、--since 透传）通过；frontend tsc + vitest（locale-parity/dash-boards）通过

### fix(netdev): centos 服务器日志读取必超时——提示符不识别 RedHat 方括号风格 + ANSI 转义未清洗

用户实测：日志页读取 `/var/log/syslog` 报 `timeout waiting for prompt`，partial output 里命令已执行完、提示符 `[root@honest-fan-1 ~]#` 明明回来了。两个独立问题：

- **提示符形状**：linux-shell 驱动的提示符正则只认 Debian 家系 `user@host:~$`，不认 RedHat 家系（centos/RHEL/Rocky 默认 PS1）的 `[root@host ~]#` 方括号形式——hosts.go 补该分支；driver 提示符 fixtures 测试加 linux 正/反例（含"sigil 后还有正文不是提示符"）
- **括号粘贴序列**：交互式 bash 在回显后发 `ESC[?2004l`、在提示符前紧贴着发 `ESC[?2004h`（同一行无换行），session 引擎用原始 PTY 字节匹配提示符时行首锚点被打断——completed() 与 OpenSession 首提示符等待改为对 ANSI 清洗后的文本匹配（cleanOutput 本就先清洗，不受影响）。新增 TestSessionCentOSBracketedPromptCompletion 复现现场报文（含 tail 报错后提示符返回的形态）
- **路径默认值跟 OS 走**：报错里的 `/var/log/syslog` 是 Debian 惯例，centos 对应 `/var/log/messages`（认证日志 secure vs auth.log）——日志页与日志工作台的文件源默认值、空态快捷片改为按所选设备 OS 家系给出，切换设备时若目标仍是默认路径则跟随切换（用户手输的路径不动）
- **动作按钮合并一行**（用户建议）：日志页「筛选/读取/跟随/交给AI/导出/清空」收进同一行，筛选展开的 行数/起于/过滤 参数单独占行
- 验证：netdev 全量（198s）+ driver 全量 + 新增测试通过；frontend tsc + vitest（dash-boards/live-ops-state/locale-parity）通过

### fix(netdev/browser): 每次 run_skill 泄漏一个 about:blank 页签

用户实测：browser-IT-ops 每执行一次，受控浏览器就多一个 about:blank 页签。两层根因，都已修：

- **switch_tab 重绑遗弃空白页签**：会话attach时 chromedp 首次 Run 会创建一个空白目标页；switchSessionTab 切换页卡时把 s.ctx 换绑到新目标，但原空白页签的 cancel 被覆盖、目标页永远没人关——每次执行留一个。修复：重绑前记录被遗弃目标的 URL，**空白页（我们自建的）在换绑成功后显式 CloseTarget**；真实页面永不被关。附带修复：控制台接管（pickFirstConsoleTab）也不再留空白页
- **switch_tab 开头的流程根本不需要新页签**：RunBrowserFlow 原本一律新建空白页签作会话载体，而 browser-IT-ops 首步就是 switch_tab（目标本就是既有页卡）。修复：首步为 switch_tab 的技能，会话直接重绑到第一个真实页面（技能自己的 switch_tab 再切到命名页卡）；navigate 开头的技能保持新建页签语义（agent 的 navigate 不得劫持用户正在看的页面）
- 验证：builtin 全量（含真实 Chrome 的控制台/接管/判稳测试）+ desktop 全量通过

### fix(netdev/browser): browser-IT-ops 提取到"(AI生成)"占位符 + extract 载荷被报告截断

用户实测：/browser-IT-ops 查询哈尔滨池告警，flow 六步全"成功"但第 6 步 extract 只拿到占位符，模型重试 3 次无果。两处根因，都已修：

- **stable 静态兜底在思考期误判完成**：流式判稳的"自始静止 10 秒兜底"（为重跑已完成块设计）在"模型首字前静默超过 10 秒"的慢后端上把 (AI生成) 占位符当成了完成态——wait 放行时流还没开始。修复：静态兜底分两档——实质性内容（≥40 字符或有渲染子元素）维持 10 秒确认；**短静态内容（占位符形态）要求 30 秒确认**，思考期再长也不会提前放行，真流式出现后仍走 2-8 秒自适应静默路径秒级完成。测试钉住事故场景（占位符静态 26 秒不得放行），既有语义用例（4 秒内不放行/流式边沿判稳/重跑 10 秒兜底）全部保持
- **extract/evaluate 载荷被 400 字符报告截断**：RunBrowserFlow 外层循环对每步输出统一截 400 字符，extract 步在 flowExecStep 里特意保留的 6000 字符上限形同虚设——表格类回答只有前几行能到达模型（"回答到了但模型只看到表头"）。修复：报告截断按步骤类型区分，extract/evaluate 维持 6000、其余 400；面板试运行同样把 extract/evaluate 从 2000 字符显示截断放宽到 6000
- 验证：stable 双测试（含新增事故用例）单独+全量通过；builtin/desktop 全量、前端 tsc 通过（全量偶发失败为真实 Chrome 测试并发争用抖动，重跑即绿）

### feat(netdev/browser): 告警导出技能闭环——等待下载 + Excel AI 研判 + 相对时间窗 + 定时巡检

围绕 browser-cybersituational-awareness（态势感知告警导出）技能的三个缺口：导出 Excel 要 20 秒~5 分钟但流程点完"导出"即结束、时间范围是录制时写死的字面量、没有常驻轮询研判。全部落地：

- **wait download（内核）**：browserWait 新增 `download`（或 `download:.xlsx`）条件——Go 侧轮询 CDP 下载记录直到终态并校验磁盘文件，返回"下载完成: 文件名（完整路径）"；步骤表写法 `| n | wait | \`download\` | 300s |`。下载记录改为**保留式**（drain 只标记 Reported 不丢弃，上限 20 条），修复快速完成的下载被上一步动作摘要清掉、后续 wait 永远等不到的竞态；进入时快照 + 20 秒宽限窗区分"本轮导出"与"陈旧下载"。对话路径（run_skill）的执行报告由此带出文件路径，模型可直接用现成 xlsx_read 分析
- **browserClick 选择器路径切 JS el.click()（内核修复）**：chromedp.Click 经 CDP Input 域派发，运维控制台浏览器环境下实测到不了页面（agent 主走 ref 路径的 JS click 故未暴露；面板/技能的 CSS 选择器点击全部中招）。选择器路径改为 evaluate 执行 scrollIntoView + el.click()（含 checkbox/radio 前后状态核对与"状态未变"告警，对齐 ref 路径契约），命中不到选择器立即返回 "element not found"（isLocateMiss 可识别）。**附带修复链式回退偏慢**：tryFlowAnchors 对 CSS 锚点先做 querySelector 存在性预检（2s 上限，非法选择器/导航中不误判）——坏锚点从烧满 ~40s 动作窗口变为毫秒级失败，在共享 8s 等待预算内轮询后立即回退下一锚点；点击类由 browserClick 内置探针覆盖，type/select 等由通用预检覆盖。**hover 步骤同批改 JS 派发**：flow 的 hover 从真实 CDP MouseEvent（Input 域，同环境不可达）改为 evaluate 派发 pointerover/mouseover/pointermove/mousemove/mouseenter 序列（文字锚与 CSS 锚两条路径）——JS 驱动的悬停菜单（ExtJS/jQuery 组件）生效，CSS :hover 样式需真实指针、步骤输出明示不适用；文字锚点击本就是 JS 派发不受影响
- **元素选择器阶梯重排（修 TestConsoleElementsInteractiveOnly）**：axRowCSS/scanDomCandidates 的选择器阶梯里 `text=` 兜底原本排在祖先作用域与 nth-of-type 结构路径**之前**——带可见标签且同类名不唯一的元素（Naive-UI 双 textarea 场景）会提前落到 text= 锚，而该行的 css 字段被点选/测试当作原生 querySelectorAll 选择器消费，SyntaxError 直接炸。重排为：稳定 id → name → data-ref → aria-label → ExtJS 前缀 → 唯一类 → 祖先作用域 → 结构路径 → **text=（最后兜底，仅当算不出任何唯一 CSS）**；两处头部注释同步。双 textarea 测试页现在产出 `#app > div:nth-of-type(2) > textarea:nth-of-type(1)` 唯一命中，builtin 全量测试转绿
- **时间范围解析（内核）**：新增 `builtin.ResolveTimeRange`——整串严格匹配"最近/近/过去 N 分钟|小时|天"、"last N m/h/d"、"今天/昨天/前天/本周/上周"或已格式化的 `Y-m-d H:M:S - Y-m-d H:M:S`，统一换算为平台 time_range 输入格式；嵌入在长文本里的短语不会被误改写。**惰性换算**：短语在**步骤执行那一刻**才换算（flowSubst/substStepParams 内置、按运行缓存一次保证多步同窗）——时间窗末端是"查询时刻"而非"发起时刻"，浏览器打开/输入/点击的秒级延迟不再让"最近5分钟"卡不准；`TimeRangeBounds` 反解字面范围，供巡检续读锚点
- **AI 研判（桌面）**：`BrowserConsoleAnalyzeDownload(path, analysisKind)`——xlsx（excelize 流式行迭代）/csv 转 markdown 管道表（单元格 120 字符裁剪、6 万字符预算内自适应缩行、截断注明），走 consoleProviderChat（BrowserUse→VLM→当前页签→默认模型链）产出固定结构研判报告。**研判方式三档（巡检普遍化）**：alerts=SIEM 告警研判（概览/需关注告警表/疑似误报/建议动作/失陷主机判定）；generic=通用表格研判（任意导出：清单/日志/监控数据——关键发现/需关注条目/数据质量）；none=仅下载不耗模型不通知。两套结论契约共享解析（失陷主机/关键发现、需关注告警数/条数 归并，`findings()/attention()` 访问器），通知门槛 compromised/attention 对两种研判同样生效；`bindWatchWindow` 容许无时间参数的自包含技能（巡检窗口仅作展示）
- **定时巡检（桌面）**：`BrowserConsoleWatchStart/Stop/State` + "browser:watch" 事件。**墙钟对齐调度**：锚点=启动时刻取整到分钟，或显式配置"巡检时间"（HH:MM，如 22:38 → 网格对齐到 22:43:00、22:48:00…）；第一轮（隐式锚点）立即执行查 `[锚点-间隔, 锚点]`，之后在 `锚点+N×间隔` 的整分刻度触发（`nextWatchFire` 免疫 Ticker 漂移与慢轮偏移），每轮查询**刚闭合**的固定间隔——窗口边界是整分刻度、与执行延迟完全解耦，轮与轮零重叠零空洞。每轮：consoleGate 抢占（忙则记"跳过"轮）→ 绑定字面窗口 → 复用试运行执行器跑技能（无人值守模式拒绝 human/ask 步骤）→ 等下载 → AI 研判 → 按通知策略投递 → 推送轮次结果。**连续覆盖兜底**：`lastEnd` 记录上一轮实际打到页面的窗口终点（onRange 在时间步执行时回填，轮次记录实时显示平台实际查询范围），跳轮/失败后下一轮自动从 lastEnd 续读补漏（向前钳制防重复查询、超 30 分钟封顶防巨型导出）。**持久化**：配置存 `browser_watch.json`，应用重启自动恢复（启动时 resumeBrowserWatch）；最小间隔 60s
- **研判结论 + 通知路由（桌面）**：研判系统提示词新增机器可读结论契约——报告尾部必须输出 ```json 块（失陷主机/需关注告警数/最高等级/需通知/通知理由；失陷主机宁缺勿滥，无确凿证据给空数组），`parseAlertVerdict` 取最后一个 JSON 围栏解析。巡检通知策略四档：**确认失陷主机时（默认推荐）/ 发现需关注告警时 / 每轮完成 / 不通知**；通道复用调度器既有桥：IM bot（schedulerIMPusher→bot gateway，负责人从 bot 最近会话下拉选择或手填 chatID）、邮件（schedulerEmailSender→SMTP 多账号）、系统通知（schedulerNotifier→应用内 toast + OS 托盘）；投递失败记入轮次 NotifyError 不失败轮次。轮次卡新增 失陷×N / 需关注×N+等级 / 已通知渠道 徽标与失陷主机清单。`BrowserConsoleAnalyzeDownload` 同步返回判定字段，试运行研判卡可复用
- **通知投递 UI 重做（前端）**：通知设置改为 68px 定宽标签列 + 控件列的**对齐网格**（控件在格内纵向堆叠，不再挤在一行）；IM 负责人固定渲染下拉（bot 最近会话，手动 chatID 为兜底选项，dest 按 platform:chatType:chatId 组合——QQ 群带 chatType 段，修复之前裸 chatId 推送路由不对的问题）；邮件发件账户下拉读取设置中的邮箱配置（默认账户标注）+ 收件人输入；新增"研判方式"选择（安全告警/通用表格/仅下载），none 时隐藏触发条件与通道设置；轮次卡徽标文案随研判方式切换（失陷×N ↔ 发现×N），运行摘要显示 研判方式·触发条件·IM·账户→收件人·sys

- **面板/编辑器（前端）**：技能编辑器时间类参数（键名含 时间/time/范围/range/日期/date）渲染快捷片（最近5/15/30分钟、1小时、今天）+ 防抖换算预览；试运行终态携带 downloads → 下载卡 + 首个表格文件**自动** AI 研判（Markdown 渲染、失败可重试）。**浏览器面板新增第四个子页签「巡检」（Radar 图标，运行中页签带脉冲活点）**——巡检的一等入口：运行状态头（技能/间隔/整分对齐/下一轮/最近失陷徽标 + 停止）、配置表单（技能下拉 + 共享的间隔/巡检时间/通知投递设置）与轮次日志（状态/时间窗/下载文件/失陷徽标/可展开研判报告与步骤明细）；配置表单抽成共享 `WatchFormFields`，技能页签展开区仍保留按技能快捷配置（启动后自动跳转巡检页）；轮次日志从技能页签底部移至巡检页。bridge 新增方法声明 + dev mock（试运行终态演示携带 mock 下载，巡检轮演示失陷主机与已通知徽标）
- **技能文件修正**（用户级 `~/.fairpeer/skills/browser-cybersituational-awareness/SKILL.md`）：删除误录的第 2 步 `navigate 支持`（回放必失败）；第 1 步时间改 `{{时间范围}}` 参数化（frontmatter `params: 时间范围=最近1小时`）；末尾加 `wait download 300s`；name 规范化修复 U+2011 不换行连字符；draft 保持 true 待实测后 wake
- 验证：timerange/browserflow/watch 单测（短语换算/直通/拒识/download 条件行解析/惰性换算与缓存/对齐窗口与补漏续读/30 分钟封顶/下一刻度计算/研判 JSON 解析（末块优先、破损拒识）/通知策略四档决策）通过；builtin 全量除 TestConsoleElementsInteractiveOnly（用户元素提取改造 WIP 的预存失败，与本批无关）；skill 包全量、desktop 全量、frontend tsc + vitest（locale-parity）+ vite 生产构建通过

### fix(netdev): 设置→运维保存静默丢失——[netdev] 段从未写进 config.toml + 全屏表单挡住反馈

用户实测：添加 Linux 主机后「测试连接」无反应、「保存设备」后设备列表找不到、网络名称也消失。日志显示 17:56 一分钟内 25 次 "SetNetDevSettings saved"（devices=1）全部"成功"，但 config.toml 始终没有 [netdev] 段。两层根因，都已修：

- **后端（根因）**：保存管线 SetNetDevSettings → applyConfigOnly → SaveTo → RenderTOMLForScope——手写渲染器只输出 ui/desktop/agent/bot/lsp 等段，[netdev] 整段没有渲染代码，每次保存返回成功却把运维清单静默丢弃；下次 Load 读回空配置，设备"消失"。修复：render.go 在 user/full 作用域补渲染 [netdev]（从 NetDevConfig 结构体经 toml.Marshal 序列化，字段演进不会再漏；project 作用域永不渲染——该段本就 pinNetDev 钉死在用户配置）。新增 TestSaveToNetDevRoundTrips 回环测试（设备/跳板/分组/站点/预设/DB 源/告警规则/discovery/extra_read 全字段往返 + project 作用域不含该段）
- **前端（反馈被盖）**：测试连接/保存的结果与错误只写页面顶部 err 横幅，而设备编辑表单是 position:fixed 全屏遮罩（z=90）——反馈全部不可见，用户只能反复点击（日志 25 连击即此）。修复：L3Panel 加 banner 属性，六个实体表单内部复述 err；「测试连接」「保存设备」加 busy/testing 防连击（半透明+禁点）；保存成功弹 toast
- **测试成功不再长得像报错**（用户复测反馈）：原成功文案是红色错误横幅里的英文 "[SYS] TARGET VERIFIED (VTY SESSION OPEN)"，被当成故障。成功与失败分离（okMsg 状态 + banner--ok 绿色横幅，L3Panel 支持 bannerOk），中文明示"SSH 已连通、登录成功、CLI 会话正常"（ndv.sets.testOk，zh/en）
- **redfish/snmp 设备无 SSH CLI 驱动**（TestConnection 只拨 CLI，对这两类必败）：设备表单对这两个 vendor 隐藏测试按钮、显示说明（带外 HTTP/SNMP 通道无需测试）；huawei/cisco/zte/vmware/linux/windows 六个 vendor 均有注册驱动，测试连接可用
- **「项目」统一改称「站点」**：内网=整个环境（network_name 为其名，运维标题栏在用，保留），项目（分组集合的过滤视图）与桌面端 coding 工作区的"项目"撞词——zh/en 文案全部改为站点（projectsTitle/newProject/noProjects/editProject/needProjectName/saveProject/tbar 系列），配置字段名不变
- 遗留：密钥库可能残留指向已丢失设备的孤儿密码（secrets.enc.json 先于配置写入）——同名设备重存密码会覆盖同一 env 键，不影响使用；设备名仅允许 A-Za-z0-9_.@-（会进备份文件路径），中文名会被校验拒绝（报错现已在表单内可见）
- 验证：internal/config 全量 + 新回环测试通过；desktop go build 通过；frontend tsc --noEmit + vitest（locale-parity/dash-boards/live-ops-state）通过

### fix(netdev): wait stable 误判"未开始"为"已完成"——AI 流式输出提取到占位符

用户实测：browser-IT-ops 六步全过，但提取结果只有"(AI生成) …"。点发送后输出块立即存在但内容是占位符，模型思考期首字前静默常超 2 秒——静默判稳窗口一过就当"生成完成"，随即提取到占位符。三处修复：

- **stable 加变化沿（rising edge）判据**：签名（匹配数+文本长度+子元素数）必须"出现过或变化过"才适用 2 秒静默判稳；自始静止的内容（重跑时早已完成的块）改用 10 秒静默兜底确认，二者都不满足则等到超时
- **超时不再谎报成功**：load/networkidle/title:/url:/stable 的页面轮询 resolve(false) 原本被丢弃（Evaluate 目标 nil），超时照样回 "waited for"——现在接回布尔值（chromedp Evaluate 加 WithAwaitPromise 选项，否则拿到的是 Promise 对象），超时报"等待 … 超时（Ns）——条件未满足"，让流程失败暴露问题而不是提取占位符
- **wait 单位消歧**：值列按秒解析，>600 的数字按毫秒折算（用户习惯输 15000 意为 15s，原被解析成 15000 秒）；后端 flow 表解析、ConsoleWait、前端 parseSeconds 三处一致
- **参数按实测调优**（用户：AI 生成常需 30-60 秒）：默认超时 15s→90s（空值兜底，全链路一致）；判稳静默窗口 2s→自适应 2-8s（生成持续越久要求尾部静默越长——中途取数/工具调用的 3-5 秒停顿不再误判为完成，长回答也不会被截半提取）；模板 stable 步骤 60s→120s，流式建议值统一 120
- 验证：占位符静态 4 秒内必须超时报错 / 变化后静默判稳 / 静态完整块 10 秒兜底通过 / title 超时报错四断言全过；流程表 15000→15s 折算断言；builtin/desktop 全量 + tsc + vitest（linkpeersignal TestRealIPFromForwarded 为 HEAD 上预存失败，与本批无关）

### fix(netdev): 纯 text= 锚报"not a valid selector" + 名称匹配双向包含——输入框点选输入复活

用户实测：点选输入框后点「输入」直接报 `querySelector: 'text=…' is not a valid selector`。两处根因，都已修：

- **纯文字锚没进锚链机制**：点选兜底会产出 `text=名称`（该行没算出 CSS 时），但输入/点击/悬停/提取四个原语只识别带 `;;` 的链——无 `;;` 的 text= 走了老路径被当 CSS 塞进 querySelector。新增 anchoredTarget 判定（`;;` 链或 text= 开头一律路由锚链执行器）
- **该行为什么没算出 CSS**：聊天页输入框的无障碍名是 aria-labelledby 拼出的"placeholder+装饰尾巴"长串，DOM 侧标签只有短 placeholder——名称匹配的单向包含（标签⊃全名）永远对不上。改为双向包含（反方向标签≥2 字防单字误配），命中后走唯一性校验的选择器阶梯
- 验证：测试页第二个 textarea 改造成 aria-labelledby 长名形态（旧单向匹配下 taCSS 必为空），断言算出唯一命中 CSS；纯 text= 输入落值断言；builtin/desktop 全量 + tsc + vitest 56/56

### fix(netdev): 撤销对位匹配——静默错 CSS 比没有 CSS 危害更大；名称匹配加可见性过滤

用户实测：CSS"完全错了"——按角色顺序对号的启发式在 AX 树（只收可见节点）与 DOM 池（混入隐藏节点）可见性语义不一致时指错元素。错误的选择器会让动作静默落在错误元素上，不可接受：

- **撤销对位匹配**：无名义下不再猜（宁可无 CSS 也不给错的）；点选兜底链变回 css;;text=名称 → css → text=名称 → 编号。无名义下极少且多为图标装饰
- **名称匹配池加可见性过滤**（rect>1px + visibility/display 检查）——隐藏元素即使名称巧合也不参与匹配；名称匹配本身加防重复占用（同名元素按行序领用）
- "元素变少"非本批所致：无障碍树超时降级为 DOM 扫描（行数骤减、汇总行有提示），流式输出结束后重抓即恢复
- 验证：对位断言撤除，名称匹配 + 唯一命中断言保留通过；builtin/desktop 全量 + tsc + vitest 56/56

### fix(netdev): 无名元素对位匹配 + 列表行直显 CSS——eN 从点选路径全面退场

用户再看：点选仍出 e11/e12——无障碍名称为空的元素（图标按钮等）被名称匹配直接跳过，永远算不出 CSS。修复：

- **对位匹配**（axRowCSS 第二遍）：名称匹配（带防重复占用）之后，剩余无 CSS 的行按**粗粒度角色分组 + 文档顺序游标**对号入座——每个角色（button/link/textbox/combobox/checkbox/radio）的未匹配行依次领取该角色中"未占用且无名"的池元素，算选择器。无名义下从此也有稳定锚
- **列表行直显 CSS**：每行右侧新增等宽字体的 CSS 缩略（悬浮看全文）——用户看得见点选会得到什么，不再靠猜
- axRow 增 Role 字段参与匹配；测试页加无名图标按钮（aria-hidden 版会被 AX 树整个忽略——测试自纠）断言对位匹配产出唯一命中
- 验证：builtin/desktop 全量 + tsc + vitest 56/56

### fix(netdev): 选择器阶梯升级 + text= 兜底——深嵌套多同类元素也能算出稳定锚

用户实测：点选 textarea 仍填 e36——CSS 计算在其页面失败（类选择器撞多个输入框不唯一、组件树嵌套 10+ 层超过 6 层路径上限 → 阶梯放弃 → 回退编号）。修复：

- **阶梯升级**（axRowCSS 的 selectorFor）：单 class → **全 class 组合**（tag.c1.c2）→ **最近带 id 祖先的作用域后代选择器**（#id tag.classes）→ 结构路径上限 6→**14 层**；每级都验证"恰好命中 1 个元素"才采用
- **点选兜底链**：css+名称 → `css;;text=名称`；仅 css → css；**无 css 有名称 → `text=名称`**（文字锚同样跨会话稳定）——编号 eN 只在既无 CSS 又无名称时才兜底出现
- 验证：测试页加第二个同类 textarea（模拟用户页面的多输入框场景），断言阶梯仍产出唯一命中正确元素的 CSS，且输入落值在**对的**那个框；builtin/desktop 全量 + tsc + vitest 56/56

### feat(netdev): 元素点选自动填稳定锚链——eN 编号从技能里根治性退场

用户把列表行的 e36 当 CSS 写进技能，试运行报 no snapshot（编号随快照/会话死）。此前我给的裸类选择器也在多输入框页面命中错误元素（用户实测退回 e36）。两层修复：

- **AX 行携带计算 CSS**（browserconsole_axcss.go）：抓元素时把每行（按无障碍名精确→包含匹配）对到 DOM 元素，跑与 DOM 互补扫描相同的选怪阶梯（#id → 唯一 class → nth-of-type 唯一路径），附在行上；**点选时目标栏自动填 `CSS;;text=名称` 锚链**——当场操作与录制技能都是稳定目标，eN 编号只在找不到 CSS 时兜底
- **用户技能第 3 步改作用域选择器**：`.question-input-cover textarea.n-input__textarea-el;;text=有什么我可以帮您吗`——.question-input-cover 限定聊天输入区，避免页面上其他 n-input 输入框抢先命中（我上次裸类选择器的实际问题）；注意事项写明编号与 CSS 的区别
- 连环雷：Go 匿名结构体 JSON 序列化大写键名 → 页面 JS 读 row.ref 全 undefined（诊断日志定位 `{}` 返回）；已改具名 axRow 类型+json 标签
- 验证：TestConsoleElementsInteractiveOnly 断言 textbox 行 css 非空且 querySelectorAll 恰好命中 1 个元素；builtin/desktop 全量 + tsc + vitest 56/56；手册第二节更新（点选填锚链，编号永不入技能）

### fix(netdev): switch_tab 支持按页卡标题切换——用户只认识名字，序号会漂移

用户实跑复盘：技能第 1 步 `switch_tab 1` 切到了 about:blank（序号随页卡开关漂移，1 已不是 AI智能助手），第 2 步点击连锁失败。用户视角只有页卡名字：

- **内核**：browserSwitchTab 的 Target 匹配扩为三级——TargetID（原语义）→ 标题精确 → 标题包含；非数字目标即按名匹配
- **链路**：确定性执行器与面板试运行的 switch_tab 目标列支持"标题或序号"（数字→序号，否则→标题）；新增 ConsoleSwitchTabByTitle 原语；**录制改记标题**（观察窗/面板切页卡时目标=标题、备注=第 N 个）；编辑器步骤提示、手册更新
- **用户技能修复**：browser-IT-ops 第 1 步目标 `1` → `AI智能助手`（按标题匹配），注意事项补说明
- 验证：TestAgentSessionJoinsPersistentBrowser 扩展——给页卡设名后按名切换成功、未知标题报错；builtin/desktop 全量 + tsc + vitest 56/56 + guides-drift 3/3

### fix(netdev): 结构化编辑器认新字段 + 面板/试运行支持 ;; 锚链

用户报两问题：改后的 browser-IT-ops 无法切回结构化模式（"结构化无法保留的内容"）；试运行报 `'a;;b' is not a valid selector`：

- **编辑器前matter 模型补全**：domain（面板域标记）与 draft（草稿标记）进 SkillDoc 解析/序列化/已知字段集——此前这两个新字段让解析器判 lossy 拒绝切结构化。含 draft 的模板技能同样受益
- **Console 原语锚链化**：ConsoleClick/Type/Hover/ExtractAs 检测到 `;;` 即路由确定性执行器的锚链机制（runWithAnchors→flowExecStep）——试运行（它调这些原语）与面板手动操作（目标栏粘 `#css;;text=文字`）从此与技能同语义；单锚目标走原路径零变化
- **连带雷**：flowTypeByTextJS 模板有 4 处 VALUE 占位但只替换前 2（历史编辑引入），第 3/4 处以字面量进 JS 报 ReferenceError——改 Replace(-1) 全量替换
- 验证：TestConsoleElementsInteractiveOnly 扩展——`坏CSS;;text=placeholder` 链路输入落值成功（CSS 侧故意失效，证明 text 锚真正生效）；builtin/desktop 全量 + tsc + vitest 56/56

### fix(netdev): browser-IT-ops 实跑复盘修复——技能文件重写 + 失败尾注不再误导

用户实跑 /browser-IT-ops 查哈尔滨池告警，第 3 步输入落空（目标还是瞬时 e36、文字写死）、第 5 步提取超时（无等待流式、nth-child 长路径依赖录制时的历史结构）：

- **技能文件重写**（~/.fairpeer/skills/browser-IT-ops/SKILL.md）：输入目标改双锚 `textarea.n-input__textarea-el;;text=有什么我可以帮您吗`；问题参数化 `{{问题}}`（调用整句自动绑定）；发送后插入 `wait stable:#image-wrapper > div:last-child div.chat-ai-generated-block 300s`（等内容稳定 2 秒=流式吐完）；提取改锚"最后一个消息组"的 AI 块 + 回退锚任意 AI 块，格式 markdown；description/何时使用/注意事项/验证按实义填写
- **失败尾注去误导**：`会话 br_N 保留（可 browser_* 工具接手排查）` → 运维对话里根本没有 browser_* 工具，模型真的去找了一圈然后开始瞎绕（web_fetch 内网被拒、翻工具列表）。改为指向运维面板浏览器页签 + 建议修步骤表重跑
- 教训沉淀：面板录制 eN 目标 + 无 wait 直 extract 是草稿通病——新草稿模板的注意事项已覆盖（双锚、参数化、stable 等待、markdown 提取），唤醒前试运行即可发现
- 验证：builtin 全量过；exe 重打包重启

### feat(netdev): 对话/技能会话统一走持久受控浏览器——不再弹第二个浏览器窗口

用户需求：对话框里跑技能（如 /browser-it-ops）会另弹一个全新浏览器；期望与运维面板「打开浏览器」同一能力——已开受控浏览器就直接接管（复用人工登录态），没开过才拉起。落地：

- **newBrowserSession（browser_open / browser-flow 技能的汇聚点）改走持久浏览器**：探测固定端口→有实例就接管（会话标注「接管」，人工登录的页卡原样复用）；没有就用持久 profile+端口拉起。原"首个 browser_open 弹独立临时 Chrome"的行为退役（保留 newBrowserSessionEphemeral 备用，当前无调用方）
- **控制台与 agent 会话的分工**：持久逻辑泛化为 persistentBrowserSession(resume)——控制台接管后回到用户正看的页卡（resume）；agent/技能会话落在**自己的新页卡**上，技能的 navigate 永远不会把你正看的页面劫走。多会话=同一浏览器多页卡，观察窗来源芯片各自可见
- 旧 exec-allocator 路径的 CHROME_PATH 检测/重试保留但不再被走（browserlaunch 自带检测）
- 验证：新增 TestAgentSessionJoinsPersistentBrowser——控制台会话开页后 agent 会话必须（a）标注接管（同一浏览器，无二次拉起）（b）落在不同页卡（c）两页卡并存于同一端点；四个浏览器集成测试 + builtin/desktop 全量过；手册第二节补说明；guides-drift 3/3

### feat(netdev/skill): 技能草稿-唤醒工作流 + 面板技能域过滤（browser-skill 出局）

用户定调：生成的技能默认**不唤醒**，面板技能页提供「唤醒」入口，唤醒的才可以在对话用；browser-skill 不该出现在面板列表：

- **草稿状态（frontmatter `draft: true`）**：动作记录草稿与模板生成的技能自带草稿标记。四层闸门：模型技能索引不收录（boot）；run_skill 拒绝并指引"到运维面板唤醒"；slash 直呼返回说明文字；斜杠菜单隐藏。**面板试运行不受限**——试运行就是唤醒前的打磨环节
- **唤醒**：技能行「唤醒」按钮（替代 ▶，未唤醒徽标提示）——BrowserConsoleWakeSkill 只重写 frontmatter 删掉 draft 行，正文逐字节不动；幂等。唤醒即进索引、对话可调
- **面板域过滤**：列表改认 `domain: browser-ops`（模板/生成器新写）或 `executor: browser-flow`——browser-skill（早期录制裸草稿，两者皆无）自然出局；browser-actions/it-ops（有 executor）保留在列
- 生成器（动作记录/4 个确定性模板）写 draft+domain；TS 类型 + i18n（徽标/按钮/提示双语）+ 徽标 CSS；手册第三节改"生成→试运行打磨→唤醒"节奏
- 验证：TestDraftSkillGated（草稿拒绝含指引→去标记后可读）；skill/boot/control/builtin/desktop 全量过；tsc + vitest 56/56；guides-drift 3/3

### fix(skill): [休眠] 标签被模型当成"技能不可用"——三处联动误导，全修

用户实测：让 agent 用 browser-it-ops 查告警，模型看到索引里 `[⚙ 确定性] [休眠] — …请完善描述与选择器`，判定技能未激活、拒绝调用、转去检查配置。三处叠加误导：

- **索引头对 [休眠] 零解释**：[关闭] 有"不可调用"说明、[休眠] 没有，模型只能猜——猜成了阻断态。索引头补条目：休眠=长期未用的索引折叠，**仍可直接调用**（调用即唤醒）；明确禁止因该标签或"描述待完善"字样拒绝/延迟/要求激活，先跑再说
- **从未用过的技能一出生就 [休眠]**：冷判定 includeNeverUsed=true 把无使用记录的新技能全标休眠（boot 注释宣称"新技能不提前退役"，实现却相反）。修复：无使用记录的技能仅在**技能文件 mtime 早于阈值**时才参与冷判定（文件年龄作为"出现时间"代理）——刚记录的技能全文进索引；真正陈年的照样折叠。新增 UsageTracker.HasUsed
- **草稿描述的祈使句进了索引**：`请完善描述与选择器` 是给作者看的 TODO，但描述字段是索引可见的，模型读成了使用前提。改为中性描述："浏览器操作流程（面板动作记录生成），可直接调用；描述与选择器可逐步打磨"
- 验证：TestHasUsed（构造器参数踩坑：NewUsageTracker 收目录非文件路径，测试自纠）；skill/boot 全测过；tsc + vitest 56/56

### fix(netdev/global): 用户自建技能被画像白名单整体隐藏 + 提取支持 Markdown + 草稿模板充实

用户三连报：①browser-actions/browser-it-ops 在对话里看不到；②生成的技能"如何使用/注意事项"太空；③想按 Markdown 结构提取 AI 回答块（如 .chat-ai-generated-block 内的文字）：

- **①根因：索引白名单漏豁免用户技能**。画像白名单（运维只列编码+运维内置技能）在索引层把白名单外的**所有**技能标 ProfileHidden 剔除——包括用户自建的浏览器技能（模型完全不知道它们存在，虽然 run_skill 还能执行）。这与 cowork 画像注释宣称的"白名单只约束出厂技能"矛盾（applyProfileToSkillDisabled 那侧有豁免、索引这侧漏了）。修复：profileHidden 只对 builtinBuiltinSkillNames 名单内的出厂技能生效——用户自建技能在所有画像可见；出厂技能的画像裁剪行为不变
- **②草稿模板充实**：何时使用写明参数化方法（固定文字改 {{参数名}}、调用时 参数名=值）；注意事项补输入步骤参数化、多锚回退链（选择器;;text=文字）、Markdown 提取建议；验证段给具体抓手
- **③提取 Markdown 全链路**：browser_extract format=markdown（browsermarkdown.go：标题/加粗/斜体/行内代码/代码块/链接/有序无序列表/引用/hr 的 HTML→MD 行走渲染）；面板提取框改三态选择（纯文本/Markdown/表格）并记入动作步骤（值列 markdown）；ConsoleExtractAs 内核原语；技能步骤值列 markdown 在解析/序列化/试运行/确定性执行全链路生效；browser_extract 工具描述同步
- 验证：boot 全测（出厂裁剪不变）；TestConsoleExtractMarkdown 集成断言（## 标题/**加粗**/`行内代码`/列表/代码块全部存活）；builtin/desktop 全量 + tsc + vitest 56/56；手册同步

### fix(netdev): AX 树读取超时不再空手——元素列表降级为 DOM 扫描并明示

用户报 `capture accessibility tree: context deadline exceeded`。诊断：整页无障碍树序列化要抢渲染主线程，页面巨大（长对话）或正在流式输出时会被饿到超过 60s 动作超时。加宽超时无意义，降级才是正解：

- **ConsoleElements 降级路径**：captureAXTree 失败时不再整体报错——跳过 AX 半边（refs 不动，宁可没有不用过期），DOM 互补扫描照常填充列表（选择器类目标点击/输入/高亮全可用，仅 e 编号类不可用）；返回结构升级为 `{elements, note}`，降级原因通过 note 上浮
- **面板**：note 显示在元素区汇总行（原扫描汇总位置），手动刷新/看门狗/切页卡三条刷新路径统一消费新结构
- 验证：elements 集成测试断言正常路径 note 为空；builtin/desktop 全量 + tsc + vitest 56/56 过

### feat(netdev): 指针 div 通用纳入 + 面板悬停按钮——组件库伪按钮的最后盲区

用户问两个裸 div（页签条目、头像图标）为何不在元素里，并明示：**能做成通用能力就做，不写特判**。实现为通用启发式（与那两个元素无任何硬编码关联，仅测试夹具同形）：

- **列表通用放宽**：DOM 互补扫描的候选集纳入 div/span（两者同权）——**计算样式 cursor:pointer 且有可读名字**（可见文字/aria/title）即列出（browser-use 标准启发式；指针样式来自样式表也能命中，属性选择器做不到）；无名字的纯图标可点 div 仍不进列表（列表行无可展示身份），用 F12 复制 CSS 粘目标栏操作。防噪前置：div/span 无 onclick/btn-class 时先做包围盒快速过滤再算计算样式
- **text= 锚通用生效**：高亮与流程文字定位的候选池同样纳入指针 div/span——`text=新对话` 这类锚对伪按钮 div 全面可用；高亮求值改宽容类型（布尔 false 与字符串分开判），未命中报中文而非反序列化错
- **面板「悬停」按钮**：目标行新增（输入/点击/悬停并排）——悬浮出菜单的标准操作：先悬停（真实鼠标移动，CSS :hover 生效）再拿元素点菜单项；记入动作为 hover 步骤；补 BrowserConsoleHover 桥接+mock
- 验证：TestConsoleElementsInteractiveOnly 扩展（带文字指针 div 列出、无文字头像 div 不列出、text= 锚高亮通过）；无特判核查（grep 生产代码零站点路径）；builtin/desktop 全量 + tsc + vitest 56/56 过；手册第二/三节更新

### fix(netdev/desktop): 动作记录区净化——撤双按钮与反馈杂文，只留步骤列表

用户看截图反馈：记录为技能出现两个（标题栏 + 末位橙色主按钮），下方杂文字（运行反馈日志）不知何用且丑：

- **撤掉末位橙色主按钮**——记录为技能只保留标题栏一个
- **运行反馈日志行整块移除**——动作记录区现在纯粹是步骤列表（序号 + 摘要 + 悬浮删钮 + 拖拽排序），错误仍走顶部横幅，反馈缓冲保留为有界诊断轨迹不再渲染
- 区块显示条件随之收敛为 history 非空（log 不再参与）；tsc 零错（本批）；vitest 56/56

### feat(netdev/desktop): 动作记录编排五连改——拖拽排序、末位生成钮、技能页让位于配置展开

用户五条反馈落地（第 2 条"切换页卡记成动作"为上轮 switch_tab 的预期行为，确认无需改）：

- **拖拽排序**：历史行 HTML5 DnD（无新依赖），拖动高亮落点（accent 上边线），行光标 grab——修剪不止删除，顺序也可在生成前编好
- **「记录为技能」按钮补末位**：原按钮在头部小图标区不易发现——步骤列表末尾新增全宽主按钮，"在列表最后找按钮"符合直觉；头部按钮保留
- **技能页签隐藏动作记录**：该页空间让给技能本身；技能行点击展开**内联配置预览**（读技能文件解析）：执行方式徽标（确定性/AI 子代理）、参数清单、前 6 步摘要（summarizeStep 同款文案）+ 余量提示；无步骤表的过程式技能显示正文首三行——不进编辑器也能看清配置
- **提取二次加工入册**：动作记录生成的技能草稿注意事项新增"提取结果一般需要二次加工后再使用；extract 放最后一步，输出落在执行报告里便于复制"；手册第二节同步
- i18n +8 键双语；CSS（拖拽态/末位钮/展开预览/配置芯片）；验证：tsc 零错（本批）、vitest 56/56（含 locale-parity）、guides-drift 3/3

### feat(netdev/desktop): 动作记录逐条可视化 + 可删除——记录先修剪、再成技能去编排

用户明确工作流：动作记录 → 逐条删除（去掉点错的）→ 记录为技能 → 到技能页签编排。此前结构化步骤根本没渲染（只显示日志文本行），也没有逐条删除：

- **步骤行渲染**：动作记录区现在显示结构化步骤列表（序号 + summarizeStep 摘要，与技能编辑器同款文案：打开…/输入…/点击…/切换到页卡…），悬浮行尾出现 ✕；运行反馈日志行保留在步骤列表下方
- **逐条删除**：✕ 删该步（其余步骤序号自动重排）；头部清空按钮保留（整份重来）。删完点「记录为技能」，幸存步骤生成草稿打开编辑器编排——一条误操作不再污染整份草稿
- i18n：brc.histDel 双语；CSS：hist-row 悬浮显删钮（默认隐藏降低视觉噪音）；手册三条路表格补说明；guides 同步
- 验证：tsc 零错（本批）；vitest 56/56（含 locale-parity）；guides-drift 3/3

### feat(netdev): 动作记录降噪 + switch_tab 全链路——切页卡是步骤，选择元素不是

用户问：动作记录准确吗？能否记录切换界面？并指出"切一个元素选择都记录"（噪音）。核对后：

- **结构化历史本来就只记动作**（导航/输入/回车/点击/提取）——选择/高亮元素不进历史；用户看到的噪音是操作日志里的自动恢复行（↻ 页面已更新…）——现在自愈完全静默（错误仍上浮），提取增加一行短结果（`提取完成（N 字符）`，内容本体留在提取框供后续使用，不灌日志）
- **switch_tab 新步骤全链路**：观察窗/面板切页卡时自动记入历史与日志（带 1 起始序号+页卡标题）；技能编辑器词汇表（基础操作组）+ skillDoc 解析/序列化/摘要 + 面板试运行（ConsoleSwitchTab）+ 确定性执行器（browserSwitchTab，目标列=序号）+ flowOps 白名单 + 技能生成提示词，全链路贯通；「记录为技能」草稿里值列带页卡标题备注
- 手册第四节补写法；guides 副本同步
- 验证：TestParseFlowTable 扩展 switch_tab 行断言；tsc 零错（本批）；vitest 56/56（含 locale-parity 双语键）；builtin/desktop 全量过

### fix(netdev): textarea 输入报 Illegal invocation——value 原型 setter 不看元素类型

用户实测：在 Naive-UI 的 textarea（e36）输入"你好"报 `TypeError: Illegal invocation at HTMLTextAreaElement`。根因：受控输入的原生 setter 技巧（绕过 React/Vue 劫持）写死了 `HTMLInputElement.prototype` 的 value 描述符，textarea 的回退分支永远走不到（input 的描述符恒存在）——原生 setter 校验收者接口，拿 input 的 setter 调 textarea 即抛 Illegal invocation。两处同病一并修（typeRefJSBody：面板/agent 的 ref 与选择器双路径共用；flowTypeByTextJS：流程文字锚）：按 `tagName === 'TEXTAREA'` 选对应原型再取 setter

- 验证：TestConsoleElementsInteractiveOnly 扩展——对 textarea 行执行 ConsoleType 后从页面实读 `textarea.value === "你好"`（在会话页卡上下文里读，避免读到空白页）；builtin 全量过

### fix(netdev): img 伪按钮进列表（DOM 可点击互补扫描）+ CSS 目标高亮 undefined 修复

用户在 Naive-UI 站点实测两元素：textarea 输入框列表里没有、发送按钮（`<img>`）也没有。核查：

- **textarea 其实一直在**（实证：Chrome AX 树把 `<textarea>` 报为角色 **textbox**，与 input 同词，角色表本就收录）——之前"看不到"是旧列表被文本节点灌满（200 截断）+ 同页不刷新的叠加；上一轮过滤修复后已在列表中（角色列显示 textbox）。角色表仍补了 "textarea" 拼写以防其他 AX 方言
- **img 按钮是真缺口**：Vue 把点击监听挂在 JS，`<img>` 无角色无名字——AX 树对"图片做的按钮"天生失明，深度扫描选择器也不含裸 img。新增 **DOM 可点击互补扫描**（browser-use 启发式）：可见 + (img/svg/onclick/class 含 btn 或 button/contenteditable/tabindex) + 指针为 pointer（仅限 img/svg/tabindex，普通 div 的 pointer 是悬停噪音）；每行生成稳定选择器（#id → 唯一 class → text= 锚 → 唯一 nth-of-type 路径，拿不到唯一选择器就跳过），以 selector 作 ref 进列表（与深度扫描行同约定），点击/输入/高亮全可用
- **潜伏雷 3（CSS 目标高亮 undefined）**：高亮 CSS 路径的外层 IIFE 没有 return——表达式恒为 undefined，chromedp 报 "encountered an undefined value"；此前被前端吞错掩盖，上轮错误可见化后暴露。修为 `return (BODY)`
- 验证：TestConsoleElementsInteractiveOnly 扩展为同形页面（静态段落+textarea+img 按钮）断言 textarea（textbox 角色+placeholder）与 img 选择器行都在、逐行高亮零报错；builtin/desktop 全量过

### fix(netdev): Vue/Naive-UI 站点元素列表混入非元素节点——高亮报 scrollIntoView is not a function

用户报：某 Vue3 + Element-Plus 站点抓不到元素，高亮报 `TypeError: this.scrollIntoView is not a function at Text.<anonymous>`（还有 `at HTMLDocument.<anonymous>`）。错误栈直接给出根因：

- **根因**：快照 ref 的发放规则是"可交互角色**或带名字的节点**"（AI 读页面内容需要静态文字有 ref）——文本节点（名字=文字内容）和文档根节点（名字=页面标题"AI智能助手"）都拿到了编号；而 ConsoleElements 把整个 ref 表倒进面板列表没过滤。Text/HTMLDocument 没有 `scrollIntoView`/`click`，一点就炸。Naive-UI 这类重文本站点列表几乎全是文字节点
- **修复 1（治本）**：面板元素列表只保留可交互角色（rolesWorthRef 提为包级 axInteractiveRoles 共用）——button/link/textbox/combobox/option/tab 等 19 种；文字节点/文档根不再出现在选择器里（AI 侧快照不变，仍能读内容）
- **修复 2（防御）**：高亮/点击 JS 加节点类型防御——`this.nodeType !== 1` 时升到父元素（Playwright 文本选择器语义：点一段文字=点它所在的链接/按钮）；无元素父级则返回明确错误（高亮：「不是可交互节点」；点击：「解析到不可点击的节点…请换编号或用锚点」）而不是裸 TypeError
- 验证：新增 TestConsoleElementsInteractiveOnly——构造同形页面（静态段落+链接+按钮+输入框），断言列表零非交互角色、逐个高亮全部成功；builtin/desktop 全量过

### fix(netdev): 「关闭浏览器」真正关闭整个受控浏览器——持久化后 UI 失去关浏览器的入口

用户问：观察窗的关闭按钮是不是应该关整个受控浏览器？澄清三级语义并补上缺失的一级：

- **语义分层**：观察窗右上角 ✕（= Esc/对话页签）只关观察窗**视图**，与其他工作台一致，保持不动；面板「关闭浏览器」才管浏览器。但持久化改造后它退化为只断开连接——**整个 UI 再也没有能真正关掉受控浏览器的入口**（只能手点 Chrome 窗口的 X）
- **修复**：ConsoleClose 对自有持久浏览器（ownsBrowser，拉起或接管两种来路）执行**优雅关闭**——向浏览器级 CDP WebSocket 发裸 `Browser.close`（chromedp 公共执行器拦截该命令并建议 chromedp.Cancel，而 Cancel 对远端分配器只是断开，故绕行走裸帧）：全部页卡+进程退出、profile 先落盘（登录态无损，下次打开重新拉起并恢复页卡）。显式 cdpURL 附着的外部浏览器永不置 ownsBrowser——用户自己的浏览器只断开不关闭
- 文案：面板按钮「关闭」→「关闭浏览器」（原文案歧义）；手册第二节补「关闭层级」说明
- 验证：接管测试新增第五阶段——ConsoleClose 后固定端口在 8 秒内停止应答（浏览器真关了）；builtin 全量 + vitest 56/56 + mirror 脚本 22 断言过

### fix(netdev/desktop): 观察窗幽灵来源芯片——会话结束未清理画面桶

用户问：观察窗的「agent br_1/br_2/br_3/控制台」是什么、为什么删不掉。它们是画面来源切换器（观察任一浏览器会话的截图），但会话关闭后画面桶不清理，死会话残留成不可移除的幽灵芯片：

- **根因**：内核在会话关闭时发 `status/end`（带 session_id），前端镜像存储的 end 分支只清 running 标志、不删会话桶——死会话一直挂到 8 个上限按新旧挤出
- **修复**：end 帧现在删除对应会话桶（聚合画面仍保留最后一帧，历史行为不变）；观察窗正在看的 agent 会话结束时自动回落到「控制台」视角，不再停在空状态
- **语义澄清**：来源芯片 = 当前活着的浏览器会话（控制台 = 手动驱动的会话，agent br_N = 对话里 AI 开的自动化会话）；会话结束芯片即消失，无需手动删
- 验证：browser-mirror 脚本测试补 4 断言（桶存在→end 删除→聚合画面保留）全过；tsc 零错；vitest 56/56

### fix(netdev): 接管后状态/预览不可用收尾——首帧直推 + 「受控制」提示条回归 + 接管会话端到端测试

用户报：接管浏览器后状态不同步、预览看不到、「浏览器正在受控制」提示条消失。诊断：

- **状态/预览不可用 = 上轮已修的 attach 启动即断连 bug 的症状**（连接死 → Location/截图全失败）。本轮在接管测试中补齐端到端验证防回归：接管会话上导航 → ConsoleStateOf 可读（状态同步）→ 测试 sink 收到带会话号的镜像帧（预览管线）。四阶段全链路：拉起→关会话→浏览器存活→接管且**真的能用**
- **接管完成立即推一帧**：接管落点是你正在看的页面，侧栏预览此前要等第一次动作才有画面——现在 newConsoleBrowserSession 接管分支直接 mirrorAfterAction，预览即时出现
- **「浏览器正在受控制」提示条回归**：持久浏览器启动参数去掉过自动化痕迹，但用户依赖这条提示区分受控浏览器与日常浏览器，消失被读作"同步坏了"——加回 `--enable-automation`（有意保留的可见线索）
- 验证：接管四阶段测试（3.3s 真浏览器）+ keepalive 测试复跑 + builtin/desktop 全量过 + tsc 本批零错

### fix(netdev): 保活 ping 升级为全页卡心跳 + 连环修复 attach 路径两枚潜伏雷

用户问：保持会话是保所有页卡吗？——不是，原来只作用**当前页卡**（跟随切换）。多页卡多站点场景（页卡1 安全平台 + 页卡2 监控系统）下另一个站登录态没人管，确为缺陷：

- **ping 改为全页卡心跳**：每个打开的 http(s) 页卡各发一次同源心跳（Cookie 按站点隔离，各站各自续命）；chrome:// 新标签页等非 http 页卡跳过（无可保会话、同源 fetch 也发不出）。个别页卡失败=部分降级（记状态行错误、其余照常、仍算刷新），全部失败才算保活失败。navigate 保持**仅当前页卡**（自动刷新所有页卡会冲掉未保存表单）；local 不变
- **生命周期安全**：每页卡一个心跳 CDP 会话，用 `context.WithoutCancel` 从会话上下文剥离（chromedp 取消页卡上下文会**关掉该页卡**，且会话拆除会级联）——只在页卡自身消失后清理；页卡增减每 tick 自适应（新建即纳入、关闭即剪除）
- **潜伏雷 1（attach 启动即死）**：newAttachedSession 的启动探测用 `WithTimeout+defer bootCancel`——chromedp v0.15 首次 Run 把浏览器连接生命周期绑在所收 context 上，bootCancel 一执行整条连接静默死亡（attach 后任何动作报 "context canceled"）。改 goroutine+外部超时（newBrowserSession 同款模式）。**影响此前所有 attach 路径**（browser_attach 工具 + 本轮持久浏览器接管）
- **潜伏雷 2（attach 空指针）**：attach 会话漏初始化 stepTracker，任何带 wrapResult 的动作直接 panic。补 `newStepTracker()`
- 工具描述/手册第七节更新（ping=全页卡、navigate=仅当前页卡、local=仅防回收）
- 验证：新增 TestKeepAlivePingAllTabs（真实浏览器双页卡两次 tick，逐页卡断言 `http 200`）+ TestConsoleBrowserSpawnAndTakeover 复跑；测试改自管临时目录带重试清理（Windows 杀进程后异步释放 profile 句柄，t.TempDir 立即清会假失败）；builtin/desktop 全量过；tsc 本批零错；guides-drift 3/3

### feat(netdev): 控制台浏览器持久化——重启 fairpeer 自动接管上次浏览器（页卡+登录态）

用户痛点：每次重开 fairpeer 都要重开浏览器、重开网页、重新登录。方案：控制台浏览器改为**持久实例**（固定 profile + 固定 CDP 端口 9333，避开协作托管浏览器的 9222）：

- **打开即接管**：ConsoleOpen 先探测 9333——上一次 fairpeer 退出后存活的浏览器（browserlaunch 用裸 cmd.Start，进程本就不随 fairpeer 死）直接接管，页卡/登录态原样保留，落点拾取第一个真实页卡而非空白页；会话栏显示「Chrome (接管)」
- **不在则拉起**：用持久 profile（`~/.fairpeer/console-browser`）+ `--restore-last-session` 拉起——登录态由 profile 恢复，上次页卡由 Chrome 会话恢复；本次运行的 Handle 只记录、**永不 Close**（浏览器必须活得比 fairpeer 久）
- **关闭语义安全**：持久会话沿用 attached 语义——ConsoleClose/空闲回收/fairpeer 退出都只断开 CDP 连接，浏览器留着等下次接管
- **降级**：固定端口被非浏览器进程占用或 profile 被僵尸进程锁死时，退回旧的临时浏览器行为（报错说明两个失败原因），不硬失败
- 代理 session 的 browser_open 不受影响（仍是临时 profile）
- 验证：TestConsoleBrowserSpawnAndTakeover 真实 Chrome 全链路（拉起→关会话→浏览器存活→重开接管且不重复拉起），隔离端口+临时 profile，泄漏清零（进程级核查 0）；builtin 全量过；tsc 本批零错；guides-drift 3/3；手册第二节更新

### feat(netdev): 流程定位等待默认化 + 重 JS 站点标准搭配进模板/手册

用户指出深层问题："CSS 加载了我们收到了，但按钮的 JS 还没绑好"——元素可见 ≠ 事件已绑。取舍决策：**"等出现"自动化（安全），"等绑好"靠代理信号显式写（没有可靠探针），"没生效就重点"永不自动化（双重提交风险）**：

- **内核：定位等待**（tryFlowAnchors）：click/type/hover/select/extract 的目标没渲染出来时，不再立刻失败——按 300ms 轮询**最多 8 秒**（整个锚点链共享预算），出现后执行一次动作。安全性由 isLocateMiss 严格分类保证：只重试"目标不在页面上"类错误（这些错误在动作派发前抛出，重试不可能双击）；动作已执行/配置错误/ref 失效类立即走下一锚点。等过才成功的步骤在报告里提示「目标 X.Xs 后才出现——建议在步骤前加 wait visible:」（慢站点的可观测性）。文字锚路径此前完全没有重试，CSS 路径仅 ~1.5s 退避，现在统一
- **模板**：browser-form-submit / browser-stream-query 内置标准搭配——navigate 后 `wait networkidle`（JS 装载执行完）、提交按钮 `:not([disabled])`（等应用宣告就绪，比可见更可靠）、点击后 stable:/效果等待
- **手册第四节**：新增「定位自带等待」与「重 JS 站点标准搭配」三条写法；guides 副本同步
- **明确不做**：点击后检测"没生效"自动重点——无法区分延迟生效与未生效，双击即双提交（与"点击不自动重试"既定原则同源）
- 验证：TestIsLocateMiss 10 用例（定位类可等 / 动作类不可等 / nil）；go build + builtin 全测过；tsc 本批零错（nmap 报错仍为并行会话半成品）；vitest 56/56 含模板重解析

### fix(netdev/desktop): ref 失效报错说人话 + 高亮失效按 角色/名称 自动找回

用户反馈：`高亮 "e6": No node with given id found (-32000)`。这是 ref 失效的第三种形态——网址没变但页面内容变了（SPA 局部刷新/轮询表格/DOM 替换），URL 看门狗比对发现不了：

- **源头友好化**（browsersnapshot.go resolveRefToObjectID）：`dom.ResolveNode` 的裸 CDP -32000 映射为「ref %q 已失效——页面内容在快照后变化了（网址未变）；重新获取元素后再选」——所有 ref 消费方（高亮/点击/输入/hover/flow）一并受益
- **绑定层兜底**：localizeConsoleErr 补 -32000 映射（防其他路径冒裸错），并把它套到 BrowserConsoleHighlight 上（此前只有 click/type 有）
- **高亮自动找回**（面板层）：点选 ref 行高亮失败时不再只报错——自动重抓元素列表，按**角色+名称**匹配同位元素，用新 ref 重新框选并更新目标栏，日志记「↻ 页面已更新：登录 编号 e6 → e9，已自动重新框选」；找不到才落横幅。高亮可以安全自动重试（框错立即可见，不像点击是破坏性动作——维持点击不自动重试的既定决策）
- **附带修复**：CSS/text= 锚点未命中时高亮原先**静默无操作**（JS 返回 false 被当成功，预览不动像"坏了"）——现返回「页面上找不到目标 %q（CSS/文字锚点未命中）」
- 验证：TestLocalizeConsoleErr 4 用例（-32000/无快照/未知 ref/透传）；go build + builtin 全测 + desktop 全测过；tsc 本批零错（nmap 报错仍为并行会话半成品）；vitest 56/56

### feat(netdev): netprobe 编排落地（P0-2 中期）+ 红队批立项决策文档

- **netprobe 产品侧编排**（netprobeorch.go）：`cmd/netprobe` 二进制早已存在（TCP 探测 + raw ICMP），但产品只会报错说 "use netprobe" 而没有任何代码跑它。补上编排：`Manager.NetprobeSweep` + agent 工具 `netdev_netprobe`——闸门与 nmap 同级（engagement 信封 → scopes 白名单），主机预算走 `max_hosts_per_job`（默认 65536 = 一个 /16，不占 tunnel 的 4096 上限），二进制 `netprobe_path` 指定或 PATH 查找；存活主机回填待确认区（`source: netprobe`，仅 ICMP 存活的主机为无端口行），单条滚动 info 汇总 + 审计。隧道探测覆盖不了的 ICMP 存活与 /16 级网段由此闭环；UDP 探测留后续
- **红队批决策文档**（docs/REDTEAM_BATCH_DECISION.md）：不写任何攻击代码——把附录 C 三要素启用门槛（engagement 红队专用信封 / profile 显式配置 / 重启生效）、三档范围选项（R1 只读侦察编排 / R2 无害验证 / R3 利用链编排）与四个拍板前必答问题成文；当前结论维持"靶场阶段不立项"
- 验证：netdev+config 全量绿（新增 TestNetprobeSweepGates：无信封拒 / 越界拒 / /16 过闸到达二进制查找三断言）

### feat(netdev/desktop): 元素列表随页面跳转自动刷新——URL 看门狗 + 导航即时重抓

用户问："切换网页的时候元素列表是否也需要刷新？"——需要：ref 是快照态的，页面一跳全部作废。此前只有切页卡会刷新（工作台广播事件），普通跳转不刷：

- **URL 看门狗**（面板层）：会话打开期间每 3 秒读一次实时网址（ConsoleStateOf 本就实时查 Location），与元素列表抓取时记录的网址不同 → 静默自动重抓（清目标/清扫描汇总）。覆盖全部跳转来源：地址栏前往、点击跳页、后退/前进、以及用户直接在被控浏览器里手动浏览
- **导航即时刷新**：地址栏「前往」成功后立即重抓，不等 3 秒轮询
- **架构归一**：元素列表生命周期（refreshElements/runDeepScan/scanSummary）从 InteractSub 上提到面板层统一管理；手动刷新按钮走 runAction（转圈+报错上浮），看门狗/切页卡/导航后走静默变体（不闪 busy、不刷日志）
- 手册第二节同步（条目 5/6 重排）；guides-drift 3/3；tsc 本批文件零错（NetDevLayout 的 nmap 报错为并行会话半成品）；vitest 56/56（17 个 no-suite 预存在）；vite build 绿

### feat(netdev): 残留批——nmap 服务探测编排 v1（P1-1）+ 滚动汇总 + 双立案防护

终检残留清单处理（PENLAB_CAPABILITY_GAPS）：

- **nmap 服务探测编排 v1**（P1-1，nmaporch.go）：产品编排用户自备的 nmap（`[netdev.discovery] nmap_path`，空则 PATH 查找；缺失报安装指引——编排外部工具，绝不内置扫描引擎）。闸门与弱口令核查同级：engagement 信封 → scopes 永不可关白名单 → 单 CIDR ≤4096 主机。`-Pn -sT -sV --version-light --open -oX -` 连接扫描免 root；XML 解析（product/version），结果经 `RecordDiscoveredPorts` 回填待确认区（Parsed 直载指纹，纳管即进 CVE 匹配）+ 单条滚动 info 汇总 + 审计。agent 工具 `netdev_nmap` 同步注册（ReadOnly=false，实况面板可见拒绝）
- **UI 入口**：发现弹窗新增「nmap 服务探测」按钮（NetDevNmapSweep 桥 + 结果卡 + 直达待确认区），i18n 双语
- **滚动汇总**（残留①）：巡检/基线的 info 级汇总发现 Source 化（`inspect:summary` / `baseline:summary`），`SaveRollingFinding` 每次运行原地更新同一张卡（ID/首次时间保留）；历史在巡检日志。基线 reconcile 显式排除 summary 卡（否则会被自己"不再命中"逻辑误 resolve）
- **双立案防护**（残留②）：`netdev_assess` 确认弱口令的回话明示"已自动立案，勿再调 netdev_finding"
- **测试竞态修复**：提案完成复核 goroutine 活过测试边界、把滚动汇总写进下一测试的临时发现目录（TestWatchDegradationRaisesFinding ~1/4 概率挂）——加 `proposalAutoRecheck` 测试缝，两个 Manager 测试助手统一关闭
- 验证：netdev+config 全量 3× 绿（新增 TestParseNmapXML / TestNmapSweepGates / TestRollingSummaryFindings）；前端 tsc 零错、vitest 56/56；desktop 模块 build 绿

### tune(netdev): 深度扫描体验收紧——预算 12 屏 / 上限 300 元素 / 渐进等待

用户拍板："要限制，不能无限滚动；元素太多也慢，体验不好"。按"宁少勿慢"收紧：

- **预算 20→12 屏**（面板传入）：最坏 ~4-6 秒封顶；无新增/到底仍会提前停
- **元素上限 400→300**：超出即停并如实上报（`cap`）
- **渐进式沉淀等待 150→350→650ms**：替换原固定 350ms 轮询——多数信息流首个 150ms 检查即稳定，快页面翻屏成本减半；慢信息流逐步放宽，2.5s 封顶不变
- **扫描中即时反馈**：点击后汇总行先显示"深度扫描中…（最多 12 屏）"，结束替换为结果汇总——不再只有按钮转圈
- 验证：go build + 合并单测 + 三场景无头实测（长静态/有界瀑布/真无限流全部按预算终止、scrollY 复位）+ vite build 绿 + vitest 56/56（17 个 no-suite 预存在；tsc 报错为并行会话的 nmap 半成品，与本批无关）

### fix(netdev): 弱口令命中真正立案——补上闭环最后一块断板（终检发现）

P2 批终检发现：`WeakCredCheck` 只返回结果、从不落 Finding，而 UI 结果卡的「查看发现」跳转、总览弱口令计数、转修复提案流程全都消费发现队列——命中弱口令后闭环在临时结果卡处断掉。修复（assess.go）：

- 确认弱口令即立案 critical 发现（Source `assess:weak-cred:<tier>:<device>`，凭证原文永不入证据/日志）；再次确认**更新同一告警**不堆积（复用 ID 与首次时间，同基线 reconcile 语法）
- 全预算复查通过时**同档自动 resolve**（"复核通过，自动恢复"）；档位编进 Source——basic 档通过不会误恢复 dictionary 档的告警
- 验证：go build 全绿；netdev 包 3 次全量复跑全过（新增 TestWeakCredFindingLifecycle：立案/去重/同档恢复/异档不误恢复）

### feat(netdev): 元素深度扫描——长页面全覆盖确认 + 瀑布流滚动收集（带停止条件）

用户问：很长的网页能否拿到所有元素？无限流瀑布怎么办？分两类回答：

- **长静态页：本来就能**——元素列表取的是整页无障碍树（含视口外），点击/高亮自带 scrollIntoView；实测 40 屏高页面的底部链接直接可选中
- **瀑布流/懒加载：定义上拿不到"全部"**（内容滚动前不存在于 DOM）——新增「⇊ 深度扫描」：逐屏下滚（0.85 视口/步）收集**当前屏**新物化的可交互元素，等 scrollHeight 稳定后再滚，锚点去重合并，滚完回原位。停止条件四选一并如实上报：`no-new`（连续 2 步无新增）/ `bottom`（到底）/ `max-scrolls`（滚动预算，面板 20 屏）/ `cap`（400 元素上限）
- **稳定锚点而非 ref**：ref 随快照失效，滚动收集的行必须滚回后仍可点——每行产出 `#id` / `tag[name=…]` / `text=可见文字` 锚点，与目标栏/技能步骤的多锚词汇完全一致；`ConsoleHighlight` 相应支持 `text=` 锚点（按可见文字在可交互元素里匹配后框选）
- **前端**：元素区头部新增 ⇊ 按钮（与刷新并列）+ 汇总行（`深度扫描：5 屏 · 214 个 · 无新增，已停`）；扫描结果行点击即框选/填目标；普通「刷新元素」回到 ref 模式并清汇总
- **验证**：无头 Chrome 三场景实测（长静态页 23 锚点到预算停；有界瀑布流 226 锚点；真无限流按预算终止不挂起；scrollY 复位 0）——注意无头环境不派发 IntersectionObserver/scroll 事件（已实测证实），瀑布流用 scrollBy 补丁模拟物化，真实浏览器物化由 IO 机制保证；mergeScanElements 纯函数单测（去重/首见序/newInLast 信号）；go build + builtin 全测 + tsc + vitest 56/56（17 个 no-suite 为预存在脚本文件）+ guides-drift 3/3；手册第二节同步更新

### fix(netdev/desktop): 元素高亮从未生效——callOnRef 的 IIFE 格式 this=undefined（实测证实）

用户反馈：右侧栏「元素」里点击 link，预览画面不框选对应元素。实为内核 bug，非能力缺失：

- **根因**：`ConsoleHighlight` 与 `hoverPointForTarget` 的 ref 路径把 JS 写成 IIFE `(function(){...})()` 传给 `Runtime.callFunctionOn`——CDP 解析时将其包成 `((...)())` **在解析阶段立即执行**，`this` 为 undefined，所有 `this.xxx` 全部 TypeError。无头 Chrome 实测复现：纯函数声明 `function(){...}` 正常（this=元素、副作用落地），IIFE 格式必抛异常
- **波及面**：面板元素 hover 闪框、点击持久框选、browser_hover/flow hover 的 **ref 目标**路径自上线以来全部静默失败（CSS 路径经 chromedp.Evaluate 不受影响）；前端 `.catch(() => undefined)` 吞错，用户只见"没反应"
- **修复**：两处 JS 改为纯函数声明（`function(){ ...this... }`）；hover 探针改返回对象由 `WithReturnByValue` 序列化；其余 8 个 callOnRef 调用点（click/type/select/upload/occlusion 等）核查均为正确格式，未受影响
- **前端不再吞错**：元素**点击**高亮失败现走上报横幅（提示"先重新拿元素"——ref 是快照态，页面变了属正常指引）；hover 闪框保持静默（每行悬停都弹横幅是噪音）
- **验证**：无头 Chrome 实测——持久标记 `marked`/类名落地/样式注入/截图含 590+ 橙色描边像素/再点 `cleared` 切换正常，hover 返回坐标 `{"x":40,"y":20}`；go build+vet（uia/screen 的 unsafe 警告预存在）+builtin 测试过；tsc 零错；vitest 56/56

### feat(netdev): 提案驱动复核（P2-3）——基线发现 Source 生命周期 + 完成即自动复跑

PENLAB_CAPABILITY_GAPS 最后一项前端外缺口，「识别→修复→复核」闭环三段全部焊死：

- **基线发现接入 Source 生命周期**（baseline.go）：每条违规发现的 `Source = "baseline:<rule>"`；新增 `reconcileBaselineFindings`——同一规则重复核查**更新同一告警**（复用 ID 与首次立案时间，不再堆积"基线：…"副本）；规则不再命中且旧告警的全部设备都在本次受检集合内时**自动 resolve**（附"复核通过，自动恢复"说明）。定向复核（只查部分设备）不会 resolve 未复核设备的告警——盲区保护
- **提案完成钩子**（proposal.go）：ExecuteProposal 全部步骤成功进入观察期时，自动对提案涉及的设备跑 `RunBaselineFor`（新增的设备限定版基线核查，空=全网），结果入审计；WatchNote 标注"已触发自动基线复核"。修复是否生效由数据说话：命中刷新原告警、不再命中自动恢复
- 验证：go build 全绿；netdev 包全量测试过（新增 TestReconcileBaselineFindings：重复命中复用 ID / 修复自动 resolve / 定向复核不误恢复三断言）

### feat(netdev/desktop): 靶场 P1-2/P2 批——纳管指纹回填 + 发现一键转修复提案

PENLAB_CAPABILITY_GAPS 剩余前端可落地项：

- **纳管指纹回填**（P1-2）：`NetDevPromoteForm` 增 `model` 字段——待确认区线索纳管时前端把 banner/HTTP 指纹浓缩成 `product + version`（如 "OpenSSH_9.6" / "nginx 1.24.0"）预填设备 Model，CVE 匹配的 vendor+os+model 文本面从纳管第一天起可用；待确认区行内同步显示指纹摘要徽标（悬停说明用途）
- **发现一键转修复提案**（P2-1）：FindingRow 未解决态新增「转修复提案」按钮——起草提示词带上发现 id/标题/设备跳对话，走既有 netdev_propose 人工审批流，护栏语义不变；「识别→修复」两段就此焊死
- 注：P2-2 弱口令核查 UI 由同期「巡检家族」左栏菜单落地（全网 basic/strong 档），未另设重复入口
- 验证：go build 全绿；前端 tsc 零错、vitest 56/56、vite build 绿

### feat(netdev): 安全导入两补——CVE feed 收 NVD 原生导出（1.1/2.0）+ 案例 IOC 台账批量导入

承接"安全告警导入场景"审计结论：CVE 导入是唯一数据入口但只认简化格式（规范 §4.5 写明"导入器支持 NVD JSON 导出"）；IOC feed 导入是 §4.6 点名来源但只有手工逐条。落地：

- **NVD 原生导入**（cve.go）：`ImportCVEFeed` 格式嗅探——`{"cves":[...]}` 简化格式直存、`{"CVE_Items":[...]}`（legacy 1.1 feed 导出）与 `{"vulnerabilities":[...]}`（API 2.0 响应导出）转换为简化条目后缓存，匹配/扫荡逻辑零改动
  - 转换规则：CPE 2.3（`cpe23Uri`/`criteria`）取 vendor + product（下划线转空格，`ios_xe`→`ios xe`，对齐清单 vendor+os+model 文本匹配面）——通配符/NA/`vulnerable:false` 过滤，单条目产品去重上限 12；severity 取 V3.1→V3.0→V2 归一小写；描述取首个 en 值截断 500 字；无 CPE 条目丢弃（无法匹配）
  - 前端 feed 占位文案更新为"简化格式或 NVD 原生导出均可"
- **IOC 批量导入**（新 `src/lib/ioc.ts` + SecWorkbench 台账区）：组标签行新增「批量导入」开关——粘贴清单（每行一个，IP/IPv6/域名/md5/sha256/关键词类型自动识别，`#` 后为备注、`#` 开头行跳过）并入当前案例台账；大写哈希可识别
- i18n：+5 键双语（ndv.sec.iocBulk* / phIocBulk）；phFeed 双语更新
- 验证：netdev 包全量测试过（新增 TestNVD20Import / TestNVD11Import / TestCPEProducts）；tsc 零错；sec-ioc-import 8/8；locale-parity 2/2；vite build 绿（未提交 git）


### fix(netdev/desktop): 界面审计修复——浏览器工作台上下 60/40 生效 + F12 时间戳 + 断行/空态打磨

用户要求全面体检界面（"你查查目前的界面是否不够出色？"）。用 scratch/uishot 无头截图（dev mock 深链 `?profile=netdev&dock=browser` / `&bench=browser`）+ 视觉模型逐区评审，实证修复：

- **关键：工作台分栏方向**：`.ndv-wb__body` 漏 `flex-direction: column`，镜像与 F12 实际左右并排（违背"上半 60% 画面、下半 F12"的需求原话）——补 column 后实测几何 60.1%/39.9% 上下堆叠
- **深链**：`benchParam()` 白名单漏 `"browser"`，`?bench=browser` 深链静默落回对话视图（截图工具与深链均失效）
- **F12 列表**：日志/网络行新增 `HH:MM:SS` 时间戳列（`time` 字段双端本就有，仅未展示）；`word-break: break-all` 改 `overflow-wrap: anywhere`，英文报错不再从单词中间断开（"x is n/ot a function"）
- **分区观感**：镜像空态底色 `#fff` → `var(--bg-soft)`（不再一大片刺眼纯白），F12 列表 `var(--bg-elev)` + `--border` 描边，上下两区边界可辨
- **会话栏**：加 `flex-wrap: wrap`，窄侧栏下按钮换行而非压扁浏览器名
- **dev mock**：`BrowserConsoleScreenshot` 原返回残缺的 `data:image/png;base64,`（真值但无图，<img> 空白且空态永不触发）——改为内置绘制的运维门户 SVG 帧，dev 模式/截图审计下镜像真实可见
- 审计排除项：右栏页签收缩截断为共享 Chrome 式页卡行为（容器查询+渐隐，有意设计）；输入框红色占位符经放大复核为普通灰色（视觉模型误报）
- 验证：tsc 零错；vitest 49/49 过（17 个 tsx 脚本式文件报 no-suite 为预存在）；无头截图 + DOM 几何实测复核

### feat(netdev): 靶场 P0 批——计划卡就地扩围（动态 scopes）+ 大网段自动拆分

PENLAB_CAPABILITY_GAPS P0-1/P0-2 落地，黑盒多层递进（拿下一层 → Precheck 发现新内网网段 → 继续探活）跑通：

- **计划卡就地扩围**（P0-1）：Precheck 计划卡对不在 scopes 白名单内的网段逐条标「越界」徽标并汇总提示；「加入探测范围」按钮经确认后调新桥 `NetDevDiscoverExtendScopes`——后端 `ExtendScopesCandidates` 做 CIDR 校验与已覆盖去重（被现有 scope 包含的直接跳过），经 `applyConfigOnly` 落盘、逐条记 guardrail 类审计、入状态历史快照。scopes 永不可关的红线不变：护栏由人**扩展**而非绕过
- **大网段计划拆分**（P0-2）：`buildPlan` 对超 tunnel 上限（4096 地址）的 IPv4 网段自动拆成 /20 块（`splitForTunnel`，继承父网段 class/default，medium/large 仍需显式勾选）；父网段宽于 /16（>16 块）不拆——保持整体 default-off，探测期拒绝对应"netprobe/调预算"指引，避免计划卡被几百行淹没
- i18n：+6 键双语（ndv.disc.outOfScope* / extendScopes / extendConfirm）
- 验证：go build 全绿；netdev 包全量测试过（新增 TestSplitForTunnel / TestBuildPlanSplitsLargeSteps / TestExtendScopesCandidates）；前端 tsc 零错、vitest 49/49、vite build 绿

### feat(netdev/desktop): UI 引导补全 G1 批——应用内手册页签 + 场景导引分组 + 空态统一

引导审计结论：骨架好但覆盖断（详见 docs/UI_GUIDANCE_SPEC.md）。落地：

- **应用内手册页签**（G1-2）：新 dock 页签「手册」（归档组，BookOpen）——三份仓库指南（NETDEV_USAGE 使用地图 / NETDEV_HELP 求助指引 / browser-ops-guide 浏览器工作台）经 Vite `?raw` 打包嵌入前端、react-markdown 渲染，不新增后端桥；副本放 `src/guides/`，vitest 漂移测试逐字节对比 `docs/` 原件防腐化
- **场景导引分组**（G1-1）：ScenarioHub 14 卡按 a/b 前缀分「日常保障 / 安全与排障」两组带组头渲染；B 组头部新增 **靶场安全闭环** 主卡（识别→修复→复核路线说明，直达手册使用地图）
- **空态统一**（G1-3）：findings/proposals/audit 空态升级为「标题+说明+主按钮」——发现→运行安全基线、提案→对话起草（预填提示词）、审计→立即巡检
- **修复**：设备空态（项目过滤分支）`ndv.dev.emptyDesc` 漏 `{}` 渲染字面量
- i18n：+24 键双语（ndv.tab.manual / ndv.man.* / ndv.sc.gA/gB/c1* / ndv.fnd.pt.aud.empty*）
- 验证：tsc 零错；guides-drift 3/3；vitest 49/49 用例过（17 个 tsx 脚本式文件报 no-suite 为预存在，与本批无关）；vite build 绿

### feat(netdev/desktop): 安全工作台一键示例——CVE feed 填入示例 + 示例案例，附空态文案渲染修复

用户反馈：安全告警的 CVE 与案例两个视图在空环境里"看不清"，希望有简单的默认数据帮助理解功能形态。落地（遵循附录 B-4「产品不分发 feed」——示例只帮看，不自动激活）：

- **CVE 视图「填入示例」**：左栏 feed 文本框旁新增按钮，一键填入 4 条著名真实 CVE 组成的示例 JSON（覆盖 cisco/huawei/windows/linux 常见清单厂商）；只填文本框，导入仍需手动点击——feed 依旧是"用户自备、用户导入"
- **案例空态「创建示例案例」**：案例列表为空时主区空态提供一键建案——本地生成带四类时间线条目（发现/日志/排查/备注，错开时间戳）+ 3 条 IOC 台账（文档保留网段 198.51.100.0/24 与 example.com，不指向真实资产）+ 范围设备自动勾选前两台 linux/windows 主机；建后整个工作台（头部/向导/时间线/台账）一眼可见，可随手删除
- **修复**：CVE 匹配与案例三处空态文案漏 `{}`（`t("...")` 渲染成字面量）——matchesHint/noMatchesHint/noCasesHint 现已正常翻译显示
- **附带修复（构建红）**：`npm run build` 在本次改动前就因令牌检查失败——chat.css:1039 z-index 2（已提交代码）与 netdev.css 大屏新样式 22 处裸 z-index/border-radius；按语义归一：z-index 2/200/5 → --z-local-raised/--z-raised/--z-inline-sticky，radius 999/3/4/5/6/8 → pill/xs/xs/--radius/sm/md（5px 用遗留 --radius 令牌保像素不变）
- i18n：+9 键双语（ndv.sec.fillExample* / exampleCase* / exEntry*）
- 验证：tsc 零新错；locale-parity 2/2；dash-boards 4/4；双 CSS 令牌检查过；双模块 build 绿（未提交 git）


### feat(netdev/desktop): 浏览器工作台 60/40 布局——下半区 F12 切片（控制台日志 + 网络请求）

用户期望：观察窗只占中间栏约 60% 高度，下方放新内容（建议 F12）。落地：

- **布局**：画面镜像 60%（flex 3）+ 下方信息面板 40%（flex 2），不再独占整栏
- **F12 切片**（内核 browserdevtools.go）：控制台会话启用 runtime+network 域事件监听——页面 console 消息（log/warn/error/exception，参数拼接截断 300 字）与网络请求列表（方法/URL/状态码/资源类型，FAIL 含加载失败）各自环形缓冲 200 条，随会话存亡；BrowserConsoleDevTools 绑定返回双缓冲
- **下方面板**：「控制台 / 网络」两个子页签（沿用 ZCode 药丸样式），错误/异常计数徽标（红圈数字）、4xx/FAIL 请求红色高亮；可见时 2 秒轮询刷新；等宽字体列表贴合 F12 观感
- 采集仅控制台会话（agent 会话保持轻量）；mock 提供示例双页签数据供浏览器开发模式演示
- 验证：tsc 零新错；locale-parity 2/2（+4 键双语）；CSS 语法过；内核测试集过；双模块 build 绿（未提交 git）


### fix(browser): 元素点选标记在预览里可见——持久高亮 + 高亮即推帧

用户反馈：点击元素后浏览器观察页面看不到标记。根因有二：高亮只亮 1.5 秒（截图轮询抓不到那一刻）；高亮不推镜像帧（预览只在操作后刷新，点元素行不算操作）。修复：

- **点击改为持久标记**：内核注入 __fp-hl 样式类（橙色描边+底色，!important 盖过站点样式），换选元素自动替换标记，再点同一元素取消；悬停仍是 500ms 短闪（150ms 防抖）
- **高亮即推帧**：ConsoleHighlight 两条路径（ref/选择器、持久/短闪）末尾都 mirrorAfterAction——侧栏内嵌预览立刻显示带标记的画面
- **工作台画面源升级**：控制台源优先用镜像分桶里的实时帧（随每个动作更新，含高亮推帧），5 秒轮询降级为兜底（覆盖手动浏览场景）
- 验证：tsc 零新错；双模块 build 绿（未提交 git，按用户规则等待指示）


### fix(netdev/desktop): 同一时刻只留一个画面预览——浏览器工作台激活时侧栏预览整体隐藏

用户反馈：工作台打开后右侧栏预览只是折叠、标题行仍占位，变成两个预览。修复：NetDevLayout 在 bench 切换时广播 fairpeer:netdev-bench-changed 事件；浏览器面板监听——bench=「浏览器」时内嵌预览（含折叠标题行）完全消失，Esc/切回对话后按原状态恢复；深链 ?bench=browser 初始即同步。另：自本条起 git 提交/推送仅在用户明确指示时执行。


### feat(netdev/desktop): 浏览器体验三修——侧栏内嵌预览回归 + 工作台去元素面板（事件同步右侧栏）+ 元素悬停/选中页面高亮

用户三点反馈：

- **侧栏内嵌预览回归**：打开浏览器即可在右侧栏看到画面（优先控制台会话自己的帧，无帧不渲染、首帧自动展开）；大图仍走中间工作台（启动条并列）——不用再绕道工作台才能看预览
- **工作台去掉元素面板**：元素只留右侧栏（单一来源）；工作台切页卡后广播 fairpeer:browser-console-changed 事件，右侧栏自动刷新元素并清旧选中（ref 随旧页面失效）——不同步问题消除
- **元素悬停/选中页面高亮**：ConsoleHighlight 内核原语（ref 或 CSS → 滚动到可见 + 橙色 outline/底色闪烁，恢复原内联样式，纯视觉无 DOM 改动）；右侧栏元素行悬停短闪 500ms（150ms 防抖）、点选长亮 1500ms——列表行和页面元素终于对上号
- 验证：tsc 零新错；locale-parity 2/2（移除 wbPickHint）；skill-doc 101/101；CSS 语法过；双模块 build 绿


### fix(netdev/desktop): 浏览器工作台只占中间对话区——修正 absolute 全窗覆盖

用户反馈：工作台盖住了左边栏和对话框，应只占中间（参照大屏的位置）。根因：.ndv-wb 用了 position:absolute; inset:0——脱离 .ndv__main 中心格覆盖全窗；日志/安全工作台是流式子元素（flex:1 填满中心格）。改为同款流式布局，左侧导航栏与右侧 dock 不再被盖。


### feat(netdev/desktop): 浏览器工作台——第五个中心工作台（页卡切换 + 大画面 + 联动元素面板）

用户澄清：不是把页卡条补回侧栏，而是要像日志/安全/大屏一样**占据中间画布**的工作台，可关闭，且页卡切换要在那里——因为切换影响元素获取。落地：

- **BrowserWorkbench 接入 bench 机制**（与 logs/sec/dash 同款）：切换条新增「浏览器」chip、Esc/对话 chip/关闭钮三路返回、`?bench=browser` 深链与 fairpeer:netdev-bench 事件均可达；侧栏只留启动条（显示页卡数）派发 bench 事件打开
- **页卡 + 元素联动**（核心诉求）：工作台内切页卡即重取元素列表（ref 随旧页面失效）；元素面板带过滤/计数/截断（50 行），点行复制编号（提示粘贴到侧栏目标框）——切页卡、看画面、拿元素一屏完成
- **画面**：控制台源 5 秒轮询近实时；agent 会话源帧实时推送（复用镜像分桶）；页卡集 8 秒自动刷新（点击自动跟随/手动开页都能反映）
- 移除上一版的浮层观察窗（被工作台取代）与侧栏页卡条（用户明确不要）；恢复被切片误删的错误横幅（第二次，已在改动流程里留意）
- 验证：tsc 零新错；locale-parity 2/2（ndv.bench.browser + brc.wbPickHint 双语）；skill-doc 101/101；CSS 语法/z-index 过


### fix(netdev/desktop): 侧栏恢复页卡条——观察窗重构后页卡/预览在侧栏消失的回退

用户反馈：看不到 Chrome 页卡和画面预览了。根因：上上轮「看/做分离」把页卡条和预览整体挪进中间观察窗，侧栏只剩纤细启动条——功能没丢但不可发现，违背用户已习惯的侧栏直视页卡。修复：侧栏恢复页卡条（当前页卡高亮、点击即切、切换刷元素），条尾并列「观察窗」按钮打开大视图（大画面 + agent 会话源）；两处并存各司其职。顺带恢复被切片误删的错误横幅。


### feat(browser): hover 步骤全链路 + select 文本锚 + 运维浏览器使用指南

自主收尾批（用户委托"把该做没做的做了"）。做了四件，明确放弃两件（OCR：依赖重、DOM 文本锚已覆盖主场景；ref 失效自动重试：想清楚是错主意——ref 是快照内不透明编号，重拍后同号是别的元素，自动重试会点错，报错引导刷新才是正解）：

- **browser_hover 工具 + hover 步骤全链路**：悬停展开菜单的站点此前重放必缺悬停。内核用**真实 mousemove 到元素中心**（合成 JS 事件不触发 CSS :hover，伪类只认可信指针输入）；录制器 mouseover 防抖采集，过滤器启发式只保留"后续动作落在悬停子树内"的悬停（菜单展开→点子项），闲逛的划过全丢；词汇全链路（编辑器基础操作组/解析序列化/朴素转换/AI prompt/试运行）；名册 20→21
- **select 的 text= 锚**：按可见标签（label/aria-label/name）找下拉框并选值——多锚链补全（upload 不补：文件输入是隐藏元素，按文本定位不可靠）
- **孤儿 CSS 清理**：.ndv-brc__preview-empty（预览重构后遗留）
- **docs/browser-ops-guide.md**：面向团队的运维浏览器使用指南（界面地图/手动操作/三路沉淀技能/多锚写法/确定性执行/定时值守/保活/分享/FAQ）
- 验证：roster+hover 21 过；TestFilterHoverEvents（菜单悬停保留/闲逛丢弃/无后继丢弃）；skill-doc 101/101（hover 往返+锚链）；locale-parity 2/2；tsc 零新错；双模块 build 绿


### feat(netdev/desktop): 观察窗多画面源（控制台 + agent 会话）+ 技能导入导出

上轮盘点的缺口前两项落地：

- **镜像帧带会话标识**（内核）：BrowserPanelFrame 增加 session_id，四处发射点（会话开/关、每帧截图）全带上——前端终于能区分"这是哪个会话的画面"
- **观察窗画面源切换**：browserMirror 存储新增按会话分桶的最新帧（限量 8 按新旧淘汰，聚合字段行为不变、既有消费者无感）；观察窗在检测到 agent 会话帧时显示画面源 chips——「控制台」（保持 5 秒轮询近实时）+ 每个 agent 会话（`agent br_5`，帧随 agent 动作实时推送）——对话里 /技能 或定时巡检跑起来，运维界面终于能围观
- **技能导入/导出**：技能行新增「导出」——SKILL.md 存成 .md 文件下载（技能即单文件，团队直接分享）；技能页签新增「导入」——选 .md 文件进编辑器，检查后保存即入列表
- 验证：browser-mirror 26/26（新增 5 断言：分桶落帧/每会话最新/start 注册/无标签帧不建桶）；skill-doc 97/97；locale-parity 2/2（zh/en 各 +6）；CSS 语法过；双模块 build 绿


### feat(netdev/desktop): 浏览器面板看/做分离——中间观察窗（页卡+大画面）+ 动作记录转技能

用户三点反馈落地：

- **看/做分离**（用户提议的绑定关系）：右侧栏回归「驾驶舱」（元素 + 目标/文本/动作 + 动作记录，同源绑定）；新增中间大视图「**浏览器观察窗**」——Chrome 页卡条 + 大画面预览绑定在一起，侧栏只留一条纤细启动器（当前页卡标题 + 页卡数）；Esc/点背景/关闭钮关闭，切换页卡即刷元素
- **近实时画面**（用户观察到的同步延迟）：镜像帧只在 fairpeer 动作后推送，手动在被控浏览器开页面看不到即时变化——观察窗打开时每 5 秒轮询截图 + 打开即截一帧，手动浏览也近实时
- **动作记录 → 记录为技能**：面板每个操作（导航/输入/回车/点击/提取）记为结构化步骤；动作记录区新增「记录为技能」（计数 + 一键转 SKILL.md 草稿，默认 executor: browser-flow）与「清空」按钮；草稿进技能编辑器可继续编辑/试运行/保存——面板操作序列直接沉淀为可复用技能
- 顺带修复重构中误删的错误横幅；新 locale 键 zh/en 各 +9
- 验证：tsc 零新错；locale-parity 2/2；skill-doc 97/97；CSS 语法/z-index token 过（观察窗用 --z-local-backdrop）


### fix(desktop/netdev): 窄窗口（≤820px）运维侧边栏被折叠成裁切残条且无法恢复

用户反馈：窗口缩小后左侧边栏"丢失"。根因：tools.css 的窄窗口媒体查询（@media ≤820px，为编码视图设计的移动适配）把 `--sidebar-width` 置 0，而运维布局 `.ndv` 的 grid 第一列直接引用该变量——rail 被压成 ~25px 的裁切残条；更糟的是没有恢复入口（AppChrome 的展开按钮只在 React `sidebarCollapsed` 状态为真时渲染，CSS 媒体折叠不会置该状态），而窗口 MinWidth=760，760~820 CSS px 整个区间都会中招（显示缩放 >100% 时更容易落入）。修复：netdev.css 以 `.app.app--netdev` 双类特异性重新声明宽度变量（不依赖打包顺序），显式折叠态再高一档保持品牌行收起按钮语义；顺带把同媒体块里 coding 视图专属的 `.global-bottom-bar { width:100% }` 与 `.layout { padding-bottom:36px }` 在 netdev 下还原（底部栏回到只盖 rail 列、主区贴底）。验证：760/780/818/840/1100px 下 rail 宽度/内容/底栏/chrome 位置全部正常，折叠↔展开往返正常，CSS 语法检查过。另注：用户截图当时窗口 ~822px（>820）rail 主体内容完全未绘制（背景/品牌行/边框在）属 WebView2 缩放时的 GPU 合成层未重绘，与本次 CSS 修复无关，出现时划过侧边栏或再缩放即可恢复，频发可用 `FAIRPEER_DESKTOP_DISABLE_WEBVIEW2_GPU=1` 启动规避。

### feat(netdev/desktop): 运维面板页卡条——多页卡可见可切，切换即刷元素

接上一批多页卡内核能力（点击自动跟随 + browser_tabs/switch_tab 工具）：右侧浏览器面板此前看不到自己有几个页卡、也不能手动切换。补齐：

- **页卡条**：会话栏下方横向页卡 chips（序号 + 标题截断，当前页卡高亮、悬停见完整 URL，多条时横向滚动）；随 refreshState 一并拉取（每次操作后自动更新）；仅会话打开时渲染
- **点击切换**：点非当前页卡即切换（内核 switchSessionTab，旧页卡保持打开）；**切换后自动刷新元素列表并清掉旧选中**（ref 随旧页面失效）——「切换获取元素」一步到位
- 新绑定 ConsoleTabs/ConsoleSwitchTab（内核原语 pageTargetInfos+sessionTargetID 复用）+ wailsjs/bridge（含 mock 两个页卡）+ types
- 验证：tsc 零新错；locale-parity 2/2；skill-doc 97/97；CSS 语法过；内核浏览器测试集过；双模块 build 绿


### feat(browser): 多页卡——点击自动跟随新页卡 + browser_tabs / browser_switch_tab

用户场景：后台系统点一下常开新页卡（target=_blank / window.open），会话还挂在旧页卡上，后续点击/提取全落空，此前完全无处理（点击只报「开了新页卡」不跟）。落地：

- **点击自动跟随**：browserClick 原有新页卡检测分支从「只报告」升级为「跟随」——切换会话到点击打开的新页卡（chromedp.NewContext(s.ctx, WithTargetID) 挂兄弟上下文，旧注释顾虑的 allocator 重建并不需要），旧页卡保持打开可切回；refs 清空（属于旧页面）；跟随失败回退为原报告文案。面板点击、技能步骤点击、agent 工具全路径生效
- **browser_tabs**（只读）：列出页卡（序号/标题/URL，标记当前驱动的页卡）；**browser_switch_tab**：按序号或 target id 切换，当前页卡保持打开；名册 18→20，只读分类补 browser_tabs
- **切换实现**：switchSessionTab 串行化（tabMu）；旧页卡上下文弃用不取消（chromedp 取消会关目标，旧页卡必须留着可切回）；refs 随切清空
- 验证：roster/分类/浏览器测试集全过；双模块 build 绿


### fix(browser/console): 面板元素列表选中的 ref 无法点击——refs 未发布进会话

用户反馈：元素列表选 e13 点「点击」报 `click ref "e13": no snapshot taken for session`。根因：ConsoleElements 用 captureAXTree+buildSnapshotRefs 生成了元素列表，但**没把 refs 发布进会话的快照仓库**（s.refs 只有 browser_snapshot 会写），而面板点击/输入走 agent 工具的 ref 解析只读 s.refs。修复：ConsoleElements 与 browser_snapshot 同一发布模式（构建后整存、不原地改），列表选中的 ref 立即可点/可输入。附带：面板 click/type 的 ref 失效错误本地化为中文指引（「页面变过，点刷新元素重新获取」），不再裸露内核英文。


### fix(browser): 裸域名导航自动补 https + 无效网址中文报错

用户反馈：地址栏不带 http 直接输域名报 CDP 原始英文错 `Cannot navigate to invalid URL (-32000)`。修复：内核 normalizeNavURL 在 browser_navigate 与 browser_open 的导航入口统一补全——无 scheme 的输入自动加 `https://`（about:/data:/file:/chrome: 等伪协议原样放行），agent 工具、运维面板、确定性执行器、保活 navigate 全部经此路径受益；面板绑定点把残留的 invalid URL 英文错翻译为中文指引（检查空格/特殊字符、直接输域名即可）。TestNormalizeNavURL 覆盖裸域名/带路径/已带 scheme/伪协议/空串。


### fix(netdev/desktop): 元素列表压坏布局——body 滚动容器 + 过滤/计数/截断的元素选择器

用户反馈：点「刷新元素」拿到一堆元素后与下方内容重叠。根因：`.ndv-brc__body` 不是滚动容器，元素区一长就把内容推出卡片外。修复两层：

- **根因**：body 加 `overflow-y: auto`（含技能/录制/交互三个子页签），任何子页签内容过长都在卡片内滚动，不再溢出压到 dock 下方
- **元素选择器重做**（ElementPicker）：标题行带计数徽标 + 过滤输入框（按角色/名称/ref/值包含匹配）；最多显示 50 行，超出提示「还有 N 项未显示，输入关键字过滤」；刷新后清掉已失效的旧选中 ref（ref 是快照瞬时值）
- 验证：tsc 零新错；locale-parity 2/2（新增 brc.filterElements/brc.elementsCapped 双语）；CSS 语法检查过


### fix(netdev/desktop): 画面预览去空占位——无画面不渲染，首帧自动展开

用户反馈：预览区空占位「操作后这里显示最新画面」又没用又丑。改为：无画面时整个预览区（含标题行）不渲染；第一帧到达后自动展开显示，标题行可折叠回。previewEmpty 文案键 zh/en 移除，locale-parity 保持一致。


### fix(netdev/desktop): 运维浏览器技能列表只显示浏览器专属技能

用户反馈：右侧浏览器页签的技能列表混入了 ppt-auto 等非浏览器技能。BrowserConsoleListSkills 原先返回整个 ~/.fairpeer/skills 的全部用户技能；改为只返回 allowed-tools 含 browser_* 的浏览器专属技能（按名排序），办公/全局技能留在全局技能索引、不进运维面板。列表标题「已生成技能」→「浏览器技能」。注意：ppt-auto 出现在该目录说明本机 ~/.fairpeer/skills 下有同名用户副本（内核内置也有一份），过滤后面板不再显示，文件未动。


### feat(browser/skill): 多锚定位——目标列回退链 `CSS;;text=可见文字`

用户在编辑器里排技能时问「按你的要求怎么编排、缺哪些占位」。落地三层定位的编排表达（上轮方案修正：分隔符用 `;;` 而非 `|`——竖线会劈开 markdown 表格单元格）：

- **目标列多锚语法**：`#login-btn;;text=登录`——运行按序回退：CSS 锚走原 browser 工具；`text=` 文本锚在 DOM 里按可见文字定位（精确→包含，可点元素/label/placeholder/aria-label 都算标签，命中最深可点祖先）后直接操作——click 走坐标 MouseClickXY、type 走原生 value set+input 事件（React 受控输入同步）、extract 取命中文本；select/upload 暂仅 CSS（诚实边界）。全锚失败才报错并点名链内容
- **录制→多锚**：朴素转换自动把录到的元素名拼成 `选择器;;text=名称`（≤20 字），AI 归纳 prompt 同步教多锚写法——录完即得带回退链的稳定步骤
- **前端**：目标输入框 placeholder 提示多锚语法；步骤摘要显示为 `#kw→「百度一下」` 链；skill-doc `;;` 目标与 executor frontmatter 无损往返入守卫
- OCR 兜底未含在本批（DOM 文本定位已覆盖绝大多数属性漂移场景；Canvas/图片文字再走 PaddleOCR 链）
- 验证：`TestSplitFlowAnchors`（链解析/纯 CSS 兼容/空段丢弃）；TestNaive 断言升级为多锚形态；skill-doc 97/97；locale-parity 2/2；tsc 零新错；三模块 build 全绿


### feat(skill/browser): 确定性执行器 executor: browser-flow——每个站点流程一个稳定技能

用户诉求澄清：不是泛用的 browser-auto，而是**每站点独立技能**（技能1 访问 A 站做 XX 拿什么、技能2 访问 B 站做 YY……流程长、要稳定）。独立技能的形态已有（录制/编辑器/步骤表），缺的稳定性闭环是**对话调用时的执行方式**——此前 `/技能名` 走 LLM 子代理读表临场发挥，长流程易漂。落地：

- **内核步骤执行器**（`internal/tool/builtin/browserflow.go`）：解析 SKILL.md 的步骤表 → 开一个浏览器会话 → 逐步原样执行（navigate/back/forward/click/type/key/scroll/select/upload/wait[含 stable:]/extract[含 table]/screenshot/evaluate），一次 LLM 决策（调用技能）、执行期零；逐步报告 + 失败时保留会话供 browser_* 工具接手排查
- **run_skill 分发**：frontmatter `executor: browser-flow` → 直接路由内核执行器（跳过子代理循环）；boot 在 cowork/netdev 分支接线；技能索引行标注 `[⚙ 确定性]` 并在索引头说明参数传法（`参数=值`）
- **断点语义（无面板时的降级，先规划期报错、不弹浏览器）**：human 步骤必须带完成检测条件（浏览器窗口可见，用户人工操作，条件满足自动继续；无条件→明确报错引导）；ask 步骤的值经调用参数注入（`工单号=A123`，无等号整串进 `问题`），缺失则点名报错；运维面板试运行不受影响（交互横幅照旧）
- **编辑器**：结构化模式新增「确定性执行」开关（写 executor frontmatter）；四个站点模板（登录保活/表单填报/流式问答/数据导出）默认开启，空白模板保持手选
- 验证：builtin `TestParseFlowTable/Errors`（表格语法全类型+未知操作）、`TestParseFlowParams`（k=v/引号/无等号回退）、`TestRunBrowserFlowPlanningGuards`（缺参点名、无条件 human 规划期报错）；skill `TestRunSkillExecutorRouting`（nil runner 报错、接线后绕过子代理、body+arguments 透传）；skill-doc 92/92（模板 executor 守卫）；tsc/locale-parity 过；三模块 build 全绿

### feat(netdev): 安全告警按项目区分——Finding 项目快照标签 + 通知带项目前缀 + 切换记忆

用户方向：不同项目（站点/客户网络）的告警不应混在一个列表里，且归属要按项目区分。项目定义（[netdev] projects，名称+设备组集合）与标题栏切换器此前已就位，本次把「区分」从查看时刻的临时推导升级为随告警固化的标签：

- **Finding 加 `project` 快照字段**（后端 finding.go）：save 时按「设备组隶属哪个项目最多」固化归属（平票取配置序；"(all)"/"(unknown)"/未分组 → 空标签）。只在字段为空时计算——重保存保留原标签，改组不追溯历史（标签是审计历史，隶属是视图语义）
- **旧数据读时回填**：ListFindings 对无标签的历史文件按当前映射在内存回填（文件不重写——读不改史）
- **盲区规则**：空标签（未分组/未知来源 syslog/旧数据）在**所有**项目视图可见——匹配不到项目的告警永远不被隐藏
- **告警页签过滤改标签制**：从「设备 ∈ 项目设备集」的实时推导改为按 `f.project` 过滤（NetDevLayout），徽标/风险点随之；项目视图下加过滤语义提示行
- **通知带项目前缀**：webhook/IM/SMTP 推送文本标题前缀 `[项目名]`（半夜收到 critical 先答"哪个站点"），默认 JSON 出口增加 `project` 字段
- **切换记忆**：活动项目选择按名存 localStorage，定义加载后校验恢复（项目已删则清除；会话内手选优先）
- 验证：netdev 全包测试过（新增 TestProjectForDevices / TestSaveFindingStampsProject / TestListFindingsBackfillsLegacyProject：多数归属/平票/伪设备/未分组桶、重保存不覆盖、回填不改文件）；locale-parity 2/2；tsc 零新错；dash-boards 4/4

### feat(browser): 浏览器能力加厚——历史导航工具 + 表格提取 + 录制捕获滚动

用户方向：邮件后置，先把浏览器能力丰富。三个缺口补齐：

- **browser_back / browser_forward**（内核工具 + 步骤词汇 + 面板原语 + 试运行）：重放「看 A → 瞄一眼 B → 回 A」类流程走历史栈（保状态、免整页重载），无历史条目时页面不动不算错；入 BrowserTools 名册（16→18）、编辑器基础操作分组、skillDoc 解析/序列化/摘要、wailsjs/bridge/mock
- **browser_extract 表格模式**（format=table）：把选择器下（或整页）的 `<table>` 渲染为 markdown 表格，行列结构保留（转义 `|`、每表 200 行、至多 10 表、沿用 200k 截断与 untrusted 包裹）——SIEM 日志栅格/结果表不再被纯文本提取拍平；面板提取盒加「表格」勾选，extract 步骤值列写 table 即表格模式
- **录制捕获滚动**：注入脚本加 scroll 监听（防抖 800ms、按元素记忆位置算增量、<200px 抖动忽略、600px≈1 屏），事件 value=「方向 屏数」；朴素转换输出 scroll 步骤（缺省方向 down/3 屏），AI 归纳 prompt 的类型清单同步加 scroll；前端轨迹摘要显示「滚动 down 4」
- 验证：roster 18 工具过；TestNaiveSkillDraftScrollSteps（滚动→步骤、缺省回退）；skill-doc 87/87（back/forward/table 无损往返 + 摘要）；locale-parity 2/2；tsc 零新错；双模块 build 全绿

### feat(netdev/desktop): 安全日志值守技能 browser-siem-watch——定时巡检安全平台、分析判定、邮件告警

用户场景：用浏览器能力登录安全态势感知平台，定时读取日志实时分析判断，发现问题调用邮箱发送。定时机制本就存在（schedule_create：every/cron/daily，任务跨重启持久化，结果可经 im/email/notify/file 投递，固定在 cowork profile 运行故浏览器+邮箱工具均在），缺的是无人值守适配的技能协议。技能库「循环与值守」组新增 **browser-siem-watch**（第 8 个模板）：

- **无人值守前提**：任何环节不等待人工——掉线的正确动作是 email_send 通知重新登录（防刷屏：只在登录态由好变坏第一次发），不是等人
- **登录态延续**：靠浏览器持久化配置文件的 cookie（首次人工登录一次）；每次巡检 browser_open+navigate 后做登录态判断（重定向登录页即掉线）
- **分析判定**：按判定要点（默认高危告警/异地或非工作时间登录/批量失败/策略命中突增，可参数化）分析提取的日志；可疑不确定的列「待人工复核」不告警
- **告警纪律**：一轮最多一封告警邮件（多条问题合并）；无问题静默；结果摘要永远返回（存档到定时任务，schedule_list 可查，防空黑洞）
- **无头邮件门控如实写进协议**：无对话页触发时 email_send 默认拒绝（RiskExternal），需设置放行或保持 cowork 页开启
- 验证：skill-doc 78/78（守卫自动覆盖新模板：browser- 前缀 + runAs:inline）；locale-parity 2/2；tsc 零新错

### fix(skill): 技能索引溢出策略——休眠降级优先于静默截断；运维面板技能列表浏览器优先

用户问：技能一多，对话会不会读太多技能描述？答：正文从不进系统提示（按需经 run_skill 加载），索引每行限 130 字符、整块限 4000 字符（约 35–45 个技能）——但原实现对超 budget 是**字符级静默截断**，多出来的技能模型看不见且截断点任意。修复：

- **分区排序**：活跃技能在前、休眠（[休眠]，长期未用）在后——预算紧张先吃休眠项
- **分级降级**：超 4000 字符时先把休眠技能压缩为仅名字行（名字仍可调用）→ 仍超才整行截断，且截断提示报告**省略的技能数**（可行动：用户仍可 /名称 直调，长期不用的可去设置退休），不再出现半行乱码
- **面板列表排序**：BrowserConsoleListSkills 浏览器技能优先、其余按名排序，技能库变大后面板仍可扫读
- 验证：skill 包全过（新增 TestApplyIndexColdSkillsDegradeFirst：活跃描述在压缩后存活、休眠项转仅名字、顺序活跃在前；截断测试改断言省略数+整行切割）；desktop build/test 过

### fix(netdev/desktop): 工作台切换条回归 SPEC——仅在非对话视图渲染，对话主区恢复 v1.1 像素等价

用户反馈：运维界面右侧 dock 里点几个按钮（日志面板「工作台 · 合并」chip、告警「创建工单/证据链」），对话主区顶部就永久多出一条「对话/日志/安全告警/大屏 + Esc 返回对话」页签栏，回到对话欢迎页也不消失，且默认编码会话从无此物，观感突兀。根因：实现把 SPEC §10.2「只开对话时切换条完全不渲染」落成了 `*BenchEverOpened` 一次性闩锁——开过任一工作台即常驻；且「Esc 返回对话」提示在对话视图下按 Esc 实际无效（误导）。修复：渲染条件改为 `bench !== "chat"`——bar 只在日志/安全/大屏工作台为当前视图时出现，Esc 或「对话」chip 返回即隐去（提示也因此只在真正生效时可见）；工作台本体保持挂载、现场不丢，重进入口不变（侧栏「大屏/安全告警」、dock 日志面板 chip、命令面板、`o`/`Alt+1..5`、`?bench=` 深链）。SPEC §10.2/§10.3 图注/§10.8 隐藏规则/§10.10 发布门禁第 6 条同步改为「对话视图下切换条不渲染（含曾开过工作台后返回对话的场景）」。

### refactor(netdev/desktop): 技能库命名加 browser- 域前缀

用户指出：模板技能保存后是全局技能（对话里随处 / 调用），裸名（data-export、page-patrol）易与未来的非浏览器技能撞名。全部模板默认名改为 `browser-*`（browser-login-keep / browser-form-submit / browser-stream-query / browser-data-export / browser-page-patrol / browser-site-console / browser-my-skill），跟随内核内置技能的域前缀惯例（browser-auto / desktop-auto / email-auto）；选 browser- 而非 netdev- 是因为技能域是浏览器操作（cowork 与运维通用），不是网络设备专属。守卫测试同步锁定：库内所有模板名必须以 browser- 开头。skill-doc 75/75；tsc、locale-parity 过。

### feat(netdev/desktop): 运维技能库——浏览器页签内置七个专门运维 skill 模板

用户要求：运维界面的浏览器应有多个专门的浏览器运维操作 skill，不止一个。模板内容抽到 `src/lib/skillTemplates.ts` 统一维护，「新建技能」画廊改为**分组技能库**（每卡带说明与形态徽标）：

- **快速起步**：空白步骤表
- **常用运维操作**（步骤表形态，可结构化编辑/试运行）：**登录与保活**（human 登录 + url: 自动检测 + 登录态验证，长间隔任务衔接「保持会话」）；**表单填报**（ask 拿单号 → 填表 → human 过滑块 → stable 等流式回执 → 提取）；**流式问答/AI 站点**（ask 拿问题 → 填入发送 → stable:.answer 等流式回答完成 → 提取回答）；**数据导出**（ask 拿筛选条件 → 应用 → 导出 → 等完成 → 提取/确认下载）
- **循环与值守**（对话式 runAs:inline 协议）：**页面巡检**（逐轮提取关键指标与上一轮对比，变化即报，空闲保活、消息唤醒）；**值守循环**（通用形态：逐轮接收指令、定位操作、等流式输出、回报、保活询问）
- **解析守卫测试**：skill-doc 新增库守卫——表格型模板必须 lossy=false、协议型必须 runAs:inline 且可解析、库 ≥7 个模板；模板破坏编辑器解析器会在 CI 失败而不是在用户面前失败
- 验证：skill-doc 68/68（含 20 项模板守卫）；locale-parity 2/2（zh/en 各 +13 键）；tsc 零新错；CSS 语法/token 检查新增部分全合规（剩余违规为预存基线）；面板/编辑器行为不变

### feat(netdev/desktop): 技能编辑体验升级——ask 运行时询问 + 分类步骤面板 + 模板画廊

用户澄清：此前给的 7 步流程只是场景之一，真实场景更复杂——不要写死流程，要可扩展；且优先把编辑界面做好用。落地：

- **ask 步骤（运行时询问，表达力扩展的核心）**：步骤表新增 `ask`——目标列=回复绑定的参数名、值列=问用户的问题；试运行到 ask 暂停并在横幅里给出**输入框**，回复经 `TrialResume(reply)` 送回绑定到参数，**后续步骤的 {{参数名}} 在运行时替换**（TrialRun 改收原始步骤+参数表，逐步执行前替换；未绑定引用保持字面量可见，不静默置空）——「用户给数据/指令 → fairpeer 拿去页面操作」的通用机制；与 human（人操作）、wait stable:（流式完成）、源码协议（任意控制流）组合，覆盖比固定模板复杂的真实流程
- **界面友好（编辑器）**：步骤类型按「基础操作 / 读取 / 等待与检测 / 人工与对话」分组（行内下拉 optgroup + 新的「按类型添加」面板，每步带一句说明）；新步骤带合理默认值；切换类型保留仍适用字段（click→type 留 target，human⇄ask 留提示语）；对话式协议技能（步骤表解析为 lossy）直接以源码模式打开并显示提示横幅（说明 /名称 调用），不再误入空结构化表单
- **界面友好（技能页签）**：「新建技能」改为**模板画廊**：空白步骤表 / 表单填报（含人工断点，演示 ask→{{参数}}→human→stable 全链路）/ 值守循环（对话式），每张卡带说明；空技能列表给出引导文案（模板起步或去录制生成）
- **AI 归纳同步**：生成 prompt 增加 ask 规则（运行时才知道的值输出 ask、目标列参数名、后续 {{参数名}} 引用）与 stable: 等流式输出的提示
- 验证：skill-doc 48/48（ask 无损往返、{{ref}} 收集）；desktop `TestSubstStepParams`（绑定替换 + 未绑定保持字面量）等全过；locale-parity 2/2（zh/en 各 +28 键）；tsc 零新错（TrustDomainPanel 预存本地错误除外）；z-index/radius 检查我的新增全部用 token（剩余违规为预存基线，经 stash 对照确认）；双模块 build 全绿

### feat(netdev/desktop): 值守循环——对话式浏览器技能模板 + 流式输出稳定检测 + 会话级保活工具

用户诉求（第二步）：技能要能表达「打开站点等人登录 → 逐轮接收对话指令 → 在用户指定位置输入/点击 → 等远端流式输出完成 → 判断后回报 → 待机保活 → 用户消息唤醒继续」的循环流程，且从编码对话框 `/技能名` 唤起逐步执行。线性步骤表表达不了循环，落地为三件套：

- **值守循环模板**（技能页签「新建值守循环」）：`runAs: inline` 的散文协议——正文折入对话回合，主模型当编排者，浏览器操作派发给 browser-auto 子代理并跨轮复用同一 session_id（登录态不丢）；开局明确「登录由用户在浏览器窗口手动完成，完成后在对话里回复」并教模型理解「好了/done/已登录」等变体；每轮含定位规则（准确选择器直用 / 描述先 snapshot）、流式等待、结论先行回报；轮末一次性保活询问（用户同意才 arm）；用户任何新消息即唤醒下一轮
- **`browser_wait` 新增 `stable:<选择器>` 条件**：元素内容签名（文本长度+子元素数）停止变化 ≥2 秒判定流式输出完成——远端 AI/流式回复「是否输出完」的标准检测；`url:`/`stable:` 均入 Description/Schema
- **会话级保活重构 + `browser_keepalive` 工具**：保活从 console 全局态下移到 `browserSession`（keepMu/keepStop/keepMode… 字段 + 每会话循环，随会话关闭而止）；运维面板「保持会话」开关（ConsoleSetKeepAlive）与 agent 侧 browser_keepalive 工具（已入 BrowserTools 名册与 browser-auto 白名单，主循环照旧 Hide 经子代理调用）arm 同一机制——值守循环里用户说「保活」即可，无需离开对话去点面板；ping 模式页内同源凭据 fetch 滑动站点会话、navigate 定时刷新、local 仅防回收，interval 60–3600s
- 验证：TestBrowserToolsRoster 更新 16 工具过；builtin browser/console/wait 测试集过；skill 包全过；skill-doc 37/37；locale-parity 2/2；tsc 零新错（TrustDomainPanel 预存本地错误除外）；双模块 build 全绿

### feat(netdev/desktop): 运维浏览器控制台三升级——技能子页签 + 人工断点步骤 + 会话保活

用户诉求：浏览器面板只有「交互/录制」两个子页签，技能编辑能力没有独立位置；短信验证码/登录这类必须人做的操作需要技能在运行时暂停等人给信号；长间隔任务（如登录后 3 小时再来）站点会话与本地会话都会掉，需要保活。

- **技能子页签**（编辑能力的位置）：浏览器面板改为「交互/录制/技能」三子页签；技能列表从录制页签迁出独立成页——新建（空白模板含 human 步骤示例）/编辑/删除（确认后删目录）/▶ 试运行（打开编辑器即自动开跑）；录制页签回归纯录制
- **人工断点（human 步骤）**：步骤类型新增 `human`——表格目标列写自动检测条件（`visible:<选择器>`/`url:<片段>`/`title:<文案>`/`hidden:`），值列写给人看的提示语；试运行遇 human 步骤暂停并广播 `waiting`，编辑器顶部弹出「等待人工操作」横幅（继续/中止按钮）；自动检测条件每 1.5s 非阻塞轮询（`ConsoleDetectOnce`），满足即自动放行，人也可随时点「已完成，继续」——两条路都通；超时默认 10 分钟；`BrowserConsoleTrialResume/Abort` 新绑定 + 试运行单飞行守卫 + 陈旧令牌排空（防连点继续误放行后续断点）
- **录制→human 归纳**：AI 生成 prompt 明确「短信/邮箱验证码、扫码、登录密码、人机验证、支付确认一律输出 human 步骤」；朴素转换 fallback 同规则（password 字段与验证码类输入转 human，验证码值绝不落盘）
- **会话保活**（会话栏「保持会话」开关 + 状态行）：每 tick 先刷 `lastUsed` 防内核 10 分钟空闲回收，再按模式刷站点会话——`ping` 页面内同源 `fetch(location.href, {credentials:'include'})` 心跳滑动 cookie/服务端会话不打扰页面（跨 tick 读 `window.__fpKA` 上报状态，401/403 报「站点会话可能已失效」）；`navigate` 定时整页刷新（可指定 URL）；`local` 仅防本地回收；间隔 1–60 分钟可调（下限 60s 防轰）；状态行显示模式/间隔/上次刷新时间/错误，30s 轮询刷新；ConsoleClose 自动停
- **kernel**：`browser_wait` 新增 `url:<text>` 条件（登录跳转检测）；`ConsoleState` 扩 keep_alive 五字段
- 验证：desktop `TestNaive*`/`TestLooksLike*` 过（密码/验证码转 human、验证码值不泄漏）；skill-doc 37/37 过（human 解析/序列化/摘要无损往返）；locale-parity 2/2 过；tsc 零新错（TrustDomainPanel 预存本地错误除外）；双模块 build 全绿

### fix(desktop/i18n): 运维界面中文化补全——ndv.* 残留英文全量翻译 + 信任域/杂项硬编码收口

用户反馈：设置（含 NetOps/信任域页签）与运维大屏在 zh 界面下仍大量英文。上批「ndv 全量翻译」实际只覆盖部分键——短英文（"Loading…"/"⏸ Pause"/列名/状态词，≤15 字符或非小写开头）全部绕过 locale-parity 的启发式护栏，长期滞留。

- **zh 词典补翻 503 键**：ndv.* 490 键（sets 设置四页签全部表单与提示——设备/跳板/项目/预设/告警规则/数据库源/通知出口/弱口令字典/审计；logp·logwb 日志与日志工作台；sec 安全案例/CVE 匹配/入侵排查向导；cut 割接全流程；tpl 变更模板；srv 配置快照与漂移；sc·gs·br 场景导引/快速上手/晨报；topo 拓扑图例；res·disc·rep·dev 结果卡/发现/状态包/设备卡；bse·brc 浏览器技能与控制台）+ 非 ndv 13 键（启动副标题/推理协议/验证令牌/API 地址/OpenAI 兼容/IM 机器人/远程 Server·Token/Hooks 页签等）；终端风格前缀（`>> NULL_DATA:`/`[SYS]`/`● REC`）、§引用、emoji、`{占位符}` 全部保留
- **硬编码收口 3 处**：TrustDomainPanel 面板头 `quorum {n}` → `trustdomain.quorum`（法定人数）；紧急刹车原因 `"paused from settings"` → `trustdomain.pauseReason`（随界面语言，入域账本可见）；NetDevSection 设备表单 `Docker socket` 标签 → `ndv.sets.fDockerSock`；三键 en/zh 同步新增
- **保留英文（有意）**：品牌/协议名（Telegram、STARTTLS、WSL…）、格式占位（`sk-…`、`name@domain.com`、`shift+tab`）、mock 演示串与既有白名单键、纯 `{占位符}` 模板——残留清单复核仅此 44 条
- 验证：locale-parity 2/2 过；en/zh `{var}` 占位符集合逐键比对零错配；tsc 零错；dash-boards + live-ops-state 10/10 过

### fix(desktop/netdev): 全新环境下启动崩溃——Go nil 切片序列化成 JSON null 被前端直用

真实后端（非 mock）首启即崩 `TypeError: Cannot read properties of null (reading 'slice')`：mock 壳数据恒非空掩盖了该问题。

- **根因 1**：`HealthSnapshot`（health.go）在无 SNMP 设备时 `Devices` 保持 nil、`pollDeviceHealth` 各失败路径 `Interfaces` nil——Go nil 切片 → JSON `null` → HealthPanel `snap.devices.filter(...).slice()` 崩。修复：生产端 `Devices`/`Interfaces` 全路径保非 nil（空数组），组件侧再加 `?? []` 双保险
- **根因 2**：`NetDevAuditTail` 审计文件不存在时 `return nil, nil` → 审计表 `audit.slice(0,100)` 同崩。修复：返回空切片
- 复核其余默认页签路径（实时/日志/发现）：均有 `.length` 前置守卫或 `?? []`，无同类隐患

### feat(netdev/desktop): 大屏家族 + 发现指纹管线 + 导入向导 + 驳回提案 + ndv i18n 全量翻译

运维侧多批在途工作合并落地（143 文件，含状态历史批次织入宿主函数的挂点与锁加固随批生效）。

- **大屏家族**（NETDEV_DASHBOARD_SPEC）：DashShell 五屏——总览/调查链/割接/发现/暴露面（OverviewPanel·ChainBoard·CutoverBoardView·DiscoveryBoardView·ExposureBoardView）；`netdev_dash_app.go` 桥 + dashEmit 事件流 + `cmd/dashshot` 截图工具；验证截图入库；修 OverviewPanel 健康卡补「可达 {ok}/{total}」说明行（dash-boards 的 honest x/y 断言 4/4 过）
- **深链召回**：`fairpeer://cutover/<id>` 等 deeplink（跨平台实现+测试）；割接 hold/决策点 IM 推送带深链直达大屏
- **发现与指纹管线**（NETDEV_IMPORT_AND_FINGERPRINT_SPEC）：discovered 线索库 / discoveryrun 断点续扫 / layerdiscover 分层发现 / httpfp·snmpfp·banner 服务指纹 / role 角色推断 / attackpath 暴露面 / escalate 升级链 / healthwater·metrics 水位 / journal 巡检账本；`netdev_discover`/`netdev_topology` 工具接线
- **导入向导与自导出**：ImportWizardCard + selfimport/selfexport 台账迁移 + topoimport/topovsdx（drawio/vsdx 拓扑导入，附件芯片一键解析入 dock）
- **提案驳回与删除**（NETDEV_COMPLETION_SPEC §4.1）：RejectProposal（理由随提案持久化，agent 下一轮可见）+ DeleteProposal（仅草稿/终结态可删）——含状态历史快照挂点与锁内读改写；IM 审批/驳回通道（netdevcmds）
- **ndv.\* 全量 i18n**：243 键中文翻译补齐 + dock 页签/导航 tt() 化；zh/en 键集一致（locale-parity 门禁）
- 验证：go 双模块 build/vet/test 全绿（含 dashboards/discovered/discoveryrun/layerdiscover/httpfp/snmpfp/topoimport/attackpath/role/escalate/journal/netdevcmds 新测试）；tsc 零错；vitest 指定集 11 文件/46 用例 + locale-parity 过

### feat(netdev/desktop): 状态历史与三层回退——对齐 Claude Code/ZCode checkpoint 实践 + 编码模式 rewind 增量

**运维模式（状态回退层，NETDEV_SPEC_V2 附录 D）**：此前 proposals/jobs/cutovers/templates/清单 TOML 全部原地覆写、零版本化——本批给每个状态迁移加"写前快照"，配 dock 新页签「状态历史」（归档组，紧挨审计：审计=不可变事实，状态历史=可回退留底）。

- **快照层** `internal/netdev/statehist.go`：复用 `checkpoint.Store` 第二实例（root=fairpeer 配置目录，同盖 `netdev/**` 与 `config.toml`，上限 200 事件）；挂点与状态迁移审计同址（~26 处：提案 propose/approve/reject/execute/rollback/delete/close-watch、job 全生命周期、cutover 全生命周期、模板、发现裁决、设置/纳管/导入/拓扑/golden/工单），actor 分 user/agent/im/system（割接 runner 经 `CtxStateActor` 标 system）；secrets/审计流水/journal/只增库明确排除
- **回退引擎**：后缀语义（回到事件 E 前 = E 起所有触碰文件还原到各自动手前，文件原不存在则删除）；恢复前写 `restore-keep` 反向事件 → 可重做（守卫：反向事件后出现新事件即禁用）；ZCode 式事前安全分类（唯一拦截条件 = 后缀触碰活跃实体：执行中/观察期提案、运行/暂停作业、运行/hold 割接，列出实体名；执行时二次复检）；config.toml 专属连带警告；UI 固定声明"仅恢复本地记录，设备回退走提案/割接回滚"
- **桥/UI**：`NetDevStateEvents/StateEventDiff/StateRestore` + StateHistoryPanel（**2.5s 自动轮询**、逐文件 unified diff、三分类确认、重做按钮）+ 页签 badge=可回退事件数（5s 轻量轮询）+ zh/en 各 ~62 键（ndv.hist.*）

**编码模式（对齐增量）**：rewind 三分类 + Reapply。

- **三分类**：`FileSnap` 增 `Hash`（快照前哈希）+ `PostHash`（写工具执行后由新增 `SetPostEditHook`/`SharedPostEditHook` 钩子链记录的"代理最后写入"哈希）；`Controller.RewindPreview` → safe（未再变）/ unsafe（外部改过，回退会丢）/ ignored（旧快照无哈希留痕）；Message.tsx 确认层三档展示（`RewindPreviewForTab` 桥；远端 tab 无 RPC 优雅降级）
- **Reapply**：`Rewind(code/both)` 以 `Store.KeepCurrent` 写合成反向检查点（`rewind-keep` 前缀）；最新反向检查点在 rewind 菜单露「重新应用上次回退」（对它做 code 恢复即重放，重放再留底 → undo/redo 翻转）；`CheckpointsForTab` 增 reapply 标志
- **checkpoint 包顺带修复**：`Finalize()`（未结束事件不再被 List 隐藏路径——事件型存储必需）；`NewWithLimit` + Begin/Finalize 后持续 prune（原只加载时裁剪）；`DiffForTurn`/`restorePerm` 改走 `safePath`（相对路径 + CWD≠root 的存量缺陷，netdev 实例全踩）
- Rewind 内部顺序调整为"先截断对话再恢复代码"（合成留底事件的会话边界取截断后消息数；失败时先拒后改文件）
- 验证：go 双模块 build/vet/test 绿；checkpoint 8 个新单测（Finalize 可见性/prune 时机/CWD 无关/分类/KeepCurrent 重放）+ netdev statehist 5 个（往返/建删/活跃拦截/重做守卫/config 同 root）；tsc 零错 + locale-parity 过；§11.6 页签契约 11→12

### feat(desktop): 工作区 Git 进展面板 + 检查点补洞 + 体验修复（WORKSPACE_GIT_SPEC G1–G3/G5–G7/F1–F4）

- **G1 分支名**：改动页签摘要行显示当前分支（本地+远程，detached 标注）——修复 `gitBranch` 后端已取、前端 `loadWorkspaceChanges` 丢弃的断链；非 git 仓库降级显示 gitUnavailable
- **G2 增删统计**：摘要行追加 `+N -N ?N`（绿/红/黄，与 CLI 状态栏同语义）——桌面搬 CLI 的 numstat 解析（二进制 `-` 列跳过，8 组单测）+ 远程 host `git/status` 同步 numstat
- **G3 分支切换**：分支名变下拉触发器（AnchoredPopover + 内置确认步骤，会话改动非空时警告）→ `GitCheckout` 后三连刷新（改动/历史/文件树）；运行中/待审批禁用（Tooltip 说明）、远程 tab 后端双重拒绝；>12 分支出过滤框
- **G5 子代理检查点补洞**：`SharedPreEditHook`（boot 建占位、controller 构造后注入快照函数）让 `task`/`run_skill` 子代理的写工具进检查点——办公模式技能写文件首次可 rewind；专项测试覆盖 hook 触发与后置 Set 语义
- **G6 轮次 diff**：会话改动条目悬停出现 diff 按钮，按检查点渲染该文件最后一次被改轮次的改动（接已有 `CheckpointDiffForTab` 桥）——非 git 项目首次可见"改了什么"；快照过期降级提示
- **G7 TodoPanel**：任务 5/5 后自动折叠为一行，新 todo_write 重展开
- **F1** `tt("ndv.gs.footer")` JSX 泄漏修复（补花括号）；**F3** 内核 notice 相邻去重（×N 徽标）+ 四条固定文案 i18n（"resumed session…"不再英文裸刷）；**F4** 发现队列清理（`DismissFinding`/`ClearFindings` 后端+桥+单测，前端按钮随运维批次）
- **F2 防回归闸**：`locale-parity.test.ts`（en/zh 键集一致 + zh 值非英文句，7 键白名单）挂入 npm test/test:all，并在 ci.yml 增设独立步骤（linux/amd64 单腿跑 vitest——全量前端测试未在 CI 验证过，暂不全上）
- 验证：go 双模块 build/vet/test 绿（vet 仅 2 条预存 UIA 警告）；tsc 相对 HEAD 零新增错误；前端 43+2 测试通过；**已实测**（2026-08-31 新构建）：分支 main + `+6 ?1` 与 git numstat 一致、非 git 降级、切换 popover 正常
- 提交切分说明：共享文件（locales/NetDevLayout 等）按 hunk 手术只纳入本批——运维 i18n 243 键翻译与 F1/F4 前端按钮与另一在途批次逐行融合，随该批次提交

### docs(netdev): SPEC v2.1——原 R6 批次改组为「按需停车场」

R1-R5 落地后原「R6 规模化与战略批」以功能菜单式批次呈现，读起来像排期承诺，实则多数条目无需求牵引。§八重写：**每条目挂启动条件 + 最小可用面**（真启动时先交付的一小块，按使用证据再加，不照菜单全做）——规模化（>200 台实测触发）、gNMI（轮询成为实测瓶颈且设备真支持）、sFlow/NetFlow（ISP 真实用户）、GPU 面（dogfooding 痛点清单驱动，原七项菜单降级为素材库）、BGP/光层（骨干网需求）；**拓扑人工覆盖层与 CSV 批量导入摘出为 §8.7 小件池**（自动布局出错第一天就有用、首次批量上架 20 台就疼——都不该锁在战略批门槛后，无门槛随时可做）；§九路线图行与全文 9 处 R6 引用同步改写，不设批次验收（立项时按最小可用面定当期验收）。规格版本 v2.0 → v2.1。

### 运维补全 SPEC C3/C4 收官：提案驳回 + 升级链 + 导入向导 + 14 卡 + 快捷键

- **提案驳回**（§4.1，本批最高优先）：`RejectProposal`/`DeleteProposal` 后端方法（draft/approved 可驳、原因随提案持久化——agent 下一轮读到提案即见被拒原因；删除限 draft/已终结态，活跃管线必须留档）+ `ProposalActions` 行内「驳回…」（内联原因输入框）与「删除」按钮 + dock 提案页签去 `slice(0,10)` 硬顶改状态筛选 chips（待办/全部/草稿/已批准/已驳回/已终结）+ 加载更多分页。测试：reject/delete 状态守卫/原因持久化/活跃管线拒绝全链路
- **通知升级链最小版**（§5.2/裁决 #8）：critical Finding 超 15 分钟未 resolve → 自动重发升级通知（全出口：SMTP+bot+webhook，每条 Finding 只升级一次），EnsureNotifier 时启动巡检协程（进程单次）。测试：注入时钟的老 critical 升级/resolved·young·warning 不升/只升一次
- **迁移导入向导**（§5.6/裁决 #11）：`ImportPreview`（读导出 JSON → 新增/同名冲突/DB 源三段 diff，无副作用）+ `ImportApply`（勾选项合并骨架——凭证不迁移，历史 findings 只读引用不覆盖）+ `ImportStageFile`（浏览器读文件内容暂存到状态目录，桥接 Wails 无文件路径的限制）+ 前端 `ImportWizardCard`（复选框逐项勾选 → 合并，凭证补录提示）；审计页「导出状态包」旁增「导入状态包…」按钮
- **ScenarioHub 扩容 6→14 卡**（§3.2）：新增 IP/MAC 定位、主机体检、弱口令核查、CVE 清单匹配、入侵排查向导、变更-故障关联、报告族、容器/K8s 诊断（每卡一句话+一个直达动作）；网格双列→三列
- **快捷键最小集**（§4.10）：`r` 刷新当前页签、`/` 聚焦当前面板搜索框（均限无输入焦点时生效，不吞命令字符）；Esc 层级退出为原有
- **规格修订**：NETDEV_SPEC_V2 §10.1 dock 页签目录恒为 10→11（浏览器页卡经评审裁决纳入）；发布门禁断言行同步
- i18n 全量（§4.9）**未做**——硬编码中文量大（页签/面板/设备卡/场景卡全量），留待独立批次
- 验证：netdev/desktop 两模块 build+test 全绿（驳回/升级链/导入新增测试）；前端 tsc/CSS/vite build 通过

### 修复：模板/提案渲染层空数组崩溃（null.length）

- **现象**：打开「提案」页签即 React 崩溃 `TypeError: Cannot read properties of null (reading 'length')`。**根因**：Go 的 nil 切片序列化为 JSON `null`（非 `[]`）——只用设备属性变量、无用户变量的模板 `vars` 为 null，`TemplateCard` 的 `t.vars.length` 直接崩；同类隐患：结构化提案步骤（k8s-apply 等）`commands` 为 null 时 `s.commands.join` 崩。**修复**：前端全部可空数组访问加 `?? []` 守卫（TemplateCard 变量表/预览、ProposalCenter 详情、stepSummary、CutoverView 步骤清单、提案页签割接卡步数），Go 侧 `Template.Vars` 补 `omitempty`（无键 + 前端兜底双保险）；前端 dist 已重建（新 hash 资产），桌面 `go build` 嵌入验证通过
- 工程：`.gitignore` 增 `/desktop/e2e-*.log` 与 `/desktop/*-e2e.exe`（e2e 调试产物防再入状态）；`desktop/test.exe` 仍在 git 跟踪中（历史遗留，建议下个提交 `git rm --cached`）

### 测试套件接线：11 个孤儿测试文件接入 `npm run test`

审计发现 `src/__tests__/` 有 11 个测试文件从未接入测试脚本——`npm run test` 不会跑到它们（其中 graph-toolbar-filter 等 5 个 import 自 vitest，被 tsx 直跑会直接崩）。已按运行器正确接线：6 个 tsx 脚本式（at-matches/browser-mirror/mermaid-export-logic/mermaid-logic/skill-doc/toolcard-state）进 tsx 链，5 个 vitest 式（graph-toolbar-filter/import-modal/rag-panel-layout/template-asset-card/template-empty）进 vitest 列表。`test` 与 `test:all` 同步更新；全量 22 个 tsx 套件 + 8 个 vitest 文件（37 测试）通过、退出码 0

### 稳定性修复：启动恢复崩溃守卫 + 退出 panic 恢复

- **恢复标签页时 React 崩溃**（`state.items` 在 `useController` 水合期间短暂为 null，预览自动检测 effect 直接读 `.length` 即崩——真机 E2E 复现一次）：effect 加空值守卫
- **退出时 Go panic**：窗口销毁与 `saveWindowStateSync` 竞态时 winc `ScaleToDefaultDPI` 除零（DPI 为 0）导致整个进程 panic 退出。几何持久化为 best-effort：recover 守卫 + 警告日志，保留上次保存的状态

### Mermaid 图导出：单图 PNG/SVG + 会话导出成图

对话流里的 mermaid 以前只能屏幕上看——单图无导出按钮，会话导出（PNG/PDF/HTML）时 mermaid 围栏按高亮源码文本处理，与线上渲染不一致。

- **单图导出**：MermaidViewer 头部加 PNG/SVG 按钮。核心在 `lib/mermaidExport.ts`：导出前以 `htmlLabels:false` 指令重渲染一版（HTML 标签走 foreignObject，经 `<img>` 落 canvas 在 Chromium 下整体空白；纯 SVG text 则稳定），光栅化前钉死显式宽高并剥 `max-width`（否则 SVG 按 0×0 加载画不出任何东西）；PNG 2x 缩放，SVG 插底色矩形；背景读自屏幕卡片（WYSIWYG）
- **会话导出成图**：`renderExportSurface` 前把 mermaid 围栏预光栅化为 data-URL 图片（`inlineMermaidFences`），PNG/PDF/HTML 三格式同时受益；失败回退原代码文本形态
- 测试：`mermaidLogic`（围栏收集/替换/SVG 尺寸解析/文件名清洗）+ `mermaidExport`（内容规范化校验/朴素回退）纯逻辑单测

### 浏览器镜像面板：办公 dock 实时呈现 agent 驱动的浏览器

补上"注释里设计了、代码从未实现"的断链：browseruse 边车本就在 SSE 里流式发截图事件，Go 侧收到即丢、镜像面板不存在。

- **内核帧源**（browser.go）：所有 chromedp 动作经 `runBrowserAction` 咽喉点，成功后 `mirrorAfterAction` 截视口图（PNG data URL + 当前 URL）；会话 open/attach/close 发生命周期状态；`browser_auto` 转发边车截图帧与 run 起止（含失败路径）
- **传输**：desktop 注册 `SetBrowserPanelSink` → Wails 事件 `browser:mirror`
- **前端**：`lib/browserMirror.ts` 模块级 store（纯 reducer + `useSyncExternalStore`，订阅挂 App 顶层——面板关着不丢流）；CoworkDock 新「浏览器」tab（MonitorPlay 图标），显示来源徽章（浏览器工具/自主浏览）、运行呼吸灯、当前 URL、最新截图；活动开始自动开 dock，手动关闭后同轮不再弹（anti-nag，复用预览 dock 模式），新 run 重置
- 测试：store 状态机 22 断言（生命周期/帧更新/活动起点判定）；内核 sink 转发 nil 安全测试

### 运维「浏览器」控制台：手动驱动 + 录制操作沉淀为技能（skill ⊃ 工作流）

右侧栏产物轴新成员：运维 dock「浏览器」页卡（ZCode 胶囊风子页卡 **交互/录制**）；办公 dock 的实时镜像见上条。工作流的容器不另建引擎——录制的操作经 AI 理解直接生成 SKILL.md 落入技能目录（步骤表=工作流，skill ⊃ 工作流），`/名称` 即刻调用，CapabilitiesPanel 统一管理。

- **交互子页**：会话开/关（独立可见 Chrome tab，支持 CDP 附加）· URL 前往 · 无障碍树元素列表（ref/CSS 双目标，点选即用）· 目标+文本输入（回车开关）/点击 · 提取（复制/交给 AI 走 `onInsertComposer`）· 操作日志 · 折叠画面预览（复用镜像流）。11 种原语（navigate/click/type/key/scroll/select/upload/wait/extract/screenshot/evaluate）全部直调既有 agent 工具——`internal/tool/builtin/browserconsole.go` 单例会话槽包装，零改动现有路径；boot 使 netdev profile 也注册浏览器工具
- **录制子页**：CDP `AddBinding`+注入脚本录制手动操作（跨导航/iframe 持久；CSS+AX 双目标；密码占位不落盘），实时"动词+元素名"步骤流 + 计时/计数；停止后**三道去噪**（页面级输入去抖 → 确定性规则：同目标连点折叠/无效点击丢弃/A→B→A 往返折叠，纯函数单测 → AI 语义归纳）且**过滤透明**（「已过滤 N 条」可展开核对）→ AI 理解生成**四段式 SKILL.md**（何时使用/步骤表/注意事项/验证；借 Hermes 反思模板与通用性规则：{{参数}}化、无用户名/密钥/绝对路径），模型不可用自动退化朴素转换并明示
- **技能编辑器**（草稿与已有技能共用）：结构化⇄源码双模式——**防丢失护栏**：解析失败或有不可往返内容时拒绝切换、保持源码模式（绝不静默覆盖手改）；步骤行内联编辑（类型/目标/值/上移下移/删除/插入）· {{参数}}默认值 · 原始录制对照 · **试运行**（逐步 ✓/⟳/✕，失败即停）· 保存校验（frontmatter/同名覆盖需确认）
- **真机 E2E 驱动的修复**（界面自动化全链路验收：打开浏览器→录制百度搜索→生成→编辑→落盘）：① chromedp `ListenTarget` handler 持锁死锁事件循环（录制安装 60s 超时并堵死会话全部命令）→ 原子标志+专用短锁+Run 出锁+15s 快失败；② AI 草稿 markdown 围栏/前导文字致编辑器拒收 → 生成侧规范化+合规校验回退；③ 朴素模板空 name（中文提示清洗为空）→ 兜底名；④ 模型解析链增「活动标签当前模型」与 `ResolveModelWithFallback("")` 终端回退两级（静态配置全空而会话在跑/恢复标签无 model 字段的场景）；⑤ 录制 label 对表单控件 name 属性优先于 placeholder（百度动态热搜词污染步骤名）。桌面层 `browser_console_app.go`：24 个绑定 + 生成器 + 试运行事件 `browser:trial`
- 验证：内核/桌面 build/vet/test 全绿（过滤规则/内容规范化/朴素模板/技能释放纯逻辑测试）；前端 tsc/CSS 校验/vite build；SKILL.md⇄结构化往返 26 断言；技能落盘 `~/.fairpeer/skills/browser-skill/SKILL.md` 真机确认

### netdev R4 尾项：§4.1 体检电池升级为 Job 引擎 runbook

- **「作业」页签新增「诊断作业」卡**：R4 Job 引擎此前只有桥方法无前端入口——暂停的 runbook（断点/熔断/on-fail=pause 冻结）无处可见也无法恢复。现在作业页签第二卡展示 netdev runbook 轨迹（体检电池等）：状态/步骤进度（✅❌⬜）/活跃时长/暂停原因，running 可暂停/终止、paused 可**从断点继续**或终止；进行中 3s 自轮询。JobsPanel 拆为双卡组件（定时任务 + 诊断作业）

- **Triage 电池走 Job 引擎**（§4.1 原文「v1 为顺序执行；R4 的 Job 引擎落地后升级为带 expect/timeout/断点的 runbook」）：主机体检（linux 11 项 / windows 5 项电池）改为构造 Job runbook 经引擎执行——每步 30s 超时、on-fail=continue（诊断电池要尽量收全，单项失败不拦整体）、预算封顶（墙钟 10 分钟、命令数=电池长度、熔断阈值=电池长度+1），**每次体检留 job 轨迹**（jobs 目录持久化，`triage:<设备>` 命名、CreatedBy=triage，审计可见）；expect/retry/断点语义电池默认关闭但引擎已具备（R5 入侵排查向导直接复用同一路径）；报告组装从 job 步骤状态映射——refused/设备错误/未跑完（watchdog 中断）三态分段呈现，Summary 标注电池完整性
- **Job 引擎增 `RunJobSync`**：启动 runbook 并阻塞至 runner 退出（终态/暂停/ctx 取消时 abort）——同步调用方（体检、巡检类）骑上引擎获得全部步骤语义与预算，无需异步交互；runner 增加 done 通道支撑同步等待
- 测试：真 SSH 全链路（bash 提示符 sim 上跑完 11 项电池、job 轨迹落盘、on-fail=continue 收全、报告分段与电池一一对应）；jobs 引擎全量回归绿

### netdev R4 对账补差：§7.1 两处规格偏差修正

- **执行前在线检查改「暂停等人确认」**（§7.1 原文：发现其他在线人员则**暂停**并列出会话，人确认后才继续——此前实现只记 Note 便继续执行）：发现会话即拦下执行（状态保持 approved、无半执行），会话清单记入提案备注并提示再次点击「执行」确认；再次执行看到**同样清单**视为确认，会话有变化则重新要求确认。配套修复一个会话层陈年毛刺：`OpenSession` 建立后**排空 shell 横幅再返回**（此前首条命令输出混入 banner——`who` 的行数统计首跑/次跑不一致）。测试：真 SSH 全链路（首次拦截/二次放行/状态不落 executing）+ 网络设备不受影响的回归
- **观察期劣化检测真正接线**（§7.1 原文：观察期内持续对比健康信号，劣化超阈值 → 最高级 Finding + 附「一键发起回滚提案」——此前注释声称由健康轮询承担但无接线）：提案 done→watching 时采集目标设备健康基线（ifDown 计数/-1 不可达，仅 SNMP 设备有信号），健康轮询每周期对比 watching 提案——可达→失联或 down 口增长即最高级 Finding（Source=watch:\<提案ID\>，附回滚指引——回滚仍需人按）+ WatchNote 只告警一次；基线即不可达不误报「仍不可达」。测试：劣化触发/不重复告警/恢复不告警/坏基线豁免

### netdev R4 完全收官：批量模板 + 服务器配置文件管理 + 带外启动器 + Telnet 裁决

R4 行剩余四小件落地——至此 §九 R4 批次 10 项全部完成。

- **批量模板**（§7.2，`internal/netdev/template.go`）：模板 = 步骤序列 + 变量（`{{name}}/{{address}}/{{hostname}}/{{vendor}}/{{group}}` 设备属性免填）；`TemplateRender` 逐台 dry-run 预览——每条渲染产物标注分类器判定与危险动词，**无任何副作用**；`TemplateApply` 生成一份多设备步骤的提案草稿，滚动执行/首败冻结/回滚矩阵（每台一行 applied/rolled/pending）全部沿用提案既有管线；**变量值白名单字符集**（附录 B-10：Unicode 字母数字 + `_.:/@%+()-` 与空格——中文描述合法，`;|&`$\` 等元字符与换行全拒）；未知占位符在预览标不可用、apply 整批拒绝（不静默空渲染）；只读组目标拒收。前端 `TemplateCard` 落「提案」页签（§10.6 家 = 提案页签内模板入口）：新建表单（变量/命令/回滚/目标勾选）→ 变量填充 → 逐台预览 → 生成草稿进既有审批流
- **服务器配置文件管理**（§7.3，`internal/netdev/srvconf.go`）：设备新增 `config_paths` 白名单（log_paths 同款授权模式，仅人工登记）；快照（`SrvConfSnapshot`，版本入 `srvconf/` 备份库，路径哈希分组）→ 两版本 UnifiedDiff（复用 internaldiff）→ **环境 Drift**（`SrvConfDrift`：同路径跨设备逐一读取对比，same/drift/absent/error 四态）；**修改不发生在产品内**——编辑产物以 file-upload 提案提交（§7.1 已落地）；**restore-verify 提案步骤类型**：把快照恢复到指定 staging 目标并跑验证读（`nginx -t` 类）证明「备份真的能恢复」——**生产目标（与备份源同组/同设备）拒绝作为演练接收方**，接收方须在自己的 config_paths 白名单该路径，回滚恢复接收方原文件。测试 4 组全绿（快照 diff/白名单拒绝/Drift 判读/恢复演练 e2e 含回滚/安全门）。前端设备卡「变更」区 `SrvConfCard`：拍快照/点选两版本 diff/勾选对照设备跑 Drift，附「修改走提案」指引
- **带外启动器**（§6.3，轻量）：设备新增 `oob_url` 深链字段；`NetDevOOBLaunch` 桥——windows 目标启动 `mstsc /v:`（非 Windows 宿主降级为提示）、vmware→`https://addr/ui`、redfish→BMC Web、任意设备可配 oob_url；**产品内不实现 RDP/VNC 协议**，只启动本地工具与浏览器，点击入审计（class=oob，审计表新增「带外」标签）；设备卡新增「带外」区按钮
- **Telnet 裁决执行**（§6.4，默认「删」）：`allow_telnet` 字段从 config/桌面视图/前端类型/表单/示例 TOML 全链路移除；protocols 注释更新为 ssh/netconf——现代环境 telnet 管理面收敛而非纵容，确需带外走 §6.3 启动器
- 设置设备表单同步新增「配置路径白名单」与「带外入口」两个字段；bridge/mocks 全量补齐（Template*/SrvConf*/OOBLaunch）
- 验证：netdev+config 全量 `--count=1`、desktop build/vet/test、前端 tsc/`npm run test` 全绿

### netdev R4 收官：Job 引擎 + 结构化提案步骤 + 人工终端全链路 + 割接模式

R4 剩余四件（规格 `docs/NETDEV_SPEC_V2.md` §7.1/§7.2/§6.1 + v1 §10.5 C 批）一次落地；至此 R4 验收线全通（设备卡点开终端直接敲命令且可审计回放；提案执行前自动提示在线人员（e2adee8 已落）；割接 runbook 以倒计时+验证门跑完全程；nginx.conf 变更走 file-upload 提案含备份一键回滚）。

- **Job 引擎**（`internal/netdev/job.go`，v1 C 批）：多步骤诊断 runbook harness——步骤带 `expect`（正则门）/`timeout`/`retries`/`on_fail`（pause|abort|continue，默认 pause 冻结等人）与 `pause_before` 断点（恢复经 `BreakpointOK` 不重复拦截）；watchdog 三预算（墙钟默认 30m、命令数默认 200、连续失败熔断默认 3）超限即暂停；全部执行走 `m.Exec` 只读密封（Job 不给分类器开任何旁路，写动作仍只存在于提案）；持久化 jobs 目录可断点续跑；测试 6 项（完成/断点/失败暂停+终止/预算熔断/expect 失配/校验）全绿
- **结构化提案步骤**（§7.1，`proposal_steps.go` + `proposal.go` 判别联合）：`cli`（现状，向后兼容）之外四种——`k8s-apply`（yaml 全文 → server-side apply PATCH；备份 = apply 前 live 对象 JSON，回滚 PUT 时 resourceVersion 钉住漂移即拒；Secret 对象与白名单外 Kind 拒收）、`sql-migration`（目标 = `[[netdev.db_sources]]`，**down 脚本必填缺则不可提交**；逐句执行逐句审计，v1 限 mysql/postgres/mssql）、`file-upload`（linux SSH 目标；备份现文件 → `base64 -d` 流式上传 → sha256 校验；声明 checksum 拨号前即核验；回滚恢复备份或删除新增文件）、`cert-replace`（证书对上传 + reload 命令，回滚恢复旧对 + reload）；**危险动词扫描**（delete/drop/truncate/undo/reboot/scale-down… 命中即强制 confirm2，回滚计划豁免）——旧 `undo vlan` 回滚不再误报；执行/回滚循环按类型分发，全部动作审计 `proposal-write`/`proposal-rollback`；注入防线：上传路径白名单字符集（发布门禁 #2）+ 测试覆盖引号/`$()` 走私
- **人工终端 v2**（§6.1，`humantty.go` 重写）：真 SSH shell 通道——独立 transport client（不随诊断会话 idle reaper 抖动）+ PTY（xterm-256color 120×30，`WindowChange` 支持 resize）；输出流式回前端（`netdev:humantty` Wails 事件），UTF-8 截断序列跨 chunk 保全（GBK 回退同诊断会话策略）；**共享 VTY 预算**（`vtySnapshotLocked` 计入人工终端，占满即拒）；全程录制（8MB 尾环 → ANSI 剥离 + 脱敏落 `netdev/humantty/*.txt`）+ 起止审计（含字节量与录制路径）；紧急停止联动（`KillAllConnections` 杀人工终端）；e2e 三测（真 SSH 全链路：输出流/按键/录制；预算拒绝；解码器）
- **人工终端前端**（§10.5）：`DeviceTerminal`（@xterm/xterm，重挂先查 status 不重复开 PTY）挂进主区终端面板**设备页签**（路由徽标 + 常亮 REC 红点，与本地页签同条可共存）——App 层 `fairpeer:netdev-terminal` 事件路由（与 `netdev-bench` 同款），设备卡「⌨ 终端」按钮直达；bridge 增 `onNetdevHumanTTY` 订阅 helper + 全套 mock（浏览器开发可独立渲染）
- **割接模式**（§7.2，`internal/netdev/cutover.go`）：runbook = 已批准提案步骤 + 只读命令步骤，每步可挂**语义验证门**（正则须**持续** SustainSec 连续匹配，中断重计窗）与 `decision_point` 回退决策点；**总倒计时**耗尽即 hold 不再执行；割接前后自动各拍一次基线快照（复用配置备份库），结束产出前后 unified diff 对比报告（可导出 .md）；决策永远人按——hold 时 [继续] [回退] 并排，回滚走提案回滚全链路（首败冻结/备份恢复/审计）；测试 5 项（全链路/决策点回退/门失败/倒计时耗尽/未批准提案拒启动）全绿；前端 `CutoverView`（对话主区任务过程视图，不开第四工作台，Esc 返回；秒级倒计时 + 步骤清单 + hold 决策条 + 报告导出），入口在「提案」页签「🌗 割接」卡
- **文件通道 UI 收口**（§6.2/§10.5）：补 `NetDevSFTPDownload/Browse` 桌面桥（b4ba7e8 前端已声明、后端欠账——下载走系统保存对话框 + 审计）；设备卡「📁 文件」下载对话框（白名单路径浏览/拉取/落盘位置选择，无常驻面板）
- **提案中心结构化展示**：步骤类型徽标 + 按类型的载荷详情（k8s manifest/Up-Down SQL/上传路径与 reload/down 缺失⚠警示/危险动词强制确认标记）+ 观察期（watching）状态行；纯逻辑抽 `proposalStepFormat.ts` 进 tsx 测试（10 断言：类型摘要/降级/标签）
- 验证：netdev+transport 全量 `--count=1`、desktop 模块 build、前端 tsc + `npm run test`（含新增 proposal-step-summary）全绿；已知 v1 边界：迁移脚本不支持存储过程体（分号切分）、k8s apply Kind 白名单（20 种常见）、file-upload 仅 linux 目标、割接创建表单为提案挑选式（自由 runbook 编辑器随批量模板批次）

### 私有网信任域（Trust Domain）：fleet 级信任基础设施全量落地

设计文档 `docs/TRUSTDOMAIN_SPEC.md`（v0.13）：经完整排除法收敛，fairpeer 唯一立项的区块链能力——**许可链 = quorum 签名复制日志**，无代币无挖矿，链上只放控制面元数据（成员/撤销/令牌/审计锚/自证摘要）。账本"骑"在成员间 E2E 加密信道上，信任面由链本身驱动。

- **账本核心**（`internal/trustdomain`，9+ 文件，30 测试）：八类记录（成员/撤销/令牌/审计锚/自证/策略/终止/暂停/继任）+ quorum 写入权限矩阵（日常零控制、权力变更强控制）+ 哈希链连续性（删块/重排/替换皆可定位检出）+ 检查点分叉裁决（最近检查点优先→长度→哈希）+ 规则版本化 + 反滥用上限；对抗验收全过（spec §14.2：伪造签名/不足 quorum/删块检测/终止后延长/分叉收敛/撤销见即生效）
- **传输层**（`internal/trustdomain/nettrans`）：TCP + mobilebridge 同款握手（Ed25519 签名 + X25519 ECDH + HKDF + AES-256-GCM + 帧防重放，全部复用既有代码）——**握手双方各自从本地账本解析对方公钥**（未准入/已撤销静默断连）；`join <addr> <域ID>` 跨进程入域（域 ID 带外锚定 + 服务端签名事后验证）；**UDP 广播局域网发现**（信标签名/域过滤/±120s 新鲜度，零依赖三平台统一，mDNS 可后补）
- **授权与执行**：能力令牌四态验证 + **令牌委托链**（深度 1/范围子集/寿命更短/只有持有者可授；撤销授予者即杀死全部下级）+ `Delegation` 委托执行五关闸（新鲜度/身份/载荷哈希绑定/令牌/范围）——执行端 `WorkHandler` 只见已验证工作
- **治理闭环**：PAUSE 紧急刹车（quorum 签名，生效即停全部委托含只读——刹车从严）+ **失联继任**（dead-man 时钟用"记录签名时间戳 − 链内最后管理员活动"，确定性无本地钟依赖；继任记录无需 quorum）+ quorum 棘轮（只升不降）+ 终止/转生/归档三形态
- **审计互锚**（spec §八）：netdev 本地审计链头（B 批既有哈希链）经阈值/时限双触发自动上链（每 16 条或 10 分钟；失败保待重试，绝不阻塞诊断手）+ 手动 `anchor`；三层防篡改（本地哈希链→域账本互锚→跨网络副本）
- **CLI 十六命令**（`fairpeer trustdomain`）：init/identity/join/status/attest/admit/revoke/token/delegate/exec/sync/quorum/succession/promote/pause/resume/anchor/run（--listen/--bootstrap/--discover/--executor netdev）
- **Agent 工具**（AI 首次成为信任域一等用户，netdev 模式）：`netdev_fleet`（本地公告板：成员/自证/暂停态）+ `netdev_remote`（agent 表达意图，自动选择覆盖 (资源,操作) 的令牌——精确优先通配；无覆盖即拒并提示补签命令）；仅 `[trustdomain] enabled` 时注册
- **首个真实消费者**：netdev 只读诊断接入委托执行（`netdev/health` 健康面板、`netdev/triage` 主机体检电池——词汇表无法表达写操作，能力隔离）；双进程冒烟全链路（init→admit→join→token→sync→exec 跨机拉取 HealthSnapshot→越权本地即拒）
- **桌面面板**（设置新页签「信任域」）：域状态/成员卡（管理员高亮、撤销置灰）/本机令牌（含转授来源）/失联继任时钟 + 紧急刹车与互锚按钮；未启用/未入域给引导文案；i18n 双语
- **工程性**：账本热重载（mtime/size 监测，CLI 与守护共享数据目录的单写者冲突实践解除）；加载时全量重验证（磁盘不可信）；`--count=1` 全绿（trustdomain 30 + nettrans 8 + config 6 + cli/netdev 若干），tsc/CSS 令牌检查/双模块 build 通过
- 已知边界（spec §16 未决问题）：多管理员域的 UI 变更需经 CLI 网络节点（联署）；账本文件锁与控制端口为长期项；真机（Linux 无头优先）/VPS 验证待做


### 密钥存储统一升级：Win/Mac/Linux 同级加密（KEK + OS 密钥库）

背景：mac/Linux 桌面端此前用「主机名+home 目录派生密钥」的 AES-GCM 兜底（代码注释自称仅测试替身，但 release 实际发布全平台），任何本机进程都能重建密钥解密 `secrets.enc.json`——保护强度接近混淆。本次改造让三平台都达到「OS 用户凭据绑定」级别，`Store` 对外 API 与语义完全不变。

- **v2 文件格式**：`{"version":2, "kekId", "kek", "secrets": base64(nonce‖AES-256-GCM)}`——全条目 AES-GCM 于一个随机 KEK 之下；kekId 兼作派生盐与密钥库账户名，主存储与 `mobilebridge.enc.json` 各自独立 KEK
- **KEK 保管按平台分治**（`internal/secret/kek_*.go`，遵循仓库 build-tag 惯例）：Windows=随机 KEK 经 DPAPI 包裹存文件内（自包含）；macOS=Keychain、Linux=Secret Service（`zalando/go-keyring` v0.2.6，零 CGO，root go.mod 新增直接依赖）；无密钥库环境（headless Linux serve/acp/bot）优先 `FAIRPEER_SECRET_PASSPHRASE[_FILE]` argon2id 派生，仍无则降级机器绑定并显式告警（sync.Once stderr + 设置面板 `banner--warn` 徽标 + doctor secrets 段；`SecurityMode()`/`SecretStoreView` 三处消费）
- **KEK 解析带真伪校验**：候选 KEK 须实际解开已知条目（GCM 认证标签天然防错键）才被接受，口令/机器等确定性后端与密钥库后端可安全共存于一条优先级链；密钥库丢失时读路径维持「视为未设置、提示重录」旧语义，写路径报错而非静默换键搁浅旧数据（空存储例外，允许重建）
- **v1→v2 透明迁移**：旧每条独立加密的文件永久可读，任意写入触发一次性整体重加密；迁移中不可解密的条目（如跨用户拷贝的死数据）丢弃并 slog 告警
- **可见性**：boot 降级一次性告警（含口令设置指引）；`loadIntoEnv` 跳过不可解密条目时 slog 计数（原先全静默）；`fairpeer doctor` 新增 secrets 段（backend/降级/存储路径脱敏）；设置面板「更新」页尾三态显示（正常=安静提示 / 降级=警告横幅 / 密钥库不可达=错误横幅），en/zh 双语键同步
- 依赖：root go.mod + `github.com/zalando/go-keyring`（MIT；macOS 走 `/usr/bin/security` CLI，已知取舍是写入瞬间 KEK 经 argv 短暂可见，仅一次性生成时刻，代码注释说明）
- 验证：`internal/secret` 16 测试全绿（9 存量不动 + 7 新增：v1→v2 迁移 / KEK 丢失 / 空存储重建 / 错误确定性键拒绝 / 口令派生确定性 / 降级标志 / 双存储 KEK 隔离，经 `newWithKekProvider` 钩子注入 fake 后端做到 CI 无密钥库环境可测）；win/darwin/linux/freebsd 交叉编译通过；root+desktop 双模块 build/vet/test 全绿（cli/config/boot/doctor/mobilebridge）；前端 tsc 通过（`CoWorkLayout.tsx` 有一处并行改动遗留的未使用导入报错，非本次引入）；**真机冒烟待做**：macOS Keychain 授权弹窗体验、Linux 桌面 Secret Service 首次写入、headless 口令模式实测

## [0.1.10] — 2026-08-27

### 右栏「概览」下线——独有内容分流至用量条 / 轮次 tab

背景：composer 用量条的悬浮面板已覆盖概览的绝大部分内容（窗口占用/压缩比/命中率/费用/RPM/请求数/会话 token 细分），概览信息重复度高。三项独有内容分流后，编码模式 dock 的「概览」tab 改为「轮次」；办公模式概览同步瘦身（那边没有用量条，保留统计条兜底）。

- **UsageChip 补齐两项独有能力**：悬浮面板 Session 区新增「用时」行；用量 ≥85%（hot 态）时芯片尾部出现小号「压缩」按钮——编码模式的主压缩入口，点击触发 `app.Compact()` 并 toast 反馈（Tooltip 是纯 hover 非交互，按钮只能放芯片本体）；精简 ContextPanel 统计卡下方也保留一个常驻「压缩」按钮，作为办公模式（无用量条）的唯一入口、编码「轮次」tab 的顺手入口，两种模式压缩能力均不缺席
- **死 CSS 顺手清理**：`.context-inspector` 两处选择器（先于本次改动即无任何组件引用）移除
- **ContextInfo 新增 `sessionElapsedMs`**（Go+TS）：取自遥测累计活跃轮次时长（含在途轮次，非墙上时钟），驱动用量条与统计条；`ContextUsageForTab` 赋值，`tabs_telemetry_test` 补断言
- **ContextPanel 重写为「统计条 + 轮次表」**：4 张 MetricCard（会话 tokens/请求数/缓存命中率/用时，全部来自 ContextInfo props，**不再 2s 轮询 `app.ContextPanel`**）+ 最近 8 轮事实表（TurnFactsForTab 3s 轮询保留）；轮次表表头硬编码中文顺手 i18n 化（`turns.*`）
- **dock 模式 `"context"` → `"turns"`**：类型/目录/持久化过滤/渲染分支全量改名；旧持久化 dockTabs 里的 `"context"` 被 catalog 静默过滤；`SHOW_CONTEXT_DOCK` 开关删除；dock 默认打开模式改 `"files"`；tab 标签「概览」→「轮次」（Activity 图标不变）
- **死代码链清理**：Go 端删 `ContextPanel` 处理器 + `ContextPanelInfo`/`ChangedFileInfo` 结构体（契约测试同步收编）；前端删 bridge `ContextPanel` 接口/类型/mock、`ContextPanelInfo`/`ReadFileRecord`/`ChangedFileInfo` 类型；App 删四个 `openRightDock*` 处理器与 reveal/list 请求态（唯二赋值方就是被删处理器，reveal 链一并死亡）；WorkspacePanel 删 scoped 过滤视图全套机制（引用文件/会话变更过滤 + 相关 refs/effects/props）——该视图唯一入口是被删的概览预览按钮；useController 死回调 `compact` 移除
- **i18n/CSS/测试收尾**：context.* 块从 41 键瘦到 5 键（changedMeta 等仍有消费方的保留）；新增 `turns.*`/`rightDock.turns`；删 `workspace.filterReferencedFiles`/`clearFileScope`/`clearChangeScope`/`mock.changedFile*`；onboarding.css 的 `.context-panel` 大块裁到骨架（root/body/section/stats/metric）、misc.css compact-btn 删除、workbench.css 主题覆盖裁剪、panels.css `workspace-files__scope` 删除；删 `context-panel-breakdown.test.ts`（donut 配比测试，对象已不存在）及 package.json 条目
- **压缩按钮运行态收敛**：两处压缩入口（用量条热态按钮、精简面板常驻按钮）在活动 tab 流式进行中禁用（`state.running` 经 `dockBusy` 穿 CoWorkLayout→CoworkDock），避免压缩写了一半的上下文；`fmtDuration` 提取到 `lib/duration.ts` 双处共用
- 验证：`go build/vet/test`（desktop 全量）+ 前端 `build`（css 检查 + tsc + vite）+ `test:all` 全绿；vitest 曾现一次 worker 超时（Windows jsdom 抖动，单跑即过，非代码问题）；dev 服务器模块探针（7 个改动文件 transform 全 200）。**真机交互冒烟仍待做**：热态按钮（需真实 ≥85% 上下文）、「轮次」tab 轮次表（dev mock 的 TurnFacts 为空，需真实会话）、办公模式新概览
- 已知取舍：「用时」= 累计轮次耗时非会话墙上时钟（后端本就无 session start 追踪，如实命名）；办公模式概览降级为统计条 + 轮次表（用户裁定，压缩入口经统计卡区按钮保留）

## [0.1.10] — 2026-08-27

### 运维能力扩展 R1-R3（NETDEV_SPEC v2.0 落地）

spec 定稿见 `docs/NETDEV_SPEC_V2.md`（675 行，含 UI 契约与 15 条裁决）；本批落地前三批核心，全部密封路径（结构性只读不变）。

- **R1 日志与体检**：主区「日志工作台」（多源勾选 → 时间戳合并时间线，无时间戳行吸附前一条；底栏跨设备 IOC 搜索，预算耗尽如实报覆盖 N/M 台）；`netdev_triage` 主机体检电池（linux 11 项/windows 5 项，失败登录爆发/磁盘水位/uid0/时钟未同步四类保守异常自动进「发现」）；巡检家族菜单四件套（网络巡检/主机体检/基线核查/**弱口令核查**——后端闲置能力正式接线）；`crontab -l`/`lastb`/`nginx -t`/`apache2ctl` 等入读表
- **R2 容器与数据目标**：设备新增 `kind` 判别式（存量零迁移）——`docker`（只读 Engine API，npipe/unix/tcp，POST 无代码路径）、`k8s`（kubeconfig 入密钥库 + 固定 context + 命名空间白名单 + 防 SSRF）、`firewall`（FortiOS REST 只读，token/Basic）；日志源 `k8s:`/`docker:` 经 API 路由——VM journal、容器日志、Pod 日志可合并进同一条时间线；三客户端密封行为均被 httptest 假服务钉死
- **R3 事件与时序**：`netdev_locate`（IP/MAC 全网 ARP 扇出定位，清单页签顶部入口）；通知出口（webhook + 飞书/钉钉/企微原生模板 + 严重度过滤 + `fairpeer://` 深链）；时序面 v1（JSONL 零依赖 + SNMP 轮询采集 + 设备卡 Sparkline）；报告族四件（值班交接/周报/凭证盘点 + 既有晨报）
- **设置三级导航（§10.9）**：表单进三级页、弹框只留阻塞确认——L3 翻页动画（reduced-motion 降级）+ 六表单迁移 + 未保存确认 + kind 选择题表单
- **含上会话在途 P2 收口**：syslog 被动接收/告警规则/审计哈希链/日志 follow/DB 只读诊断源（mysql/pg/redis 白名单）
- **功能补全（0.1.10 增补二）**：DB 源 **Via 跳板链**（本地转发穿 SSH direct-tcpip，七引擎通用，生产库在堡垒后的正解）；SNMP **trap 接收器**（v2c/v1，link-down/cold-start 自动 Finding + 10 分钟去重）；**SMTP 通知出口**（与 webhook 并行）；**状态导出**（审计页签一键，JSON 快照不含密钥）
- **数据库引擎扩展（0.1.10 增补）**：新增 mongodb（canonical-JSON 命令白名单，$-操作符结构性拒绝）/ mssql（sys.dm_* 视图精确语句白名单）/ clickhouse（HTTP GET 接口，URL 编码天然防注入）/ elasticsearch（GET 端点路径白名单）——共 7 引擎；TiDB/OceanBase（MySQL 模式）直接用 mysql 类型；Oracle/达梦待驱动评估
- 验证：go 双模块 + `internal/netdev` 全量测试 + 前端构建全绿；**真机冒烟待做**（三个 API 客户端目前仅 httptest 验证）


对标 ZCode 远程连接的四步向导（选择方式 → 填写配置 → 连接中 → 选择目录），P1 交付 **WSL** 一种连接方式，Docker/SSH/Server 复用同一协议后续补。核心架构：**controller 跑在远端 headless host 进程里，桌面端经 stdio NDJSON JSON-RPC attach**——agent 工具、文件、git、会话存储全部在 WSL 内执行，桌面只是 UI 与转发。

- **WP0 host 协议**：新包 `internal/remotehost`（复用 `internal/acp` 的双向 JSON-RPC Conn）+ CLI 子命令 `fairpeer host`。方法面覆盖桌面驱动本地 controller 的全部操作（submit/steer/approve/rewind/fork/setModel 重建等 ~40 个）+ 文件/git（fs/list、fs/read 支持 dataURL 媒体、fs/search、git/status）；事件以共享 wire 形状回推（新包 `internal/eventwire` 双向编解码，与 desktop/serve 现有 wire 字段级一致）；审批/ask 以 host→desktop 出站请求回程（照抄 acp 模式，30 分钟超时拒识防挂死）。管道测试覆盖握手/configure/会话/fs/越界防护
- **WP1 桌面端接线**：`WorkspaceTab.Ctrl` 字段类型换为 `tabSession` 接口（字段名不变，`*control.Controller` 结构化满足，编译器裁定方法面 ~90 个）；新 `remoteSession` 全量实现（运行态/模式/上下文用量本地缓存 + 事件流回灌 `tabEventSink`，遥测/自动快照/mobilebridge 转发原样工作）；`RemoteRef` 持久化进 desktop-tabs.json 与项目索引（slug 前缀 `remote-wsl-<distro>-<user>-` 防撞本地路径）；`@` 引用/文件预览/git 面板/reveal 在远程 tab 分支走 RPC（reveal 打开 `\\wsl$` UNC）
- **WP2 WSL transport**：发行版探测（`wsl -l -v` UTF-16 输出嗅探解码）；Linux host 二进制下发（`%LOCALAPPDATA%\fairpeer\hosts` → distro 内 `~/.fairpeer/bin/fairpeer`，字节级比对免重复拷贝 + chmod）；`wsl.exe --exec` 拉起接管；断线 4 秒自动重连（按 pin 的 transcript 路径 reattach）；首连自动推送桌面端模型配置（仅远端无可用 provider 时落盘，不覆盖手工配置——密钥入远端 secret store + 进程 env）
- **WP2 终端桥接**：`PTYCreateForTab`——远程 tab 的 ConPTY 直接跑 `wsl.exe -d <distro> --cd <root>`，终端 day 1 进 Linux 环境，无需远程 PTY 协议
- **WP3 向导 UI**：`RemoteConnectWizard` 四步弹层（发行版下拉 + Linux 用户 + 实时连接日志 + 远程目录树选择）；项目树新增「远程连接」入口；文件夹选择器选到 `\\wsl$` 路径时提示改走 WSL 连接（对齐 ZCode wsl-unc prompt）；zh/en 文案
- **端到端验证**：真实 WSL Ubuntu 冒烟全绿（下发 87MB 二进制 → host/hello linux/amd64 → configure 推送 → 开会话 → fs/read 返回远端文件内容 → git/status → 干净退出）
- **Docker transport（+1，同协议复用）**：`docker ps --format json` 容器检测（NDJSON 解析 + 单测）、`docker cp` 下发 host 二进制 + `docker exec -i` 拉起；向导第二方式（容器下拉，镜像/状态展示）；连接管理器按 kind 分发 transport（wsl/docker，ssh/server 预留）。alpine 临时容器真机冒烟全绿（cp → exec → hello → configure → 会话 → fs/read "hi-docker" → 干净退出）
- **SSH transport（+1，同协议复用）**：复用 `internal/netdev/transport`（密码/私钥/ssh-agent 认证、`~/.ssh/config` 别名解析与向导入、系统 known_hosts + TOFU 落 `remote-known-hosts`、指纹冲突硬失败）；host 二进制经 exec stdin 流式上传（按字节数比对免重复传）；`fairpeer host` 跑在普通 session 的管道 stdio 上。凭据：RemoteRef 只持久化主机/用户/私钥路径（非机密），密码/口令入桌面端加密密钥库，重连自动取回；向导表单含别名导入下拉（复用 NetDevSSHImportCandidates）。真机 e2e 通过（alpine sshd 容器：认证 → TOFU → 上传 → host 拉起 → hello → configure → 会话 → fs/read，2.2s）
- **Server transport（+1，收官）**：`fairpeer host --listen <addr> --token <t>` TCP 模式（每连接 token 握手 `host/auth`，常数时间比对；**会话注册表跨连接共享**——桌面断线重连后按 id 找回会话）；桌面端 `serverTransport` 拨号 + 握手探测（错误 token 干净报错），token 入加密密钥库；向导第四种方式（地址 + token）。本机 e2e 通过（错误 token 拒绝 ✓ 断线重连会话存活 ✓）
- **终端桥接补全**：`PTYCreateForTab` 现覆盖三种远程——WSL `wsl.exe --cd`、Docker `docker exec -it`、SSH `ssh`（BatchMode，需密钥/agent 认证）
- **新建任务集成（ZCode 式）**：侧栏「新建任务」主入口改为下拉菜单（新建会话 Ctrl+N / 远程连接…→弹向导）；命令面板新增 cmd-remote-connect；Welcome 空态新增「远程连接…」虚线入口——三处均可唤起向导；项目树服务器图标保留
- **前端徽标**：TabBar 远程 tab 显示方式徽标（WSL/Docker/SSH/Srv，离线态红删线）；订阅 `remote:status` 事件刷新 TabMeta
- **生命周期补漏**：应用退出时统一关闭全部 host 进程；向导重试/放弃时回收无会话的空闲连接（不残留 WSL/Docker/SSH 进程）
- **收尾件**：`scripts/build-hosts.sh`（交叉编译 linux amd64/arm64 host 二进制入缓存）；`TabMeta` 增加 `remote`/`remoteState` 字段
- **安全闭环（+1）**：SSH 首连从静默 TOFU 改为**显式指纹确认**——向导新增「验证主机」步骤（SSHInspectHost 取 SHA256 指纹 → 用户确认 → SSHTrustHost 写入本地 known_hosts），传输层对未信任密钥一律拒绝、指纹冲突硬失败；Server 新增 `--tls`（自签证书持久化于 config 目录，指纹跨重启稳定），桌面端 TLS 拨号**首连锁定证书指纹**、此后强校验（`ServerForget` 清除令牌+指纹）。e2e：未信任拒连 ✓ 信任后连通 ✓ 错误指纹拒绝 ✓
- **远程话题侧栏枚举（+1）**：host `session/list` 富化（.meta 侧车的 turns/topicID/标题/预览）；新 `remote-projects.json` 注册表；ListProjectTree 合并远程项目节点（在线时按话题聚合 turns/最近活动，离线显示裸节点）；点击远程话题经 `OpenRemoteTopicTab` 打开并 pin 最新会话。远程工作区在侧栏的行为与本地项目对齐
- 附带：cowork/onboarding 遗漏的圆角令牌化补齐（regex 全量）
- 已知边界（P1）：远程 tab 的记忆/技能/MCP 热插拔/专家协作为本地特性暂不可用（接口返回 not-supported）；远程会话离线时不在侧栏枚举（重连后恢复）；ExpertCollab/Item 事件不在 wire 上（与桌面本地 toWire 行为一致）
- **测试套件修复（配合分支 WIP「0 轮话题在树中隐藏」的新行为）**：新增 `seedTopicTurn` 测试助手（写一条 user 消息 + `.meta` 侧车使话题计 1 轮），9 个 topic 可见性测试按新行为对齐；修复 WIP 中 `reviveParkedTabs` 的死过滤条件（停靠态 `Ready=true`，原 `!Ready` 永不匹配——改为 `Ctrl==nil && StartupErr==""`），`TestSetDefaultModelRevivesParkedTabs` 转绿；**desktop 全量测试套件自 WIP 以来首次全绿（142s ok）**

### 歧义收口 + dream 泄漏修复 — 08-27

- **context7 整体撤除**：`internal/builtinmcp` 包删除（含测试）；config/render/profiles/boot/control/desktop 全链路清空——`[builtin_mcp]` 段不再渲染、`HiddenPlugins` 只留 codegraph、ccswitch 文档 MCP 表去项；`UpsertPlugin` 预留名守卫（feishu/lark/qq/weixin/telegram + codegraph/context7）拒绝混名安装并给可操作指引（机器人网关不是 MCP server），存量守卫前条目仍可替换；3 个守卫测试
- **dream 任务书泄漏根治**：后台自进化子代理原先复用宿主 tab sink，任务书正文流进转写/输入框（ppt-capability-upgrade-spec P3 根因 agent.go:721）——新增 `quietDreamSink` 只放行 Usage/Notice/TurnDone（完全隐藏；DreamRunView 另行报告状态）+ 2 个测试
- **TestTurnDone 确定性重构**：改为先等自动保存循环 idle 再读文件一次——写者仍在飞行时轮询读文件正是全量负载下偶发失败的窗口；连过 3 轮
- **UsageChip 悬浮面板验收 + 补键**：无头 Edge 悬停 mock 截图确认面板（42,124/128,000 · 32.9% · 平均缓存命中率 94.2% hero 行 · 会话累计分区，证据 `gui-test-screenshots/usage-hover-panel.png`）；验收抓出并补齐 zh/en 缺失的 5 个键——elapsed（用时行曾裸显键名 `composer.usageDetail.elapsed`）/compactLabel/compactHint/compactDone/compactFail

## [编码体验升级 M0–M3] — 2026-08-21

对标 Codex CLI / Pi 的三方合流升级（规格书 `docs/FAIRPEER_UPGRADE_SPEC.md`，51 任务首批落地 32 + 测试清零），7 个 commit 自 `ff965a2` 起。

- **M0 断链清零**：FileDiff wire 透传（wireTool/wireApproval/wireUsage 补字段，desktop+serve 双传输同步）、apply_patch/doc 族 Previewer、审批富上下文（逐文件 diff + bash 命令 + j/k/e 导航）、reasoning Markdown、成本/RPM 显示（激活 netdev 区死接口）、⌘K 真实现、输入历史（Alt+↑/↓）、删除 4 个孤立组件
- **M1 展示重构**：UnifiedDiff（词级 intra-line、行号、Unified/Split 切换，服务端 diff 为准）、工具卡注册表、web_search/netdev 专用卡、只读分组显示 subject、Turn 改动汇总卡、失败重试、备份 diff 统一、bash 头尾折叠
- **M2 进度队列**：Phase 事件带工具名序号、thinking 标题提取、FollowUp 队列（Alt+Enter / 忙时默认排队为独立下一 turn + 自动排水）、队列条可视化、TodoPanel 进度条、AgentDashboard（Ctrl/Cmd+I）
- **M3 循环补课**：写工具按路径集合并行（同路径串行）、集中参数校验、办公产物可 rewind（还原点 Previewer）、rewind 前逐文件 diff 预览、Proposal 应用内审批对话框、cowork 终端接入
- **预存失败清零**：ListBranches 适配每会话目录布局（真 bug：新布局下分支列表恒空）、会话路径测试对齐存储重构、签名公钥期望 ID 更新——**测试套件首次全绿**
- 遗留与勘察：3-3 流式补丁预览、3-7① MCP 进度透传（勘察就绪）、3-4/3-10/3-11 及阶段 4/5 详见 spec 附录 G

### 模型理解路由层收敛（非智能体层）— 08-21

设计定稿 `docs/MODEL_ROUTING_SPEC.md`：两键正交（模型 ID→行为纠正、端点 BaseURL→线格式方言，`reasoning_protocol`=人工覆盖逃生口），五站点职责表 + 六条受保护不变量（非智能体/Build 期一次定型/切模型=全量重建/addon 只对实测失效模式开/协议错字必报/方言嗅探保持精确主机名）。落地修复：

- **家族嗅探精度**（`instruction/family.go`）：子串 Contains 改两级匹配——供应商前缀精确（qwen/ deepseek/ z.ai/ moonshot(ai)/ minimax(i)/ openai/ anthropic/）+ token 边界整词（`glmw-v2` 不再误判 glm）+ 数字融合规则（`qwen3-max`→qwen）；家族表补 anthropic(claude)、o1/o3/o4
- **端点方言逃生口**：`reasoning_protocol` 新增 `"minimax"` 显式值——代理/网关 BaseURL 防不住主机名嗅探，显式声明强制 thinking.type 方言；未知值不再静默吞为 auto，构建期报错并列出合法值（boot 统一校验覆盖 anthropic 路径）
- **注册表每模型数据保留**：`mergeRemote` 不再丢弃 models.dev 的每模型 `Reasoning` 标志（`ProviderTemplate.ReasoningModels`，仅 UI 展示，行为层不读——依赖方向约束写入 spec）
- 测试：family 碰撞表扩充、协议显式 minimax/未知值拒绝、既有全量绿；boot/desktop/wails 打包通过
- **推理标记接 UI**（C 项收尾）：设置页"发现的模型"候选列表与引导页模型下拉对 models.dev 标记的推理模型显示"推理"徽章（`ProviderView.ReasoningModels` 由注册表按供应商名关联下发；网格三列布局容纳徽章）；mock/类型/locale 同步

### 命名/描述歧义清理（skill + MCP）— 08-27

审计结论：browser↔desktop、document↔ppt 两对边界是互斥声明的范本；实修 5 处真歧义 + 1 处改名 + feishu 同名异物根治：

- **feishu 同名异物根治**：MCP 安装三路（市集/AddMCPServer、install_source、配置直写）全部汇于 `config.UpsertPlugin`，新增保留名守卫——IM 机器人通道名（feishu/lark/qq/weixin/telegram）与内置运行时名（codegraph/context7）不可用作新 MCP 名，报错附改名建议（如 feishu-docs）；守卫前的存量条目仍可原地编辑不被砖死；机器人网关设置页文案明确"通道=IM 消息网关，与 MCP 服务器无关"
- **skill 描述五处**：research 首句立判别（EXTERNAL 库/文档问题 vs explore 的 OUR 代码勘察，双向互指）；review 去掉裸 security 一词并指向 security-review；netdev-playbook 定位总纲入口、netdev-diag-* 三张专项卡回指总纲（领域重叠但层级声明了）；codegraph SteerText 增补双身份说明（codegraph_* 工具与插件列表 codegraph 是同一运行时的两种模式）；install-capability 与设置页市集互指（技能=任意来源+卸载，市集=官方注册表浏览）
- **rag-auto → knowledge-auto 改名**：名字说技术（RAG）不说职责（知识库）；legacySkillRenames 保留旧名兼容（白名单/禁用表继续生效），roster/办公白名单/办公路由表/UI 官方集/测试全量同步
- mock 清理：figma/github/linear 三个假"随包发行"服务器移出浏览器 dev mock（从未真实存在，误导开发者）
- 测试：保留名守卫 3 个新测试（拒绝+改名提示/存量可编辑/大小写），全套绿；wails 打包通过
- **"feishu 顶替 IM bot"错位清扫**（同日续修）：审计全部 feishu 出现点——配置结构层（FeishuBotView/QQBotView/WeixinBotView 按通道各归各、allowlist 的 feishuUsers 等为通道专属）本就诚实；真错位在三处 label 回落（连接表/安装目标/侧栏 IM 平台的 default 分支把未知平台显示成"飞书"）改为显式 feishu 分支 + 未知平台原样显示（新 `settings.botChannelUnknown` 兜底），未来新增平台永不冒名飞书；顺带移除六个已无渲染点的 legacy 飞书表单文案键

## [0.1.9] — 2026-08-19 ~ 2026-08-21

三天四线并进：**编码/办公/运维三界面框架统一**、**循环工程上线**、**运维模式能力大批次（安全护栏 → 连接扩展收官）**、**技能体系架构重整 + ppt-auto 提速 + codegraph 分发根治**（后者为 08-21 架构专项，设计全记录于 `docs/SKILL_ARCHITECTURE_SPEC.md`）。无破坏性配置变更；codegraph 默认开关语义有一处显式调整（见兼容性）。

### 供应商接入 — 08-18

- **内置本地供应商**：Ollama / llama.cpp 以 keyless 模板随每个安装开箱可用（免 API key、免手填 URL）；实现在 config.Load 时注入（`BuiltinLocalProviders`），刻意绕开 TOML array-of-tables 按下标字段合并会覆写用户 `[[providers]]` 的坑——Default 保持 nil、只在没有用户 providers 时注入
- **models.dev 动态刷新**：注册表四层降级（远程 → 本地缓存 → 内嵌快照），xAI 等新厂商数据走实时刷新而非硬编码；无密钥供应商通过显式闸门（Validate / 桌面模型选择 / CLI modelRefs）但排除在 fallback 解析外
- **模型分类**：`IsLikelyChatModel` 补 imagine/video 等非对话 token，避免把生图/视频模型当对话模型列出

### 三界面统一 — 编码 / 办公 / 运维（08-19 ~ 08-21）

以编码界面为唯一基准，办公/运维两个模式全面学习其框架——品牌行、侧栏、顶栏、右栏四层全部同构：

- **骨架对齐**：办公/运维侧栏改为**全高贯穿顶栏行**（原从顶栏下方才开始，左上角空缺、logo 无处安放）；品牌行与编码同款同位置（logo → 模式名 → 工作区胶囊的演进同步三份实例），可折叠（品牌行收合钮 + 折叠后 chrome 展开钮）、可拖宽（共享 `--sidebar-width`）；侧栏根不滚动、品牌行钉住、下方内容自滚，与编码 `.sidebar` 完全同构
- **单头 chrome**：移除 `modeChrome` 特例——命令面板（⌘K）/终端（Ctrl+`）/工作区开关/三档切换器右簇**三模式完全一致**；办公头 = topicbar 且所有面板常显（原切日历/专家团时整条消失）；运维头 = 同一 topicbar 前置 + 运维 chip（网络名/项目/[诊断·只读]/紧急停止）居右
- **DockTabs 公共组件**：编码右栏的浏览器式页签条逐行抽为共享组件（只显示已打开页签、逐个可关、+ 号目录下拉、localStorage 持久化、首帧坐标预钳位防跳变）；办公（今日/邮件/文件/概览 + 知识库三页签）与运维（设备/手册/拓扑/发现/提案/审计）全部接入；**关掉最后一个页签 = 收起整栏**
- **运维右栏补齐编码交互**：chrome 工作区开关可开合（`netdevDockOpen` 状态机接入 toggle/close 链路）、宽度走共享 `--workspace-width` 可拖宽、拖到边缘自动收起；拖宽器按编码同款网格专列放置（不压底部统计条）
- **运维左栏章法重整**：搜索框 → 诊断会话 → 项目工作区 → **左下角导航组**（设备清单/立即巡检/审计/运维偏好，与编码"编码偏好"、办公"办公偏好"同组同款）；设备清单移入右栏「设备」页签（分组 + 堡垒链 + 上次巡检）；ProjectTree profile 三态（运维获独立项目分区 `projects-netdev`）
- **配色全面 token 化**：清理运维页散落的写死色（蓝 `#7ab8ff`/青 `#00ffcc`/红 `#ff3b30` 等及各色 rgba 底/辉光）与终端风装饰（等宽标题/辉光/大写停止钮），全部换 `--accent/--ok/--warn/--err/--danger/--fg*/--bg*` 语义 token——除语义需要外一律跟随全局格式，浅色主题下不再发灰发脏；拓扑图 SVG 的假 token 回退值（`--text-dim`/`--accent-dim` 未定义）一并修正
- **杂项修复**：全局横幅（启动错误/更新提示）三模式可见（原挂在被隐藏的 chat-pane 里永远看不见）；终端面板单实例（Ctrl+` 三模式可用，运维内不再双重挂载）；`/` 键聚焦搜索框修复（ref 曾落到 CSS 隐藏的副本上）

### 右栏页签与面板体系 — 08-19

- **双轴面板体系 + ZCode 式单头**（pane-system P1-P3 伴随层）：topicbar 并入顶栏 chrome、右栏改页卡式工作台
- **浏览器式页签管理**：右栏页卡可关闭、+ 号下拉添加（目录化）；folder-tab 页签语法（梯形斜边、激活页与内容区连体）；关闭钮与 + 号多轮打磨（19px 命中区、主题色描边圆钮、下拉首帧钳位防两帧抖动）
- **副会话并入右栏**为第四页卡（与底部入口并存），1:1 复用主对话消息渲染（Markdown 一致）
- **面板体验补完**：终端历史持久化、预览前进后退、后台任务 chips
- **可控浏览器导航**：`OpenTab`（CDP /json/new PUT）+ 预览页卡一键拉起
- **侧栏顶部换新**：新建会话并入品牌行（幽灵图标钮，退役全宽渐变大按钮）、搜索改静默条（mono ❯ 提示符 + / 键帽、全局 / 键聚焦）
- **底部状态栏退役**：状态行（模型名/缓存%/轮次）整体移除，输入框贴到窗口底缘；底部条收窄为只盖侧栏列（宽随 `--sidebar-width` 同步动画），历史/回收站/设置等图标迁入；侧栏底预留 36px 防遮挡
- **上下文用量条（UsageChip）**：输入框参数行 46px 填充条 + `会话token · 已用/窗口`（≥85% 警示色），替代原悬浮提示
- **用量条悬浮详情**（08-21）：悬浮/键盘聚焦展开面板——上下文精确值+压缩线、**平均缓存命中率**（Σhit/Σ(hit+miss)，telemetry 会话聚合，覆盖所有供应商）+ 本轮命中率（实时 wire 事件）+ 缓存读/写、会话累计输入/输出/思考/请求数；后端 `ContextInfo` 顺手带出 7 个 telemetry 聚合字段（零额外轮询），Tooltip 新增 `bodyClassName` 支持富面板
- **两级运行状态**：背景运行（phase"嘎吱嘎吱"）收进参数行小 chip 不占行高；需要介入（已暂停）才在输入框上方亮警示条——PhaseCard 逐行渲染退役

### 循环工程（loop）— 08-19

- **引擎 MVP 全量上线**（spec: `docs/loop-engineering-spec.md`）：五段闭环（传感器→agent 轮→验收 exec 直跑→失败 git 回滚→记账）/ 预算停机（轮次+单轮超时+熔断：连续 3 轮无进展中止、探索模式连续 2 轮无新问题自然完成）/ 自治分级 L1 只读·L2 每轮验证回滚·L3 档位预留
- **目标锚定**：显式选择项目会话（全局会话拒绝并提示）、全程固定工作目录——切走标签页不漂移；状态携带 workspaceRoot 供审计
- **LoopPanel**：左栏 6 个预置案例（测试修复/债务清扫/覆盖率爬坡/依赖升级/文档追补/只读巡检）+ 任务队列（睡前挂多任务串行链启）；右栏配置/运行时间线（✓/↺/– 每轮记录）/晨报三态；紧急停止常驻 + 全局 Ctrl+Shift+\；入口弹框化 + profile 切换自动关闭（三界面独立）；浏览器 dev 附 3 轮 mock 模拟器

### 运维模式（netdev）— 08-19 ~ 08-21

- **安全与护栏**（08-19）：智能体优先四原则落地——密封路径堵洞/知识一键生长（未知只读命令经用户确认入读表）/提案行内审批；护栏下沉到每次询问与每条命令（`confirm_each_command` / `turn_command_budget` / `allowed_groups`）；配置安全基线核查（密封读 running-config + 本地规则电池：Telnet/SNMPv1v2c/明文密码/SSHv1/NTP/Syslog）
- **诊断与拓扑**（08-19）：拓扑渲染纪律——本地 IP 规划推断为主（零设备会话零模型调用）、LLDP/CDP 实测按需校准；交互式拓扑（节点点击→设备卡，读即抓取/写走提案）；项目（站点）作用域进标题栏切换器并过滤全栏；诊断提示词第 8 条（mermaid 画邻居关系/故障路径/排查流程，聊天内已集成渲染）
- **随行资料**：内置技能 netdev-help + 提示词附录 #9；求助指引速查表 + 使用逻辑总图文档
- **连接方式扩展收官**（08-21）：ESXi 驱动（虚拟化层入网）、Redfish 带外通道（GET-only 客户端 + 路径白名单）、Linux/Windows 服务器驱动 + SNMP 指标通道
- **实用能力三连**：备份版本库 + diff、诊断命令组合、每日简报；备份后版本列表自刷新
- **崩溃根治 ×2**：拓扑图空值加固（无设备时实测快照 null 致 exe 崩溃）；真包根因——Go nil 切片 = JSON null 的序列化契约
- 界面对齐编码侧（品牌行 + 常显三档切换器，详见"三界面统一"节）；设置页减负（操作回运维页、配置留设置）

### 会话存储重构 — 08-21

- **每会话独立目录**（按用户决策：历史数据可弃，重规划不迁移）：`<sessions>/<yyyymmdd-hhmmss-xxxx>/<同id>.jsonl`，同名 `.meta/.ckpt/.present` 全部收进会话目录；全局（dev=`sessions/`、cowork/netdev 分区子目录）与项目（`projects/<slug>/sessions/…`）**完全同构**；日期前缀天然时间序、文件名=目录名保 BranchID 唯一、目录永不会挤满几千个文件
- **全局目录固定化**（前置修复）：空根会话统一落 `<userDir>/sessions[/<profile>]`——根除 dev 全局曾按进程 CWD 漂移落进随机项目目录的缺陷；`desktopSessionDir("")` 删除 Getwd 回退
- **双布局兼容读取**：ListSessions/findTopicSession 同时扫描平铺旧布局与会话目录（旧数据仍可见，无需迁移）；回收站 flatten 入 `<trash>/<id>.jsonl/`、恢复统一落目录布局；RestoreSession 话题索引指向新路径（修复恢复后项目树断链）
- 测试：新布局生成/双布局列举/回收往返 3 个新测试 + 旧断言升级；附 `TestGlobalSessionDirFixedPerProfile` 防 CWD 回归

### 全局域退役（三模式项目严格隔离）— 08-21

- **决策**：全局（scope=global）"又麻烦又容易出问题"，三模式全部取消——每个会话必须属于一个项目，dev/cowork/netdev 项目互相严格隔离
- **工作台项目取代全局**：每 profile 一个真实项目根 `<configDir>/fairpeer/home-<profile>`（标题"工作台"），常显于项目树/胶囊目录；新建会话/首启/定时任务/移动端全部落工作台
- **会话零迁移**：工作台的会话目录路由覆盖到原全局分区（`desktopSessionDir(For)` 识别 home 根 → `SessionDirFor(profile)`），旧全局 transcript 原地可达；索引/标题/创建时间一次性迁入工作台（`migrateGlobalIntoHome`，启动+树构建双触发、幂等）
- **旧入口全垫片**：`OpenGlobalTab`→工作台项目页签；`EnsureBlankTab`/`CreateTopic`/`ensureTopicIndexed`/`restoreSessionTopicIndex` 把 global/空根统一归一为工作台项目；持久化 global 页签恢复时改挂工作台（topicID 保留）；历史会话恢复/legacy 迁移收编进工作台
- **前端摘除**：胶囊去"全局"入口与 global 态（三模式统一）；历史面板范围筛选只剩全部/项目；blankSessionTarget 恒 project；locale/mock 同步收敛
- 兼容性：旧 `global_topic` 侧车与 `GlobalTopics` 索引字段保留解析（迁移后置空）；IM bot 无根映射经垫片落工作台
- 测试：11 个全局行为断言重写为工作台语义（迁移/恢复/重排/空树/并发），全套通过
- **跨 profile 项目污染生成级根治**（同日续修，fairbox 事件）：两个生成源头——①pre-profile 时代的共享最近项目列表（desktop-workspaces.json）被 ListWorkspaces 灌进"当前 profile"（新开运维界面第一跳就吸进编码根）；②模式切换携带激活项目根跨 profile。修复在生成端：共享列表一次性归入它所属的 dev 索引后**删除文件**（RememberWorkspace 死代码一并退役），三 profile 的项目生成从此只读各自索引、无任何共享源；模式切换不再携带根（目标模式落本模式工作台）。归属判定=有真实使用（非默认标题话题或分区会话文件）；启动 `pruneForeignProjects` 归一化存量污染、外来根持久页签恢复落本 profile 工作台——这些只处理 pre-isolation 遗留数据，正常运行中无可拦截之物（隔离是不变量，不是防御层）；新增 5 个隔离回归测试
- **全路径隔离审计**（同日）：逐条核验所有写索引/建页签/收编会话的生成路径——**修补一处**：legacy 无 scope 会话收编原传 dev 分区目录（config.SessionDir）给任意 profile（新开运维界面会把编码时代旧会话吸进运维工作台），改为只读本 profile 分区；`addProject` 本体成为注册门（外来根结构上不可写入索引，未来任何调用方都无法生成跨 profile 内容）。核验无问题：`pruneGhostProjects`（按 profile 判留存）、bot 无根映射（垫片落配对 profile 家）、`listSessions`/回收站（partition 归属过滤）、loop（强制 dev）、netdev 站点项目（自有设置存储）、页签恢复（外来根改挂本 profile 家）、移动端 NewTab（走 EnsureBlankTab 归一）；新增分区纪律回归测试
- **全局死代码清扫**（同日收尾）：ProjectTree 的 global_folder/global_topic 渲染分支、`__global__` 排序 token、global 主题色 CSS、TabBar/标题助手的 global 回退全部摘除（后端已不产出该类节点，属纯死代码）；IM 来源徽章改为按 topicId 直查；autosave 时序测试（TestTurnDonePersistsSession）等待上限 2s→10s，根除全量高负载下的偶发超时

### 桌面体验 — 08-19 ~ 08-20

- **生产构建原生右键菜单**：打包后所有输入区/正文恢复复制粘贴等右键功能
- **工作区胶囊**（design A）：左上角从模式名升级为"我在哪"；侧栏品牌行三模式各显其名；品牌行去 logo 统一章法
- 侧栏密度收紧（最近/项目各约 6 行视口）；z-index 守卫两处 token 合规
- **启动健壮性**：陈旧模型在 Build 咽喉处回退（启动永不被已删除的 provider 砖死）；陈旧页签模型回退本地预设
- Onboarding：卡壳定高 + 步骤内部滚动（完成按钮不再被顶出）；厂商卡 flex 布局（修复 WebView2 grid 折叠缺陷）
- Registry：Moonshot 引导链接更新 Kimi 品牌域名；MiniMax/讯飞/小米取钥直链校正
- **项目树右键菜单扩充**：复制会话 / 导出（Markdown·JSON·PDF·长图）/ 改动页卡直达 / **复制任务路径**（topicId 反查 .jsonl 存储路径）——顶栏三个常驻按钮退役入菜单
- **编码偏好/办公知识库遮挡修复**：侧栏内联 paddingBottom 覆盖了底栏预留（被 36px 底条盖住"消失"）、办公滚动容器 8px 写死——统一走 `--bottom-bar-height` 单变量
- **阅读列流体化**：`--maxw` 由固定 768px 改 `min(92cqw, 1440px)`，最大化窗口时消息/工具卡/输入框随聊天区铺开
- **会话条目紧凑化**：话题行 34px→28px、12px/400 字重与"最近"列表同款；用户气泡 88% 宽
- 顶栏 chrome 底色统一 chat-bg（主题层白渐变旧规则退役）；「最近」→「最近会话」；办公今日简报邮件图标与同级统一 fg-faint

### 技能体系架构重整 — 08-21（spec: SKILL_ARCHITECTURE_SPEC）

- **computer-auto → desktop-auto**：名字收窄为"桌面 GUI 自动化"，技能体/路由表/界面描述三处写入**代码优先原则**——能用代码直调（文件/进程/系统信息）绝不模拟鼠标键盘
- **技能三分域**：编码（dev）/ 办公（cowork）/ 运维（netdev）由 profile `enabled_skills` 白名单强制；netdev-help 收编运维域（原系名单漂移漏网的颠倒状态）；cowork 从全量暴露改为办公白名单；research 归纯编码域
- **MCP/工具域门控**：新增 `hidden_plugins` 按名隐藏机制（不动用户自装 MCP）——codegraph server / context7 / lsp 三件挡在办公/运维模式外；**修复 codegraph 注入绕过白名单的历史 bug**；`builtinBuiltinSkillNames` 漂移修复 + 漂移守护测试（编译期拦截此类 bug）+ 旧名 computer-auto 白名单自动迁移
- **调用控制三原则**落地：per-skill `max-steps` frontmatter（ppt-auto 设 80）；desktop-auto"每步必复验"改**检查点复验**；全技能审计表入 spec
- **描述修复**：explore 不再揽 review 的活；document-auto 与 ppt-auto 划清 .pptx 边界；netdev-help 英文化；install-capability 自解释
- **codegraph / context7 统一 opt-in 默认关**
- **派生可编辑副本**：能力面板一键把内置技能落成 `~/.fairpeer/skills/` 文件副本（同名遮蔽即生效，删副本即还原）
- 前端：能力面板新增"运维"域分组；技能选择弹窗描述修正；`fairpeer.example.toml` 补 profiles/hidden_plugins 文档块

### ppt-auto 提速与调用控制 — 08-21（SkillVersion 43 → 45）

- **页面骨架生成器**（最大收益）：7 种页面类型（封面/目录/章节/卡片/两栏/要点/结尾）由紧凑 pages.json 一次生成全部 SVG——每页 ~300 token 替代手写 ~5K token；validate 全量检查 7/7 通过（cards/列自适应行距 + 底部主题线满足四带覆盖；check_svg 给章节页密度豁免）
- **preflight 一步化**：模板提色 + 视觉合并 + 项目 init + 配置摘要 JSON 合为一次调用（原 5 个回合）
- **批量 fix+check**：单解释器循环处理全部页，替代每页两条命令；fast/validate 模式沿用
- **QA 采样**：`qa_compare --sample N`（fast 模式封面+等距页，砍 VLM 主要耗时）
- **图标占位符正规化**：SKILL.md 写明 `<use data-icon>` 语法（11600 图标库从"白放"变正式入口）；icons README 幽灵工作流（引用不存在的 icon_sync.py）修复
- description 重写（五条路线全覆盖 + 与 document-auto 划界）；cowork 路由行去实现细节 + 补修改模式提示
- **首启依赖后台预装**（marker 守卫）；**PDF 参考预分析后台补齐**（同步 6 页后 goroutine 跑满剩余页）

### codegraph 分发可靠性 — 08-21

- **runtime 随包内置**：`codegraph_embed` 构建标签 + CI 按平台抓取资产暂存——**发布版首启零网络，"下载不下来"失败类别整体消失**；嵌入式字节同样过编译期 SHA256 表
- **镜像链安全收敛**：公共 ghproxy 代理全部移除（匿名运营端点，校验保护内容不保护接触面）——终态为自定义 download_url → GitHub 直连，仅开发构建使用
- 失败通知附行动指引；research 提示词补"codegraph 不可用时 grep 兜底"

### 发布工程 — 08-19 ~ 08-21

- **打包教训成文**（08-19）：桌面产物必须 `wails build`，裸 `go build` 不可分发（窗口不出现 + 体积 +17MB）
- **双分发模式**：Windows 同发 NSIS 安装器 + 免安装便携 exe（原 CI 会丢弃便携版，已改双产物）；**manifest 钉死 installer**——Windows 更新器对下载物直接当安装器启动，matchPlatform 跳过 `-portable.`（与 .deb 同款先例），防便携版随机抢占更新通道
- **更新检查失败不再骚扰**（08-21）：GitHub 与镜像均不可达时（典型：受限网络每次启动），启动自动检查曾把整串原始错误（多端点 URL + transport 错误链）甩成顶部横幅"更新失败：…"。现拆分 `checkError`（检查失败）与 `error`（安装失败）：自动检查失败**完全静默**（网络状况非用户可行动项），手动检查仍在设置面板内联显示；后端 `fetchManifest` 错误缩短为一句话（"N 个端点不可达，详见日志"），完整逐端点明细写入日志

### 环境预设护栏（ambient presets）— 08-21

- **修复"没选 Ollama 却被 Ollama 接管"**：`config.toml` 无任何 `[[providers]]` 时注入的 keyless 本地预设（Ollama / llama.cpp）此前可被陈旧页签引用的回退链捕获——所有页签静默变成 `ollama/qwen3-coder:30b`，聊天实际发往本机 11434。引入 **ambient 语义**：注入时打运行时标记（`injectedLocalPresets`，不落盘），未进 `desktop.provider_access` 的预设**只可见、不可被选**——`ResolveModelWithFallback` 直接解析与回退循环双双跳过，页签持有此类死引用时置空并停入 Welcome（附通知），不再报 "unknown model" 启动错误
- **保存不再固化预设**：渲染器跳过 ambient 预设——此前在"纯预设配置"上任何一次设置保存都会把 Ollama/llama.cpp 写成永久 `[[providers]]`（并使其脱离 ambient 语义）；用户在向导/设置里显式添加（进 provider_access）后照常落盘
- **边界保持**：用户手写 `[[providers]]` 的 keyless 供应商（非注入、无标记）回退照旧（240faf1 的防砖语义保留）；CLI `--model ollama/...` 显式点名走 `ResolveModel` 直达不受影响；向导添加过的预设（在 access 内）回退/解析均正常。测试：`TestResolveModelWithFallbackSkipsAmbientLocalPresets`、`TestRenderSkipsAmbientLocalPresets`、`TestStaleTabModelWithOnlyAmbientPresetsParksInWelcome` 等
- **设置默认模型唤醒全部停驻页签**（08-21）：`SetDefaultModel` 此前只更新活动页签——其他项目页签停在 Welcome（模型/Label 为空），输入框不显示默认模型，直到发出第一条消息才按需重建。新增 `reviveParkedTabs()`：设置默认模型成功后扫描所有"Ready 且无控制器且无启动错误"的页签逐个重建，空模型自动解析为新默认值；向导（SetupProvider → SetDefaultModel）与设置面板两条路径一并覆盖。测试：`TestSetDefaultModelRevivesParkedTabs`

### 输入框工具权限改下拉框 — 08-21

- **三连钮退役**：输入框底部"询问 / 自动 / YOLO"三段式切换（滑块 modebar）改为与模型/力度/知识库同款的**下拉框**（`ApprovalModeSwitcher`）
- **命名直白化**：`询问 → 变更询问`、`自动 → 自动编辑`、`YOLO → 完全访问`——中文统一四字对齐（英文 Ask first / Auto-edit / Full access）；状态栏徽标、设置页、快捷键提示（Ctrl/Cmd+Y 切换完全访问）全量同步，"YOLO批准"等旧叫法清除
- **菜单紧凑、说明悬浮**：下拉项为单行（图标 + 名称 + 勾选），每档的完整说明悬浮显示（Tooltip），打开即可扫读、悬停即见全义；闭合触发钮同样悬浮显示当前档说明
- **安全态可视化**：闭合触发钮按档位着色——自动编辑淡蓝、完全访问红系警示（浅色主题下前景色由模式色派生保证可读；theme-style 变体下同样生效），不点开也能一眼看到当前授权姿态
- 行为零变化：三档语义（ask / auto / yolo）与后端 `SetToolApprovalMode` 通道、Ctrl/Cmd+Y 快捷键、页签持久化全部保持；未新增档位（"计划"只读属协作方式轴，与权限轴正交，重复添加反而混淆）

### 兼容性

- **陈旧页签模型回退语义收窄**：仅剩环境预设（未添加的 Ollama/llama.cpp）可用时，陈旧页签不再落到本地预设，而是清空模型停入 Welcome 引导用户显式选择——已有被"污染"页签（`desktop-tabs.json` 里存了 `ollama/...`）会在下次启动收到一条通知并被置空；显式添加过本地预设或手写 `[[providers]]` 的用户不受影响
- **codegraph 默认开关变更**：由"默认开（存量配置继承）"改为 **opt-in 默认关**（与 context7 统一）。存量用户若从未显式写 `[codegraph]` 段，升级后 codegraph 关闭——在设置或配置中开启一次即恢复；显式配置过的用户不受影响
- 白名单旧名 `computer-auto` 自动映射 `desktop-auto`；用户自装文件技能与 MCP 不受域门控影响；`hidden_plugins` 等新配置字段零值兼容

### 已知边界

codegraph 校验表锚定"所选版本"而非"上游可信"——上游仓库若被攻破且新版被照常采纳，毒会穿透所有传输层；缓解：Version bump 为人工审查闸口（spec 有明确知情记录）。`desktop/internal/update` 的 `TestEmbeddedPublicKeyParses` 在当前工作区失败（测试期望的 minisign 公钥与嵌入不一致，疑似密钥轮换未同步测试）。

---

## [0.1.9·前置批次 — ppt-vision] — 2026-08-15/16

聚焦**图片/扫描 PDF → PPT** 全链路的能力批次（17 个提交，skill v24→v38）。以真机 32 页华为运维方案 PDF 为金标准迭代：从"表格必丢、颜色跑偏、PDF 路径死亡"修到"30+/32 页 QA PASS"。

### 预分析与路由（桌面层）

- **VLM 预分析并行化**：分类判定并入四段提取首行（3 次串行→1 次往返，实测 12.5s）；颜色 JSON 投机并行；PDF 逐页有界并发（池 3）；全部调用 90s 超时兜底
- **推理模型 token 饥饿修复**：mimo 类模型隐藏推理先烧 max_tokens，1024 上限下颜色请求返回 content=None 静默丢失——上限提到 4096/8192（上限是天花板非目标）
- **本地绝对路径路由**：手打路径（原仅识别 @attachments）与路径不存在告警；**残留文件不变式**（PPT 意图无参考时清理 reference-style.json/pdf-pages，防跨任务污染）
- **降级全可见**：ppt:reference-warning 事件全情形发射 + 前端 toast（原事件发到空气）；判定结果事件供用户当场纠错
- **PDF 渲染脚本嵌入**：pdf_to_page_images.py 从仓库根移入 go:embed，boot 释放到 ~/.fairpeer/scripts/（打包运行原本必然找不到，PDF 视觉路径整体死亡）
- **防绕过注入**：PDF 参考就绪时向模型 input 追加系统提示（display 不变）——主模型曾自己抽文字编 32 页大纲把表格拍平

### skill 侧（ppt-auto）

- **画后 QA 回路**（Step 6.5）：SVG 渲染对比参考图，severity 门控（仅 MAJOR 返工）、双轮硬顶、无进展熔断、**断点续跑**（2 分钟 bash 超时杀不死：增量写报告+同轮种子跳过）；透明画布合成 deck 底色（消除模板页误报黑底）
- **修改模式**（R1-R4）：MAJOR 页局部修复工作流，不重跑全流程
- **全页分析补齐**：analyze_pdf_pages.py 打破桌面 6 页限速（分批 --max 8 + source_path 路径兜底）
- **表格骨架化**（Phase A）：CONTENT 的 markdown 表机械生成整页（CJK 列宽/换行/表头/斑马纹），compact 模式 24 行单页；禁止模型手写表格坐标
- **流程图骨架化**（Phase B）：DSL（节点/判断/连线）→ 分层布局 + 肘形箭头 + 虚线回路，层级>6 自动横向；--timeline 时间线模式
- **裁剪嵌入**：画不出的截图/logo/照片从参考页裁剪以 `<image>` 嵌入 + data URI 内联
- **颜色权威链**：上游转述的 hex 不得冒充用户输入覆盖 config（实测 #0078D4 被转述成 #1a3c6e）

### 已知边界

VLM 判定存在自然波动（同 deck 两轮 ±2 页 MAJOR 翻转，双轮硬顶是真正保险）；海报级流程图只还原主干链；图形时间线+密集表同页复合布局留待 Phase C；dream 任务书曾泄漏到输入框（根因已定位未修，待产品决策）。

---

## [0.1.8] — 2026-08-12

聚焦**搜索降级链 UI 补全**的修复批次：0.1.0 在后端加入了 AnySearch 第 4 级搜索引擎，但桌面端设置面板、类型层与能力探测一直漏接——用户在 UI 上既看不到也配不了 AnySearch（后端却会静默使用环境变量里的 key）。本次把这条链路从 UI 到文档全层对齐。无破坏性变更（见末尾兼容性说明）。

### Web Search — AnySearch 全层对齐

- **设置面板新增 AnySearch 输入框** (`desktop/frontend/src/components/SettingsPanel.tsx`)
  - `WebSearchSection` 第 4 个 `WebSearchKeyField`：label `AnySearch`、env `ANYSEARCH_API_KEY`，与前三个引擎共用同一保存/清除链路（`SetProviderKey`/`ClearProviderKey`，无白名单校验，直接落盘凭证文件）
- **后端状态视图补字段** (`desktop/settings_app.go`)
  - `WebSearchView` 加 `AnySearchKeySet`（json tag `anysearchKeySet`）
  - **两个** builder（正常路径 + fallback 路径）都补 `ANYSEARCH_API_KEY` 探测——首轮 `replace_all` 因两处缩进不同只命中一个，复查时抓出正常路径漏改并补齐（否则状态灯永远显示"未设置"）
- **能力探测补引擎** (`desktop/screenshot_solve.go`)：`webSearchKeyConfigured` 加入 `ANYSEARCH_API_KEY`，配了 AnySearch 也算"web 搜索可用"
- **类型 / mock 对齐** (`desktop/frontend/src/lib/types.ts`、`bridge.ts`)：`WebSearchView` 接口 + mock 默认值补 `anysearchKeySet`
- **文案补全** (`desktop/frontend/src/locales/zh.ts`、`en.ts`)：降级链描述由"这三个搜索引擎（Brave -> Exa -> Linkup）"改为"这些搜索引擎（Brave -> Exa -> Linkup -> AnySearch）"
- **文档对齐** (`README_cn.md`、`docs/FAIRPEER_FEATURES.md`、`docs/DEV_COWORK_TOOL_COMPARISON.md`、`docs/COWORK_IMPLEMENTATION_PLAN.md`)：四处仍写"三引擎"的全部更新为四引擎链

### 兼容性

无破坏性变更：`WebSearchView` 加字段零值兼容（旧前端读到缺省 `false` 等同"未设置"）；未配 `ANYSEARCH_API_KEY` 时降级链行为与 0.1.7 完全一致（Brave → Exa → Linkup 三级），AnySearch 仅作为末位兜底。

## [0.1.7] — 2026-08-12

聚焦**语音输入**的新功能批次：用户可通过麦克风说话转文字填入对话框，agent 能理解用户上传的音频附件。新增统一的语音转写接口（基于多模态 `input_audio`），与主对话模型完全解耦。无破坏性变更（见末尾兼容性说明）。

### 语音输入 — 统一转写接口

- **`CallSTT` 统一入口** (`internal/tool/builtin/stt.go` 新增)
  - 音频 base64 → 多模态聊天 `input_audio` content part → 转写文字，复用 VLM 的 provider chat runner（`SetProviderChatRunner`）
  - 一套接口服务所有接受 `input_audio` 的多模态模型（MiMo-V2.5 / GLM-4-Voice / GPT-4o-audio / Qwen-Omni 等），**不写逐家适配**
  - `voice_model` 独立于主对话模型——音频先转文字，再发给任意主模型（含 DeepSeek/Claude/Kimi/MiniMax 这些无音频能力的模型），voice_model 本身就是"兜底"
- **`input_audio` content 支持** (`internal/provider`)
  - `ContentPart` 新增 `InputAudio` 字段 + `InputAudio` 类型（Data/Format）；新增 `AudioContent`/`AudioParts` helper，对称于 `image_url`/`ImageContent`
  - `openai` provider `buildRequest` 处理 `input_audio` part → 标准 OpenAI 网络格式 `{"type":"input_audio","input_audio":{"data":...,"format":...}}`；`ContentLen`/`hasAudioParts` 配套
  - 与 `image_url` 共用同一个 wire 转换器（`imageContentParts` 扩展为处理全部多模态 part 类型）

### 语音输入 — 配置 UI

- **`voice_model` 配置字段** (`internal/config`、`desktop`)
  - `CoworkConfig` 新增 `VoiceModel`（`[cowork] voice_model`），对称 `screenshot_vlm_model`；`render.go` 配套渲染
  - `CoWorkSettingsView` 双向映射 + **热生效**：`SetCoWorkSettings` 末尾重新 `SetVoiceModel`，改完立即生效不用重启
- **onboarding / Settings 选择器**
  - 三步向导 `ModelStep` 新增第 4 个"语音识别模型"下拉（Settings 添加 provider 复用同一组件，改一处两入口都生效）
  - Settings 模型管理页新增 voice `ModelPicker`（事后修改）；所有 cowork base fallback 补 `voiceModel` 字段，避免改其他字段时清空 voice_model
  - `SetupProvider` 链路加 `voiceModel` 参数（Go + TS + wailsjs 绑定同步）

### 语音输入 — 麦克风按钮

- **对话框麦克风按钮** (`desktop/frontend/src/components/Composer.tsx`)
  - 发送按钮左侧新增麦克风按钮（cowork/dev 两种界面共用同一 Composer，改一次通用），不扩大输入框——复用发送按钮区，仅微调 textarea 右 padding
  - 状态机：idle → recording（红色脉冲呼吸动画）→ transcribing（spinner）→ idle；未配置 voice_model 时按钮不显示（对话框保持干净）
  - 转写结果插入 textarea **光标位置**（前字符非空格自动补空格），用户可编辑后再发送
- **独立录音工具** (`desktop/frontend/src/lib/voiceRecorder.ts` 新增)
  - Web Audio API（`ScriptProcessorNode`）采集 PCM → 编码 **WAV**（统一格式，所有目标模型都支持 wav，避开 MediaRecorder 的 webm 兼容坑）
  - `startRecording`/`stop()` 返回 base64 WAV data URL；`VoiceRecorderError` 错误分类（denied/notfound/unsupported/other）

### 语音输入 — 麦克风权限（跨平台）

- **macOS**：新增 `build/darwin/Info.plist` 的 `NSMicrophoneUsageDescription`（Wails build 时 merge 进最终 plist；无此 key macOS 直接拒绝 getUserMedia，不弹窗）
- **Windows / Linux**：WebView2 / WebKitGTK 原生支持 getUserMedia（无需特殊配置）
- **权限预检 + 分级提醒**：点麦克风前先查 `Permissions API`，系统级禁用直接指引"Windows 设置 → 隐私 → 麦克风 / macOS 系统设置 → 隐私 → 麦克风"（不浪费一次失败重试）；四级错误映射（系统禁用 / 网页拒绝 / 无设备 / 浏览器不支持）

### 音频附件 — agent 理解（对称 image 单轨）

- **`audio_understand` 工具** (`internal/tool/builtin/audio_understand.go` 新增)
  - 照搬 `image_understand` 单轨设计：读音频文件 → base64 → `CallSTT` 转写 → 返回文字
  - 25MB 大小限制、symlink/目录拒绝、`sniffAudioMIME`（mp3/wav/m4a/flac/ogg/aac/webm/opus）
- **音频引用识别** (`internal/control/refs.go`)
  - 新增 `refAudio` 分类 + `isAudioAttachmentRef` + `ResolveRefs` 生成 `<audio path="...">` 文本引用
  - 音频字节**永不内联**进主模型请求（绝大多数模型不能处理 audio part）——agent 看到 `<audio>` 引用后调 `audio_understand` 拿转写文字，再自行翻译/总结/引用
  - 前端零改动（音频文件本就被正确存为附件，只是之前 refs.go 当二进制黑盒）
- **测试** (`internal/control/refs_test.go`)：`TestClassifyRef`/`TestResolveRefsAttachmentKinds` 加 mp3 → refAudio 分类与 `<audio path>` 引用断言

### 升级兼容性

无破坏性变更：旧 TOML 无 `voice_model` → 默认空（语音功能禁用，麦克风按钮不显示，对话框外观不变）；`ContentPart`/`CoworkConfig`/`CoWorkSettingsView`/`ProviderTemplate` 加字段零值兼容；未配置 voice_model 时所有语音入口（麦克风按钮、`CallSTT`、`audio_understand`）优雅禁用并返回明确配置提示，不影响现有对话/图片能力。

## [0.1.6] — 2026-08-12

聚焦**计费准确性、代码编辑工具健壮性、办公面工作区安全、RAG hybrid 检索激活**的修复批次。无破坏性变更（见末尾兼容性说明）。

### Provider — 计费准确性

- **Anthropic cache-write 计费修正** (`internal/provider`)
  - `Usage` 新增 `CacheWriteTokens` 字段；`Pricing` 新增 `cache_write` 档位（per 1M cache-creation tokens）
  - `cache_creation_input_tokens` 不再混入 `CacheMissTokens` 按普通 input 1× 计——独立走 `cache_write`，默认 1.25× input（Anthropic 5m cache write 实际费率）
  - 旧 TOML 不配 `cache_write` 时自动用 1.25× input 兜底，比原来的 1× 更准
  - 全链路同步 hit-rate / new tokens / metrics 语义，保持用户可见数字连贯：`session hit-rate`、textsink `new`、`run --metrics`、桌面 metrics 都把 cache write 计入"非命中"分母
  - wire usage 事件 + telemetry + `ContextPanel` 新增 `cacheWriteTokens` 字段
  - `cache_shape.go` 的 prefix-churn miss 诊断自动变准（cache write 不再算 miss）

### 代码编辑工具 — 健壮性

- **multi_edit / apply_patch** 现在正确返回 post-edit LSP 诊断（之前静默丢弃），edit→diagnose→fix 闭环在批量编辑时不再断裂
- **apply_patch move** 删源失败时回滚（之前静默吞错，留下源+目标并存，move 变成 silent copy）
- **apply_patch** 保留 CRLF 行尾（之前 CRLF 源 + LF patch 会混合行尾，git 整文件 diff）
- **delete_symbol / notebook_edit** 改用原子写（temp+rename），崩溃不再留半个文件
- **edit_file** `old_string == new_string` 时短路返回（不再污染 mtime / git status）
- **Preview**（write_file/edit_file/multi_edit）补 workspace confine，批准前的 diff 卡片不再泄露工作区外文件内容
- **edit_file/multi_edit/apply_patch/delete_*** 现在拒绝二进制文件（`readFileEncoded` 加 NUL 字节检查，与 `read_file` 的二进制标记统一）——之前可盲目编辑二进制而损坏文件；`write_file` 覆盖二进制不受影响（容错读错误，回落 UTF-8）

### 办公面 — 工作区安全

- **mindmap_create** 补 workspace confine + 原子写——之前是唯一能写工作区外的写工具（`~/.bashrc` / `.git/config` 等）
- **workspace 装配路径**补 `doc_write`/`csv_write`/`xlsx_write`/`doc_convert` 的 roots 绑定——desktop/cowork 多项目模式之前回落到未约束实例
- `ConfineWriters` 补 `delete_range`/`delete_symbol`/`notebook_edit`/`mindmap_create`（CLI 路径防御纵深）

### RAG — hybrid 检索激活

- **embedding 重排重接**：`rag_search` 改组合式管道（LLM 负责查询扩展召回，embedding 做向量 cosine 精排，LLM rerank 兜底）；`boot.go` 移除强制 `SetRAGEmbedder(nil)` 断路（不再在 profile 切换时周期性清空 desktop 注入）；desktop 的 HEService 就绪后注入 HEClient 作为 embedder——兑现设置面板已暴露的 "hybrid search" 承诺，比 LLM rerank 更便宜（无 20 候选上限、无额外 LLM 调用）。无 embedder 时（headless/CLI 或 HE 未启动）自动退回 LLM rerank，embed 调用失败优雅降级为纯 FTS5

### 其他

- **currencySymbol** 映射 `INR → ₹`（之前返回字面量 "INR"）
- **doc_convert** 补 post-edit hook（与 doc_write 一致）
- **CSV 写出** 用 CRLF 行尾（RFC 4180 §2 合规，Excel/WPS 互通更稳）
- **启动时清理** `.docx-append-*` 残留临时文件（崩溃遗留）
- **wizard** 添加 provider 时 `Vision` 标志跟随 models.dev 判定（之前硬编码 true，纯文本模型也被标 vision 导致图片被静默丢弃）

### Removed

- 移除 legacy `SetEmailConfig` / `SetIMAPConfig` 单账号 setter 及其死 fallback 分支（`normalizeEmailAccounts` 在 config 加载时已把 `[cowork.smtp]`/`[cowork.imap]` 折叠进 `[[cowork.email_accounts]]`，运行时无需 fallback）

### 升级兼容性

无破坏性变更：旧 TOML `[price]` 缺 `cache_write` → 用 1.25× input 默认；旧 `telemetry.json` 缺 `cacheWriteTokens` → 0；旧 `session.json` 不受影响（不持久化 Usage）；前端 TS 结构化缺字段不报错；`[cowork.smtp]`/`[cowork.imap]` 旧配置仍工作；`Usage`/`Pricing` Go 结构加字段零值兼容。

## [0.1.4] — 2026-08-07

聚焦 **PPT-auto 技能的深度修复与架构重构**，以及专家团、主题系统、沙箱的改进。

### PPT-auto — 配色与模板

- **模板配色提取重写** (`extract_template_colors.py`)
  - 新增读取 `theme1.xml` 的 `clrScheme`（accent1-6, dk1/lt1/dk2/lt2），解析 `schemeClr` 引用——之前完全漏掉了模板真正的主题色来源
  - 新增全屏 `blipFill` 图片背景检测 + PIL 算真实背景色——图片背景模板不再误判为深色主题
  - 新增从 `theme1.xml` 的 `fontScheme` 提取模板字体（标题/正文），自动构建跨平台降级链（模板字体 → 微软雅黑 → PingFang SC → Noto Sans CJK SC）
  - `card_bg` 和 `line` 改为根据背景深浅自动适配的 `rgba()` 半透明值，不再硬编码纯色

- **模板布局继承** (`pptx_builder.py`)
  - 恢复 `Presentation(template_path)` 模式：有模板时打开模板、清空 slides、用模板 layout 添加新 slide，模板的背景/渐变/logo 通过 OOXML 继承自动透出
  - 修复 `Duplicate name: ppt/slides/slide1.xml` 警告——清空 slides 时同时 `drop_rel` 删除关系

- **SVG → DrawingML 颜色转换**
  - `parse_hex_color` 支持 `rgb()`/`rgba()` 格式——之前 SVG 里的 `rgba(0,0,0,0.35)` 会变成 `noFill`，卡片/色块变透明
  - 新增 `parse_color_alpha()` 提取 rgba alpha 通道，`build_fill_xml`/`build_stroke_xml` 保留透明度

- **视觉配色提取层**（新增，`desktop/ppt_template_vision.go`）
  - 用户选模板时后台调视觉模型识别背景图的配色/风格，写入 `~/.fairpeer/ppt-template-style.json`
  - 优先级最高的配色来源，静默降级（无 VLM 配置时回退到 XML+PIL 提取）

- **去除硬编码品牌色/风格**
  - 默认配色从 Fairpeer 暗色主题（#121212/"科技感深色极客"）改为中性浅色（白底深字蓝色强调）
  - 删除 `brand_green`（Fairpeer 品牌色）、`"科技极简风"` 风格指向
  - 默认字体从 Inter 改为微软雅黑（中文/数字更正式）

### PPT-auto — 流程与验证

- **SKILL.md 从 16 步精简为 8 步**
  - 合并 fix_svg/check_svg/notes 进生成步骤，删除视觉检查/PDF 导出/用户反馈等非核心步骤
  - 修复步骤顺序：读配置（Step 3）提前到写大纲（Step 5）之前——模型不再凭主题名自创品牌色
  - 修复 init/design_spec 顺序：init 创建目录后再写 design_spec.md

- **check_svg 质量检查器**
  - `--mode` 不再硬编码——从 `template_config.json` 的 `mode` 字段读取（设置面板的快速/校验切换真正生效）
  - WARN 返回 exit code 0（只有 ERROR 返回 2）——不再因 WARN 卡住 complete_step
  - 文字重叠检测修复：大数字+下方标签的正常堆叠不再误报（重叠面积 >40% 才报）
  - `<image>` 背景检测加全屏覆盖判定——小 logo 不再被误认为背景

- **启动性能** (`release.go`)
  - `EnsurePPTAutoSkill` 跳过内容未变化的文件——11632 个图标不再每次版本 bump 都重写，启动白屏恢复正常

- **设置面板模式选择**
  - 修复校验模式每次启动被重置为 fast 的 bug——`SyncPPTMode()` 在发布脚本后恢复用户的 mode 选择

### PPT-auto — 沙箱与 evidence

- **沙箱白名单**：`~/.fairpeer` 自动加入 write roots——`extract_template_colors.py` 写 `template_config.json` 不再被拦截
- **complete_step evidence 指引**：脚本生成的文件用 `kind: verification`，只有 `write_file` 写的文件用 `kind: files`
- **project_manager.py**：重复目录自动加 `_2`/`_3` 后缀，不再报错让模型重试 3 次

### 专家团

- **修复 ExpertSessionView 永久卡在"协作中…"** — 启动失败/取消确认时重置 `liveRunning`，不再需要关标签页
- **修复未定义 CSS 变量** — 28 处 `--fg-muted`/`--text-muted` 改为 `--fg-faint`（每个主题都有定义），专家卡片模式标签/成员列表颜色恢复正常
- **辩论/流水线模式要求 ≥2 个专家** — 不再允许无意义的单人阵容

### 主题系统

- **CoworkDock 硬编码颜色** — "生成深度决策指引"按钮、"今日日程与任务"星星图标的 `#f26522` 改为 `var(--accent)`，跟随主题切换

---

## [0.1.1] — 2026-08-06

The first major feature release after the v0.1.0 brand migration (momapeer →
fairpeer). This version transforms fairpeer from a provider-agnostic coding
agent into a **reliable, self-evolving, ecosystem-connected AI assistant** —
with operation-level failure recovery, bilingual semantic search, a third-party
skill marketplace, pre-write syntax validation, and comprehensive Office/PPT
enhancements.

### Design Constraints

All new features in this release adhere to two hard constraints:

1. **零用户学习成本** — 功能开箱即用，用户永远不需要理解新概念（如 RiskClass、操作指纹、Inbox 状态机）。用户的心智模型始终是："FairPeer 自己会处理，我只在被问时点一下"
2. **零提示词膨胀** — 功能实现不得向 base system prompt 注入新指令。能力靠 host 侧硬机制（纯函数/代码逻辑），而非 prompt 说教。base prompt 保持 ~900 字符不变

### Added — Reliability (Phase 1)

- **Operation-level failure-recovery gate** (`internal/agent/op_gate.go`)
  - Stops the agent from looping on the same failing write/command — the #1 cause
    of autonomous task failure and user intervention
  - Two budgets: per-operation (3 same-fingerprint failures stops that op) and
    per-turn (6 total failures pauses all writes; read-only diagnosis continues)
  - Pure `decide()` function, SHA-256 operation fingerprint, qualifying-failure
    filter (permission/plan/hook denials, read-only failures, transient timeouts
    never count). Coexists with existing stormBreaker/repeatSuccessBlock
  - *(Borrowed from DeepSeek-Reasonix's `internal/recovery/`)*

- **Permission risk classification** (`internal/permission/risk.go`)
  - 4-level RiskClass (Read/WriteLocal/Exec/External) replaces hardcoded
    `isIrreversibleOutwardTool` switch
  - MCP tools default to External (safe default); `[[plugins]] risk="read"`
    overrides per-server. `Classify()` is pure host logic — users never see the
    class name, they only experience "local ops don't ask, outward ops do"
  - *(Borrowed from openworker's `risk.py`)*

- **Pre-write syntax validation** (`internal/validation/`)
  - write_file/edit_file/multi_edit validate `.go` (go/parser) and `.json`
    content BEFORE writing; broken syntax is refused and the file is not
    corrupted on disk. Zero new deps (stdlib only); unrecognized extensions pass

### Added — Skill Ecosystem (Phase 2)

- **Pre-install skill content safety scan** (`internal/installsource/safety.go`)
  - Statically inspects a skill body for prompt-injection / payload patterns
    before it enters the install plan
  - Injection patterns (block): "ignore all previous instructions", identity
    overrides, system-prompt shadowing
  - Payload patterns (warn): eval/os.system/subprocess/__import__, network
    fetches, destructive commands (rm -rf /)
  - Obfuscation: large base64 blobs (≥80 chars)
  - Findings flow into the plan's existing riskReasons; block-level escalates
    RiskLevel to high — zero new UI
  - *(Borrowed from rooster's `_loader.py`)*

- **Skill install manifest** (`internal/installsource/manifest.go`)
  - Persists `.installed.json` recording each skill's source, content hash,
    install time, and mode — audit trail + foundation for future update-check

- **Third-party marketplace integration** (`internal/installsource/marketplace.go` + `clawhub.go`)
  - Multi-source skill catalog: 4 default sources (Curated builtin, Anthropic
    Skills, OpenAI Skills, ClawHub Community) — no single-service dependency
  - `skill_market` tool: browse/search/install across all sources
  - Claude `.claude-plugin/marketplace.json` support: GitHub repos with
    marketplace.json are auto-detected, plugins[] become install actions
  - ClawHub.ai REST API client (list/search/download with install counts)
  - All installs reuse existing safety scan + manifest pipeline

- **Skill market UI** (Settings → Skills → Marketplace section)
  - Search box + results cards (name, source badge, install count, description)
  - One-click install from the card; success/error feedback inline
  - Users no longer need the agent's natural-language flow to find/install skills

### Added — Intelligence (Phase 3)

- **LLM-driven bilingual semantic search** (`internal/rag/llm_semantic.go`)
  - Query expansion: LLM rewrites the query into synonyms, English word-stems
    (run/running/ran), and cross-language equivalents (数据库↔database)
  - LLM rerank: FTS5 over-fetches, LLM orders by genuine relevance
  - Fixes FTS5's three blind spots at once: CJK synonyms, English stemming,
    cross-language alignment
  - Fully internalized — reuses the user's already-configured provider, no
    embedding model, no vector store, no Python, no new deps
  - 30-min cache; graceful degradation to plain FTS5 on any failure

- **Context budget** (`context_budget_percent` config)
  - Caps the effective window for compaction decisions; 0/100 = full window
    (default), 80 = compact as if the window were 20% smaller — saves cost on
    input pricing tiers and avoids quality drop near the edge

- **Dream memory dedup + red-line protection**
  - DreamTask prompt rules: "NEVER duplicate a fact already in the file" and
    respect `<!-- protected -->` regions (user-declared red lines that dream
    must not modify)

### Added — Model Registry

- **Dynamic model registry**: embedded JSON snapshot replaces hardcoded Go table
  (`desktop/default_registry.json`); remote fetch from models.dev with cache +
  startup init
- **Model library UI**: SettingsPanel model-library box with vendor templates
- **Multi-vendor onboarding wizard**: pick vendor → paste key → pick model
- **13 verified vendor templates** (11 direct + 2 aggregators), down from the
  original 18 (removed 7 coding-plan aggregators — 0.1.0 is direct-vendors-only)

### Added — Office Enhancements

- **Word (.docx)**: image insertion (PNG/JPG/GIF), table of contents generation
- **Excel (.xlsx)**: charts (bar/line/pie/scatter), conditional formatting
  (cell/data_bar/color_scale), per-cell number format + style, real numeric cells
- **PPT**: animation/transition docs (corrected effect names), narration support

### Changed

- Provider count corrected from 18 to 13 (11 direct + 2 aggregators); 7
  coding-plan aggregators removed
- Hook system documentation corrected: 12 events (not 2), mature gating/timeout/
  degradation/trust (directory `internal/hook/` singular, not `internal/hooks/`)
- Error recovery assessment corrected: already has 10-retry HTTP backoff +
  Retry-After + 4 loop detectors (not "simple retry")
- SPEC_v2.md rewritten based on code audit (previous version listed already-
  implemented capabilities as "to-do")

### Fixed

- **docxwrite image bugs**: dynamic image rIds (was hardcoded rId100, broke
  multi-image docs), dedup content-types/basenames, validate image files
  pre-write, reject SVG with clear error, preserve original package in append
  mode (rels/content-types/media were silently destroyed)
- **xlsxwrite chart bug**: category/value separation (was binding both to same
  range, producing meaningless charts)
- **xlsxwrite conditional format**: color_scale/data_bar with correct excelize
  fields (was rejected as "parameter is invalid"); number format preserved when
  combined with cell style (second SetCellStyle was dropping the first's
  CustomNumFmt); added Number field for real numeric cells (was stored as text)
- **PPT route-B apply crash**: DEFAULT_TRANSITION degraded to 'keep' when
  pptx_animations absent (was 'fade' not in argparse choices → every apply call
  crashed with exit 2)
- **Compile error**: removed undefined `globalDocCache.Invalidate` call that
  broke the entire `builtin` package
- **install_source Append field**: was parsed but never passed to writeDOCX

### Security

- **Permission risk classification**: MCP tools default to External-risk,
  requiring approval; configurable per-server override
- **Skill safety scan**: prompt-injection / payload / obfuscation detection
  before any third-party skill enters the install pipeline
- **SSRF guard**: install_source already guards against cloud metadata / internal
  services; now all marketplace fetches go through the same guarded httpClient

### Added — Cross-Platform Desktop Automation

桌面自动化（screen_click / screen_type / screen_key / screen_scroll / screen_perceive）从
Windows-only 扩展到三平台（Windows / macOS / Linux）全部可用。

- **跨平台点击/输入/快捷键/滚动** — macOS 通过 `cliclick`、Linux 通过 `xdotool` 实现底层
  输入；`parseKeyCombo` 从 Windows VK code 重构为平台无关的 key-name 字符串，各平台自行翻译
- **screen_perceive macOS/Linux** — VLM-only 路径（截图 → 视觉模型 → JSON 坐标），不需要 UIA
  元素树；Windows 仍保留 UIA+VLM 融合路径
- **screenAttachmentsDir 修复** — 从 windows-only 文件移到平台无关文件，解除 macOS/Linux
  编译阻塞（影响 10 个下游包）
- **三平台 `go build ./...` 全绿**（Windows/Linux/macOS 交叉编译验证通过）
- browser 工具链确认为原生跨平台（chromedp CDP 协议，三平台统一，无需改动）

### Added — Dynamic Model Registry (models.dev)

从硬编码 Go 静态表升级为动态模型注册表，模型/URL 更新不再需要改代码发版。

- **embed JSON 快照**（`default_registry.json`）— 11 厂商数据编译时内嵌，离线兜底
- **models.dev 远程同步** — 启动时异步拉取 `models.dev/api.json`，过滤 11 家 tracked vendors，
  合并远程数据（URL/模型/上下文）与快照角色字段（DisplayName/推荐角色）
- **本地缓存 12h TTL**（`~/.fairpeer/registry-cache.json`）
- **四层兜底**：内存 → 本地缓存 → models.dev → embed 快照（永不失败）
- **设置面板"检查更新"按钮** — RegistryBox 显示最后更新时间，手动触发刷新

### Added — Multi-Vendor Onboarding

首次启动向导从单 API key 输入框重写为三步向导。

- **Step 1 选厂商** — 下拉框（11 家直连厂商，透明背景 + 悬浮高亮）
- **Step 2 填 key** — ProbeVendorKey 探测选中厂商端点 + 获取 key 链接
- **Step 3 选模型** — 预置模型列表（带 default/vision/fast 角色标注），SetupProvider 一次性
  完成 key 写入 + provider 配置 + 设为默认

### Added — Web Search

- **AnySearch 第 4 级搜索引擎** — 降级链 Brave → Exa → Linkup → AnySearch
- **搜索缓存** — 内存 map（10min TTL），原 SQLite 方案因 CGO 依赖改为纯 Go 内存

### Changed

- **厂商数据官方文档核实** — 11 家厂商 model ID / base_url 全部对照官方 API 文档确认
  （通义 qwen3.8-max、DeepSeek deepseek-v4-pro、智谱 glm-5.2、MiniMax MiniMax-M3、讯飞
  maas-token-api 端点 + xopglm52 模型 ID 等）
- **聚合平台移除** — v0.1.0 只保留 11 家直连厂商，7 个 Coding Plan 聚合平台后续再加
- **邮箱示例更新** — SMTP/IMAP 占位符从 139/chinamobile 改为 QQ/126
- **PPT SKILL.md 精简** — 441→196 行（-55%）；新增 todo 管理规则（解决子代理 readiness
  死锁根因）；动画参数抽到 `references/animations.md`；默认无动画无过渡

### Fixed

- **PPT 子代理卡死** — 根因：子代理 todo_write 项未全部标 completed → finalReadinessCheck
  阻止输出 → 3 次重试后报错。修复：SKILL.md 增加 todo 管理规范（每步完成即标记、最终答案前
  确认无 in_progress 项）
- **search_cache.go CGO 依赖** — SQLite + go-sqlite3 导致非 CGO 环境编译失败；改为内存 map
- **screenAttachmentsDir 跨平台** — 平台无关函数被放在 windows-only 文件里，阻塞 mac/linux
  编译；移到 screen_tools.go
- **Session 从左侧项目工作区消失** — ListSessions 只扫描当前 tab 的 session 目录，切换 tab 后其他
  项目的 session 不可见。改为遍历 knownSessionDirs() 扫描全局+所有项目+所有 tab
- **专家团 session 无法识别** — BranchMeta.DefaultScope() 把 scope="expert" 映射成 "global"，
  丢失标识。修复：保留 "expert" + 新增 ExpertTeamID 字段端到端传递（BranchMeta→SessionInfo→
  SessionMeta→前端 types.ts）
- **HistoryPanel 显示空会话** — 新建 tab 但未发消息会留下孤儿 .meta 文件（无对应 .jsonl）。
  修复：启动时 pruneOrphanMetaFiles() 自动清理；ListSessions 的 turns==0 过滤保留原有逻辑
  （用户发了消息但模型未回复的会话仍然显示）
- **邮箱服务器示例** — SMTP/IMAP 占位符从 139/chinamobile 改为 QQ/126
- **Compact 按钮无反馈** — 点击后无 loading 状态，用户以为没反应。改为按钮显示"压缩中…"+禁用
- **marketplace 测试断言** — TestDefaultMarketSources 期望 "anthropics" 但实际 ID 是
  "anthropic"；TestCatalogMatches 空查询期望 false 但实现返回 true（显示全部）。测试已同步

### Added — Office Overview Tab + Manual Compaction

- **办公模式"概览"页卡** — CoworkDock（今日/邮件/文件）新增第 4 个 tab「概览」，渲染
  ContextPanel：token 用量甜甜圈图、prompt/completion/reasoning/other 分色、cache hit 率、
  健康度、压缩进度、引用文件、变更文件
- **「立即压缩」按钮** — ContextPanel 的 session-status 区块新增按钮，点击调 app.Compact()
  手动触发上下文压缩；带 loading 状态（"压缩中…" + 禁用防重复点击）
- 数据透传链路：App.tsx → CoWorkLayout → CoworkDock → DefaultDock → ContextPanel
  （contextInfo/usage/sessionTokens/activeTabId/dockRefreshKey）

---

## [0.1.0] - 2026-08-03

The initial release of fairpeer, fully migrated from momapeer. This is a
provider-agnostic, multi-vendor AI coding and automation assistant.

### Added

- Multi-vendor LLM support: 11 direct providers (Qwen, DeepSeek, Volcengine,
  Zhipu, MiniMax, Moonshot, MiMo, StepFun, iFlytek, Anthropic, OpenAI)
- Provider-agnostic architecture with configurable model roles
  (default/vision/fast)
- Desktop automation with VLM (Vision Language Model) support
- RAG (Retrieval-Augmented Generation) knowledge base system (FTS5 + entity
  extraction + vector search)
- Email integration with multi-account support
- Calendar and scheduler integration
- PPT generation with template intelligence (SVG → PPTX + template fill)
- Expert team (multi-model collaboration) system
- Memory system with persistent context (portrait + dream/distill + auto-memory)
- Browser automation via CDP
- CLI, Desktop (Wails), HTTP/SSE, IM Bot (Feishu/WeChat/QQ), and ACP interfaces
- Checkpoint/rewind for safe file editing
- Hook system (12 lifecycle events: PreToolUse/PostToolUse/UserPromptSubmit/
  PostLLMCall/PreCompact/SessionStart/SessionEnd/SubagentStop/Notification/
  PermissionRequest/Stop/Startup)
- Skill system (local Markdown skills, install_source from URL/GitHub/local,
  MCP plugin support)
- Compose spec-driven development workflow (grill→spec→implement→verify→review)
- Auto-plan + Goal mode + Max Mode (parallel best-of-N sampling)
- Token efficiency: multi-level compaction + prompt caching + two-pass output
  optimization

### Changed (from momapeer)

- Complete brand replacement: momapeer → fairpeer (Go module, env vars, UI text,
  build configs, CI/CD, update chain, data directories)
- Config rewritten to be provider-agnostic (no hardcoded jiutian/moma defaults)
- 11 preset vendor templates replace single-provider assumption
- Frontend decoupled from any specific provider/model
- VLM simplified from degradation chain to single configurable vision model
- Email system de-branded (mainstream IMAP/SMTP providers)

### Removed

- MoMA capability system (per-provider declaration replaces it)
- Jiutian multimodal tools and hardcoded vision model chain
- All momapeer/moma/jiutian brand references

### Security

- API keys stored with DPAPI (Windows) / AES-GCM encryption on supported platforms
- Per-provider role fields (no key leakage across providers)
- Sandbox with configurable write roots and network policy
- Hook trust model (project hooks require explicit trust)
