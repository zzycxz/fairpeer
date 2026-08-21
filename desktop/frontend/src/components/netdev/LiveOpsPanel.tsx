import { useEffect, useMemo, useRef, useState } from "react";
import { Activity, ChevronDown } from "lucide-react";
import { app, onNetdevLive } from "../../lib/bridge";
import type { NetDevLiveEvent, NetDevLiveSnapshot } from "../../lib/types";
import {
  applyLiveEvent, isDeviceLive, liveStateFromSnapshot,
  type LiveOpsState,
} from "../../lib/liveOpsState";

// LiveOpsPanel (操作实况) — the human's supervision surface: everything the
// agent is doing on connected devices, live. Four layers (NETDEV_SPEC §10.4):
//   1. budget strip   — this turn's command budget + risk counters
//   2. device cards   — status dot, VTY occupancy, in-flight command, a
//                       terminal-textured OUTPUT TAIL that streams as the
//                       device answers, and recent command chips
//   3. guardrail rows — refusals are loud; being stopped must be visible
//   4. empty state    — teaches what this panel is for
//
// Data: NetDevLiveSnapshot on mount, then "netdev:live" events (Go-side
// coalesced batches). The folding logic lives in lib/liveOpsState.ts (pure,
// unit-tested); this component only renders. Every chunk the backend sends is
// already ANSI-stripped and REDACTED — secrets never reach this stream.

const CLASS_LABEL: Record<string, string> = { read: "读", write: "写", dangerous: "危险", unknown: "未知", guardrail: "护栏", assess: "评估" };
const STATUS_ICON: Record<string, string> = { ok: "✓", "device-error": "⚠", failure: "✕", refused: "⛔", running: "⏳" };

function classColor(cls: string): string {
  if (cls === "read") return "var(--accent)";
  if (cls === "guardrail") return "var(--danger)";
  return "var(--warn)";
}

