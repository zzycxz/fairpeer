// AgentDashboard is the multi-tab overview (upgrade spec 2-7, codex
// AgentsOverviewView pattern): every tab — agent or plain topic — grouped by
// what it is doing (running / idle), searchable, one click to switch, one to
// stop. The "needs you" group (a background tab blocked on an approval) needs
// per-tab approval state exposed in TabMeta and joins in a later batch.
import { useMemo, useState } from "react";
import { CircleDot, X } from "lucide-react";
import type { TabMeta } from "../lib/types";

export function AgentDashboard({
  open,
  onClose,
  tabs,
  activeTabId,
  onSwitch,
  onStop,
}: {
  open: boolean;
  onClose: () => void;
  tabs: TabMeta[];
  activeTabId?: string;
  onSwitch: (tabId: string) => void;
  onStop: (tabId: string) => void;
}) {
  const [query, setQuery] = useState("");
  if (!open) return null;

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return tabs;
    return tabs.filter((t) =>
      [t.label, t.topicTitle, t.workspaceName, t.mode, t.collaborationMode]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q)),
    );
  }, [tabs, query]);

  const running = filtered.filter((t) => t.running);
  const idle = filtered.filter((t) => !t.running);

  const row = (t: TabMeta) => (
    <div key={t.id} className={`agd__row${t.id === activeTabId ? " agd__row--active" : ""}`}>
      <button
        type="button"
        className="agd__jump"
        onClick={() => {
          onSwitch(t.id);
          onClose();
        }}
      >
        {t.running ? <CircleDot size={13} className="agd__ico--run" /> : <span className="agd__dot" />}
        <span className="agd__label">{t.label || t.topicTitle || t.id}</span>
        <span className="agd__meta">
          {[t.workspaceName, t.collaborationMode !== "normal" ? t.collaborationMode : ""]
            .filter(Boolean)
            .join(" · ")}
        </span>
      </button>
      {t.running && (
        <button type="button" className="agd__stop" onClick={() => onStop(t.id)} title="Stop">
          <X size={13} />
        </button>
      )}
    </div>
  );

  return (
    <div className="agd" role="dialog" aria-label="agents overview">
      <div className="agd__card">
        <div className="agd__head">
          <span className="agd__title">Agents</span>
          <input
            className="agd__search"
            value={query}
            placeholder="filter…"
            onChange={(e) => setQuery(e.target.value)}
            autoFocus
          />
          <button type="button" className="agd__close" onClick={onClose}>
            <X size={14} />
          </button>
        </div>
        <div className="agd__body">
          {running.length > 0 && (
            <>
              <div className="agd__group agd__group--run">Working · {running.length}</div>
              {running.map(row)}
            </>
          )}
          {idle.length > 0 && (
            <>
              <div className="agd__group">Idle · {idle.length}</div>
              {idle.map(row)}
            </>
          )}
          {filtered.length === 0 && <div className="agd__empty">no tabs match</div>}
        </div>
      </div>
    </div>
  );
}
