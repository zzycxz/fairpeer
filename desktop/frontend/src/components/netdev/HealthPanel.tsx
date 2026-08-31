import { useEffect, useState } from "react";
import { app, onNetdevHealth } from "../../lib/bridge";
import { exportTextFile } from "../../lib/netdevExport";
import { useI18n } from "../../lib/i18n";
import type { NetDevDeviceHealth } from "../../lib/types";

// HealthPanel — the 健康 dock tab: the SNMP health sweep's fleet view
// (reachability / uptime / interface up-down). Pulls the snapshot on mount,
// merges "netdev:health" change events; the poller runs Go-side at
// [netdev].poll_interval_seconds (0 = off — the panel says so).
//
// completion-spec §4.3: 接口明细展开（数据一直在手，过去只算计数）；
// sparkline 指标选择（可达/掉线口/up 口——MetricHistory 三列都有）；
// 默认异常置顶 + 「仅看异常」过滤。i18n（§4.9）：本面板全量双语。

type Metric = "up" | "ifdown" | "ifup";

// Sparkline: compact step area (SVG, no chart lib). 可达 = 阶梯面积
// （绿=可达、缺口=宕）；接口指标 = 归一化折线。一眼回答「今天一直
// 好好的，还是抖过」。
function Sparkline({ device, metric }: { device: string; metric: Metric }) {
  const { t } = useI18n();
  const [pts, setPts] = useState<{ time: string; up: boolean; us: number; iu: number; id: number }[] | null>(null);
  useEffect(() => {
    let alive = true;
    app.NetDevMetricHistory(device).then(h => { if (alive) setPts(h ?? []); }).catch(() => {});
    return () => { alive = false; };
  }, [device]);
  if (!pts || pts.length < 2) return null;
  const chron = [...pts].reverse(); // oldest → newest
  const W = 240, H = 16;
  const step = W / (chron.length - 1);
  if (metric === "up") {
    let path = "";
    chron.forEach((p, i) => {
      const x = i * step, y = p.up ? 0 : H;
      path += (i === 0 ? `M${x},${y}` : ` L${x},${y}`);
    });
    const downs = chron.filter(p => !p.up).length;
    return (
      <svg width={W} height={H} style={{ display: "block", marginLeft: 22 }} aria-label={t("ndv.health.ariaReach")}>
        <path d={`${path} L${W},${H} L0,${H} Z`} fill="var(--ok)" opacity="0.18" />
        <path d={path} fill="none" stroke={downs > 0 ? "var(--warn)" : "var(--ok)"} strokeWidth="1.5" />
      </svg>
    );
  }
  // 接口指标：取值归一化到 [0, H]，值域宽度留 1px 边距。
  const val = (p: { iu: number; id: number }) => (metric === "ifdown" ? p.id : p.iu);
  const vals = chron.map(val);
  const lo = Math.min(...vals, 0), hiRaw = Math.max(...vals);
  const hi = Math.max(hiRaw, lo + 1);
  const y = (p: { iu: number; id: number }) => H - 1 - ((val(p) - lo) / (hi - lo)) * (H - 2);
  let path = "";
  chron.forEach((p, i) => {
    const x = i * step;
    path += (i === 0 ? `M${x},${y(p)}` : ` L${x},${y(p)}`);
  });
  const bad = metric === "ifdown" && vals.some(v => v > 0);
  return (
    <svg width={W} height={H} style={{ display: "block", marginLeft: 22 }} aria-label={t(metric === "ifdown" ? "ndv.health.ariaIfDown" : "ndv.health.ariaIfUp")}>
      <path d={path} fill="none" stroke={bad ? "var(--warn)" : "var(--accent)"} strokeWidth="1.5" />
    </svg>
  );
}

