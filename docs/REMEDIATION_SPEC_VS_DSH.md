# fairpeer vs deepseek-harness 全面对比整改 Spec

> 版本：v1.4（2026-09-11）
> 对比对象：`fairpeer`（本仓库）vs `deepseek-harness`（dsh，`../deepseek-harness`）
> 方法：三轮对比。第一轮按模块逐条排查（办公/编码/运维/编排/显示）；第二轮按 48 个真实场景走读两侧代码全链路（provider 韧性/记忆上下文/安全/协作/生态）+ mock LLM 故障注入对拍；第三轮把每条不足放回"用户坐在屏幕前"的视角逐行走查确认（日常编码/办公任务/运维值班/会话历史/安装首跑五个席位）。合计 21 项运行时实测 + 10 项关键发现亲自复核。
> **v1.3 变更：本版完全自包含**——v1.2 中 17 处"同 v1.1/v1.0 保留"引用因逐版重写而悬空（被引用的详细方案已不在文件中），本版恢复全部条目的完整证据/方案/验收；并把 4 个散落条目（200k 截断、watchdog 预算、审批跨 tab、SDK 缺失）归位为正式编号。
> 每项整改给出：**证据（file:line）→ 用户视角确认（如适用）→ dsh 对照（如适用）→ 整改方案 → 验收标准**。行号以 2026-09-11 工作区为准，整改时如漂移以符号名定位。

---

## 0. 总体判定

| 方向 | 结论 |
|---|---|
| 办公 | **能力 fairpeer 压倒性优势**（dsh 无 docx/xlsx/pptx/pdf 处理，其文件工具仅 UTF-8 纯文本）。但存在"信任杀手"级交付缺陷：append xlsx 静默覆盖旧表、docx 空文档假成功、OCR 报错注入正文、二进制格式超 200k 永不可读、产物全程不可视。 |
| 编码 | 各有胜场：fairpeer 强在编辑事务性（5 级模糊匹配、双阶段补丁、编码保真、checkpoint、循环硬闸、diff 反馈环）；dsh 强在并发写 CAS、Windows 沙箱强制、持久 PTY、spill 保真。 |
| 运维 | 能力垄断（dsh 无设备面/割接/审计链/监控），但值班体验有三处"看似正常实际已断"的死端：割接门失败后"继续"永远失败、watchdog 预算暂停后恢复即再冻结、重启后 running 作业无 runner 仍照常倒计时。 |
| 任务编排 | dsh 明显领先：编排状态（plan/goal/todo）全部会话日志化可重放、崩溃可恢复；fairpeer 全内存、goal 崩溃失忆。fairpeer 独有的 evidence ledger/goal judge/auto-plan 必须守住。 |
| 对话显示 | 各有胜负：dsh 强在流式渲染性能（冻结块增量解析）与可观测性（TTFT/tok-s/cache）；fairpeer 强在 diff 渲染、内嵌终端、Ctrl+F 查找、会话管理。fairpeer 压缩摘要卡/桌面审批卡是同类中透明度最高的设计。 |
| LLM 供应商层 | dsh 系统性领先恢复体系（超窗自愈、可配重试、错误码分类学、per-model effort 预检、token 计量投影）；fairpeer 反超 wire 级 cache 控制、RPM 预算、代理、响应格式修复。运行时实锤：双次断流 dsh 活 fairpeer 死。 |
| 记忆与上下文 | fairpeer 领先自动记忆（portrait/Dream）与 RAG 全家；落后四项安全网（spill、session 检索、recall 检索面、预算化注入），另有历史搜索三重静默漏检。 |
| 安全 | 两个已验证 P0：apply_patch/move_file 绕过写边界；跨会话/跨进程零写保护。另有密钥 env 暴露链、只读表绕过等 5 个 P1。dsh 的 fail-closed 沙箱与单一 fs 咽喉点直接对应这些缺口。 |
| 多会话/协作 | fairpeer 缺：并行 fan-out、入站 webhook、agent 间消息、有鉴权浏览器远程、审批跨 tab 可见性；反超：双人审批（IM 管理员门控+推送订阅）。 |
| 扩展生态 | dsh 的 everything-is-a-plugin 开发体验全面领先；fairpeer 的 MCP 协议广度反超（prompts/resources/elicitation/progress/OAuth）。fairpeer 缺 MCP 重连、图片结果、分页、CC hooks 兼容、包化分发、SDK。 |
| 工程基建 | fairpeer 576 测试文件/10.2 万行、CI 2 workflow；dsh 863 个/30 万行、19 workflow（含 provider e2e、沙箱运行、快照、文档站）。全新 checkout `make test` 不绿（1 稳定失败+1 负载偶发）。 |
| 用户体验 | 好设计真实存在（审批会话级授权、压缩卡全文透明、Esc 未回包撤回），但四类系统性病灶：**①静默失败**（删除/推送/预装/搜索漏检无声）、**②假成功**（空文档/覆盖/无效审批回"已批准"）、**③死端无出口**（错误无下一步指引、UI 按钮缺失）、**④中英混杂**。 |

---

## 1. P0 整改项（可利用缺陷 / 数据丢失 / 功能坏死）

### P0-1 apply_patch 与 move_file 完全绕过工作区写边界【已代码+测试证实】
- **证据**：`internal/tool/builtin/confine.go:46-74` ConfineWriters 列出 13 个 writer（writeFile/editFile/multiEdit/deleteRange/deleteSymbol/notebookEdit/docWrite/imageGenerate/csvWrite/xlsxWrite/docConvert/mindmapCreate/ragMindMap），**无 applyPatch/moveFile**；`internal/tool/builtin/workspace.go:44-79` Workspace.Tools() overrides 同样无此二者；二者经 init() 零值注册（`apply_patch.go:17`、`movefile.go:16`），roots==nil 时 confine() 直接放行（`confine.go:144-147`）。
- **用户确认**：单窗口用户低频，但配合提示注入即构成非交互任意写链（~/.ssh/authorized_keys、crontab）；headless 模式更免审批（`permission.go:545-557`）。同文件注释显示 notebook_edit 曾因同类问题专项修复——这两个漏了。
- **dsh 对照**：单一 fs 咽喉点架构（所有写过 `checkedTarget`，`fs/fs-sandbox/src/index.ts:122-144`），结构上不存在漏注册。
- **整改**：ConfineWriters 与 Workspace.Tools() 各补两行注册；**结构性回归测试**：反射遍历注册表中所有 `ReadOnly()==false` 且非 bash/netdev 的工具，断言每个都被 roots 绑定。
- **验收**：配置 workspace_root 后 apply_patch/move_file 写根外被拒；结构性测试进 CI。

### P0-2 跨会话/跨进程零写保护 + 会话文件无锁【已代码证实】
- **证据**：`internal/tool/builtin/writefile.go:49-81` 无条件覆盖（仅同内容 no-op）；`editfile.go:70-113` 执行时重读+old_string 替换，另一会话改过但 old_string 仍匹配时静默叠加；无 per-target 锁；会话文件无锁（serve+桌面双开同一会话=后写覆盖先写）。回滚 `RestoreCode` 对 unsafe 文件也无条件覆盖（`checkpoint.go:554-606`）。
- **用户确认（第三轮修正）**：双页笔回滚的 unsafe 警告是**事前展示**的（`Message.tsx:230-237` 确认步骤列出"将丢失外部修改"的文件清单）——但警告后二次点击即执行覆盖，无按文件排除、无备份。
- **dsh 对照**：per-target 锁 + 乐观 CAS（`fs-local/src/index.ts:174,184-190,232-242`，FS_STALE_VERSION/FS_NOT_OBSERVED）+ 跨进程会话租约（`session-persistence-jsonl/src/lease.ts:80-126`，flock+Windows 信号量）。
- **整改**：①工具层记录 read 时 mtime+size，写入前不等返回 `FILE_STALE_SINCE_READ` 结构化错误；②会话文件保存前取锁，争用时提示"该会话已在其他窗口打开"；③回滚确认对话框支持按文件取消勾选 + 执行前自动备份 unsafe 文件到 checkpoint 归档。
- **验收**：双进程并发 edit 后者收 stale 错误；同会话双开后者只读；回滚 unsafe 文件可从备份找回。

### P0-3 OCR 扫描件路径坏死【运行时证实；第三轮升级用户影响】
- **证据**：`ocr_pdf.py:109` 定义 3 参 `_ocr_page`，`:189` 以 4 实参调用（运行时复现 `TypeError: _ocr_page() takes 3 positional arguments but 4 were given`）；`:186` `show_log=False` 在 PaddleOCR 3.x 已移除；`:43-49` 对 paddleocr≥3 做"卸载重装 2.x"，内网 pip 失败时 3.x API 漂移同样 TypeError。
- **用户确认（升级）**：①报错文本**拼进文档正文**（`:196-197` except 把错误串 append 进 all_parts）与合同条款同级返回给模型——模型可能复述报错或**无视报错就地编造该页内容**；②脚本查找顺序 exe 相邻目录优先于内嵌副本（`docconv.go:58-77`），用户机器上旧副本**遮蔽修复**；③首次读扫描件在工具调用内**静默 pip 安装 paddlepaddle 数百 MB**（输出 DEVNULL 吞掉，`ocr_pdf.py:24-49`），超时上限 10 分钟（`rag/officedoc.go:159`），用户只看到工具卡转圈数分钟。盖章合同/发票/红头文件几乎全是扫描件，触发面极大。
- **整改**：①修签名 + PaddleOCR 3.x/2.x 双兼容（去掉 show_log，按版本选 API）；②报错不得进正文，改结构化 `extraction_warnings` 字段；③docconv 查找顺序内嵌副本优先；④依赖安装进度经事件上屏；⑤`--selftest` 子命令进 CI。
- **验收**：扫描 PDF 实测无 "OCR error" 正文注入；无网环境安装失败有中文指引而非转圈；selftest CI 绿。

### P0-4 全新 checkout `make test` 不绿【运行时证实】
- **证据**：`internal/rag/store_test.go:143-155` TestBinaryFormatRejected 期待 docx 被拒（"Phase 3"），但 `internal/rag/extract.go:678-686` 已把 docx/xlsx/pptx/pdf 列入导入白名单——稳定 FAIL（测试未跟上功能）；`internal/boot` 全量并行负载下偶发失败（单跑通过，时间敏感断言）。
- **整改**：改写该测试匹配现状（docx 导入成功且可搜索；真正不可解析二进制断言拒绝/降级）；定位 boot 包墙钟敏感断言改注入时钟或放大容差。
- **验收**：干净 clone+干净 GOPATH 连续 5 次全量 `go test ./...` 全绿；CI 增加干净环境全量测试门。

### P0-5 Job/Cutover ID 重启撞号 + 重启后 running 态无 runner【第三轮升级】
- **证据**：`internal/netdev/job.go:113-123` newJobID、`cutover.go:143-153` newCutoverID 均为进程内内存序号，重启归零；同日重启后新建 `J<date>-1` 经 `AtomicWriteFile` **静默覆盖**旧文件（`job.go:136-142`、`cutover.go:166-172`）。对照 `proposal.go:339-370` 已正确从磁盘回播当日最大序号。
- **第三轮升级（另一半）**：重启后磁盘上 status=running 的 job/cutover **没有 runner 恢复扫描**（proposal 有 `recoverStaleExecuting` `proposal.go:295`，job/cutover 无等价物）。操作员看到大屏照常倒计时、步骤全 pending、无输出；`CutoverContinue` 拒绝（"only held cutovers continue" `cutover.go:476-478`）、`JobPause` 报 "state drift"（`job.go:337-339`）——唯一动作是 Abort，且界面无"执行已断"提示。
- **整改**：①统一扫盘取号（抽 fileutil.NextSeqInDir 供 ops/proposal/job/cutover 四处复用）；②Load 时对 running 态转为 `interrupted` 并在大屏标"后端重启，执行已中断，可继续/放弃"。
- **验收**：预置同 ID 文件后新建得 -2；重启后 UI 明示中断态而非假装运行。

### P0-6 Windows 沙箱裸奔且 fail-open【运行时证实】
- **证据**：`internal/sandbox/seatbelt_other.go` 在 Windows 直接不包裹；`RequireAvailable` 默认关（`config.go:1576`）——无后端时静默 unconfined（`seatbelt_other.go:44` 仅启动警告）。本机实测 doctor 输出 "bash runs unconfined"、每次 run 打警告。
- **dsh 对照**：Windows 受限令牌 ACL 沙箱（WRITE_RESTRICTED token + 每工作区写 SID，FFI 全 API 失败即抛），fail-closed，诚实声明 full/partial。
- **整改**：①快速步：Windows Restricted Token 沙箱 + `require_available=true` 时拒绝执行；②完整步：enforcement 级别声明进 doctor 与工具结果；升级阶梯审批复用现有 permission Ask 流；③无沙箱宿主 doctor 给醒目风险项。
- **验收**：Windows 上 bash 写工作区外被拒且错误结构化；doctor 显示 enforcement；require_available=true 无沙箱即拒跑。

