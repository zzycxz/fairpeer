import { useEffect, useState } from "react";
import { app, onNetdevHealth } from "../../lib/bridge";
import type { NetDevDeviceHealth } from "../../lib/types";

// HealthPanel — the 健康 dock tab: the SNMP health sweep's fleet view
// (reachability / uptime / interface up-down). Pulls the snapshot on mount,
// merges "netdev:health" change events; the poller runs Go-side at
// [netdev].poll_interval_seconds (0 = off — the panel says so).

export function HealthPanel() {
  const [snap, setSnap] = useState<{ pollIntervalSeconds: number; devices: NetDevDeviceHealth[] } | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    let alive = true;
    app.NetDevHealthSnapshot().then(s => { if (alive) setSnap(s); }).catch(e => setErr(String(e)));
    return onNetdevHealth(h => {
      setSnap(prev => {
        if (!prev) return prev;
        const devices = prev.devices.map(d => d.device === h.device ? h : d);
        return { ...prev, devices };
      });
    });
  }, []);

  const fmtUptime = (sec: number): string => {
    if (sec <= 0) return "—";
    const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
    return d > 0 ? `${d}天${h}时` : h > 0 ? `${h}时${m}分` : `${m}分`;
  };

  const ifUp = (d: NetDevDeviceHealth) => d.interfaces.filter(i => i.operUp).length;
  const ifDown = (d: NetDevDeviceHealth) => d.interfaces.filter(i => i.adminUp && !i.operUp).length;

  return (
    <div className="ndv__card">
      <div className="ndv__card-title">健康</div>
      {err && <div className="ndv__hint">{err}</div>}
      {!snap && !err && <div className="ndv__hint">加载中…</div>}
      {snap && snap.pollIntervalSeconds <= 0 && (
        <div className="ndv__hint" style={{ marginBottom: 8 }}>
          SNMP 健康轮询未开启——设置 → 运维 → 采集策略 设 poll_interval_seconds（如 60），并为设备配置 [snmp] 块后生效。
        </div>
      )}
      {snap && snap.devices.length === 0 && (
        <div className="ndv__hint">还没有配置 SNMP 的设备——在 设置 → 运维 的设备编辑里填 SNMP 团体字（v2c）。</div>
      )}
      {snap && snap.devices.map(d => (
        <div key={d.device} className="ndv__device">
          <span className={`ndv__dot ${d.reachable ? "ndv__dot--ok" : "ndv__dot--down"}`} />
          <span className="ndv__device-name">{d.device}</span>
          <span className="ndv__device-addr">
            {d.reachable
              ? `在线 · uptime ${fmtUptime(d.uptimeSec)} · 接口 ${ifUp(d)} up / ${ifDown(d)} down`
              : (d.lastError || "不可达")}
          </span>
          {ifDown(d) > 0 && <span className="ndv__badge ndv__badge--warn">{ifDown(d)} 掉线</span>}
        </div>
      ))}
    </div>
  );
}
