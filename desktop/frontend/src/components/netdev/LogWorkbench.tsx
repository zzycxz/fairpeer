import { useMemo, useRef, useState } from "react";
import { app } from "../../lib/bridge";
import type { NetDevDeviceView, NetDevLogSearchResult } from "../../lib/types";

// LogWorkbench — 主区「日志工作台」v1（NETDEV_SPEC_V2 §3.2/§10.3）。
// dock 的 日志 tab（LogPanel）是单设备快速尾随；这里是任务视图：勾选多个
// (设备,源) 一次读取，客户端按时间戳合并成一条时间线——无时间戳行吸附
// 同源前一条（§3.2 契约）。底栏是跨设备 IOC 搜索（App.NetDevLogSearch 的
// 密封扇出）。所有读取都走后端密封路径（分类器/预算/脱敏/审计），前端只
// 做编排与呈现。父层保持本组件挂载以兑现 §10.2「关闭重开不丢」。

type SrcKind = "file" | "journal" | "docker" | "k8s";

interface SrcEntry {
  id: number;
  device: string;
  kind: SrcKind;
  target: string;
  active: boolean;
}

interface Row {
  ts: number;    // ms; 0 = none seen yet in this source
  tsText: string;
  device: string;
  source: string;
  line: string;
}

const MAX_ROWS = 2000;

// parseTs mirrors the Go side's extractLogTimestamp for the two families that
// matter in the merge: syslog `Aug 27 10:00:00` and ISO-ish
// `2026-08-27[T| ]10:00[:00]`. Anything else returns 0 (carry-forward then).
function parseTs(line: string): { ms: number; text: string } {
  const s = line.trimStart();
  const syslog = /^([A-Z][a-z]{2}) +(\d{1,2}) (\d{2}):(\d{2}):(\d{2})/.exec(s);
  if (syslog) {
    const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    const mi = months.indexOf(syslog[1]);
    if (mi >= 0) {
      const now = new Date();
      const t = new Date(now.getFullYear(), mi, Number(syslog[2]), Number(syslog[3]), Number(syslog[4]), Number(syslog[5]));
      if (!isNaN(t.getTime())) return { ms: t.getTime(), text: `${syslog[1]} ${syslog[2].padStart(2, " ")} ${syslog[3]}:${syslog[4]}:${syslog[5]}` };
    }
  }
  const iso = /^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2})(?::(\d{2}))?/.exec(s);
  if (iso) {
    const t = new Date(`${iso[1]}T${iso[2]}:${iso[3] ?? "00"}:00`);
    if (!isNaN(t.getTime())) return { ms: t.getTime(), text: `${iso[1]} ${iso[2]}${iso[3] ? ":" + iso[3] : ""}` };
  }
  return { ms: 0, text: "" };
}

// mergeRows carries each source's last timestamp forward (无时间戳行吸附前一条)
// and stable-sorts the union ascending.
function mergeRows(parts: { device: string; source: string; lines: string[] }[]): Row[] {
  const rows: Row[] = [];
  for (const p of parts) {
    let ts = 0, tsText = "";
    for (const raw of p.lines) {
      const line = raw.replace(/\r$/, "");
      if (!line.trim()) continue;
      const parsed = parseTs(line);
      if (parsed.ms > 0) { ts = parsed.ms; tsText = parsed.text; }
      rows.push({ ts, tsText, device: p.device, source: p.source, line });
    }
  }
  rows.sort((a, b) => a.ts - b.ts); // stable: same-ts keeps source/line order
  return rows.slice(-MAX_ROWS);
}

const levelClass = (line: string): string => {
  const l = line.toLowerCase();
  if (/(error|err|fatal|crit|emerg|failed|failure|refused|denied)/.test(l)) return "ndv-log__line--error";
  if (/(warn|warning|retry|slow|timeout)/.test(l)) return "ndv-log__line--warn";
  return "";
};

const sourceOf = (e: SrcEntry) => e.kind === "k8s" ? `k8s:${e.target.trim()}` : `${e.kind}:${e.target.trim()}`;

