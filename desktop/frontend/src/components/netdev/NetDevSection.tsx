import { useCallback, useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import { useConfirm } from "../../lib/confirm";
import { useToast } from "../../lib/toast";
import type {
  NetDevAlertRuleView,
  NetDevDBSourceView,
  NetDevPresetView,
  NetDevProjectView,
  NetDevSettingsView,
  NetDevSSHImportCandidate,
} from "../../lib/types";

// NetDevSection is the 运维 settings tab: device/hop inventory (persisted to
// the USER config — the [netdev] section is globally pinned), credentials
// (secret store, never in TOML), scan scopes, and the audit tail. The agent
// itself has no tool to edit any of this — inventory changes are human-only.
//
// 2026-08-27 二级化：内容按 settings-subtabs（Models/MCP/Skills 同款）分四组
// —— 设备与跳板 / 护栏与读表 / 站点与自动化 / 高级；实体编辑（设备、跳板、
// 项目、诊断组合、扫描导入）全部走弹框，列表行只留 只读摘要 + 编辑/删除。

const VENDORS = ["huawei", "cisco", "zte", "vmware", "redfish", "linux", "windows", "snmp"];
const OSES: Record<string, string[]> = {
  huawei: ["vrp8", "vrp5"],
  cisco: ["ios", "iosxe"],
  zte: ["zxr10"],
  vmware: ["esxi8", "esxi7"],
  redfish: ["bmc"],
  linux: ["ubuntu", "debian", "centos", "rocky", ""],
  windows: ["win11", "ws2022", ""],
  snmp: ["v2c"],
};
const READ_VENDORS = ["huawei", "cisco", "zte"];

type EditDevice = NetDevSettingsView["devices"][number];
type EditHop = NetDevSettingsView["hops"][number];
type SubTab = "inventory" | "guardrails" | "sites" | "advanced";

const emptyDevice = (): EditDevice => ({
  name: "", vendor: "huawei", os: "vrp8", model: "", address: "", port: 22,
  via: [], group: "", username: "", passwordEnv: "", passwordSet: false,
  identityFile: "", encoding: "auto", allowTelnet: false, password: "", logPaths: [], protocols: [], snmpVersion: "", snmpCommunityEnv: "", snmpCommunitySet: false, snmpCommunity: "",
});

const emptyHop = (): EditHop => ({
  name: "", host: "", port: 22, user: "", passwordEnv: "", passwordSet: false,
  proxyJump: "", password: "",
});

export function NetDevSection() {
  const confirm = useConfirm();
  const { showToast } = useToast();
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [view, setView] = useState<NetDevSettingsView>({ enabled: false, networkName: "", devices: [], hops: [], groups: [], auditRetention: "", scopes: [], guardConfirmEach: false, guardTurnBudget: 0, guardAllowedGroups: [], extraRead: {}, projects: [], presets: [], inspectionInterval: "", backupInterval: "", dbSources: [], pollIntervalSeconds: 0, alertRules: [], syslogPort: 0, defaultMode: "", maxSessionsPerDevice: 0, discoveryRate: 0, discoveryMode: "", probeFallback: "", groupDefs: [] });
  const [sub, setSub] = useState<SubTab>("inventory");
  const [editingDevice, setEditingDevice] = useState<EditDevice | null>(null);
  const [editingDB, setEditingDB] = useState<NetDevDBSourceView | null>(null);
  const [editingRule, setEditingRule] = useState<NetDevAlertRuleView | null>(null);
  const [editingHop, setEditingHop] = useState<EditHop | null>(null);
  const [editingProject, setEditingProject] = useState<{ draft: NetDevProjectView; index: number } | null>(null);
  const [editingPreset, setEditingPreset] = useState<{ draft: NetDevPresetView; index: number } | null>(null);
  const [sshCandidates, setSSHCandidates] = useState<NetDevSSHImportCandidate[]>([]);
  const [readAdd, setReadAdd] = useState<Record<string, string>>({});
  const [scanOpen, setScanOpen] = useState(false);
  const [scanXml, setScanXml] = useState("");
  const [scanBusy, setScanBusy] = useState(false);

  const reload = useCallback(async () => {
    try {
      const v = await app.NetDevSettings();
      setView({
        ...v,
        devices: v.devices ?? [],
        hops: v.hops ?? [],
        groups: v.groups ?? [],
        scopes: v.scopes ?? [],
        projects: v.projects ?? [],
        presets: v.presets ?? [],
      });
      setErr("");
    } catch (e) {
      setErr(String(e));
    } finally {
      setLoaded(true);
    }
  }, []);

  useEffect(() => { void reload(); }, [reload]);

  const save = useCallback(async (v: NetDevSettingsView) => {
    setBusy(true);
    try {
      await app.SetNetDevSettings(v);
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }, [reload]);

  const patch = (p: Partial<NetDevSettingsView>) => setView(v => ({ ...v, ...p }));

  // First-device flow: connect → TOFU confirm (fingerprint shown verbatim) →
  // trust → retest. Any other failure is reported as text.
  const [testing, setTesting] = useState(false);
  const testConnection = useCallback(async (device: string) => {
    setTesting(true);
    try {
      let r = await app.NetDevTestConnection(device);
      if (r.status === "unknown-host-key") {
        const ok = await confirm({
          title: "UNTRUSTED HOST KEY",
          message: `首次连接 ${device}（${r.host}）\n主机密钥指纹：\n  ${r.keyType}  ${r.fingerprint}\n\n确认信任此密钥？（确认后写入本机 known_hosts）`,
          danger: true
        });
        if (!ok) { setErr("[SYS] KEY REJECTED"); return; }
        if (!r.fingerprint) { setErr("[SYS] INTERNAL ERROR: NO FINGERPRINT"); return; }
        await app.NetDevTrustHostKey(r.fingerprint);
        r = await app.NetDevTestConnection(device);
      }
      setErr(r.status === "ok" ? "[SYS] TARGET VERIFIED (VTY SESSION OPEN)" : `测试失败（${r.status}）：${r.detail ?? ""}`);
    } catch (e) {
      setErr(String(e));
    } finally {
      setTesting(false);
    }
  }, []);

  if (!loaded) return <div className="mem-hint">…</div>;

  const SUBTABS: { key: SubTab; label: string; count?: number }[] = [
    { key: "inventory", label: "设备与跳板", count: view.devices.length + view.hops.length },
    { key: "guardrails", label: "护栏与读表" },
    { key: "sites", label: "站点与自动化" },
    { key: "advanced", label: "高级" },
  ];

  return (
    <div className="settings-page settings-page--form">
      <div className="settings-page__header">
        <h2 className="settings-page__title">运维</h2>
        <p className="settings-page__desc">
          设备清单与凭证存于用户全局配置（项目级 fairpeer.toml 注入无效）；密码只写入加密密钥库，绝不进 TOML。
          诊断能力结构性只读：写/危险命令一律拒执行并落审计。
        </p>
      </div>

      {err && <div className="banner banner--error" style={{ marginBottom: 8 }}>{err}</div>}

      <div className="optional-module__controls optional-module__controls--inline" style={{ marginBottom: 12 }}>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <input type="checkbox" checked={view.enabled} onChange={e => patch({ enabled: e.target.checked })} />
          启用运维（netdev）能力
        </label>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
          网络名称
          <input className="mem-input" style={{ width: 180 }} value={view.networkName ?? ""} placeholder="如：总部生产网" onChange={e => patch({ networkName: e.target.value })} />
        </label>
        <span className="btn btn--primary btn--small" role="button" onClick={() => void save(view)}>{busy ? "保存中…" : "保存"}</span>
      </div>

      <div className="settings-subtabs">
        {SUBTABS.map(t => (
          <button
            key={t.key}
            type="button"
            className={`settings-subtab${sub === t.key ? " settings-subtab--active" : ""}`}
            aria-selected={sub === t.key}
            onClick={() => setSub(t.key)}
          >
            {t.label}
            {typeof t.count === "number" && t.count > 0 ? <small>{t.count}</small> : null}
          </button>
        ))}
      </div>

      {/* ── 子页签 1：设备与跳板 ─────────────────────────────────────────── */}
      {sub === "inventory" && (
        <>
          <Section
            title={`设备（${view.devices.length}）`}
            desc="设备是运维世界的一切入口：未录入的地址 AI 不可见、不可连。"
            actions={
              <>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice(emptyDevice())}>+ 添加设备</span>
                <span
                  className="btn btn--secondary btn--small" role="button"
                  onClick={async () => {
                    try {
                      const c = await app.NetDevSSHImportCandidates();
                      setSSHCandidates(c ?? []);
                    } catch (e) { setErr(String(e)); }
                  }}
                >从 ~/.ssh/config 导入</span>
              </>
            }
          >
            {sshCandidates.length > 0 && (
              <div className="mem-hint" style={{ marginBottom: 6 }}>
                {sshCandidates.slice(0, 12).map(c => (
                  <span key={c.alias}
                    className="btn btn--secondary btn--small" role="button" style={{ marginRight: 6 }}
                    onClick={() => setEditingDevice({
                      ...emptyDevice(),
                      name: c.alias, address: c.host || c.alias, username: c.user || "",
                      port: c.port || 22,
                    })}
                  >{c.alias}</span>
                ))}
              </div>
            )}
            {view.devices.length === 0 && (
              <div className="mem-hint">还没有设备。添加设备或从 ~/.ssh/config 导入，测试连接通过后即可在运维页看到它。</div>
            )}
            {view.devices.length > 0 && (
              <table className="mem-hint" style={{ width: "100%", borderCollapse: "collapse" }}>
                <thead>
                  <tr style={{ textAlign: "left" }}><th>名称</th><th>厂商/OS</th><th>地址</th><th>路由</th><th>凭证</th><th /></tr>
                </thead>
                <tbody>
                  {view.devices.map(d => (
                    <tr key={d.name}>
                      <td>{d.name}{d.group ? `（${d.group}）` : ""}</td>
                      <td>{d.vendor}/{d.os}</td>
                      <td>{d.address}{d.port && d.port !== 22 ? `:${d.port}` : ""}</td>
                      <td>{(d.via ?? []).join("→") || "直连"}</td>
                      <td>{d.passwordSet ? "✓ 已设" : "✗ 未设"}</td>
                      <td>
                        <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice({ ...d, password: "" })}>编辑</span>{" "}
                        <span className="btn btn--secondary btn--small" role="button" title="删除"
                          onClick={async () => { if (await confirm({ title: "DELETE DEVICE", message: `删除设备 ${d.name}？（不影响已存凭证）`, danger: true })) void save({ ...view, devices: view.devices.filter(x => x.name !== d.name) }); }}>×</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Section>

          <Section
            title={`跳板/堡垒机（${view.hops.length}）`}
            desc="跳板只能人工注册——探测结果永远不会自动晋升为路由。"
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop(emptyHop())}>+ 添加跳板</span>}
          >
            {view.hops.length === 0 && <div className="mem-hint">暂无跳板。大多数设备直连即可——只有需要先登录堡垒机再跳转时才配置。</div>}
            {view.hops.map(h => (
              <div key={h.name} className="mem-hint" style={{ display: "flex", gap: 8, marginTop: 4, alignItems: "center" }}>
                <span style={{ minWidth: 160 }}>{h.name} → {h.host}{h.proxyJump ? `（经 ${h.proxyJump}）` : ""}</span>
                <span>{h.passwordSet ? "✓ 凭证已设" : "✗ 未设"}</span>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop({ ...h, password: "" })}>编辑</span>
                <span className="btn btn--secondary btn--small" role="button" title="删除"
                  onClick={async () => { if (await confirm({ title: "DELETE HOP", message: `删除跳板 ${h.name}？`, danger: true })) void save({ ...view, hops: view.hops.filter(x => x.name !== h.name) }); }}>×</span>
              </div>
            ))}
          </Section>
        </>
      )}

      {/* ── 子页签 2：护栏与读表 ────────────────────────────────────────── */}
      {sub === "guardrails" && (
        <>
          <Section title="护栏" desc="控制到每一次询问与每一条工具命令。">
            <div style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <input
                  type="checkbox"
                  checked={!!view.guardConfirmEach}
                  onChange={e => patch({ guardConfirmEach: e.target.checked })}
                />
                每条命令确认：netdev_exec / netdev_netconf 执行前弹审批卡（优先级压过全自动模式，"记住允许"也无效）
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                每轮命令预算
                <input
                  className="mem-input" style={{ width: 70 }} type="number" min={0}
                  value={view.guardTurnBudget ?? 0}
                  onChange={e => patch({ guardTurnBudget: Math.max(0, Number(e.target.value) || 0) })}
                />
                条（0 = 不限；每次你发送消息预算重置，超出后 agent 收到提醒并停下汇总）
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                设备组作用域
                <input
                  className="mem-input" style={{ width: "50%" }}
                  value={(view.guardAllowedGroups ?? []).join(", ")}
                  placeholder="留空 = 全部组；例：核心, 汇聚"
                  onChange={e => patch({ guardAllowedGroups: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
                />
              </label>
              <div style={{ opacity: 0.6, fontSize: 11.5 }}>
                作用域非空时，范围外的设备对 AI 完全不可见（netdev_devices 列表也过滤）——在第一个 token 花出去之前就完成控制。脱敏提醒与拒绝提醒默认常开，无需配置。
              </div>
            </div>
          </Section>

          <Section title="探测范围白名单" desc="CIDR 列表（逗号分隔）；范围外探测一律拒绝——永不关闭的护栏。">
            <input
              className="mem-input" style={{ width: "100%" }}
              value={(view.scopes ?? []).join(", ")}
              placeholder="例：10.30.0.0/16, 10.31.0.0/16"
              onChange={e => patch({ scopes: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
            />
            <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 8, fontSize: 12, alignItems: "center" }}>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                发现模式
                <select className="mem-select" value={view.discoveryMode || "auto"}
                  onChange={e => patch({ discoveryMode: e.target.value })}>
                  <option value="auto">auto</option>
                  <option value="tunnel">tunnel</option>
                  <option value="probe">probe</option>
                </select>
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                并发上限
                <input className="mem-input" type="number" style={{ width: 70 }} placeholder="0=默认"
                  value={view.discoveryRate ?? 0}
                  onChange={e => patch({ discoveryRate: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
            </div>
          </Section>

          <Section title="读表扩展" desc="用户教会 AI 识别更多只读命令——模型永远不能自我声明。">
            {READ_VENDORS.map(vendor => {
              const list = view.extraRead?.[vendor] ?? [];
              const draft = readAdd[vendor] ?? "";
              return (
                <div key={vendor} style={{ display: "flex", gap: 6, alignItems: "center", marginBottom: 6, flexWrap: "wrap" }}>
                  <span style={{ minWidth: 60, fontWeight: 600 }}>{vendor}</span>
                  {list.map(cmd => (
                    <span key={cmd} className="btn btn--secondary btn--small" role="button" title="点击移除"
                      onClick={() => patch({ extraRead: { ...view.extraRead, [vendor]: list.filter(c => c !== cmd) } })}>
                      {cmd} ×
                    </span>
                  ))}
                  <input
                    className="mem-input" style={{ width: 200 }} placeholder="只读命令，如 display health"
                    value={draft}
                    onChange={e => setReadAdd(r => ({ ...r, [vendor]: e.target.value }))}
                    onKeyDown={e => {
                      if (e.key !== "Enter" || !draft.trim()) return;
                      patch({ extraRead: { ...view.extraRead, [vendor]: [...list, draft.trim()] } });
                      setReadAdd(r => ({ ...r, [vendor]: "" }));
                    }}
                  />
                  <span className="btn btn--secondary btn--small" role="button"
                    onClick={() => {
                      if (!draft.trim()) return;
                      patch({ extraRead: { ...view.extraRead, [vendor]: [...list, draft.trim()] } });
                      setReadAdd(r => ({ ...r, [vendor]: "" }));
                    }}>+ 添加</span>
                </div>
              );
            })}
            <div style={{ opacity: 0.6, fontSize: 11.5 }}>
              对话中被拒绝的未知命令也会在设备卡上出现「允许此命令」一键加入。扩展只让更多命令「可读」，永远不可能放开写操作。
            </div>
          </Section>
        </>
      )}

      {/* ── 子页签 3：站点与自动化 ──────────────────────────────────────── */}
      {sub === "sites" && (
        <>
          <Section
            title={`项目（${(view.projects ?? []).length}）`}
            desc="站点级作用域（一个机房/园区/客户网络）——运维页标题栏可快速切换。"
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingProject({ draft: { name: "", groups: [], note: "" }, index: -1 })}>+ 新建项目</span>}
          >
            {(view.projects ?? []).length === 0 && <div className="mem-hint">暂无项目。不建项目时全部设备同属一个范围。</div>}
            {(view.projects ?? []).map((p, i) => (
              <div key={p.name + i} className="mem-hint" style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 4, flexWrap: "wrap" }}>
                <span style={{ fontWeight: 600, minWidth: 80 }}>{p.name}</span>
                <span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>
                  {(p.groups ?? []).length > 0 ? p.groups.map(g => (
                    <span key={g} className="btn btn--secondary btn--small" role="button" style={{ borderColor: "var(--accent, #7ab8ff)", color: "var(--accent, #7ab8ff)", opacity: 1 }}>{g}</span>
                  )) : <span style={{ opacity: 0.55 }}>（未选分组）</span>}
                </span>
                {p.note && <span style={{ opacity: 0.6 }}>{p.note}</span>}
                <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingProject({ draft: { ...p, groups: [...(p.groups ?? [])] }, index: i })}>编辑</span>
                <span className="btn btn--secondary btn--small" role="button" title="删除"
                  onClick={() => patch({ projects: (view.projects ?? []).filter((_, j) => j !== i) })}>×</span>
              </div>
            ))}
          </Section>

          <Section
            title={`诊断命令组合（${(view.presets ?? []).length}）`}
            desc="设备卡「诊断组合」一键逐条执行，走密封只读路径。"
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingPreset({ draft: { name: "", commands: [], vendors: [] }, index: -1 })}>+ 新建组合</span>}
          >
            {(view.presets ?? []).length === 0 && <div className="mem-hint">暂无组合。建一个「接口体检」（display interface brief 等）试试。</div>}
            {(view.presets ?? []).map((p, i) => (
              <div key={p.name + i} className="mem-hint" style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 4, flexWrap: "wrap" }}>
                <span style={{ fontWeight: 600, minWidth: 80 }}>{p.name}</span>
                <span style={{ flex: 1, minWidth: 200, fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace", fontSize: 11.5 }}>
                  {(p.commands ?? []).join("; ")}
                </span>
                {(p.vendors ?? []).length > 0 && <span style={{ opacity: 0.55 }}>仅 {p.vendors.join("/")}</span>}
                <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingPreset({ draft: { ...p, commands: [...(p.commands ?? [])], vendors: [...(p.vendors ?? [])] }, index: i })}>编辑</span>
                <span className="btn btn--secondary btn--small" role="button" title="删除"
                  onClick={() => patch({ presets: (view.presets ?? []).filter((_, j) => j !== i) })}>×</span>
              </div>
            ))}
          </Section>

          <Section title="定时任务" desc="到点自动巡检/全量配置备份（密封读+脱敏落版本）；结果分别进「发现」和设备卡的备份历史。">
            <div style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                巡检周期
                <input className="mem-input" style={{ width: 90 }} placeholder="如 1h / 30m，留空=关"
                  value={view.inspectionInterval ?? ""}
                  onChange={e => patch({ inspectionInterval: e.target.value })} />
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                备份周期
                <input className="mem-input" style={{ width: 90 }} placeholder="如 24h，留空=关"
                  value={view.backupInterval ?? ""}
                  onChange={e => patch({ backupInterval: e.target.value })} />
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                SNMP 健康轮询
                <input className="mem-input" type="number" style={{ width: 90 }} placeholder="秒，0=关"
                  value={view.pollIntervalSeconds ?? 0}
                  onChange={e => patch({ pollIntervalSeconds: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
              <div className="mem-hint">轮询所有带 [snmp] 块的设备（可达性/uptime/接口状态）→ 运维页「健康」页卡。设备编辑表单里配团体字。</div>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                syslog 接收端口
                <input className="mem-input" type="number" style={{ width: 90 }} placeholder="0=关，如 5140"
                  value={view.syslogPort ?? 0}
                  onChange={e => patch({ syslogPort: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
              <div className="mem-hint">设备 syslog 指向本机该端口（UDP）；按设备聚合进「日志」页卡的 syslog 源，链路翻动/认证失败等模式自动升级为发现。改端口需重启应用。</div>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                默认模式
                <select className="mem-select" value={view.defaultMode || "diagnose"}
                  onChange={e => patch({ defaultMode: e.target.value })}>
                  <option value="diagnose">diagnose（诊断）</option>
                  <option value="assess">assess（评估）</option>
                </select>
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                每设备最大会话数
                <input className="mem-input" type="number" style={{ width: 70 }} placeholder="0=默认"
                  value={view.maxSessionsPerDevice ?? 0}
                  onChange={e => patch({ maxSessionsPerDevice: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
            </div>
          </Section>

          <Section
            title="分组策略"
            desc="分组的提案策略与维护窗口：窗口外的写提案需二次确认或拒绝；read-only 组的写提案直接拒绝。"
          >
            {(() => {
              const defs = view.groupDefs && view.groupDefs.length > 0
                ? view.groupDefs
                : [...new Set((view.devices ?? []).map(d => d.group).filter(Boolean))].map(n => ({ name: n, policy: "", changeWindow: "" }));
              if (defs.length === 0) return <div className="mem-hint">还没有分组——设备编辑里给设备填分组后，这里可配策略与维护窗口。</div>;
              return defs.map((g, i) => (
                <div key={g.name} style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12, marginBottom: 6 }}>
                  <span style={{ minWidth: 90, fontWeight: 600 }}>{g.name}</span>
                  <select className="mem-select" style={{ width: 170 }} value={g.policy || "read-only"}
                    onChange={e => patch({ groupDefs: defs.map((x, j) => j === i ? { ...x, policy: e.target.value } : x) })}>
                    <option value="read-only">read-only（拒绝写提案）</option>
                    <option value="proposal">proposal（人工签核）</option>
                    <option value="proposal+confirm2">proposal+confirm2（二次确认）</option>
                  </select>
                  <input className="mem-input" style={{ flex: 1 }} placeholder="维护窗口，如 tue,thu 22:00-24:00，空=不限"
                    value={g.changeWindow ?? ""}
                    onChange={e => patch({ groupDefs: defs.map((x, j) => j === i ? { ...x, changeWindow: e.target.value } : x) })} />
                </div>
              ));
            })()}
          </Section>

          <Section
            title="告警规则"
            desc="健康轮询的阈值规则：命中自动生成「发现」（带告警中徽标），条件清除后自动标记已恢复；也可在发现卡手动标记已处理。"
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingRule({ name: "", metric: "reachable", op: "==", value: 0, severity: "warning", enabled: true })}>添加规则</span>}
          >
            {(view.alertRules ?? []).length === 0 && (
              <div className="mem-hint">还没有规则。常用：「设备不可达」（reachable == 0, critical）、「接口掉线 ≥1」（if_down_count &gt;= 1, warning）、「设备重启」（uptime_reset == 1, warning）。</div>
            )}
            {(view.alertRules ?? []).map((r, i) => (
              <div key={r.name} className="ndv__device">
                <span className={`ndv__dot ${r.enabled ? "ndv__dot--ok" : "ndv__dot--down"}`} />
                <span className="ndv__device-name">{r.name}</span>
                <span className="ndv__device-addr">{r.metric} {r.op} {r.value} · {r.severity}</span>
                <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }}
                  onClick={() => setEditingRule({ ...r })}>编辑</span>
                <span className="btn btn--secondary btn--small" role="button"
                  onClick={() => patch({ alertRules: (view.alertRules ?? []).filter((_, j) => j !== i) })}>删除</span>
              </div>
            ))}
          </Section>

          <Section
            title="数据库源（只读诊断）"
            desc="连接数/慢查询/主从延迟类诊断。硬边界有两条：账号本身必须只读授权；白名单是精确语句（如 SHOW PROCESSLIST），不含的语句在连接前即被拒绝。"
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDB({ name: "", type: "mysql", host: "", port: 3306, username: "", passwordEnv: "", passwordSet: false, database: "", allowlist: ["SHOW PROCESSLIST"], password: "" })}>添加源</span>}
          >
            {(view.dbSources ?? []).length === 0 && (
              <div className="mem-hint">还没有数据库源。密码进加密存储，白名单语句逐条列出（一行一条）。</div>
            )}
            {(view.dbSources ?? []).map((s, i) => (
              <div key={s.name} className="ndv__device">
                <span className="ndv__device-name">{s.name}</span>
                <span className="ndv__device-addr">{s.type} · {s.host}{s.passwordSet ? " · 密码已存" : ""}</span>
                <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }}
                  onClick={() => setEditingDB({ ...s, password: "" })}>编辑</span>
                <span className="btn btn--secondary btn--small" role="button"
                  onClick={() => patch({ dbSources: (view.dbSources ?? []).filter((_, j) => j !== i) })}>删除</span>
              </div>
            ))}
          </Section>
        </>
      )}

      {/* ── 子页签 4：高级 ─────────────────────────────────────────────── */}
      {sub === "advanced" && (
        <>
          <Section
            title="扫描导入"
            desc="把已有的 nmap 扫描结果变成发现（Finding）——清单外主机标「待确认」，导入本身不拨号。"
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => { setScanXml(""); setScanOpen(true); }}>导入 nmap XML</span>}
          >
            <div className="mem-hint">nmap -oX 输出的主机与开放端口会汇总为一条发现；不在设备清单内的地址标记「待确认」，AI 不会主动连接。</div>
          </Section>

          <Section title="审计" desc="每条设备命令（含拒绝）都落审计；这里只控制保留期。">
            <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
              审计保留
              <input
                className="mem-input" style={{ width: 90 }} placeholder="如 180d，留空=默认"
                value={view.auditRetention ?? ""}
                onChange={e => patch({ auditRetention: e.target.value })}
              />
            </label>
            <div className="mem-hint">审计只记命令与字节数，输出原文不入档（脱敏在进入上下文之前完成）。</div>
          </Section>

          <Section title="日常操作" desc="巡检、审计、发现（含证据链）、提案审批都在运维页的左下角与右栏——本页只放配置。">
            <div className="mem-hint">本页改动（设备、跳板、项目、护栏、读表、组合、探测范围、周期）记得点顶部「保存」。</div>
          </Section>
        </>
      )}

      {/* 设备编辑表单 */}
      {editingDevice && (
        <L3Panel crumbs={["运维 / 设备与跳板", view.devices.some(d => d.name === editingDevice.name) ? `编辑 ${editingDevice.name}` : "添加设备"]} onBack={() => setEditingDevice(null)} confirmDiscard>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label="名称 *"><input className="mem-input" value={editingDevice.name} onChange={e => setEditingDevice({ ...editingDevice, name: e.target.value })} /></Field>
            <Field label="厂商">
              <select className="mem-select" value={editingDevice.vendor} onChange={e => setEditingDevice({ ...editingDevice, vendor: e.target.value, os: (OSES[e.target.value] ?? [""])[0] })}>
                {VENDORS.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            </Field>
            <Field label="OS">
              <select className="mem-select" value={editingDevice.os} onChange={e => setEditingDevice({ ...editingDevice, os: e.target.value })}>
                {(OSES[editingDevice.vendor] ?? []).map(o => <option key={o} value={o}>{o}</option>)}
              </select>
            </Field>
            <Field label="型号"><input className="mem-input" value={editingDevice.model} onChange={e => setEditingDevice({ ...editingDevice, model: e.target.value })} /></Field>
            <Field label="地址 *"><input className="mem-input" value={editingDevice.address} onChange={e => setEditingDevice({ ...editingDevice, address: e.target.value })} /></Field>
            <Field label="端口"><input className="mem-input" type="number" value={editingDevice.port} onChange={e => setEditingDevice({ ...editingDevice, port: Number(e.target.value) || 22 })} /></Field>
            <Field label="分组">
              <input
                className="mem-input" list="ndv-groups" placeholder="如 core / edge（可新起名）"
                value={editingDevice.group ?? ""}
                onChange={e => setEditingDevice({ ...editingDevice, group: e.target.value })}
              />
              <datalist id="ndv-groups">
                {(view.groups ?? []).map(g => <option key={g} value={g} />)}
              </datalist>
            </Field>
            <Field label="登录用户"><input className="mem-input" value={editingDevice.username} onChange={e => setEditingDevice({ ...editingDevice, username: e.target.value })} /></Field>
            <Field label={editingDevice.passwordSet ? "密码（留空=保持不变）" : "密码"}>
              <input className="mem-input" type="password" value={editingDevice.password} onChange={e => setEditingDevice({ ...editingDevice, password: e.target.value })} />
            </Field>
            <Field label="路由 via（跳板名，逗号分隔）">
              <input className="mem-input" value={(editingDevice.via ?? []).join(",")}
                onChange={e => setEditingDevice({ ...editingDevice, via: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })} />
            </Field>
            <Field label="编码">
              <select className="mem-select" value={editingDevice.encoding || "auto"} onChange={e => setEditingDevice({ ...editingDevice, encoding: e.target.value })}>
                {["auto", "utf-8", "gbk"].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
            <Field label="日志路径白名单（逗号分隔）">
              <input
                className="mem-input" placeholder="/var/log 已默认放行；如 /opt/app/logs、/usr/local/tomcat/logs"
                value={(editingDevice.logPaths ?? []).join(",")}
                onChange={e => setEditingDevice({ ...editingDevice, logPaths: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
              />
            </Field>
            <Field label="SNMP（健康轮询）">
              <select className="mem-select" value={editingDevice.snmpVersion ?? ""}
                onChange={e => setEditingDevice({ ...editingDevice, snmpVersion: e.target.value })}>
                <option value="">（不启用）</option>
                <option value="v2c">v2c</option>
              </select>
            </Field>
            <Field label={editingDevice.snmpCommunitySet ? "SNMP 团体字（留空=保持不变）" : "SNMP 团体字"}>
              <input className="mem-input" type="password" value={editingDevice.snmpCommunity ?? ""}
                onChange={e => setEditingDevice({ ...editingDevice, snmpCommunity: e.target.value })} />
            </Field>
            <Field label="协议优先级（逗号分隔）">
              <input className="mem-input" placeholder="ssh, telnet, netconf"
                value={(editingDevice.protocols ?? []).join(", ")}
                onChange={e => setEditingDevice({ ...editingDevice, protocols: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })} />
            </Field>
            <Field label="数据面 kind">
              <select className="mem-select" value={editingDevice.kind ?? ""} onChange={e => setEditingDevice({ ...editingDevice, kind: e.target.value })}>
                <option value="">自动（按厂商——网络设备/主机 CLI）</option>
                <option value="docker">docker（Docker Engine API 只读）</option>
                <option value="k8s">k8s（Kubernetes API 只读）</option>
                <option value="firewall">firewall（FortiOS REST 只读）</option>
              </select>
            </Field>
            {(editingDevice.kind ?? "") === "docker" && (
              <Field label="Docker socket">
                <input className="mem-input" placeholder="留空 = 本地默认（Windows npipe / Linux unix sock）；或 tcp://10.0.0.9:2375"
                  value={editingDevice.dockerSocket ?? ""}
                  onChange={e => setEditingDevice({ ...editingDevice, dockerSocket: e.target.value })} />
              </Field>
            )}
            {(editingDevice.kind ?? "") === "k8s" && (
              <>
                <Field label={editingDevice.k8sKubeconfigSet ? "kubeconfig（密钥库已存；粘贴 = 替换）" : "kubeconfig（粘贴全文，存密钥库）"}>
                  <textarea className="mem-input" rows={4} style={{ width: "100%", fontFamily: "var(--font-mono, monospace)", fontSize: 11 }}
                    placeholder="apiVersion: v1&#10;kind: Config&#10;…"
                    value={editingDevice.k8sKubeconfig ?? ""}
                    onChange={e => setEditingDevice({ ...editingDevice, k8sKubeconfig: e.target.value })} />
                </Field>
                <Field label="固定 context（可空）">
                  <input className="mem-input" placeholder="留空 = kubeconfig 的 current-context"
                    value={editingDevice.k8sContext ?? ""}
                    onChange={e => setEditingDevice({ ...editingDevice, k8sContext: e.target.value })} />
                </Field>
                <Field label="命名空间白名单（逗号分隔，空 = 全部）">
                  <input className="mem-input" placeholder="prod, kube-system"
                    value={(editingDevice.k8sNamespaces ?? []).join(", ")}
                    onChange={e => setEditingDevice({ ...editingDevice, k8sNamespaces: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })} />
                </Field>
              </>
            )}
            {(editingDevice.kind ?? "") === "firewall" && (
              <Field label={editingDevice.fwApiTokenSet ? "REST API token（密钥库已存；粘贴 = 替换）" : "REST API token（粘贴，存密钥库）"}>
                <input className="mem-input" type="password" placeholder="FortiOS REST API token"
                  value={editingDevice.fwApiToken ?? ""}
                  onChange={e => setEditingDevice({ ...editingDevice, fwApiToken: e.target.value })} />
              </Field>
            )}
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span
              className="btn btn--secondary btn--small" role="button"
              onClick={() => { if (editingDevice.name.trim()) void testConnection(editingDevice.name); }}
              title="连接 → 主机密钥确认（首次） → CLI 会话验证"
            >{testing ? "测试中…" : "测试连接"}</span>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice(null)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingDevice.name.trim() || !editingDevice.address.trim()) { setErr("名称和地址必填"); return; }
                const exists = view.devices.some(d => d.name === editingDevice.name);
                const devices = exists ? view.devices.map(d => d.name === editingDevice.name ? editingDevice : d) : [...view.devices, editingDevice];
                setEditingDevice(null);
                void save({ ...view, devices });
              }}
            >保存设备</span>
          </div>
        </L3Panel>
      )}

      {/* 数据库源编辑表单 */}
      {editingDB && (
        <L3Panel crumbs={["运维", view.dbSources?.some(s => s.name === editingDB.name) ? "编辑数据库源" : "添加数据库源"]} onBack={() => setEditingDB(null)} confirmDiscard>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label="名称 *"><input className="mem-input" value={editingDB.name} onChange={e => setEditingDB({ ...editingDB, name: e.target.value })} /></Field>
            <Field label="类型">
              <select className="mem-select" value={editingDB.type} onChange={e => {
                const t = e.target.value;
                setEditingDB({ ...editingDB, type: t, port: t === "mysql" ? 3306 : t === "postgres" ? 5432 : 6379 });
              }}>
                {["mysql", "postgres", "redis"].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
            <Field label="地址 *"><input className="mem-input" value={editingDB.host} onChange={e => setEditingDB({ ...editingDB, host: e.target.value })} /></Field>
            <Field label="端口"><input className="mem-input" type="number" value={editingDB.port} onChange={e => setEditingDB({ ...editingDB, port: Number(e.target.value) || 3306 })} /></Field>
            <Field label="只读账号 *"><input className="mem-input" value={editingDB.username} onChange={e => setEditingDB({ ...editingDB, username: e.target.value })} /></Field>
            <Field label={editingDB.passwordSet ? "密码（留空=保持不变）" : "密码"}>
              <input className="mem-input" type="password" value={editingDB.password} onChange={e => setEditingDB({ ...editingDB, password: e.target.value })} />
            </Field>
            <Field label="默认库（可空）"><input className="mem-input" value={editingDB.database} onChange={e => setEditingDB({ ...editingDB, database: e.target.value })} /></Field>
          </div>
          <div style={{ marginTop: 8 }}>
            <div className="set-label" style={{ marginBottom: 4 }}>语句白名单（一行一条，精确语句——这是硬边界）</div>
            <textarea
              className="mem-input" rows={5} style={{ width: "100%", fontFamily: "var(--font-mono, monospace)", fontSize: 11.5 }}
              placeholder={editingDB.type === "redis" ? "redis 无需白名单（内置只读诊断命令集）" : "SHOW PROCESSLIST\nSHOW ENGINE INNODB STATUS\nSELECT * FROM information_schema.processlist"}
              value={(editingDB.allowlist ?? []).join("\n")}
              onChange={e => setEditingDB({ ...editingDB, allowlist: e.target.value.split("\n").map(s => s.trim()).filter(Boolean) })}
            />
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDB(null)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingDB.name.trim() || !editingDB.host.trim()) { setErr("名称和地址必填"); return; }
                if (editingDB.type !== "redis" && (editingDB.allowlist ?? []).length === 0) { setErr("mysql/postgres 源至少一条白名单语句"); return; }
                const exists = (view.dbSources ?? []).some(s => s.name === editingDB.name);
                const dbSources = exists ? (view.dbSources ?? []).map(s => s.name === editingDB.name ? editingDB : s) : [...(view.dbSources ?? []), editingDB];
                setEditingDB(null);
                void save({ ...view, dbSources });
              }}
            >保存源</span>
          </div>
        </L3Panel>
      )}

      {/* 告警规则编辑表单 */}
      {editingRule && (
        <L3Panel crumbs={["运维", (view.alertRules ?? []).some(r => r.name === editingRule.name) ? "编辑告警规则" : "添加告警规则"]} onBack={() => setEditingRule(null)} confirmDiscard>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label="名称 *"><input className="mem-input" value={editingRule.name} onChange={e => setEditingRule({ ...editingRule, name: e.target.value })} /></Field>
            <Field label="指标">
              <select className="mem-select" value={editingRule.metric} onChange={e => setEditingRule({ ...editingRule, metric: e.target.value, value: e.target.value === "reachable" || e.target.value === "uptime_reset" ? 0 : 1 })}>
                <option value="reachable">设备可达（1=在线 0=不可达）</option>
                <option value="if_down_count">掉线接口数</option>
                <option value="uptime_reset">重启检测（uptime 回绕）</option>
              </select>
            </Field>
            <Field label="比较">
              <select className="mem-select" value={editingRule.op || ">="} onChange={e => setEditingRule({ ...editingRule, op: e.target.value })}>
                {["==", ">=", "<="].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
            <Field label="阈值"><input className="mem-input" type="number" value={editingRule.value} onChange={e => setEditingRule({ ...editingRule, value: Number(e.target.value) || 0 })} /></Field>
            <Field label="严重度">
              <select className="mem-select" value={editingRule.severity || "warning"} onChange={e => setEditingRule({ ...editingRule, severity: e.target.value })}>
                {["info", "warning", "critical"].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
            <Field label="启用">
              <select className="mem-select" value={editingRule.enabled ? "1" : "0"} onChange={e => setEditingRule({ ...editingRule, enabled: e.target.value === "1" })}>
                <option value="1">启用</option>
                <option value="0">停用</option>
              </select>
            </Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingRule(null)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingRule.name.trim()) { setErr("规则名必填"); return; }
                const exists = (view.alertRules ?? []).some(r => r.name === editingRule.name);
                const alertRules = exists ? (view.alertRules ?? []).map(r => r.name === editingRule.name ? editingRule : r) : [...(view.alertRules ?? []), editingRule];
                setEditingRule(null);
                void save({ ...view, alertRules });
              }}
            >保存规则</span>
          </div>
        </L3Panel>
      )}

      {/* 跳板编辑表单 */}
      {editingHop && (
        <L3Panel crumbs={["运维", view.hops.some(h => h.name === editingHop.name) ? "编辑跳板" : "添加跳板"]} onBack={() => setEditingHop(null)} confirmDiscard>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label="名称 *"><input className="mem-input" value={editingHop.name} onChange={e => setEditingHop({ ...editingHop, name: e.target.value })} /></Field>
            <Field label="地址 *"><input className="mem-input" value={editingHop.host} onChange={e => setEditingHop({ ...editingHop, host: e.target.value })} /></Field>
            <Field label="端口"><input className="mem-input" type="number" value={editingHop.port} onChange={e => setEditingHop({ ...editingHop, port: Number(e.target.value) || 22 })} /></Field>
            <Field label="用户"><input className="mem-input" value={editingHop.user} onChange={e => setEditingHop({ ...editingHop, user: e.target.value })} /></Field>
            <Field label={editingHop.passwordSet ? "密码（留空=保持不变）" : "密码"}>
              <input className="mem-input" type="password" value={editingHop.password} onChange={e => setEditingHop({ ...editingHop, password: e.target.value })} />
            </Field>
            <Field label="上级跳板（可选，名称）"><input className="mem-input" value={editingHop.proxyJump} onChange={e => setEditingHop({ ...editingHop, proxyJump: e.target.value })} /></Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop(null)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingHop.name.trim() || !editingHop.host.trim()) { setErr("名称和地址必填"); return; }
                const exists = view.hops.some(h => h.name === editingHop.name);
                const hops = exists ? view.hops.map(h => h.name === editingHop.name ? editingHop : h) : [...view.hops, editingHop];
                setEditingHop(null);
                void save({ ...view, hops });
              }}
            >保存跳板</span>
          </div>
        </L3Panel>
      )}

      {/* 项目编辑表单 */}
      {editingProject && (
        <L3Panel crumbs={["运维", editingProject.index >= 0 ? "编辑项目" : "新建项目"]} onBack={() => setEditingProject(null)} confirmDiscard>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <Field label="名称 *">
              <input className="mem-input" placeholder="如：一号机房 / 总部生产网" value={editingProject.draft.name}
                onChange={e => setEditingProject({ ...editingProject, draft: { ...editingProject.draft, name: e.target.value } })} />
            </Field>
            <Field label="包含分组（点选）">
              <span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>
                {(view.groups ?? []).map(g => {
                  const on = editingProject.draft.groups.includes(g);
                  return (
                    <span key={g} className="btn btn--secondary btn--small" role="button"
                      style={on ? { borderColor: "var(--accent, #7ab8ff)", color: "var(--accent, #7ab8ff)" } : { opacity: 0.55 }}
                      onClick={() => {
                        const gs = new Set(editingProject.draft.groups);
                        if (gs.has(g)) gs.delete(g); else gs.add(g);
                        setEditingProject({ ...editingProject, draft: { ...editingProject.draft, groups: [...gs] } });
                      }}
                    >{g}</span>
                  );
                })}
                {(view.groups ?? []).length === 0 && <span style={{ opacity: 0.55, fontSize: 11.5 }}>还没有分组——先在设备编辑里给设备填分组</span>}
              </span>
            </Field>
            <Field label="备注（悬停可见）">
              <input className="mem-input" value={editingProject.draft.note}
                onChange={e => setEditingProject({ ...editingProject, draft: { ...editingProject.draft, note: e.target.value } })} />
            </Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingProject(null)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingProject.draft.name.trim()) { setErr("项目名称必填"); return; }
                const projects = [...(view.projects ?? [])];
                if (editingProject.index >= 0) projects[editingProject.index] = editingProject.draft;
                else projects.push(editingProject.draft);
                setEditingProject(null);
                void save({ ...view, projects });
              }}
            >保存项目</span>
          </div>
        </L3Panel>
      )}

      {/* 诊断组合编辑表单 */}
      {editingPreset && (
        <L3Panel crumbs={["运维", editingPreset.index >= 0 ? "编辑组合" : "新建组合"]} onBack={() => setEditingPreset(null)} confirmDiscard>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <Field label="名称 *">
              <input className="mem-input" placeholder="如：接口体检" value={editingPreset.draft.name}
                onChange={e => setEditingPreset({ ...editingPreset, draft: { ...editingPreset.draft, name: e.target.value } })} />
            </Field>
            <Field label="命令（分号或换行分隔，全部走只读密封路径）">
              <textarea
                className="mem-input" rows={5} style={{ width: "100%", resize: "vertical", fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace", fontSize: 12 }}
                placeholder={"display interface brief\ndisplay interface description"}
                value={editingPreset.draft.commands.join("\n")}
                onChange={e => setEditingPreset({ ...editingPreset, draft: { ...editingPreset.draft, commands: e.target.value.split(/[;\n]/).map(c => c.trim()).filter(Boolean) } })}
              />
            </Field>
            <Field label="适用厂商（不选 = 全部）">
              <span style={{ display: "inline-flex", gap: 4 }}>
                {READ_VENDORS.map(v => {
                  const on = editingPreset.draft.vendors.includes(v);
                  return (
                    <span key={v} className="btn btn--secondary btn--small" role="button"
                      style={on ? { borderColor: "var(--accent, #7ab8ff)", color: "var(--accent, #7ab8ff)" } : { opacity: 0.55 }}
                      onClick={() => {
                        const vs = new Set(editingPreset.draft.vendors);
                        if (vs.has(v)) vs.delete(v); else vs.add(v);
                        setEditingPreset({ ...editingPreset, draft: { ...editingPreset.draft, vendors: [...vs] } });
                      }}
                    >{v}</span>
                  );
                })}
              </span>
            </Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingPreset(null)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingPreset.draft.name.trim() || editingPreset.draft.commands.length === 0) { setErr("名称与至少一条命令必填"); return; }
                const presets = [...(view.presets ?? [])];
                if (editingPreset.index >= 0) presets[editingPreset.index] = editingPreset.draft;
                else presets.push(editingPreset.draft);
                setEditingPreset(null);
                void save({ ...view, presets });
              }}
            >保存组合</span>
          </div>
        </L3Panel>
      )}

      {/* 扫描导入弹框 */}
      {scanOpen && (
        <Modal title="导入 nmap XML" onClose={() => setScanOpen(false)}>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <div className="mem-hint">粘贴 nmap -oX 输出全文。主机与开放端口会汇总为一条发现；清单外地址标「待确认」，导入不拨号。</div>
            <textarea
              className="mem-input" rows={12} style={{ width: "100%", resize: "vertical", fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace", fontSize: 11.5 }}
              placeholder='<?xml version="1.0"?>&#10;<nmaprun>…</nmaprun>'
              value={scanXml}
              onChange={e => setScanXml(e.target.value)}
            />
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setScanOpen(false)}>取消</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={async () => {
                if (!scanXml.trim()) return;
                setScanBusy(true);
                try {
                  const f = await app.NetDevImportNmap(scanXml);
                  showToast(f ? f.title : "导入完成", "info");
                  setScanOpen(false);
                } catch (e) { setErr(String(e)); }
                finally { setScanBusy(false); }
              }}
            >{scanBusy ? "导入中…" : "导入"}</span>
          </div>
        </Modal>
      )}
    </div>
  );
}