export function HealthPanel({ onOpenSettings }: { onOpenSettings?: (tab: string) => void }) {
  const { t } = useI18n();
  const [snap, setSnap] = useState<{ pollIntervalSeconds: number; devices: NetDevDeviceHealth[] } | null>(null);
  const [err, setErr] = useState("");
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);
  const [metric, setMetric] = useState<Metric>("up");
  const [onlyBad, setOnlyBad] = useState(false);
  const [expanded, setExpanded] = useState<string>("");

  const reload = () => {
    app.NetDevHealthSnapshot().then(s => { setSnap(s); setUpdatedAt(new Date()); setErr(""); }).catch(e => setErr(String(e)));
  };

  useEffect(() => {
    reload();
    return onNetdevHealth(h => {
      setSnap(prev => {
        if (!prev) return prev;
        const devices = prev.devices.map(d => d.device === h.device ? h : d);
        return { ...prev, devices };
      });
      setUpdatedAt(new Date());
    });
  }, []);

  // "Xs 前" ticker: re-render every 30s so the header stays honest.
  const [, forceTick] = useState(0);
  useEffect(() => {
    const timer = setInterval(() => forceTick(n => n + 1), 30_000);
    return () => clearInterval(timer);
  }, []);
  const ago = updatedAt ? Math.max(0, Math.round((Date.now() - updatedAt.getTime()) / 1000)) : null;

  const fmtUptime = (sec: number): string => {
    if (sec <= 0) return "—";
    const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
    if (d > 0) return t("ndv.uptimeDays", { d, h });
    if (h > 0) return t("ndv.uptimeHours", { h, m });
    return t("ndv.uptimeMins", { m });
  };

  const ifUp = (d: NetDevDeviceHealth) => (d.interfaces ?? []).filter(i => i.operUp).length;
  const ifDown = (d: NetDevDeviceHealth) => (d.interfaces ?? []).filter(i => i.adminUp && !i.operUp).length;
  const isBad = (d: NetDevDeviceHealth) => !d.reachable || ifDown(d) > 0;

  const shown = snap
    ? (onlyBad ? (snap.devices ?? []).filter(isBad) : (snap.devices ?? []))
      // 异常置顶：不可达最前，其次掉线口多者，稳定序兜底按名。
      .slice().sort((a, b) => Number(isBad(b)) - Number(isBad(a)) || ifDown(b) - ifDown(a) || a.device.localeCompare(b.device))
    : [];

  return (
    <div className="ndv__card">
      <div className="ndv__card-title">
        {t("ndv.health.title")}
        {ago !== null && <span className="ndv__meta" style={{ marginLeft: 8, fontWeight: 400 }}>{ago < 60 ? t("ndv.agoSec", { n: ago }) : t("ndv.agoMin", { n: Math.round(ago / 60) })}</span>}
        <select className="mem-select" style={{ width: 92, fontSize: 11, marginLeft: 8 }} value={metric}
          onChange={e => setMetric(e.target.value as Metric)} title={t("ndv.health.metricPick")}>
          <option value="up">{t("ndv.health.metricUp")}</option>
          <option value="ifdown">{t("ndv.health.metricIfDown")}</option>
          <option value="ifup">{t("ndv.health.metricIfUp")}</option>
        </select>
        <span className={`btn btn--small ${onlyBad ? "btn--primary" : "btn--secondary"}`} role="button" style={{ fontSize: 11 }}
          onClick={() => setOnlyBad(v => !v)}>{t("ndv.health.onlyBad")}</span>
        <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }} onClick={reload}>{t("ndv.refresh")}</span>
        {snap && snap.devices.length > 0 && (
          <span className="btn btn--secondary btn--small" role="button" title={t("ndv.health.exportTip")} onClick={() => void (async () => {
            const esc = (v: string) => /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
            const csv = [
              "device,reachable,uptime_sec,if_up,if_down,down_interfaces,last_error",
              ...snap!.devices.map(d => [
                d.device, d.reachable ? "1" : "0", String(d.uptimeSec),
                String(ifUp(d)), String(ifDown(d)),
                esc(d.interfaces.filter(i => i.adminUp && !i.operUp).map(i => i.name).join(" ")),
                esc(d.lastError ?? ""),
              ].join(",")),
            ].join("\n") + "\n";
            const p = await exportTextFile(`netdev-health-${new Date().toISOString().slice(0, 10)}.csv`, csv, "text/csv");
            if (p) setErr(t("ndv.exportedTo", { path: p }));
          })()}>{t("ndv.health.exportCsv")}</span>
        )}
      </div>
      {err && <div className="ndv__hint">{err}</div>}
      {!snap && !err && <div className="ndv__hint">{t("ndv.loading")}</div>}
      {snap && snap.pollIntervalSeconds <= 0 && (
        <div className="ndv__hint" style={{ marginBottom: 8, display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
          {t("ndv.health.pollOff")}
          {onOpenSettings && <span className="btn btn--secondary btn--small" role="button" onClick={() => onOpenSettings("netdev")}>{t("ndv.goSettings")}</span>}
        </div>
      )}
      {snap && snap.devices.length === 0 && (
        <div className="ndv__hint">{t("ndv.health.noSnmp")}</div>
      )}
      {snap && shown.length === 0 && snap.devices.length > 0 && <div className="ndv__hint">{t("ndv.health.noMatch")}</div>}
      {snap && shown.map(d => (
        <div key={d.device} className="ndv__device" style={{ flexDirection: "column", alignItems: "stretch", gap: 2 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span className={`ndv__dot ${d.reachable ? "ndv__dot--ok" : "ndv__dot--down"}`} />
            <span className="ndv__device-name">{d.device}</span>
            <span className="ndv__device-addr" role="button" style={{ cursor: "pointer" }} title={t("ndv.health.ifDetail")}
              onClick={() => setExpanded(x => x === d.device ? "" : d.device)}>
              {d.reachable
                ? (d.interfaces.length > 0
                    ? t("ndv.health.onlineIfs", { uptime: fmtUptime(d.uptimeSec), up: ifUp(d), down: ifDown(d) })
                    : t("ndv.health.online", { uptime: fmtUptime(d.uptimeSec) }))
                : (d.lastError || t("ndv.health.unreachable"))}
            </span>
            {ifDown(d) > 0 && <span className="ndv__badge ndv__badge--warn">{t("ndv.health.ifDownBadge", { n: ifDown(d) })}</span>}
            {(d.cpuPct ?? 0) > 0 && (
              <span className="ndv-health__water" data-hot={(d.cpuPct ?? 0) >= 80} title={t("ndv.health.waterTip")}>
                cpu {d.cpuPct}%{d.memPct ? ` · mem ${d.memPct}%` : ""}
              </span>
            )}
          </div>
          <Sparkline device={d.device} metric={metric} />
          {expanded === d.device && d.interfaces.length > 0 && (
            <div style={{ marginLeft: 22, borderLeft: "1px solid var(--border)", paddingLeft: 8, display: "flex", flexDirection: "column", gap: 1 }}>
              {d.interfaces.map(i => (
                <div key={i.name} style={{ display: "flex", gap: 8, fontSize: 11 }}>
                  <span style={{ width: 6, height: 6, borderRadius: "50%", background: i.operUp ? "var(--ok)" : i.adminUp ? "var(--err, #e5484d)" : "var(--fg-faint)", marginTop: 4 }} />
                  <span style={{ fontFamily: "var(--font-mono, monospace)", minWidth: 110 }}>{i.name}</span>
                  <span style={{ opacity: 0.65 }}>{i.operUp ? "up" : i.adminUp ? t("ndv.health.ifDownAdminUp") : "admin down"}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
