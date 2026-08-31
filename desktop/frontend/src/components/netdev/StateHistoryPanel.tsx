import { useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import { useI18n } from "../../lib/i18n";
import type { NetDevStateEventView, NetDevStateFileDiff } from "../../lib/types";
import { InlineConfirmButton } from "../InlineConfirmButton";
import { CodeViewer } from "../CodeViewer";

// StateHistoryPanel — the 状态历史 dock tab: the ops mode's local-record
// rewind (Claude Code/ZCode-aligned checkpoint practice, NETDEV_SPEC_V2 三层
// 回退). Every state transition (approve/execute/rollback, job & cutover
// lifecycle, settings/promote/import…) snapshots the files it is about to
// overwrite; this timeline lists those events and rewinds to before any of
// them (suffix semantics). Layer contract shown on every confirm: this
// restores LOCAL RECORDS ONLY — device rollback stays on the proposal/cutover
// path. The audit tab remains the immutable what-happened trail.
export function StateHistoryPanel() {
  const { t } = useI18n();
  const [events, setEvents] = useState<NetDevStateEventView[] | null>(null);
  const [err, setErr] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [expanded, setExpanded] = useState<number | null>(null);
  const [diffs, setDiffs] = useState<Record<number, NetDevStateFileDiff[] | null>>({});

  const reload = async () => {
    try {
      setEvents(await app.NetDevStateEvents());
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  };
  useEffect(() => { void reload(); }, []);

  const toggle = async (id: number) => {
    if (expanded === id) {
      setExpanded(null);
      return;
    }
    setExpanded(id);
    if (diffs[id] === undefined) {
      setDiffs(prev => ({ ...prev, [id]: null })); // null = loading, [] = loaded empty
      try {
        const d = await app.NetDevStateEventDiff(id);
        setDiffs(prev => ({ ...prev, [id]: d }));
      } catch (e) {
        setErr(String(e));
      }
    }
  };

  const restore = async (id: number, redo = false) => {
    setBusy(true);
    try {
      const res = await app.NetDevStateRestore(id);
      setNotice(redo ? t("ndv.hist.redone", { w: res.written.length + res.deleted.length }) : t("ndv.hist.restored", { w: res.written.length, d: res.deleted.length }));
      setExpanded(null);
      setDiffs({});
      await reload();
    } catch (e) {
      setNotice("");
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  };

  const restorable = (events ?? []).filter(e => e.canRestore).length;

  // Suffix of the event at index idx (newest-first list): the event itself plus
  // every later one — exactly the files a rewind to before it would restore.
  const suffixFiles = (idx: number): string[] => {
    const set = new Set<string>();
    for (let i = 0; i <= idx; i++) for (const p of events?.[i].paths ?? []) set.add(p);
    return [...set];
  };

  return (
    <div className="ndv__card">
      <div className="ndv__card-title">
        {t("ndv.hist.title")}
        {events && events.length > 0 && <span className="ndv__meta" style={{ marginLeft: 8, fontWeight: 400 }}>{t("ndv.hist.count", { n: events.length, ok: restorable })}</span>}
        <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }} onClick={() => void reload()}>{t("ndv.refresh")}</span>
      </div>
      <div className="ndv__hint">{t("ndv.hist.layerNote")}</div>
      {err && <div className="ndv__hint" style={{ color: "var(--danger, #e5484d)" }}>{err}</div>}
      {notice && <div className="ndv__hint" style={{ color: "var(--ok, #30a46c)" }}>{notice}</div>}
      {!events && !err && <div className="ndv__hint">{t("ndv.loading")}</div>}
      {events && events.length === 0 && <div className="ndv__hint">{t("ndv.hist.empty")}</div>}
      {(events ?? []).map((ev, idx) => {
        const suffix = suffixFiles(idx);
        const cfgTouched = suffix.some(p => p.endsWith("config.toml"));
        return (
        <div key={ev.id} className="ndv__device" style={{ flexDirection: "column", alignItems: "stretch", gap: 4 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <span className={`ndv__dot ${ev.live.length > 0 ? "ndv__dot--down" : ev.kind === "restore-keep" ? "ndv__dot--warn" : "ndv__dot--ok"}`} />
            <span className="ndv__device-name" role="button" title={ev.id.toString()} onClick={() => void toggle(ev.id)}>
              {t(`ndv.hist.kind.${ev.kind}` as never) !== `ndv.hist.kind.${ev.kind}` ? t(`ndv.hist.kind.${ev.kind}` as never) : ev.kind}
            </span>
            {ev.entity && <span className="ndv__device-addr">{ev.entity}</span>}
            <span className="ndv__device-addr">{t(`ndv.hist.actor.${ev.actor}` as never) !== `ndv.hist.actor.${ev.actor}` ? t(`ndv.hist.actor.${ev.actor}` as never) : ev.actor}</span>
            <span className="ndv__device-addr">{new Date(ev.time).toLocaleTimeString()}</span>
            <span style={{ marginLeft: "auto", display: "flex", gap: 6, alignItems: "center" }}>
              {ev.live.length > 0 && (
                <span className="ndv__device-addr" title={ev.live.map(l => `${l.type} ${l.id}: ${l.status}`).join("\n")}>
                  {t("ndv.hist.blocked", { n: ev.live.length })}
                </span>
              )}
              {ev.canRedo && (
                <InlineConfirmButton
                  label={t("ndv.hist.redo")}
                  confirmLabel={t("ndv.hist.redoConfirm")}
                  cancelLabel={t("ndv.hist.cancel")}
                  disabled={busy}
                  onConfirm={() => void restore(ev.id, true)}
                />
              )}
              {ev.canRestore && ev.kind !== "restore-keep" && (
                <InlineConfirmButton
                  label={t("ndv.hist.restore")}
                  confirmLabel={t("ndv.hist.restoreConfirm", { n: suffix.length })}
                  cancelLabel={t("ndv.hist.cancel")}
                  danger
                  disabled={busy}
                  onConfirm={() => void restore(ev.id)}
                />
              )}
            </span>
          </div>
          {ev.live.length > 0 && (
            <div className="ndv__hint" style={{ color: "var(--danger, #e5484d)" }}>
              {t("ndv.hist.liveWhy")}{ev.live.map(l => t("ndv.hist.liveEntry", { type: l.type, id: l.id, status: l.status })).join("；")}
            </div>
          )}
          {cfgTouched && ev.canRestore && (
            <div className="ndv__hint" style={{ color: "var(--warn, #f5a524)" }}>{t("ndv.hist.cfgWarn")}</div>
          )}
          {expanded === ev.id && (
            <div className="ndv__audit-scroll" style={{ maxHeight: 320 }}>
              {(diffs[ev.id] ?? null) === null && <div className="ndv__hint">{t("ndv.loading")}</div>}
              {(diffs[ev.id] ?? []).length === 0 && diffs[ev.id] !== null && <div className="ndv__hint">{t("ndv.hist.noDiff")}</div>}
              {(diffs[ev.id] ?? []).map(d => (
                <div key={d.path} style={{ marginBottom: 6 }}>
                  <div className="ndv__audit-row" style={{ display: "flex", gap: 8 }}>
                    <span className="ndv__audit-dev">{d.kind}</span>
                    <span className="ndv__audit-cmd" title={d.path}>{d.path}</span>
                    {(d.added > 0 || d.removed > 0) && <span className="ndv__device-addr">+{d.added} -{d.removed}</span>}
                  </div>
                  {d.diff && <CodeViewer value={d.diff} language="diff" />}
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