### P0-7 持久化原子性三处【已亲验】
- **证据**：①`internal/scheduler/scheduler.go:159-174` Store.save 用 `os.WriteFile(tmp, b, 0o644)`+rename——任务含 prompt 明文全局可读、无 fsync，仓库已有更严格的 `fileutil/atomicwrite.go:43-69` 未用；②`internal/checkpoint/checkpoint.go:599` RestoreCode 用裸 os.WriteFile 直写（同文件 :493 持久化却用 AtomicWriteFile——双标）；③`internal/ops/store.go:114-127` SaveRequest 仅写盘持锁，GetRequest→mutate→Transition→SaveRequest 全程无锁（`tools.go:99-112`）可丢状态跳变；`internal/netdev/writeauth.go:445` `_ = fileutil.AtomicWriteFile(...)` 丢弃 OpStep 台账写失败。
- **整改**：①②改走 fileutil.AtomicWriteFile(0o600)；③store 提供 UpdateRequest(id, fn) 全程持锁，工具层迁移；OpStep 写失败返回错误并升级审计 warning。
- **验收**：POSIX 下断言权限 0600；注入写失败时恢复不留半文件、opstep 返回错误；`-race` 并发 Transition 单测过。

---

## 2. P1 整改项

### A. LLM 恢复体系（含运行时对拍实锤）

**P1-A1 流中断恢复：每回合仅 1 次、anthropic 无重连、残文漏出**
- 证据：`internal/agent/agent.go:34` maxStreamRecoveries=1（第二次断流整回合失败）；`anthropic.go:176-177` readStream 无重放循环（openai 有 3 次透明重放 `openai.go:233-275`）；恢复靠追加用户消息污染缓存前缀。**运行时对拍**：`partial_disconnect,partial_disconnect,success` 序列——fairpeer 报 `unexpected EOF` 整回合死亡且两段残文漏进输出；dsh 同序列干净恢复（未 settle 的 chunk 不入日志）。
- 第三轮 UX 升级：①TUI 重试提示只显示 "1/1"，恢复后模型常把已显示内容重讲一遍且无分隔标记；②桌面 NoticeCard"重试"按钮语义是**整条重发最近用户消息**（`App.tsx:1350-1363`，重复计费），最近一条非 user 消息时**点击静默无反应**（`App.tsx:1361`）；③第二次断流的裸系统错误（read tcp...reset）无指引。
- dsh 对照：llm-retry 在持久 step 边界整体重跑失败 step，策略可配 5 次/无限（`retry-policy.ts:48-54`）。
- 整改：①maxStreamRecoveries 配置化（默认 3）；②anthropic 补对等重连；③转录插入"⚠ 连线中断，以下为续写"分隔；④桌面重试改"从断点继续"语义或移除，非 user 消息时禁用。
- 验收：mock 双次断流 E2E 输出最终答案、无重复残文；按钮无死点。

**P1-A2 上下文超窗无被动恢复**（两个 agent 独立确认）
- 证据：provider 返回 context-length 400 只展示错误并终结回合（`errmsg.go:53-58`、`controller.go:568`、流错误直接 return `agent.go:1430-1436`）；主动压缩仅 usage 驱动（`compact.go:100-164`），单条超大消息即可击穿。dsh：专门监听器命中 CONTEXT_WINDOW_EXCEEDED → 最大化压缩 → 授权重试（`compaction-basic/src/index.ts:180-224`，maxOverflowRetries）。
- 整改：provider 错误分类加超窗判定（复用 dsh `error.ts:51-86` 多网关正则集），agent 层捕获后强制 compact 再重试一次同一请求。
- 验收：E2E 构造超窗请求，回合自动压缩后完成而非报错。

**P1-A3 重试策略硬编码、无持久化、无无限模式**
- 证据：`retry.go:19` MaxRetries=10、退避封顶 15s 全硬编码；无 step 级持久重试。dsh：per-provider 可配 + always 模式（"stopping only on success, cancellation, or plugin disposal"）+ 重试前先落 llm/retry 事件（`llm-retry/src/index.ts:188`）。
- 整改：重试参数进 [provider] 配置；增加 retry_mode="normal|always"；重试计数写会话 sidecar。
- 验收：always 模式下限流风暴场景长任务存活（mock 注入验证）。

**P1-A4 稳定错误码分类学缺失**
- 证据：fairpeer 仅 AuthError/APIError/StreamInterrupted 三类；dsh 有 RATE_LIMIT/QUOTA/CONTEXT_WINDOW_EXCEEDED/EMPTY_RESPONSE/TRANSPORT/TIMEOUT/MALFORMED_RESPONSE/STREAM_CLOSED 等完整分类（`error.ts:25-100`），余额耗尽与限流可区分。
- 整改：定义 provider.ErrCode 枚举，各适配器归类；errmsg/前端按码分支（QUOTA 显示"余额/配额"而非"请求失败"）。
- 验收：QUOTA 错误 UI 文案正确分支。

**P1-A5 reasoning 模型支持浅：无 per-model effort 元数据与发送前校验**
- 证据：effort 是 provider 级静态开关（`openai.go:54-97` New 时校验），配错值运行时才 400；dsh 每模型声明 reasoning.efforts 集合并在网络 I/O 前校验（`llm/src/index.ts:860-895`）。另：OpenAI 兼容路径不回传 reasoning_content（`agent.go:898-900` 自认，与 DeepSeek 官方工具调用流程相悖，CoT 连续性受损——有意取舍但应可配）。
- 整改：模型元数据加 efforts 集合与默认值，请求前校验；reasoning_replay=on|off 可配（默认 off 保持现状）。
- 验收：配置不支持的 effort 在发请求前报可读错误。

**P1-A6 provider 自定义请求头 / key 轮换 / 模型发现深度 / 计量投影**
- 证据：请求头硬编码（`openai.go:203-205`、`anthropic.go:160-163`，headers 仅限 MCP `config.go:1393`）；单 api_key_env 无轮换/OAuth（`config.go:1186`）；模型发现仅 openai /models id 列表（`fetch_models.go:36-82`）；token 计量仅 lastUsage+Pricing（`provider.go:486-529`），无压力/构成投影、无图像视觉 token 估价（dsh token-meter 有三大投影+图像 14px patch 算法精确复现）。dsh：per-profile headers、OAuth 登录+自动刷新、含容量归一的发现解析、token-meter 全家。
- 整改：[[providers]] 增加 headers 与 api_keys_env（数组轮换）；发现解析补 context_window/output 上限；计量增加会话级投影（为 P2 统计面板供数）。
- 验收：企业网关自定义鉴权头与双 key 轮换可用；发现能解析容量。

### B. 上下文安全网

**P1-B1 工具结果溢出后不可找回（spill 缺失）**
- 证据：截断 32KB 首+尾（`agent.go:30,2151-2171`）、bash/jobs 1MiB 环形头尾缓冲中段丢弃（`jobs.go:74-78`）、剪除内容只进归档 jsonl 且占位符让模型"重跑工具"（`prune.go:75`、`agent.go:2167`）。dsh：spill 全文落盘+有界预览+locator+read/grep 取回指引（`spill-policy/README.md:55-57`）。
- 整改：新增 internal/spill——bash/job/工具输出超 256KiB 全文落盘 `~/.fairpeer/spills/`，占位符附路径与 read 提示。
- 验收：2MiB 构建日志场景模型能读回中段关键行。

**P1-B2 模型无法检索历史会话 + 搜索三重静默漏检**
- 证据：`SearchSessions` 是 UI 子串线性扫描（`internal/agent/search.go:38-96`），无模型工具。dsh：SQLite FTS5+模型侧 5 工具（session_search/event_search/trace/read，workspace 授权，`tool-session-query/README.md:47-53`）+跨会话 @引用。"按上次的方案"场景 fairpeer 基本失能（模型只能让用户人肉搬运）。
- 第三轮升级（UI 姊妹问题）：①>32MB 会话静默跳过（`search.go:34,71`）——漏的恰是跑一天的大会话；②只搜当前页签目录（`controller.go:2748`）而历史列表是跨目录并集——口径不一致；③50 命中静默截断（`search.go:31`），老会话天然排后最易被截。三处均无上屏提示。
- 整改：①新增 session_search 工具（复用 SQLite 建会话 FTS 索引，按会话作用域授权）；②三处边界上屏（"已排除 N 个大会话/仅当前目录/已到 50 上限"）；③FTS 化后自然消解。
- 验收：新会话中模型可检索到历史决策原文；搜索界面明示排除范围。

**P1-B3 记忆归档 recall 无检索面 + 无时间上下文**
- 证据：v0.4 删除 FTS 后 recall 只有"精确名/全列表"（`recall.go:47-90`；`store.go:48-52`）；会话无当前日期注入（dsh 有 time-context），记忆中的 deadline 无法判过期；portrait 预算仅 2000 字符（`memory.go:250`）。
- 整改：recall 增加 FTS5 关键词模式；boot 注入当前日期；portrait 预算提到 4000 可配。
- 验收：关键词可召回 30 天前归档事实；提示词含今天日期。

**P1-B4 摘要调用不复用暖前缀 + AGENTS.md 链无预算无会话中刷新**
- 证据：`summarize` 发全新两消息请求（`compact.go:665-671`），50 万 token 会话摘要全价重算；dsh 逐字节重放原前缀复用缓存（`compaction-basic/README.md:231`）。`Block()` 原样拼接全部 AGENTS.md 无字节上限（`memory.go:211-213`）；boot 一次发现后中途新建/变更不可见（`doc.go:78-101`）；dsh 有 64KB 预算+"丢宽保窄"降级+嵌套文件增量发现（`agent-instructions/README.md:70`）。
- 整改：摘要请求携带 pinned 前缀（system+首条）复用缓存；文档链加总预算与降级通知；编辑工具触达子目录时增量发现嵌套 AGENTS.md。
- 验收：压缩前后 cache 命中率统计（CompareShape 已有基建）；子目录 AGENTS.md 会话中途生效。

**P1-B5 二进制办公文档 200k 字节截断：静默内容缺失（v1.0 B-1 升格归位）**
- 证据：`document.go:103` const max=200_000，6 处截断全部字节切片 content[:max]（xlsx :124/docx :133/pptx :141/pdf :155/mindmap :173/csv+json :228）——UTF-8 汉字 3 字节，边界落字内概率约 2/3 → 尾部 U+FFFD 乱码。**更糟**：`:63` 描述明说 "binary formats cannot be paged"——第 200,001 字节之后的合同条款/表格行模型**永远读不到**，模型不知道自己少了什么。桌面 ReadFile 预览路径有 trimUTF8PartialSuffix（`desktop/app.go` 6100 附近），工具输出路径没有。附带：formatRows 列宽按字节算（`officedoc.go:873-878`），CJK 表格对不齐；text 单行超 1MiB 整体报错（`document.go:318`）。
- 整改：统一 truncateRunes 工具（按 rune 截断+回退边界）；doc_read/xlsx_read 二进制模式增加 offset/limit 翻页参数（超长文档可分段读）；列宽按 rune 宽度。
- 验收：中文文档截断处无乱码；300k 文本可分两次读完；单行超限降级截断不整体失败。

### C. 安全强制层

**P1-C1 密钥运行时全量暴露链**（已代码证实）
- 证据：启动 `LoadIntoEnv()` 全量明文进进程环境（`boot.go:741`、`store.go:26-43` 自认 audit A9 遗留）；bash 子进程全量继承 os.Environ()（`bash.go:273-285`）；`env`/`printenv` 在只读白名单**免审批**（`bash_readonly.go:11-16`）——一条免审命令导出全部 API key，再经免审 web_fetch GET 参数外传（`webfetch.go:50` ReadOnly=true）。stdio MCP 子进程同样全量继承（`transport_stdio.go:67`）。日志/导出无 redaction。dsh：worker 环境白名单只给 TMP/TEMP（`workflow-worker-thread/src/host.ts:48-60`）、MCP 子进程先剥 /KEY|PASSWORD|SECRET|TOKEN/i（`mcp-client/src/transport.ts:12-20`）。
- 整改：bash/MCP 子进程环境白名单传递（PATH/HOME/SYSTEMROOT + 用户显式 env_passthrough）；provider 密钥改请求时按需注入。
- 验收：bash 中 `env | grep -i key` 看不到 API key。

**P1-C2 bash 只读表白名单绕过**（运行时证实）
- 证据：`isReadOnlyBashSubject("npm audit fix")==true`（运行时测试通过）——该命令改写 lockfile、装包并**执行依赖 lifecycle 脚本**；`cargo check/doc` 同理执行依赖 build.rs（`bash_readonly.go:44-52` 白名单 + `:165-189` hasUnsafePrefixArgs 无 npm/cargo 分支）。Windows 上叠加 P0-6 无沙箱即实际执行链。
- 整改：hasUnsafePrefixArgs 补 npm 分支（audit 带参数即不可信）与 cargo 分支（check/doc 移出白名单——编译即执行 build.rs）；结构性测试遍历白名单逐项标注副作用。
- 验收：`npm audit fix`/`cargo check` 走审批；`npm audit --json` 仍放行。

**P1-C3 project-scope skill 零信任门 + 同名劫持内置技能**
- 证据：项目根 skill 优先级最高且无信任门（`skill.go:230-255`）；仓库放同名 `runas: subagent` 的 SKILL.md 可劫持 security_review/explore 执行体（`tools.go:303-311` 仅 RunAs≠subagent 时反弹），body 直进子代理 system prompt；scripts/ 绝对路径引导 bash 执行（`skill.go:630-663`）。
- 整改：项目级技能首次使用一次性确认（记 `.fairpeer/trusted-skills`）；内置技能名不可被项目级遮蔽或遮蔽时醒目告警。
- 验收：clone 恶意仓库后触发同名技能出现信任提示。

