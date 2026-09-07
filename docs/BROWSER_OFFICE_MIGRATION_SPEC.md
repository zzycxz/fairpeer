# 浏览器归属办公迁移 SPEC（BROWSER_OFFICE_MIGRATION_SPEC v1.0）

> 2026-09-06 用户定稿并当日落地。记录浏览器能力（工具）、入口（面板）、技能（站点技能）三位一体从运维界面迁至办公界面的完整决策链、迁移映射、不变量与验证——后续任何浏览器能力的增删以此为准绳。

## 一、决策记录（Decision Record）

| 日期 | 决策 | 状态 |
|---|---|---|
| 2026-08 下旬 | 浏览器控制台因态势感知平台驱动需求建于**运维界面**（录制/锚点/AX 树对着该平台开发） | 已执行（历史路径依赖） |
| 2026-09-04 | browser-auto 进 netdev 白名单（"通用兜底"） | **已被本 spec 推翻** |
| 2026-09-05 | S7 技能库面板下沉办公（文员零 AI 门槛）——出现双入口 | 已执行（本 spec 收敛） |
| 2026-09-06 | **浏览器能力/面板/技能三位一体归办公**；运维界面回归网络设备作业纯粹身份 | ✅ 本 spec |

**定稿依据**：① 浏览器自动化本质是 GUI 操作，与办公模式的 Computer-Use 身份同源（desktop-auto/browser-auto 原生地）；② 运维界面身份是网络设备作业（SSH/SNMP/NETCONF 只读通道、提案闸、割接），与浏览器无关；③ S7 下沉后同一能力双入口，用户实测困惑（"办公技能库是干什么的"）——单家原则被破坏；④ 运维真正需要的浏览器场景（站点技能调用）不依赖面板位置（run_skill / 定时巡检皆与界面解耦）。

## 二、迁移映射（旧 → 新）

| 对象 | 旧位置 | 新位置 |
|---|---|---|
| 浏览器工作台（BrowserWorkbench：交互/记录/技能/巡检 + 观察窗） | NetDevLayout 第五工作台（bench="browser"） | CoWorkLayout「浏览器」页签（activePanel="skills"，常驻挂载 hidden 切换，状态保留） |
| 侧栏入口 | 运维侧栏 MousePointerClick 按钮 | 办公侧栏「浏览器」按钮（原「技能库」更名） |
| 工作台切换 chip | 运维 bench 条 chip | （无——办公页签天然承担） |
| dock 页签 | 运维 dock "browser" 页签（BrowserConsolePanel） | （撤除；工作台内含同面板） |
| 事件路由 fairpeer:netdev-bench "browser" | NetDevLayout 监听→openBrowserBench | **CoWorkLayout 监听**→切浏览器页签（面板"打开观察窗"/编辑器"去重录"按钮的落点） |
| 镜像隐藏信号 netdev-bench-changed | NetDevLayout bench 切换时派发 | CoWorkLayout activePanel 切换时派发（skills↔browser，一屏一画面） |
| 技能索引（domain: browser-ops） | netdev+cowork 双索引（SkillDomains） | **仅 cowork 索引**（netdev SkillDomains=["netdev"]） |
| browser-auto 白名单 | netdev EnabledSkills 含 | **移除**（仅 cowork） |
| 浏览器工具组 + browser-flow 执行器注册 | boot.go cowork‖netdev 分支 | **仅 cowork** 分支 |
| 文案/文档 | 运维侧 ndv.bench.browser、ndv.tab.browser | 移除；cowork.panel.skills 更名「浏览器」；能力地图③④⑦行/netdev-help 矩阵/spec 九句⑤⑥/browser-ops-guide 顶部迁址注记 |

## 三、不变量（迁移不改变的东西）

