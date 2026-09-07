import { useCallback, useEffect, useState } from "react";
import { Play, Plus, Trash2 } from "lucide-react";
import { app } from "../../lib/bridge";
import { useI18n } from "../../lib/i18n";
import type { NetDevDeviceView } from "../../lib/types";

// AuditProjectPanel（SCENARIO_SPEC S5）：项目经理按项目发起上线前安全审计。
// 项目 = 设备清单 + 联系人 + 上线窗口 + 套餐勾选；发起跑套餐电池（Go 侧
// RunProjectAudit，信封闸不绕过）；风险清单逐项 open/fixed/accepted，全绿
// （fixed∨accepted）才放行；复扫按签名自动转 fixed。

interface AuditProject {
  id: string;
  name: string;
  devices: string[];
  contacts?: string;
  launch_window?: string;
  checklist: string[];
  created_at: string;
}

interface AuditItem {
  finding_id: string;
  signature: string;
  title: string;
  device: string;
  severity: string;
  fix?: { type?: string; ref?: string; link?: string; confidence?: string };
  status: "open" | "fixed" | "accepted";
  first_seen: string;
  last_scan: string;
}

interface AuditReport {
  project_id: string;
  at: string;
  items: AuditItem[];
  battery_notes?: Record<string, string>;
}

const STAGES: { key: string; labelKey: string }[] = [
  { key: "baseline", labelKey: "ndv.ap.stBaseline" },
  { key: "vuln", labelKey: "ndv.ap.stVuln" },
  { key: "logs", labelKey: "ndv.ap.stLogs" },
  { key: "exposure", labelKey: "ndv.ap.stExposure" },
  { key: "weakcred", labelKey: "ndv.ap.stWeakcred" },
];

