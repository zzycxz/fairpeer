import { useEffect, useMemo, useRef, useState } from "react";
import { app, onNetdevLogFollow } from "../../lib/bridge";
import type { NetDevDBSourceView, NetDevDeviceView, NetDevLogFollowEvent } from "../../lib/types";

// LogPanel — the 日志 dock tab: the structured log-source reader/streamer on
// top of netdev_log_read / log follow. The agent never free-hands shell and
// neither does this panel: sources are file:/journal:/docker:/db: and every
// read goes through the sealed Go path (classifier, budget, redaction, audit).
//
// Layout contract: left source tree (host → source), right viewer; the main
// conversation is untouched. "交给 AI" fills the main composer via
// onInsertComposer with a bounded excerpt (the evidence, not the firehose).

const MAX_VIEW_LINES = 2000;

// Canonical diagnostic queries per db type — buttons only offer entries the
// source's allowlist actually contains (the seal is server-side; this just
// avoids dead buttons).
const DB_QUICK: Record<string, string[]> = {
  mysql: [
    "SHOW PROCESSLIST",
    "SHOW ENGINE INNODB STATUS",
    "SELECT * FROM information_schema.processlist",
    "SELECT * FROM information_schema.processlist WHERE command <> 'Sleep'",
  ],
  postgres: [
    "SELECT * FROM pg_stat_activity",
    "SELECT * FROM pg_stat_activity WHERE state <> 'idle'",
    "SELECT * FROM pg_stat_replication",
  ],
  redis: ["slowlog get 10", "info", "info memory", "dbsize", "client list"],
};

const levelClass = (line: string): string => {
  const l = line.toLowerCase();
  if (/(error|err|fatal|crit|emerg|failed|failure|refused|denied)/.test(l)) return "ndv-log__line--error";
  if (/(warn|warning|retry|slow|timeout)/.test(l)) return "ndv-log__line--warn";
  return "";
};

