import { useEffect, useMemo, useRef, useState } from "react";
import { app, onNetdevLogFollow } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { exportTextFile } from "../../lib/netdevExport";
import type { NetDevDBSourceView, NetDevDeviceView, NetDevLogFollowEvent, NetDevLogSourceProbe, NetDevSyslogCountRow } from "../../lib/types";

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

// RHEL 家系（centos/rocky）系统日志是 /var/log/messages + /var/log/secure；
// Debian 家系才是 /var/log/syslog + /var/log/auth.log。文件源默认值跟着
// 所选设备的 OS 走，centos 用户不再先撞一次 "cannot open /var/log/syslog"。
const isRhelFamily = (os?: string) => os === "centos" || os === "rocky";
const defaultFileTarget = (os?: string) => (isRhelFamily(os) ? "/var/log/messages" : "/var/log/syslog");
const DEFAULT_FILE_TARGETS = ["/var/log/syslog", "/var/log/messages"];

export function LogPanel({ devices, dbSources, onInsertComposer, onOpenWorkbench, onOpenSettings, onOpenDiscovery }: {
  devices: NetDevDeviceView[];
  dbSources: NetDevDBSourceView[];
  onInsertComposer?: (text: string) => void;
  onOpenWorkbench?: () => void;
  onOpenSettings?: (tab: string) => void;
  onOpenDiscovery?: () => void;
}) {
  const t = useT();
  // 事件量（R3 journal，页签充实）：挂载拉一次——日志域自己的统计层。
  const [sysCounts, setSysCounts] = useState<NetDevSyslogCountRow[] | null>(null);
  useEffect(() => {
    let alive = true;
    app.NetDevSyslogCounts(500).then(rows => { if (alive) setSysCounts(rows ?? []); }).catch(() => {});
    return () => { alive = false; };
  }, []);
  const hosts = useMemo(() => devices.filter(d => d.vendor === "linux" || d.vendor === "windows" || d.vendor === "vmware" || d.kind === "docker" || d.kind === "k8s"), [devices]);
  const [device, setDevice] = useState(hosts[0]?.name ?? "");
  const dev = hosts.find(h => h.name === device);

  // Source selection: kind + free-form target (unit / path / container / ns+pod).
  // system（整本 journal）是 Linux 设备的默认源：不要求用户知道自己这台
  // 机器的发行版文件名（syslog vs messages），一个「系统日志」全通用。
  const isLinuxHost = (d?: NetDevDeviceView) => d?.vendor === "linux" && !d?.kind;
  const [kind, setKind] = useState<"file" | "journal" | "docker" | "k8s" | "db" | "syslog" | "winevt" | "system">(
    isLinuxHost(hosts[0]) ? "system" : "file"
  );
  const [target, setTarget] = useState(defaultFileTarget(hosts[0]?.os));
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
  // 导出选项（当前显示 / 时间段）：时间段导出按所选范围重新走一次密封
  // 读取（tail 拉满、套用当前过滤词）直接落文件，不要求先加载进查看器。
  const [exportOpen, setExportOpen] = useState(false);
  const [exportBusy, setExportBusy] = useState(false);
  // 日志源探测：sealed 只读命令枚举这台机器真实存在的源（运行中的服务/
  // 容器//var/log 文件），点选即切换——不再靠用户猜文件名。
  const [probe, setProbe] = useState<NetDevLogSourceProbe | null>(null);
  const [probeBusy, setProbeBusy] = useState(false);
  const runProbe = async () => {
    if (!device) { setNote(t("ndv.logp.pickSource")); return; }
    setProbeBusy(true);
    try {
      if (following) { await app.NetDevLogFollowStop(device); setFollowing(false); }
      setProbe(await app.NetDevProbeLogSources(device));
      setNote("");
    } catch (e) { setNote(String(e)); } finally { setProbeBusy(false); }
  };
  // 白名单外目录的一键登记：把目录追加进该设备的 log_paths（同一人工批准
  // 的用户配置管线），之后 file: 读取即放行。
  const registerLogPath = async (filePath: string) => {
    const dir = filePath.slice(0, filePath.lastIndexOf("/"));
    if (!device || !dir) return;
    try {
      const v = await app.NetDevSettings();
      const devices = (v.devices ?? []).map(d => d.name === device
        ? { ...d, logPaths: [...new Set([...(d.logPaths ?? []), dir])] }
        : d);
      await app.SetNetDevSettings({ ...v, devices });
      setNote(t("ndv.logp.pathRegistered", { dir }));
    } catch (e) { setNote(String(e)); }
  };
  // 筛选收纳：行数/起于/过滤 默认收起（320px dock 里常驻太挤），点「筛选」展开；
  // 有非默认参数时按钮带摘要。levelFilter 是查看器侧的等级过滤（仅显示层）。
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [levelFilter, setLevelFilter] = useState<"" | "error" | "warn">("");
  const boxRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!device && hosts[0]) setDevice(hosts[0].name);
    if (!dbSource && dbSources[0]) setDbSource(dbSources[0].name);
  }, [hosts, dbSources, device, dbSource]);

  const source = kind === "db" ? `db:${dbSource}` : kind === "journal" ? `journal:${unit}` : kind === "docker" ? `docker:${container}` : kind === "k8s" ? `k8s:${k8sNs.trim() ? k8sNs.trim() + "/" : ""}${k8sPod.trim()}` : kind === "winevt" ? `winevt:${evtChannel.trim()}` : kind === "system" ? "system:main" : `file:${target}`;

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
        setNote(t("ndv.logp.followStopped", { reason: ev.reason ?? "" }));
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
    if (!device || (kind === "docker" && !container.trim()) || (kind === "k8s" && !k8sPod.trim()) || (kind === "winevt" && !evtChannel.trim())) { setNote(t("ndv.logp.pickSource")); return; }    setBusy(true); setNote("");
    try {
      if (following) { await app.NetDevLogFollowStop(device); setFollowing(false); }
      if (kind === "syslog") {
        const rows = await app.NetDevSyslogTail(device, tailN, grep);
        setLines(rows);
        if ((rows ?? []).length === 0) setNote(t("ndv.logp.syslogEmpty"));
        return;
      }
      const res = await app.NetDevLogRead(device, source, tailN, since, grep);
      if (res.refused) {
        setLines([]);
        setNote(res.refusal ?? t("ndv.logp.refused"));
      } else {
        setLines((res.output ?? "").split("\n").filter(Boolean));
        if (res.is_error) setNote(t("ndv.logp.deviceError"));
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
      setNote(t("ndv.logp.following"));
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
    onInsertComposer(`${t("ndv.logp.aiPrefix", { dev: device || dbSource, src: source })}\n\n\`\`\`\n${excerpt}\n\`\`\``);
  };

  // 时间段导出只对有时间语义的源开放（file/journal/system；syslog 聚合与
  // DB 快查询没有 since 概念）。since 语法与读取参数一致（-1h / 2026-09-03）。
  const rangeExportable = kind === "file" || kind === "journal" || kind === "system";
  const todayStart = () => {
    const d = new Date();
    const p = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  };
  const exportByRange = async (tag: string, since: string) => {
    if (!device) return;
    setExportBusy(true);
    try {
      const res = await app.NetDevLogRead(device, source, 1000, since, grep);
      const body = res.refused ? `# ${res.refusal}\n` : (res.output ?? "");
      const path = await exportTextFile(`netdev-logs-${device}-${kind}-${tag}.txt`, body + "\n");
      if (path) { setNote(t("ndv.exportedTo", { path })); setExportOpen(false); }
    } catch (e) { setNote(String(e)); } finally { setExportBusy(false); }
  };

  // 转为发现：把当前查看（含等级过滤）的日志行提交为一条 Finding——原始
  // 日志行不会自动进总览，运维认为值得跟踪的内容从这里进入发现管线
  // （发现中心/总览卡片、告警生命周期、通知出口、每日晨报）。
  const [filing, setFiling] = useState(false);
  const fileFinding = async () => {
    if (!device || shownLines.length === 0) return;
    setFiling(true);
    try {
      await app.NetDevFileFinding(device, source, levelFilter === "error" ? "critical" : errCount > 0 ? "warning" : "info", shownLines);
      setNote(t("ndv.logp.findingFiled", { n: shownLines.length }));
    } catch (e) { setNote(String(e)); } finally { setFiling(false); }
  };

  return (
    <div className="ndv__card" style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}>
      <div className="ndv__card-title">{t("ndv.logp.title")}{following ? <span style={{ color: "var(--ok)" }}> · {t("ndv.logp.followingTag")}</span> : ""}
        {onOpenWorkbench && <span className="ndv__chip" role="button" style={{ marginLeft: 8, fontWeight: 400 }} title={t("ndv.logp.benchTip")} onClick={onOpenWorkbench}>⟶ {t("ndv.logp.bench")}</span>}
        {onOpenDiscovery && <span className="ndv__chip" role="button" style={{ marginLeft: 8, fontWeight: 400 }} title={t("ndv.logp.discoverTip")} onClick={onOpenDiscovery}>🧭 {t("ndv.logp.discover")}</span>}
      </div>

      {/* 事件量条（R3）：近 24h 按类目 + 设备排行——点类目即 grep 联动。 */}
      {(sysCounts?.length ?? 0) > 0 && (
        <div className="ndv-syscount">
          {(() => {
            const cutoff = Date.now() - 24 * 3600 * 1000;
            const byClass = new Map<string, number>();
            const byDev = new Map<string, number>();
            for (const r of sysCounts ?? []) {
              const ts = Date.parse(r.hour.length === 13 ? r.hour + ":00:00" : r.hour.replace("T", " ")) || 0;
              if (ts < cutoff) continue;
              byClass.set(r.class, (byClass.get(r.class) ?? 0) + r.n);
              byDev.set(r.device, (byDev.get(r.device) ?? 0) + r.n);
            }
            const total = Array.from(byClass.values()).reduce((a, b) => a + b, 0);
            const cls = Array.from(byClass.entries()).sort((a, b) => b[1] - a[1]).slice(0, 5);
            const devs = Array.from(byDev.entries()).sort((a, b) => b[1] - a[1]).slice(0, 3);
            if (total === 0) return null;
            const max = cls[0]?.[1] || 1;
            return (<>
              <span className="ndv-syscount__label">{t("ndv.sysc.title", { n: total })}</span>
              {cls.map(([k, v]) => (
                <span key={k} className="ndv-syscount__bar" role="button" title={`${k}: ${v}`}
                  onClick={() => setGrep(k)}>
                  <span className="ndv-syscount__k">{k}</span>
                  <span className="ndv-syscount__track"><span style={{ width: `${Math.max(6, (v / max) * 100)}%` }} /></span>
                  <span className="ndv-syscount__n">{v}</span>
                </span>
              ))}
              <span className="ndv-syscount__devs dim">{devs.map(([d, v]) => `${d} ${v}`).join(" · ")}</span>
            </>);
          })()}
        </div>
      )}

      {/* Source bar: device + kind + target + params, one row-ish grid. */}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8 }}>
        <select className="mem-select" value={device} onChange={e => {
          void stopFollow();
          setDevice(e.target.value);
          const nd = hosts.find(h => h.name === e.target.value);
          if (nd?.kind === "k8s") setKind("k8s");
          else if (nd?.kind === "docker") setKind("docker");
          else setKind(isLinuxHost(nd) ? "system" : "file");
          // 换设备时若目标仍是默认路径，跟着换到新设备 OS 家系的默认值
          // （用户手输过的路径不动）。
          const dft = defaultFileTarget(nd?.os);
          setTarget(tg => (DEFAULT_FILE_TARGETS.includes(tg) ? dft : tg));
        }} disabled={kind === "db"}>
          {hosts.map(h => <option key={h.name} value={h.name}>{h.name}{h.kind ? `（${h.kind}）` : ""}</option>)}
          {hosts.length === 0 && <option value="">{t("ndv.logp.noHosts")}</option>}
        </select>
        <select className="mem-select" value={kind} onChange={e => { void stopFollow(); setKind(e.target.value as typeof kind); }}>
          {dev?.kind !== "k8s" && dev?.kind !== "docker" && <>
            {isLinuxHost(dev) && <option value="system">{t("ndv.logp.kSystem")}</option>}
            <option value="file">{t("ndv.logp.kFile")}</option>
            <option value="journal">{t("ndv.logp.kJournal")}</option>
            <option value="docker">{t("ndv.logp.kDockerSsh")}</option>
            {dbSources.length > 0 && <option value="db">{t("ndv.logp.kDb")}</option>}
            <option value="syslog">{t("ndv.logp.kSyslog")}</option>
          </>}
          {dev?.kind === "k8s" && <option value="k8s">{t("ndv.logp.kK8s")}</option>}
          {dev?.kind === "docker" && <option value="docker">{t("ndv.logp.kDockerApi")}</option>}
          {dev?.vendor === "windows" && !dev?.kind && <option value="winevt">{t("ndv.logp.kWinevt")}</option>}
        </select>
        {kind === "file" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 180 }} list="ndv-log-paths" value={target} onChange={e => setTarget(e.target.value)} placeholder="/var/log/nginx/error.log" />
        )}
        {kind === "journal" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 120 }} value={unit} onChange={e => setUnit(e.target.value)} placeholder={t("ndv.logp.phUnit")} />
        )}
        {kind === "docker" && (
          <>
            <input className="mem-input" style={{ flex: 1, minWidth: 100 }} value={container} onChange={e => setContainer(e.target.value)} placeholder={t("ndv.logp.phContainer")} />
            {dev?.kind === "docker" && (
              <>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => void loadContainers()}>{listBusy ? "…" : t("ndv.logp.listContainers")}</span>
                {ctrList.length > 0 && (
                  <select className="mem-select" style={{ maxWidth: 200 }} value={container} onChange={e => setContainer(e.target.value)}>
                    <option value="">{t("ndv.logp.pickContainer", { n: ctrList.length })}</option>
                    {ctrList.map(x => <option key={x.value} value={x.value}>{x.label}</option>)}
                  </select>
                )}
              </>
            )}
          </>
        )}
        {kind === "k8s" && (
          <>
            <input className="mem-input" style={{ width: 120 }} value={k8sNs} onChange={e => setK8sNs(e.target.value)} placeholder={t("ndv.logp.phNs")} />
            <input className="mem-input" style={{ flex: 1, minWidth: 100 }} value={k8sPod} onChange={e => setK8sPod(e.target.value)} placeholder={t("ndv.logp.phPod")} />
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void loadPods()}>{listBusy ? "…" : t("ndv.logp.listPods")}</span>
            {podList.length > 0 && (
              <select className="mem-select" style={{ maxWidth: 220 }} value="" onChange={e => {
                const v = e.target.value;
                if (!v) return;
                const i = v.indexOf("/");
                if (i > 0) { setK8sNs(v.slice(0, i)); setK8sPod(v.slice(i + 1)); } else { setK8sNs(""); setK8sPod(v); }
              }}>
                <option value="">{t("ndv.logp.pickPod", { n: podList.length })}</option>
                {podList.map(x => <option key={x.value} value={x.value}>{x.label}</option>)}
              </select>
            )}
          </>
        )}
        {kind === "winevt" && (
          <input className="mem-input" style={{ flex: 1, minWidth: 120 }} value={evtChannel} onChange={e => setEvtChannel(e.target.value)} placeholder={t("ndv.logp.phChannel")} />
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

      {/* 探测结果：这台机器上真实存在的日志源——点选候选即切换为当前源。 */}
      {probe && probe.device === device && (
        <div className="ndv__hint" style={{ marginBottom: 8, display: "flex", flexDirection: "column", gap: 4 }}>
          {probe.services.length > 0 && (
            <div style={{ display: "flex", flexWrap: "wrap", gap: 4, alignItems: "center" }}>
              <span className="ndv__meta">{t("ndv.logp.probeServices")}</span>
              {probe.services.slice(0, 24).map(s => (
                <span key={s} className="ndv__chip" role="button" onClick={() => { void stopFollow(); setKind("journal"); setUnit(s); }}>journal:{s}</span>
              ))}
              {probe.services.length > 24 && <span className="ndv__meta">+{probe.services.length - 24}</span>}
            </div>
          )}
          {probe.containers.length > 0 && (
            <div style={{ display: "flex", flexWrap: "wrap", gap: 4, alignItems: "center" }}>
              <span className="ndv__meta">{t("ndv.logp.probeContainers")}</span>
              {probe.containers.slice(0, 24).map(c => (
                <span key={c} className="ndv__chip" role="button" onClick={() => { void stopFollow(); setKind("docker"); setContainer(c); }}>docker:{c}</span>
              ))}
            </div>
          )}
          {probe.files.length > 0 && (
            <div style={{ display: "flex", flexWrap: "wrap", gap: 4, alignItems: "center" }}>
              <span className="ndv__meta">{t("ndv.logp.probeFiles")}</span>
              {probe.files.slice(0, 30).map(f => (
                <span key={f.path} className="ndv__chip" role="button"
                  title={f.allowed ? f.path : t("ndv.logp.needRegister", { path: f.path })}
                  style={f.allowed ? undefined : { borderStyle: "dashed", opacity: 0.85 }}
                  onClick={() => {
                    void stopFollow();
                    setKind("file");
                    setTarget(f.path);
                    if (!f.allowed) void registerLogPath(f.path);
                  }}>{f.allowed ? "" : "登记· "}{f.name}{" "}
                  <span style={{ opacity: 0.6 }}>{f.size}</span></span>
              ))}
              {probe.files.length > 30 && <span className="ndv__meta">+{probe.files.length - 30}</span>}
            </div>
          )}
          {probe.services.length === 0 && probe.containers.length === 0 && probe.files.length === 0 && (
            <span>{t("ndv.logp.probeNone")}</span>
          )}
          {probe.errors.length > 0 && <span style={{ opacity: 0.6 }}>{probe.errors.join(" · ")}</span>}
        </div>
      )}

      {/* db quick queries: allowlisted statements only. */}
      {kind === "db" && dbSrc && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8 }}>
          {(DB_QUICK[dbSrc.type] ?? []).filter(allowlisted).map(q => (
            <span key={q} className="ndv__chip" role="button" onClick={() => void dbQuery(q)}>{q}</span>
          ))}
          {(DB_QUICK[dbSrc.type] ?? []).filter(allowlisted).length === 0 && (
            <span className="ndv__hint">
              {t("ndv.logp.noStatements")}
              <span className="btn btn--secondary btn--small" role="button" style={{ margin: "0 4px" }} onClick={() => onOpenSettings?.("netdev")}>{t("ndv.goSettings2")}</span>
              {t("ndv.logp.egProcesslist")}
            </span>
          )}
        </div>
      )}

      {/* 展开的读取参数（行数/起于/过滤）——动作按钮收进下面同一行，参数
          展开区单独占行，避免「筛选/读取/跟随」散在两行。 */}
      {kind !== "db" && filtersOpen && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8, alignItems: "center" }}>
          <label className="ndv__meta">{t("ndv.logp.rows")}</label>
          <input className="mem-input" type="number" style={{ width: 64 }} value={tailN} min={1} max={1000} onChange={e => setTailN(Math.min(1000, Math.max(1, Number(e.target.value) || 100)))} />
          <label className="ndv__meta">{t("ndv.logp.since")}</label>
          <input className="mem-input" style={{ width: 130 }} value={since} onChange={e => setSince(e.target.value)} placeholder="2026-08-27 10:00 或 -1h" />
          <label className="ndv__meta">{t("ndv.logp.grep")}</label>
          <input className="mem-input" style={{ width: 110 }} value={grep} onChange={e => setGrep(e.target.value)} placeholder={t("ndv.logp.phRegex")} />
        </div>
      )}
      <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8, alignItems: "center" }}>
        {isLinuxHost(dev) && (
          <span className={`btn btn--small ${probe ? "btn--primary" : "btn--secondary"}`} role="button"
            title={t("ndv.logp.probeTip")}
            onClick={() => void runProbe()}>{probeBusy ? t("ndv.logp.probing") : t("ndv.logp.probe")}</span>
        )}
        {kind !== "db" && (
          <>
            <span className={`btn btn--small ${filtersOpen || filterActive ? "btn--primary" : "btn--secondary"}`} role="button"
              title={t("ndv.logp.filterTip")}
              onClick={() => setFiltersOpen(o => !o)}>
              {t("ndv.logp.filter")}{filterActive ? ` · ${tailN}${t("ndv.logp.rowsUnit")}${since.trim() ? ` · ${since.trim()}` : ""}${grep.trim() ? ` · /${grep.trim()}/` : ""}` : ""}
            </span>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void read()}>{busy ? t("ndv.logp.reading") : t("ndv.logp.read")}</span>
            {kind !== "syslog" && dev?.kind !== "k8s" && dev?.kind !== "docker" && (
              <span className={`btn btn--small ${following ? "btn--primary" : "btn--secondary"}`} role="button" onClick={() => void toggleFollow()}>{following ? t("ndv.logp.stopFollow") : t("ndv.logp.follow")}</span>
            )}
          </>
        )}
        {onInsertComposer && lines.length > 0 && (
          <span className="btn btn--secondary btn--small" role="button" onClick={sendToAI}>{t("ndv.sendToAI")}</span>
        )}
        {lines.length > 0 && (
          <span className="btn btn--secondary btn--small" role="button" title={t("ndv.logp.fileFindingTip")}
            onClick={() => void fileFinding()}>{filing ? t("ndv.logp.filing") : t("ndv.logp.fileFinding")}</span>
        )}
        {(lines.length > 0 || rangeExportable) && (
          <span className="btn btn--secondary btn--small" role="button" onClick={() => setExportOpen(o => !o)}>
            {exportBusy ? t("ndv.logp.reading") : t("ndv.export")}{exportOpen ? " ▴" : " ▾"}
          </span>
        )}
        {lines.length > 0 && <span className="btn btn--secondary btn--small" role="button" onClick={() => { setLines([]); setNote(""); }}>{t("ndv.logp.clear")}</span>}
      </div>

      {/* 导出选项：当前显示，或按时间段重新读取后直接落文件（上限 1000 行）。 */}
      {exportOpen && (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginBottom: 8 }}>
          {lines.length > 0 && (
            <span className="ndv__chip" role="button" onClick={() => void (async () => {
              const path = await exportTextFile(`netdev-logs-${device}-${kind}.txt`, lines.join("\n") + "\n");
              if (path) { setNote(t("ndv.exportedTo", { path })); setExportOpen(false); }
            })()}>{t("ndv.logp.expNow", { n: lines.length })}</span>
          )}
          {rangeExportable && <>
            <span className="ndv__chip" role="button" onClick={() => void exportByRange("1h", "-1h")}>{t("ndv.logp.exp1h")}</span>
            <span className="ndv__chip" role="button" onClick={() => void exportByRange("6h", "-6h")}>{t("ndv.logp.exp6h")}</span>
            <span className="ndv__chip" role="button" onClick={() => void exportByRange("24h", "-24h")}>{t("ndv.logp.exp24h")}</span>
            <span className="ndv__chip" role="button" onClick={() => void exportByRange("today", todayStart())}>{t("ndv.logp.expToday")}</span>
          </>}
        </div>
      )}

      {note && <div className="ndv__hint" style={{ marginBottom: 6 }}>{note}</div>}

      {/* Level summary chips: counts double as one-click view filters. */}
      {jsonCols.length === 0 && lines.length > 0 && (
        <div style={{ display: "flex", gap: 6, marginBottom: 6, alignItems: "center" }}>
          <span className={`ndv__chip ${levelFilter === "error" ? "" : "ndv__chip--off"}`} role="button"
            style={levelFilter === "error" ? { borderColor: "var(--danger)", color: "var(--danger)" } : undefined}
            onClick={() => setLevelFilter(f => (f === "error" ? "" : "error"))}>
            {t("ndv.logp.errors", { n: errCount })}
          </span>
          <span className={`ndv__chip ${levelFilter === "warn" ? "" : "ndv__chip--off"}`} role="button"
            style={levelFilter === "warn" ? { borderColor: "var(--warn)", color: "var(--warn)" } : undefined}
            onClick={() => setLevelFilter(f => (f === "warn" ? "" : "warn"))}>
            {t("ndv.logp.warns", { n: warnCount })}
          </span>
          {levelFilter && <span className="ndv__meta">{t("ndv.logp.showing", { shown: shownLines.length, total: lines.length })}</span>}
        </div>
      )}

      {/* Viewer. */}
      <div ref={boxRef} className="ndv-log__box" style={{ flex: 1, minHeight: 120, overflow: "auto", background: "var(--bg)", border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", padding: 8, fontSize: 11.5, fontFamily: "var(--font-mono, monospace)", lineHeight: 1.5 }}>
        {lines.length === 0 && !following && <span className="ndv__hint">{t("ndv.logp.emptyHint")}</span>}
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
