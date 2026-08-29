// CutoverView — 割接模式前端（NETDEV_SPEC_V2 §7.2）：整份 runbook 的总倒计
// 时 + 步骤 + 语义验证门 + 回退决策点。按 §10 契约不开第四个工作台——它
// 是「对话」主区里的任务过程视图（Esc 返回对话），入口在「提案」页签。
// 决策永远是人按的：hold 状态出现 [继续] 与 [回退] 并排，回退走提案回滚
// 全链路（首败冻结 + 备份恢复），结束后可导出前后基线对比报告。
import { useCallback, useEffect, useMemo, useState } from "react";
import { app } from "../../lib/bridge";
import type { NetDevCutoverRun, NetDevCutoverStep, NetDevProposal } from "../../lib/types";

const STEP_MARK: Record<string, string> = {
  pending: "⬜",
  running: "…",
  gating: "⏳",
  done: "✅",
  approved: "✅",
  failed: "❌",
  "rolled-back": "↩️",
  skipped: "⏭",
};

const STATUS_LABEL: Record<string, string> = {
  running: "执行中",
  hold: "⏸ 等待决策",
  done: "已完成",
  failed: "失败",
  aborted: "已终止/回退",
};

function fmtCountdown(ms: number): string {
  if (ms <= 0) return "已耗尽";
  const s = Math.floor(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}` : `${m}:${String(sec).padStart(2, "0")}`;
}

export function CutoverView({
  runId,
  proposals,
  devices,
  onClose,
}: {
  runId: string; // "" → creation form; otherwise the run to watch
  proposals: NetDevProposal[];
  devices: { name: string; vendor: string }[];
  onClose: () => void;
}) {
  // The creation form "graduates" into watching the run it started without a
  // parent re-render: watchId tracks the effective run.
  const [watchId, setWatchId] = useState(runId);
  useEffect(() => setWatchId(runId), [runId]);
  const [run, setRun] = useState<NetDevCutoverRun | null>(null);
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const [now, setNow] = useState(Date.now());

  const reload = useCallback(async () => {
    if (!watchId) return;
    try {
      setRun(await app.NetDevCutoverGet(watchId));
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, [watchId]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // 活跃割接 2s 轮询 + 秒级倒计时；已结束 15s 兜底刷新。
  useEffect(() => {
    const tick = setInterval(() => setNow(Date.now()), 1000);
    const poll = setInterval(() => void reload(), run?.status === "running" || run?.status === "hold" ? 2000 : 15000);
    return () => {
      clearInterval(tick);
      clearInterval(poll);
    };
  }, [reload, run?.status]);

  const countdown = useMemo(() => (run ? fmtCountdown(new Date(run.deadline).getTime() - now) : ""), [run, now]);

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

  if (!watchId) {
    return <CutoverCreate proposals={proposals} devices={devices} onCreated={(id) => setWatchId(id)} />;
  }
  if (!run) {
    return <div className="ndv__card"><div className="ndv__hint">{err || "加载中…"}</div></div>;
  }

  const hold = run.status === "hold";
  return (
    <div className="ndv-cutover">
      <div className="ndv-cutover__head">
        <span className="ndv-cutover__name">🌗 {run.name}</span>
        <span className={`ndv__badge ${run.status === "running" ? "" : run.status === "hold" ? "ndv__badge--warn" : ""}`}>
          {STATUS_LABEL[run.status] ?? run.status}
        </span>
        <span className={`ndv-cutover__count ${new Date(run.deadline).getTime() - now < 5 * 60_000 ? "ndv-cutover__count--low" : ""}`}>
          ⏱ 总倒计时 {countdown}
        </span>
        <span className="terminal-panel__spacer" />
        {run.report && (
          <span className="btn btn--secondary btn--small" role="button" onClick={() => void act("report", () => app.NetDevCutoverReport(run.id))}>
            导出对比报告
          </span>
        )}
        <span className="btn btn--secondary btn--small" role="button" onClick={onClose}>返回对话</span>
      </div>

      {err && <div className="ndv__hint">{err}</div>}

      {hold && (
        <div className="ndv-cutover__hold">
          <div className="ndv-cutover__hold-note">{run.hold_note || "等待决策"}</div>
          <div className="ndv-cutover__hold-actions">
            <span className="btn btn--primary btn--small" role="button" onClick={() => void act("continue", () => app.NetDevCutoverContinue(run.id))}>
              {busy === "continue" ? "继续中…" : "▶ 继续"}
            </span>
            <span
              className="btn btn--danger btn--small"
              role="button"
              title="按决策点回退：已执行提案逆序回滚（备份恢复 + 审计）"
              onClick={() => void act("rollback", () => app.NetDevCutoverRollback(run.id))}
            >
              {busy === "rollback" ? "回退中…" : "↩ 回退"}
            </span>
          </div>
          <div className="ndv__hint">回退仍走提案回滚全链路——AI 的手永远慢一步，决策是人按的（§7.2）。</div>
        </div>
      )}
      {run.status === "running" && (
        <div className="ndv-cutover__hold ndv-cutover__hold--running">
          <span className="ndv__hint">runbook 自动执行中……到验证门或决策点会自动停下。</span>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => void act("abort", () => app.NetDevCutoverAbort(run.id))}>
            {busy === "abort" ? "终止中…" : "⛔ 终止"}
          </span>
        </div>
      )}

      <div className="ndv-cutover__steps">
        {(run.steps ?? []).map((s, i) => (
          <CutoverStepRow key={i} step={s} active={run.status === "running" && i === run.cursor} />
        ))}
      </div>

      {run.report && (
        <div className="ndv-cutover__report">
          <div className="ndv__card-title">割接前后基线对比</div>
          <pre className="ndv-cutover__report-body">{run.report}</pre>
        </div>
      )}
    </div>
  );
}

function CutoverStepRow({ step, active }: { step: NetDevCutoverStep; active: boolean }) {
  return (
    <div className={`ndv-cutover__step${active ? " ndv-cutover__step--active" : ""}`}>
      <span className="ndv-cutover__step-mark">{STEP_MARK[step.status] ?? "⬜"}</span>
      <div className="ndv-cutover__step-body">
        <div className="ndv-cutover__step-title">
          {step.label}
          {step.proposal_id && <span className="ndv__meta"> · 提案 {step.proposal_id}</span>}
          {step.est_sec ? <span className="ndv__meta"> · 预计 {step.est_sec}s</span> : null}
          {step.decision_point && <span className="ndv__meta"> · ⚑ 决策点{step.impact ? `（${step.impact}）` : ""}</span>}
        </div>
        {(step.device || step.command) && (
          <div className="ndv__meta ndv__audit-cmd">{step.device} ❯ {step.command}</div>
        )}
        {step.gate && (
          <div className="ndv__meta">
            ⏳ 验证门：{step.gate.device} ❯ {step.gate.command} 期待 /{step.gate.expect}/ 持续 {step.gate.sustain_sec ?? 30}s
          </div>
        )}
        {step.error && <div className="ndv__hint ndv__hint--err">{step.error}</div>}
      </div>
    </div>
  );
}

// ── creation form ────────────────────────────────────────────────────────────

interface DraftStep {
  label: string;
  proposalId: string;
  gateCmd: string;
  gateExpect: string;
  gateSustain: string;
  decision: boolean;
  impact: string;
}

function CutoverCreate({
  proposals,
  devices,
  onCreated,
}: {
  proposals: NetDevProposal[];
  devices: { name: string; vendor: string }[];
  onCreated: (id: string) => void;
}) {
  const approved = proposals.filter((p) => p.status === "approved");
  const [name, setName] = useState("");
  const [minutes, setMinutes] = useState("120");
  const [sel, setSel] = useState<Record<string, DraftStep>>({});
  const [checkCmd, setCheckCmd] = useState<DeviceCommand>({ device: "", command: "", expect: "", sustain: "30" });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  const toggle = (p: NetDevProposal) => {
    setSel((prev) => {
      const next = { ...prev };
      if (next[p.id]) delete next[p.id];
      else next[p.id] = { label: p.intent.slice(0, 24) || p.id, proposalId: p.id, gateCmd: "", gateExpect: "", gateSustain: "30", decision: true, impact: "" };
      return next;
    });
  };

  const start = async () => {
    setBusy(true);
    setErr("");
    try {
      const steps: NetDevCutoverStep[] = [];
      for (const d of Object.values(sel)) {
        const p = proposals.find((x) => x.id === d.proposalId);
        const gateDevice = p?.steps[0]?.device ?? devices[0]?.name ?? "";
        steps.push({
          label: d.label,
          proposal_id: d.proposalId,
          est_sec: 60,
          decision_point: d.decision,
          impact: d.impact || undefined,
          status: "",
          gate:
            d.gateCmd && d.gateExpect
              ? { device: gateDevice, command: d.gateCmd, expect: d.gateExpect, sustain_sec: Number(d.gateSustain) || 30 }
              : undefined,
        });
      }
      if (checkCmd.device && checkCmd.command && checkCmd.expect) {
        steps.push({
          label: "割接后验证",
          device: checkCmd.device,
          command: checkCmd.command,
          est_sec: Number(checkCmd.sustain) || 30,
          status: "",
          gate: { device: checkCmd.device, command: checkCmd.command, expect: checkCmd.expect, sustain_sec: Number(checkCmd.sustain) || 30 },
        });
      }
      const created = await app.NetDevCutoverStart({
        id: "",
        name,
        deadline: new Date(Date.now() + (Number(minutes) || 120) * 60_000).toISOString(),
        steps,
        status: "",
        cursor: 0,
        created_at: "",
      });
      onCreated(created.id);
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="ndv-cutover ndv-cutover--create">
      <div className="ndv__card">
        <div className="ndv__card-title">发起割接（§7.2）——倒计时 runbook + 验证门 + 回退决策点</div>
        <div className="ndv__hint">
          割接前自动拍全网基线快照；每步可挂语义验证门（如「OSPF 邻居 Full 且持续 60s」），门不过停在回退决策点——继续 or 回退由人按；结束后出前后对比报告。
          步骤引用的提案必须<b>已批准</b>（割接只执行已批准的变更）。
        </div>
        <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
          <input className="mem-input" style={{ flex: 2 }} placeholder="割接名称（如：核心交换机更换·夜割）" value={name} onChange={(e) => setName(e.target.value)} />
          <input className="mem-input" style={{ width: 120 }} placeholder="窗口（分钟）" value={minutes} onChange={(e) => setMinutes(e.target.value)} />
        </div>

        <div className="ndv__group-label" style={{ marginTop: 10 }}>已批准提案（点击加入 runbook）</div>
        {approved.length === 0 && <div className="ndv__hint">没有已批准的提案——先在「提案」页签批准变更草稿。</div>}
        {approved.map((p) => {
          const d = sel[p.id];
          return (
            <div key={p.id} className={`ndv-cutover__pick${d ? " ndv-cutover__pick--on" : ""}`} role="button" onClick={() => toggle(p)}>
              <div className="ndv-cutover__pick-head">
                <span>{d ? "☑" : "☐"} {p.id}</span>
                <span className="ndv__meta">{p.intent.slice(0, 60)}</span>
              </div>
              {d && (
                <div className="ndv-cutover__pick-form" onClick={(e) => e.stopPropagation()}>
                  <input className="mem-input" placeholder="步骤标签" value={d.label} onChange={(e) => setSel(prev => ({ ...prev, [p.id]: { ...d, label: e.target.value } }))} />
                  <input className="mem-input" placeholder="影响描述（决策点并列展示）" value={d.impact} onChange={(e) => setSel(prev => ({ ...prev, [p.id]: { ...d, impact: e.target.value } }))} />
                  <div style={{ display: "flex", gap: 6 }}>
                    <input className="mem-input" placeholder="验证门命令（可选，如 display ospf peer）" value={d.gateCmd} onChange={(e) => setSel(prev => ({ ...prev, [p.id]: { ...d, gateCmd: e.target.value } }))} />
                    <input className="mem-input" style={{ width: 140 }} placeholder="期待正则（如 Full）" value={d.gateExpect} onChange={(e) => setSel(prev => ({ ...prev, [p.id]: { ...d, gateExpect: e.target.value } }))} />
                    <input className="mem-input" style={{ width: 80 }} placeholder="持续s" value={d.gateSustain} onChange={(e) => setSel(prev => ({ ...prev, [p.id]: { ...d, gateSustain: e.target.value } }))} />
                  </div>
                  <label className="ndv__meta">
                    <input type="checkbox" checked={d.decision} onChange={(e) => setSel(prev => ({ ...prev, [p.id]: { ...d, decision: e.target.checked } }))} />
                    完成后停在回退决策点
                  </label>
                </div>
              )}
            </div>
          );
        })}

        <div className="ndv__group-label" style={{ marginTop: 10 }}>割接后验证命令（可选）</div>
        <div style={{ display: "flex", gap: 6 }}>
          <select className="mem-input" value={checkCmd.device} onChange={(e) => setCheckCmd({ ...checkCmd, device: e.target.value })}>
            <option value="">选设备…</option>
            {devices.map((d) => (
              <option key={d.name} value={d.name}>{d.name}</option>
            ))}
          </select>
          <input className="mem-input" style={{ flex: 1 }} placeholder="验证命令" value={checkCmd.command} onChange={(e) => setCheckCmd({ ...checkCmd, command: e.target.value })} />
          <input className="mem-input" style={{ width: 140 }} placeholder="期待正则" value={checkCmd.expect} onChange={(e) => setCheckCmd({ ...checkCmd, expect: e.target.value })} />
          <input className="mem-input" style={{ width: 80 }} placeholder="持续s" value={checkCmd.sustain} onChange={(e) => setCheckCmd({ ...checkCmd, sustain: e.target.value })} />
        </div>

        {err && <div className="ndv__hint ndv__hint--err">{err}</div>}
        <div style={{ marginTop: 10 }}>
          <span
            className="btn btn--primary btn--small"
            role="button"
            style={busy || !name || Object.keys(sel).length === 0 ? { opacity: 0.5 } : undefined}
            onClick={() => { if (!busy && name && Object.keys(sel).length > 0) void start(); }}
          >
            {busy ? "启动中…" : "▶ 启动割接（自动拍基线快照）"}
          </span>
        </div>
      </div>
    </div>
  );
}

interface DeviceCommand {
  device: string;
  command: string;
  expect: string;
  sustain: string;
}