export function LogPanel({ devices, dbSources, onInsertComposer }: {
  devices: NetDevDeviceView[];
  dbSources: NetDevDBSourceView[];
  onInsertComposer?: (text: string) => void;
}) {
  const hosts = useMemo(() => devices.filter(d => d.vendor === "linux" || d.vendor === "windows" || d.vendor === "vmware"), [devices]);
  const [device, setDevice] = useState(hosts[0]?.name ?? "");
  const dev = hosts.find(h => h.name === device);

  // Source selection: kind + free-form target (unit / path / container).
  const [kind, setKind] = useState<"file" | "journal" | "docker" | "db" | "syslog">("file");
  const [target, setTarget] = useState("/var/log/syslog");
  const [unit, setUnit] = useState("nginx");
  const [container, setContainer] = useState("");
  const [dbSource, setDbSource] = useState(dbSources[0]?.name ?? "");
  const dbSrc = dbSources.find(s => s.name === dbSource);

  // Read params + output.
  const [tailN, setTailN] = useState(100);
  const [since, setSince] = useState("");
  const [grep, setGrep] = useState("");
  const [lines, setLines] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [following, setFollowing] = useState(false);
  const boxRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!device && hosts[0]) setDevice(hosts[0].name);
    if (!dbSource && dbSources[0]) setDbSource(dbSources[0].name);
  }, [hosts, dbSources, device, dbSource]);

  const source = kind === "db" ? `db:${dbSource}` : kind === "journal" ? `journal:${unit}` : kind === "docker" ? `docker:${container}` : `file:${target}`;
  const allowlisted = (q: string): boolean => {
    if (!dbSrc) return false;
    const norm = (s: string) => s.trim().replace(/\s+/g, " ").replace(/;$/, "").toLowerCase();
    return dbSrc.allowlist.some(a => {
      const na = norm(a);
      return na === norm(q) || norm(q).startsWith(na + " ");
    });
  };

  const appendLines = (newLines: string[]) => {
    setLines(prev => [...prev, ...newLines].slice(-MAX_VIEW_LINES));
  };

  // Streaming follow events (filtered to the selected device).
  useEffect(() => {
    return onNetdevLogFollow((ev: NetDevLogFollowEvent) => {
      if (ev.device !== device) return;
      if (ev.done) {
        setFollowing(false);
        setNote(`跟踪已停止：${ev.reason ?? ""}`);
        return;
      }
      if (ev.chunk) appendLines(ev.chunk.split("\n").filter(Boolean));
    });
  }, [device]);

  // Auto-scroll while following.
  useEffect(() => {
    if (following && boxRef.current) boxRef.current.scrollTop = boxRef.current.scrollHeight;
  }, [lines, following]);

  const read = async () => {
    if (kind === "db") return; // db reads go through the quick-query buttons
    if (!device || (kind === "docker" && !container.trim())) { setNote("请先选择设备/填好源"); return; }
    setBusy(true); setNote("");
    try {
      if (following) { await app.NetDevLogFollowStop(device); setFollowing(false); }
      if (kind === "syslog") {
        const rows = await app.NetDevSyslogTail(device, tailN, grep);
        setLines(rows);
        if ((rows ?? []).length === 0) setNote("缓冲区为空——设备需把 syslog 指向本机接收端口（设置 → 运维 → syslog）。");
        return;
      }
      const res = await app.NetDevLogRead(device, source, tailN, since, grep);
      if (res.refused) {
        setLines([]);
        setNote(res.refusal ?? "已拒绝");
      } else {
        setLines((res.output ?? "").split("\n").filter(Boolean));
        if (res.is_error) setNote("设备返回错误（见输出）");
      }
    } catch (e) {
      setNote(String(e));
    } finally {
      setBusy(false);
    }
  };

  const toggleFollow = async () => {
    if (!device || kind === "syslog") return;
    if (following) {
      await app.NetDevLogFollowStop(device);
      setFollowing(false);
    } else {
      setLines([]);
      setNote("跟踪中…（硬顶自动断流）");
      try {
        await app.NetDevLogFollowStart(device, source);
        setFollowing(true);
      } catch (e) {
        setNote(String(e));
      }
    }
  };

  const dbQuery = async (q: string) => {
    setBusy(true); setNote("");
    try {
      const out = await app.NetDevDBQuery(dbSource, q);
      setLines((out ?? "").split("\n").filter(Boolean));
    } catch (e) {
      setLines([]);
      setNote(String(e));
    } finally {
      setBusy(false);
    }
  };

  const sendToAI = () => {
    if (!onInsertComposer || lines.length === 0) return;
    const excerpt = lines.slice(-80).join("\n");
    onInsertComposer(`请基于以下来自 ${device || dbSource} 的日志（${source}）诊断问题，先给结论再给证据：\n\n\`\`\`\n${excerpt}\n\`\`\``);
  };

  return (
    <div className="ndv__card" style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}>
      <div className="ndv__card-title">日志{following ? <span style={{ color: "var(--ok)" }}> · 跟踪中</span> : ""}</div>

      {/* Source bar: device + kind + target + params, one row-ish grid. */}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8 }}>
        <select className="mem-select" value={device} onChange={e => { setDevice(e.target.value); setFollowing(false); }} disabled={kind === "db"}>
          {hosts.map(h => <option key={h.name} value={h.name}>{h.name}</option>)}
          {hosts.length === 0 && <option value="">（无服务器设备）</option>}
        </select>
        <select className="mem-select" value={kind} onChange={e => { setKind(e.target.value as typeof kind); setFollowing(false); }}>
          <option value="file">文件</option>
          <option value="journal">journald 单元</option>
          <option value="docker">容器</option>
          {dbSources.length > 0 && <option value="db">数据库</option>}
          <option value="syslog">syslog（被动）</option>
        </select>
        {kind === "file" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 180 }} list="ndv-log-paths" value={target} onChange={e => setTarget(e.target.value)} placeholder="/var/log/nginx/error.log" />
        )}
        {kind === "journal" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 120 }} value={unit} onChange={e => setUnit(e.target.value)} placeholder="服务名，如 nginx" />
        )}
        {kind === "docker" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 120 }} value={container} onChange={e => setContainer(e.target.value)} placeholder="容器名" />
        )}
        {kind === "db" && (
          <select className="mem-select" value={dbSource} onChange={e => setDbSource(e.target.value)}>
            {dbSources.map(s => <option key={s.name} value={s.name}>{s.name}（{s.type}）</option>)}
          </select>
        )}
        <datalist id="ndv-log-paths">
          {["/var/log/syslog", "/var/log/auth.log", "/var/log/messages", "/var/log/secure", "/var/log/dmesg",
            ...(dev?.logPaths ?? []).map(p => `${p.replace(/\/$/, "")}/`)].map(p => <option key={p} value={p} />)}
        </datalist>
      </div>

      {/* db quick queries: allowlisted statements only. */}
      {kind === "db" && dbSrc && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8 }}>
          {(DB_QUICK[dbSrc.type] ?? []).filter(allowlisted).map(q => (
            <span key={q} className="ndv__chip" role="button" onClick={() => void dbQuery(q)}>{q}</span>
          ))}
          {(DB_QUICK[dbSrc.type] ?? []).filter(allowlisted).length === 0 && (
            <span className="ndv__hint">该源的白名单里没有常用诊断语句——去 设置 → 运维 → 数据库源 添加（如 SHOW PROCESSLIST）。</span>
          )}
        </div>
      )}

      {/* Read params + actions. */}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8, alignItems: "center" }}>
        {kind !== "db" && (
          <>
            <label className="ndv__meta">行数</label>
            <input className="mem-input" type="number" style={{ width: 64 }} value={tailN} min={1} max={1000} onChange={e => setTailN(Math.min(1000, Math.max(1, Number(e.target.value) || 100)))} />
            <label className="ndv__meta">起于</label>
            <input className="mem-input" style={{ width: 130 }} value={since} onChange={e => setSince(e.target.value)} placeholder="2026-08-27 10:00 或 -1h" />
            <label className="ndv__meta">过滤</label>
            <input className="mem-input" style={{ width: 110 }} value={grep} onChange={e => setGrep(e.target.value)} placeholder="正则" />
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void read()}>{busy ? "读取中…" : "读取"}</span>
            {kind !== "syslog" && (
              <span className={`btn btn--small ${following ? "btn--primary" : "btn--secondary"}`} role="button" onClick={() => void toggleFollow()}>{following ? "停止跟踪" : "跟踪"}</span>
            )}
          </>
        )}
        {onInsertComposer && lines.length > 0 && (
          <span className="btn btn--secondary btn--small" role="button" onClick={sendToAI}>交给 AI 诊断</span>
        )}
        {lines.length > 0 && <span className="btn btn--secondary btn--small" role="button" onClick={() => { setLines([]); setNote(""); }}>清空</span>}
      </div>

      {note && <div className="ndv__hint" style={{ marginBottom: 6 }}>{note}</div>}

      {/* Viewer. */}
      <div ref={boxRef} className="ndv-log__box" style={{ flex: 1, minHeight: 120, overflow: "auto", background: "var(--bg)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", padding: 8, fontSize: 11.5, fontFamily: "var(--font-mono, monospace)", lineHeight: 1.5 }}>
        {lines.length === 0 && !following && <span className="ndv__hint">选择源后「读取」最近日志，或「跟踪」实时流。文件路径限 /var/log 与设备 log_paths 白名单。</span>}
        {lines.map((l, i) => <div key={i} className={`ndv-log__line ${levelClass(l)}`}>{l}</div>)}
        {following && <div className="ndv-log__line" style={{ color: "var(--accent)" }}>▍</div>}
      </div>
    </div>
  );
}
