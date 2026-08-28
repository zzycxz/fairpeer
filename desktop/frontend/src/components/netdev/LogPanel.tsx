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
  mongodb: ['{"ping":1}', '{"serverStatus":1}', '{"dbStats":1}', '{"listDatabases":1}', '{"currentOp":1}'],
  mssql: [
    "SELECT * FROM sys.dm_exec_requests",
    "SELECT session_id, status, login_name FROM sys.dm_exec_sessions",
    "SELECT name, state_desc FROM sys.databases",
    "SELECT * FROM sys.dm_os_sys_info",
  ],
  clickhouse: ["SHOW PROCESSLIST", "SHOW TABLES", "SELECT * FROM system.metrics LIMIT 50", "SELECT database, name, total_bytes FROM system.parts LIMIT 20"],
  elasticsearch: ["/_cluster/health", "/_cat/indices", "/_cat/shards", "/_nodes/stats"],
};

const levelClass = (line: string): string => {
  const l = line.toLowerCase();
  if (/(error|err|fatal|crit|emerg|failed|failure|refused|denied)/.test(l)) return "ndv-log__line--error";
  if (/(warn|warning|retry|slow|timeout)/.test(l)) return "ndv-log__line--warn";
  return "";
};

export function LogPanel({ devices, dbSources, onInsertComposer, onOpenWorkbench }: {
  devices: NetDevDeviceView[];
  dbSources: NetDevDBSourceView[];
  onInsertComposer?: (text: string) => void;
  onOpenWorkbench?: () => void;
}) {
  const hosts = useMemo(() => devices.filter(d => d.vendor === "linux" || d.vendor === "windows" || d.vendor === "vmware" || d.kind === "docker" || d.kind === "k8s"), [devices]);
  const [device, setDevice] = useState(hosts[0]?.name ?? "");
  const dev = hosts.find(h => h.name === device);

  // Source selection: kind + free-form target (unit / path / container / ns+pod).
  const [kind, setKind] = useState<"file" | "journal" | "docker" | "k8s" | "db" | "syslog" | "winevt">("file");
  const [target, setTarget] = useState("/var/log/syslog");
  const [unit, setUnit] = useState("nginx");
  const [container, setContainer] = useState("");
  const [k8sNs, setK8sNs] = useState("");
  const [k8sPod, setK8sPod] = useState("");
  const [evtChannel, setEvtChannel] = useState("Security");
  const [podList, setPodList] = useState<{ value: string; label: string }[]>([]);
  const [ctrList, setCtrList] = useState<{ value: string; label: string }[]>([]);
  const [listBusy, setListBusy] = useState(false);
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
  // 筛选收纳：行数/起于/过滤 默认收起（320px dock 里常驻太挤），点「筛选」展开；
  // 有非默认参数时按钮带摘要。levelFilter 是查看器侧的等级过滤（仅显示层）。
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [levelFilter, setLevelFilter] = useState<"" | "error" | "warn">("");
  const boxRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!device && hosts[0]) setDevice(hosts[0].name);
    if (!dbSource && dbSources[0]) setDbSource(dbSources[0].name);
  }, [hosts, dbSources, device, dbSource]);

  const source = kind === "db" ? `db:${dbSource}` : kind === "journal" ? `journal:${unit}` : kind === "docker" ? `docker:${container}` : kind === "k8s" ? `k8s:${k8sNs.trim() ? k8sNs.trim() + "/" : ""}${k8sPod.trim()}` : kind === "winevt" ? `winevt:${evtChannel.trim()}` : `file:${target}`;

  // Pod/容器选择器：k8s/docker 目标一键列出再挑，不再手拼名字。列表只是
  // 选择辅助——密封在读路径上，与选择器无关。
  const loadPods = async () => {
    if (!device) return;
    setListBusy(true);
    try {
      const out = await app.NetDevK8sGet(device, "pods", "", "", 0);
      const doc = JSON.parse(out || "{}");
      setPodList(((doc.items ?? []) as any[]).slice(0, 200).map(it => {
        const ns = it?.metadata?.namespace ?? "";
        const name = it?.metadata?.name ?? "";
        return { value: ns ? `${ns}/${name}` : name, label: ns ? `${ns}/${name}` : name };
      }).filter(x => x.value));
    } catch (e) { setNote(String(e)); } finally { setListBusy(false); }
  };
  const loadContainers = async () => {
    if (!device) return;
    setListBusy(true);
    try {
      const out = await app.NetDevDockerGet(device, "ps", "", 0);
      const arr = JSON.parse(out || "[]") as any[];
      setCtrList(arr.slice(0, 200).map(c => {
        const name = String(c?.Names?.[0] ?? "").replace(/^\//, "") || String(c?.Id ?? "").slice(0, 12);
        return { value: name, label: `${name}（${c?.State ?? "?"}）` };
      }).filter(x => x.value));
    } catch (e) { setNote(String(e)); } finally { setListBusy(false); }
  };
  const allowlisted = (q: string): boolean => {
    if (!dbSrc) return false;
    // redis has no per-source allowlist — the seal is the server-side builtin
    // diagnostic command set, so every offered quick query is allowed.
    if (dbSrc.type === "redis") return true;
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

  const errCount = lines.filter(l => levelClass(l) === "ndv-log__line--error").length;
  const warnCount = lines.filter(l => levelClass(l) === "ndv-log__line--warn").length;
  const shownLines = levelFilter
    ? lines.filter(l => levelClass(l) === `ndv-log__line--${levelFilter}`)
    : lines;
  const filterActive = tailN !== 100 || since.trim() !== "" || grep.trim() !== "";

  const read = async () => {
    if (kind === "db") return; // db reads go through the quick-query buttons
    if (!device || (kind === "docker" && !container.trim()) || (kind === "k8s" && !k8sPod.trim()) || (kind === "winevt" && !evtChannel.trim())) { setNote("请先选择设备/填好源"); return; }    setBusy(true); setNote("");
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

  // Switching device/kind must STOP the server-side stream too — flipping the
  // UI flag alone would leave the old follow pulling lines until its caps.
  const stopFollow = async () => {
    if (!following || !device) return;
    try { await app.NetDevLogFollowStop(device); } catch { /* already gone */ }
    setFollowing(false);
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

  // DB 结果表格化：每行都是 JSON 对象时按表格渲染（列取并集，≤60 行 × ≤12 列）。
  const { jsonCols, jsonRows } = useMemo(() => {
    const empty = { jsonCols: [] as string[], jsonRows: [] as Record<string, unknown>[] };
    if (lines.length === 0 || lines.length > 60) return empty;
    const rows: Record<string, unknown>[] = [];
    for (const l of lines.slice(0, 60)) {
      if (!l.startsWith("{")) return empty;
      try { rows.push(JSON.parse(l) as Record<string, unknown>); } catch { return empty; }
    }
    const cols: string[] = [];
    for (const r of rows) for (const k of Object.keys(r)) if (!cols.includes(k)) cols.push(k);
    return { jsonCols: cols.slice(0, 12), jsonRows: rows };
  }, [lines]);

  const sendToAI = () => {
    if (!onInsertComposer || lines.length === 0) return;
    const excerpt = lines.slice(-80).join("\n");
    onInsertComposer(`请基于以下来自 ${device || dbSource} 的日志（${source}）诊断问题，先给结论再给证据：\n\n\`\`\`\n${excerpt}\n\`\`\``);
  };

  return (
    <div className="ndv__card" style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}>
      <div className="ndv__card-title">日志{following ? <span style={{ color: "var(--ok)" }}> · 跟踪中</span> : ""}
        {onOpenWorkbench && <span className="ndv__chip" role="button" style={{ marginLeft: 8, fontWeight: 400 }} title="多源合并时间线与跨设备搜索在主区工作台" onClick={onOpenWorkbench}>⟶ 工作台 · 多源合并</span>}
      </div>

      {/* Source bar: device + kind + target + params, one row-ish grid. */}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8 }}>
        <select className="mem-select" value={device} onChange={e => {
          void stopFollow();
          setDevice(e.target.value);
          const nd = hosts.find(h => h.name === e.target.value);
          if (nd?.kind === "k8s") setKind("k8s");
          else if (nd?.kind === "docker") setKind("docker");
          else setKind("file");
        }} disabled={kind === "db"}>
          {hosts.map(h => <option key={h.name} value={h.name}>{h.name}{h.kind ? `（${h.kind}）` : ""}</option>)}
          {hosts.length === 0 && <option value="">（无服务器设备）</option>}
        </select>
        <select className="mem-select" value={kind} onChange={e => { void stopFollow(); setKind(e.target.value as typeof kind); }}>
          {dev?.kind !== "k8s" && dev?.kind !== "docker" && <>
            <option value="file">文件</option>
            <option value="journal">journald 单元</option>
            <option value="docker">容器（SSH docker logs）</option>
            {dbSources.length > 0 && <option value="db">数据库</option>}
            <option value="syslog">syslog（被动）</option>
          </>}
          {dev?.kind === "k8s" && <option value="k8s">K8s Pod（只读 API）</option>}
          {dev?.kind === "docker" && <option value="docker">容器（只读 API）</option>}
          {dev?.vendor === "windows" && !dev?.kind && <option value="winevt">Windows 事件</option>}
        </select>
        {kind === "file" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 180 }} list="ndv-log-paths" value={target} onChange={e => setTarget(e.target.value)} placeholder="/var/log/nginx/error.log" />
        )}
        {kind === "journal" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 120 }} value={unit} onChange={e => setUnit(e.target.value)} placeholder="服务名，如 nginx" />
        )}
        {kind === "docker" && (
          <>
            <input className="mem-input" style={{ flex: 1, minWidth: 100 }} value={container} onChange={e => setContainer(e.target.value)} placeholder="容器名" />
            {dev?.kind === "docker" && (
              <>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => void loadContainers()}>{listBusy ? "…" : "列出容器"}</span>
                {ctrList.length > 0 && (
                  <select className="mem-select" style={{ maxWidth: 200 }} value={container} onChange={e => setContainer(e.target.value)}>
                    <option value="">选择容器（{ctrList.length}）</option>
                    {ctrList.map(x => <option key={x.value} value={x.value}>{x.label}</option>)}
                  </select>
                )}
              </>
            )}
          </>
        )}
        {kind === "k8s" && (
          <>
            <input className="mem-input" style={{ width: 120 }} value={k8sNs} onChange={e => setK8sNs(e.target.value)} placeholder="命名空间（空=默认）" />
            <input className="mem-input" style={{ flex: 1, minWidth: 100 }} value={k8sPod} onChange={e => setK8sPod(e.target.value)} placeholder="Pod 名" />
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void loadPods()}>{listBusy ? "…" : "列出 Pods"}</span>
            {podList.length > 0 && (
              <select className="mem-select" style={{ maxWidth: 220 }} value="" onChange={e => {
                const v = e.target.value;
                if (!v) return;
                const i = v.indexOf("/");
                if (i > 0) { setK8sNs(v.slice(0, i)); setK8sPod(v.slice(i + 1)); } else { setK8sNs(""); setK8sPod(v); }
              }}>
                <option value="">选择 Pod（{podList.length}）</option>
                {podList.map(x => <option key={x.value} value={x.value}>{x.label}</option>)}
              </select>
            )}
          </>
        )}
        {kind === "winevt" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 120 }} value={evtChannel} onChange={e => setEvtChannel(e.target.value)} placeholder="通道：Security / System / Application" />
        )}
        {kind === "db" && (
          <select className="mem-select" value={dbSource} onChange={e => setDbSource(e.target.value)}>
            {dbSources.map(s => <option key={s.name} value={s.name}>{s.name}（{s.type}）</option>)}
          </select>
        )}
        <datalist id="ndv-log-paths">
          {["/var/log/syslog", "/var/log/auth.log", "/var/log/messages", "/var/log/secure", "/var/log/dmesg",
            "/var/log/nginx/access.log", "/var/log/nginx/error.log",
            "/var/log/apache2/access.log", "/var/log/httpd/access_log",
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

      {/* Read params (collapsed) + actions. */}
      {kind !== "db" && (
        <div style={{ marginBottom: 8 }}>
          <span className={`btn btn--small ${filtersOpen || filterActive ? "btn--primary" : "btn--secondary"}`} role="button"
            title="行数 / 起始时间 / 正则过滤"
            onClick={() => setFiltersOpen(o => !o)}>
            筛选{filterActive ? ` · ${tailN}行${since.trim() ? ` · ${since.trim()}` : ""}${grep.trim() ? ` · /${grep.trim()}/` : ""}` : ""}
          </span>
          {filtersOpen && (
            <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 6, alignItems: "center" }}>
              <label className="ndv__meta">行数</label>
              <input className="mem-input" type="number" style={{ width: 64 }} value={tailN} min={1} max={1000} onChange={e => setTailN(Math.min(1000, Math.max(1, Number(e.target.value) || 100)))} />
              <label className="ndv__meta">起于</label>
              <input className="mem-input" style={{ width: 130 }} value={since} onChange={e => setSince(e.target.value)} placeholder="2026-08-27 10:00 或 -1h" />
              <label className="ndv__meta">过滤</label>
              <input className="mem-input" style={{ width: 110 }} value={grep} onChange={e => setGrep(e.target.value)} placeholder="正则" />
            </div>
          )}
        </div>
      )}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8, alignItems: "center" }}>
        {kind !== "db" && (
          <>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void read()}>{busy ? "读取中…" : "读取"}</span>
            {kind !== "syslog" && dev?.kind !== "k8s" && dev?.kind !== "docker" && (
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

      {/* Level summary chips: counts double as one-click view filters. */}
      {jsonCols.length === 0 && lines.length > 0 && (
        <div style={{ display: "flex", gap: 6, marginBottom: 6, alignItems: "center" }}>
          <span className={`ndv__chip ${levelFilter === "error" ? "" : "ndv__chip--off"}`} role="button"
            style={levelFilter === "error" ? { borderColor: "var(--danger)", color: "var(--danger)" } : undefined}
            onClick={() => setLevelFilter(f => (f === "error" ? "" : "error"))}>
            错误 {errCount}
          </span>
          <span className={`ndv__chip ${levelFilter === "warn" ? "" : "ndv__chip--off"}`} role="button"
            style={levelFilter === "warn" ? { borderColor: "var(--warn)", color: "var(--warn)" } : undefined}
            onClick={() => setLevelFilter(f => (f === "warn" ? "" : "warn"))}>
            警告 {warnCount}
          </span>
          {levelFilter && <span className="ndv__meta">显示 {shownLines.length}/{lines.length} 行（仅显示层过滤，「交给 AI」仍取完整缓冲）</span>}
        </div>
      )}

      {/* Viewer. */}
      <div ref={boxRef} className="ndv-log__box" style={{ flex: 1, minHeight: 120, overflow: "auto", background: "var(--bg)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", padding: 8, fontSize: 11.5, fontFamily: "var(--font-mono, monospace)", lineHeight: 1.5 }}>
        {lines.length === 0 && !following && <span className="ndv__hint">选择源后「读取」最近日志，或「跟踪」实时流。文件路径限 /var/log 与设备 log_paths 白名单。</span>}
        {jsonCols.length > 0 ? (
          <table style={{ borderCollapse: "collapse", width: "100%" }}>
            <thead><tr>{jsonCols.map(c => <th key={c} style={{ textAlign: "left", borderBottom: "1px solid var(--border)", padding: "2px 6px", position: "sticky", top: 0, background: "var(--bg)", whiteSpace: "nowrap" }}>{c}</th>)}</tr></thead>
            <tbody>{jsonRows.map((r, i) => <tr key={i}>{jsonCols.map(c => <td key={c} style={{ padding: "2px 6px", borderBottom: "1px solid var(--border)", whiteSpace: "nowrap", maxWidth: 320, overflow: "hidden", textOverflow: "ellipsis" }} title={String(r[c] ?? "")}>{String(r[c] ?? "")}</td>)}</tr>)}</tbody>
          </table>
        ) : shownLines.map((l, i) => <div key={i} className={`ndv-log__line ${levelClass(l)}`}>{l}</div>)}
        {following && <div className="ndv-log__line ndv-log__cursor">▍</div>}
      </div>
    </div>
  );
}
