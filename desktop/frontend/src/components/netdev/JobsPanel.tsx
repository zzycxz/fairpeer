import { useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import type { RunRecordView, TaskView } from "../../lib/types";

// JobsPanel — the 作业 dock tab: scheduled-task status over the existing
// scheduler bridge (ListScheduledTasks / ScheduledTaskHistory /
// RunScheduledTaskNow). Pure UI — the scheduler engine and its audit trail
// are Go-side; creating/editing tasks stays in the 定时任务 settings.

export function JobsPanel() {
  const [tasks, setTasks] = useState<TaskView[] | null>(null);
  const [expanded, setExpanded] = useState<string>("");
  const [history, setHistory] = useState<RunRecordView[] | null>(null);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");

  const reload = async () => {
    try {
      const t = await app.ListScheduledTasks();
      setTasks(t ?? []);
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  };

  useEffect(() => { void reload(); }, []);

  const runNow = async (id: string) => {
    setBusy(id);
    try {
      await app.RunScheduledTaskNow(id);
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy("");
    }
  };

  const toggle = async (id: string) => {
    if (expanded === id) {
      setExpanded("");
      setHistory(null);
      return;
    }
    setExpanded(id);
    setHistory(null);
    try {
      setHistory(await app.ScheduledTaskHistory(id));
    } catch (e) {
      setErr(String(e));
    }
  };

  return (
    <div className="ndv__card">
      <div className="ndv__card-title">作业中心</div>
      {err && <div className="ndv__hint">{err}</div>}
      {!tasks && !err && <div className="ndv__hint">加载中…</div>}
      {tasks && tasks.length === 0 && (
        <div className="ndv__hint">还没有定时任务——设置 → 运维 → 定时任务（巡检/备份在此配置周期）。</div>
      )}
      {(tasks ?? []).map(t => (
        <div key={t.id} className="ndv__device" style={{ flexDirection: "column", alignItems: "stretch", gap: 4 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span className={`ndv__dot ${t.enabled ? "ndv__dot--ok" : "ndv__dot--down"}`} />
            <span className="ndv__device-name" role="button" onClick={() => void toggle(t.id)}>{t.name}</span>
            <span className="ndv__device-addr">{t.expression} · 已跑 {t.runCount} 次</span>
            <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }}
              onClick={() => void runNow(t.id)}>{busy === t.id ? "触发中…" : "立即运行"}</span>
          </div>
          {t.lastDeliverErr && <div className="ndv__hint">上次投递失败：{t.lastDeliverErr}</div>}
          {expanded === t.id && (
            <div className="ndv__audit-scroll" style={{ maxHeight: 180 }}>
              {(history ?? []).length === 0 && <div className="ndv__hint">暂无历史。</div>}
              {(history ?? []).map((r, i) => (
                <div key={i} className="ndv__audit-row">
                  <span className="ndv__audit-time">{String(r.at ?? "").slice(5, 16).replace("T", " ")}</span>
                  <span className="ndv__audit-dev">{r.status ?? ""}</span>
                  <span className="ndv__audit-cmd">{String(r.result ?? "").slice(0, 120)}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