**P1-C4 MCP readOnlyHint 被信任免审 + 网络默认开 + fail-open**
- 证据：恶意 server 对危险工具自报 readOnlyHint:true 即交互模式免审批（`plugin.go:1021-1032,1152-1155` → `permission.go:328-329`）；Spec.Network 默认 true（`config.go:1572-1576`）→ 默认配置下沙箱不禁网；无后端 fail-open。dsh：无 readOnly 免审概念、fail-closed。
- 整改：readOnlyHint 仅作 UI 提示不作免审依据；network 默认改 false 或按 profile 区分；无后端 enforce 显式确认。
- 验收：自报只读的 MCP 工具仍走审批；默认配置 Seatbelt profile 含 deny network*。

**P1-C5 serve 无鉴权令牌 + 嵌入生态缺失**（第三轮精确化）
- 证据：CSRF（`serve.go:250-264`）/Host 校验（`:287-331`）/DNS-rebinding 防线**已建成**；真实缺口仅剩无 token——用户绑 0.0.0.0 供手机访问后 **LAN 内任何人可调 /auto-approve-tools 一键开 YOLO** 再提交任意任务（`serve.go:771-806`）、读 /history 拿走全部代码上下文。自定义 JSON 协议非 OpenAI 兼容，无官方 SDK/客户端库。dsh：launch token→签名 HttpOnly cookie+trustedHosts（`browser-auth.ts:12-18`、`api-request-trust.ts:69-103`）+ SDK 三件套（protocol/client/server，TS+Python 双语言）。
- 整改：①serve 增加 --token 启动门（token→会话 cookie，全路由校验），wildcard bind 警告改人话；②中期：对外发布协议包与最小 SDK；评估 OpenAI 兼容端点。
- 验收：无 token 远程请求 401；带 token 浏览器全功能可用。

### D. 协作与生态

**P1-D1 并行 fan-out 缺失：task 串行、experts 串行**
- 证据：`task` 自身 ReadOnly()==false（`task.go:176-178`）→ 连续 task 调用被并行派发门挡死（`agent.go:1655-1661`，task.go:54-55 自认）；experts 全串行（`orchestrator.go:18`）；并行只剩 background job 手工收割（task.go:258-295）。dsh：workflow 脚本 parallel()/pipeline()+worker thread+AGENT_CAP（`workflow/src/index.ts:113-125`）。
- 整改：task 增加 concurrent=true 参数（写型子代理走已有 worktree 隔离），宿主并发帽默认 3；experts 加 parallel 模式。
- 验收：3 个子目录并行任务墙钟≈最慢单任务。

**P1-D2 入站 webhook + agent 间消息**
- 证据：入站仅 IM 与 UI 导航 deeplink（`deeplink.go:32-39`）；webhook 全是出站（`netdev_app.go:136-137`）；子代理只返回 final message 无 send_message（`task.go:48-51`）。dsh：HMAC 验签 webhook→自动建会话+GitHub 适配器（`webhook-github/src/handler.ts:92-105`）；父子互发消息+持久 inbox+冷恢复（`tool-subagent-control/README.md:20-22`）。
- 整改：①internal/webhook：HMAC 验签入站端点→规则路由建会话（先 GitHub PR）；②task_message 工具：父↔后台子代理互发。
- 验收：GitHub PR ready_for_review 自动触发审查会话；后台子代理可被父追加指令。

**P1-D3 会话导出：CLI 有损 + 桌面两个残余缺口**（第三轮修正后）
- 证据：桌面端导出**已包含**推理与工具调用（`App.tsx:696-752`，Markdown/JSON/PDF/HTML 四格式）——原"导出有损"对桌面不成立；CLI `/export` 仍丢弃工具与推理（`chat_tui.go:3704-3759` 注释自认）。残余缺口：①导出的是前端内存 items，直播时已截断的输出导出也是截断版；②只能导出当前打开的会话（`App.tsx:3464-3466`），历史面板无导出入口；③无脱敏管线（dsh redactSecrets，`settings/src/redact.ts:105`）。
- 整改：CLI /export 补工具与推理；历史面板加导出入口（走桌面管线）；导出前可选脱敏扫描。
- 验收：CLI 导出含工具轨迹；历史面板可直接导出未打开会话；脱敏后无 api key 模式命中。

**P1-D4 MCP server 崩溃无自动重连**
- 证据：readErr 置位后快速失败，全包无 reconnect/respawn（`transport_stdio.go:58`）；用户只能 /mcp remove+add 或重启。dsh：指数退避 500ms→30s 最多 10 次、防 crash-loop、恢复后自动刷新工具集（mcp-client README:63-66,87-91）。
- 整改：stdio transport 加监督循环（退避重连+连续失败标记 unhealthy 摘除+doctor 可见）。
- 验收：kill MCP 子进程后工具自动恢复。

**P1-D5 Claude Code hooks 配置不兼容**
- 证据：fairpeer hooks 配置是扁平 {match,command}（`hook.go:96-113`），CC 嵌套 {matcher, hooks:[{type:command}]} 解析得 0 条；payload 键名 toolName/toolArgs vs CC tool_name/tool_input/hook_event_name（`hook.go:234-248`）。dsh 有专桥直接读 .claude/hooks.json（hooks-claude-code README:36-52，仅 7/30 事件但文件级兼容）。
- 整改：hooks 加载器兼容 CC 嵌套形状与下划线键名；增加 hook 执行持久审计事件。
- 验收：现成 CC hooks.json 不改一字生效（至少 PreToolUse/PostToolUse/Stop）。

**P1-D6 插件/分发闭环：无包管理、命令纯模板、profile 不可分发**
- 证据：技能/插件仅内容哈希 manifest 无 semver/lockfile/update（`manifest.go:46`）；slash 命令纯文本模板无 handler/allowed-tools（`command.go:148-154`）；profile 与代码耦合不可外部分发（`profile.go:111-217,491-500` 内建地板硬编码）。dsh：pnpm 转发器获得完整 npm 语义（`apps/cli/src/plugin.ts:2-8`）、带 handler 的命令（`docs/subsystems/commands.md:30-60`）、发行版=可安装 bundle+会话级 preset。
- 整改：①技能 frontmatter 加 version + install_source 支持 semver 与 upgrade；②命令 frontmatter 支持 allowed-tools/model；③profile 导出为 TOML+skills 分享包。
- 验收：技能可声明版本可升级；命令可限制工具面。

### E. 运维与编排

**P1-E1 割接门失败死循环：skip 缺失 + 文案误导 + hold 态无终止（第三轮升级）**
- 证据：门失败不前移 Cursor（`cutover.go:605-619`），Continue 从同 Cursor 重跑（`:469-491`）；proposal 步重跑因状态已非 approved 被拒（`proposal.go:682-684`）→ hold_note 显示自相矛盾的"验证门未过：变更 P… **执行失败**"（变更其实已落盘）——**该步永远不可能通过继续按钮通过**；UI 只在 running 态渲染终止按钮（`CutoverView.tsx:121,151-158`），hold 态无终止入口；无 CutoverSkipStep API。并发缺口：CutoverRollback 不持 cutoverMu（`cutover.go:495-531` 无锁读改写，对照 Continue/Abort 的 :449-487 临界区）——两人并发按 Continue/Rollback 时设备可能同时被回滚与执行，双方都收成功反馈。
- 整改：①CutoverSkipStep(id, step, reason)：仅 hold 态、reason 入审计链、runbook 标 skipped；②hold_note 区分"执行失败/验证未过/变更已落盘"三种文案；③hold 态补终止按钮；④Rollback 补 cutoverMu+saveCutoverLocked。
- 验收：单测门失败→skip→后续可继续；文案三种情形各对；`-race` 并发 Continue+Rollback 无丢失更新；值班演练通过。

**P1-E2 evidence 链缺口（第三轮精确化）**
- 证据：netdev_exec 回执 Command=""（`ReceiptFromToolCall` 只为 bash 提取，`evidence.go:550-552`）→ per-turn netdev_exec 分支成死代码；**更糟**：netdev_exec 被拒/设备报错时返回 JSON+nil error（`tools.go:1433-1437`）→ 回执 Success=true——**被拒命令被记成成功回执**；跨轮失败判定只认 "error:"/"blocked:" 字符串前缀（`evidence.go:528-539`）；跨轮匹配过松（非 bash 工具只比工具名，引用 "netdev_exec" 即命中任意历史调用，`completestep.go:440-457`）。
- 整改：netdev_exec 回执提取命令文本；失败判定改结构化字段（事件已带 is_error）；跨轮匹配比对命令文本。
- 验收：被拒命令不再记成功；跨轮引用须命令文本匹配。

**P1-E3 调度器语义五处（第三轮升级）**
- 证据：①周期任务宕机错过触发静默丢弃（`scheduler.go:26-27,302-330`，仅一次性任务补提醒）；②运行历史全局 100 条环形（`:220,576-582`），every-30m 任务两天挤光 daily 任务历史；③500 字符截断发生在**投递之前**（`:594-598` 先截断再 deliverOutput）——IM/邮件收到的也只有半句，UI 再叠 120 字符截断（`JobsPanel.tsx:104`）；④运行出错时投递失败 toast 被抑制（`:521` 条件 !runErr）双故障反而无提示；⑤bot gateway 平台缺失时 Push 返回 nil **假成功**（`gateway.go:378-382`）；IM `/approve` 对无效/过期 ID 无条件回"已批准。"（`:592-627`；对照 /desktop 分支诚实，`bot_bridge.go:295-313`）。dsh 对照：事件溯源+错过推进+派发前先落 dispatch 事件（`schedule/domain.ts:515-560`、`runtime.ts:279-301`）。
- 整改：①MissedRuns 计数并在下次投递附"期间错过 N 次"；②per-task 20 条+全局 500 条；③全文落运行历史 sidecar、投递文本 4000 字符；④投递失败 toast 不随 runErr 抑制；⑤Push 空适配器返回错误；/approve 校验 pending 存在才回成功。
- 验收：宕机 3 周期恢复带 MissedRuns=3；历史互不冲掉；IM 审批无效 ID 得到诚实回复。

**P1-E4 编排状态日志化：goal/plan/todo 崩溃可恢复（最大架构代差）**
- 证据：goal 状态全内存且有意不恢复（`controller.go:2500-2513` 注释明说）；plan=上一条 assistant 文本+正则抽数组（`:790,3837-3890`）；todo 纯内存协议。dsh：goal/plan/todo 全是会话日志事件，重启投影重放恢复；resume 后 goal 保留 durable phase 但降 disarmed 需人工 re-arm（`goal/src/index.ts:255-266`）。
- 整改：①会话 sidecar 增加 JSONL 事件流（goal/state、plan/mode、todo/write），追加写+序号；②启动重放恢复 goal 为 **paused**（不自动续跑，UI 提示"发'继续'恢复"）；③todo/complete_step evidence 门控保持不变仅加持久化。
- 验收：goal 模式中 kill -9 后 resume 处于 paused 且上下文完整；发"继续"接续（轮次预算延续）。

**P1-E5 会话持久化协议：fsync + 撕尾修复**
- 证据：会话保存 tmp+rename 无 fsync、无撕尾检测（`agent/save.go:139`）。dsh：撕尾识别+截断+fsync+目录 fsync（`session-persistence-jsonl/src/index.ts:592-599,979,1036-1037`）。
- 整改：AtomicWriteFile 增加 fsync（Windows FlushFileBuffers/POSIX）；会话加载识别末行残缺 JSON 截断告警。
- 验收：半行 JSON 会话文件可加载且丢尾告警。

**P1-E6 netdev job watchdog 预算不可恢复（从里程碑注记升格）**
- 证据：wall-clock/命令数预算累计持久（`job.go:433-446` 预算检查先于一切步骤，`j.ActiveMS` 持久累加 :478,503,521，`j.Commands` 只增），resume 不清零 → 撞墙暂停后按"继续"**第一个循环立即再次冻结**，同一条英文 pause_note 反复出现（"watchdog: wall clock 30.1m exceeds budget 30m"，`JobsPanel.tsx:219` 显示）——操作员视角"像坏了"；运行中无法调大 Budget；FailStreak 熔断可被成功步清零（`:490`）两种暂停行为不一致且无提示。
- 整改：①resume 时提供"预算续期"动作（重置 ActiveMS 或调大 Budget 的 UI/工具入口）；②pause_note 中文化并附下一步指引；③与 P0-5 的 interrupted 转换联动（转换时预算处理明确化）。
- 验收：撞墙暂停→续期→可继续执行；pause_note 有指引。

### F. 用户体验专项（第三轮新增）

**P1-F1 后台任务完成模型不自动得知**：DrainCompletedNote 只在用户下一条消息时注入（`input.go:160-164`、`controller.go:724`）——agent 收工后后台任务完成，模型不会醒，用户必须自己问；TUI 里 agent 后台任务输出无查看入口（`chat_tui.go:146-153` 只追踪用户 `!` 命令），桌面有 Jobs 面板——两端不对等。整改：turn 结束若有 job 完成自动追加零成本系统轮或通知唤醒；TUI 补 jobs 查看。验收：后台任务完成后无需用户发消息模型即得知。

