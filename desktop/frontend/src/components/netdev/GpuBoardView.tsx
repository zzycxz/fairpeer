import { useEffect } from "react";
import { app } from "../../lib/bridge";
import { PanelErrorState } from "./PanelStates";
import { usePanelData } from "./usePanelData";
import { useI18n } from "../../lib/i18n";

// GpuBoardView — 智算屏（FDE_AIINFRA gap §4.1-6，批次 C 前置）。三区：
// KPI 条（总卡数/采样主机/最高温/活动 XID）+ 卡×指标矩阵（逐设备逐卡，
// 温度/利用率/显存热感着色）+ XID 事件流（活动 + 24h 内已恢复）。
// 数据面 = gpuhealth.go 采集段 + series + Finding，零新探针。

interface Props {
  onJump?: (j: { tab: string; filter?: string }) => void;
  onFocusDevice?: (device: string) => void;
}

// 温度→色档：≤70 灰、71-85 琥珀、>85 红（与告警引擎 85 默认阈值对齐）。
function tempClass(c: number): string {
  if (c > 85) return "ndv-gpu__temp--hot";
  if (c > 70) return "ndv-gpu__temp--warm";
  return "";
}

export default function GpuBoardView({ onJump, onFocusDevice }: Props) {
  const { t } = useI18n();
  const q = usePanelData(() => app.NetDevGPUBoard(), []);
  const b = q.data;

  useEffect(() => {
    const on = (e: Event) => {
      const screens = (e as CustomEvent<{ screens?: string[] }>).detail?.screens ?? [];
      if (screens.includes("gpu") || screens.includes("overview")) q.retry();
    };
    window.addEventListener("fairpeer:netdev-dash", on);
    return () => window.removeEventListener("fairpeer:netdev-dash", on);
  }, [q.retry]);

  if (q.status === "error") return <PanelErrorState onRetry={q.retry} />;
  if (q.status === "loading" || !b) return <div className="ndv__card" style={{ padding: 16 }}>{t("ndv.gpu.loading")}</div>;

  return (
    <div className="ndv-gpu">
      <div className="ndv-gpu__generated">{t("ndv.gpu.generated", { at: b.generated_at })}</div>

      <div className="ndv-exp__kpis">
        <span className="ndv-exp__kpi"><b>{b.total_cards}</b>{t("ndv.gpu.cards")}</span>
        <span className="ndv-exp__kpi"><b>{b.sampled_devices}</b>{t("ndv.gpu.sampled")}</span>
        <span className={`ndv-exp__kpi${(b.worst_temp ?? 0) > 85 ? " ndv-exp__kpi--crit" : (b.worst_temp ?? 0) > 70 ? " ndv-exp__kpi--warn" : ""}`}>
          <b>{b.worst_temp || "—"}</b>{t("ndv.gpu.worstTemp")}{b.worst_temp_dev && <em className="ndv-sim">{b.worst_temp_dev}</em>}
        </span>
        <span className={`ndv-exp__kpi${b.xid_active > 0 ? " ndv-exp__kpi--crit" : ""}`}>
          <b>{b.xid_active}</b>{t("ndv.gpu.xidActive")}
        </span>
      </div>

      <div className="ndv-gpu__devices">
        {b.devices.length === 0 && <div className="ndv__card" style={{ padding: 16 }}>{t("ndv.gpu.empty")}</div>}
        {b.devices.map(d => (
          <div key={d.device} className="ndv__card" style={{ padding: "10px 12px" }}>
            <div className="ndv__card-title" style={{ marginBottom: 8 }}>
              <span role="button" style={{ cursor: "pointer" }} onClick={() => onFocusDevice?.(d.device)}>{d.device}</span>
              {d.profileSku && (
                <span className={`ndv__badge${d.readiness === "experimental" ? " ndv__badge--warn" : ""}`}
                  title={[d.readiness && `${t("ndv.gpu.readiness")}: ${d.readiness}`, d.interconn && `${t("ndv.gpu.interconn")}: ${d.interconn}`, d.profileNote].filter(Boolean).join(" · ")}>
                  {d.profileSku}{d.special ? `·${d.special}` : ""}{d.readiness && d.readiness !== "production" ? `·${d.readiness}` : ""}
                </span>
              )}
              {!d.reachable && <span className="ndv__badge" style={{ color: "var(--err)" }}>{t("ndv.gpu.down")}</span>}
              {d.gpuOnly && <span className="ndv__badge" title={t("ndv.gpu.gpuOnlyTip")}>{t("ndv.gpu.gpuOnly")}</span>}
              {(d.xidMax ?? 0) > 0 && <span className={`ndv__badge${(d.xidMax ?? 0) >= 79 ? " ndv__badge--warn" : ""}`}>XID {d.xidMax}</span>}
              {d.lastError && <span className="ndv__meta" style={{ color: "var(--warn)", marginLeft: "auto" }} title={d.lastError}>{d.lastError.length > 60 ? d.lastError.slice(0, 60) + "…" : d.lastError}</span>}
            </div>
            {(d.profileAdvisories ?? []).length > 0 && (
              <div className="ndv__meta" style={{ color: "var(--warn)", margin: "-2px 0 6px" }}>
                {d.profileAdvisories!.map((s, i) => <div key={i}>⚠ {s}</div>)}
              </div>
            )}
            {(d.cards ?? []).length > 0 ? (
              <table className="ndv-gpu__matrix">
                <thead>
                  <tr>
                    <th>{t("ndv.gpu.thIndex")}</th>
                    <th>{t("ndv.gpu.thModel")}</th>
                    <th>{t("ndv.gpu.thTemp")}</th>
                    <th>{t("ndv.gpu.thUtil")}</th>
                    <th>{t("ndv.gpu.thMem")}</th>
                  </tr>
                </thead>
                <tbody>
                  {(d.cards ?? []).map(c => (
                    <tr key={c.index}>
                      <td>#{c.index}</td>
                      <td className="ndv-gpu__model">{c.name || "—"}</td>
                      <td className={`ndv-gpu__temp ${tempClass(c.tempC ?? 0)}`}>{c.tempC || "—"}°C</td>
                      <td>{c.utilPct ?? 0}%</td>
                      <td>{c.memTotalMB ? `${Math.round((c.memUsedMB ?? 0) / 1024)}/${Math.round((c.memTotalMB ?? 0) / 1024)}G (${c.memPct ?? 0}%)` : "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : (
              <div className="ndv__meta">{d.reachable ? t("ndv.gpu.noCards") : t("ndv.gpu.noData")}</div>
            )}
          </div>
        ))}
      </div>

      <div className="ndv__card" style={{ padding: "10px 12px" }}>
        <div className="ndv__card-title">{t("ndv.gpu.xidStream")}</div>
        {(b.xid_events ?? []).length === 0 && <div className="ndv__meta">{t("ndv.gpu.xidEmpty")}</div>}
        {(b.xid_events ?? []).map(ev => (
          <div key={ev.id} className="ndv-gpu__xidrow" style={{ display: "flex", gap: 8, alignItems: "center", padding: "3px 0" }}>
            <span className="dim">{ev.at}</span>
            <span role="button" style={{ cursor: "pointer", textDecoration: "underline dotted" }} onClick={() => onFocusDevice?.(ev.device)}>{ev.device}</span>
            <span className={ev.severity === "critical" ? "ndv__badge ndv__badge--warn" : "ndv__badge"}>#{ev.maxCode || "?"} {ev.severity}</span>
            {!ev.active && <span className="dim">{t("ndv.gpu.xidRecovered")}</span>}
            <span style={{ marginLeft: "auto" }} />
            {ev.active && (
              <span className="btn btn--secondary btn--small" role="button" onClick={() => onJump?.({ tab: "findings" })}>
                {t("ndv.gpu.xidOpen")}
              </span>
            )}
          </div>
        ))}
      </div>

      <div className="ndv__card" style={{ padding: "10px 12px" }}>
        <div className="ndv__card-title" title={t("ndv.gpu.svcZoneTip")}>{t("ndv.gpu.svcZone")}</div>
        {(b.services ?? []).length === 0 && <div className="ndv__meta">{t("ndv.gpu.svcEmpty")}</div>}
        {(b.services ?? []).length > 0 && (
          <table className="ndv-gpu__matrix">
            <thead>
              <tr>
                <th>{t("ndv.gpu.svcDevice")}</th>
                <th>{t("ndv.gpu.svcPort")}</th>
                <th>{t("ndv.gpu.svcModel")}</th>
                <th>{t("ndv.gpu.svcKv")}</th>
                <th>{t("ndv.gpu.svcRunning")}</th>
                <th>{t("ndv.gpu.svcQueued")}</th>
                <th>{t("ndv.gpu.svcTtft")}</th>
                <th>{t("ndv.gpu.svcPreempt")}</th>
                <th>{t("ndv.gpu.svcTok")}</th>
              </tr>
            </thead>
            <tbody>
              {(b.services ?? []).map(s => (
                <tr key={`${s.device}:${s.svc}:${s.model ?? ""}`}>
                  <td><span role="button" style={{ cursor: "pointer", textDecoration: "underline dotted" }} onClick={() => onFocusDevice?.(s.device)}>{s.device}</span></td>
                  <td>:{s.svc}</td>
                  <td className="ndv-gpu__model">{s.model || "—"}</td>
                  <td className={(s.kvUsage ?? 0) >= 90 ? "ndv-gpu__temp ndv-gpu__temp--hot" : (s.kvUsage ?? 0) >= 80 ? "ndv-gpu__temp ndv-gpu__temp--warm" : ""}>{s.kvUsage}%</td>
                  <td>{s.running}</td>
                  <td>{s.queued}</td>
                  <td>{s.ttftMs != null ? `${Math.round(s.ttftMs)}ms` : "—"}</td>
                  <td>{s.preemptRate != null ? `${s.preemptRate.toFixed(1)}/min` : "—"}</td>
                  <td>{s.tokensRate != null ? `${Math.round(s.tokensRate)}/s` : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {(b.services ?? []).some(s => s.ageMin > 5) && (
          <div className="ndv__meta" style={{ color: "var(--warn)" }}>{t("ndv.gpu.svcStale")}</div>
        )}
      </div>
    </div>
  );
}