export function LogWorkbench({ devices, onInsertComposer, hidden }: {
  devices: NetDevDeviceView[];
  onInsertComposer?: (text: string) => void;
  hidden?: boolean;
}) {
  const hosts = useMemo(
    () => devices.filter(d => d.vendor === "linux" || d.vendor === "windows" || d.vendor === "vmware" || d.kind === "docker" || d.kind === "k8s"),
    [devices],
  );

  const [entries, setEntries] = useState<SrcEntry[]>([]);
  const nextId = useRef(1);
  const [addDevice, setAddDevice] = useState("");
  const [addKind, setAddKind] = useState<SrcKind>("file");
  const [addTarget, setAddTarget] = useState("/var/log/syslog");

  const [tailN, setTailN] = useState(200);
  const [since, setSince] = useState("");
  const [viewGrep, setViewGrep] = useState("");
  const [rows, setRows] = useState<Row[]>([]);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);

  // 底栏：跨设备 IOC 搜索
  const [pattern, setPattern] = useState("");
  const [searchSince, setSearchSince] = useState("");
  const [search, setSearch] = useState<NetDevLogSearchResult | null>(null);
  const [searchBusy, setSearchBusy] = useState(false);

  const effectiveAddDevice = addDevice || hosts[0]?.name || "";

  const addEntry = (device: string, kind: SrcKind, target: string) => {
    const t = target.trim();
    if (!device || !t) { setNote("先选设备并填好源目标"); return; }
    setEntries(prev => [...prev, { id: nextId.current++, device, kind, target: t, active: true }]);
    setNote("");
  };

  const fetchAll = async () => {
    const act = entries.filter(e => e.active && e.target.trim());
    if (act.length === 0) { setNote("先在左侧勾选至少一个源"); return; }
    setBusy(true); setNote("");
    try {
      const results = await Promise.all(act.map(async e => {
        const r = await app.NetDevLogRead(e.device, sourceOf(e), tailN, since, "");
        return { e, r };
      }));
      const parts: { device: string; source: string; lines: string[] }[] = [];
      const notes: string[] = [];
      for (const { e, r } of results) {
        if (r.refused) notes.push(`${e.device} ${sourceOf(e)}：${r.refusal ?? "已拒绝"}`);
        else if (r.is_error) notes.push(`${e.device} ${sourceOf(e)}：设备返回错误（可能文件不存在）`);
        else parts.push({ device: e.device, source: sourceOf(e), lines: (r.output ?? "").split("\n") });
      }
      setRows(mergeRows(parts));
      setNote(notes.length ? notes.join("；") : (parts.length ? `已合并 ${parts.length} 个源、${rowsCount(parts)} 行` : "没有可用输出"));
    } catch (e) {
      setNote(String(e));
    } finally {
      setBusy(false);
    }
  };

  const rowsCount = (parts: { lines: string[] }[]) => parts.reduce((n, p) => n + p.lines.filter(l => l.trim()).length, 0);

  const runSearch = async () => {
    if (!pattern.trim()) return;
    setSearchBusy(true);
    try {
      setSearch(await app.NetDevLogSearch(pattern.trim(), [], [], searchSince.trim()));
    } catch (e) {
      setSearch(null);
      setNote(String(e));
    } finally {
      setSearchBusy(false);
    }
  };

  const viewRe = useMemo(() => {
    if (!viewGrep.trim()) return null;
    try { return new RegExp(viewGrep.trim()); } catch { return null; }
  }, [viewGrep]);
  const shown = useMemo(() => {
    if (!viewGrep.trim()) return rows;
    const lit = viewGrep.trim();
    return rows.filter(r => (viewRe ? viewRe.test(r.line) : r.line.includes(lit)));
  }, [rows, viewGrep, viewRe]);

  const sendToAI = () => {
    if (!onInsertComposer || shown.length === 0) return;
    const excerpt = shown.slice(-80).map(r => `[${r.tsText || "-"}] ${r.device} ${r.source} ${r.line}`).join("\n");
    onInsertComposer(`请基于以下多源合并日志诊断问题，先给结论再给证据：\n\n\`\`\`\n${excerpt}\n\`\`\``);
  };

  return (
    <div className="ndv-logwb" style={hidden ? { display: "none" } : undefined}>
      {/* 左栏：源选择（§10.3 契约：设备 → 源，多选） */}
      <div className="ndv-logwb__srcs">
        <div className="ndv__card-title" style={{ fontSize: 11.5 }}>源（{entries.filter(e => e.active).length}/{entries.length} 活跃）</div>
        {entries.map(e => (
          <div key={e.id} className="ndv-logwb__src">
            <input type="checkbox" checked={e.active} onChange={ev => setEntries(prev => prev.map(x => x.id === e.id ? { ...x, active: ev.target.checked } : x))} />
            <span title={`${e.device} ${sourceOf(e)}`} style={{ flex: 1, minWidth: 0 }}>
              <b style={{ fontWeight: 600 }}>{e.device}</b>
              <small> {sourceOf(e)}</small>
            </span>
            <span role="button" title="移除" style={{ cursor: "pointer", opacity: 0.6 }} onClick={() => setEntries(prev => prev.filter(x => x.id !== e.id))}>×</span>
          </div>
        ))}
        <div style={{ display: "flex", flexDirection: "column", gap: 4, marginTop: 4 }}>
          <select className="mem-select" value={effectiveAddDevice} onChange={e => setAddDevice(e.target.value)}>
            {hosts.map(h => <option key={h.name} value={h.name}>{h.name}</option>)}
            {hosts.length === 0 && <option value="">（无服务器设备）</option>}
          </select>
          <div style={{ display: "flex", gap: 4 }}>
            <select className="mem-select" style={{ width: 74 }} value={addKind} onChange={e => setAddKind(e.target.value as SrcKind)}>
              <option value="file">文件</option>
              <option value="journal">单元</option>
              <option value="docker">容器</option>
              <option value="k8s">K8s</option>
            </select>
            <input className="mem-input" style={{ flex: 1, minWidth: 0 }} value={addTarget} onChange={e => setAddTarget(e.target.value)}
              placeholder={addKind === "file" ? "/var/log/nginx/error.log" : addKind === "journal" ? "nginx" : addKind === "k8s" ? "namespace/pod（或仅 pod）" : "容器名"} />
          </div>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => addEntry(effectiveAddDevice, addKind, addTarget)}>＋ 添加源</span>
        </div>
        {(devices.length > 0 && hosts.length === 0) && <div className="ndv__hint">日志源面向服务器设备（linux/windows/vmware）；当前清单里还没有。</div>}
      </div>

      {/* 主区：合并时间线 + 底栏搜索 */}
      <div className="ndv-logwb__main">
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
          <label className="ndv__meta">行数</label>
          <input className="mem-input" type="number" style={{ width: 60 }} value={tailN} min={1} max={1000}
            onChange={e => setTailN(Math.min(1000, Math.max(1, Number(e.target.value) || 200)))} />
          <label className="ndv__meta">起于</label>
          <input className="mem-input" style={{ width: 128 }} value={since} onChange={e => setSince(e.target.value)} placeholder="2026-08-27 10:00 或 -1h" />
          <span className={`btn btn--small ${busy ? "" : "btn--primary"}`} role="button" onClick={() => void fetchAll()}>{busy ? "读取中…" : "读取并合并"}</span>
          <label className="ndv__meta" style={{ marginLeft: 8 }}>过滤</label>
          <input className="mem-input" style={{ width: 120 }} value={viewGrep} onChange={e => setViewGrep(e.target.value)} placeholder="正则（仅视图）" />
          <span className="ndv__meta">{shown.length} 行</span>
          {onInsertComposer && shown.length > 0 && (
            <span className="btn btn--secondary btn--small" role="button" onClick={sendToAI}>交给 AI 诊断</span>
          )}
        </div>
        {note && <div className="ndv__hint">{note}</div>}

        <div className="ndv-logwb__rows">
          {entries.length === 0 && rows.length === 0 && (
            <div className="ndv__hint" style={{ padding: 8 }}>
              多源合并时间线：添加多个 (设备,源) 后「读取并合并」，按时间戳排成一条线。
              journal 与 docker 日志无需配置，填服务名/容器名即可；自定义文件路径需先加入设备的 log_paths 白名单（/var/log 始终放行）。
              {hosts[0] && (
                <span style={{ display: "inline-flex", gap: 6, marginLeft: 8 }}>
                  <span className="ndv__chip" role="button" onClick={() => addEntry(hosts[0].name, "file", "/var/log/syslog")}>＋ {hosts[0].name} /var/log/syslog</span>
                  <span className="ndv__chip" role="button" onClick={() => addEntry(hosts[0].name, "file", "/var/log/auth.log")}>＋ auth.log</span>
                </span>
              )}
            </div>
          )}
          {shown.map((r, i) => (
            <div key={i} className={`ndv-logwb__row ${levelClass(r.line)}`}>
              {r.tsText && <span className="ts">{r.tsText} </span>}
              <span className="src">{r.device}·{r.source.replace(/^(file|journal|docker):/, "")} </span>
              {r.line}
            </div>
          ))}
        </div>

        {/* 底栏：跨设备搜索（§3.3——一个 IP 搜全网的家） */}
        <div className="ndv-logwb__search">
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
            <span className="ndv__meta" style={{ fontWeight: 600 }}>跨设备搜索</span>
            <input className="mem-input" style={{ width: 200 }} value={pattern} onChange={e => setPattern(e.target.value)}
              placeholder="IP / 哈希 / 关键字（正则）" onKeyDown={e => { if (e.key === "Enter") void runSearch(); }} />
            <input className="mem-input" style={{ width: 110 }} value={searchSince} onChange={e => setSearchSince(e.target.value)} placeholder="-1h" />
            <span className={`btn btn--small ${searchBusy ? "" : "btn--secondary"}`} role="button" onClick={() => void runSearch()}>{searchBusy ? "搜索中…" : "全网搜索"}</span>
            {search && (
              <span className="ndv__meta">
                覆盖 {search.covered_devices}/{search.total_devices} 台 · 命中 {search.hits.length} 条（{search.devices_with_hits} 台）
                {search.budget_stopped && <b style={{ color: "var(--danger)" }}> · 中途停止，未覆盖≠干净</b>}
              </span>
            )}
          </div>
          {search?.note && <div className="ndv__hint">{search.note}</div>}
          {search && search.hits.length > 0 && (
            <div className="ndv-logwb__hits">
              {search.hits.map((h, i) => (
                <details key={i}>
                  <summary className={levelClass(h.line)}>
                    <b>{h.device}</b> {h.source} — {h.line.slice(0, 160)}
                  </summary>
                  {(h.context ?? []).map((c, j) => <div key={j} className="ndv__meta" style={{ paddingLeft: 16 }}>{c}</div>)}
                </details>
              ))}
            </div>
          )}
          {search && search.skipped.length > 0 && (
            <div className="ndv__hint">跳过：{search.skipped.slice(0, 5).join("；")}{search.skipped.length > 5 ? ` …（共 ${search.skipped.length}）` : ""}</div>
          )}
        </div>
      </div>
    </div>
  );
}