**P1-F2 Goal 模式无预算显示**：50 轮上限撞了才知道（`controller.go:241,888,977-980`）。整改：Goal chip 显示"第 N/50 轮"，剩余 5 轮预警。验收：状态栏可见剩余预算。

**P1-F3 审批卡透明度三处**：①工作目录字段死代码——ApprovalModal.tsx:48,229 读 args.workdir/cwd，但没有任何工具定义该字段（grep 证实）；②风险等级 4 级体系设计为永不可见（`risk.go:8-10`）——bash 审批至少显示 EXEC/EXTERNAL；③桌面"总是允许"不显示将保存的规则作用域（`zh.ts:955-956`；TUI 反而显示）。整改：审批载荷补 cwd；风险级与规则作用域上屏。验收：bash 审批可见工作目录与风险级。

**P1-F4 Esc 语义与取消痕迹**：Esc 一键四义（撤回/取消整轮/清空/rewind，`chat_tui.go:937-973`），长流式时误触立即取消整轮且转录不留"已停止"痕迹（取消映射 nil err，`controller.go:560-568`；TUI 只在有 err 时打印 `chat_tui.go:3398`）。整改：运行中 Esc 二次确认（300ms 内双按）；取消后插入"⏹ 已由用户停止"行；桌面同补。验收：误触率下降；停止有痕迹。

**P1-F5 TUI 无 mid-turn steer**：TUI 运行中 Enter 只能排队（`chat_tui.go:1042-1062`），steer 管道只有桌面接（`useController.ts:1368-1374`）。整改：TUI 增加显式 steer 快捷键（Alt+Enter 插话）。验收：TUI 可中途纠偏。

**P1-F6 错误与系统文案混杂+无出口**：空回答终态（"model finished without a visible final answer 3 times" `agent.go:944-945`）、压缩管线全部 notice（`compact.go:113,136,143,149,160-162`）、goal 停止原因、watchdog pause_note 全部硬编码英文且无"下一步"——对比 maxSteps 错误有完整自救指引（`agent.go:994`）。桌面端 localizeNoticeText 只映射 4 条，内核英文通知透进中文 UI（`Transcript.tsx:1369-1376`）。整改：以 maxSteps 文案为模板补齐各终态出口；全部走 i18n。验收：文案审查无裸英文终态、无无出口错误。

**P1-F7 办公数据信任三连（已亲验）**：①`doc_write` append=true 对 .xlsx **静默覆盖**——xlsx 分支提前 return（`document.go:623-639`）绕过 csv 才有的 "(append not supported…overwrote)" 提示（`:752-758`），schema 描述也未声明；②docx 只传 content 不传 sections 时**静默生成空文档**并返回 "wrote X (0 sections)" 假成功（`:600-611`）；③文件锁探测只覆盖模板填充路径（`docxsafety.go:159-163`），主写入/append/xlsx 裸奔——Windows 下报裸 "Access is denied" 不说"请关闭 Word"；附带 checkCellValue（32767 上限守卫）是死代码零调用（`docxsafety.go:68`，grep 证实）。整改：①append+xlsx 返回显式覆盖警告或拒绝；②sections 空且 content 非空拒绝并提示；③锁探测提公共函数覆盖全部写路径；④checkCellValue 接入写入。验收：四个复现脚本转绿。

**P1-F8 办公产物不可视**：docx/xlsx/pptx/mindmap 卡片路径不可点击（`toolCards.tsx:84-98,193-197`，CSS 无 cursor/onClick）；无内嵌预览（面板预览仅文本抽取 `app.go:6041-6057`）；产物落点三层深（`session_profile.go:55-61` + `projects/<名>/exports/`）；mindmap .html 硬编码 jsdelivr CDN（`mindmap.go:136-138`）内网白屏，成功消息却承诺 "double-click to view"（`:186`）。整改：卡片路径可点击（reveal in explorer）；docx/xlsx 接已有面板预览；mindmap 资源本地化。验收：全链路应用内可见产物。

**P1-F9 历史管理静默失败集**：删除/改名/恢复/彻底删除全部 `.catch(() => {})`（`useController.ts:1526-1529`）——删除打开中会话返回 errActiveSession（`sessions.go:20`）、跨目录校验失败、恢复撞名，用户全看不到（两步确认走完、行还在）；侧栏"最近"运行期点击死点击（`App.tsx:2778` 无提示）。整改：失败 toast 化；运行期点击给提示。验收：所有失败操作有可见反馈。

**P1-F10 首跑三连（阻断 adoption 最大单点摩擦）**：①CLI setup 无云厂商预设——菜单仅 ollama/lmstudio/llamacpp/Custom/Anthropic（`cli.go:868-879`；`config.go:1622-1660` 只回灌本地预设），DeepSeek/通义/智谱新手被迫手打 base_url+env 名（打错首次对话 401 才爆），桌面端却有完整厂商模板+key 探活（`desktop/provider_templates.go`+ProbeVendorKey）——两端严重不对称；②未知 TOML 键静默忽略（全仓无 Undecoded() 调用，grep 证实）——default_model 打错后掉进两层外的 `unknown model ""`（`boot.go:269` 文案本身尚可）；③doctor 只陈列事实无处方、不探活 URL/key（`report.go:248-343`）——救不了"agent 不干活"新手；附带 doctor 全文不本地化。整改：①CLI 向导接入桌面同一份厂商注册表；②mergeFile 后 Undecoded 键黄字警告；③doctor 增加 action 列与 base_url/key 探活。验收：新手三步内从裸装机到首次对话成功。

**P1-F11 崩溃恢复无痕迹**：30s 快照间的内容（流式文本/已完成工具调用）崩溃后静默消失（`controller.go:2589-2610`；前端注释自认 `useController.ts:1246-1248`），转录戛然而止、无中断标记、无续跑入口——plan/YOLO 恢复反而有通知（`controller.go:2521-2530`）。整改：会话元数据记 interrupted，重开顶部横幅"上次运行中断于第 N 轮"+一键续跑。验收：崩溃重开有横幅与续跑。

**P1-F12 误报学习永久压制**：同源 ≥2 次误报 → 该设备该类 syslog/trap 永久降 info 且不再推送（`alertqueue.go:80-88` 持久表无 TTL/衰减/解除入口；推送门槛默认 warning `notify.go:70-74`）——凌晨同类真故障值班手机不响，唯一线索是聚合行 Suppressed 计数。整改：抑制表 TTL 默认 7 天+阈值提 3+大屏/IM 一键解除+降级 Finding 每日摘要兜底。验收：被抑制源的真告警仍能推送（TTL 过后或人工解除）。

**P1-F13 审批请求跨 tab/跨应用不可见**（从判定表归位）：ApprovalModal 只在拥有该审批的 tab 渲染（`App.tsx:3219-3238`），无跨 tab 角标/汇总；OS toast 仅一条无 ID 的 "Approval needed"（`notify/sink.go:58-62`）——confirm 档写审批或提案审批可能长时间无人理会，聊天侧表现为"turn 卡住"。整改：tab 标题角标+全局审批汇总入口；toast 带上下文。验收：审批等待时任何 tab 可见。

---

## 3. P2 整改项

### 3.1 前端渲染与显示（v1.0 P2-1~8 恢复全文）

**P2-1 流式渲染：增量 markdown（冻结块方案）**
- 证据：`Markdown.tsx:216,236-242` 流式时每帧对整条消息全文 re-parse+全树重建（rAF 合帧已做但解析成本 O(全文)）。dsh：除最后两块外全部冻结为缓存 React 元素、只 re-parse 尾巴、块 key 用源偏移量（`ui-primitives/markdown/MarkdownText.tsx:63-142`）。
- 整改/验收：块级缓存层（按 fence/段落边界 split，完成块 memo，尾块随 delta 重渲染）；React Profiler 断言每 delta 提交时长上限。

**P2-2 代码块渲染：修硬编码深色+去掉每块一个 CodeMirror**
- 证据：`editors/CodeMirrorCode.tsx:9` `const theme="dark"` 硬编码（浅色主题全黑代码块；桌面默认主题恰是 light，`config.go:241`）；`CodeViewer.tsx:22` 每块懒加载一个 CodeMirror 实例；HljsCode 闲置。dsh：shiki 视口门控+懒语法包+CSS 变量主题（`CodeBlock.tsx:63-67`）。
- 整改/验收：聊天块默认 HljsCode+主题接 data-theme+视口外延迟高亮；浅色主题代码块浅色。

**P2-3 转写性能三连**：①`Transcript.tsx:89-102` scrollVersion 每次 items 变化全量 map+join——改 reducer 内增量版本号；②热区节点数组随流式每帧重建（`:547-731`）——按 item 分组 memo；③DOM 无虚拟化——冷温区先行。dsh 对照：滚动采样 500ms+prepend 锚点补偿（`ChatView.tsx:503-521,584-610`）。验收：1000-item 会话流式提交次数达标；加载旧历史不跳屏。

**P2-4 会话统计面板**：fairpeer 仅 usage chip+contextInfo 数字（`App.tsx:2099,3269`）。dsh：StatsLine 步数/LLM 耗时/工具耗时/TTFT/decode tok-s/cache 命中率/输入输出分桶（`StatsLine.tsx:163-208`）+ContextMeter 环形占用+三段分解（`ContextMeter.tsx:106-166`）。后端 e2ebench 已消费 cache_hit_tokens，只差前端呈现。整改：turn 结束事件补 ttft/decode 速率；统计条+上下文弹层。验收：底部可见 TTFT/tok-s/cache 命中率。

**P2-5 工具卡折叠行信息密度**：折叠后仅"名称+subject"（`ToolCard.tsx:212-249`）。dsh：折叠行内嵌可点击文件路径（`ToolRow.tsx:209-217`）、diff 行 +N -N（`:165-170`）、错误替换摘要（`:159-161`）。整改：write/edit 卡显示路径与 ±N；错误态折叠行显示错误首行。验收：批量编辑 10 文件不看展开即知 ±行数。

**P2-6 感知延迟三招**：思考折叠行滚动最新一行（dsh `ReasoningRow.tsx:26-61`）；turn 运行 15s 后时长时钟（`ChatView.tsx:168-201`）；本地提交乐观回显+原子替换（`MessageItem.tsx:273-317`）。fairpeer 已有"推理首行粗体作状态"（`App.tsx:3293-3296`）。验收：三项可感知、无双份闪现。

**P2-7 i18n 硬编码中文清理**：Transcript.tsx:1389 "重试"、:1433 与 ToolCard.tsx:236 "在编辑器中打开"、Message.tsx:462 "文件与当前状态一致"、ToolCard.tsx:266 "补丁生成中——实时预览"、DocPreview.tsx:47、ErrorBoundary.tsx:71。整改：迁 locales+ESLint 规则禁止组件内中文字面量。验收：英文界面无中文漏出。

**P2-8 前端小项集**：ANSI 解析进 bash 卡（dsh TerminalBlock 的 ansi.ts 思路）；xterm 主题接令牌（`TerminalSession.tsx:52-55` 硬编码 Catppuccin）；图片比例 clamp+失败重试+相邻画廊合并（dsh `MessageImage.tsx:40-120`）；拖放全屏反馈层+禁用态插画（`DropOverlay.tsx`）；跨分页跳轮次自动 loadThrough（`ChatView.tsx:715-751`）；工具行 a11y 文本（`ToolRow.tsx:99-106`）；max-tokens 截断专用提示+重试倒计时（`MessageItem.tsx:64-160`）；registerShortcut 空壳实现或移除（`keyboardShortcuts.ts:46-49`）。

### 3.2 后端小项（v1.0 B-1~B-17 恢复；B-1 已升格为 P1-B5）