// Section — the settings page's standard card (same classes as
// SettingsPanel's internal SettingsSection, which isn't exported).
function Section({ title, desc, actions, children }: { title: React.ReactNode; desc?: React.ReactNode; actions?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="settings-section">
      <div className="settings-section__head">
        <div>
          <div className="settings-section__title">{title}</div>
          {desc && <div className="settings-section__desc">{desc}</div>}
        </div>
        {actions && <div className="settings-section__actions">{actions}</div>}
      </div>
      <div className="settings-section__body">{children}</div>
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="set-label" style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <span>{label}</span>
      {children}
    </label>
  );
}

// L3Panel — 设置三级导航的第三级（NETDEV_SPEC_V2 §10.9）：表单类配置不再用
// 弹框，而是由右向左翻页进入的整页表单（面包屑 + 返回 + Esc）。弹框从此只
// 留给阻塞确认类。confirmDiscard 的表单在返回/Esc 前确认丢弃未保存修改。
function L3Panel({ crumbs, onBack, confirmDiscard, children }: { crumbs: string[]; onBack: () => void; confirmDiscard?: boolean; children: React.ReactNode }) {
  const confirm = useConfirm();
  const back = useCallback(async () => {
    if (confirmDiscard) {
      const ok = await confirm({ title: "放弃未保存的修改？", message: "返回将丢弃表单里未保存的修改。", danger: true, confirmLabel: "放弃并返回" });
      if (!ok) return;
    }
    onBack();
  }, [confirm, confirmDiscard, onBack]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") void back(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [back]);
  return (
    <div className="ndv-l3" role="dialog" aria-label={crumbs.join(" / ")}>
      <div className="ndv-l3__head">
        <button className="btn btn--secondary btn--small" onClick={() => void back()}>← 返回</button>
        <span className="ndv-l3__crumbs">{crumbs.join(" / ")}</span>
      </div>
      <div className="ndv-l3__body">{children}</div>
    </div>
  );
}

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div
      role="dialog" aria-modal="true"
      style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,.45)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50 }}
      onClick={e => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div style={{ background: "var(--bg-elevated, #23272e)", borderRadius: 8, padding: 16, minWidth: 520, maxWidth: 680, maxHeight: "80vh", overflowY: "auto" }}>
        <div className="set-label" style={{ marginBottom: 10 }}>{title}</div>
        {children}
      </div>
    </div>
  );
}
