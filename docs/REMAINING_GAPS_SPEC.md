# fairpeer 未修内容盘点与实施规格书（收尾批）

> 对象：本会话全部评审轮次（15 个子代理 + 十遍清查 + codex 差距审计两轮）标记
> "应修"但尚未落地的每一项。日期：2026-09-12 | 状态：Draft → 待排期
>
> 裁决原则：
> - **应该修** = 有真实安全/正确性/UX 影响，且可在 1–2 天内高质量交付；
> - **专项排期** = 有真实影响但需多日（≥3 天）或跨领域协调，须先向用户确认排期；
> - **不该修** = 形态差异、纯理论路径、或修了反而引入风险。

---

## 一、逐项裁决表

| # | 差距 | 来源 | 裁决 | 理由 |
|---|------|------|------|------|
| 1 | G1 Windows OS 沙箱 | 审计 G1 | **专项排期** | 3–5 天安全工程；受限令牌/Job Object/WFP 须逐层验证 |
| 2 | G4 网络 per-host 策略 | 审计 G4 | **专项排期** | 依赖 G1 的 WFP 层或独立 MITM 代理，3–5 天 |
| 3 | exec_session OS 沙箱包装（macOS/Linux） | 评审 P4-A | **应该修** | sandbox.Spec 管线未生效（脚本中止丢失），macOS enforce 模式越狱 |
| 4 | exec_session 生命周期（teardown 泄漏 + write ctx + cap 跨 tab） | 评审 P4-A | **应该修** | 进程孤儿 + 写死锁 + 跨 tab 干扰 |
| 5 | view_image 桌面可见性 | 评审 P3-B | **应该修** | 模型说"看到了"但用户只看到路径字符串——真实性缺口 |
| 6 | anthropic vision-strip | 评审 P4-B | **应该修** | bailian-coding 预设 (vision=false) 是真实部署路径 |
| 7 | G3 信任门 v1.2 扩展（LSP/rg_path/browser_path/scheduler） | 评审 P4-B F4 | **应该修** | LSP command 是未信任 TOML 直接拉起的代码执行面 |
| 8 | 信任门 pin 条件边界 | 评审 P5-A P1 | **不修（误报）** | 评审误读标志语义——projectPluginsPresent 是文件存在性而非 plugins 段存在性，statusline-only TOML 必然触发门 |
| 9 | exec_session write ctx-awareness | 评审 P4-A | **应该修** | 阻塞写可卡住 tool call 整轮 |
| 10 | exec_session 跨 tab 会话命名空间 | 评审 P4-C | **不修（暂缓）** | 单用户桌面，跨 tab 干扰是设计取舍不是缺陷；netdev 已封印 |
| 11 | i18n trust_cmd 硬编码中文 | 评审 P5 | **应该修** | 一行 |
| 12 | Spec-1 语法高亮卡 / Exploring | 首轮裁决 | 不修 | generic 管线已覆盖，专卡冗余（已裁决） |
| 13 | Spec-2 Phase 3 工具输出高亮 | 首轮裁决 | 不修 | 语法高亮器内部穿透需渲染架构重构，P3 |
| 14 | Spec-5 后续 item-event 迁移 | 首轮裁决 | 不修 | 架构重构待稳定，已通线 |
| 15 | G2 --output-schema/--ephemeral | 审计 G2 | 不修 | 低优后续 |
| 16 | Claude Code 迁移 / 云端任务 / 实时语音 | 审计 | 不修 | 产品生态位差异 |
| 17 | view_image 双读 / base64 会话膨胀 / tokPerChar 偏差 | 评审 P3 | 不修 | 均为 P3 运营优化，行为正确 |

**结论**：#3/4/5/6/7/9/11 共 7 项确认应该修，合计约 4–5 天；#1/2 共 2 项专项排期；
#8 为误报；#10/12–17 为裁决不做。

---

## 二、实施规格

### Spec-α：exec_session 纪律补全（#3 + #4 + #9 + #11，合计 ~1.5 天）

#### α-1 OS 沙箱包装（macOS/Linux）

**现状**：`execSessionSpawn` 用裸 `exec.CommandContext(sctx, shell, flag, command)`，
不经过 `sandbox.Command(sb, shell, command)` 包装。macOS `[sandbox] mode="enforce"` 下
bash 被 Seatbelt 困住而 exec_session 裸跑——一键越狱。

**修法**：
```go
// execSessionTool 增字段：
sb sandbox.Spec

// workspace.go 绑定：
"exec_session": execSessionTool{workDir: w.Dir, sb: w.Bash},

// execSessionSpawn：
argv, _ := sandbox.Command(p.sb, shell, command)
cmd := exec.CommandContext(sctx, argv[0], argv[1:]...)
```
**注意**：`sandbox.Command` 非 enforce 时返回原 argv + false——不需要 fallback 分支
（bash 同样忽略 bool）。

