# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [0.1.10] — 2026-08-27

### 运维能力扩展 R1-R3（NETDEV_SPEC v2.0 落地）

spec 定稿见 `docs/NETDEV_SPEC_V2.md`（675 行，含 UI 契约与 15 条裁决）；本批落地前三批核心，全部密封路径（结构性只读不变）。

- **R1 日志与体检**：主区「日志工作台」（多源勾选 → 时间戳合并时间线，无时间戳行吸附前一条；底栏跨设备 IOC 搜索，预算耗尽如实报覆盖 N/M 台）；`netdev_triage` 主机体检电池（linux 11 项/windows 5 项，失败登录爆发/磁盘水位/uid0/时钟未同步四类保守异常自动进「发现」）；巡检家族菜单四件套（网络巡检/主机体检/基线核查/**弱口令核查**——后端闲置能力正式接线）；`crontab -l`/`lastb`/`nginx -t`/`apache2ctl` 等入读表
- **R2 容器与数据目标**：设备新增 `kind` 判别式（存量零迁移）——`docker`（只读 Engine API，npipe/unix/tcp，POST 无代码路径）、`k8s`（kubeconfig 入密钥库 + 固定 context + 命名空间白名单 + 防 SSRF）、`firewall`（FortiOS REST 只读，token/Basic）；日志源 `k8s:`/`docker:` 经 API 路由——VM journal、容器日志、Pod 日志可合并进同一条时间线；三客户端密封行为均被 httptest 假服务钉死
- **R3 事件与时序**：`netdev_locate`（IP/MAC 全网 ARP 扇出定位，清单页签顶部入口）；通知出口（webhook + 飞书/钉钉/企微原生模板 + 严重度过滤 + `fairpeer://` 深链）；时序面 v1（JSONL 零依赖 + SNMP 轮询采集 + 设备卡 Sparkline）；报告族四件（值班交接/周报/凭证盘点 + 既有晨报）
- **设置三级导航（§10.9）**：表单进三级页、弹框只留阻塞确认——L3 翻页动画（reduced-motion 降级）+ 六表单迁移 + 未保存确认 + kind 选择题表单
- **含上会话在途 P2 收口**：syslog 被动接收/告警规则/审计哈希链/日志 follow/DB 只读诊断源（mysql/pg/redis 白名单）
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
