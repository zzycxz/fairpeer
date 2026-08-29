import { useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import type { NetDevJob, RunRecordView, TaskView } from "../../lib/types";

// JobsPanel — the 作业 dock tab, two cards:
// ① 定时任务：scheduled-task status over the existing scheduler bridge
//    (ListScheduledTasks / ScheduledTaskHistory / RunScheduledTaskNow).
// ② 诊断作业（R4 Job 引擎 §九/v1 C 批）：netdev runbook runs（体检电池、
//    人工启动的诊断 runbook）——状态/步骤进度/暂停原因可见；paused 的作业
//    可继续或终止（断点/熔断/on-fail=pause 的唯一恢复入口），running 的可
//    暂停/终止。引擎与审计全在 Go 侧。
export function JobsPanel() {
  return (
    <>
      <ScheduledTasksCard />
      <NetDevJobsCard />
    </>
  );
}

function ScheduledTasksCard() {
  const [tasks, setTasks] = useState<TaskView[] | null>(null);
  const [expanded, setExpanded] = useState<string>("");
  const [history, setHistory] = useState<RunRecordView[] | null>(null);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");

  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const reload = async () => {
    try {
      const t = await app.ListScheduledTasks();
      setTasks(t ?? []);
      setUpdatedAt(new Date());
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  };

  useEffect(() => { void reload(); }, []);
  const ago = updatedAt ? Math.max(0, Math.round((Date.now() - updatedAt.getTime()) / 1000)) : null;

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
      <div className="ndv__card-title">
        作业中心
        {ago !== null && <span className="ndv__meta" style={{ marginLeft: 8, fontWeight: 400 }}>{ago < 60 ? `${ago}s 前` : `${Math.round(ago / 60)}m 前`}</span>}
        <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }} onClick={() => void reload()}>刷新</span>
      </div>
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

const JOB_STATUS: Record<string, string> = {
  running: "执行中",
  paused: "⏸ 已暂停",
  done: "已完成",
  failed: "失败",
  aborted: "已终止",
};

function NetDevJobsCard() {
  const [jobs, setJobs] = useState<NetDevJob[] | null>(null);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");

  const reload = async () => {
    try {
      setJobs(await app.NetDevJobs());
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  };
  useEffect(() => { void reload(); }, []);

  const live = (jobs ?? []).some(j => j.status === "running" || j.status === "paused");
  useEffect(() => {
    if (!live) return;
    const t = setInterval(() => void reload(), 3000);
    return () => clearInterval(t);
  }, [live]);

  const act = async (label: string, fn: () => Promise<unknown>) => {
    setBusy(label);
    try {
      await fn();
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy("");
    }
  };

  const stepCounts = (j: NetDevJob) => {
    const c = { ok: 0, failed: 0, pending: 0 };
    for (const s of j.step_state ?? []) {
      if (s.status === "ok") c.ok++;
      else if (s.status === "failed") c.failed++;
      else c.pending++;
    }
    return c;
  };

  return (
    <div className="ndv__card" style={{ marginTop: 8 }}>
      <div className="ndv__card-title">
        诊断作业（runbook）
        {live && <span className="ndv__meta" style={{ marginLeft: 8 }}>· 进行中</span>}
        <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }} onClick={() => void reload()}>刷新</span>
      </div>
      {err && <div className="ndv__hint">{err}</div>}
      {!jobs && !err && <div className="ndv__hint">加载中…</div>}
      {jobs && jobs.length === 0 && (
        <div className="ndv__hint">还没有 runbook 作业——一键体检、带 expect/断点的诊断 runbook 会在这里留下轨迹（§4.1/§九 R4）。</div>
      )}
      {(jobs ?? []).slice(0, 8).map(j => {
        const c = stepCounts(j);
        const active = j.status === "running" || j.status === "paused";
        return (
          <div key={j.id} className="ndv__device" style={{ flexDirection: "column", alignItems: "stretch", gap: 4 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
              <span className={`ndv__dot ${j.status === "done" ? "ndv__dot--ok" : j.status === "running" ? "ndv__dot--warn" : j.status === "paused" ? "ndv__dot--down" : ""}`} />
              <span className="ndv__device-name" title={j.id}>{j.name}</span>
              <span className="ndv__device-addr">
                {JOB_STATUS[j.status] ?? j.status} · ✅{c.ok} ❌{c.failed} ⬜{c.pending} · {Math.round(j.active_ms / 1000)}s
              </span>
              {active && (
                <span style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
                  {j.status === "running" && (
                    <span className="btn btn--secondary btn--small" role="button" onClick={() => void act(`pause:${j.id}`, () => app.NetDevJobPause(j.id))}>
                      {busy === `pause:${j.id}` ? "…" : "⏸ 暂停"}
                    </span>
                  )}
                  {j.status === "paused" && (
                    <span className="btn btn--primary btn--small" role="button" title="从断点/失败处继续" onClick={() => void act(`resume:${j.id}`, () => app.NetDevJobResume(j.id))}>
                      {busy === `resume:${j.id}` ? "…" : "▶ 继续"}
                    </span>
                  )}
                  <span className="btn btn--secondary btn--small" role="button" onClick={() => void act(`abort:${j.id}`, () => app.NetDevJobAbort(j.id))}>
                    {busy === `abort:${j.id}` ? "…" : "⛔ 终止"}
                  </span>
                </span>
              )}
            </div>
            {j.pause_note && <div className="ndv__hint">{j.pause_note}</div>}
          </div>
        );
      })}
    </div>
  );
}
