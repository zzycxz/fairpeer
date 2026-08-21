// BranchTree (upgrade spec 4-3) is the session-tree navigator: every branch
// (fork/rewind point) of the current tab's session laid out as an indented
// tree, newest info first per node — click navigates the tab to that branch
// (the backend swaps the session; the app then reloads the transcript). The
// data — ParentID links, previews, turn counts — already lives in the branch
// sidecars; this is the missing view over it.
import { useEffect, useMemo, useState } from "react";
import { ChevronRight, GitBranch, X } from "lucide-react";
import { app } from "../lib/bridge";

interface BranchRow {
  id: string;
  name?: string;
  parentId?: string;
  path: string;
  preview?: string;
  turns?: number;
  updatedAt: number;
  current?: boolean;
}

function fmtAgo(ms: number): string {
  const s = Math.max(1, Math.round((Date.now() - ms) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.round(s / 60)}m`;
  if (s < 86400) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}

export function BranchTree({
  open,
  onClose,
  onSwitched,
}: {
  open: boolean;
  onClose: () => void;
  onSwitched: () => void;
}) {
  const [rows, setRows] = useState<BranchRow[]>([]);
  const [err, setErr] = useState("");
  const [busyID, setBusyID] = useState("");

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    app.BranchesForTab("")
      .then((b) => { if (!cancelled) { setRows(b ?? []); setErr(""); } })
      .catch((e) => { if (!cancelled) setErr(String(e)); });
    return () => { cancelled = true; };
  }, [open]);

  // Lay the flat list out as a tree: roots are nodes whose parent is not in
  // the set; children follow their parent, in listing order.
  const ordered = useMemo(() => {
    const byId = new Map(rows.map((r) => [r.id, r]));
    const children = new Map<string, BranchRow[]>();
    const roots: BranchRow[] = [];
    for (const r of rows) {
      if (r.parentId && byId.has(r.parentId)) {
        const list = children.get(r.parentId) ?? [];
        list.push(r);
        children.set(r.parentId, list);
      } else {
        roots.push(r);
      }
    }
    const out: { row: BranchRow; depth: number }[] = [];
    const walk = (r: BranchRow, depth: number) => {
      out.push({ row: r, depth });
      for (const c of children.get(r.id) ?? []) walk(c, depth + 1);
    };
    for (const r of roots) walk(r, 0);
    return out;
  }, [rows]);

  if (!open) return null;

  const switchTo = (r: BranchRow) => {
    if (r.current || busyID) return;
    setBusyID(r.id);
    app.SwitchBranchForTab("", r.id)
      .then(() => { onSwitched(); onClose(); })
      .catch((e) => setErr(String(e)))
      .finally(() => setBusyID(""));
  };

  return (
    <div className="agd" role="dialog" aria-label="session branches">
      <div className="agd__card">
        <div className="agd__head">
          <GitBranch size={14} />
          <span className="agd__title">分支</span>
          <span className="agd__search" style={{ opacity: 0.6 }}>{rows.length} 个会话节点</span>
          <button type="button" className="agd__close" onClick={onClose}><X size={14} /></button>
        </div>
        <div className="agd__body">
          {err && <div className="agd__empty">{err}</div>}
          {ordered.map(({ row, depth }) => (
            <button
              key={row.id}
              type="button"
              className={`agd__jump${row.current ? " agd__row--active" : ""}`}
              style={{ paddingLeft: 8 + depth * 18 }}
              onClick={() => switchTo(row)}
              disabled={Boolean(row.current) || busyID === row.id}
              title={row.path}
            >
              <ChevronRight size={12} style={{ opacity: 0.4, flex: "none" }} />
              <span className="agd__label">{row.name || row.id}</span>
              <span className="agd__meta">
                {row.turns ? `${row.turns} 轮 · ` : ""}{fmtAgo(row.updatedAt)}
              </span>
              {row.current && <span className="branch-tree__cur">当前</span>}
            </button>
          ))}
          {rows.length === 0 && !err && (
            <div className="agd__empty">此会话还没有分支——在消息操作栏 Fork 或 Rewind 即可创建</div>
          )}
        </div>
      </div>
    </div>
  );
}
