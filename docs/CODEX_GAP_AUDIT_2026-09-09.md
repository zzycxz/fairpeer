# fairpeer vs codex 第二轮差距审计（源码级）

> 对照物：本地 codex 完整源码 `Swarm-OS/codex/codex-rs`（~140 crates）。
> 方法：双代理独立盘点双方能力面（各 10 维度、带文件证据），人工交叉核实关键断言。
> 日期：2026-09-09 | 前置：FAIRPEER_CODEX_GAP_SPEC 六维度已全部落地，本文覆盖**其余维度**。

---

## 结论摘要

第一轮六维度（工具卡片/对话搜索/证据链/PTY/事件架构/MCP 通知）已闭环，本轮未发现回退。
新排查发现的差距集中在**沙箱、脚本化输出、信任模型、部分工具语义**四处；
而工具面广度（84 vs ~15）、领域纵深（浏览器/办公/运维）、证据链、hooks/skills 生态上
fairpeer 反超。没有发现"推倒重来"级的缺口。

---

## 一、实质性差距（建议排期）

### G1【P1】Windows 无 OS 级沙箱后端 —— 且 Windows 是 fairpeer 的主平台
- codex：`windows-sandbox-rs/` 完整实现——受限令牌、deny-read ACL、AppContainer 桌面隔离、
  **WFP 网络过滤**、DPAPI、提权辅助（elevated_impl.rs），TUI 内有沙箱提示流。
- fairpeer：`internal/sandbox/` 仅 Seatbelt（darwin）+ Linux（bwrap），**Windows 路径缺省**
  （文档化 fail-open/refuse）。即 fairpeer 在主平台上跑 bash 无内核级围栏，只有
  permission 层的静态命令分析（bash_readonly.go）。
- 影响：审批模式"变更询问"挡得住模型提交的命令文本，但命令执行后的行为（子进程、
  网络外联）无系统级拦截。codex 在 Windows 上能做到按路径读写 ACL + 网络过滤。

### G2【P1→已修复 2026-09-09】CLI 无机器可读输出模式（codex exec --json）
- codex：`codex exec --json`（JSONL 事件流）+ `-o last-message` + `--output-schema`
  （JSON Schema 约束最终答案）+ `--ephemeral` + 稳定事件 schema（exec_events.rs）——
  CI/脚本/管道一等公民。
- fairpeer：`fairpeer run` 只有文本/markdown stdout + `--metrics` JSON（token/成本）。
  无事件流输出、无 schema 约束输出、无 ephemeral 模式。脚本集成只能靠抓屏幕文本。
- 影响：自动化编排（运维平台的无人值守场景尤甚）缺少可靠的机器接口。
  （serve/ACP/bot 是进程级接口，替代不了"一条命令跑完拿结构化结果"。）
- **修复**：`fairpeer run --json` 已落地——事件流按 eventwire 共享契约逐行输出
  （Item/ExpertCollab/Resumed 全覆盖，共享编解码器同步补齐三类，remotehost/desktop
  链路一并受益），结束时输出 `{"kind":"result","ok","text","sessionPath"}` 汇总行；
  错误与人读提示走 stderr。`--output-schema`/`--ephemeral` 仍为后续项。

### G3【P2】无项目信任等级（codex [projects."<path>"] trust_level）
- codex：per-project `trust_level`（trusted/untrusted）+ 信任引导 UI；不可信项目降权限。
- fairpeer：只有 **hooks** 有信任门（internal/hook/trust.go，项目 hooks 需用户信任 root）；
  项目级 `fairpeer.toml` 与 `.mcp.json` 的合并**无信任门**——克隆一个恶意仓库即带入
  配置/MCP 服务器。这与 codex 的 trust_directory 引导是同位功能缺失。