**验证限制**：沙箱包装仅 macOS/Linux 生效；Windows 开发机上只能交叉编译验证构建，
运行时行为需 CI（macOS runner）或手动 macOS 验证。

**注意**：Windows 无 OS 沙箱后端（G1），`sandbox.Command` 对 Windows 返回原 argv
（seatbelt_other.go 只在非 darwin 编译时给 bwrap），行为与当前一致。macOS/Linux 生效。

#### α-2 write ctx-awareness

**现状**：`execSessionWrite(p)` 不接收 ctx；子进程停消费 stdin 时 Write 永久阻塞。

**修法**：
```go
func execSessionWrite(ctx context.Context, p execSessionArgs) (string, error) {
    // stdinMu 防并发写交错；ctx select 防永久阻塞
    s.stdinMu.Lock()
    defer s.stdinMu.Unlock()
    type result struct{ n int; err error }
    ch := make(chan result, 1)
    go func() {
        n, err := s.stdin.Write([]byte(input))
        ch <- result{n, err}
    }()
    select {
    case r := <-ch:
        if r.err != nil { return "", fmt.Errorf(...) }
        return fmt.Sprintf("sent %d bytes...", r.n), nil
    case <-ctx.Done():
        return "", fmt.Errorf("write cancelled: %w", ctx.Err())
    }
}
```

#### α-3 teardown KillAll

**现状**：sessions map 是包级全局，controller.Close / tab 关闭无任何回收路径。
8 个泄漏的活会话永久占用 cap，进程存活期内无法释放。

**修法**：
```go
// KillAll terminates every live session. Called from controller teardown.
func KillAll() {
    sessMu.Lock()
    defer sessMu.Unlock()
    for _, s := range sessions {
        s.mu.Lock()
        exited := s.exited
        cancel := s.cancel
        s.mu.Unlock()
        if !exited && cancel != nil { cancel() }
        if s.stdin != nil { s.stdin.Close() }
    }
}
```
**接线**：boot.go 在 Build 返回的 cleanup 链中追加 `builtin.KillAll()`。
**作用域注意**：KillAll 是进程级的——关一个 tab 会杀所有 tab 的会话。对进程退出正确；
单 tab 关闭应跳过（v1 不做 per-tab tracking，留作后续）。

#### α-4 i18n trust_cmd

trust_cmd.go:31/42 硬编码中文 → 改用 i18n.M（新增 UsageTrustGranted/UsageTrustRevoked
两 key，en/zh 各一行）。

#### α 测试计划

- spawn → write → read → kill 生命周期（已有，补 POSIX LF 断言）；
- sandbox 包装：macOS 上 `sandbox.Spec{Mode:"enforce"}` 时 argv 含 seatbelt 前缀
  （mock Shell + Spec 断言）；
- write ctx 取消：子进程 `sleep 999` 模拟不消费，write 在 ctx 取消后返回错误；
- KillAll 后 sessions 清空 + 进程退出。

---

### Spec-β：view_image 桌面可见性 + anthropic vision-strip（~1.5 天）

#### β-1 内核：view_image 结果 → event.Attachment

**现状**：ToolResult 事件的 `Attachments` 字段由 `extractImageAttachments` 填充，
正则只匹配 `.fairpeer/attachments/` 下的路径——view_image 的任意绝对路径不命中，
前端收到零附件。

**修法**（agent.go ToolResult 装配处 + extractImageAttachments）：
```go
// view_image 成功输出时，额外附加 event.Attachment{Path: <abs>, Kind: "image"}。
// event.Attachment.Path 的文档合同从 "repo-relative, under .fairpeer/attachments/"
// 放宽为 "workspace-relative or absolute (view_image)"。
```
**安全边界**：event.Attachment 路径仅供前端 `AttachmentDataURL` 桥读图。
`internal/control/attachments.go:316` `cleanAttachmentPath` 目前硬拒绝对路径——
需新增允许分支：路径在 `[sandbox] read_roots`（或 workspace root）内且扩展名为
图像 → 允许。不做的话前端拿到绝对路径也无法读文件。

#### β-2 前端：ToolCard view_image 渲染

ToolCard.tsx 已有 `ToolAttachments` 组件（通过 `app.AttachmentDataURL` 拉图）。
最小改动：`lib/tools.ts` `summarize` 加 view_image case（"viewed <basename>"）；
ToolCard 检测 `item.name === "view_image"` 且 output 含 `view_image: <path>` 标记时
渲染 `<ToolAttachments paths={[path]}/>`。

