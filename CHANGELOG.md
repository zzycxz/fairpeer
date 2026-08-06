# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
