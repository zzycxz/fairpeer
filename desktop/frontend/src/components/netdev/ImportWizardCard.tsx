// ImportWizardCard — 迁移导入向导的待确认区（§5.6 / completion-spec §6 #11）。
// 预览导出 JSON 的新增/冲突设备，用户逐项勾选后合并——凭证不迁移，骨架
// 落盘后需在设置中补录。
import { useMemo, useState } from "react";
import { app } from "../../lib/bridge";
import { useI18n } from "../../lib/i18n";
import { useToast } from "../../lib/toast";
import type { NetDevImportPreview } from "../../lib/types";

export function ImportWizardCard({
  pv,
  onClose,
  onApplied,
}: {
  pv: NetDevImportPreview;
  onClose: () => void;
  onApplied: (n: number) => void;
}) {
  const { t } = useI18n();
  const { showToast } = useToast();
  const [addSet, setAddSet] = useState<Set<string>>(
    new Set([...pv.new_devices.map(d => d.name), ...pv.db_new.map(s => "db:" + s.name)])
  );
  const [takeSet, setTakeSet] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);

  const toggle = (set: Set<string>, name: string, on: boolean) => {
    const next = new Set(set);
    if (on) next.add(name); else next.delete(name);
    return next;
  };

  const total = useMemo(() => addSet.size + takeSet.size, [addSet, takeSet]);

  const apply = async () => {
    if (total === 0) return;
    setBusy(true);
    try {
      const n = await app.NetDevImportApply(pv.source, [...addSet], [...takeSet]);
      onApplied(n);
    } catch (e) {
      showToast(String(e), "error");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="ndv__card" style={{ marginTop: 8 }}>
      <div className="ndv__card-title">
        📥 {t("ndv.import.title", { at: pv.exported_at })}
        <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }} onClick={onClose}>{t("common.cancel")}</span>
      </div>
      <div className="ndv__hint" style={{ marginBottom: 6 }}>
        {t("ndv.import.skeletonOnly")}<strong>{t("ndv.import.noCreds")}</strong>{t("ndv.import.reenter")}
        {t("ndv.import.findingsRef", { n: pv.findings_seen })}
      </div>

      {pv.new_devices.length > 0 && (
        <>
          <div className="ndv__group-label">{t("ndv.import.newDevices", { n: pv.new_devices.length })}</div>
          {pv.new_devices.map(d => (
            <label key={d.name} style={{ display: "flex", alignItems: "center", gap: 6, padding: "2px 0", cursor: "pointer" }}>
              <input type="checkbox" checked={addSet.has(d.name)} onChange={e => setAddSet(toggle(addSet, d.name, e.target.checked))} />
              <span className="ndv__meta">{d.name} · {d.vendor}{d.kind ? ` · ${d.kind}` : ""} · {d.address}{d.group ? ` · ${d.group}` : ""}</span>
            </label>
          ))}
        </>
      )}

      {pv.conflict_devices.length > 0 && (
        <>
          <div className="ndv__group-label" style={{ marginTop: 8 }}>{t("ndv.import.conflicts", { n: pv.conflict_devices.length })}</div>
          {pv.conflict_devices.map(c => (
            <label key={c.imported.name} style={{ display: "flex", alignItems: "center", gap: 6, padding: "2px 0", cursor: "pointer" }}>
              <input type="checkbox" checked={takeSet.has(c.imported.name)} onChange={e => setTakeSet(toggle(takeSet, c.imported.name, e.target.checked))} />
              <span className="ndv__meta">
                <strong>{c.imported.name}</strong>：{t("ndv.import.imp", { addr: c.imported.address, vendor: c.imported.vendor })} vs {t("ndv.import.local", { addr: c.local.address, vendor: c.local.vendor })}
              </span>
            </label>
          ))}
        </>
      )}

      {pv.db_new.length > 0 && (
        <>
          <div className="ndv__group-label" style={{ marginTop: 8 }}>{t("ndv.import.newDb", { n: pv.db_new.length })}</div>
          {pv.db_new.map(s => (
            <label key={s.name} style={{ display: "flex", alignItems: "center", gap: 6, padding: "2px 0", cursor: "pointer" }}>
              <input type="checkbox" checked={addSet.has("db:" + s.name)} onChange={e => setAddSet(toggle(addSet, "db:" + s.name, e.target.checked))} />
              <span className="ndv__meta">{s.name} · {s.type} · {s.host}:{s.port}</span>
            </label>
          ))}
        </>
      )}

      {pv.db_overlap.length > 0 && (
        <div className="ndv__hint" style={{ marginTop: 6 }}>{t("ndv.import.dbOverlap", { list: pv.db_overlap.join("、") })}</div>
      )}

      <div style={{ display: "flex", gap: 6, marginTop: 10 }}>
        <span
          className={`btn btn--small ${total > 0 ? "btn--primary" : "btn--secondary"}`}
          role="button"
          onClick={() => void apply()}
          style={{ opacity: total === 0 || busy ? 0.5 : 1 }}
        >
          {busy ? t("ndv.import.merging") : t("ndv.import.mergeN", { n: total })}
        </span>
      </div>
    </div>
  );
}