1. **单源组件**：BrowserWorkbench/BrowserConsolePanel 一份代码，办公挂载是同一实例——禁止再复制出"办公版浏览器面板"。
2. **run_skill 按名调用不受索引限制**：域折叠只影响提示词索引可见性；`run_skill("browser-IT-ops", …)` 在任何 profile 仍可执行（既有的"索引折叠≠硬禁用"双闸设计）。运维对话里**点名**技能名仍能跑——只是模型不再主动路由。
3. **持久浏览器会话全局共享**：受控浏览器（登录态/页卡/cookie）是桌面级单例，跨 profile 共用；迁移后办公/运维看到的还是同一个浏览器。
4. **定时巡检后端不动**：browser_watch.json 全局存储、轮次执行（runConsoleSteps）、危险评分 rubric、通知出口（netdev notify：webhook/SMTP/IM）、夜班窗口（[netdev.alerts]）全部留在原位——巡检**配置界面**随面板在办公，**判定与通知**仍在运维后端。
5. **站点技能文件不搬家**：~/.fairpeer/skills/browser-*（domain: browser-ops）原地不动，可见性由 SkillDomains 决定。

## 四、用户可见的行为变化（须知）

| 场景 | 迁移前 | 迁移后 |
|---|---|---|
| 手动驱动/录制浏览器技能 | 运维界面浏览器工作台 | **办公界面**「浏览器」页签 |
| 对话说"查哈尔滨池的告警"（运维对话） | 模型索引可见 browser-IT-ops，自动路由 | 索引不可见，**不再自动路由**——正确位置是办公对话，或运维对话点名 `/browser-IT-ops 问题=…` |
| 定时告警巡检的配置 | 运维浏览器面板巡检页签 | 办公浏览器工作台巡检页签（轮次/评分/通知行为不变） |
| 态势处置骨架（browser-soc-response）录制/唤醒 | 运维浏览器面板 | 办公浏览器工作台 |

## 五、验证与守护

- **测试钉住**：`TestNetDevExcludesBrowserDomain`（netdev 无 browser-ops 域、无 browser-auto 白名单——原 `TestNetDevWhitelistsBrowserAuto` 反转）；`TestBuiltinProfileSkillDomains` 契约更新为 cowork 独占 browser-ops。
- **九句场景路由回归**（SCENARIO_SPEC 总验收）⑤查告警/⑥导出研判两句的预期入口改为**办公界面**。
- 本批全量验证：config/boot 包测试、双模块 build、tsc、npm test 43/43 绿。

## 六、遗留与后续

- **用户习惯引导**：运维同事的肌肉记忆入口已变——运维侧未留跳转入口（用户定稿"整个浏览器功能都不应放在运维界面"）；若后续反馈强烈，可加"跳办公浏览器"轻量导航（一行事件派发即可，不做重复挂载）。
- **回滚**：单次 revert 提交即可整体回退（决策记录保留）；但 2026-09-04 决策已被本 spec 取代，回滚需连同白名单测试一起回。

## 六之二、迁移完整性复核（两轮：抽样 6 项 + 分层全量 4 层）

用户两次质询触发：第一轮抽样清查 6 项（5 修 1 确认），第二轮分层全量清查（L1 前端 / L2 后端 / L3 测试 / L4 文档）又发现 4 项——**其中 1 项是功能级回归**。完整清单：

### 第一轮（抽样）

| # | 残留 | 处置 |
|---|---|---|
| 1 | benchParam 返回类型含 "browser" | ✅ 收紧 |
| 2 | 3 个死 locale 键 × 双端 | ✅ 移除 |
| 3 | 面板"打开观察窗"/"去重录"事件无监听 | ✅ CoWorkLayout 接管 |
| 4 | 办公 dock 镜像与工作台双画面 | ✅ bench-changed 驱动临时收起 |
| 5 | netdev 手册 browser 篇目 | ✅ 保留为跨域参考并标注 |
| 6 | 浏览器→发现 lifecycle 桥 | ✅ 确认 desktop 级无需改 |

### 第二轮（分层全量）