| # | 问题 | 证据 | 整改 |
|---|---|---|---|
| B-2 | xlsx_read full 模式忽略 sheet 参数（json.Unmarshal 静默丢弃；overview/page 正常） | `document.go:81-85,851-853` vs schema `officedoc.go:818` | full 分支透传 sheet 或 schema 改文案 |
| B-3 | apply_patch 强制补尾换行+归一化破坏混合行尾 | `apply_patch.go:202-209,266-268` | 仅原有尾换行才补；单一行尾才归一 |
| B-4 | multi_edit replace_all 绕过 5 级模糊（纯精确 Count/ReplaceAll） | `multiedit.go:100-106` | replace_all 复用 fuzzy 层级，歧义报错 |
| B-5 | 模糊匹配 lineTrim/indentNorm fallback 死路径（返回必败文本而非继续下一层级） | `edit_fuzzy.go:284-285,310` | fallback 返回"层级不可用"信号继续降级 |
| B-6 | LSP 诊断等待硬编码 2s（大工程常拿空诊断且不可配） | `internal/lsp/manager.go:255` | 配置化默认 2s 上限 30s；未就绪返回"诊断进行中" |
| B-7 | 写前语法校验仅 .go/.json | `internal/validation/validator.go:49-58` | 至少加 Python（ast）；TS/JS 启发式；失败仅警告 |
| B-8 | DOCX 列表项样式静默丢弃 | `docxwrite.go:920-921` | renderList 透传 DocStyle |
| B-9 | append 图片 docPr id 可能冲突（1000+imgIdx 不扫既有 max；rId 已修） | `docxwrite.go:1014-1016,516-529` | 扫现有 docPr id 取 max+1 |
| B-10 | OCR 逐页重复 fitz.open 整个 PDF | `ocr_pdf.py:111-119` | 打开一次循环取页 |
| B-11 | text 单行超 1MiB 整体失败 | `document.go:318` | 捕获 token too long 降级截断（并入 P1-B5） |
| B-12 | firstLineOf 把 `\` 当行分隔（Windows 路径被截断） | `cutover.go:433` | 只按 `\n\r` 切 |
| B-13 | ops ListRequests 读目录出错静默返回空（"尚无记录"误导） | `ops/store.go:151-154` | 返回错误，前端区分 |
| B-14 | ListOpSteps 按 ID 字典序混排设备 | `writeauth.go:488` | 按 (Device, Nanos) 排序 |
| B-15 | jobs 无所有权围栏（拿到 id 即可 Kill/Output） | `internal/jobs/jobs.go` | 非本会话 job 拒绝 |
| B-16 | PowerShell 链式守卫只拦 &&/\|\|（; 语义差异不提示） | `bash.go:136-140` | `;` 出现时一次性警告 |
| B-17 | go vet unsafe.Pointer 提示 5 处 | `screen_windows.go:571`、`uia_windows.go:239-244` | 改 unsafe.Slice 或注明理由 |

### 3.3 第二轮新增后端项（B-18~B-32 恢复）

| # | 问题 | 证据 | 整改 |
|---|---|---|---|
| B-18 | Retry-After 不解析 HTTP-date；错误不带 x-request-id | `retry.go:155-164`；dsh `adapter.ts:311-324` | 补 date 解析；捕获 request-id |
| B-19 | 配置无热更新（改 endpoint/key 需重启） | boot 构造期解析 | settings 重读路径 |
| B-20 | 模型发现浅（无容量归一/Anthropic 原生发现）；无 compat 开关族 | `fetch_models.go:36-82` | 发现解析补全（并入 P1-A6） |
| B-21 | MCP 图片等非文本结果被丢弃留标记 | `plugin.go:1242-1250` | 图片块校验后入上下文 |
| B-22 | MCP 无 tools/list 分页（大目录静默丢工具）、无 structuredContent、协议版本固定 2024-11-05 且不协商 | `plugin.go:30,1035-1044,994-1015` | 分页+outputSchema+版本协商 |
| B-23 | web_fetch 放行 loopback、代理路径域名不校验 SSRF | `webfetch.go:53-57,106-115` | 非公网默认拒绝（白名单可配） |
| B-24 | read_file/grep/MCP 结果无 untrusted 围栏（web/rag/browser 已做，fence 消毒已就绪） | `readfile.go` 零命中 | 本地读取统一 WrapUntrusted |
| B-25 | 审计链仅覆盖 netdev/trustdomain，工具调用/审批无结构化事件 | `chain.go` | 工具/审批事件进会话 sidecar |
| B-26 | 更新链含第三方镜像 ghproxy.net；镜像陈旧→静默"已是最新"（checkError 不上 banner）；安装重启无过渡提示。降级防护本身已存在（semver 比较） | `updater.go:44-55,143-169`；`UpdateBanner.tsx:63-66` | 镜像检查失败上 banner；重启前过渡页 |
| B-27 | RAG 无文件监听/定长 3000 字符切块/词法首跳无向量兜底/同义实体不合并 | `rag/store.go:1100,1126`；`embedding.go:25-30` | fsnotify 增量、结构感知切块、零命中向量首跳 |
| B-28 | 会话标题启发式（首条消息截 24 字，title/preview 同源） | `tabs.go:1591-1677` | fast_task_model 生成标题（dsh session-title 策略参考） |
| B-29 | internal/memory/doc.go:10-15 宣传已删除的 bitemporal/memory_query | `store.go:48-52` | 修文档漂移 |
| B-30 | stdio MCP 子进程继承全量环境 | `transport_stdio.go:67` | 并入 P1-C1 白名单 |
| B-31 | 技能无 model/user 调用面拆分、无目录热刷新 | `skill.go:490-521` | frontmatter 加 user_invocable；watcher |
| B-32 | 移动端 mobilebridge 客户端未交付 | `mobilebridge/doc.go:9-10` | P1-C5 鉴权落地后 Web 响应式适配 |

### 3.4 第三轮 UX 项（U-1~U-21）

| # | 问题 | 证据 | 整改 |
|---|---|---|---|
| U-1 | Ctrl+F 计数含冷区但跳转静默失效（锚点在未渲染冷区不存在；计数骗人比不支持更伤） | `Transcript.tsx:435-475,863-866` | 命中冷区自动 loadThrough |
| U-2 | （并入 B-28 会话标题） | `tabs.go:1591-1677` | 同 B-28 |
| U-3 | 重载后子代理 prompt/旁白成"幽灵用户消息"（user/assistant 项被推到顶层）；前端轮次编号与 checkpoint 编号错位（rewind 定位风险，建议运行时复现确认严重度） | `useController.ts:300-321`；`agent.go:824` | 子转录项保持折叠在 task 卡内；编号统一走后端 |
| U-4 | 首次打开重会话白屏 Welcome→内容弹入，无骨架屏（await 7 个 bridge 调用） | `useController.ts:1168-1197` | 骨架屏+分批渲染 |
| U-5 | 回滚粒度按轮无按文件排除（并入 P0-2 ③） | `checkpoint.go:554-572` | 同 P0-2 |
| U-6 | BranchTree 分支无名（退化时间戳 id）、preview 字段前端不渲染、无 fork 位置提示 | `BranchTree.tsx:98-118` | 渲染 preview+fork 轮次 |
| U-7 | 历史面板自动预览抢焦点：每击键换预览、无防抖 | `HistoryPanel.tsx:196-209` | 预览防抖 |
| U-8 | 调度器中文时间解析坑："明早8点"提前 12 小时今天触发（"明早"不在词表）；"每个工作日早上九点"报英文错（中文数字不识别、无映射提示） | `reltime.go:75-77,150-151,308-309`；`expr.go:52-53` | 补明早/今晚/工作日词表与中文数字；错误给最近似合法表达式 |
| U-9 | 割接深链：手机上死文本（无 IM cutover 指令，findings 有 /netdev 兜底而 cutover 没有）；前端丢弃 id 只落大屏 | `netdevcmds.go`；`App.tsx:1801-1802` | IM 补 cutover continue/abort（管理员门）；深链带 id |
| U-10 | （并入 P1-E1 ④ Rollback 补锁） | `cutover.go:495-531` | 同 P1-E1 |
| U-11 | 运行中 Ctrl+V 静默吞掉；TUI 审批横幅长命令截断无展开 | `chat_tui.go:1021-1024,2518` | 粘贴提示；横幅展开键 |
| U-12 | doc_convert 拒 docx→pdf 不指路（不提 Word COM 自动化出路）——高频请求被答"做不到" | `document.go:1003` | 错误补真实出路 |
| U-13 | cowork 路由纯语义表无确定性兜底，"做个PPT"走错 document-auto 后空转（其工具集无 pptx 写能力） | `profile.go:233,831`（builtins.go:826/831） | 路由失败回退提示；描述加硬边界 |
| U-14 | （并入 P1-F7 ④ checkCellValue） | `docxsafety.go:68` | 同 P1-F7 |
| U-15 | 日历：跨休眠 >10 分钟提醒静默丢弃无补发；IM/邮件推送失败只写 debug 日志、MarkReminded 照写（永无重试） | `reminder.go:124-136,187-205` | 开机补发；失败上屏+不写去重标记 |
| U-16 | 桌面中文/CLI 英文双轨（中文 Windows CLI 默认英文：CLI 只读 env 不读 UI language）；内核英文通知透进中文 UI | `i18n.go:422-429`；`config.go:216-218`；`Transcript.tsx:1369-1376` | CLI 语言探测补 UI language；notice 映射扩容 |
| U-17 | PowerShell 降级警告一行 stderr，GUI 不可见（sync.Once 且非 Notice 事件）——之后 bash 语法持续翻车不知为何 | `boot.go:622-627` | 降级时注入一次性 Notice 进转录 |
| U-18 | 桌面 exe 不认 --help（弹窗）+每次启动 deeplink 日志噪音；仓库根多代 exe 并存加剧混淆 | `desktop/main.go:67-97`；`deeplink_windows.go:25-49` | 桌面 exe 带 --help/--version 出口；.gitignore 产物 |
| U-19 | 会话无保留策略：回收站永不自动清；.display.json 无限累积；1000 会话时 serve /sessions 全量解析+未命中逐个 LLM 生成标题（烧 token 卡死） | `sessions.go`；`serve.go:987-1028,1114-1139,951-983` | 回收站 30 天 TTL+预览缓存+列表分页 |
| U-20 | 回收站 purge（不可逆）与 trash（可逆）确认视觉无分级；恢复撞名硬失败且被吞 | `HistoryPanel.tsx:256-349`；`sessions.go:231-235` | purge 二次输入确认；撞名自动改名 |
| U-21 | 更新镜像陈旧静默漏更+重启空窗（并入 B-26） | `UpdateBanner.tsx:63-66` | 同 B-26 |

---

## 4. 必须守住的优势（三轮确认，25 项）

1. evidence ledger + complete_step 证据强制（完成项无回执硬失败）——dsh todo 完全信任模型自报。
2. 独立 goal judge（"模型自称完成只是证据不是证明"）+ strict 模式 + idle 检测。
3. auto-plan 启发式+LLM 分类器——dsh 无自动进 plan 机制。
4. finalReadinessCheck 就绪门禁（未完成 todo 或写后没跑检查命令就拦截回答）。
5. cache 形状诊断（CompareShape）+ wire 级 cache_control 断点/prompt_cache_key——dsh 无 wire 级 cache 控制。
6. worktree 写隔离子代理与 transcript continue_from/fork_from。
7. 循环硬闸三件套（opGate/stormBreaker/repeatSuccessBlock）——dsh 仅建议性提醒。
8. 编码保真全家：GBK/UTF-16/BOM/CRLF 全程保留、写前语法闸、diff 进模型反馈环、截断 finish 整批工具调用作废、closeTruncatedJSON 参数修复——dsh 只检测不修复。
9. 运维全栈：14 态请求生命周期、三档写授权锁、审计哈希链、割接人机混合控制流、runbook 断点续跑、五屏大屏。
10. 办公全栈：OOXML 读写/模板填充/excelize 富表格/三级 PDF 流水线/VLM PPT 模板/RFC5545 日历+农历/公文支持。
11. 桌面体验：ConPTY 内嵌终端、Ctrl+F HAST 高亮、会话回收站/预览/导出、7 风格主题、快捷键速查表。
12. 调度结果多通道路由（IM/邮件/文件/通知+探活）——dsh schedule 只投回模型上下文。
13. 全局 RPM 预算与主/后台优先级——dsh 明示不做限流。
14. 代理支持（auto/env/socks5/no_proxy）——dsh 直连无代理。
15. 双人审批（IM 管理员门控+推送订阅+先到者赢）——dsh 无多用户身份。
16. 项目×模式记忆隔离+portrait/Dream 自动记忆——dsh 无任何自动记忆层。
17. RAG 全家（FTS5 CJK/混合重排/LLM 扩展/图谱/office 解析/AutoSearch）。
18. 压缩保留底线（user 原话逐字保留/首条锚定/增量摘要/机械折叠兜底）。
19. MCP 协议覆盖广度（prompts/resources/elicitation/progress/OAuth PKCE——dsh 全部未实现）。
20. hook 事件广度（12 vs 7）与技能执行控制（allowed-tools/runAs/model/effort）。
21. **审批会话级授权**：20 次编辑 2-4 次确认（permission.go:616-670 统一 Edit 作用域+bash 前缀规则）——重构勿破坏。
22. **压缩摘要卡全文透明**：用户逐行可读模型记得什么，/compact <焦点> 定向补强（chat_tui.go:2364-2381）——同类最高透明度。
23. **桌面审批卡**：文件列表+unified diff+j/k/e 导航+bash 高亮（ApprovalModal.tsx:163-231）。
24. **turn 汇总卡**（"Edited N files +X −Y"）与 **Esc 未回包撤回**。
25. **双页笔回滚事前警告**：unsafe 文件清单在确认步骤展示（Message.tsx:397-417）——加强（P0-2）而非重做。

---

## 5. 实施顺序与里程碑

### 5.0 执行取舍原则（v1.4 新增）

**优先级裁定原则：交互可见的体验项升为一等优先。** 一项整改是否值得做，按"用户能不能看见变好"分四档：

| 档位 | 定义 | 处置 |
|---|---|---|
| **T0 信任前提** | 数据丢失/假成功/安全可利用——出一次用户就卸载 | 必修，先于一切（约 15 项） |
| **T1 看得见的体验**（本版升格） | 每次打开就能感到的变好：渲染流畅、配色统一、反馈及时、信息密度 | 一等优先，独立体验升级包排期（约 20 项，见 5.0.1） |
| **T2 高频痛点** | 特定场景疼（弱网断流/历史检索/首跑配置） | 按场景触发排期（约 15 项） |
| **T3 幕后与追赶** | 架构对齐 dsh、性能埋点未证实的优化、生态建设 | 放 backlog，用真实反馈触发；"差距≠债务" |

**明确缓做的代表项**（避免为想象需求施工）：P1-E4 全量事件溯源、P1-D6 插件包生态、B-19 热更新、P2-3 虚拟化（先埋点测量）、B-27 RAG 结构感知切块。**明确保留的取舍**：reasoning 不回传是省 token 的设计，P1-A5 只加开关不改默认。

### 5.0.1 体验升级包（T1，"改完用户会看到什么"排序）

每项一句话描述交付后的可见变化，按 第一眼冲击 × 出现频率 排序：

| # | 项 | 用户会看到 |
|---|---|---|
| X1 | P2-2 代码块主题 + xterm 接令牌（含 U 系主题项） | 浅色主题下代码块不再是黑底——默认主题第一眼修复 |
| X2 | P2-7 i18n 漏字清理 | 界面不再中英混排（"补丁生成中…"出现在英文 UI、"重试"按钮等 7 处） |
| X3 | P2-6 感知延迟三招 | 思考折叠行滚动显示模型在想什么；长任务 15 秒后出现计时钟；发送即回显不再等首包 |
| X4 | P2-5 工具卡折叠行信息密度 | 折叠状态就能看到文件路径（可点击）和 +N −N，批量编辑一眼扫完 |
| X5 | P2-1 增量 markdown | 长回答流式输出不再卡顿掉帧（冻结块只重解析尾巴） |
| X6 | P1-F8 办公产物可视化 | 生成的 docx/xlsx 路径可点击、应用内可预览；思维导图不再内网白屏 |
| X7 | P2-4 统计面板 | 底部出现 TTFT/tok-s/cache 命中率——观感专业度直接提升 |
| X8 | P1-F4/F13 停止痕迹+审批角标 | 按 Esc 停止有"⏹ 已停止"行；审批等待在任何页签有角标不再"假死" |
| X9 | U-4 骨架屏 + U-1 Ctrl+F 冷区修复 | 打开大会话不再白屏闪现；搜索跳转不再静默失灵 |
| X10 | P2-8 小项集 | bash 卡片颜色码正常渲染；图片画廊化；拖放有全屏反馈；max-tokens 有专用提示 |
| X11 | P1-F2/F6 状态与文案 | Goal 显示"第 N/50 轮"；错误文案中文化并带下一步指引 |
| X12 | U-6/U-7 分支树与历史面板 | 分支树显示摘要不再是一排时间戳；搜索预览不再抢焦点 |

X1+X2+X3+X4 是"一屏之内立刻可见"四件套，建议合为一个版本发布；X5 是流式手感的大头，单独排期。

### 里程碑表（v1.4 调整：体验包并行插入）

| 里程碑 | 内容 | 出口标准 |
|---|---|---|
| M1（1 周）安全+数据信任急件（T0） | P0-1（两行+结构性测试）、P1-C2（白名单分支）、P0-4（测试红灯）、P0-3（OCR 含正文注入/遮蔽/静默安装三修）、P1-F7（xlsx append/docx 空文档/锁探测/死代码守卫）、P1-E3 之⑤（/approve 与 Push 假成功）、P1-F10 之②（未知键警告） | 每项复现脚本转绿；`make test` 连续 5 次全绿 |
| **MX-1（1.5 周，可与 M1 并行）体验四件套（T1）** | **X1 代码块/终端主题、X2 i18n 漏字、X3 感知延迟三招、X4 工具卡折叠行** | 浅色主题全链路无黑块；英文 UI 无中文漏出；折叠卡可见路径与 ±N；三招可感知 |
| **MX-2（2 周）体验第二波（T1）** | **X5 增量 markdown、X6 办公产物可视化、X7 统计面板、X8 停止痕迹+审批角标** | 长回答流式 Profiler 达标；办公产物应用内可看；统计条可见；停止/审批有痕迹 |
| M2（2 周）值班体验急件（T0/T2） | P0-2（CAS+会话锁+回滚文件勾选备份）、P0-5（撞号+runner 恢复扫描）、P0-6/7、P1-C1/C3/C4、P1-E1（skip+文案+hold 终止+补锁）、P1-E6（watchdog 续期）、P1-F12（抑制表 TTL/解除） | 值班演练：门失败可跳过、watchdog 可恢复、重启有中断标记；env 无密钥泄漏 |
| **MX-3（持续）体验清仓（T1）** | **X9~X12 + P2-8 小项集** | 按各自验收标准 |
| M3（4 周）恢复体系（T2） | P1-A1~A4（含 UX 三处）、P1-B1（spill）、P1-B2（session_search+三漏检上屏）、P1-B5（200k 截断+翻页）、P1-F6（文案出口+i18n） | mock 双次断流 E2E 过；超窗自愈过；文案审查过 |
| M4（6 周）编排与协作（T2） | P1-E4/E5、P1-D1/D2、P1-F1/F2（后台唤醒/goal 预算）、P1-F4/F5、P1-F11 | goal 崩溃恢复演示；PR→审查会话链路 |
| M5（backlog，T2/T3 按反馈触发） | P1-A5/A6、P1-B3/B4、P1-C5、P1-D3~D6、P1-F3/F9/F10/F13、其余 P2 与 T3 项 | 真实用户反馈驱动，对比报告不再作为唯一触发源 |

---

## 6. 验证与证据基线

**运行时实测（21 项）**：
1. go build ./... ✅；2. go vet（5 条 unsafe 提示→B-17）✅；3. 根模块全量测试：rag 1 稳定失败（P0-4）+ boot 负载偶发；4. desktop 全量 ✅；5. tsc ✅；6. vite build ✅；7. CLI 全套子命令 ✅（根目录 fairpeer.exe 为 8/31 陈旧桌面产物，会误导测试）；8. run/serve 无模型错误 UX ✅；9. OCR TypeError 运行时复现 ✅；10. 办公工具单测 ✅；11. fairpeer 文本轮 E2E（OpenAI 兼容 mock）✅；12. fairpeer 工具循环 E2E（bash 调用→执行→回填→终答）✅；13. dsh build:lib:host ✅；14. dsh headless E2E ✅；15-16. dsh vitest 抽样 273+557 项全过 ✅；17. 两侧共用同一 mock LLM 服务器完成端到端；18. fairpeer 对 500,500,success 恢复成功；19. 单次 partial_disconnect 两侧均恢复；20. **双次 partial_disconnect 对拍：fairpeer 整回合死亡+残文漏出，dsh 干净恢复（决定性差异）**；21. `isReadOnlyBashSubject("npm audit fix")==true` 运行时证明。

**第三轮 UX 验证**：5 席位逐行走查；10 项关键发现亲自复核属实（xlsx append 绕过提示、docx 空文档假成功、checkCellValue 零调用、watchdog 预算累计、无 runner 恢复扫描、ApprovalModal workdir 死代码、无 Undecoded 调用、CLI setup 无云厂商预设、删除静默吞、Ctrl+F 冷区锚点）。

**降级修正记录（7 项，诚实校准）**：①审批摩擦比预想好（会话级授权）；②压缩 UX 透明度是优点；③桌面导出已含工具与推理（仅 CLI 缺）；④更新器降级防护已存在（真问题是镜像静默漏更）；⑤双页笔回滚 unsafe 警告是事前展示（缺的是强制保护）；⑥serve 的 CSRF/Host/DNS-rebinding 已防（缺 token）；⑦旧 exe --help 无输出属桌面二进制固有（范围限定）。

---

## 7. 版本差异说明

### 7.0 五遍复排查结果（2026-09-12，15 个子任务）

五遍、每遍 3 个子任务的遗漏排查（修复质量复核 → 未审子系统 → 并发契约 → 场景盲区 → 运行时终对账）共产出 **105 条原始发现，去重合并后 70 项**，全部收录于 **§8 遗漏清单**（本节只记结论）。核心教训与宏观结论：

1. **我们自己的修复有半修/回归面 11 项**（NEW-12/14/16/27/28/30/31/32/33/53/59/60/63 等，含 3 个测试回归已当场修复：control 信任门、mobilebridge pion v4、anthropic 通道归属）。修复不是一次性的——每项交付都需要"兄弟点扫描+组合场景回归"。
2. **两个从未审计的面贡献了最多高危**：会话生命周期基础设施（HMAC .sig 三重缺陷、SwitchBranch TOCTOU、checkpoint symlink 逃逸）和 RAG 深层（embedding 缓存永不命中、AutoSearch 5 分钟阻塞、删除不剪枝）。
3. **runtime 终验 6 场景 5 PASS 1 SKIP**（工具循环/新配置键/未知键告警/session_search E2E/serve token 全过；export 为 TUI-only 不可 headless 达）。
4. 测试基线：五遍排查后全仓 67 包 ok + 3 处测试回归当场修复归绿；mobilebridge 两个 ICE E2E 为仓库主人进行中工作（Windows 回环环境敏感），不属于本 spec 范围。

### 7.1 执行进度（2026-09-11 M1+MX-1 完成记录）

**M5 进度（2026-09-11 第五批，“干完”批次）**：

| 项 | 交付 | 验证 |
|---|---|---|
| P1-C5 | **核实已由仓库主人并行实现**（--token 旗标+FAIRPEER_SERVE_TOKEN+非回环无 token 大声警告+tokenGuard 全路由 401）；补 tokenGuard 回归测试锁住 header/query 两种放行形态 | TestTokenGuard 绿 |
| P1-F10① | CLI 向导接入 11 家云厂商预设（qwen/deepseek/volcengine/zhipu/minimax/moonshot/mimo/stepfun/xfyun/openai/xai，数据镜像 example.toml）——DeepSeek/智谱 key 持有者不再被迫走 Custom 手打 base_url；家族通用流程自动接管 key 提示与 /models 探活 | 更新 TestWithBuiltinFamilies 新契约（用户项不重复+预设恰好一份）绿 |
| P1-B3 | recall 增 `query` 关键词模式（扫名字+正文、返回匹配行、帽 20）；portrait 预算 2000→4000；当前日期 boot 注入系统提示（先试 per-turn 注入，测试暴露其污染持久化历史与 #/@ 引用流——**设计回退为 boot 一次注入**，午夜陈旧性可忽略） | control/memory 包绿 |
| P1-D3 | CLI `/export --full`：补推理链（details 折叠）+工具调用（名称+截断参数）+工具结果（800 字符截断折叠块）；默认视图不变 | cli 包绿 |
| M5 前端（agent） | F9 静默失败清理（删除/恢复/重命名/预览失败经既有 onNotice toast 弹真实原因；运行中死点击给提示）；U-6 分支树渲染 preview 暗色行（fork_turn 字段不存在如实跳过）；U-7 历史面板自动预览 300ms 防抖 | tsc/build/npm test 全绿（locale-parity 过） |

**M1→M5 五个批次全部完成。** 剩余未做项（如实记录）：P1-D2 webhook+agent 间消息（独立子系统，建议单独排期）、E4 完整事件溯源（goal/todo 已覆盖，事件流化留 T3）、P1-A5/A6（reasoning 预检/headers/key 轮换）、P2 渲染性能三件（X5/X7，需先埋点测量）、其余 P2/U 小项随日常 PR。


**M4b 进度（2026-09-11 第四批收尾）**：

| 项 | 交付 | 验证 |
|---|---|---|
| P1-B2 session_search | 新工具（internal/agent/sessionsearch_tool.go，放 agent 包避免 builtin→agent import 环）：search 模式关键词跨会话检索（标题+全文+摘录，**排除当前会话**防自匹配噪音）；read 模式渲染指定会话为可读转录（按消息分页）；路径守卫（必须 session 目录内 .jsonl，防变成任意文件读取器）；boot 注入会话目录、controller 在全部 8 处 sessionPath 变更点回填当前会话排除；netdev profile 不注册（硬封印+防跨 profile 泄漏）。“按上次的方案”死路打通 | TestSessionSearchFindAndRead（找得到+读得回+自排除）+PathGuard 绿 |
| P1-D1 并行 fan-out | task 工具新增 `concurrent` 参数（描述明确适用条件：独立子任务、互不依赖）；parallelisable 门扩展：concurrent=true 的 task 调用并入只读并行批次（写型子代理本就跑隔离 worktree）；runParallelCap 支持按批次收窄并发——task 批次帽 3（maxConcurrentTasks），只读批次保持 8 | TestPartitionConcurrentTasks（三并发一并行批/非 concurrent 串行/与只读混合）绿 |
| P1-E4 todo 部分核实 | **已由现有"确定性重建"覆盖**：rebuildTodoState 从转录扫最新 todo_write 回执并重放 complete_step（:451 在会话加载即调用）——todo 持久化无需 sidecar，spec 该子项标记完成 | 既有测试覆盖 |

M4 剩余（转 M5/反馈驱动）：P1-D2 webhook+agent 间消息、E4 完整事件溯源（goal/todo 之外的事件流化）。


**M4 进度（2026-09-11 第四批）**：

| 项 | 交付 | 验证 |
|---|---|---|
| P1-E5 | Session.Save 补 rename 前 fsync（此前 tmp+rename 无 Sync，断电可留撕尾）；LoadSession 对末行残缺（ErrUnexpectedEOF）保全量抢救+warn 而非整文件拒载（present sidecar 本就容忍） | TestLoadSessionToleratesTornTail 绿 |
| P1-F1 | TurnDone 自然完成后若无用户排队但后台任务已完成 → 发通知"后台任务已完成——自动让模型查看结果"并以 [system] 消息自动唤醒（自限链：唤醒轮内无新完成即停；用户取消不触发） | control 包绿 |
| P1-B1 | internal/spill 包（~/.fairpeer/spills/，0600，保留最新 100 个）；cappedBuffer 超限后所有字节（含 full 早退分支）流式落盘，截断标记携带路径并指路 read_file；job/bash 两条路径接入，进程结束封口 | TestCappedBufferSpillsOverflow 绿（中段/后段字节可取回，head 不重复） |
| P1-E4 切片 | goal 三字段（text/status/turns）入 BranchMeta sidecar；SetGoal/stopGoal 即时同步（sidecar 缺失时创建）；Resume 恢复 running 态 goal+预算并提示"第 N/50 轮——发送'继续'接续"——**不自动点火**（C8 的自动续跑风险由"循环仅在用户下一条消息后才推进"保证），goal 崩溃失忆终结 | TestGoalPersistsAndRestores 绿 |

M4 剩余：P1-D1 并行 fan-out、P1-D2 webhook+agent 间消息、P1-E4 完整事件溯源（goal 之外的 plan/todo 持久化）、P1-B2 session_search。


**M3+MX-3 进度（2026-09-11 第三批）——两个"旗舰场景"运行时复测通过**：

| 项 | 交付 | 验证 |
|---|---|---|
| P1-A1 | anthropic 移植 streamWithReconnect（3 次预输出重放，readStream 改返回 emitted+err）；maxStreamRecoveries 可配（[agent] stream_recoveries，默认 1→3）；恢复时发"⚠ 连线中断，已保留部分输出，正在续写（N/M）"可见分隔 | **E2E 复测：双断流场景（第二轮曾整回合死亡+残文重复）现在两次可见恢复标记后输出最终答案**；agent/provider 包绿 |
| P1-A2 | isContextOverflowError 分类器（9 种网关拼写，带 wrapped 链）；流错误命中且本回合未试过 → 强制压缩（bypass 阈值）+ step-- 重试一次 | **E2E 复测：context_overflow → 压缩警告 → 自动重试 → 最终答案**（此前直接报错终结回合）；分类器 11 用例绿 |
| P1-A3 | provider.SetRetryPolicy 包级策略（MaxRetries/MaxBackoff/Mode）；[agent] retry_max_attempts/retry_backoff_max_sec/retry_mode；"always" 模式解除次数帽（无人值守长任务等网关回魂） | TestRetryPolicyConfigurable 绿；既有重试测试全绿 |
| P1-B5 | truncateAtMax：6 处 200k 字节截断全部 rune 安全（回退到字符边界），截断标记中文化并指路分页参数 | TestTruncateAtMaxRuneSafe（21 万字节 CJK 无乱码）绿 |
| B-2 | xlsx_read full 模式 honoring sheet：新增 readXLSXSheetFilter（大小写不归一匹配 + 未知表名报错列出可用表）；不带 sheet 保持全 sheet 行为 | TestXLSXReadFullModeHonorsSheet 三断言绿 |
| P1-F2/X11 后端 | Controller.GoalTurns() + tabSession 接口 + Meta.goalTurns/goalMaxTurns（remote 占位 0,0）；前端 Composer 目标 chip 显示"N/M"预算 | 双模块 build 过；tsc+前端 build 绿 |
| MX-3 | X9a Ctrl+F 冷区跳转修复（ensureTurnRendered+pendingJump 机制，QuestionJumpBar 同款故障一并修）；X9b 首开骨架屏（shimmer 行替代 Welcome 闪现）；X10a bash 输出 ANSI 清洗（新 lib/ansi.ts，9 用例）；X10b 21 组后端通知串本地化（每条对照 Go 源核实，含截断/空回答/goal/压缩/防环五类） | tsc/build/npm test 全绿（含 locale-parity） |

M3 剩余（转入 M4 批次）：P1-B1 spill、P1-B2 session_search、P1-F6 剩余文案 i18n。


**M2+MX-2 进度（2026-09-11 第二批）**：

| 项 | 交付 | 验证 |
|---|---|---|
| P0-7 | scheduler save 改 fileutil.AtomicWriteFile(0600)（原 0o644 明文无 fsync）；checkpoint RestoreCode 改原子写（与持久化同标）；ops 新增 UpdateRequest 全程持锁（load→mutate→transition→save），classify/plan 两处迁移，plan 的 Scoped 转移挪入锁内（修掉迁移引入的丢失 bug，测试抓出后修复）；opstep 台账写失败不再吞——slog.Warn + evidence 镜像一条 audit-only 失败回执 | ops/checkpoint/scheduler/netdev 包全绿 |
| P0-5 | newJobID/newCutoverID 改扫盘取号（镜像 proposal 模式，昨日文件不污染今日编号）；ListJobs/ListCutovers 惰性扫描：盘上 running 但无活 runner（后端重启孤儿）→ 新状态 `interrupted` + 中文备注；JobResume/CutoverContinue 接受 interrupted；CutoverAbort 接受 interrupted | p0_recovery_test.go 三测全绿（撞号/孤儿标记/活 runner 不误扫） |
| P1-E6 | JobResume 重置 ActiveMS（人工续跑=预算续期，命令数保留累计）；watchdog 三类 pause_note 中文化并带下一步指引 | 更新断言后 job 测试绿 |
| P1-E1 | CutoverRollback 全程持 cutoverMu+saveCutoverLocked（并发继续/回退不再互踩）；新增 CutoverSkipStep（hold 态、原因必填入审计链、步骤标 skipped、变更保留可后续回退）+ 桌面桥 NetDevCutoverSkip + CutoverView「跳过本步」按钮（原因 prompt，空拒绝）；门失败 hold_note 三分文案（变更已落盘/验证结果/可选动作含跳过） | netdev 构建绿；现有割接 E2E 绿 |
| P1-F12 | 抑制表条目带 LastAt，7 天 TTL 载入时惰性剪枝；阈值 2→3；新增 UnsuppressSource 人工解除（入审计）；旧格式（永久抑制）视为过期 | TestAlertQueueLifecycle 扩展（两轮不降级/三轮降级/解除恢复）绿 |
| MX-2 | X8a 停止痕迹：Go 侧 TurnDone 增加 Cancelled 字段打通 event/desktop wire，前端 turn_done+cancelled 显示"⏹ 已由用户停止"（不落 present sidecar，重载不重现——待 Go 持久化补）；X8b 审批角标：AppChrome 全局铃铛胶囊+侧栏最近圆点（tab 式页签已不存在，角标落在现行表面）；X6 办公产物：路径可点（编辑器/浏览器）+ 文件夹 reveal + doc/xlsx 预览走附件灯箱，doc_read 等补注册；X9 割接 hold 态终止按钮 + 深链携带 id 直达 run | tsc+build 绿（agent 顺带修复 3 处既有 CSS 构建闸破坏；期间遇仓库主人并发搬运 calendar 目录的瞬时编译错，自愈） |

注：X8a 停止标记的持久化（present sidecar）与 skip 按钮的防误触（现用原生 prompt）列为 M5 打磨项。

**M2 验收状态**：netdev 全包单跑绿（287s）；desktop Go + 前端 build 绿；根模块全量 72 包中 69 绿，3 个负载敏感抖动（TestSaveCutoverAtomicConcurrentReads=Windows 原子替换窗口的共享竞争、TestConsoleWaitStable×2=Chrome 启动争用）单跑均复绿、且都在本批未触碰的代码上——登记为既有抖动 backlog（与 P0-4 的 boot 偶发同类），不阻断 M2。

**M2 验收中自抓并修复的两处**（全量测试的价值实证）：① CutoverRollback 补锁后与 cutoverFinishReport 自死锁（FinishReport 自取 cutoverMu）——TestCutoverRollbackAtDecisionPoint 卡死暴露，改为出报告前显式解锁；② 大屏管线测试种子的"盘上 running 无活 runner"被孤儿扫描按新语义转 interrupted——测试改为登记活 runner 入口，与真实后端行为一致。


**M1 全部 7 项完成，测试全绿**（根模块全量 + desktop 模块 vet+test 连续通过；含并行开发中途态的两次全绿——"连续 5 次"完整验收待工作区稳定后补跑）：

| 项 | 交付 | 回归测试 |
|---|---|---|
| P0-1 | confine.go/workspace.go 补 applyPatch/moveFile 注册；**结构性测试额外抓到并修复 image_generate 桌面路径漏绑（第三个漏网 writer）** | confine_patch_test.go：功能 2 个 + 结构性 1 个 |
| P1-C2 | cargo check/doc 移出只读白名单；npm audit fix/fix-*/fix-force 判为非只读 | TestDependencyExecutorsNotReadOnly（12 用例） |
| P0-4 | RAG 红灯已修（HOME 隔离使测试封闭）；补正向覆盖 TestOfficeDocImportSearchable（真 docx 导入可搜索） | rag 包全绿 |
| P1-F7 | xlsx append 覆盖改为显式拒绝（含 schema 描述）；docx 空 sections 拒绝并指出 content 误用；checkCellValue 接入两条 xlsx 写路径；rejectLockedTarget 锁探测覆盖 docx 全写/append/xlsx 双路径 | document_trust_test.go 3 个复现脚本 |
| P1-E3⑤ | Controller.ApproveIfPending；/approve、/deny 对无效 ID 诚实回复；gateway.Push 返回 ErrPlatformOffline（桌面桥映射为 ErrIMOffline 保调度语义） | bot/control/desktop 全绿 |
| P1-F10② | Config.ConfigWarnings + mergeFile Undecoded 检测；启动横幅黄字警告（已实测：`default-model` 拼写错误当场点名）；doctor 汇入 | config/cli/doctor 全绿 |
| P0-3 | 报错移出正文（页级 unavailability 标记 + 结构化 [OCR warnings] 尾块，Go 侧解析）；pip 安装 stderr 可见 + 记入 warnings；docconv 查找顺序托管副本优先（旧 exe 旁副本不再遮蔽）；同步修复进内嵌副本 internal/assets/scripts/ | 输出契约运行时验证；脚本语法校验 |

**MX-1 完成 X1/X2/X4**（X3 感知延迟三招转入 MX-2）：X1 代码块/终端主题跟随应用主题（含 light 下 Catppuccin Latte）；X2 硬编码中文清零（20 个新 locale 键，cowork/netdev 模块级中文留待本地化专项）；X4 工具卡折叠行显示可点击路径、+N −N 双色计数、错误首行替换。tsc+build+locale-parity 等前端测试通过；顺带修复 3 处 HEAD 上已存在的 CSS 构建闸破坏。

注：OCR 的"扫描 PDF 实测"验收项需 paddlepaddle 环境，本轮以单元级契约验证代替，装好环境后补测。


- **v1.4**：确立"交互可见的体验项为一等优先"的取舍原则（5.0 节）；新增 T0~T3 四档分类与体验升级包 X1~X12（5.0.1 节，按"用户会看到什么"排序）；里程碑插入 MX-1/MX-2/MX-3 并行体验轨道；明确缓做项（事件溯源/插件生态/热更新/虚拟化等）与保留取舍（reasoning 不回传只加开关）；M5 改为反馈驱动的 backlog。

- **v1.3**：完全自包含重写——恢复 v1.2 中悬空引用的全部详细内容（P1-A/B/C/D/E 六族、P2-1~8 前端、B-2~B-17 与 B-18~B-32 后端表）；4 个散落条目归位（200k 截断→P1-B5、watchdog→P1-E6、审批跨 tab→P1-F13、SDK→P1-C5）；U-2/U-5/U-10/U-14/U-21 标注并入项；里程碑对齐新编号。
- v1.2：第三轮 UX 确认（5 席位+10 项复核）；P1-F1~F12；U-1~U-21；里程碑重排；5 项体验优势入守住清单；7 项降级修正。
- v1.1：第二轮场景化（48 场景+故障注入对拍）；新增 2 个安全 P0 与 13 个 P1；测试/CI 规模量化。
- v1.0：第一轮模块化对比；9 个 P0、7 个 P1、前端 8 项+后端 17 项。
- 三轮累计：11 个方向源码排查、48+ 场景走读、21 项运行时实测、约 200 个对比项、约 90 条编号整改项。

---

## 8. 遗漏清单（2026-09-12 五遍复排查，70 项）

> 合并五遍 105 条原始发现，去重后 70 项。分类：A=必须修（数据丢失/功能坏死）；B=应修（真实场景）；C=缓做；D=不做。★=已逐行核验。**A 组前 5 项（NEW-01/03/04/05/09）是当前最高优先级。**

### A 组 · 必须修（10 项）

| ID | 标题 | 证据 |
|---|---|---|
| NEW-01 | 会话 .sig 三重缺陷：HMAC 校验先于撕尾抢救（好会话被误杀）+ sig 写非原子吞错 + 盘满 O_TRUNC | ★internal/agent/save.go:171-176,229-234 |
| NEW-02 | SSE 消费端 1MiB 行上限 → 大工具调用单行超限，回合不可恢复 | openai.go:444,535 + agent.go:1468 |
| NEW-03 | task worktree 隔离死代码：wt 从未传给子代理 → 写主工作区却谎报"no file changes"；concurrent 并行写无保护 | ★internal/agent/task.go:247,284-316,475-489；worktree.go:75 |
| NEW-04 | bot 网关办公坏死：默认 dev profile + workspace=CWD → doc_write 被 confine 拒绝（P0-1 回归） | ★internal/bot/gateway.go:820-827；boot.go:2397,1941 |
| NEW-05 | cowork 文档写入无界：DocumentTools 零值实例在 ConfineWriters 之后注册覆盖（last-wins）→ P0-1 在 cowork 面死代码 | ★boot.go:879-882 vs :643,:2195-2201；tool.go:207-217 |
| NEW-06 | goal 终止后复活：循环内终态不落 sidecar + syncModeToMeta 无锁 RMW + 无锁读 sessionPath | controller.go goal 同步/推进路径 |
| NEW-07 | 强制压缩把 10MB 粘贴原文送摘要器 → 必败回退机械折叠，内容静默丢失 | compact.go forced 分支 |
| NEW-08 | 盘满系统性静默失败：autosave/checkpoint/spill/scheduler history 写失败全吞 | 各写点（Sweep4） |
| NEW-09 | checkpoint safePath 不解析符号链接 → restore 可逃逸工作区写任意路径 | ★checkpoint.go:621-628 |
| NEW-10 | scheduler Run 在 active tab 双跑：无 running 守卫 + 绕过 checkpoint | scheduler Run / 桌面桥 |

### B 组 · 应修（48 项）

NEW-11 anthropic 重连双缺口（clean-FIN 无 emitted 标记→恢复对干净断连失效；ToolCallStart 未标 emitted）；NEW-12 [agent] 四新键被配置 SaveTo 抹除（render.go 不渲染+example.toml 缺失+retry_mode 无校验）；NEW-13 review 命令绕过 boot.Build（无重试策略/写边界）；NEW-14 `npm audit --fix` 绕过白名单；NEW-15 白名单挂起/交互漏判（docker stats、kubectl -w、find -ok、env -S、rg --pre）；NEW-16 TurnDone 唤醒 TOCTOU 丢排队消息；NEW-17 spill 五缺口（bash 前台不封口/每调用建文件+Prune/字节无上限/标记悬挂/跨会话撞名）；NEW-18 session_search 封印破损（init 注册漏进 netdev schema+跨 profile 读+包级变量竞态）；NEW-19 serve/remotehost 会话列表只认平铺布局；NEW-20 dream agent 共享主会话+nil 门+静默失败；NEW-21 LSP 三缺口（无退避/ensureSynced 竞态/无 didClose）；NEW-22 云厂商预设缺 ContextWindow（压缩禁用）；NEW-23 goal×审批僵局；NEW-24 IMAP 143 明文登录；NEW-25 邮件附件三缺口；NEW-26 compose PASS 先于 FAIL 求值；NEW-27 job/cutover 双竞态（Start 窗口+runner 陈旧保存覆盖 Abort）；NEW-28 command-cap resume 死端；NEW-29 CutoverRollback 漏 landed-but-failed/skipped 步骤+landed 文案死代码；NEW-30 OCR 分批修复未达部署副本+桌面 finder 旧序；NEW-31 OCR warnings 被 RAG 索引+大 PDF 截丢；NEW-32 写边界结构测试对 DocumentTools 盲区；NEW-33 rewind 不重建 todo（幽灵 todo 误判 evidence 门）；NEW-34 运行中删除会话被静默重建；NEW-35 AutoSearch 三缺口（无总超时/查询无界/plan 模式照跑）；NEW-36 QQ 网关无去重无恢复；NEW-37 审批回环两处（serve 钉死断连/桌面 waiter 泄漏）；NEW-38 remotehost 鉴权行无上限；NEW-39 Windows PowerShell 通知参数损坏；NEW-40 linkpeersignal 两处（WS 无读上限 OOM/重连注销错删）；NEW-41 trustdomain 无锁并发+pair 活指针；NEW-42 expert_runs 缺 runID 校验；NEW-43 calendar List 按首现过滤（周期事件消失）；NEW-44 RRULE UNTIL Z 后缀+PT1H30M 解析错；NEW-45 RAG 缓存两处（embedding 键含 query 永不命中/semantic 永不淘汰）；NEW-46 RAG 删除不剪枝（导入不清结构化层/删文件不删索引）；NEW-47 RAG 一致性两处（半删无回滚/MergeEntities 撞 UNIQUE）；NEW-48 RAG 双进程 busy+.meta 互踩；NEW-49 RAG 导入无流式（500MB 进内存+17 万次 LLM）；NEW-50 plugin.Host.Add 双启动竞态；NEW-51 mobilebridge 文件投递三缺口；NEW-52 browseruse 孤儿 agent 双驱动；NEW-53 配额耗尽误报限流+scheduler 无限重试；NEW-54 「⏹ 已停止」仅桌面渲染（CLI/serve 缺）；NEW-55 control 两处数据竞争（lastDone 指针/autosaveWG）；NEW-56 saveHistory 0644。

### C 组 · 缓做（11 项）

NEW-59 抑制前端阈值未跟（≥2 示"已降级"误导）；NEW-60 UnsuppressSource 无 UI 出口；NEW-61（=U-19）serve /sessions 全量解码+GET 触发 LLM 标题；NEW-62 complete_step O(n²) 重扫；NEW-63 唤醒链式 LLM 成本；NEW-64 jobs map 永不清理+每 Notice 全扫；NEW-65 搜索三缺口（gitignore 重编译/glob 无视/dist 漏）；NEW-66 实体嵌入永不失效；NEW-67 tokenGuard 非常量时间；NEW-68 删会话留 sidecar；NEW-69 xfyun Vision=false。

### D 组 · 不做（1 项）

NEW-70 并行批次内权限拒绝不停止兄弟任务——concurrent 语义即"独立互不依赖"，保持现状并文档化。

### 8.1 对抗性验证终判（2026-09-12，4 轮验证 + 1 轮新鲜度复查）

> 方法：对 §8 全部条目做对抗性验证——尝试证伪而非确认；能复现的写临时测试跑完即删；同时做反 dsh-envy 审查（剔除"dsh 有所以算问题"的伪需求）。

**总裁决：可列 72 项（§8 原列表 70 项 + 验证期新增 2 项：NEW-71 max_runs_per_day 死旋钮、NEW-72 SwitchBranch/setModel TOCTOU）。证实 60 项、部分证实（修正点）9 项、证伪/已修 2 项（NEW-29 回滚过滤器已在工作区修复未提交；NEW-64 前端症状证伪）、未复核 1 项无。** 严重度修正 6 项（NEW-02 降级低概率高后果、NEW-04 改述为能力缺失、NEW-06 降 P2、NEW-14 升高、NEW-15 拆分、NEW-38 分场景）。

A 组 10 项新鲜度复查（当前工作树）：**10/10 全部仍存在**。B 组 13 项升级项复查：**13/13 全部仍存在**。

**反 dsh-envy 审查终判**（"dsh 有所以要做"的剔除结果）：
- **不需要**：全编排状态事件溯源（goal/plan/todo 的 sidecar 切片已覆盖用户可感知面）；入站 webhook（目标用户已被 IM bot+schedule 覆盖，白添公网入站面）；A2A 消息原语（无单用户流程被阻塞）；持久 PTY（one-shot+链式+后台任务已覆盖真实工作流）；Windows Restricted Token 沙箱全量（把 NEW-08 类静默失败与危险命令警告做好即可，见反审查第 g 条）。
- **需要现在做**：NEW-22 云厂商预设补 ContextWindow（与 dsh 无关，纯自己的 bug——compact 对 0 直接禁用，wizard 主路径厂商全部命中）。
- **等用户反馈**：MCP 自动重连（`/mcp connect <name>` 一条命令已可恢复，个人 server 崩溃是周级事件；自动重连引入崩溃循环风险）。

**证伪/修正明细**（防止后续误修）：
- NEW-29：CutoverRollback 过滤器已在当前工作区修复（switch 含 failed/skipped，未提交）——尽快提交即可；残留 landed 文案死代码（cutover.go ~782-784 只认 Approved/Done）随手清。
- NEW-64：前端 jobs 状态是整体替换非累积（useController.ts:971），"前端无限增长"证伪；后端 jobs map 每 session 有界、扫描毫秒级——关闭。
- NEW-15：find -ok 被 containsShellSyntax 驳倒（`;` 变体全拦截）；但 `$';'` ANSI-C 混淆是次级缺口。
- NEW-04：bot 会话（dev profile）根本没有 doc_write——真实缺陷改述为"bot 办公能力整体缺失+工作区=CWD"，非安全回归。
- NEW-06：正常退出（桌面关 tab/退出、CLI 退出）都调 Snapshot 自愈；窗口仅"终态翻转后到任一 snapshot 前崩溃"。定 P2。
- NEW-02：主流网关按 token 分片 SSE，合并单行 >1MiB 属边缘配置——低概率、确定性且回合。
- NEW-11 附注：这是 fairpeer 内部 parity 缺口（openai 有守卫 anthropic 没有），与 dsh 无关。
- NEW-41：pair store 在 linkpeersignal 非 trustdomain；trustdomain node.go 有 "single-goroutine by design" 注释——是 nettrans 违反契约，非 Node 缺锁。
- NEW-65(a)：重编译仅发生在"自带 .gitignore 的目录"，常态 500-pattern 单根 .gitignore 全程 1 次编译 ≈19ms——原 500 万次说法不成立。
- NEW-64 附注：NEW-62 complete_step 重扫实测影响可忽略（per-turn ledger miss 才走全扫、纯内存、数十次/会话）——关闭。

**值得现在修的短选（≤2 天，按天分组）**：
- 第 1 天·安全/边界：NEW-09 symlink 解析（0.5h）、NEW-14+15 白名单（1h）、NEW-05(+32) cowork 注册顺序+测试补盲（1.5h）、NEW-10 Run 加守卫（1h）、NEW-01 sig 原子写+抢救顺序（1h）、NEW-12 四键防抹除（1h）、NEW-39 通知参数引号包裹（0.5h）、NEW-61 接 .meta 缓存（0.75h）
- 第 2 天·数据完整性/功能：NEW-03 worktree 传递+并发保护（2.5h）、NEW-25 邮件附件（1.5h）、NEW-19 会话列表嵌套布局（2h）、NEW-08 盘满有声化（2.5h）、NEW-22 预设补 ContextWindow（1h）
- 顺手项：NEW-59 一行修；NEW-29 提交已修代码+清 landed 死文案；NEW-07 压缩补截断注记
- 明确顺延：NEW-45/66（RAG 缓存）、NEW-17（spill 五缺口）、NEW-18（session_search 封印）——中危但有独立排期价值

### 8.2 修复落地记录（2026-09-12）

| 项 | 交付 | 测试 |
|---|---|---|
| NEW-14/15 | npm `--fix` 旗标、env -S/--split-string、rg --pre、docker stats 无 --no-stream、kubectl/docker logs -f、kubectl get -w 全部移出只读白名单 | TestWhitelistFlagAndHangEscapes 13 用例 |
| NEW-09 | safePath 对已存在前缀 EvalSymlinks 解析，链接隧道 fail-closed | TestSafePathResolvesSymlinkTunnel（真实 symlink 写穿被拒） |
| NEW-01 | .sig 改 AtomicWriteFile；错配且 jsonl 更新于 .sig → 判崩溃窗口陈旧签名放行解码+撕尾抢救（真篡改仍硬失败） | TestLoadSessionToleratesStaleSig |
| NEW-05(+32) | DocumentTools(roots) 构造期绑定四写手+mindmap_create，boot 传 writeRoots；结构测试显式覆盖运行时组装写手 | TestAllRootsBearingWritersAreConfined 扩展 |
| NEW-10 | Controller.Run 加 running 守卫（定时任务不再与用户 turn 并发/绕过 checkpoint） | control 包绿 |
| NEW-12 | render.go 渲染四新键+example.toml 文档化+retry_mode 大小写不敏感 | TestRenderTOMLKeepsResilienceKeys 绿 |
| NEW-39 | PowerShell 通知改 -EncodedCommand+单引号 PS 字面量（$args 恒空与注入面一并消除） | TestPsQuote |
| NEW-61 | serve /sessions 优先读 .meta 缓存；文件夹布局会话进入列表（NEW-19 serve 侧同修） | serve 包绿 |
| NEW-19(remotehost) | session/list 认 <dir>/<id>/<id>.jsonl 布局 | 构建绿 |
| NEW-25 | 附件 25MiB 上限+重名递增后缀不覆盖 | 构建绿 |
| NEW-08 | 自动保存失败发一次性"⚠ 会话自动保存失败"Notice | control 包绿 |
| NEW-29 残项 | landed 文案纳入 Failed 状态提案步（门失败也提示变更已落盘）；回滚过滤器确认仓库主人已修 | netdev 构建（与并行修复合流） |
| NEW-03 | 双重谎言消除：如实告知写主工作区（checkpoint 可回滚）；并发 fan-out 暂停（运行时证实丢失更新，隔离未接线前串行） | TestPartitionConcurrentTasks 更新为串行契约 |
| NEW-07 | 机械折叠摘要明确"超大原文已归档、不在上下文中，勿猜测" | compact 包绿 |
| NEW-22 补充 | xfyun 预设 Vision 改 true（三源矛盾收敛） | TestWithBuiltinFamilies 绿 |
| 合并冲突 | rag/officedoc.go warnings 字段与内联附加两路交汇——统一为"struct 字段传递+runOCRBatches 末尾单次附加" | rag 包绿 |

仍未做（维持 §8.1 分类，按模块分批）：NEW-16/17/18/20/21/33/34/35/36/37/40/41/42/43/44/46~58 中未在上方覆盖者、C 组缓做维持项。
