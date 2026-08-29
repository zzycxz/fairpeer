import { useEffect, useState } from "react";
import { app, onNetdevHealth } from "../../lib/bridge";
import type { NetDevDeviceHealth } from "../../lib/types";

// HealthPanel — the 健康 dock tab: the SNMP health sweep's fleet view
// (reachability / uptime / interface up-down). Pulls the snapshot on mount,
// merges "netdev:health" change events; the poller runs Go-side at
// [netdev].poll_interval_seconds (0 = off — the panel says so).

// Sparkline: the device's reachable-history as a compact step area (SVG, no
// chart lib). Green = reachable, gap/red = down — one glance answers "it was
// fine all day, or was it?".
function Sparkline({ device }: { device: string }) {
  const [pts, setPts] = useState<{ time: string; up: boolean; iu: number; id: number }[] | null>(null);
  useEffect(() => {
    let alive = true;
    app.NetDevMetricHistory(device).then(h => { if (alive) setPts(h ?? []); }).catch(() => {});
    return () => { alive = false; };
  }, [device]);
  if (!pts || pts.length < 2) return null;
  const chron = [...pts].reverse(); // oldest → newest
  const W = 240, H = 16;
  const step = W / (chron.length - 1);
  let path = "";
  chron.forEach((p, i) => {
    const x = i * step, y = p.up ? 0 : H;
    path += (i === 0 ? `M${x},${y}` : ` L${x},${y}`);
  });
  const downs = chron.filter(p => !p.up).length;
  return (
    <svg width={W} height={H} style={{ display: "block", marginLeft: 22 }} aria-label="可达性历史">
      <path d={`${path} L${W},${H} L0,${H} Z`} fill="var(--ok)" opacity="0.18" />
      <path d={path} fill="none" stroke={downs > 0 ? "var(--warn)" : "var(--ok)"} strokeWidth="1.5" />
    </svg>
  );
}

export function HealthPanel({ onOpenSettings }: { onOpenSettings?: (tab: string) => void }) {
  const [snap, setSnap] = useState<{ pollIntervalSeconds: number; devices: NetDevDeviceHealth[] } | null>(null);
  const [err, setErr] = useState("");
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null);

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
    const t = setInterval(() => forceTick(n => n + 1), 30_000);
    return () => clearInterval(t);
  }, []);
  const ago = updatedAt ? Math.max(0, Math.round((Date.now() - updatedAt.getTime()) / 1000)) : null;

  const fmtUptime = (sec: number): string => {
    if (sec <= 0) return "—";
    const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
    return d > 0 ? `${d}天${h}时` : h > 0 ? `${h}时${m}分` : `${m}分`;
  };

  const ifUp = (d: NetDevDeviceHealth) => d.interfaces.filter(i => i.operUp).length;
  const ifDown = (d: NetDevDeviceHealth) => d.interfaces.filter(i => i.adminUp && !i.operUp).length;

  return (
    <div className="ndv__card">
      <div className="ndv__card-title">
        健康
        {ago !== null && <span className="ndv__meta" style={{ marginLeft: 8, fontWeight: 400 }}>{ago < 60 ? `${ago}s 前` : `${Math.round(ago / 60)}m 前`}</span>}
        <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }} onClick={reload}>刷新</span>
      </div>
      {err && <div className="ndv__hint">{err}</div>}
      {!snap && !err && <div className="ndv__hint">加载中…</div>}
      {snap && snap.pollIntervalSeconds <= 0 && (
        <div className="ndv__hint" style={{ marginBottom: 8, display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
          SNMP 健康轮询未开启——设置 → 运维 → 定时任务 设轮询间隔，设备编辑里配 SNMP 团体字。
          {onOpenSettings && <span className="btn btn--secondary btn--small" role="button" onClick={() => onOpenSettings("netdev")}>去设置</span>}
        </div>
      )}
      {snap && snap.devices.length === 0 && (
        <div className="ndv__hint">还没有配置 SNMP 的设备——在 设置 → 运维 的设备编辑里填 SNMP 团体字（v2c）。</div>
      )}
      {snap && snap.devices.map(d => (
        <div key={d.device} className="ndv__device" style={{ flexDirection: "column", alignItems: "stretch", gap: 2 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span className={`ndv__dot ${d.reachable ? "ndv__dot--ok" : "ndv__dot--down"}`} />
            <span className="ndv__device-name">{d.device}</span>
            <span className="ndv__device-addr">
              {d.reachable
                ? `在线 · uptime ${fmtUptime(d.uptimeSec)} · 接口 ${ifUp(d)} up / ${ifDown(d)} down`
                : (d.lastError || "不可达")}
            </span>
            {ifDown(d) > 0 && <span className="ndv__badge ndv__badge--warn">{ifDown(d)} 掉线</span>}
          </div>
          <Sparkline device={d.device} />
        </div>
      ))}
    </div>
  );
}