#### β-3 anthropic vision-strip

**现状**：anthropic provider 无任何 vision 门——image blocks 无条件发 wire。
bailian-coding 预设（kind=anthropic, vision=false，文本/编码模型）是真实部署路径。

**修法**（与 openai 同构）：
1. anthropic client 加 `vision bool` 字段（从 provider.Config 读，同 openai 的
   `c.vision` 注入路径）；
2. buildRequest 中 RoleUser 图像块与 RoleTool 嵌套图像块：`!vision` 时剥离
   image blocks，保留 text；
3. RoleTool 嵌套块为空时保留 "(no output)" 兜底（已有）。

#### β 测试计划

- openai_test：RoleTool parts → vision=true 含 image_url 数组 / vision=false 纯 text
  字符串（钉 P2-2 修复）；
- anthropic_test：RoleTool parts → 嵌套 text/image blocks；vision=false 时仅 text；
- 前端：ToolCard 对 view_image 输出渲染附件（如项目有前端测试基建）。

---

### Spec-γ：G3 信任门 v1.2——安全面快照扩展（~0.5 天）

#### γ-1 快照扩展

`projectSecuritySnapshot` 增字段：
```go
LSP       LSPConfig       // [lsp] servers.<id>.command — 未信任 TOML 可拉起任意二进制
Search    SearchConfig    // [search] rg_path — grep 工具直接 exec
Cowork    CoworkConfig    // [cowork] browser_path + smtp/imap 凭据通道
Scheduler SchedulerConfig // [scheduler] confirm_agent_tasks — agent 自建 AI 任务的审批门
```
restoreProjectSecurity 同步恢复四组。快照/恢复时机不变（用户源合并后、项目合并后）。

#### γ-2 .env 范围声明（v1 遗留，本版只文档化）

dotenv 加载注入任意未设环境变量——数据面外泄向量。v1 仅文档声明；
v2 可收窄为白名单（`FAIRPEER_*` + provider `api_key_env` 显式键）。

#### γ 测试计划

- 未信任项目 TOML 含 `[lsp.servers.x] command = "evil"` → cfg.LSP 无 x 服务器；
- 含 `[search] rg_path = "evil"` → cfg.Search.RgPath 为默认值；
- 信任后 → 正常合并；
- statusline 命令未信任项目 → 恢复为用户全局值。

---

### Spec-δ（专项排期）：G1 Windows OS 沙箱 + G4 网络 per-host 策略

#### δ-1 G1 Windows 沙箱（3–5 天）

分层实施（对标 codex windows-sandbox-rs）：
- **P1 层（1 天）**：受限令牌 + Job Object（KILL_ON_JOB_CLOSE）——封文件写/子进程逃逸；
- **P2 层（1–2 天）**：deny-read ACL（敏感目录读封堵）+ AppContainer 桌面隔离；
- **P3 层（1–2 天）**：WFP 网络过滤（per-host/per-port deny）；
- 接口：`internal/sandbox/sandbox_windows.go` 实现 `Command(sb Spec, sh, cmd)` 同签名，
  boot 路径 `ConfineBash`/`ConfineWriters`/`ConfineReaders`/exec_session 全部自动生效。

#### δ-2 G4 网络策略（2–3 天，可独立于 δ-1）

- 轻量路线（无 WFP）：代理层 per-host allow/deny——`[network.proxy]` 已有基础，
  加 `[network.policy]` 段（host 列表 + 默认动作 + 超策略时的 deny 日志）；
- 完整路线（依赖 δ-1 P3 WFP）：OS 层按策略过滤出站——与 G1 P3 合并实施。

#### δ 验收

- enforce 模式下 bash/exec_session/view_image 三工具的文件写/网络出站行为一致；
- 未信任项目 TOML 不能解除沙箱/网络限制；
- 每层有独立的绕过测试（mock OS 调用断言策略参数）。

---

## 三、排期建议

| 批次 | 内容 | 天数 | 前置 |
|------|------|------|------|
| **α** | exec_session 纪律补全 | 1.5 | 无（可立即动工） |
| **β** | view_image 可见性 + anthropic strip | 1.5 | 无（可立即动工） |
| **γ** | G3 信任门 v1.2 | 0.5 | 无（可立即动工） |
| **δ-1** | G1 Windows 沙箱 P1 层 | 1–2 | 无 |
| **δ-2** | G4 网络策略 | 2–3 | δ-1 P3 层（可选独立） |
| 切版 | 全量回归 + 0.2.4 切版 + exe | 0.5 | α+β+γ 后 |

α+β+γ 可并行/串行合计 ~3.5 天；δ 专项需确认后单独排。
