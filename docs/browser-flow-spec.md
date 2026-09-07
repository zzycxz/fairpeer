# 浏览器工作流（browser-flow）完整规格

> 版本：2026-09-06 · 状态：已实现（与 `internal/tool/builtin/browserflow.go`、`desktop/browser_console_app.go`、`desktop/browser_console_watch.go` 对应）
> 面向：技能作者 / 内核维护者。用户操作手册见 `desktop/frontend/src/guides/browser-ops-guide.md`。

---

## 1. 概述

运维界面右侧栏「浏览器」提供一套**确定性网页工作流引擎**：把人工操作录制/编排成步骤表（SKILL.md），之后无需大模型即可精确回放，并支撑面板试运行、对话调用（run_skill）、定时巡检三种执行路径。

设计三原则：

1. **确定性优先**：同样的步骤每次执行完全一致，可无人值守；大模型只出现在明确标注的环节（AI 研判）。
2. **速度优先、质量兜底**：快路径零开销（无控制列 = 裸执行），质量机制（校验/重试/复核/留证）全部 opt-in 且预算封顶。
3. **失败要响**：宁可 loudly fail 不可静默错——时间写入失败抛错、校验不过视同失败、拼写错误在打开浏览器前报错。

---

## 2. 技能文件格式

位置：`~/.fairpeer/skills/<name>/SKILL.md`（name 需匹配 `^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`，否则回退用目录名）。

### 2.1 frontmatter

| 字段 | 必填 | 说明 |
|------|------|------|
| `name` | ✓ | 调用名（`/name 参数=值`） |
| `description` | ✓ | 一句话用途 |
| `executor` | — | `browser-flow` = 确定性执行（绕过 LLM 子代理） |
| `domain` | — | `browser-ops` 面板域标记 |
| `draft` | — | `true` 时 run_skill 拒绝调用（面板试运行/巡检不受限） |
| `params` | — | 参数默认值，`k=v, k2=v2`（逗号分隔，区别于调用的空格分隔） |
| `runAs` / `allowed-tools` | — | 沿用技能体系通用字段 |

### 2.2 步骤表（`## 步骤` 段）

```
| # | 操作 | 目标 | 值 | 控制 |
|---|------|------|------|------|
| 1 | type | `input[name="time_range"]` | {{时间范围}} |  |
| 2 | click | `text=导出;;.log-center .x-btn` |  | 重试=1 校验=networkidle |
```

- **4 列或 5 列**均可；第 5 列（控制）不写 = 无 harness，行为与旧版完全一致。
- 单元格两端的反引号被剥离；**单元格内不能出现 `|`**（会切断表格）——查询语句、JS 表达式需避开。
- 表格外的散文会被解析器忽略；frontmatter 未知键会让结构化编辑器拒绝切回（lossy 护栏）。

---

## 3. 步骤操作词汇（17 种）

| 操作 | 目标列 | 值列 |
|------|--------|------|
| navigate | URL | — |
| back / forward / screenshot | — | — |
| switch_tab | 1 起始序号**或**页卡标题（先精确后包含） | 页卡标题（备注） |
| click | 锚点链 | — |
| hover | 锚点链 | — |
| type | 锚点链 | 输入文本 |
| key | 锚点（可空） | enter/tab/escape（默认 enter） |
| scroll | 方向（默认 down） | 屏数（默认 3） |
| select | 锚点链 | 选项值 |
| upload | CSS/ref | 文件路径逗号分隔 |
| wait | 条件（见 §6） | 超时秒数（>600 的数按毫秒折算） |
| extract | 锚点链 | `table` / `markdown`（结构化提取） |
| screenshot | — | — |
| evaluate | JS 表达式 | — |
| human | 自动检测条件 | 提示语（流程模式缺条件=规划期报错） |
| ask | 回复绑定的参数名 | 问题（流程模式视为已满足的参数） |

## 4. 锚点系统

目标列支持**多级回退链**，`;;` 分隔（不能用 `|`），按序尝试、首个命中生效：

```
.logcenterpanel .x-toolbar a.x-btn;;text=导出
```

