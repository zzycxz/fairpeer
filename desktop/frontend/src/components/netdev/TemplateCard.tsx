// TemplateCard — 批量模板（NETDEV_SPEC_V2 §7.2）的家：「变更」页签内的「模
// 板」入口（§10.6 映射表）。模板 = 步骤序列 + 变量；渲染时逐台 dry-run 预
// 览（每条标注分类与危险动词，无任何副作用），人审的是「N 份渲染结果」整
// 体；生成即一份多设备步骤的变更草稿——滚动执行/首败冻结/回滚矩阵全部沿
// 用变更既有管线。
import { useCallback, useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import type { NetDevDeviceView, NetDevTemplate, NetDevTemplatePreviewDevice } from "../../lib/types";

interface DraftTpl {
  name: string;
  intent: string;
  vars: string;
  commands: string;
  rollback: string;
}

const EMPTY_DRAFT: DraftTpl = { name: "", intent: "", vars: "", commands: "", rollback: "" };

export function TemplateCard({ devices, onDrafted }: { devices: NetDevDeviceView[]; onDrafted: () => void }) {
  const tr = useT();
  const [templates, setTemplates] = useState<NetDevTemplate[] | null>(null);
  const [activeID, setActiveID] = useState("");
  const [vars, setVars] = useState<Record<string, string>>({});
  const [preview, setPreview] = useState<NetDevTemplatePreviewDevice[] | null>(null);
  const [targets, setTargets] = useState<Record<string, boolean>>({});
  const [draft, setDraft] = useState<DraftTpl | null>(null);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const [note, setNote] = useState("");

  const reload = useCallback(async () => {
    try {
      setTemplates(await app.NetDevTemplates());
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, []);
  useEffect(() => { void reload(); }, [reload]);

  const tpl = templates?.find(t => t.id === activeID) ?? null;

  const render = async () => {
    if (!tpl) return;
    setBusy("render");
    try {
      setPreview(await app.NetDevTemplateRender(tpl.id, vars));
      setErr("");
    } catch (e) {
      setErr(String(e));
    } finally { setBusy(""); }
  };

  const apply = async () => {
    if (!tpl) return;
    setBusy("apply");
    try {
      const p = await app.NetDevTemplateApply(tpl.id, vars);
      setNote(tr("ndv.tpl.proposalDrafted", { id: p.id }));
      setErr("");
      onDrafted();
    } catch (e) {
      setErr(String(e));
    } finally { setBusy(""); }
  };

  const saveDraft = async () => {
    if (!draft) return;
    setBusy("save");
    try {
      const t = await app.NetDevTemplateSave({
        id: "",
        name: draft.name,
        intent: draft.intent,
        vars: draft.vars.split(/[,，\s]+/).filter(Boolean),
        steps: [{
          commands: draft.commands.split("\n").map(s => s.trim()).filter(Boolean),
          rollback: draft.rollback.split("\n").map(s => s.trim()).filter(Boolean),
        }],
        targets: Object.entries(targets).filter(([, v]) => v).map(([k]) => k),
        created_at: "",
      });
      setDraft(null);
      await reload();
      setActiveID(t.id);
      setNote(tr("ndv.tpl.saved"));
    } catch (e) {
      setErr(String(e));
    } finally { setBusy(""); }
  };

  return (
    <div className="ndv__card">
      <div className="ndv__card-title">
        📋 {tr("ndv.tpl.title", { n: templates?.length ?? 0 })}
        <span
          className="btn btn--secondary btn--small"
          role="button"
          style={{ marginLeft: "auto" }}
          onClick={() => { setDraft(draft ? null : { ...EMPTY_DRAFT }); setNote(""); }}
        >{draft ? tr("ndv.tpl.cancelNew") : tr("ndv.tpl.newBtn")}</span>
      </div>
      <div className="ndv__hint ndv__hint--flush">{tr("ndv.tpl.hint1")}</div>
      {err && <div className="ndv__hint ndv__hint--err">{err}</div>}
      {note && <div className="ndv__hint">{note}</div>}

      {draft && (
        <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 6 }}>
          <input className="mem-input" placeholder={tr("ndv.tpl.phName")} value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} />
          <input className="mem-input" placeholder={tr("ndv.tpl.phIntent")} value={draft.intent} onChange={e => setDraft({ ...draft, intent: e.target.value })} />
          <input className="mem-input" placeholder={tr("ndv.tpl.phVars")} value={draft.vars} onChange={e => setDraft({ ...draft, vars: e.target.value })} />
          <textarea className="mem-input" rows={3} placeholder={tr("ndv.tpl.phCommands")} value={draft.commands} onChange={e => setDraft({ ...draft, commands: e.target.value })} />
          <textarea className="mem-input" rows={2} placeholder={tr("ndv.tpl.phRollback")} value={draft.rollback} onChange={e => setDraft({ ...draft, rollback: e.target.value })} />
          <div className="ndv__group-label">{tr("ndv.tpl.targets")}</div>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
            {devices.map(d => (
              <label key={d.name} className="ndv__meta" style={{ display: "inline-flex", gap: 4, alignItems: "center" }}>
                <input type="checkbox" checked={!!targets[d.name]} onChange={e => setTargets({ ...targets, [d.name]: e.target.checked })} />
                {d.name}
              </label>
            ))}
          </div>
          <div>
            <span className="btn btn--primary btn--small" role="button" onClick={() => void saveDraft()}>{busy === "save" ? tr("ndv.tpl.saving") : tr("ndv.tpl.save")}</span>
          </div>
        </div>
      )}

      {!draft && (templates ?? []).map(t => (
        <div key={t.id} className="ndv__device" style={{ flexDirection: "column", alignItems: "stretch", gap: 4 }}>
          <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
            <span className={`ndv__dot ${activeID === t.id ? "ndv__dot--ok" : ""}`} />
            <span className="ndv__device-name" role="button" onClick={() => { setActiveID(activeID === t.id ? "" : t.id); setPreview(null); setNote(""); setVars({}); }}>
              {t.name}
            </span>
            <span className="ndv__device-addr">{tr("ndv.tpl.targetsVars", { n: (t.targets ?? []).length, v: (t.vars ?? []).length })}</span>
            <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }} onClick={() => { void app.NetDevTemplateDelete(t.id).then(reload); }}>{tr("ndv.tpl.delete")}</span>
          </div>
          {activeID === t.id && (
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              {(t.vars ?? []).length > 0 && (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                  {(t.vars ?? []).map(v => (
                    <input key={v} className="mem-input" style={{ width: 160 }} placeholder={`{{${v}}}`} value={vars[v] ?? ""} onChange={e => setVars({ ...vars, [v]: e.target.value })} />
                  ))}
                </div>
              )}
              <div style={{ display: "flex", gap: 6 }}>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => void render()}>{busy === "render" ? tr("ndv.tpl.rendering") : tr("ndv.tpl.preview")}</span>
                <span className="btn btn--primary btn--small" role="button" onClick={() => void apply()}>{busy === "apply" ? tr("ndv.tpl.applying") : tr("ndv.tpl.apply")}</span>
              </div>
              {preview && preview.map(pd => (
                <div key={pd.device} className="ndv-cutover__pick" style={{ opacity: pd.available ? 1 : 0.6 }}>
                  <div className="ndv-cutover__pick-head">
                    <span>{pd.available ? "✅" : "⛔"} {pd.device}</span>
                    {!pd.available && <span className="ndv__hint--err">{pd.reason}</span>}
                  </div>
                  {(pd.steps ?? []).map((st, j) => (
                    <div key={j} className="ndv__meta" style={{ marginTop: 4 }}>
                      <div className="ndv__audit-cmd">{tr("ndv.tpl.changeLine", { list: (st.commands ?? []).map((c, k) => `${c} 〈${(st.classes ?? [])[k]}〉`).join("；") })}{st.dangerous ? tr("ndv.tpl.dangerTag") : ""}</div>
                      <div className="ndv__audit-cmd">{tr("ndv.tpl.rollbackLine", { list: (st.rollback ?? []).join("；") })}</div>
                    </div>
                  ))}
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