### G4【P2】网络策略无 per-host 准入（codex network-proxy）
- codex：本地 MITM HTTPS 代理（证书生成、凭据代理、per-host allow/deny/**ask**、
  SOCKS、Windows TCP 归因）+ 审批联动（NetworkPolicyAmendment）。
- fairpeer：沙箱 Network 开关（默认断网）+ `[network.proxy]` 系统代理；无域名粒度策略。
  Windows 上沙箱本就缺位（G1），网络面实际靠 permission 静态分析。

### G5【P2】exec 工具无持久交互会话（codex unified_exec）
- codex：`exec_command` 带 tty/yield_time_ms/max_output_tokens，unified_exec 进程管理器
  支持持久 shell 会话 + write_stdin 喂入。
- fairpeer：`bash`（后台任务）+ `bash_output`/`kill_shell` 覆盖了"跑长命令收输出"，
  但**无交互式 stdin 会话**（vim/ssh/python REPL 这类需要喂 stdin 的交互程序，
  代理只能靠 one-shot 技巧）。设备 PTY（HumanTTY）仅限 netdev 人工终端。

### G6【P2】read_file 不能读图（codex view_image）
- codex：`view_image` 工具把图片直接喂给多模态模型；统一 image 预算管理。
- fairpeer：read_file 仅文本；视觉走独立 `image_understand`（VLM 转述）——多一跳、
  丢细节，且与主模型的视觉能力脱节。多模态主模型（GPT-4o/Qwen-VL 类）无法直接看图。

---

## 二、形态差异（不算落后，记录在案）

| 维度 | codex | fairpeer | 判定 |
|---|---|---|---|
| 界面 | TUI（终端） | Wails 桌面 GUI + CLI 双面 | 形态差异；GUI 侧搜索/主题/状态显示已对标甚至更强 |
| Composer 增强 | Vim 模式、Ctrl+R 增量历史搜索、keymap 重映射、外部编辑器 | Alt+↑/↓ 历史回放、粘贴折叠、@ 模糊补全、语音输入 | Ctrl+R 级**增量搜索**缺（有回放无搜索）；其余形态差异 |
| Code Mode | V8 内嵌 JS REPL 编排工具调用 | 无 | codex 亦实验性；观察项 |
| 云端任务 | codex cloud 浏览/跑云端任务 | 无（linkpeer 移动端是另一生态位） | 产品依赖 |
| 实时语音对话 | realtime（WebRTC 双向） | 语音输入（STT 单向） | 形态差异 |
| Provider OAuth | ChatGPT OAuth PKCE + OS keyring + Bedrock SigV4 | 纯 API key + 加密 keystore | 目标市场（国内供应商）以 key 为主，影响低 |
| Claude Code 迁移 | /import | 无 | 低优先 |
| 会话存储 | SQLite + zstd + 索引 DB | JSONL + HMAC 完整性 + 目录搜索 | 各有取舍；量级大了后 fairpeer 需索引 |
| 遥测 | analytics + OTel + sentry | 匿名启动 ping（可关） | 隐私上 fairpeer 更保守，算特性 |

## 三、fairpeer 反超的维度（对照确认）

- **工具面广度**：84 个内建 vs codex ~15（codex 走"少而精 + MCP 扩展"路线，fairpeer 内建纵深）
- **领域套件**：浏览器自动化 21 工具、办公/文档套件、netdev 运维全域——codex 均无
- **证据链**：todo_write + complete_step 回执制签核（codex update_plan 无验证）
- **Hooks**：事件集对齐 codex（PreToolUse…PreCompact 十事件）且带项目信任门
- **Skills 市场**：ClawHub/Anthropic/OpenAI 多源 + install_source 安全管线
- **调度器**：持久化 + 中文自然语言时间（"每天早上9点"）——codex 无内建调度
- **IM bot**：QQ/飞书/Telegram/微信驱动 agent——codex 无
- **会话完整性**：JSONL + HMAC-SHA256 篡改检测——codex rollout 无签名
- **MCP 热插拔**：/mcp 管理器 + 通知热刷新（本轮落地）+ OAuth——对齐并部分超出
- **移动端**：linkpeer P2P——codex 无

## 四、建议优先级

1. **G1 Windows 沙箱**（P1）：主平台安全底线。可分期——先做受限令牌 + Job Object
   （codex windows-sandbox-rs 的核心机制有公开参考实现），WFP 网络过滤二期。
2. **G2 exec --json**（P1）：`fairpeer run --json` 输出既有 wire 事件流（Spec-5 已通线，
   item 事件可直接复用），工作量小、运维平台无人值守场景直接受益。
3. **G3 项目信任门**（P2）：项目配置/MCP 合并前引导确认，复用 hook trust 的既有
   trust.json 机制。
4. **G5 交互会话工具 / G6 view_image**（P2）：各约 1-2 天。
5. **G4 网络策略**（P2）：依赖 G1 的 WFP 层，随二期。