- **CSS 锚**：执行前做 `querySelector` 存在性预检（2s 上限）——坏锚点毫秒级失败，在步骤共享的 **8s 等待预算**内 300ms 轮询"等目标出现"，预算耗尽立即回退下一锚（不再烧 40s 动作窗口）。
- **`text=可见文字` 锚**：按可见标签（innerText/value/placeholder/aria-label/title）匹配，先精确后包含（<40 字符）；命中后当步 JS 派发动作。属性不受改版影响时优先 CSS，改版保命靠 text=。
- **点击全部走 JS `el.click()`**（scrollIntoView + 原生 click，含 checkbox/radio 前后状态核对）：CDP Input 域在运维控制台环境实测不可达，ref/文字/选择器三条路径统一 JS 派发。
- **hover 走 JS 事件序列**（pointerover/mouseover/pointermove/mousemove/mouseenter）：悬停菜单生效；纯 CSS `:hover` 样式无法模拟，步骤输出明示。

## 5. 参数与时间范围

- `{{参数名}}` 可出现在任意字符串字段；**整串**值等于时间短语时在**该步执行那一刻**惰性换算（一次运行内缓存，保证多步同窗）。
- 支持短语：`最近/近/过去 N 分钟|小时|天`、`last N m/h/d`、`今天/昨天/前天/本周/上周`、已格式化的 `Y-m-d H:m:s - Y-m-d H:m:s`（直通规范化）。嵌在长文本里的短语不换算。
- 输出格式固定 `2006-01-02 15:04:05 - 2006-01-02 15:04:05`（`builtin.ResolveTimeRange`；`TimeRangeBounds` 反解）。

## 6. wait 条件词汇

`load` / `networkidle` / **`download`**（或 `download:.xlsx`：轮询 CDP 下载记录至终态并校验磁盘文件，返回完整路径）/ `visible:<sel>` / `hidden:<sel>` / `title:<text>` / `url:<text>` / `stable:<sel>`。

`stable:` 流式判稳（AI 回答类页面）：
- 签名（匹配数+文本长度+子元素数）**出现过变化**后按自适应静默窗（2-8s，随流式时长增长）判稳；
- 自始静止的内容走**两档静态兜底**：实质性内容（≥40 字符或有子元素）10s 确认；短静态内容（占位符形态，如"(AI生成)"）**30s** 确认——模型首字前思考超 10s 不再误判为完成。

下载机制：文件落 `%LOCALAPPDATA%\fairpeer\browser-downloads\<建议名>`；记录**保留式**（drain 标记不丢弃，上限 20）；wait 进入时快照 + 20s 宽限窗区分"本轮导出"与"陈旧下载"。

## 7. Harness 控制列（第 5 列）

语法（空格分隔，中英键等价，**解析在表格解析期、拼写错误在打开浏览器前报错**）：

```
重试=N 校验=<wait条件> 校验预算=Ns 失败=停止|继续|视觉
retry=N verify=<cond> verify-budget=Ns on-fail=stop|continue|vision
```

执行契约（`runFlowStepHarness` / `runConsoleSteps` 内同名逻辑）：

```
for attempt in 0..N:
    if attempt > 0:
        复核(3s)：校验条件已满足？ → "上一次动作已生效（信号滞后）"，结束 ✔
        退避 1s/2s/4s…
    执行动作
    动作失败 → 记录，重试
    校验(默认10s)失败 → 视同失败，重试
    成功 → 输出 + "（校验通过）" ✔
最终失败 → 截图存 browser-evidence/<步骤>-<时间戳>.jpg，路径并入错误
```

关键语义：

- **复核再重发**：校验信号滞后 ≠ 动作没生效；退避后先花 3s 复核，通过则不重发——杜绝慢信号导致的二次点击/二次导出。
- **失败=继续**：记录失败继续后续步骤；终态事件/报告汇总"⚠️ N 步失败但放行"；巡检场景由补漏窗口衔接数据缺口。
- **失败=视觉**：预留挂钩，当前按停止处理并在输出注明。
- 空控制列 = 单次裸执行，**零额外延迟**。

## 8. 执行路径与会话生命周期

| | 面板试运行 / 巡检 | 对话 run_skill |
|---|---|---|
| 入口 | 编辑器试运行、巡检页签、技能▶ | `/技能名 参数=值` |
| 会话 | 控制台会话（复用当前页卡） | `newBrowserSession`（持久浏览器） |
| 执行器 | `runConsoleSteps`（共享） | `RunBrowserFlow` |
| 断点 | human/ask 可 park（试运行）/拒绝（巡检） | 规划期即要求参数齐备 |
| 人工步骤 | 支持 | human 缺检测条件=规划期报错 |

页签语义：
- **switch_tab 开头的技能**：会话直接绑到第一个真实页面（不新建 about:blank），技能自己的 switch_tab 再切到命名页卡。
- **navigate 开头的技能**：新建空白页签（agent 的导航不得劫持用户正在看的页面）。
- 页签重绑时**被遗弃的空白页签显式关闭**；真实页面永不被关。
- 空闲会话 10 分钟回收（回收只断开，不动用户页卡）。

