# 设备操作审计链路说明(申报书口径核实)

> 目的:回答申报书中「操作步骤进入 git 审计」在代码里到底对应什么机制。
> 结论先行:**netdev 设备操作步骤不进 git**——它进入的是一条独立的
> **防篡改审计账本(SHA-256 哈希链 JSONL)**,链头定期**锚定到信任域分布式
> 账本**;git 只出现在工作区侧,用于呈现 agent 会话改动与提交历史。
> 申报书措辞建议按文末替换。

日期:2026-09-04

---

## 一、三层机制总览

| # | 机制 | 覆盖对象 | 防篡改手段 | 关键代码 |
|---|---|---|---|---|
| 1 | **netdev 审计账本**(设备操作审计) | 每一次设备交互(读/写/高危,含 agent 提案执行、人工直达命令) | 追加式 JSONL + 逐条 SHA-256 哈希链,整链可重算校验 | `internal/netdev/audit.go` |
| 2 | **信任域锚定**(audit cross-anchoring) | 上述哈希链的链头 | 链头按阈值写入 trust domain 分布式账本,多节点互持,单机重写整文件也能被发现 | `internal/netdev/auditanchor.go` |
| 3 | **工作区 git 面板** | agent 在工程目录里的会话改动与提交历史 | git 只读探针(status/numstat/log);`GitCheckout` 是唯一写路径 | `desktop/workspace_changes.go` |

另外两处相邻但**不属于**「步骤进入 git 审计」的机制,避免混淆:

- 会话快照/回退(`internal/checkpoint/checkpoint.go`)刻意 **git-free**,按用户轮次存文件快照;
- 案例/诊断包(`internal/netdev/cases.go`)是排查工作产物的时间线引用卡,Bundle 的 zip+manifest 版随 v1.1。

## 二、机制 1:审计账本(`internal/netdev/audit.go`)

- **存储**:`<用户配置目录>/fairpeer/netdev/audit.jsonl`,追加式,一行一条
  (`audit.go:27-32`、`auditFile :73`)。
- **记录内容**:`Audit` 结构(`audit.go:33-48`)——时间、设备、通路(via)、
  命令文本、分类器判定(class:read|write|dangerous)、执行状态、输出字节数、
  错误。**刻意不存原始回显**(设备回显可能带密钥,只存字节数)。
- **哈希链**:`hash = sha256(prevHash + 本条不含 hash 字段的规范化 JSON)`
  (`auditChainHash :15-25`)。写入时「读上一链头 → 哈希 → 追加 → 更新缓存」
  在同一临界区完成(`AppendAudit :115-158`),并发追加不会链错。
- **校验**:`VerifyAuditChain`(`:243-275`)重算整链,任何一条被篡改/删除,
  从该条起全部失配,返回首个断点行号。前端「审计」页签徽标实时展示
  (bridge `NetDevAuditVerify`,desktop/netdev_app.go:1967 接线;
  UI 在 `desktop/frontend/src/layouts/NetDevLayout.tsx` 审计页签)。
- 链落地前的历史条目 hash 为空,校验时跳过(链从第一条有哈希的条目开始)。

## 三、机制 2:信任域锚定(`internal/netdev/auditanchor.go`)

- **动机**:哈希链只能防「改中间某条」;有磁盘权限的人重写**整个文件**
  仍可自洽。把链头交给 trust domain 账本、多节点互持,单机无法全网重写。
- **频率**:每累积 16 条审计 或 距上次锚定超 10 分钟,取其一触发
  (`auditanchor.go:19-22`,遵守低频控制面不变量);锚定失败保留待办计数,
  下一条审计自动重试,绝不阻塞诊断主流程(`maybeAnchorAudit :69-97`)。
- 未配置 `[trustdomain]` 的部署不启用,钩子退化为一次互斥锁开销。

## 四、机制 3:工作区 git 面板(`desktop/workspace_changes.go`)

- `WorkspaceChanges()`(`:27`)把 agent 会话改动与 git 只读探针
  (分支/status/numstat)合并展示;`WorkspaceGitHistory :317`、
  `WorkspaceGitCommitDetail :348` 用 `git log` / `diff-tree` 展示提交历史。
- `GitCheckout :286` 是唯一写路径,其余 git 调用全部只读
  (见 `docs/WORKSPACE_GIT_SPEC.md`)。
- UI 入口:工作区面板 `desktop/frontend/src/components/WorkspacePanel.tsx`。

## 五、申报书口径核实结论与替换措辞

**核实结论**:「(netdev 操作)步骤进入 git 审计」与代码事实不符——
设备操作步骤进入的是哈希链审计账本 + 信任域锚定(机制 1+2),与 git 无关;
git 仅覆盖工作区侧的会话改动展示(机制 3)。申报书该句建议替换为:

> 平台对每一次设备操作(读、写、高危及 AI 提案执行)全量落盘防篡改审计
> 账本:逐条 SHA-256 哈希链接链,任何篡改或删除都会导致其后全部条目校验
> 失配,前端审计页实时展示整链校验状态;账本链头周期性锚定到信任域
> 分布式账本,多节点互持,可抵御单机整文件重写。工作区侧另提供 git
> 面板,呈现 AI 会话对工程文件的改动与提交历史,只读可审计。

## 六、建议截图取景(材料归档用)

1. **主图(审计页)**:netdev「审计」页签,顶部链校验徽标(绿色/总数/
   已链条数)与下方命令流水同框——一张图同时证明「记了什么」和「链可校验」。
   若能现场演示更好:手工改动 audit.jsonl 中间一行 → 徽标变红并指出断点行号。
2. **辅图(信任域锚定)**:trust domain 账本视图中最近一条 AnchorAudit 记录
   与本地 `AuditChainHead()` 值一致的对照(两张小图拼一)。
3. **辅图(工作区 git 面板)**:WorkspacePanel 的会话改动列表 + 提交历史,
   证明「git 审计」在工作区侧的真实存在,与主图口径互补。
