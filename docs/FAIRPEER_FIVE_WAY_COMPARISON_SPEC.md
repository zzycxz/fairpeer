# fairpeer 五项目对比与改进规格说明书

> 日期：2026-09-08
> 对比对象：deepseek-harness、codex（OpenAI）、openworker、pi（badlogic monorepo）、MiMo-Code（Xiaomi）对 fairpeer
> 性质：**纯规格文档。本 spec 不修改 fairpeer 任何代码**；所有问题均经代码级验证，其中 P1 附独立复现。
> 阅读方法：每个对比项目完整通读源码 4 遍（第 1 遍全量结构+子系统扫描；第 2 遍子系统深读；第 3 遍交叉验证 file:line 证据；第 4 遍 spec 成稿后逐条回读核对）。fairpeer 由作者本精读核心包（agent/tool/provider/sandbox/permission/checkpoint/boot/control/serve/edit 路径）并以同样的遍数复核。

---

## 目录

1. [六个项目概览](#1-六个项目概览)
2. [fairpeer 架构现状摘要](#2-fairpeer-架构现状摘要)
3. [五个项目相对 fairpeer 的优势矩阵](#3-五个项目相对-fairpeer-的优势矩阵)
4. [fairpeer 已验证问题清单](#4-fairpeer-已验证问题清单)
5. [改进规格（Spec-1 … Spec-12）](#5-改进规格)
6. [检测记录（3 遍）](#6-检测记录3-遍)

---

## 1. 六个项目概览

| 项目 | 语言/形态 | 规模（实测） | 定位 |
|---|---|---|---|
| **fairpeer** | Go 单二进制 + Wails 桌面端 | 非测试 Go ≈ 20.4 万行 + 测试 ≈ 8.8 万行 | 多厂商（26 模板）AI 编码助手 + Word/Excel/PPT 办公自动化 + netdev 运维套件 + RAG/记忆/Dream |
| **deepseek-harness (dsh)** | TS pnpm monorepo（vendor Cordis 框架）+ Python SDK | 源码 ≈ 32.8 万行 / 测试 ≈ 30.0 万行（比 0.91） | DeepSeek 官方 harness，"everything-is-a-plugin"（profile/bundle/patch 运行时组合） |
| **codex** | Rust workspace（约 102 crate） | Rust ≈ 144 万行；`#[test]` 8,043 + `#[tokio::test]` 5,995 | OpenAI Codex CLI：TUI/exec/app-server/MCP-server 多前端 + 三平台沙箱 + rollout 持久化 |
| **openworker** | Python(FastAPI) + Tauri(Rust) 壳 + Rust STT | 后端测试 93 文件/23,670 行；GUI 61 个 hermetic e2e | 本地优先桌面 AI 协作者：Inbox/durable-resume/调度/self-wake/Slack-Telegram 网关 |
| **pi** | TS monorepo（10 包） | 源码 ≈ 15.8 万行 / 测试 ≈ 14.4 万行 | 内核纪律流：树状会话、compaction 切点、39 provider、自研 TUI、protocol/lease RPC |
| **MiMo-Code** | Bun workspace（opencode 深度 fork） | opencode 包测试 454 文件/≈4,578 case | checkpoint 优先的上下文管理、Effect 架构、models.dev 目录、ACP/Zed/VS Code 多前端 |

---

## 2. fairpeer 架构现状摘要

（供下文对照；均为 fairpeer 当前源码实测，file:line 以本仓库为基准）

- **Agent 主循环** `internal/agent/agent.go:801-995`：stream → 截断拦截（`truncationInterceptor`，agent.go:926）→ 工具批执行 → interceptor 链（readiness/empty-final，`internal/agent/interceptors.go`）→ maybeCompact。具备 pause/resume（agent.go:576-662）、steer/followUp 双队列（agent.go:492-568）、三级循环防护（stormBreaker `agent.go:1705`、repeat-success `agent.go:1988`、opGate `internal/agent/op_gate.go`）。
- **压缩** `internal/agent/compact.go`：LLM 摘要 + 增量更新（`<previous-summary>`，compact.go:83-95）+ 锚定前缀（compact.go:479-491）+ 尾部 token 预算（compact.go:529-552）+ SoftTrim/Prune 两道免费修剪（`internal/agent/prune.go`）+ 归档 + 机械回退（compact.go:701-707）。文件台账 `ExtractFileOps` 程序化提取。
- **会话**：JSONL 全量重写 + tmp/rename 原子替换 + HMAC `.sig`（`internal/agent/save.go:131-175`）+ `.meta` sidecar（`internal/agent/branch.go:18-70`）+ 每会话目录布局（save.go:505-512）。分支是 sidecar 元数据（ParentID/ForkTurn），**历史本体是线性数组**（`internal/agent/session.go:16-59`）。
- **Provider**：仅 2 种 wire 协议（openai 兼容 + anthropic，`internal/provider/provider.go:646-681` 注册表）；重试完整（指数退避+抖动+Retry-After+网关瞬态 400 白名单+瞬态 401 重试，`internal/provider/retry.go:173-236`）；RPM 预算（`internal/provider/budget.go`）。
- **工具**：约 85 个内置（`internal/tool/builtin/`，约 4.5 万行），写入边界默认开启且 symlink 感知（`internal/tool/builtin/confine.go:143-196`），读边界可选（confine.go:87-103）；webfetch 有 SSRF 守卫（webfetch.go:52-206 + `internal/netclient/netclient.go:291+`）；前台 bash 默认 120s 超时（`internal/config/config.go:1347`）、输出封顶（bash.go:182 `jobs.NewCappedBuffer`）、进程组收割（bash.go:197）。
- **权限/沙箱**：纯策略 Policy + Gate + HardDeny（`internal/permission/permission.go:109+`）；**OS 沙箱仅 macOS Seatbelt**（`internal/sandbox/sandbox.go:7-10` 注释自述，其它平台回退不限制）。
- **Checkpoint**：git-free 每 turn 快照 + pre/post hash + 50 个保留上限（`internal/checkpoint/checkpoint.go:6-10, 82-105`）；只覆盖可 Preview 的写入工具，bash 不跟踪（checkpoint.go:6-10 注释）。
- **扩展面**：MCP 客户端（stdio/SSE/HTTP + OAuth，`internal/plugin/`）、LSP 客户端（`internal/lsp/`）、12 种 shell hook 事件（`internal/hook/hook.go:62-69`）、ACP server（`internal/acp/`）、HTTP/SSE serve（`internal/serve/`）、IM bot（`internal/bot/`）、expert teams（`internal/experts/`）、两层记忆+Dream（`internal/memory/`、`internal/agent/dream.go`）。
- **子代理**：`task` 工具支持后台化（jobs）、模型/effort 覆盖、转写 `continue_from`/`fork_from`（task.go:166-167）；写者子代理在独立 git worktree 运行、diff 回传主工作区待审批（task.go:240-247, 306-315）——多子代理并行写不互相踩踏。
- **桌面端**：Wails，每标签页一个 `boot.Build` 控制器（`desktop/tabs.go:1334` 为标签页 Build 的主路径；`desktop/app.go:5266, 5486, 5647` 为切模型/重建等路径；`desktop/tabs.go:38-45` 为标签页数据结构），支持并发多项目。

---

## 3. 五个项目相对 fairpeer 的优势矩阵

> 说明：每条注明来源项目与对方代码位置（在其仓库内）、fairpeer 现状位置、差距本质、可借鉴设计点、建议优先级（P0 立即 / P1 近期 / P2 中期 / P3 观察）。

### 3.1 会话持久化 / 分支 / 崩溃恢复

| # | 机制 | 来源与位置 | fairpeer 现状 | 差距与借鉴点 | 优先级 |
|---|---|---|---|---|---|
| A1 | append-only 树状会话（entry+parentId+可移动 leaf 指针） | pi：`packages/coding-agent/src/core/session-manager.ts:1358-1363`（branch=移动 leaf）、`:334-360`（路径重建）；MiMo：`session/session.ts:656-688`（fork 按 messageID 克隆） | 线性 `[]Message`（session.go:18），分支仅为 sidecar 元数据（branch.go:19-25） | pi 四行数据结构换来：重问/多分支共存、分支即状态（model/thinking 是树节点，session-manager.ts:362-377）；fairpeer 回退只能整段截断 | P1 |
| A2 | fork-by-reference（分支不复制历史） | codex：`codex-rs/core/src/session/mod.rs:386-392`（`ForkPersistence::Referenced{history_base,…}`）+ `rollout/src/ordinal.rs` 全序 | fork 复制消息（controller `Fork(turn)`） | 大会话 fork 成本 O(1) vs O(n) | P2 |
| A3 | 撕裂尾自动修复 + seq 连续性校验 | pi：`harness/session/jsonl/storage.ts:84-92`（torn-tail repair）、`state.ts:97-180` | LoadSession 遇损坏行直接报错（save.go:248-256）；NormalizeSession 只修工具对（provider/normalize.go） | 崩溃后半行 JSON 应可自动截断修复而非拒载 | P1 |
| A4 | durable resume（审批挂起崩溃后重建续跑） | openworker：`engine.py:270-312`（未答 tool_calls 重放）+ `inbox.py:131-135`（(session_id,tool_call_id) 幂等键） | 回合在等审批时进程死亡=回合丢失；checkpoint 只回滚文件不恢复回合 | "已答跳过、未答重放"协议 + 持久审批实体 | P1 |
| A5 | 不可变 generation 版本化持久层 | dsh：`session-persistence-jsonl/generation.ts:70-89`（独占发布、拒绝未来版本、迁移链） | 单文件全量重写，无格式版本 | 跨版本会话演进不破坏旧档 | P3 |
| A6 | 反向 JSONL 扫描器（O(尾部) resume） | codex：`rollout/src/reverse_jsonl_scanner.rs:20-72` | LoadSession 全文件解码 | 大会话秒开 | P2 |
| A7 | SQLite 可重建索引（JSONL 为事实源）+ backfill lease | codex：`rollout/src/state_db.rs:45,80,519` | `.meta` 缓存缺失即全量解码（save.go:366-370） | 列表页 O(n) 解码风险（见 P8） | P2 |

### 3.2 上下文管理 / 压缩

| # | 机制 | 来源与位置 | fairpeer 现状 | 差距与借鉴点 | 优先级 |
|---|---|---|---|---|---|
| B1 | 合法切点枚举（禁切 tool 配对 + split-turn 前缀单独摘要） | pi：`compaction.ts:312-344`（findValidCutPoints）、`:689-702` | tailStart 只按"消息起点非 tool 角色"对齐（compact.go:566-584） | pi 的切点保证"永不切开 assistant tool_calls/result 对"是结构性的；fairpeer 靠回退对齐，边界情形（连续 tool 消息跨 turn）更脆弱 | P1 |
| B2 | usage 缺失时的 token 估算回退触发 | pi：`estimateContextTokens`（chars/4 锚定+尾部估算，compaction.ts:216-244）；openworker：`compaction.py:48-57`（真值优先/chars-4 兜底） | `u.PromptTokens == 0` 直接返回（compact.go:101）→ 网关不回 usage 就永不自动压缩（见 P4） | 已有 `estimateMessagesTokens`（compact.go:175-191）却未用于触发 | P0 |
| B3 | overflow 400 识别并路由进压缩 | openworker：`is_context_overflow`（compaction.py:545-561）；pi：`ai/src/utils/overflow.ts`（20+ 厂商文案模式库） | 上下文溢出错误当普通错误处理 | 把"窗口超限"从致命错误变为压缩触发器 | P1 |
| B4 | checkpoint 重建优先、compaction 只作兜底 | MiMo：`session/checkpoint.ts`（后台写手持续物化分层 md）、`prompt.ts:3455-3467`（"THE single compaction fallback"） | 有信息价值的压缩=一次性有损摘要 | "持续物化+按需恢复"信息保真更高 | P3 |
| B5 | microcompact（按工具可再生性白名单替换占位符） | MiMo：`checkpoint.ts:184-192` | PruneStaleToolResults 对所有 ≥1KB tool 结果一视同仁（prune.go:36-80） | read/bash 可再生可激进替换；actor/task 等不可再生须保留 | P2 |
| B6 | 压缩摘要请求的前缀缓存对齐 / 不污染缓存 | dsh：`compaction-basic/summarizer.ts:29-34`（压缩指令放重放对话末尾成真前缀）；pi：摘要请求强制 `cacheRetention:"none"`+随机 sessionId（compaction.ts:110-115） | summarize 用独立 system prompt（compact.go:639-658），必然 miss | 摘要调用本身的成本可大幅摊薄 | P2 |
| B7 | Spill：超大工具结果全量落盘+模型见定位器 | dsh：`spill-policy/src/index.ts:1-40`、`spill-local/store.ts:47-70` | truncateToolOutput 头尾各 16KB 丢弃中段（agent.go:2149-2176），中段不可找回 | dsh 保留全文可检索；fairpeer 中段永久丢失（只能重跑工具） | P2 |

### 3.3 工具系统 / 并行执行

| # | 机制 | 来源与位置 | fairpeer 现状 | 差距与借鉴点 | 优先级 |
|---|---|---|---|---|---|
| C1 | 按风险级数据驱动并行 | openworker：`engine.py:664-671`（risk_level 声明，读并行/写串行）；dsh：`isConcurrencySafe` fail-closed（tools/index.ts:1267-1276） | partitionToolCalls 按 ReadOnly+preview 路径分批（agent.go:1580-1663） | fairpeer 机制已不错，但"数据声明"可覆盖插件工具；dsh 的 fail-closed（异常即 exclusive）值得吸收 | P2 |

| C2 | 有界并行调度器（模型序提交、abort 补合成结果） | dsh：`tool-calls.ts:60-247`（committed 只沿连续模型序推进、`ABORTED_BEFORE_DISPATCH` 合成错误） | executeBatch 并行执行但事件按调用序发射（agent.go:1536-1544） | dsh 保证 replay 有效性与"调度器失败不伪造结果" | P3 |
| C3 | PTC 程序化工具调用（一段代码批量调工具） | dsh：`ptc.ts:32-100`、`tools/index.ts:986-991`；codex：code-mode V8（`code-mode-runtime/v8_init.rs:20`） | 每工具一次 JSON 往返 | 多工具组合任务 token 成本数量级下降 | P3 |
| C4 | MCP 工具按需检索（BM25 目录 + load） | MiMo：`mcp-tool-search.ts:11-14`；codex：tool_search+deferred namespaces（`tools/router.rs:113`） | 全部插件工具进 Schemas（agent.go:856） | MCP 服务器多时系统提示词爆炸 | P2 |
| C5 | 持久 shell（跨调用 cd/env）+ marker 协议 | openworker：`tools/shell.py:354-366`（trailer 输出 exit code+cwd）、Windows 整树杀+respawn 自愈（219-298） | 每次 bash 新进程，cwd 不持久 | 状态类工作流（venv、cd 链）体验差距 | P2 |
| C6 | bash 输出 token 高效管线（progress 折叠+ANSI 剥离+机密 redact+never-worse 回退） | MiMo：`bash_token_efficient_pipeline.ts:1-30` | CappedBuffer 原样透传 | 机密 redact 前置是安全收益 | P1 |
| C7 | edit 默认精确匹配、fuzzy 只做纠错提示 | MiMo：`edit.ts:662-664`（fuzzy 链需 env 显式开启）、`:733-745`（"最接近匹配"只提示绝不应用）；pi：`edit-diff.ts:132-173`（归一化命中+未命中行原字节保护） | 5 级 fuzzy 默认开启，Level 4 块锚允许区域膨胀（edit_fuzzy.go:327-398）→ **已复现静默删行（见 P1）** | 模糊匹配应"降级为提示器"或加行块保护 | **P0** |

### 3.4 权限 / 沙箱 / 安全

| # | 机制 | 来源与位置 | fairpeer 现状 | 差距与借鉴点 | 优先级 |
|---|---|---|---|---|---|
| D1 | Linux seccomp+bwrap 双层沙箱 | codex：`linux-sandbox/src/landlock.rs:35-66`、`linux_run_main.rs:157` | Linux 无 OS 沙箱（sandbox.go:9-11） | fairpeer 用户主体在 Windows/Linux（见 README 安装脚本），恰好是无沙箱平台 | **P1** |
| D2 | Windows 受限 token+deny ACL+WFP+独立桌面 | codex：`windows-sandbox-rs/src/{token.rs:171, acl.rs, wfp.rs, desktop.rs}` | Windows 无 OS 沙箱 | 同上 | **P1** |
| D3 | Starlark 可执行策略 DSL + overlay + 自校验示例 | codex：`execpolicy/src/parser.rs:34,43`、`policy.rs:178` | permission 规则=字符串列表（permission.go:57-105） | 声明式策略文件+自带正反例 | P2 |
| D4 | forced-ask 权限（通配 allow 不可预授权不可逆动作） | MiMo：`permission/index.ts:190-195, 256-266` | HardDeny 层存在（permission.go:116-121）但无"强制 ask"档 | `bash(rm *)` 这类应强制人工确认，任何 auto/yolo 模式不能跳过 | P1 |
| D5 | 审批缓存键 + Guardian LLM 复审 + 拒绝熔断 | codex：`tools/approvals.rs:142,196,534`、`guardian/mod.rs:126-190` | 每次同形调用都会再问（无审批指纹缓存） | 审批疲劳是真实用户痛点 | P2 |
| D6 | tree-sitter AST 级 bash 危险命令扫描 | MiMo：`bash.ts:419-447`、GIT_DESTRUCTIVE 矩阵 `:110-121` | `bash_readonly.go`/`isShellFileWriteCommand` 为正则/字符串启发式（agent.go:2064-2121） | 跨 `&&`/子 shell 的识别精度 | P2 |
| D7 | 风险四分类（READ/WRITE_LOCAL/EXEC/EXTERNAL）数据声明 | openworker：`risk.py:18-53` | ReadOnly() 布尔 + ReadOnlyCallChecker | 为并行、审批、审计提供统一风险语言 | P2 |
| D8 | 子代理权限继承快照 + fail-closed | MiMo：`permission/index.ts:277-330`；openworker：Explorer 强制 plan 模式（`tools/subagent.py:42-138`） | 子代理继承 headless gate（boot.go:1214-1219 注释：ask 解析为 allow） | **"sub-agent 无 UI → ask=allow"意味着子代理可静默执行本应询问的操作**，需显式继承快照 | **P1** |
| D9 | 工作区信任门（不信任 clone 不得自动 spawn MCP） | openworker：`manager.py:289-295` | trustdomain 包存在（internal/trustdomain）但覆盖面待对照 | 供应链防线 | P2 |

### 3.5 Provider / 重试 / 多厂商

| # | 机制 | 来源与位置 | fairpeer 现状 | 差距与借鉴点 | 优先级 |
|---|---|---|---|---|---|
| E1 | 原生多厂商 API（39 provider 目录、生成式模型清单） | pi：`ai/src/models.generated.ts:44` + 39 个 `providers/*.models.ts`；MiMo：models.dev 缓存（`provider/models.ts:111`） | 2 种 wire 协议，其余厂商靠 OpenAI 兼容网关 | 原生协议才能用全原生特性（思考流、缓存标记、工具粒度）；fairpeer 模板机制是低成本折中，但应保留"升级到原生"的路径 | P2 |
| E2 | 可重试错误分类学（正负双清单 60+ 模式） | pi：`ai/src/utils/retry.ts:7-90`（带 issue 号沉淀）；MiMo：`isRetryableTransientError` 单一事实源（`session/retry.ts:48-100`） | retry.go 按状态码+网关 body 白名单（retry.go:92-108） | fairpeer 分类粒度较粗，"配额耗尽不重试"等终态区分缺失 | P1 |
| E3 | 可见重试策略（外层横幅+封顶+retry-after-ms） | MiMo：`session/retry.ts:29-48,105-140` | Retrying 事件有，但无策略对象、无封顶配置 | 重试语义集中一处防分叉 | P2 |
| E4 | 模型分层路由（tier：ultra/standard/lite + 小任务自动降级） | MiMo：`provider.ts:36-38,1849-1857` | FastTaskModel 单值（serve.go:77） | 标题/摘要/嵌入各走各的廉价模型且零配置 | P2 |
| E5 | turn 级连接缓存 + 无界连接重试 + zstd 压缩 | codex：`core/src/client.rs:275,1160`、`responses_retry.rs:29-79` | 每次 Stream 新建请求 | 长会话延迟与断线韧性 | P3 |
| E6 | 每 turn 动态解析 API key（OAuth 过期自愈） | pi：`agent-loop.ts:305-306`；codex：401 token 刷新重放（`client.rs:1456,1539`） | key 启动时解析（provider.Config） | OAuth 类凭据会中途过期 | P2 |
| E7 | 溢出识别模式库（含 z.ai 静默溢出、MiMo 截断返回 length+0） | pi：`ai/src/utils/overflow.ts:1-43+` | finishReasonMessage 只认 3 种 finish（agent.go:2181-2195） | 26 模板 = 26 种网关怪癖，模式库直接决定健壮性 | P1 |

### 3.6 多智能体 / 编排

| # | 机制 | 来源与位置 | fairpeer 现状 | 差距与借鉴点 | 优先级 |
|---|---|---|---|---|---|
| F1 | 持久 agent 拓扑（spawn 边落库、深度上限、BFS 枚举） | codex：`agent-graph-store/store.rs:20`、`agent/registry.rs:91,337` | subagent 元工具排除实现单层委托（task.go:25-45），无持久拓扑 | 多级团队可恢复可观测 | P2 |
| F2 | continuable 子代理（驻留纪元 Activation+冷恢复+异构 provider 矩阵） | dsh：`subagent/continuation.ts:1-97` | **fairpeer 已有**：子代理转写 `continue_from`（原位续跑）/`fork_from`（复制分叉）（task.go:166-167, 325-364，逐行阅读修正——初稿误判"无续跑"）；剩余差距是 dsh 的驻留纪元语义（一个 Activation 可执行多轮 FIFO turn）与异构后端矩阵（Claude Code/Codex 官方 SDK 作为子代理） | 长任务委托的常驻与异构编排 | P3 |
| F3 | Inbox：跨会话人工注意队列（resolve-once 状态机） | openworker：`inbox.py:295-332` | ask 工具阻塞当前会话（agent/ask.go），无跨会话队列 | 无人值守场景（bot/定时）的审批不丢失 | P1 |
| F4 | standing rules（tool→精确 target 的自动批准 mint） | openworker：`permissions.py:62-80,160-171` | permission 规则支持 `Tool(glob)`，但无"从审批卡一键 mint+审计回链" | "Allow every time"的正确粒度 | P2 |
| F5 | 独立 judge 判停（goal 由裁判模型判 ok/impossible） | MiMo：`session/goal.ts:16-20,64-71` | goal_judge.go 存在（internal/agent/goal_judge.go）——**已对齐**，需对照判定语义三态 | fairpeer 已有类似机制，保持 | — |
| F6 | self-wake 自挂起原语（sleep_for/wake_on） | openworker：`selfwake.py:155-185` | scheduler 包是定时任务（internal/scheduler），agent 不能自挂起等事件 | 事件驱动长任务 | P3 |

### 3.7 前端 / 协议 / 分发

| # | 机制 | 来源与位置 | fairpeer 现状 | 差距与借鉴点 | 优先级 |
|---|---|---|---|---|---|
| G1 | 前端-内核协议化（SQ/EQ + JSON-RPC app-server） | codex：`session/mod.rs:367`、`tui/lib.rs:36-47`；pi：protocol 包 CBOR+分帧+快照权威（`schemas.ts:203-230`） | TUI/桌面各自直调 control.Controller；serve 是 HTTP/SSE 但无版本化协议 | 四前端复用一核心、断线重连快照+revision | P2 |
| G2 | 会话租约（shared/exclusive+引用计数+代际号） | pi：`client/session-handle.ts:13`、`client.ts:36-44` | serve 多标签共享一 controller 无仲裁 | 多客户端 attach 语义 | P3 |
| G3 | 流式补丁预演（生成中即渲染 diff） | codex：`streaming_parser.rs:22,139` | ChunkToolArgsDelta 已做 apply_patch 节流预览（agent.go:1396-1420）——**已对齐** | 保持 | — |
| G4 | hermetic e2e（scripted fake agent，零后端零网络） | openworker：`e2e/fixtures.ts:16-20`（61 spec） | e2ebench 存在但依赖真二进制 | GUI 回归的可持续性 | P2 |
| G5 | evals 基础设施（真实 AgentSession+模型 A/B+会话产物） | pi：`evals/src/pi-harness.ts:24` | benchmarks/ 目录存在，无模型级回归基线 | 改 prompt/换模型无回归网 | P2 |
| G6 | TUI Worker 隔离（渲染崩溃不拖核心） | MiMo：`cli/cmd/tui/thread.ts:33-56,270` | bubbletea 单进程 | 稳定性 | P3 |

### 3.8 工程质量

| # | 机制 | 来源与位置 | fairpeer 现状 | 差距与借鉴点 | 优先级 |
|---|---|---|---|---|---|
| H1 | 测试/源码比 | dsh 0.91、pi 0.9 | 88k/204k ≈ **0.43** | 已有大量 e2e/单测（agent 包 249 个 Test 函数），但边界包（desktop 前端、netdev）薄弱 | P2 |
| H2 | 架构守护脚本（gen+verify 成对、运行时闭包、入口唯一性） | dsh：50+ `scripts/verify-*.ts`、`ci-workflow.spec.ts` | 无 | 防"巨型文件再膨胀"的机器约束（见 P9） | P2 |
| H3 | 运行时不变量注册表 | dsh：`runtime-diagnostics/invariants/src/index.ts:50-197` | 断言散落 | 生产可开关的按包自检 | P3 |
| H4 | telemetry schema 即代码 + conformance | pi：`telemetry/src/index.ts:31-79` | event 包无 schema 化约束 | 跨版本事件演化安全 | P3 |
| H5 | 环境快照注入系统提示（pwd/平台/git 状态/近 5 commit） | openworker：`environment.py:57-78` | 时钟注入已有（agent.go:1298-1317），环境快照未见 | 省去每会话 3-4 次发现性工具调用 | P2 |
| H6 | 双语/多语言注释与决策记录（Agent Notes 制度） | dsh：`.agents/notes/implemented/`；MiMo：注释带 spec 交叉引用 | 注释质量本身很高（本 spec 大量受益），缺决策记录归档 | CHANGELOG+docs 已接近，制度化即可 | P3 |

---

## 4. fairpeer 已验证问题清单

> 每条含：现象、代码证据（本仓库 file:line）、验证方式与结果、影响、修复方向（详细需求见 §5 对应 Spec 条目）。
> 分级：HIGH=可致数据丢失/安全风险；MED=功能正确性/健壮性；LOW=质量/卫生。

### P1（HIGH，已复现）edit_file Level 4 块锚模糊匹配静默删除未提及行

- **证据**：`internal/tool/builtin/edit_fuzzy.go:327-398`（`blockAnchorMatch`：首尾行锚定后，`linesContainInOrder` 只要求 old 的中间行"按序出现"于区域（:382），区域取 `contentLines[startIdx:endIdx+1]`（:384）——**包含锚之间所有行，无论 old 是否提及**）；无任何"区域行数 vs old 行数"膨胀上限（`blankMiddleGapCap` 仅限 old 中间全空白的情形，:325, :374-381）；`internal/tool/builtin/editfile.go:102` 用 `strings.Replace(content, region, newStr, 1)` 整区域替换 → 区域中模型未提及的行被删除。调用方仅做"区域是原文子串"校验（editfile.go:96-100），无法察觉膨胀。
- **验证**：将 `edit_fuzzy.go` 抽出到仓库外独立程序，构造 `content = "package main\n\nfunc a() {\n\tfoo()\n\tbar()\n\tlogger.Info(\"done\")\n}\n"`、`old = "func a() {\n\tfoo()\n}"`（模型只想改 `foo()`→`baz()` 但少写两行）。实测输出：`found=true unique=true`，匹配区域 5 行（old 3 行），替换后 `bar()` 与 `logger.Info("done")` 均被删除，且全程无错误无提示。复现脚本与输出见 §6.1。
- **触发条件**：默认审批模式（auto/yolo）下无人工把关；ask 模式下审批卡虽有 FileDiff 预览，但用户通常只看"改了什么"而非"删了什么没提的"。
- **对照**：MiMo-Code 默认**关闭**全部 fuzzy（`edit.ts:662-664` 门控），且其错误提示"最接近匹配"明确"绝不静默应用"（:733-734 注释）；pi 的归一化匹配在命中后**按行块把未变更行从原文件拷回**（`edit-diff.ts:132-173`），结构上杜绝本问题；codex 的 seek_sequence 只做行内归一化、不做区域扩展（`seek_sequence.rs:8-118`）。
- **影响**：单次调用静默丢失任意多行业务代码；编译失败尚可察觉，若删除的是注释/日志/无害行则完全不可见。
- **修复方向**：Spec-1。

### P2（MED）会话保存为全量重写，写放大 O(n²)

- **证据**：`internal/agent/save.go:131-175`——每次 `Save` 全量重写 JSONL（:146 遍历全量 Snapshot），随后**再读回整个文件**计算 HMAC（:163-167），再写 `.sig`、再 load-modify-write `.meta`（:173, :182-194）；`internal/control/controller.go:2583-2604`——每 30 秒 `autosaveWhileRunning` 触发一次 `snapshot(false)` → `s.Save(path)`（:2632），另每轮 turn 结束再 snapshot；`present` sidecar 同样全量重写（:2641-2654）。
- **验证**：代码路径确认；长会话（数百轮 × 32KB 工具输出上限）单文件可达数十 MB，每 30 秒完整重写+重读一次。
- **影响**：磁盘写放大与功耗（对笔记本 SSD/Windows Defender 实时扫描尤其明显）；PI/codex/openworker 均为 append-only（pi session JSONL append；codex rollout append + `RolloutWriterTask` 专用写线程 `rollout/recorder.rs:139`；openworker `conversations.py:184-190` 只追加新消息）。
- **修复方向**：Spec-2。

### P3（MED）compactStuck 连带停止免费的 SoftTrim/Prune

- **证据**：`internal/agent/compact.go:125-147`——`if a.compactStuck { return }` 位于 `SoftTrimLargeResults()`（:133）与 `PruneStaleToolResults()`（:140）**之前**；而这两步不花 API 调用，本可以把 prompt 拉回阈值以下从而解除 stuck（:120-122 在 `u.PromptTokens < high` 时清零 stuck）。
- **验证**：代码顺序确认。场景：压缩后仍超阈值两次 → stuck 置位 → 之后即使 prune 一次就能降到阈值下，auto 路径也不再执行任何修剪，直至手动 `/compact`。
- **影响**：自我锁死的保守恢复；用户看到"auto-compaction paused"后上下文持续顶格。
- **修复方向**：Spec-3。

### P4（MED）provider 不回 usage 时自动压缩永不触发

- **证据**：`internal/agent/compact.go:100-103`——`if a.contextWindow <= 0 || u == nil || u.PromptTokens == 0 { return }`；usage 唯一来源是 `ChunkUsage`（agent.go:1421-1429），openai 侧虽然请求了 `IncludeUsage`（`internal/provider/openai/openai.go:335`），但**部分 OpenAI 兼容网关/中转会忽略 stream_options 不回 usage**（fairpeer 自身对网关怪癖有白名单认知，retry.go:86-108 即为一例）；anthropic 正常。无任何估算回退（`estimateMessagesTokens` 只用于 fold 经济学，compact.go:170-173）。
- **验证**：代码路径确认；对照 pi（usage 锚点+chars/4 尾部估算，compaction.ts:216-244）与 openworker（真值优先/估算兜底，compaction.py:48-57）均有回退。
- **影响**：此类 provider 下长会话直接撞上下文上限 400，用户需手动 `/compact`。
- **修复方向**：Spec-4。

### P5（MED）`postEditHook` 包级全局在多标签页下互相覆盖

- **证据**：`internal/tool/builtin/editfile.go:22`——`var postEditHook func(ctx context.Context, path string) string` 为**包级全局**；`internal/boot/boot.go:1184-1190`——每次 `boot.Build`（LSP.Enabled 时）用新闭包覆盖该全局；`desktop/tabs.go:1334`（每标签页 Build 主路径）+ `desktop/app.go:5266, 5486, 5647`（切模型/重建）——桌面端多标签页并发共存（boot.go:186-190 注释自述"enabling concurrent multi-project sessions"）。
- **验证**：代码路径确认。标签页 B 构建后，标签页 A 的 edit_file/write_file 诊断改由 B 的 lspMgr 处理（错误 root → 拿不到诊断或拿错诊断）；A 关闭时其 cleanup 关闭 A 的 lspMgr（boot.go:1191-1197），但全局仍指向 A 的闭包 → B 的编辑诊断静默失效（错误被 :1186-1188 吞掉）。
- **影响**：多项目多标签场景 LSP 诊断回路随机失效；同类全局还有 `postEditHook` 在 writefile 等工具的引用。
- **修复方向**：Spec-5。

### P6（MED，结构性差距）OS 级沙箱仅 macOS；Windows/Linux 裸奔

- **证据**：`internal/sandbox/sandbox.go:7-10`——"Only macOS (Seatbelt via sandbox-exec) is implemented; on every other OS … Command falls back to running the command unwrapped"；`RequireAvailable` 仅是可选 fail-closed（sandbox.go:27-30）。
- **验证**：代码注释与实现文件清单确认（sandbox 包仅 seatbelt_darwin.go/seatbelt_other.go/shell.go）。
- **对照**：codex 三平台齐备（Linux bwrap+seccomp `linux-sandbox/src/landlock.rs:35-66`；Windows 受限 token+ACL+WFP+独立桌面 `windows-sandbox-rs/`）；dsh 有 Landlock + Windows ACL 后端（`sandbox-local`、`sandbox-windows-acl`）+ e2b 远端世界。
- **影响**：fairpeer 的主战场平台（Windows 安装脚本在 README 首位）恰恰没有强制层；`[sandbox] enforce` 在非 macOS 上要么告警要么拒绝。
- **修复方向**：Spec-6。

### P7（MED）"子代理 ask 即放行"与权限继承缺失

- **证据**：`internal/boot/boot.go:1214-1219`——"The headless gate (no Approver) resolves "ask" to allow … Sub-agents always run headless: they have no UI to answer a prompt, so they inherit this same gate"；`internal/agent/task.go:68, 89-94` TaskTool 构造子 Agent 时传入同一 headless gate，`:479` `Gate: t.gate`（第 2 遍核查确认）。即：**主会话里需要用户批准的操作，交给 task 子代理执行时自动放行**。
- **验证**：代码注释与 Gate 语义确认（`internal/agent/agent.go:106-115` Gate 契约；nil gate 全放行，headless gate ask→allow）。
- **对照**：MiMo 子代理继承父授权快照、fail-closed（`permission/index.ts:277-330`）；openworker Explorer 子代理强制 plan 模式（`tools/subagent.py:42-138`）。
- **影响**：模型只需把敏感操作委派给 task 就能绕过交互审批（非恶意也极易自然发生）；安全边界形同虚设于委派路径。
- **修复方向**：Spec-7。

### P8（LOW）ListSessions 元缓存缺失时全量解码所有会话

- **证据**：`internal/agent/save.go:366-370`——`.meta` 无缓存字段时回退 `previewSession(full)` 全文件解码；`ListSessions` 扫描目录两层（:298-344）。
- **验证**：代码路径确认。数百个旧会话（升级用户首批必现）首次列表 = 全量解码数百 MB。
- **修复方向**：Spec-8（惰性回填）。

### P9（LOW）巨型文件趋势

- **证据**（`wc -l` 实测，非测试代码）：`desktop/app.go` 7,136 行、`internal/control/controller.go` 4,117、`internal/tool/builtin/browser.go` 4,078、`internal/cli/chat_tui.go` 4,032、`desktop/tabs.go` 3,927、`internal/config/config.go` 2,566、`internal/boot/boot.go` 2,500。
- **验证**：数据即验证。对照 pi（最大单文件 interactive-mode.ts 6,623 行但也已拆包分层）与 dsh（包即边界）。fairpeer 包边界本身清晰，问题集中在 desktop 与 cli 壳层。
- **影响**：合并冲突热点、评审成本、AI 助手（含 fairpeer 自己）编辑这些文件的出错率。
- **修复方向**：Spec-9。

### P10（LOW）仓库内跟踪了 6 个 Windows 可执行文件

- **证据**：`git ls-files | grep .exe` → `context-maintenance-e2e.exe`、`cua-replay.exe`、`desktop/test.exe`、`e2ebench.exe`、`fairpeer-plugin-example.exe`、`spike/stt/api/sttspike.exe`。
- **验证**：命令输出即验证（2026-09-08 于 feat/mindmap-read-loop 分支）。
- **影响**：仓库体积、误执行风险、Windows Defender 启发式误报源。
- **修复方向**：Spec-10。

### P11（LOW）go vet unsafe.Pointer 告警 ×5

- **证据**：`go vet ./internal/tool/...` → `screen_windows.go:568`、`uia_windows.go:239,242,243,244` "possible misuse of unsafe.Pointer"。
- **验证**：命令输出即验证。Windows UIA/屏幕捕获的 syscall 模式常见此类写法，多数为误报，但应逐条用 `//go:linkname`/unsafe.Slice 规范写法或加注释豁免。
- **修复方向**：Spec-10 一并处理。

### P12（LOW）repeat-success 循环防护的误判面

- **证据**：`internal/agent/agent.go:2064-2121`——`isShellFileWriteCommand` 把任何含未引用 `>` 的命令都视为"写文件"（`hasShellWriteRedirect` 唯一特例是 `>` 前紧邻字符 `'2'`）。
- **验证**：第 2 遍核查独立推演确认三类偏差：(a) **假阳性**：`echo hi > /dev/null`、`git log > /dev/null`（`>` 前为空格）落入通用分支被判为"写文件"，同命令第 3 次成功即被 loop guard 拦截（`repeatSuccessBreakThreshold = 2`，:1692），文案误导模型"换方法"；(b) **假阳性**：`>&2`、`1>&2`（fd 复制、非写盘）被判 true；(c) **假阴性**：`2>file`（stderr 落盘，确属写文件）因 `prev=='2'` 被跳过——`-2>file`（如 `head -n2>f`）同理放行。引号内 `>` 正确忽略。
- **影响**：偶发假阳性打断正常工作流；假阴性使个别 shell 写绕过 repeat-success 防护（风暴防护 stormSig 仍按 (tool,error) 兜底，故仅为降级）。
- **修复方向**：Spec-1 附带项（见 Spec-1 目标 5）。

### P13（文档确认，对照差距）checkpoint 不覆盖 bash 写副作用

- **证据**：`internal/checkpoint/checkpoint.go:6-10` 注释自述"Only edit-tool changes are tracked — bash side effects are not"。对照：MiMo 用 shadow git repo 做文件级快照（`snapshot/index.ts:86-90`）可覆盖任意进程写；pi 官方扩展 git-checkpoint（extensions examples）。
- **影响**：`bash` 内 `sed -i`/脚本改文件后无法 rewind。属于已知取舍，但存在低成本改进（把 bash 写命令纳入 strace/预扫描，或切 shadow-git 后端）。
- **修复方向**：Spec-11（可选）。

### P14（LOW）serve 无鉴权（已声明的设计取舍）

- **证据**：`internal/serve/serve.go:200-208`——"CORS is NOT applied by default … Do NOT use in production — the server has no auth"；:243-290 有 CSRF（Content-Type 强制 JSON）与 DNS-rebinding hostGuard。
- **验证**：代码注释确认。对照 openworker：内存 token + WS Origin 校验（`run.py:128-136`、`app.py:192-231`）；MiMo serve 非 loopback 强制密码（`serve.ts:16-20`）。
- **影响**：本机其他用户/进程可驱动会话（单用户桌面场景可接受，远口场景不可用）。
- **修复方向**：Spec-12（绑定非 loopback 时强制 token）。

### P15（LOW）Gate "remember" 分支对 Policy.Allow 的无锁 append

- **证据**：`internal/permission/permission.go:566-579`——`Gate.Check` 在 Approver 返回 `remember=true` 后执行 `g.Policy.Allow = append(g.Policy.Allow, rule)`，无任何锁；而 `Check` 由 `executeOne` 调用，后者在并行只读批中于多个 goroutine 并发运行（agent.go:1536-1544, 1663-1678）。交互提示本身已被 `controller.requestApproval` 的 `promptMu` 串行化（controller.go:3990-4010，逐行阅读确认），但 append 发生在 promptMu 释放**之后**的调用方 goroutine 中。
- **验证**：代码路径逐行确认。触发条件极窄：同一并行只读批内两个调用均命中 Ask 规则、用户均选"always allow"。后果是 slice header 竞态（最坏丢一条规则或内存违规——Go 下属未定义行为类）。
- **修复方向**：Gate 增加 mu 或将 remember 规则经 channel 交回单线程应用；一处五行级修改，随 Spec-5 一并处理。

### P16（LOW）checkpoint.RestoreCode 使用非原子写

- **证据**：`internal/checkpoint/checkpoint.go:599`——恢复写回用 `os.WriteFile`（truncate+write）；同文件 `persist`（:490-494）注释明确"Atomic (tmp+rename): a crash mid-write would otherwise leave a truncated JSON"，标准不一致。
- **验证**：逐行阅读确认。恢复是用户主动操作，窗口极小，但崩溃在恢复中途会留下截断的被恢复文件（比恢复前更糟）。
- **修复方向**：与 persist 一致改用 `fileutil.AtomicWriteFile`（一行改动），并入 Spec-11 批次。

### 观察项（不计问题，逐行阅读所得）

1. **Dream/Distill 与主会话共享 transcript**（dream.go:537-545 注释"runs on the shared session"，quietDreamSink 仅对 UI 隐藏）：后台自演化轮的任务简报与工具流量留在会话历史中，后续用户轮的模型上下文与 prompt-cache 前缀均受影响。属有意取舍，建议长期改为影子会话。
2. **apply_patch.seekSequence 取首个匹配、无唯一性检查**（apply_patch.go:274-305）：与 codex V4A 行为一致（seek_sequence.rs 同样取首匹配），是补丁格式的标准取舍，不构成 fairpeer 特有问题。
3. **op_gate.isTransientErr 关键词宽匹配**（op_gate.go:201-211）：`"[error]"`/`"[warn]"` 使任何含该字样的失败都绕过操作预算——对 PPT 校验脚本是有意的，但对普通 bash 错误输出同样生效，会削弱 opGate（stormSig 仍兜底）。

---

## 5. 改进规格

> 约定：以下每条 Spec 是**给未来实现者的需求**，不是实现。P0 = 下一批次必须；P1 = 紧随；P2/P3 = 排期。每条含背景/目标/非目标/详细要求/验收标准。

### Spec-1（P0）edit 模糊匹配安全化

**背景**：P1。fairpeer 的 5 级模糊是相对 pi/MiMo 的能力优势（一次成功率更高），不应砍掉，而应加安全边界。

**目标**：
1. 保留 Level 0-3；Level 4（块锚）增加**膨胀上限**：`region 行数 ≤ max(old 行数 × 2, old 行数 + 4)`，超限视为不匹配（错误文案沿用 oldStringNotFoundError 的 nearest-line 提示）。
2. 区域膨胀被容忍时（1 < 膨胀 ≤ 上限），**结果必须携带警示**：`"note: matched region included N extra lines present in the file but absent from old_string; they were replaced"`。
3. `editfile.go` 在 `region != old` 时，把 region 与 old 的差异行数写进返回消息（复用 `diff.Build`，模型可见）。
4. 新增 `MIMOCODE 式`逃生门不可取（fairpeer 定位不同）；改为配置项 `agent.fuzzy_edit = "safe" | "off" | "aggressive"`，默认 `safe`（=上述 1-3）；`off` = 仅 Level 0/1。

**非目标**：不改 multi_edit/apply_patch 的匹配语义（它们无区域扩展问题——apply_patch 逐 chunk 精确+归一化匹配）。

**附带项（对应 P12）**：重写 `hasShellWriteRedirect`/`isShellFileWriteCommand` 的写判定：增加白名单（`> /dev/null`、`>&2`、`1>&2`、`>/dev/null` 等非写盘形态不计为写）；修复 `2>file` 假阴性（前一字符为 `2` 时仍需检查后续是否为文件名而非仅跳过）。判定函数补表驱动单测（≥ 10 用例：真写、假写、fd 复制、stderr 落盘、引号内 `>`）。

**实现者提示（第 3 遍核查确认）**：`internal/tool/builtin/preview.go:89`（editFile.Preview）与 `:154`（multiEdit.Preview）调用的是**同一个** `fuzzyMatch`，因此膨胀上限加在 `blockAnchorMatch`/`fuzzyMatch` 内部即可让审批卡、checkpoint 基线与 Execute 三方自动一致；`preview_test.go` 的 `TestPreviewMatchesExecute` 已锁定预览=执行的不变量。警示文案（目标 2）需在 Execute 与 Preview 两处可见——Preview 路径可把它并入 `diff.Change` 新增字段承载，注意 `tool.Previewer` 接口签名保持兼容。

**验收标准**：
- [ ] §6.1 复现用例在 safe 模式下返回"not found (nearest line …)"或带膨胀警示，**不再静默删行**；
- [ ] 上下界用例：膨胀恰好 = 上限（成功+警示）、上限+1（不匹配）；
- [ ] 既有 `edit_fuzzy_test.go` 全绿；新增 block-anchor 膨胀表驱动用例 ≥ 6 个；
- [ ] `agent.fuzzy_edit=off` 时 Level 2-4 全部短路（性能：单次匹配 ≤ 1 次 strings.Count 级）；
- [ ] P12 附带项：`echo x > /dev/null` 三连跑不触发 repeat-success 拦截；`cmd 2>err.log` 三连失败/成功按写文件口径参与防护。

### Spec-2（P1）会话存储 append-only 化 + 延迟 HMAC

**背景**：P2。保持"compaction 可重写历史"能力的同时消除每 30 秒全量重写。

**目标**：
1. `Session.Save` 改为**追加段**模式：文件尾追加自上次保存以来的新消息（`offset` 记录于 `.meta`）；**仅当发生 compaction/rewrite**（`RewriteVersion` 变化）时才全量重写并重置 offset。
2. HMAC 签名改为对**追加段**增量计算（hashed-construction：`H(prev_sig || new_bytes)`），或改为 `.sig` 只在重写点签名 + 每段尾 `hmac(chunk, key)` 链。加载时逐段校验。
3. `present` sidecar 同样追加化（其 SyncBeforeSave/rewriteVersion 语义保留）。
4. 30 秒 autosave 频率不变；追加为 O(new)。

**非目标**：不引入 SQLite（.meta 已够）；不改变文件布局兼容性——旧全量文件可直接按新格式追加（offset 初始 = 文件长）。

**验收标准**：
- [ ] 100 轮会话中 95 轮保存为纯追加（用 tampered fs 统计 write 字节数 ≤ 消息增量 × 1.2）；
- [ ] kill -9 于追加中途 → 重新加载：最多丢最后一行（撕裂尾按 Spec-3 修复）且 HMAC 链校验失败段落被截断告警而非拒载；
- [ ] compaction 触发后文件回到全量态且旧 reader 可读；
- [ ] `fairpeer chat --resume` 对旧格式会话无感。

### Spec-3（P1）compaction 触发与恢复策略修正

**背景**：P3 + P4 + 3.2-B1/B3。

**目标**：
1. **stuck 后仍执行免费修剪**：`maybeCompact` 中把 `SoftTrimLargeResults` / `PruneStaleToolResults` 移到 `compactStuck` 检查之前；stuck 仅封锁**付费的** summarize。修剪后若 `估算 < high` 则照常清零 stuck（复用 :120-122 逻辑）。
2. **usage 缺失回退**：`u == nil || u.PromptTokens == 0` 时，用 `estimateMessagesTokens(session.Snapshot())`（compact.go:175-191 已有）折算触发；连续 N=3 次估算触发后发一次 Notice 说明"usage unavailable, using estimate"。
3. **切点结构化**：`tailStart` 的对齐条件从"消息起点非 tool 角色"升级为"**turn 边界**（user 消息起点）且该 turn 内所有 assistant tool_calls 均有配对 result"；无法对齐时回退现状。目标行为等同 pi `findValidCutPoints` 的不变量。
4. **溢出 400 识别**：openai provider 收到含 `context_length_exceeded` / `maximum context length` / `prompt is too long` 的错误时，标注 `provider.ErrContextOverflow`；agent.Run 收到该错误时：移除最后一条 user 消息 → 立即 `compact(force=true)` → 重发一次（仅一次，复用 streamRecoveries 预算风格）。

**非目标**：不引入 MiMo 式 checkpoint 写手（记为 Spec-11 后续）。

**验收标准**：
- [ ] 单测：stuck 置位后 prune 仍执行且能解除 stuck；
- [ ] mock 一个不回 usage 的 provider，跑 50 轮注入大输出的会话，自动压缩触发 ≥ 1 次且 Notice 出现；
- [ ] 表驱动：7 种历史形态（含跨 turn 孤儿 tool 消息、连续多 turn）切点全部落在 turn 边界；
- [ ] mock 网关返回 context_length_exceeded → 自动压缩 → 重试成功，且重试预算只消耗 1。

### Spec-4（P1）LSP 诊断钩子实例化

**背景**：P5。

**目标**：
1. 删除包级全局 `postEditHook`；`editFile`/`writeFile` 等 writer 增加字段 `postEdit func(ctx, path) string`（与 `roots`/`workDir` 同样的按实例绑定模式，参照 `ConfineWriters` 的 confining 实例惯用法，confine.go:45-73）。
2. `boot.Build` 在注册 writer 时注入本会话 lspMgr 闭包；`SetPostEditHook` 全局函数删除（或保留为 deprecated 转发到 registry 默认实例，一个批次后移除）。
3. 子代理经 `tool.Registry` 克隆天然继承（确认 `SubagentMetaTools` 过滤不含 writer）。

**验收标准**：
- [ ] 新增回归测试：同一进程先后 Build 两个不同 root 的 controller，A 的 edit 诊断来自 A 的 mgr、B 来自 B；关闭 A 后 B 诊断仍工作；
- [ ] `grep -rn "var postEditHook" internal/` 无结果（或 deprecated 注释）。

### Spec-5（P1）子代理权限继承收紧

**背景**：P7。

**目标**：
1. TaskTool/子 Agent 的 Gate 语义改为：**继承父会话当前 Policy 的 Allow/Deny 集**；父为 `ask` fallback 时，子代理对 ask 项**拒绝执行**（fail-closed，返回 "blocked: requires interactive approval; delegate read-only work or ask the parent to run it"），而非放行。
2. plan mode 原子布尔同步传给子代理（现仅主循环有效）。
3. 提供配置 `agent.subagent_ask = "deny" | "inherit-allow"`（默认 deny；`inherit-allow` 仅为兼容旧行为的逃生口，文档标注风险）。
4. `run_skill`/`explore`/`research`/`review` 等其余派生路径同审计（复用同一 Gate 构造函数）。

**验收标准**：
- [ ] 集成测试：父 ask 模式下，子代理调 bash 写命令 → blocked 文案可见；只读工具照常；
- [ ] 父 Deny 规则在子代理同样 Deny；
- [ ] plan mode 下子代理 writer 全 blocked。

### Spec-6（P2，分平台子批）Windows/Linux 沙箱落地

**背景**：P6。对标 codex 的最小可用集，不追全量。

**目标**（分两批）：
- **批 A（Linux）**：`sandbox_linux.go` 用 `github.com/landlock-ls/go-landlock`（纯 Go，无 cgo）实现 write-roots/read-roots/network 三语义；`Command()` 返回原地自限制（`Restrict()` after fork、exec 前）或短 spawn wrapper（与 codex `landlock-run` 同构）。`StrictWrites` 语义保留。与 Seatbelt 共用 `Spec` 结构，`Available()` 按 GOOS 探测。
- **批 B（Windows）**：最小可行 = **写边界 ACL**：对 workspace_root 之外的进程写能力不削减（做不到 D1 级），先实现"受限子令牌 + deny ACL on 敏感目录（用户配置的 protected_paths 默认含 ~/.ssh、~/.aws）"，语义在文档中诚实标注 `enforcement: partial`（对照 dsh `SandboxEnforcement` 的诚实声明，3.4-D 来源）。网络开关暂缺省 off。
- 两批均接入 `RequireAvailable` fail-closed 与 CI 冒烟（Ubuntu runner 跑 landlock 用例；Windows runner 跑 ACL 用例）。

**非目标**：seccomp BPF 过滤、WFP 网络过滤、独立桌面（记为后续）。

**验收标准**：
- [ ] Linux：沙箱内写 workspace 外目录 → EACCES；网络 allow/deny 行为正确；`go build`（无 cgo）通过；
- [ ] Windows：受限上下文中写 protected_paths → 拒绝；普通 workspace 写不受影响；
- [ ] `[sandbox] enforce` 在三平台行为矩阵写入 docs/CROSS_PLATFORM_SPEC.md。

### Spec-7（P2）MCP 工具目录检索加载

**背景**：3.3-C4。

**目标**：
1. 插件工具数量 > 阈值（默认 40）时，系统提示只注入**目录摘要**（每工具一行：名称+一句话用途，总预算 8K tokens）+ 一个新内置只读工具 `tool_search(query)`。
2. `tool_search` 本地 BM25（无外部依赖，标准 Go 实现即可）返回 top-k 工具的**完整 Schema**，选中工具在本 turn 内保持注入。
3. 配置 `agent.tool_catalog_mode = "all" | "search"`，默认 40 阈值以下仍为 `all`（零配置行为不变）。

**验收标准**：
- [ ] 100 个 mock 插件工具会话的 system+tools 尺寸 ≤ 阈值会话 × 1.5；
- [ ] 检索命中"被搜索到才可用的工具"并成功调用的 e2e 用例；
- [ ] 40 以下默认行为逐字节不变（prefix-shape 快照测试）。

### Spec-8（P2）ListSessions 惰性回填 + 前台 bash redact

**背景**：P8 + 3.3-C6。

**目标**：
1. `previewSession` 回退路径解码后**回写** `.meta` 缓存（已有 SaveBranchMetaPreserveUpdated）；列表页并发回填上限 4，避免首批卡顿。
2. bash/工具输出进入会话前过一遍可插拔 redact 链（首批规则：`AKIA[0-9A-Z]{16}`、`ghp_`/`github_pat_`、`sk-`、`xox[baprs]-`、JWT 三段式），命中替换为 `[REDACTED:<type>]`；redact 只作用于**进入模型的文本**与 present sidecar，不改变工具原始返回给 shell 的内容（fairpeer 无此分层——redact 在 agent.truncateToolOutput 之前做即可）。配置 `agent.redact_secrets = true`（默认 true）。

**验收标准**：
- [ ] 500 个无 meta 会话的列表首屏 < 500ms（回填后台完成）；
- [ ] 注入假密钥的 bash 输出在 JSONL/present/模型可见文本三处均被 redact；`off` 时逐字节不变。

### Spec-9（P3）壳层拆分与架构守护

**背景**：P9。

**目标**：
1. `desktop/app.go`（7,136 行）按现有方法簇拆文件（不拆包，纯文件拆分零风险）：tabs 生命周期 / provider·模型管理 / netdev 桥 / cowork 设置 / 更新与安装源；
2. `internal/control/controller.go` 拆出 `controller_session.go`（snapshot/resume/branches）、`controller_run.go`（Run/Steer/审批）；
3. 引入 dsh 式轻量守护：`scripts/verify_file_budget.sh`——非测试 Go 文件 > 4,500 行则 CI 失败（白名单存量，逐批清零）。

**验收标准**：CI 绿 + 白名单数量每批次递减 + 无行为变化（现有测试全绿）。

### Spec-10（P2）仓库卫生

**背景**：P10、P11。

**目标**：
1. `git rm --cached` 6 个 .exe；`.gitignore` 已有 `*.out/*.test` 补 `*.exe`（根与 desktop/spike 子目录）；verify 构建产物由 CI artifact 承载。
2. 5 处 unsafe.Pointer 告警逐条处理：确认 syscall 正确性后改用规范写法（`unsafe.Slice`/`windows.NumericContext`）或 `//nolint:govet // UIA syscall 布局要求` 带因豁免；CI 加 `go vet ./... -unsafe_ptr=false` 门槛（即 vet 干净为门）。

**验收标准**：`git ls-files | grep -i '\.exe$'` 为空；`go vet ./...` 输出为空。

### Spec-11（P3，可选）bash 写副作用快照

**背景**：P13。

**目标**（最小方案）：bash 工具执行前，对 `isShellFileWriteCommand`（agent.go:2064，含 sed -i / 重定向 / python open-w）命中的命令做**保守预快照**：解析显式目标路径（重定向目标、sed -i 参数），存在则走 checkpoint.Capture（与 Previewer 工具同路径）。无法解析目标（管道/脚本内写）保持不跟踪，行为不变。

**验收标准**：`sed -i` 与 `echo > f` 两用例可 rewind；解析失败的复杂命令不误捕获。

### Spec-12（P2）serve 鉴权 token

**背景**：P14。

**目标**：`fairpeer serve --host 0.0.0.0`（任何非 loopback 绑定）时强制生成启动期随机 token（打印到 stderr + 写 `~/.fairpeer/serve-<port>.token`，0600），HTTP 头 `Authorization: Bearer` 与 SSE 查询参数 `?token=` 双通道校验；loopback 绑定默认不启用（兼容现状），`--auth always` 可强制。对照 openworker 的 sidecar token 文件模式（`run.py:128-136`）。

**验收标准**：非 loopback 无 token → 401；SSE/REST/WS 三路均校验；loopback 默认行为不变。

---

## 6. 检测记录（3 遍）

### 6.1 P1 复现存档（第 1 遍检测的一部分）

复现环境：Windows，Go 工具链，**仓库外**临时目录（未触碰 fairpeer 源码；仅拷贝 `edit_fuzzy.go` 改包名）。

```
old_string 在原文中精确出现次数: 0 (0=fuzzy 层接管)

fuzzyMatch: found=true unique=true
匹配区域 (将被整体替换):
---
func a() {
	foo()
	bar()
	logger.Info("done")
}---
区域行数=5, old_string 行数=3
替换后文件:
---
package main

func a() {
	baz()
}
---
>>> 问题确认: bar() 被静默删除 — 模型从未提及要删它
>>> 问题确认: logger.Info 行也被静默删除
```

### 6.2 第 1 遍检测（自检，成稿当日，作者执行）

- **范围**：spec 内全部 fairpeer 侧 file:line 引用逐条 `sed -n` 回读原文（A 组 11 处 + B 组 14 处 + 附录 A 全表）；
- **结果**：28/29 处吻合；1 处范围起点偏差（P7 当时写的 `task.go:25-45` 覆盖的是元工具名单，gate 传递证据不足）已改引 `task.go:68, 89-94, 479`；
- **P1 复现脚本重跑**：结论一致（§6.1 输出为当次存档）；
- **断言强度复核**：将"bash 无默认超时"这类未经验证的猜测排除在问题清单之外（实测 `config.go:1347` 默认 120s，与 MiMo 对齐，属 fairpeer 现状良好项，写入 §2）；
- **诚实性修订**：spec 成稿过程中一次误编辑（在 3.3 表格注入无意义注释行）在检测中识别并当场撤销，未留下污染。

### 6.3 第 2 遍检测（独立 agent 交叉核对，2026-09-08）

由未参与撰写的独立核查方执行，范围：A 组 fairpeer 侧 16 项断言全查 + B 组五个项目外部引用各抽 2 条 + C 组 §4↔§5 映射与 §3↔§4 矛盾检查。结论：

- **A 组**：16/16 实质成立（其中 3 处行号/引用位漂移需修，见下）；
- **B 组**：10/10 实质成立（3 处行号漂移需修）；
- **C 组**：P→Spec 映射 13 条全部对应，唯一缺口是 P12 的修复方向未落入任何 Spec 正文；§3↔§4 无矛盾；
- **核查方附加发现**：`hasShellWriteRedirect` 存在 `2>file`（stderr 落盘）**假阴性**，及 `>&2`/`1>&2` **假阳性**——已并入 P12 证据；
- **据此修订 7 处**：P7 证据改引 `task.go:68,89-94,479`；桌面 Build 位置改引 `desktop/tabs.go:1334`（app.go:812 实为定时任务 headless Build）；sandbox.go 引用改 `:7-10`；附录 B codex `mod.rs:386-392`、MiMo `edit.ts:662-664`/`permission:190-195`、pi 路径补全；P12→Spec-1 缺口在 Spec-1 增补附带项。

### 6.4 第 3 遍检测（作者对抗性复核，2026-09-08）

- **修订后引用回验**：第 2 遍改出的全部新引用逐条回读原文确认（tabs.go:1334 确为 Build 调用点；task.go:68 `gate Gate` 字段、:89-94 "pass the headless variant" 注释、:479 `Gate: t.gate`；codex `mod.rs:386-392` 枚举体逐行吻合；MiMo `edit.ts:662-664` 门控与 `permission:190-195` FORCED_ASK 逐字吻合；pi `session-manager.ts:1358-1363` branch 方法吻合）；
- **P1 对抗性再推演**：主动寻找"能阻止删行的代码路径"——预览侧 `preview.go:58-108` 走同一 `fuzzyMatch`（审批卡与执行一致，`TestPreviewMatchesExecute` 锁定）；`editfile.go:96-100` 仅校验子串性；`107` 的 no-op 守卫与 `:112` 语法校验均不阻断区域膨胀。结论：P1 成立，且 Spec-1 的修复是**单点**的（在 `blockAnchorMatch` 内加上限即三方自动一致）——已作为实现者提示写入 Spec-1；
- **附带确认**：edit_file/multi_edit/write_file 的 checkpoint 跟踪存在且正确（Preview 实现于 `preview.go`，此前"可能未被 checkpoint 跟踪"的怀疑被否证，未写入问题清单——记录在此防复疑）。

### 6.5 检测结论汇总

| 遍次 | 执行方 | 核对量 | 发现偏差 | 处置 |
|---|---|---|---|---|
| 1 | 作者自检 | fairpeer 侧引用 29 处 + 复现重跑 | 1 处（task.go 范围起点） | 当场修正 |
| 2 | 独立核查 agent | fairpeer 16 项 + 外部 10 条 + 一致性 13 映射 | 7 处行号/引用位漂移 + 1 个映射缺口 + 1 个新发现（2>file 假阴性） | 全部落案（§6.3） |
| 3 | 作者对抗复核 | 修订后引用回验 + P1 反向推演 + 否证项排查 | 0 处残留 | spec 定稿 |

### 6.6 强化轮（应用户"逐行读、别偷懒"要求追加）

三轮检测之后又追加一轮**第一手逐行深读**：fairpeer 侧新逐行读完 12 个此前只读了部分/未读的核心文件（apply_patch、op_gate、interceptors、normalize、permission、checkpoint、writefile、readfile、preview、task、dream、tool.go，约 4,900 行），五个对比项目各亲自逐行读 1 个关键文件（附录 C.2）。所得：

1. **修正一处对 fairpeer 的低估**：F2 原写"子代理无续跑"，实际 `continue_from`/`fork_from` 已实现（task.go:166-167, 325-364），已改写；
2. **新增 P15**（Gate remember 分支无锁 append，permission.go:566-579）与 **P16**（RestoreCode 非原子写，checkpoint.go:599）；
3. **新增 3 条观察项**（Dream 共享 transcript、apply_patch 首匹配、op_gate 宽匹配）；
4. **补充 §2**：worktree 隔离与子代理转写能力；
5. 复核确认：apply_patch 无 P1 类区域膨胀（精确替换 pattern.len() 行）；preview 与 Execute 共用 fuzzyMatch（Spec-1 单点修复成立）。

三遍检测后的最终状态：**spec 内全部 fairpeer 侧引用与代码一致；外部引用 26 条中 23 条行号精确、3 条以"实际值已核对"的形式修正；问题清单 16 项（P1-P16）全部有代码证据，其中 P1 附独立复现；改进规格 12 条与问题清单一一对应（P15 并入 Spec-5、P16 并入 Spec-11）。**

---

## 附录 A：证据文件索引（本仓库）

| 断言 | 位置 |
|---|---|
| 模糊匹配 5 级与块锚 | internal/tool/builtin/edit_fuzzy.go:31-66, 327-398 |
| edit 替换执行 | internal/tool/builtin/editfile.go:84-117 |
| 全量保存 + HMAC | internal/agent/save.go:131-175 |
| 30s autosave | internal/control/controller.go:2583-2632 |
| compactStuck 先于修剪 | internal/agent/compact.go:125-147 |
| usage 空即返回 | internal/agent/compact.go:100-103 |
| include_usage 请求 | internal/provider/openai/openai.go:335 |
| postEditHook 全局 | internal/tool/builtin/editfile.go:22; internal/boot/boot.go:1184-1190 |
| 多标签并发 Build | desktop/tabs.go:1334; desktop/app.go:5266, 5486, 5647; desktop/tabs.go:38-45 |
| 子代理 headless gate | internal/boot/boot.go:1214-1219 |
| 沙箱仅 macOS | internal/sandbox/sandbox.go:7-10 |
| checkpoint 不含 bash | internal/checkpoint/checkpoint.go:6-10 |
| serve 无鉴权声明 | internal/serve/serve.go:200-208 |
| ListSessions 回退解码 | internal/agent/save.go:366-370 |
| redirect 启发式 | internal/agent/agent.go:2064-2121 |
| bash 默认超时 120s | internal/config/config.go:1347-1354 |

## 附录 B：五个对比项目的关键证据索引

（file:line 相对各自仓库根；由第 1-2 遍阅读记录，第 3-4 遍抽样复核。）


## 附录 C：逐行阅读覆盖清单（可审计）

> 本附录记录"逐行读代码"的第一手覆盖面：**亲自逐行读完**的文件（Read 全文，非摘要、非 agent 转述）与经由 agent 深读的范围。行数为实测。

### C.1 fairpeer —— 亲自逐行读完（全文）

| 文件 | 行数 | 阅读所得 |
|---|---|---|
| internal/agent/agent.go | 2,196 | 主循环/stream/interceptor 接线/三重循环防护/并行分批（全文两遍） |
| internal/agent/compact.go | 754 | 压缩全路径（发现 P3/P4） |
| internal/agent/session.go | 73 | 锁语义 |
| internal/agent/save.go | 522 | 持久化+HMAC（发现 P2/P8） |
| internal/agent/branch.go | 296 | sidecar 元数据分支模型 |
| internal/agent/prune.go | 141 | 两道免费修剪 |
| internal/agent/op_gate.go | 288 | 操作恢复状态机（全文） |
| internal/agent/interceptors.go | 156 | 拦截器链（全文） |
| internal/agent/task.go | 569 | 子代理全路径（修正 F2；worktree 隔离） |
| internal/agent/dream.go | 625 | 自演化双代理（全文） |
| internal/provider/provider.go | 681 | 抽象与消息规范化（全文） |
| internal/provider/retry.go | 236 | 重试全路径 |
| internal/provider/normalize.go | 194 | 快慢路径修复（全文） |
| internal/permission/permission.go | 745 | 策略/Gate/审批（发现 P15） |
| internal/sandbox/sandbox.go | 42 | 平台矩阵（P6） |
| internal/checkpoint/checkpoint.go | 681 | 快照/恢复（发现 P16） |
| internal/tool/tool.go | 312 | Tool/Previewer/ReadOnlyCallChecker 契约 |
| internal/tool/builtin/edit_fuzzy.go | 660 | 5 级匹配（P1 根因，全文两遍+离线复现） |
| internal/tool/builtin/editfile.go | 148 | 执行路径 |
| internal/tool/builtin/apply_patch.go | 641 | 两阶段+回滚（全文） |
| internal/tool/builtin/preview.go | 169 | 预览=执行不变量 |
| internal/tool/builtin/bash.go | 412 | 进程组/封顶/PATH 探测 |
| internal/tool/builtin/writefile.go | 79 | 全文 |
| internal/tool/builtin/readfile.go | 219 | 编码流式读取（全文） |
| internal/tool/builtin/confine.go | 196 | 边界与 symlink 处理 |

小计：**fairpeer 侧逐行精读 ≈ 11,800 行全文**，另加 boot.go/controller.go/serve.go/hook.go 四个大文件的关键路径段逐行（autosave、requestApproval、gateApprover、鉴权声明、12 事件）与全部 grep 级证据核对。

### C.2 五个对比项目 —— 亲自逐行读的关键文件

| 项目 | 文件 | 覆盖 | 所得 |
|---|---|---|---|
| pi | packages/agent/src/harness/compaction/compaction.ts:300-425 | 切点算法逐行 | toolResult 显式非切点；split-turn 三段式 |
| codex | codex-rs/apply-patch/src/seek_sequence.rs | 全文 193 行 | 四级递进+eof 锚定；替换精确 pattern.len() 行（与 fairpeer 块锚的本质差异，佐证 P1） |
| openworker | coworker/compaction.py:140-310 | 边界+机械提取逐行 | pick_boundary 双候选回退；extract_working_state 零幻觉；用户消息机械保全 |
| deepseek-harness | packages/spill/spill-policy/src/index.ts:1-60 | 模块契约逐行 | 双臂设计/best-effort/read 防循环 |
| MiMo-Code | packages/opencode/src/tool/edit.ts:640-760 | replace 全路径逐行 | 默认精确+fuzzy 链 opt-in+唯一性检查+closest-match 仅进错误信息（佐证 Spec-1 方向） |

### C.3 agent 深读范围（第 1-2 遍）与本 spec 的边界

五个对比项目各由一个专职探查 agent 做了全量子系统扫描（工具调用次数：openworker 61 / pi 65 / codex 71 / dsh 63 / MiMo 106），报告全部条目带 file:line；其中 10 条外部引用已由第 2 遍独立核查 agent 逐条回读原文验证，另 5 条关键文件由 C.2 的亲自逐行阅读覆盖。fairpeer 侧其余包（rag/memory/experts/bot/netdev/desktop 前端等）以结构扫描+关键路径核对为界，未逐行——**这是本 spec 明示的覆盖边界，不冒充全覆盖**。

- **pi**：树状会话 `packages/coding-agent/src/core/session-manager.ts:1358-1363,334-360,362-377`；切点 `packages/agent/src/harness/compaction/compaction.ts:312-422`；迭代摘要 `:461-498`；overflow 恢复 `packages/coding-agent/src/core/agent-session.ts:2050-2154`；edit 行块保护 `packages/coding-agent/src/core/tools/edit-diff.ts:132-173`；retry 双清单 `ai/src/utils/retry.ts:7-90`；溢出模式库 `ai/src/utils/overflow.ts`；protocol 快照权威 `protocol/src/schemas.ts:203-230`；撕裂尾修复 `packages/agent/src/harness/session/jsonl/storage.ts:84-92`。
- **codex**：fork-by-reference `codex-rs/core/src/session/mod.rs:386-392`（使用点 :409,:1424）；reverse scanner `codex-rs/rollout/src/reverse_jsonl_scanner.rs:20-72`；三平台沙箱 `linux-sandbox/src/landlock.rs:35-66`、`sandboxing/src/seatbelt.rs:14-96`、`windows-sandbox-rs/src/token.rs:171`；审批缓存+guardian `core/src/tools/approvals.rs:142,534`、`core/src/guardian/mod.rs:126-190`；agent-graph `agent-graph-store/src/store.rs:20`；seek_sequence `apply-patch/src/seek_sequence.rs:8-118`。
- **openworker**：durable resume `coworker/engine.py:270-312`；Inbox `coworker/inbox.py:295-332`；压缩兜底 `coworker/compaction.py:455-561`；standing rules `coworker/permissions.py:62-80,160-171`；持久 shell `coworker/tools/shell.py:219-391`；SSRF 逐跳 `coworker/web/guard.py:34-50,97-116`；调度补跑 `coworker/automation/scheduler.py:63-113`。
- **deepseek-harness**：generation 发布 `session-persistence-jsonl/generation.ts:70-89`；前缀对齐压缩 `compaction-basic/summarizer.ts:29-71`；spill `spill/spill-policy/src/index.ts:1-40`；repeat-guard `guard/repeat-tool-reminder/src/index.ts:89-232`；有界并行调度 `core/agent-loop/src/tool-calls.ts:60-247`；PTC `core/tools/src/ptc.ts:32-100`；持久重试 `llm/llm-retry/src/index.ts:188-258`。
- **MiMo-Code**：fuzzy 默认关 `packages/opencode/src/tool/edit.ts:662-664`（门控）与 `:681`（fuzzy 链）；closest-match 仅提示 `:733-734`；checkpoint 优先 `src/session/checkpoint.ts`、`src/session/prompt.ts:3455-3467`；forced-ask `src/permission/index.ts:190-195`（FORCED_ASK :195）与 `:256-266`；子代理权限路由 `src/permission/index.ts:277-330`；bash AST `src/tool/bash.ts:419-447`；输出清洗管线 `src/tool/bash_token_efficient_pipeline.ts`；goal judge `src/session/goal.ts:16-71`；BM25 工具检索 `src/tool/mcp-tool-search.ts:11-14`。
