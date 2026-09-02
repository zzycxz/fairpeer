# UI 引导补全 Spec（G1 批）

> 背景：界面引导审计结论——引导骨架（三步上手、场景导引 14 卡、总览 jump 体系、页签分组、
> 告警 5 步向导）质量不错，但存在：一处空状态渲染 bug、场景卡无分组平铺 14 张、
> 应用内无帮助入口（NETDEV_HELP/browser-ops-guide/NETDEV_USAGE 三份好文档零入口）、
> 部分面板空状态无下一步动作。本批只做前端，不触后端。
>
> 关联：`docs/PENLAB_CAPABILITY_GAPS.md`（靶场场景）；`docs/NETDEV_USAGE.md` §四。

## 改动清单

### G1-0 空状态渲染 bug（P0）

`NetDevLayout.tsx` 设备空态 `ndv.dev.emptyDesc` 一处 `tt(...)` 未包 `{}`，
用户看到字面量。修一行。

### G1-1 场景导引分组 + 靶场闭环卡（P1）

- ScenarioHub 14 张卡按既有 a/b 前缀分两组渲染，各加组头：
  - A 组 a1–a6「日常保障」（告警/诊断/巡检/日志/基线/漂移）
  - B 组 b1–b8「安全与排障」（定位/主机排查/弱口令/CVE/分诊/关联/报告/K8s）
- B 组头部新增一张 **靶场安全闭环** 主卡：desc 点明「识别 → 修复 → 复核」
  顺序（基线/CVE/弱口令 → Finding → 提案 → 观察期复核），动作直达手册页签
  （G1-2）。闭环各环节的单独入口已有（b3/b4/b5/a5），本卡补的是"路线"视角。

### G1-2 应用内手册页签（P1）

- 新 dock 页签 `manual`（「手册」，归档组尾部，BookOpen 图标）。
- 内容 = 三份既有文档，`?raw` 前端打包嵌入（不新增后端桥）：
  - `NETDEV_USAGE.md`（使用地图·六条主流程）· 默认选中
  - `NETDEV_HELP.md`（求助指引速查）
  - `browser-ops-guide.md`（浏览器运维工作台）
- 副本放 `src/guides/`，配 vitest **漂移测试**：逐字节对比仓库 `docs/` 原件，
  原件更新不同步即测试失败（防文档腐化）。
- 渲染用现成 `react-markdown`；`vite-env.d.ts` 补 `vite/client` 引用支持 `?raw`。

### G1-3 空状态统一（P2）

findings / proposals / audit 三处空态统一为「标题 + 一句说明 + 一个主按钮」
（向设备空态与 GettingStarted 看齐）：

| 面板 | 按钮动作 |
|---|---|
| findings | 运行安全基线（复用 onBaseline 同款 runBaseline） |
| proposals | 去对话起草提案（onInsertComposer 填提示词 + 切 live） |
| audit | 立即巡检（复用 runInspection） |

## 不做（本批外）

- engagement 信封向导、scopes 就地扩围引导（依赖后端改动，归 PENLAB P0-1/P2 批）
- 晋升流程首用提示（依赖待确认区交互改造）
- 交互式 tour / 高亮气泡

## 验收

1. `cd desktop/frontend && npx tsc --noEmit && npx vitest run` 通过
2. 有项目过滤且设备空时，空态文案正常渲染（非 `tt(...)` 字面量）
3. 总览场景中心两组带组头；靶场闭环卡可跳手册
4. 手册页签三份文档可切换渲染；改 `docs/NETDEV_USAGE.md` 不同步副本时漂移测试红
5. findings/proposals/audit 空态各有可点的下一步按钮；中英文案齐全
