import { useCallback, useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import { useConfirm } from "../../lib/confirm";
import { useT } from "../../lib/i18n";
import { useToast } from "../../lib/toast";
import type {
  NetDevAlertRuleView,
  NetDevDBSourceView,
  NetDevPresetView,
  NetDevProjectView,
  NetDevSettingsView,
  NetDevSSHImportCandidate,
} from "../../lib/types";

// NetDevSection is 运维设置 tab: device/hop inventory (persisted to
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
  identityFile: "", encoding: "auto", password: "", logPaths: [], configPaths: [], oobUrl: "", protocols: [], snmpVersion: "", snmpCommunityEnv: "", snmpCommunitySet: false, snmpCommunity: "",
});

const emptyHop = (): EditHop => ({
  name: "", host: "", port: 22, user: "", passwordEnv: "", passwordSet: false,
  proxyJump: "", password: "",
});

export function NetDevSection() {
  const t = useT();
  const confirm = useConfirm();
  const { showToast } = useToast();
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [view, setView] = useState<NetDevSettingsView>({ enabled: false, networkName: "", devices: [], hops: [], groups: [], auditRetention: "", scopes: [], guardConfirmEach: false, guardTurnBudget: 0, guardAllowedGroups: [], extraRead: {}, projects: [], presets: [], inspectionInterval: "", backupInterval: "", scheduledBaseline: false, dbSources: [], pollIntervalSeconds: 0, alertRules: [], syslogPort: 0, defaultMode: "", maxSessionsPerDevice: 0, discoveryRate: 0, discoveryMode: "", probeFallback: "", groupDefs: [], notifyWebhook: "", notifyFormat: "", notifyMinSeverity: "", notifyBotDest: "", notifySMTPHost: "", notifySMTPPort: 587, notifySMTPUser: "", notifySMTPFrom: "", notifySMTPTo: [], notifySMTPPassSet: false, briefingPushTime: "", weakCredDict: "" });
  const [sub, setSub] = useState<SubTab>("inventory");
  const [editingDevice, setEditingDevice] = useState<EditDevice | null>(null);
  const [editingDB, setEditingDB] = useState<NetDevDBSourceView | null>(null);
  const [editingRule, setEditingRule] = useState<NetDevAlertRuleView | null>(null);
  // notifySMTPPassword lives outside `view`: write-only, never round-trips.
  const [notifySMTPPassword, setNotifySMTPPassword] = useState("");
  const [notifyTesting, setNotifyTesting] = useState(false);
  const [syslogStatus, setSyslogStatus] = useState<{ listening: boolean; port: number; buffered: number } | null>(null);
  const [trapStatus, setTrapStatus] = useState<{ listening: boolean; port: number; buffered: number } | null>(null);
  const testNotify = async () => {
    setNotifyTesting(true);
    try {
      await app.NetDevNotifyTest();
      setErr("");
    } catch (e) {
      setErr(String(e));
    } finally {
      setNotifyTesting(false);
    }
  };
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
  useEffect(() => { app.NetDevSyslogStatus().then(setSyslogStatus).catch(() => {}); }, [view.syslogPort]);
  useEffect(() => { app.NetDevTrapStatus().then(setTrapStatus).catch(() => {}); }, []);

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
          message: t("ndv.sets.tofuMsg", { device, host: r.host ?? "", keyType: r.keyType ?? "", fp: r.fingerprint ?? "" }),
          danger: true
        });
        if (!ok) { setErr("[SYS] KEY REJECTED"); return; }
        if (!r.fingerprint) { setErr("[SYS] INTERNAL ERROR: NO FINGERPRINT"); return; }
        await app.NetDevTrustHostKey(r.fingerprint);
        r = await app.NetDevTestConnection(device);
      }
      setErr(r.status === "ok" ? "[SYS] TARGET VERIFIED (VTY SESSION OPEN)" : t("ndv.sets.testFailed", { status: r.status, detail: r.detail ?? "" }));
    } catch (e) {
      setErr(String(e));
    } finally {
      setTesting(false);
    }
  }, []);

  // C1.5 凭证清除：从密钥库删除已存凭证（NetDevDeleteSecret），随后清空
  // 实体上的 env 指针——保存后 passwordSet 回到未设态。删除前确认。
  const clearCreds = async (label: string, entries: { kind: string; env?: string; what: string }[], onDone: () => void) => {
    const live = entries.filter(e => (e.env ?? "").trim());
    if (live.length === 0) { setErr(t("ndv.sets.noCreds")); return; }
    if (!(await confirm({
      title: "CLEAR CREDENTIALS",
      message: t("ndv.sets.clearCredsMsg", { label, list: live.map(e => e.what).join("、") }),
      danger: true, confirmLabel: t("ndv.sets.clearCredsBtn"),
    }))) return;
    try {
      for (const e of live) await app.NetDevDeleteSecret(e.kind, (e.env ?? "").trim());
      setErr("");
      showToast(t("ndv.sets.credsCleared", { label }), "info");
      onDone();
    } catch (e) { setErr(String(e)); }
  };

  if (!loaded) return <div className="mem-hint">…</div>;

  const SUBTABS: { key: SubTab; label: string; count?: number }[] = [
    { key: "inventory", label: t("ndv.sets.tabInventory"), count: view.devices.length + view.hops.length },
    { key: "guardrails", label: t("ndv.sets.tabGuardrails") },
    { key: "sites", label: t("ndv.sets.tabSites") },
    { key: "advanced", label: t("ndv.sets.tabAdvanced") },
  ];

  return (
    <div className="settings-page settings-page--form">
      <div className="settings-page__header">
        <h2 className="settings-page__title">{t("ndv.sets.title")}</h2>
        <p className="settings-page__desc">
          {t("ndv.sets.desc1")}
          {t("ndv.sets.desc2")}
        </p>
      </div>

      {err && <div className="banner banner--error" style={{ marginBottom: 8 }}>{err}</div>}

      <div className="optional-module__controls optional-module__controls--inline" style={{ marginBottom: 12 }}>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
          <input type="checkbox" checked={view.enabled} onChange={e => patch({ enabled: e.target.checked })} />
          {t("ndv.sets.enable")}
        </label>
        <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
          {t("ndv.sets.networkName")}
          <input className="mem-input" style={{ width: 180 }} value={view.networkName ?? ""} placeholder={t("ndv.sets.phNetwork")} onChange={e => patch({ networkName: e.target.value })} />
        </label>
        <span className="btn btn--primary btn--small" role="button" onClick={() => void save(view)}>{busy ? t("ndv.sets.saving") : t("ndv.sets.save")}</span>
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
            title={t("ndv.sets.devicesTitle", { n: view.devices.length })}
            desc={t("ndv.sets.devicesDesc")}
            actions={
              <>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice(emptyDevice())}>{"+ "}{t("ndv.sets.addDevice")}</span>
                <span
                  className="btn btn--secondary btn--small" role="button"
                  onClick={async () => {
                    try {
                      const c = await app.NetDevSSHImportCandidates();
                      setSSHCandidates(c ?? []);
                    } catch (e) { setErr(String(e)); }
                  }}
                >{t("ndv.sets.importSsh")}</span>
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
              <div className="mem-hint">{t("ndv.sets.noDevices")}</div>
            )}
            {view.devices.length > 0 && (
              <table className="mem-hint" style={{ width: "100%", borderCollapse: "collapse" }}>
                <thead>
                  <tr style={{ textAlign: "left" }}><th>{t("ndv.sets.colName")}</th><th>{t("ndv.sets.colVendorOs")}</th><th>{t("ndv.sets.colAddr")}</th><th>{t("ndv.sets.colRoute")}</th><th>{t("ndv.sets.colCred")}</th><th /></tr>
                </thead>
                <tbody>
                  {view.devices.map(d => (
                    <tr key={d.name}>
                      <td>{d.name}{d.group ? `（${d.group}）` : ""}</td>
                      <td>{d.vendor}/{d.os}</td>
                      <td>{d.address}{d.port && d.port !== 22 ? `:${d.port}` : ""}</td>
                      <td>{(d.via ?? []).join("→") || t("ndv.sets.direct")}</td>
                      <td>{d.passwordSet ? t("ndv.sets.credSet") : t("ndv.sets.credUnset")}</td>
                      <td>
                        <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice({ ...d, password: "" })}>{t("common.edit")}</span>{" "}
                        <span className="btn btn--secondary btn--small" role="button" title={t("common.delete")}
                          onClick={async () => { if (await confirm({ title: "DELETE DEVICE", message: t("ndv.sets.delDeviceMsg", { name: d.name }), danger: true })) void save({ ...view, devices: view.devices.filter(x => x.name !== d.name) }); }}>×</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Section>

          <Section
            title={t("ndv.sets.hopsTitle", { n: view.hops.length })}
            desc={t("ndv.sets.hopsDesc")}
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop(emptyHop())}>{"+ "}{t("ndv.sets.addHop")}</span>}
          >
            {view.hops.length === 0 && <div className="mem-hint">{t("ndv.sets.noHops")}</div>}
            {view.hops.map(h => (
              <div key={h.name} className="mem-hint" style={{ display: "flex", gap: 8, marginTop: 4, alignItems: "center" }}>
                <span style={{ minWidth: 160 }}>{h.name} → {h.host}{h.proxyJump ? t("ndv.sets.viaHop", { name: h.proxyJump }) : ""}</span>
                <span>{h.passwordSet ? t("ndv.sets.hopCredSet") : t("ndv.sets.credUnset")}</span>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop({ ...h, password: "" })}>{t("common.edit")}</span>
                <span className="btn btn--secondary btn--small" role="button" title={t("common.delete")}
                  onClick={async () => { if (await confirm({ title: "DELETE HOP", message: t("ndv.sets.delHopMsg", { name: h.name }), danger: true })) void save({ ...view, hops: view.hops.filter(x => x.name !== h.name) }); }}>×</span>
              </div>
            ))}
          </Section>
        </>
      )}

      {/* ── 子页签 2：护栏与读表 ────────────────────────────────────────── */}
      {sub === "guardrails" && (
        <>
          <Section title={t("ndv.sets.guardTitle")} desc={t("ndv.sets.guardDesc")}>
            <div style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <input
                  type="checkbox"
                  checked={!!view.guardConfirmEach}
                  onChange={e => patch({ guardConfirmEach: e.target.checked })}
                />
                {t("ndv.sets.guardConfirmEach")}
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.guardBudget")}
                <input
                  className="mem-input" style={{ width: 70 }} type="number" min={0}
                  value={view.guardTurnBudget ?? 0}
                  onChange={e => patch({ guardTurnBudget: Math.max(0, Number(e.target.value) || 0) })}
                />
                {t("ndv.sets.guardBudgetNote")}
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.guardGroups")}
                <input
                  className="mem-input" style={{ width: "50%" }}
                  value={(view.guardAllowedGroups ?? []).join(", ")}
                  placeholder={t("ndv.sets.phGroups")}
                  onChange={e => patch({ guardAllowedGroups: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
                />
              </label>
              <div style={{ opacity: 0.6, fontSize: 11.5 }}>
                {t("ndv.sets.guardGroupsNote")}
              </div>
            </div>
          </Section>

          <Section title={t("ndv.sets.scopesTitle")} desc={t("ndv.sets.scopesDesc")}>
            <input
              className="mem-input" style={{ width: "100%" }}
              value={(view.scopes ?? []).join(", ")}
              placeholder={t("ndv.sets.phScopes")}
              onChange={e => patch({ scopes: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
            />
            <div style={{ display: "flex", flexWrap: "wrap", gap: 8, marginTop: 8, fontSize: 12, alignItems: "center" }}>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.discoveryMode")}
                <select className="mem-select" value={view.discoveryMode || "auto"}
                  onChange={e => patch({ discoveryMode: e.target.value })}>
                  <option value="auto">auto</option>
                  <option value="tunnel">tunnel</option>
                  <option value="probe">probe</option>
                </select>
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.concurrency")}
                <input className="mem-input" type="number" style={{ width: 70 }} placeholder={t("ndv.sets.phRate")}
                  value={view.discoveryRate ?? 0}
                  onChange={e => patch({ discoveryRate: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
            </div>
          </Section>

          <Section title={t("ndv.sets.readExtTitle")} desc={t("ndv.sets.readExtDesc")}>
            {READ_VENDORS.map(vendor => {
              const list = view.extraRead?.[vendor] ?? [];
              const draft = readAdd[vendor] ?? "";
              return (
                <div key={vendor} style={{ display: "flex", gap: 6, alignItems: "center", marginBottom: 6, flexWrap: "wrap" }}>
                  <span style={{ minWidth: 60, fontWeight: 600 }}>{vendor}</span>
                  {list.map(cmd => (
                    <span key={cmd} className="btn btn--secondary btn--small" role="button" title={t("ndv.sets.clickRemove")}
                      onClick={() => patch({ extraRead: { ...view.extraRead, [vendor]: list.filter(c => c !== cmd) } })}>
                      {cmd} ×
                    </span>
                  ))}
                  <input
                    className="mem-input" style={{ width: 200 }} placeholder={t("ndv.sets.phReadCmd")}
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
                    }}>{"+ "}{t("ndv.sets.add")}</span>
                </div>
              );
            })}
            <div style={{ opacity: 0.6, fontSize: 11.5 }}>
              {t("ndv.sets.readExtNote")}
            </div>
          </Section>
        </>
      )}

      {/* ── 子页签 3：站点与自动化 ──────────────────────────────────────── */}
      {sub === "sites" && (
        <>
          <Section
            title={t("ndv.sets.projectsTitle", { n: (view.projects ?? []).length })}
            desc={t("ndv.sets.projectsDesc")}
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingProject({ draft: { name: "", groups: [], note: "" }, index: -1 })}>{"+ "}{t("ndv.sets.newProject")}</span>}
          >
            {(view.projects ?? []).length === 0 && <div className="mem-hint">{t("ndv.sets.noProjects")}</div>}
            {(view.projects ?? []).map((p, i) => (
              <div key={p.name + i} className="mem-hint" style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 4, flexWrap: "wrap" }}>
                <span style={{ fontWeight: 600, minWidth: 80 }}>{p.name}</span>
                <span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>
                  {(p.groups ?? []).length > 0 ? p.groups.map(g => (
                    <span key={g} className="btn btn--secondary btn--small" role="button" style={{ borderColor: "var(--accent, #7ab8ff)", color: "var(--accent, #7ab8ff)", opacity: 1 }}>{g}</span>
                  )) : <span style={{ opacity: 0.55 }}>{t("ndv.sets.noGroups")}</span>}
                </span>
                {p.note && <span style={{ opacity: 0.6 }}>{p.note}</span>}
                <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingProject({ draft: { ...p, groups: [...(p.groups ?? [])] }, index: i })}>{t("common.edit")}</span>
                <span className="btn btn--secondary btn--small" role="button" title={t("common.delete")}
                  onClick={() => patch({ projects: (view.projects ?? []).filter((_, j) => j !== i) })}>×</span>
              </div>
            ))}
          </Section>

          <Section
            title={t("ndv.sets.presetsTitle", { n: (view.presets ?? []).length })}
            desc={t("ndv.sets.presetsDesc")}
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingPreset({ draft: { name: "", commands: [], vendors: [] }, index: -1 })}>{"+ "}{t("ndv.sets.newPreset")}</span>}
          >
            {(view.presets ?? []).length === 0 && <div className="mem-hint">{t("ndv.sets.noPresets")}</div>}
            {(view.presets ?? []).map((p, i) => (
              <div key={p.name + i} className="mem-hint" style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 4, flexWrap: "wrap" }}>
                <span style={{ fontWeight: 600, minWidth: 80 }}>{p.name}</span>
                <span style={{ flex: 1, minWidth: 200, fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace", fontSize: 11.5 }}>
                  {(p.commands ?? []).join("; ")}
                </span>
                {(p.vendors ?? []).length > 0 && <span style={{ opacity: 0.55 }}>{t("ndv.sets.vendorsOnly", { list: p.vendors.join("/") })}</span>}
                <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingPreset({ draft: { ...p, commands: [...(p.commands ?? [])], vendors: [...(p.vendors ?? [])] }, index: i })}>{t("common.edit")}</span>
                <span className="btn btn--secondary btn--small" role="button" title={t("common.delete")}
                  onClick={() => patch({ presets: (view.presets ?? []).filter((_, j) => j !== i) })}>×</span>
              </div>
            ))}
          </Section>

          <Section title={t("ndv.sets.schedTitle")} desc={t("ndv.sets.schedDesc")}>
            <div style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.inspectionCycle")}
                <input className="mem-input" style={{ width: 90 }} placeholder={t("ndv.sets.phInspection")}
                  value={view.inspectionInterval ?? ""}
                  onChange={e => patch({ inspectionInterval: e.target.value })} />
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <input type="checkbox" checked={view.scheduledBaseline ?? false}
                  onChange={e => patch({ scheduledBaseline: e.target.checked })} />
                {t("ndv.sets.schedBaseline")}
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.backupCycle")}
                <input className="mem-input" style={{ width: 90 }} placeholder={t("ndv.sets.phBackup")}
                  value={view.backupInterval ?? ""}
                  onChange={e => patch({ backupInterval: e.target.value })} />
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.snmpPolling")}
                <input className="mem-input" type="number" style={{ width: 90 }} placeholder={t("ndv.sets.phSeconds")}
                  value={view.pollIntervalSeconds ?? 0}
                  onChange={e => patch({ pollIntervalSeconds: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
              <div className="mem-hint">{t("ndv.sets.snmpPollNote")}</div>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.syslogPort")}
                <input className="mem-input" type="number" style={{ width: 90 }} placeholder={t("ndv.sets.phSyslogPort")}
                  value={view.syslogPort ?? 0}
                  onChange={e => patch({ syslogPort: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
              <div className="mem-hint">{t("ndv.sets.syslogNote")}{syslogStatus ? " " + (syslogStatus.listening ? t("ndv.sets.listening", { port: syslogStatus.port, n: syslogStatus.buffered }) : t("ndv.sets.notListening")) : ""}</div>
              <div className="mem-hint">{t("ndv.sets.trapStatus", { state: trapStatus ? (trapStatus.listening ? t("ndv.sets.trapListening", { port: trapStatus.port, n: trapStatus.buffered }) : t("ndv.sets.trapNotListening")) : t("ndv.sets.trapUnknown") })}</div>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.defaultMode")}
                <select className="mem-select" value={view.defaultMode || "diagnose"}
                  onChange={e => patch({ defaultMode: e.target.value })}>
                  <option value="diagnose">{t("ndv.sets.modeDiagnose")}</option>
                  <option value="assess">{t("ndv.sets.modeAssess")}</option>
                </select>
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.maxSessions")}
                <input className="mem-input" type="number" style={{ width: 70 }} placeholder={t("ndv.sets.phRate")}
                  value={view.maxSessionsPerDevice ?? 0}
                  onChange={e => patch({ maxSessionsPerDevice: Math.max(0, Number(e.target.value) || 0) })} />
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.briefingPush")}
                <input className="mem-input" style={{ width: 80 }} placeholder={t("ndv.sets.phBriefing")}
                  value={view.briefingPushTime ?? ""}
                  onChange={e => patch({ briefingPushTime: e.target.value })} />
              </label>
              <div className="mem-hint">{t("ndv.sets.briefingNote")}</div>
            </div>
          </Section>

          <Section
            title={t("ndv.sets.groupPolicyTitle")}
            desc={t("ndv.sets.groupPolicyDesc")}
          >
            {(() => {
              const defs = view.groupDefs && view.groupDefs.length > 0
                ? view.groupDefs
                : [...new Set((view.devices ?? []).map(d => d.group).filter(Boolean))].map(n => ({ name: n, policy: "", changeWindow: "" }));
              if (defs.length === 0) return <div className="mem-hint">{t("ndv.sets.noGroupsDefs")}</div>;
              return defs.map((g, i) => (
                <div key={g.name} style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 12, marginBottom: 6 }}>
                  <span style={{ minWidth: 90, fontWeight: 600 }}>{g.name}</span>
                  <select className="mem-select" style={{ width: 170 }} value={g.policy || "read-only"}
                    onChange={e => patch({ groupDefs: defs.map((x, j) => j === i ? { ...x, policy: e.target.value } : x) })}>
                    <option value="read-only">{t("ndv.sets.policyRo")}</option>
                    <option value="proposal">{t("ndv.sets.policyProposal")}</option>
                    <option value="proposal+confirm2">{t("ndv.sets.policyConfirm2")}</option>
                  </select>
                  <input className="mem-input" style={{ flex: 1 }} placeholder={t("ndv.sets.phWindow")}
                    value={g.changeWindow ?? ""}
                    onChange={e => patch({ groupDefs: defs.map((x, j) => j === i ? { ...x, changeWindow: e.target.value } : x) })} />
                </div>
              ));
            })()}
          </Section>

          <Section
            title={t("ndv.sets.notifyTitle")}
            desc={t("ndv.sets.notifyDesc")}
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => void testNotify()}>{notifyTesting ? t("ndv.sets.sending") : t("ndv.sets.sendTest")}</span>}
          >
            <div style={{ display: "flex", flexDirection: "column", gap: 8, fontSize: 12 }}>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.severityGate")}
                <select className="mem-select" value={view.notifyMinSeverity || "warning"}
                  onChange={e => patch({ notifyMinSeverity: e.target.value })}>
                  <option value="info">{t("ndv.sets.sevInfo")}</option>
                  <option value="warning">{t("ndv.sets.sevWarning")}</option>
                  <option value="critical">{t("ndv.sets.sevCritical")}</option>
                </select>
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                Webhook
                <input className="mem-input" style={{ flex: 1 }} placeholder="https://open.feishu.cn/open-apis/bot/v2/hook/…"
                  value={view.notifyWebhook ?? ""} onChange={e => patch({ notifyWebhook: e.target.value })} />
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.format")}
                <select className="mem-select" value={view.notifyFormat || "generic"}
                  onChange={e => patch({ notifyFormat: e.target.value })}>
                  <option value="generic">{t("ndv.sets.fmtGeneric")}</option>
                  <option value="feishu">feishu</option>
                  <option value="dingtalk">dingtalk</option>
                  <option value="wecom">wecom</option>
                </select>
              </label>
              <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
                {t("ndv.sets.imPush")}
                <input className="mem-input" style={{ flex: 1 }} placeholder={t("ndv.sets.phBotDest")}
                  value={view.notifyBotDest ?? ""} onChange={e => patch({ notifyBotDest: e.target.value })} />
              </label>
              <div className="set-label" style={{ marginBottom: -2 }}>{t("ndv.sets.smtpSection")}</div>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                <input className="mem-input" style={{ flex: 2, minWidth: 140 }} placeholder={t("ndv.sets.phSmtpHost")}
                  value={view.notifySMTPHost ?? ""} onChange={e => patch({ notifySMTPHost: e.target.value })} />
                <input className="mem-input" type="number" style={{ width: 70 }} placeholder="587"
                  value={view.notifySMTPPort || 587} onChange={e => patch({ notifySMTPPort: Number(e.target.value) || 587 })} />
                <input className="mem-input" style={{ flex: 1, minWidth: 100 }} placeholder={t("ndv.sets.phUser")}
                  value={view.notifySMTPUser ?? ""} onChange={e => patch({ notifySMTPUser: e.target.value })} />
              </div>
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                <input className="mem-input" type={view.notifySMTPPassSet ? "password" : "text"} style={{ flex: 1, minWidth: 120 }}
                  placeholder={view.notifySMTPPassSet ? t("ndv.sets.phPwdKeep") : t("ndv.sets.phPwd")}
                  value={notifySMTPPassword} onChange={e => setNotifySMTPPassword(e.target.value)} />
                <input className="mem-input" style={{ flex: 1, minWidth: 120 }} placeholder={t("ndv.sets.phFrom")}
                  value={view.notifySMTPFrom ?? ""} onChange={e => patch({ notifySMTPFrom: e.target.value })} />
                <input className="mem-input" style={{ flex: 2, minWidth: 160 }} placeholder={t("ndv.sets.phTo")}
                  value={(view.notifySMTPTo ?? []).join(", ")}
                  onChange={e => patch({ notifySMTPTo: e.target.value.split(/[,，]/).map(x => x.trim()).filter(Boolean) })} />
              </div>
            </div>
          </Section>

          <Section
            title={t("ndv.sets.rulesTitle")}
            desc={t("ndv.sets.rulesDesc")}
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingRule({ name: "", metric: "reachable", op: "==", value: 0, severity: "warning", enabled: true })}>{t("ndv.sets.addRule")}</span>}
          >
            {(view.alertRules ?? []).length === 0 && (
              <div className="mem-hint">{t("ndv.sets.noRules")}</div>
            )}
            {(view.alertRules ?? []).map((r, i) => (
              <div key={r.name} className="ndv__device">
                <span className={`ndv__dot ${r.enabled ? "ndv__dot--ok" : "ndv__dot--down"}`} />
                <span className="ndv__device-name">{r.name}</span>
                <span className="ndv__device-addr">{r.metric} {r.op} {r.value} · {r.severity}</span>
                <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }}
                  onClick={() => setEditingRule({ ...r })}>{t("common.edit")}</span>
                <span className="btn btn--secondary btn--small" role="button"
                  onClick={() => patch({ alertRules: (view.alertRules ?? []).filter((_, j) => j !== i) })}>{t("common.delete")}</span>
              </div>
            ))}
          </Section>

          <Section
            title={t("ndv.sets.dbTitle")}
            desc={t("ndv.sets.dbDesc")}
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDB({ name: "", type: "mysql", host: "", port: 3306, username: "", passwordEnv: "", passwordSet: false, database: "", allowlist: ["SHOW PROCESSLIST"], password: "" })}>{t("ndv.sets.addSource")}</span>}
          >
            {(view.dbSources ?? []).length === 0 && (
              <div className="mem-hint">{t("ndv.sets.noDbSources")}</div>
            )}
            {(view.dbSources ?? []).map((s, i) => (
              <div key={s.name} className="ndv__device">
                <span className="ndv__device-name">{s.name}</span>
                <span className="ndv__device-addr">{s.type} · {s.host}{s.passwordSet ? " · " + t("ndv.sets.pwdStored") : ""}</span>
                <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }}
                  onClick={() => setEditingDB({ ...s, password: "" })}>{t("common.edit")}</span>
                <span className="btn btn--secondary btn--small" role="button"
                  onClick={() => patch({ dbSources: (view.dbSources ?? []).filter((_, j) => j !== i) })}>{t("common.delete")}</span>
              </div>
            ))}
          </Section>
        </>
      )}

      {/* ── 子页签 4：高级 ─────────────────────────────────────────────── */}
      {sub === "advanced" && (
        <>
          <Section
            title={t("ndv.sets.scanTitle")}
            desc={t("ndv.sets.scanDesc")}
            actions={<span className="btn btn--secondary btn--small" role="button" onClick={() => { setScanXml(""); setScanOpen(true); }}>{t("ndv.sets.importNmap")}</span>}
          >
            <div className="mem-hint">{t("ndv.sets.scanNote")}</div>
          </Section>

          <Section title={t("ndv.sets.dictTitle")} desc={t("ndv.sets.dictDesc")}>
            <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
              {t("ndv.sets.dictPath")}
              <input className="mem-input" style={{ width: 340 }} placeholder={t("ndv.sets.phDict")}
                value={view.weakCredDict ?? ""} onChange={e => patch({ weakCredDict: e.target.value })} />
            </label>
            <div className="mem-hint">{t("ndv.sets.dictNote")}</div>
          </Section>

          <Section title={t("ndv.sets.auditTitle")} desc={t("ndv.sets.auditDesc")}>
            <label className="set-label" style={{ display: "flex", gap: 8, alignItems: "center" }}>
              {t("ndv.sets.auditRetention")}
              <input
                className="mem-input" style={{ width: 90 }} placeholder={t("ndv.sets.phRetention")}
                value={view.auditRetention ?? ""}
                onChange={e => patch({ auditRetention: e.target.value })}
              />
            </label>
            <div className="mem-hint">{t("ndv.sets.auditNote")}</div>
          </Section>

          <Section title={t("ndv.sets.opsTitle")} desc={t("ndv.sets.opsDesc")}>
            <div className="mem-hint">{t("ndv.sets.opsNote")}</div>
          </Section>
        </>
      )}

      {/* 设备编辑表单 */}
      {editingDevice && (
        <L3Panel crumbs={[t("ndv.sets.crumbInv"), view.devices.some(d => d.name === editingDevice.name) ? t("ndv.sets.editX", { name: editingDevice.name }) : t("ndv.sets.addDevice")]} onBack={() => setEditingDevice(null)} confirmDiscard>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label={t("ndv.sets.fName")}><input className="mem-input" value={editingDevice.name} onChange={e => setEditingDevice({ ...editingDevice, name: e.target.value })} /></Field>
            <Field label={t("ndv.sets.fVendor")}>
              <select className="mem-select" value={editingDevice.vendor} onChange={e => setEditingDevice({ ...editingDevice, vendor: e.target.value, os: (OSES[e.target.value] ?? [""])[0] })}>
                {VENDORS.map(v => <option key={v} value={v}>{v}</option>)}
              </select>
            </Field>
            <Field label={t("ndv.sets.fOs")}>
              <select className="mem-select" value={editingDevice.os} onChange={e => setEditingDevice({ ...editingDevice, os: e.target.value })}>
                {(OSES[editingDevice.vendor] ?? []).map(o => <option key={o} value={o}>{o}</option>)}
              </select>
            </Field>
            <Field label={t("ndv.sets.fModel")}><input className="mem-input" value={editingDevice.model} onChange={e => setEditingDevice({ ...editingDevice, model: e.target.value })} /></Field>
            <Field label={t("ndv.sets.fAddress")}><input className="mem-input" value={editingDevice.address} onChange={e => setEditingDevice({ ...editingDevice, address: e.target.value })} /></Field>
            <Field label={t("ndv.sets.fPort")}><input className="mem-input" type="number" value={editingDevice.port} onChange={e => setEditingDevice({ ...editingDevice, port: Number(e.target.value) || 22 })} /></Field>
            <Field label={t("ndv.sets.fGroup")}>
              <input
                className="mem-input" list="ndv-groups" placeholder={t("ndv.sets.phGroup")}
                value={editingDevice.group ?? ""}
                onChange={e => setEditingDevice({ ...editingDevice, group: e.target.value })}
              />
              <datalist id="ndv-groups">
                {(view.groups ?? []).map(g => <option key={g} value={g} />)}
              </datalist>
            </Field>
            <Field label={t("ndv.sets.fUser")}><input className="mem-input" value={editingDevice.username} onChange={e => setEditingDevice({ ...editingDevice, username: e.target.value })} /></Field>
            <Field label={editingDevice.passwordSet ? t("ndv.sets.phPwdKeep") : t("ndv.sets.phPwd")}>
              <input className="mem-input" type="password" value={editingDevice.password} onChange={e => setEditingDevice({ ...editingDevice, password: e.target.value })} />
            </Field>
            <Field label={t("ndv.sets.fVia")}>
              <input className="mem-input" value={(editingDevice.via ?? []).join(",")}
                onChange={e => setEditingDevice({ ...editingDevice, via: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })} />
            </Field>
            <Field label={t("ndv.sets.fEncoding")}>
              <select className="mem-select" value={editingDevice.encoding || "auto"} onChange={e => setEditingDevice({ ...editingDevice, encoding: e.target.value })}>
                {["auto", "utf-8", "gbk"].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
            <Field label={t("ndv.sets.fLogPaths")}>
              <input
                className="mem-input" placeholder={t("ndv.sets.phLogPaths")}
                value={(editingDevice.logPaths ?? []).join(",")}
                onChange={e => setEditingDevice({ ...editingDevice, logPaths: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
              />
            </Field>
            <Field label={t("ndv.sets.fConfigPaths")}>
              <input
                className="mem-input" placeholder={t("ndv.sets.phConfigPaths")}
                value={(editingDevice.configPaths ?? []).join(",")}
                onChange={e => setEditingDevice({ ...editingDevice, configPaths: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })}
              />
            </Field>
            <Field label={t("ndv.sets.fOob")}>
              <input
                className="mem-input" placeholder={t("ndv.sets.phOob")}
                value={editingDevice.oobUrl ?? ""}
                onChange={e => setEditingDevice({ ...editingDevice, oobUrl: e.target.value })}
              />
            </Field>
            <Field label={t("ndv.sets.fSnmp")}>
              <select className="mem-select" value={editingDevice.snmpVersion ?? ""}
                onChange={e => setEditingDevice({ ...editingDevice, snmpVersion: e.target.value })}>
                <option value="">{t("ndv.sets.optOff")}</option>
                <option value="v2c">v2c</option>
              </select>
            </Field>
            <Field label={editingDevice.snmpCommunitySet ? t("ndv.sets.fSnmpCommKeep") : t("ndv.sets.fSnmpComm")}>
              <input className="mem-input" type="password" value={editingDevice.snmpCommunity ?? ""}
                onChange={e => setEditingDevice({ ...editingDevice, snmpCommunity: e.target.value })} />
            </Field>
            <Field label={t("ndv.sets.fProtocols")}>
              <input className="mem-input" placeholder="ssh, netconf"
                value={(editingDevice.protocols ?? []).join(", ")}
                onChange={e => setEditingDevice({ ...editingDevice, protocols: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })} />
            </Field>
            <Field label={t("ndv.sets.fKind")}>
              <select className="mem-select" value={editingDevice.kind ?? ""} onChange={e => setEditingDevice({ ...editingDevice, kind: e.target.value })}>
                <option value="">{t("ndv.sets.kAuto")}</option>
                <option value="docker">{t("ndv.sets.kDocker")}</option>
                <option value="k8s">{t("ndv.sets.kK8s")}</option>
                <option value="firewall">{t("ndv.sets.kFirewall")}</option>
              </select>
              <span style={{ opacity: 0.6, fontSize: 11 }}>{t("ndv.sets.kindNote")}</span>
            </Field>
            {(editingDevice.kind ?? "") === "docker" && (
              <Field label={t("ndv.sets.fDockerSock")}>
                <input className="mem-input" placeholder={t("ndv.sets.phDockerSock")}
                  value={editingDevice.dockerSocket ?? ""}
                  onChange={e => setEditingDevice({ ...editingDevice, dockerSocket: e.target.value })} />
              </Field>
            )}
            {(editingDevice.kind ?? "") === "k8s" && (
              <>
                <Field label={editingDevice.k8sKubeconfigSet ? t("ndv.sets.fKcKeep") : t("ndv.sets.fKc")}>
                  <textarea className="mem-input" rows={4} style={{ width: "100%", fontFamily: "var(--font-mono, monospace)", fontSize: 11 }}
                    placeholder="apiVersion: v1&#10;kind: Config&#10;…"
                    value={editingDevice.k8sKubeconfig ?? ""}
                    onChange={e => setEditingDevice({ ...editingDevice, k8sKubeconfig: e.target.value })} />
                </Field>
                <Field label={t("ndv.sets.fContext")}>
                  <input className="mem-input" placeholder={t("ndv.sets.phContext")}
                    value={editingDevice.k8sContext ?? ""}
                    onChange={e => setEditingDevice({ ...editingDevice, k8sContext: e.target.value })} />
                </Field>
                <Field label={t("ndv.sets.fNsWhitelist")}>
                  <input className="mem-input" placeholder="prod, kube-system"
                    value={(editingDevice.k8sNamespaces ?? []).join(", ")}
                    onChange={e => setEditingDevice({ ...editingDevice, k8sNamespaces: e.target.value.split(/[,，]/).map(s => s.trim()).filter(Boolean) })} />
                </Field>
              </>
            )}
            {(editingDevice.kind ?? "") === "firewall" && (
              <Field label={editingDevice.fwApiTokenSet ? t("ndv.sets.fTokenKeep") : t("ndv.sets.fToken")}>
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
              title={t("ndv.sets.testTip")}
            >{testing ? t("ndv.sets.testing") : t("ndv.sets.testConn")}</span>
            {(editingDevice.passwordSet || editingDevice.snmpCommunitySet || editingDevice.fwApiTokenSet || editingDevice.k8sKubeconfigSet) && (
              <span className="btn btn--secondary btn--small" role="button" title={t("ndv.sets.clearCredsTip")}
                onClick={() => void clearCreds(t("ndv.sets.devLabel", { name: editingDevice.name }), [
                  { kind: "password", env: editingDevice.passwordEnv, what: t("ndv.sets.whatPwd") },
                  { kind: "password", env: editingDevice.snmpCommunityEnv, what: t("ndv.sets.whatSnmp") },
                  { kind: "api-token", env: editingDevice.fwApiTokenEnv, what: t("ndv.sets.whatToken") },
                  { kind: "kubeconfig", env: editingDevice.k8sKubeconfigEnv, what: "kubeconfig" },
                ], () => setEditingDevice(d => d && ({ ...d, passwordEnv: "", snmpCommunityEnv: "", fwApiTokenEnv: "", k8sKubeconfigEnv: "", passwordSet: false, snmpCommunitySet: false, fwApiTokenSet: false, k8sKubeconfigSet: false })))}>{t("ndv.sets.clearCredsBtn2")}</span>
            )}
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDevice(null)}>{t("common.cancel")}</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingDevice.name.trim() || !editingDevice.address.trim()) { setErr(t("ndv.sets.needNameAddr")); return; }
                const exists = view.devices.some(d => d.name === editingDevice.name);
                const devices = exists ? view.devices.map(d => d.name === editingDevice.name ? editingDevice : d) : [...view.devices, editingDevice];
                setEditingDevice(null);
                void save({ ...view, devices, notifySMTPPassword });
              }}
            >{t("ndv.sets.saveDevice")}</span>
          </div>
        </L3Panel>
      )}

      {/* 数据库源编辑表单 */}
      {editingDB && (
        <L3Panel crumbs={[t("ndv.sets.crumbOps"), view.dbSources?.some(s => s.name === editingDB.name) ? t("ndv.sets.editDb") : t("ndv.sets.addDb")]} onBack={() => setEditingDB(null)} confirmDiscard>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label={t("ndv.sets.fName")}><input className="mem-input" value={editingDB.name} onChange={e => setEditingDB({ ...editingDB, name: e.target.value })} /></Field>
            <Field label={t("ndv.sets.fType")}>
              <select className="mem-select" value={editingDB.type} onChange={e => {
                const t = e.target.value;
                const DEF_PORTS: Record<string, number> = { mysql: 3306, postgres: 5432, redis: 6379, mongodb: 27017, mssql: 1433, clickhouse: 8123, elasticsearch: 9200 };
                setEditingDB({ ...editingDB, type: t, port: DEF_PORTS[t] ?? 3306 });
              }}>
                {["mysql", "postgres", "redis", "mongodb", "mssql", "clickhouse", "elasticsearch"].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
            <Field label={t("ndv.sets.fAddress")}><input className="mem-input" value={editingDB.host} onChange={e => setEditingDB({ ...editingDB, host: e.target.value })} /></Field>
            <Field label={t("ndv.sets.fPort")}><input className="mem-input" type="number" value={editingDB.port} onChange={e => setEditingDB({ ...editingDB, port: Number(e.target.value) || 3306 })} /></Field>
            <Field label={t("ndv.sets.fRoAccount")}><input className="mem-input" value={editingDB.username} onChange={e => setEditingDB({ ...editingDB, username: e.target.value })} /></Field>
            <Field label={editingDB.passwordSet ? t("ndv.sets.phPwdKeep") : t("ndv.sets.phPwd")}>
              <input className="mem-input" type="password" value={editingDB.password} onChange={e => setEditingDB({ ...editingDB, password: e.target.value })} />
            </Field>
            <Field label={t("ndv.sets.fDatabase")}><input className="mem-input" value={editingDB.database} onChange={e => setEditingDB({ ...editingDB, database: e.target.value })} /></Field>
          </div>
          <div style={{ marginTop: 8 }}>
            <div className="set-label" style={{ marginBottom: 4 }}>{t("ndv.sets.allowlistLabel")}</div>
            <textarea
              className="mem-input" rows={5} style={{ width: "100%", fontFamily: "var(--font-mono, monospace)", fontSize: 11.5 }}
              placeholder={editingDB.type === "redis" ? t("ndv.sets.phRedisAllow") : "SHOW PROCESSLIST\nSHOW ENGINE INNODB STATUS\nSELECT * FROM information_schema.processlist"}
              value={(editingDB.allowlist ?? []).join("\n")}
              onChange={e => setEditingDB({ ...editingDB, allowlist: e.target.value.split("\n").map(s => s.trim()).filter(Boolean) })}
            />
          </div>
          <div style={{ marginTop: 8 }}>
            <div className="set-label" style={{ marginBottom: 4 }}>{t("ndv.sets.viaLabel")}</div>
            <input
              className="mem-input" style={{ width: "100%" }}
              placeholder={t("ndv.sets.phVia")}
              value={(editingDB.via ?? []).join(", ")}
              onChange={e => setEditingDB({ ...editingDB, via: e.target.value.split(/[,，]/).map(x => x.trim()).filter(Boolean) })}
            />
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            {editingDB.passwordSet && (
              <span className="btn btn--secondary btn--small" role="button" title={t("ndv.sets.clearDbTip")}
                onClick={() => void clearCreds(t("ndv.sets.dbLabel", { name: editingDB.name }), [
                  { kind: "password", env: editingDB.passwordEnv, what: t("ndv.sets.whatDbPwd") },
                ], () => setEditingDB(s => s && ({ ...s, passwordEnv: "", passwordSet: false })))}>{t("ndv.sets.clearCredsBtn2")}</span>
            )}
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingDB(null)}>{t("common.cancel")}</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingDB.name.trim() || !editingDB.host.trim()) { setErr(t("ndv.sets.needNameAddr")); return; }
                if (editingDB.type !== "redis" && (editingDB.allowlist ?? []).length === 0) { setErr(t("ndv.sets.needAllowlist")); return; }
                const exists = (view.dbSources ?? []).some(s => s.name === editingDB.name);
                const dbSources = exists ? (view.dbSources ?? []).map(s => s.name === editingDB.name ? editingDB : s) : [...(view.dbSources ?? []), editingDB];
                setEditingDB(null);
                void save({ ...view, dbSources, notifySMTPPassword });
              }}
            >{t("ndv.sets.saveSource")}</span>
          </div>
        </L3Panel>
      )}

      {/* 告警规则编辑表单 */}
      {editingRule && (
        <L3Panel crumbs={[t("ndv.sets.crumbOps"), (view.alertRules ?? []).some(r => r.name === editingRule.name) ? t("ndv.sets.editRule") : t("ndv.sets.addRule")]} onBack={() => setEditingRule(null)} confirmDiscard>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label={t("ndv.sets.fName")}><input className="mem-input" value={editingRule.name} onChange={e => setEditingRule({ ...editingRule, name: e.target.value })} /></Field>
            <Field label={t("ndv.sets.fMetric")}>
              <select className="mem-select" value={editingRule.metric} onChange={e => setEditingRule({ ...editingRule, metric: e.target.value, value: e.target.value === "reachable" || e.target.value === "uptime_reset" ? 0 : 1 })}>
                <option value="reachable">{t("ndv.sets.mReachable")}</option>
                <option value="if_down_count">{t("ndv.sets.mIfDown")}</option>
                <option value="uptime_reset">{t("ndv.sets.mReboot")}</option>
                <option value="flap_count">{t("ndv.sets.mFlap")}</option>
                <option value="if_down_above_p90">{t("ndv.sets.mDrift")}</option>
              </select>
            </Field>
            <Field label={t("ndv.sets.fOp")}>
              <select className="mem-select" value={editingRule.op || ">="} onChange={e => setEditingRule({ ...editingRule, op: e.target.value })}>
                {["==", ">=", "<="].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
            <Field label={t("ndv.sets.fValue")}><input className="mem-input" type="number" value={editingRule.value} onChange={e => setEditingRule({ ...editingRule, value: Number(e.target.value) || 0 })} /></Field>
            <Field label={t("ndv.sets.fSeverity")}>
              <select className="mem-select" value={editingRule.severity || "warning"} onChange={e => setEditingRule({ ...editingRule, severity: e.target.value })}>
                {["info", "warning", "critical"].map(x => <option key={x} value={x}>{x}</option>)}
              </select>
            </Field>
            <Field label={t("ndv.sets.fEnabled")}>
              <select className="mem-select" value={editingRule.enabled ? "1" : "0"} onChange={e => setEditingRule({ ...editingRule, enabled: e.target.value === "1" })}>
                <option value="1">{t("ndv.sets.optOn")}</option>
                <option value="0">{t("ndv.sets.optOff2")}</option>
              </select>
            </Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingRule(null)}>{t("common.cancel")}</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingRule.name.trim()) { setErr(t("ndv.sets.needRuleName")); return; }
                const exists = (view.alertRules ?? []).some(r => r.name === editingRule.name);
                const alertRules = exists ? (view.alertRules ?? []).map(r => r.name === editingRule.name ? editingRule : r) : [...(view.alertRules ?? []), editingRule];
                setEditingRule(null);
                void save({ ...view, alertRules, notifySMTPPassword });
              }}
            >{t("ndv.sets.saveRule")}</span>
          </div>
        </L3Panel>
      )}

      {/* 跳板编辑表单 */}
      {editingHop && (
        <L3Panel crumbs={[t("ndv.sets.crumbOps"), view.hops.some(h => h.name === editingHop.name) ? t("ndv.sets.editHop") : t("ndv.sets.addHop")]} onBack={() => setEditingHop(null)} confirmDiscard>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
            <Field label={t("ndv.sets.fName")}><input className="mem-input" value={editingHop.name} onChange={e => setEditingHop({ ...editingHop, name: e.target.value })} /></Field>
            <Field label={t("ndv.sets.fAddress")}><input className="mem-input" value={editingHop.host} onChange={e => setEditingHop({ ...editingHop, host: e.target.value })} /></Field>
            <Field label={t("ndv.sets.fPort")}><input className="mem-input" type="number" value={editingHop.port} onChange={e => setEditingHop({ ...editingHop, port: Number(e.target.value) || 22 })} /></Field>
            <Field label={t("ndv.sets.phUser")}><input className="mem-input" value={editingHop.user} onChange={e => setEditingHop({ ...editingHop, user: e.target.value })} /></Field>
            <Field label={editingHop.passwordSet ? t("ndv.sets.phPwdKeep") : t("ndv.sets.phPwd")}>
              <input className="mem-input" type="password" value={editingHop.password} onChange={e => setEditingHop({ ...editingHop, password: e.target.value })} />
            </Field>
            <Field label={t("ndv.sets.fProxyJump")}><input className="mem-input" value={editingHop.proxyJump} onChange={e => setEditingHop({ ...editingHop, proxyJump: e.target.value })} /></Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            {editingHop.passwordSet && (
              <span className="btn btn--secondary btn--small" role="button" title={t("ndv.sets.clearHopTip")}
                onClick={() => void clearCreds(t("ndv.sets.hopLabel", { name: editingHop.name }), [
                  { kind: "password", env: editingHop.passwordEnv, what: t("ndv.sets.whatPwd") },
                ], () => setEditingHop(h => h && ({ ...h, passwordEnv: "", passwordSet: false })))}>{t("ndv.sets.clearCredsBtn2")}</span>
            )}
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingHop(null)}>{t("common.cancel")}</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingHop.name.trim() || !editingHop.host.trim()) { setErr(t("ndv.sets.needNameAddr")); return; }
                const exists = view.hops.some(h => h.name === editingHop.name);
                const hops = exists ? view.hops.map(h => h.name === editingHop.name ? editingHop : h) : [...view.hops, editingHop];
                setEditingHop(null);
                void save({ ...view, hops });
              }}
            >{t("ndv.sets.saveHop")}</span>
          </div>
        </L3Panel>
      )}

      {/* 项目编辑表单 */}
      {editingProject && (
        <L3Panel crumbs={[t("ndv.sets.crumbOps"), editingProject.index >= 0 ? t("ndv.sets.editProject") : t("ndv.sets.newProject")]} onBack={() => setEditingProject(null)} confirmDiscard>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <Field label={t("ndv.sets.fName")}>
              <input className="mem-input" placeholder={t("ndv.sets.phProject")} value={editingProject.draft.name}
                onChange={e => setEditingProject({ ...editingProject, draft: { ...editingProject.draft, name: e.target.value } })} />
            </Field>
            <Field label={t("ndv.sets.fGroups")}>
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
                {(view.groups ?? []).length === 0 && <span style={{ opacity: 0.55, fontSize: 11.5 }}>{t("ndv.sets.noGroupsYet")}</span>}
              </span>
            </Field>
            <Field label={t("ndv.sets.fNote")}>
              <input className="mem-input" value={editingProject.draft.note}
                onChange={e => setEditingProject({ ...editingProject, draft: { ...editingProject.draft, note: e.target.value } })} />
            </Field>
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingProject(null)}>{t("common.cancel")}</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingProject.draft.name.trim()) { setErr(t("ndv.sets.needProjectName")); return; }
                const projects = [...(view.projects ?? [])];
                if (editingProject.index >= 0) projects[editingProject.index] = editingProject.draft;
                else projects.push(editingProject.draft);
                setEditingProject(null);
                void save({ ...view, projects });
              }}
            >{t("ndv.sets.saveProject")}</span>
          </div>
        </L3Panel>
      )}

      {/* 诊断组合编辑表单 */}
      {editingPreset && (
        <L3Panel crumbs={[t("ndv.sets.crumbOps"), editingPreset.index >= 0 ? t("ndv.sets.editPreset") : t("ndv.sets.newPreset")]} onBack={() => setEditingPreset(null)} confirmDiscard>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <Field label={t("ndv.sets.fName")}>
              <input className="mem-input" placeholder={t("ndv.sets.phPreset")} value={editingPreset.draft.name}
                onChange={e => setEditingPreset({ ...editingPreset, draft: { ...editingPreset.draft, name: e.target.value } })} />
            </Field>
            <Field label={t("ndv.sets.fCommands")}>
              <textarea
                className="mem-input" rows={5} style={{ width: "100%", resize: "vertical", fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace", fontSize: 12 }}
                placeholder={"display interface brief\ndisplay interface description"}
                value={editingPreset.draft.commands.join("\n")}
                onChange={e => setEditingPreset({ ...editingPreset, draft: { ...editingPreset.draft, commands: e.target.value.split(/[;\n]/).map(c => c.trim()).filter(Boolean) } })}
              />
            </Field>
            <Field label={t("ndv.sets.fVendors")}>
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
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditingPreset(null)}>{t("common.cancel")}</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={() => {
                if (!editingPreset.draft.name.trim() || editingPreset.draft.commands.length === 0) { setErr(t("ndv.sets.needPreset")); return; }
                const presets = [...(view.presets ?? [])];
                if (editingPreset.index >= 0) presets[editingPreset.index] = editingPreset.draft;
                else presets.push(editingPreset.draft);
                setEditingPreset(null);
                void save({ ...view, presets });
              }}
            >{t("ndv.sets.savePreset")}</span>
          </div>
        </L3Panel>
      )}

      {/* 扫描导入弹框 */}
      {scanOpen && (
        <Modal title={t("ndv.sets.importNmap")} onClose={() => setScanOpen(false)}>
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <div className="mem-hint">{t("ndv.sets.scanModalNote")}</div>
            <textarea
              className="mem-input" rows={12} style={{ width: "100%", resize: "vertical", fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace", fontSize: 11.5 }}
              placeholder='<?xml version="1.0"?>&#10;<nmaprun>…</nmaprun>'
              value={scanXml}
              onChange={e => setScanXml(e.target.value)}
            />
          </div>
          <div style={{ marginTop: 10, display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setScanOpen(false)}>{t("common.cancel")}</span>
            <span
              className="btn btn--primary btn--small" role="button"
              onClick={async () => {
                if (!scanXml.trim()) return;
                setScanBusy(true);
                try {
                  const f = await app.NetDevImportNmap(scanXml);
                  showToast(f ? f.title : t("ndv.sets.importDone"), "info");
                  setScanOpen(false);
                } catch (e) { setErr(String(e)); }
                finally { setScanBusy(false); }
              }}
            >{scanBusy ? t("ndv.sec.importing") : t("ndv.sets.importBtn")}</span>
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
  const t = useT();
  const back = useCallback(async () => {
    if (confirmDiscard) {
      const ok = await confirm({ title: t("ndv.sets.discardTitle"), message: t("ndv.sets.discardMsg"), danger: true, confirmLabel: t("ndv.sets.discardBtn") });
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
        <button className="btn btn--secondary btn--small" onClick={() => void back()}>← {t("ndv.sets.back")}</button>
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