export function AuditProjectPanel({ devices }: { devices: NetDevDeviceView[] }) {
  const { t } = useI18n();
  const [projects, setProjects] = useState<AuditProject[]>([]);
  const [reports, setReports] = useState<Record<string, AuditReport | null>>({});
  const [greens, setGreens] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const [editing, setEditing] = useState<AuditProject | null>(null);

  const reload = useCallback(async () => {
    try {
      const list = (await app.NetDevAuditProjects()) as unknown as AuditProject[] | null;
      setProjects(list ?? []);
    } catch (e) {
      setErr(String(e));
    }
  }, []);

  useEffect(() => { void reload(); }, [reload]);

  // 状态逐项目拉取（Go 桥返回 {project, report, green} 包装结构）。
  const loadStatus = useCallback(async (id: string) => {
    try {
      const r = (await app.NetDevAuditProjectStatus(id)) as unknown as { report?: AuditReport | null; green?: boolean };
      setReports(prev => ({ ...prev, [id]: (r && "report" in r ? r.report : null) ?? null }));
      setGreens(prev => ({ ...prev, [id]: Boolean(r && "green" in r && r.green) }));
    } catch { /* best-effort: 状态拉取失败不阻塞列表 */ }
  }, []);

  useEffect(() => { for (const p of projects) void loadStatus(p.id); }, [projects, loadStatus]);

  const run = async (id: string) => {
    setBusy(id);
    setErr("");
    try {
      const rep = (await app.NetDevAuditProjectRun(id)) as unknown as AuditReport;
      setReports(prev => ({ ...prev, [id]: rep }));
      await loadStatus(id);
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy("");
    }
  };

  const setItem = async (pid: string, sig: string, status: string) => {
    try {
      await app.NetDevAuditItemSetStatus(pid, sig, status);
      await loadStatus(pid);
    } catch (e) {
      setErr(String(e));
    }
  };

  const newProject = () => {
    setEditing({ id: "", name: "", devices: [], checklist: ["baseline", "vuln", "logs"], contacts: "", launch_window: "", created_at: "" });
  };

  const save = async () => {
    if (!editing) return;
    try {
      const saved = (await app.NetDevAuditProjectSave(editing as never)) as unknown as AuditProject;
      setEditing(null);
      setProjects(prev => [saved, ...prev.filter(p => p.id !== saved.id)]);
    } catch (e) {
      setErr(String(e));
    }
  };

  const remove = async (id: string) => {
    try {
      await app.NetDevAuditProjectDelete(id);
      setProjects(prev => prev.filter(p => p.id !== id));
    } catch (e) {
      setErr(String(e));
    }
  };

  return (
    <div>
      <div style={{ display: "flex", gap: 6, alignItems: "center", marginBottom: 8 }}>
        <span className="btn btn--primary btn--small" role="button" onClick={newProject}><Plus size={12} /> {t("ndv.ap.new")}</span>
        {err && <span className="ndv__hint" style={{ color: "var(--danger, #e5484d)" }}>{err}</span>}
      </div>

      {editing && (
        <div className="ndv__card" style={{ padding: 10, marginBottom: 8 }}>
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginBottom: 6 }}>
            <input className="mem-input" style={{ width: 140 }} placeholder={t("ndv.ap.phName")} value={editing.name}
              onChange={e => setEditing({ ...editing, name: e.target.value })} />
            <input className="mem-input" style={{ width: 140 }} placeholder={t("ndv.ap.phContacts")} value={editing.contacts ?? ""}
              onChange={e => setEditing({ ...editing, contacts: e.target.value })} />
            <input className="mem-input" style={{ width: 110 }} placeholder={t("ndv.ap.phWindow")} value={editing.launch_window ?? ""}
              onChange={e => setEditing({ ...editing, launch_window: e.target.value })} />
          </div>
          <div className="ndv__group-label">{t("ndv.ap.devices")}</div>
          <div style={{ display: "flex", gap: 4, flexWrap: "wrap", marginBottom: 6 }}>
            {devices.map(d => (
              <label key={d.name} style={{ fontSize: 11.5, display: "flex", gap: 3, alignItems: "center", border: "1px solid var(--border-soft)", borderRadius: 8, padding: "1px 6px" }}>
                <input type="checkbox" checked={editing.devices.includes(d.name)}
                  onChange={e => setEditing({ ...editing, devices: e.target.checked ? [...editing.devices, d.name] : editing.devices.filter(n => n !== d.name) })} />
                {d.name}
              </label>
            ))}
          </div>
          <div className="ndv__group-label">{t("ndv.ap.stages")}</div>
          <div style={{ display: "flex", gap: 4, flexWrap: "wrap", marginBottom: 8 }}>
            {STAGES.map(s => (
              <label key={s.key} style={{ fontSize: 11.5, display: "flex", gap: 3, alignItems: "center", border: "1px solid var(--border-soft)", borderRadius: 8, padding: "1px 6px" }}>
                <input type="checkbox" checked={editing.checklist.includes(s.key)}
                  onChange={e => setEditing({ ...editing, checklist: e.target.checked ? [...editing.checklist, s.key] : editing.checklist.filter(k => k !== s.key) })} />
                {t(s.labelKey as never)}
              </label>
            ))}
          </div>
          <span className="btn btn--primary btn--small" role="button" onClick={() => void save()}>{t("common.save")}</span>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => setEditing(null)}>{t("common.cancel")}</span>
        </div>
      )}

      {projects.length === 0 && <div className="ndv__hint">{t("ndv.ap.empty")}</div>}
      {projects.map(p => {
        const rep = reports[p.id];
        const green = greens[p.id];
        return (
          <div key={p.id} className="ndv__card" style={{ padding: 10, marginBottom: 8 }}>
            <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
              <span style={{ fontWeight: 600 }}>{p.name}</span>
              <span className="ndv__meta">{p.devices.length} {t("ndv.ap.devUnit")} · {p.launch_window || "—"}</span>
              {rep && <span className={`ndv__badge${green ? "" : " ndv__badge--warn"}`}>{green ? "🟢 " + t("ndv.ap.green") : `🔴 ${rep.items.filter(i => i.status === "open").length} ${t("ndv.ap.openUnit")}`}</span>}
              <span style={{ flex: 1 }} />
              <span className="btn btn--primary btn--small" role="button" aria-disabled={busy === p.id} style={busy === p.id ? { opacity: 0.5, pointerEvents: "none" } : undefined} onClick={() => void run(p.id)}>
                <Play size={11} /> {busy === p.id ? t("ndv.ap.running") : t("ndv.ap.run")}
              </span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void remove(p.id)} title={t("ndv.ap.delTip")}><Trash2 size={11} /></span>
            </div>
            {rep?.battery_notes && (
              <div className="ndv__meta" style={{ marginTop: 4, fontSize: 11 }}>
                {Object.entries(rep.battery_notes).map(([k, v]) => `${t(("ndv.ap.note." + k) as never)}: ${v}`).join(" · ")}
              </div>
            )}
            {rep && rep.items.length > 0 && (
              <div style={{ marginTop: 6 }}>
                {rep.items.map(it => (
                  <div key={it.signature} style={{ display: "flex", gap: 6, alignItems: "center", fontSize: 11.5, padding: "2px 0", borderTop: "1px solid var(--border-soft)" }}>
                    <span style={{ width: 70 }}>{it.device}</span>
                    <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={it.title + (it.fix ? `（fix: ${it.fix.type} → ${it.fix.ref}）` : "")}>
                      {it.severity === "critical" ? "🔴" : "⚠"} {it.title}{it.fix ? ` · ${it.fix.type}→${it.fix.ref}` : ""}
                    </span>
                    {it.status === "open" ? (
                      <>
                        <span className="btn btn--secondary btn--small" role="button" onClick={() => void setItem(p.id, it.signature, "fixed")}>{t("ndv.ap.markFixed")}</span>
                        <span className="btn btn--secondary btn--small" role="button" onClick={() => void setItem(p.id, it.signature, "accepted")}>{t("ndv.ap.markAccepted")}</span>
                      </>
                    ) : (
                      <span className="ndv__meta">{it.status === "fixed" ? "✓ " + t("ndv.ap.stFixed") : "◉ " + t("ndv.ap.stAccepted")}</span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