输出契约：报告每步截断 400 字符，**extract/evaluate 载荷步骤 6000**（面板显示 2000/载荷 6000）；下载文件带完整路径。

## 9. 定时巡检（watch）

- **配置**：技能 + 间隔（1/3/5/10/15/30 分钟，下限 60s）+ 巡检时间（HH:MM 锚点，空=启动整分）+ 研判方式 + 通知投递。持久化 `browser_watch.json`，应用重启自动恢复。
- **整分对齐调度**：锚点取整到分钟，第 1 轮（隐式锚点）立即查 `[锚点-间隔, 锚点]`，之后在 `锚点+N×间隔` 刻度触发（`nextWatchFire` 免疫 Ticker 漂移），每轮查**刚闭合**的间隔——窗口边界与执行延迟完全解耦，轮间零重叠零空洞。
- **连续覆盖**：`lastEnd` 记录上轮实际打到页面的窗口终点（onRange 在时间步执行时回填）；跳轮/失败后下一轮从 lastEnd 续读补漏（向前钳制防重复、超 30 分钟封顶）。
- 与试运行共用 consoleGate 互斥；忙则记"跳过"轮。
- **研判方式**：`alerts`（SIEM 告警，失陷主机判定）/ `generic`（任意表格：关键发现/需关注条目/数据质量）/ `none`（仅下载）。表格转 markdown 管道表（单元格 120 字符、6 万字符预算自适应缩行、截断注明）。
- **通知**：四档触发（确认失陷主机|关键结论时（默认）/ 需关注时 / 每轮 / 不通知）× 三通道（IM bot——最近会话下拉、dest 按 `platform:chatId`（QQ 群 `qq:chatType:chatId`）；邮件——发件账户读设置 + 收件人；系统通知）。投递失败记 NotifyError 不失败轮次。

## 10. 机器可读结论（verdict）

研判报告尾部必须带 ```json 块（取**最后一个**围栏解析）：

```json
{"失陷主机": ["10.0.0.5"], "需关注告警数": 3, "最高等级": "high", "需通知": true, "通知理由": "…"}
{"关键发现": ["…"], "需关注条数": 2, …}   ← generic 档
```

两套键归并到同一访问器（`findings()/attention()`）；失陷主机/关键发现**宁缺勿滥**（无确凿证据给空数组）。

## 11. UI 契约

- **浏览器面板四页签**：交互 / 录制 / 技能 / **巡检**（雷达图标；运行中带脉冲活点；状态头+配置表单+轮次日志；技能页签内仍可按技能快捷配置，启动后跳转巡检页）。
- **编辑器**：结构化⇄源码双模式（lossy 护栏）；时间类参数快捷片+换算预览；每步"控制"输入；试运行终态携带 downloads → 下载卡 + 首个表格文件自动 AI 研判（Markdown 渲染、可重试）。
- 轮次卡徽标：失陷×N / 需关注×N+等级 / 已通知渠道 / 补漏窗口说明。

## 12. 已知限制与预留

- 单元格内禁 `|`；无流级 harness frontmatter（步骤级即可，frontmatter 加键会触发编辑器 lossy 护栏）。
- `视觉` 失败策略、`vision=` 锚点、`wait see:` 视觉断言、失败截图的 VLM 自动诊断：**已留挂钩未接线**（接入顺序：失败诊断 → 锚点兜底 → 视觉断言；视觉永不进热路径，巡检默认禁用视觉兜底）。
- `.xls` 老格式不支持（提示改导 xlsx/csv）；ExtJS 动态 id（button-3458 类）不可写进步骤。

## 13. 验证清单（对应测试）

解析：`TestParseFlowTable*`（4/5 列、evaluate 单行 JS 单元格、控制列规划期报错）、`TestParseStepControl`（中英键/非法值拒识）、前端 skill-doc 回环（5 列、老行字节不变）。
语义：`TestConsoleWaitStable*`（占位符 4s/26s 不放行、流式边沿判稳、静态两档兜底）、`TestWatchAlignedWindow*` / `TestNextWatchFire`（整分窗口/补漏/封顶/刻度）、`TestParseAlertVerdict` / `TestWatchNotifyShould`（末块优先、双 schema 归并、四档门槛）、`TestResolveTimeRange*` / `TestFlowSubstLazyTimeRange`（短语/拒识/惰性换算/缓存）。