| # | 层 | 发现 | 级别 | 处置 |
|---|---|---|---|---|
| 7 | L2 后端 | **boot.go 收窄把 netdev 的 browser-flow 执行器一并移除**——站点技能（browser-IT-ops 等）在运维对话按名 run_skill 会报 "no flow runner"，违反本 SPEC 不变量 2 | **功能回归** | ✅ 分层注册：netdev 保留 SetFlowRunner+浏览器路径（RunBrowserFlow 直调 builtin 结构体、不经 Registry），工具 schema 与 browser-auto 子代理面仍仅 cowork；新增 `TestNetDevKeepsBrowserFlowRunner` 守卫 + `skill.RunFlow` 导出 |
| 8 | L3 测试 | `TestBuildSkillDomainFolding` 仍断言 netdev 覆盖 browser-ops 域（与域收窄矛盾，靠旧断言侥幸未爆） | 契约漂移 | ✅ 反转：netdev 下 browser-ops 技能必须折叠 |
| 9 | L1 文案 | skillTemplates siem-watch 正文"在运维浏览器…登录"/"在运维页签手动运行"指向已不存在的入口；brc.tgCommon"常用运维操作"；ndv.man.browser 篇名 | 用户误导 | ✅ 全部改为办公提法；模板运行环境说明合并为"工作台与定时任务都在办公 profile" |
| 10 | L4 文档 | docs/NETDEV_HELP.md 场景表浏览器行未标注归属；SKILL_ARCHITECTURE_SPEC §六运维白名单行仍记载 09-04"通用兜底"为现行决策 | 文档失真 | ✅ 帮助表标注办公入口+办公注册；spec 运维行改为决策推翻记录（划线标注） |

**逐文件扫描确认无残留**（第二轮过筛零命中或仅合法引用）：前端 App/AppChrome/TabBar/DockTabs/Settings/Composer/Capabilities/Markdown/PreviewPane/main（命中均为 webview/browserslist/编码等无关词）；镜像焦点逻辑（App.tsx coworkActive 门控——浏览器活动只在办公聚焦 dock，与迁移方向一致）；命令面板（无浏览器条目）；后端 mobilebridge/watch/notify（无界面耦合）；fairpeer:// 深链与 ?dock= 参数；brc.* 文案全量（无"运维面板"提法残留）。

| 11 | 用户实测 | **办公浏览器右栏空白**——控制台面板（交互/记录/技能库/巡检）在原布局住 dock browser 页签，迁移未跟迁；办公 dock 该页签仍是旧纯镜像面板且被防双画面逻辑压制 | **功能缺失** | ✅ dock browser 页签改挂 BrowserConsolePanel（主区=大镜像+F12、右栏=操作面板的完整布局）；进工作台自动开 dock 切 browser 页签；撤销页签压制（双画面由面板内联镜像收起机制防） |

| 12 | 运行链路 | 控制台面板"交给 AI"（onInsertComposer）断链——CoWorkLayout/CoworkDock 均未接此 prop，办公面板按钮灰死 | 功能缺失 | ✅ 三级贯通 App→CoWorkLayout→CoworkDock→BrowserConsolePanel |
| 13 | 运行链路 | 进工作台只切 dock 页签不开 dock 本体（requestBrowserMirrorFocus 不开 dock）——右栏仍可能不可见 | UX 缺陷 | ✅ 入口两处补 onDockOpen()；Esc 退出工作台回任务中心（对齐原运维 Esc 语义） |
| 14 | 死代码 | BrowserMirrorPanel（旧 dock 纯镜像面板）无引用残留 | 清理 | ✅ 删除 |
| 15 | 复核确认 | netdev.css 全局加载（两布局静态 import，CSS 全局生效）——办公下 ndv-wb/ndv-brc 样式正常；backend browser 桥无 profile 门控；mirror suppress（App）与 coworkActive 门控方向一致 | 无需改 | — |

### 方法论沉淀

迁移类改动的验收三步（后续迁移照此执行）：
1. **分层 grep**：前端组件/事件/locale、后端注册/桥、测试断言、文档镜像四层各自全量扫描，不抽样；
2. **不变量反证**：SPEC 写下的每条不变量配一条守卫测试（本轮不变量 2 无守卫即漏掉功能回归）；
3. **文案指针清零**：所有"运维面板→浏览器/运维浏览器/ops browser tab"类指向性文案 grep 清零（用户会照着走）。

## 七、关联文档

- `docs/SCENARIO_SPEC.md`（九句路由 ⑤⑥ 已归办公）
- `docs/SCENARIO_CAPABILITY_MAP.md`（③④⑦ 行面板列指办公工作台）
- `docs/browser-ops-guide.md`（顶部迁址注记）
- CHANGELOG [Unreleased]「浏览器归属办公」条目