function fmtClock(ms: number): string {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

function stateDot(state: string): { color: string; label: string } {
  switch (state) {
    case "connected": return { color: "var(--ok)", label: "已连接" };
    case "connecting": return { color: "var(--warn)", label: "连接中" };
    case "reconnecting": return { color: "var(--warn)", label: "重连中" };
    case "idle-closed": return { color: "var(--fg-faint)", label: "空闲回收" };
    case "stopped": return { color: "var(--danger)", label: "已断开" };
    default: return { color: "var(--fg-faint)", label: "未连接" };
  }
}

export function LiveOpsPanel() {
  const [state, setState] = useState<LiveOpsState>(() => ({ devices: new Map(), spent: 0, budget: 0, guardrails: [] }));
  const [ready, setReady] = useState(false);
  // Mutable fold target. applyLiveEvent mutates device records in place (ring
  // buffers keep folding O(1)); folding must happen OUTSIDE the setState
  // updater — React StrictMode double-invokes updaters in dev, which would
  // fold every batch twice and visibly duplicate tail lines/chips.
  const foldRef = useRef<LiveOpsState>({ devices: new Map(), spent: 0, budget: 0, guardrails: [] });

  useEffect(() => {
    let cancelled = false;
    const publish = () => setState({ ...foldRef.current, devices: new Map(foldRef.current.devices) });
    void app.NetDevLiveSnapshot()
      .then((snap: NetDevLiveSnapshot) => {
        if (cancelled) return;
        foldRef.current = liveStateFromSnapshot(snap);
        publish();
        setReady(true);
      })
      .catch(() => { /* backend not ready — events will populate */ setReady(true); });

    return onNetdevLive((events: NetDevLiveEvent[]) => {
      if (cancelled || !Array.isArray(events)) return;
      // Fold into the existing state object then publish a cloned identity so
      // React sees the change.
      applyLiveBatch(foldRef.current, events);
      publish();
    });
  }, []);

  // Active devices: connected or with recent activity; idle ones collapse to
  // a single row. Order: in-flight first, then most recent activity.
  const { active, idle } = useMemo(() => {
    const list = [...state.devices.values()];
    const now = Date.now();
    const act = list.filter((d) => isDeviceLive(d, now)).sort((a, b) => (b.current ? 1 : 0) - (a.current ? 1 : 0) || b.lastAt - a.lastAt);
    return { active: act, idle: list.filter((d) => !isDeviceLive(d, now)) };
  }, [state]);

  const writeCount = useMemo(() => {
    let n = 0;
    for (const d of state.devices.values()) for (const c of d.cmds) if (c.class === "write" || c.class === "dangerous" || c.class === "guardrail") n++;
    return n;
  }, [state]);

  return (
    <div className="ndv__card ndv__live">
      <div className="ndv__live-budget">
        <span className="ndv__live-budget-label">
          <Activity size={12} /> 本轮预算
        </span>
        <span className="ndv__live-budget-meter">
          {state.budget > 0
            ? Array.from({ length: Math.min(state.budget, 30) }, (_, i) => (
                <span key={i} className={`ndv__live-seg${i < Math.min(state.spent, 30) ? " ndv__live-seg--on" : ""}`} />
              ))
            : <span className="ndv__live-budget-num">不限</span>}
        </span>
        <span className="ndv__live-budget-num">{state.budget > 0 ? `${Math.min(state.spent, state.budget)}/${state.budget}` : String(state.spent)}</span>
        <span className="ndv__live-budget-sep">·</span>
        <span>活动 {active.length} 台</span>
        <span className="ndv__live-budget-sep">·</span>
        <span>拦截/写 <span style={{ color: writeCount > 0 ? "var(--danger)" : "inherit" }}>{writeCount}</span></span>
      </div>

      {active.length === 0 && state.guardrails.length === 0 && (
        <div className="ndv__hint" style={{ padding: 0 }}>
          {ready
            ? "AI 在设备上执行命令时，这里实时显示每条命令与输出——供你随时检查。设备状态灯也会跟随连接/重连变化。"
            : "正在连接实况通道…"}
        </div>
      )}

      {active.map((d) => {
        const dot = stateDot(d.state);
        return (
          <div key={d.device} className="ndv__live-card">
            <div className="ndv__live-card-head">
              <span className="ndv__live-dot" style={{ background: dot.color }} title={dot.label} />
              <span className="ndv__live-dev">{d.device}</span>
              {(d.vendor || d.os) && <span className="ndv__live-vendor">{d.vendor}{d.os ? `/${d.os}` : ""}</span>}
              <span className="ndv__live-state" style={{ color: dot.color }}>{dot.label}</span>
              {d.vtyCap > 0 && (
                <span
                  className="ndv__live-vty"
                  style={d.vtyUse > d.vtyCap ? { color: "var(--danger)", fontWeight: 700 } : undefined}
                  title="会话占用 / 设备上限（CLI + NETCONF 共享 VTY）"
                >
                  {d.vtyUse}/{d.vtyCap} VTY
                </span>
              )}
            </div>
            {d.current && (
              <div className="ndv__live-current">
                <span className="ndv__live-spinner" />
                <span className="ndv__live-cmd">{d.current.command}</span>
                <span className="ndv__live-badge" style={{ color: classColor(d.current.class), borderColor: classColor(d.current.class) }}>
                  {CLASS_LABEL[d.current.class] ?? d.current.class}
                </span>
              </div>
            )}
            {d.tail.length > 0 && <TailView lines={d.tail} device={d.device} />}
            {d.cmds.length > 0 && (
              <div className="ndv__live-chips">
                {d.cmds.slice(0, 12).map((c, i) => (
                  <span
                    key={`${c.at}-${i}`}
                    className={`ndv__live-chip${c.status === "refused" ? " ndv__live-chip--refused" : ""}`}
                    title={`${c.command}${c.ms ? ` · ${c.ms}ms` : ""}${c.bytes ? ` · ${c.bytes}B` : ""}`}
                  >
                    {STATUS_ICON[c.status] ?? "·"} {c.command.length > 24 ? c.command.slice(0, 24) + "…" : c.command}
                  </span>
                ))}
              </div>
            )}
          </div>
        );
      })}

      {idle.length > 0 && (
        <div className="ndv__live-idle" title={idle.map((d) => d.device).join("、")}>
          {idle.length} 台设备空闲（{idle.slice(0, 5).map((d) => d.device).join("、")}{idle.length > 5 ? "…" : ""}）
        </div>
      )}

      {state.guardrails.length > 0 && (
        <div className="ndv__live-guardrails">
          {state.guardrails.map((g, i) => (
            <div key={`${g.at}-${i}`} className="ndv__live-guardrail">
              <span className="ndv__live-guardrail-time">{fmtClock(g.at)}</span>
              <span className="ndv__live-guardrail-main">
                拦截：<b>{g.device}</b> <code>{g.command}</code>
              </span>
              <span className="ndv__live-guardrail-reason">{g.reason}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// applyLiveBatch folds one coalesced batch in order.
function applyLiveBatch(state: LiveOpsState, events: NetDevLiveEvent[]) {
  for (const ev of events) applyLiveEvent(state, ev);
}

// TailView — the terminal-textured output tail. Sticks to the bottom unless
// the user scrolls up (hover pauses auto-scroll); click the header to expand.
function TailView({ lines, device }: { lines: string[]; device: string }) {
  const [expanded, setExpanded] = useState(false);
  const [pinned, setPinned] = useState(true);
  const boxRef = useRef<HTMLDivElement>(null);
  const prevCount = useRef(0);

  useEffect(() => {
    const el = boxRef.current;
    if (!el || !pinned) return;
    if (lines.length !== prevCount.current) {
      el.scrollTop = el.scrollHeight;
      prevCount.current = lines.length;
    }
  }, [lines, pinned]);

  return (
    <div className={`ndv__live-tail${expanded ? " ndv__live-tail--expanded" : ""}`}>
      <button
        type="button"
        className="ndv__live-tail-head"
        onClick={() => setExpanded((v) => !v)}
        title={expanded ? "收起" : "展开"}
      >
        <ChevronDown size={11} className={expanded ? "" : "ndv__live-tail-chev"} />
        输出尾随 · {device}
      </button>
      <div
        ref={boxRef}
        className="ndv__live-tail-box"
        onMouseEnter={() => setPinned(false)}
        onMouseLeave={() => setPinned(true)}
        onScroll={(e) => {
          const el = e.currentTarget;
          const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
          if (atBottom) setPinned(true);
        }}
      >
        {lines.map((line, i) => (
          <div key={i} className={`ndv__live-tail-line${line.startsWith("[") && line.includes("#") ? " ndv__live-tail-line--cmd" : ""}`}>{line || " "}</div>
        ))}
      </div>
    </div>
  );
}
