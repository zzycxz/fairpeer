// SrvConfCard — 服务器配置文件管理（NETDEV_SPEC_V2 §7.3）：设备卡「变更」区
// 的配置文件面板。白名单 = 设备 config_paths（log_paths 同款授权模式）：
// 拍快照入备份库 → 两版本 UnifiedDiff → 环境 Drift（同路径跨设备对比）。
// **修改不在此发生**：编辑产物以 file-upload 变更提交（人对整份内容签
// 字）；备份可恢复性由 restore-verify 变更步骤演练（后端已就绪，经对话起
// 草）。
import { useCallback, useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import type { NetDevDeviceView, NetDevSrvConfDriftRow, NetDevSrvConfVersion } from "../../lib/types";

export function SrvConfCard({ device, peers }: { device: NetDevDeviceView; peers: NetDevDeviceView[] }) {
  const t = useT();
  const roots = device.configPaths ?? [];
  const [path, setPath] = useState(roots[0] ?? "");
  const [versions, setVersions] = useState<NetDevSrvConfVersion[] | null>(null);
  const [picked, setPicked] = useState<string[]>([]);
  const [diff, setDiff] = useState("");
  const [drift, setDrift] = useState<NetDevSrvConfDriftRow[] | null>(null);
  const [driftPeers, setDriftPeers] = useState<Record<string, boolean>>({});
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");

  const loadVersions = useCallback(async () => {
    if (!path) return;
    try {
      setVersions(await app.NetDevSrvConfVersions(device.name, path));
      setPicked([]);
      setDiff("");
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, [device.name, path]);
  useEffect(() => { void loadVersions(); }, [loadVersions]);

  if (roots.length === 0) {
    return (
      <div className="ndv__hint">
        {t("ndv.srv.noPaths1")}<code>config_paths</code>{t("ndv.srv.noPaths2")}
      </div>
    );
  }

  const snapshot = async () => {
    setBusy("snap");
    try {
      await app.NetDevSrvConfSnapshot(device.name, path);
      await loadVersions();
    } catch (e) { setErr(String(e)); }
    finally { setBusy(""); }
  };

  const pick = (id: string) => {
    const next = picked.includes(id) ? picked.filter(x => x !== id) : [...picked, id].slice(-2);
    setPicked(next);
    if (next.length === 2) {
      void app.NetDevSrvConfDiff(next[0], next[1])
        .then(setDiff)
        .catch(e => setErr(String(e)));
    } else {
      setDiff("");
    }
  };

  const runDrift = async () => {
    setBusy("drift");
    try {
      const sel = [device.name, ...Object.entries(driftPeers).filter(([, v]) => v).map(([k]) => k)];
      if (sel.length < 2) throw new Error(t("ndv.srv.pickTwo"));
      setDrift(await app.NetDevSrvConfDrift(path, sel));
      setErr("");
    } catch (e) { setErr(String(e)); }
    finally { setBusy(""); }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      <div style={{ display: "flex", gap: 6 }}>
        <select className="mem-input" value={path} onChange={e => setPath(e.target.value)}>
          {roots.map(r => <option key={r} value={r}>{r}</option>)}
        </select>
        <input className="mem-input" style={{ flex: 1 }} placeholder={t("ndv.srv.phPath")} value={path.startsWith(roots[0]) ? path : roots[0]} onChange={e => setPath(e.target.value)} />
        <span className="btn btn--secondary btn--small" role="button" onClick={() => void snapshot()}>{busy === "snap" ? t("ndv.srv.snapping") : t("ndv.srv.snapshot")}</span>
      </div>
      {err && <div className="ndv__hint ndv__hint--err">{err}</div>}
      {versions !== null && (
        <div className="ndv__audit-scroll" style={{ maxHeight: 120 }}>
          {versions.length === 0 && <div className="ndv__hint">{t("ndv.srv.noSnapshots")}</div>}
          {versions.map(v => (
            <div key={v.id} className="ndv__audit-row" role="button" style={{ cursor: "pointer", opacity: picked.includes(v.id) ? 1 : 0.7 }} onClick={() => pick(v.id)} title={t("ndv.srv.pickTwoTip")}>
              <span className="ndv__audit-time">{v.at}</span>
              <span className="ndv__audit-dev">{t("ndv.srv.linesBytes", { lines: v.lines, bytes: v.bytes })}</span>
              <span className="ndv__audit-cmd">{picked.includes(v.id) ? "☑ " : "☐ "}{v.id}</span>
            </div>
          ))}
        </div>
      )}
      {diff && (
        <div>
          <div className="ndv__group-label">{t("ndv.srv.versionDiff")}</div>
          <pre className="ndv-cutover__report-body" style={{ maxHeight: 160 }}>{diff || t("ndv.srv.noDiff")}</pre>
        </div>
      )}
      <div className="ndv__group-label">{t("ndv.srv.envDrift")}</div>
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
        {peers.filter(p => p.name !== device.name).map(p => (
          <label key={p.name} className="ndv__meta" style={{ display: "inline-flex", gap: 4, alignItems: "center" }}>
            <input type="checkbox" checked={!!driftPeers[p.name]} onChange={e => setDriftPeers({ ...driftPeers, [p.name]: e.target.checked })} />
            {p.name}
          </label>
        ))}
        <span className="btn btn--secondary btn--small" role="button" onClick={() => void runDrift()}>{busy === "drift" ? t("ndv.comparing") : t("ndv.srv.diffBtn")}</span>
      </div>
      {drift && drift.map(r => (
        <div key={r.device} className="ndv-cutover__pick">
          <div className="ndv-cutover__pick-head">
            <span>{r.device}</span>
            <span className="ndv__meta">
              {r.status === "same" ? t("ndv.srv.stSame") : r.status === "drift" ? t("ndv.srv.stDrift") : r.status === "absent" ? t("ndv.srv.stAbsent") : "⛔ " + r.error}
            </span>
          </div>
          {r.diff && <pre className="ndv-cutover__report-body" style={{ maxHeight: 140 }}>{r.diff}</pre>}
        </div>
      ))}
      <div className="ndv__hint ndv__hint--flush">
        {t("ndv.srv.changeViaProposal")}
      </div>
    </div>
  );
}
