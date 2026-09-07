# 九场景能力地图（SCENARIO_CAPABILITY_MAP）

> SCENARIO_SPEC K1 的落地。**单一权威视图**：每场景四列——技能入口｜工具（场景内子集）｜模板/面板｜MCP。
> 数据与 `netdev-help` 技能场景矩阵、`docs/NETDEV_HELP.md` 同源；三处改动必须同步（K5-1 登记制）。
> 标注：`⟟M2/M3/M4`=SCENARIO_SPEC 对应里程碑的规划项；`（站点）`=部署站点自备的用户技能。

| 场景 | 技能入口 | 工具（场景内子集） | 模板/面板 | MCP |
|---|---|---|---|---|
| **① 数通运维**（含⑨智算） | netdev-diag-auto（排障 sweep 子代理）、netdev-config-vault ⟟M1（配置版本化/恢复回退）、netdev-draft ⟟M4（命令起草） | netdev_exec / netdev_devices / netdev_fanout / netdev_snmp / netdev_topology / netdev_locate / netdev_netconf / netdev_backup / netdev_propose；GPU 档 ⟟M3（nvidia-smi/npu-smi 入读表+triage GPU 档） | 割接大屏（precheck 红绿灯 ⟟M3）、提案中心、总览巡检卡 | 封印（见下） |
| **② 漏洞管理** | netdev-seccheck-auto（入口=清单\|套餐） | netdev_cve_match / netdev_baseline / netdev_exec / netdev_fanout | 蓝队核查页卡、发现中心（fix 结构化 ⟟M2）；安全工作台 CVE 导入 | 封印 |
| **③ 浏览器技能**（入口在**办公界面**） | browser-IT-ops（站点）、browser-cybersituational-awareness（站点）——办公界面索引/调用 | browser_* 全组（隐藏，子代理+browser-flow 执行器，**仅 cowork 注册**） | **办公**浏览器工作台（2026-09-06 迁入：录制/试运行/技能库/巡检） | 封印 |
| **④ 告警联动**（夜班值守） | —（watch 机制）；研判 rubric（netdev-help"告警研判"节） | （经办公界面的浏览器技能间接） | **办公**浏览器工作台巡检页签（定时轮+研判）；DangerScore 分级+夜班 ⟟M2 | 封印 |
| **⑤ 日志审计**（项目维度） | netdev-seccheck-auto（入口=套餐，上线前审计） | netdev_log_read / netdev_log_search / netdev_fanout / netdev_db_query | 日志工作台（多源合并/IOC）；「项目审计」页签+风险清单 ⟟M3 | 封印 |
| **⑥ 办公集成×运维联动** | ops-weekly ⟟M4（运维周报模板，读简报）；ppt-auto 及办公技能族（cowork 侧） | （cowork 域办公工具：doc/excel/email/im） | 运维简报 ⟟M1（Briefing 生成落 briefings/）；告警邮件通报 ⟟M1 | 封印（netdev 侧）；cowork 侧按白名单 |
| **⑦ 后台文员自动化** | 文员三模板 ⟟M4（form-submit/data-export/expense-submit） | browser_* 全组（cowork 子代理） | **办公**浏览器工作台（=③同一面板，零对话一键） | cowork 侧按白名单 |
| **⑧ LinkPeer 移动端** | — | IM bridge `/netdev` 命令族（发现/详情/ack/审批） | 手机 App（独立 Flutter 仓 M4+）；手机审批深链 ⟟停车场 | — |
| **⑨ 智算纳管** | （并入①数通运维） | netdev_k8s（已就绪）；GPU 只读命令+triage GPU 档 ⟟M3；智算平台巡检=浏览器技能（现场录制 ⟟停车场） | 设备卡 GPU 徽标 ⟟M3 | 封印 |

## §MCP（全场景统一）
netdev 界面：**封印**——PluginAllowlist 钉死且白名单为空（防 write/exec MCP 绕过 tool_scope 封印），CodeGraph 双保险隐藏。决策全文与解锁通道草稿见 `SCENARIO_SPEC §MCP 治理决策`。cowork 界面：按用户白名单正常加载。

## 维护规则（K5-1 登记制）
1. 新增/修改运维技能、工具、面板时，**必须**同步本表对应行 + netdev-help 场景矩阵 + docs/NETDEV_HELP.md。
2. 新能力进表时标注里程碑标签（⟟Mx）或"已就绪"；从停车场重启时移标注。
3. 每个格子禁止留空：无能力显式写"无"或"⟟停车场"。
