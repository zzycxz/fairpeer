import { useMemo, useRef, useState } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { exportTextFile } from "../../lib/netdevExport";
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

// 关联源（§5.4 实体360°）：变更/发现/事件合并进同一条时间线。
const KIND_LABEL: Record<string, string> = { change: "ndv.logwb.kChange", finding: "ndv.logwb.kFinding", event: "ndv.logwb.kEvent" };

const sourceOf = (e: SrcEntry) => e.kind === "k8s" ? `k8s:${e.target.trim()}` : `${e.kind}:${e.target.trim()}`;

export function LogWorkbench({ devices, onInsertComposer, hidden }: {
  devices: NetDevDeviceView[];
  onInsertComposer?: (text: string) => void;
  hidden?: boolean;
}) {
  const t = useT();
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
  const [rel, setRel] = useState<{ change: boolean; finding: boolean; event: boolean }>({ change: true, finding: true, event: false });
  const [relHours, setRelHours] = useState(24);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);

  // 底栏：跨设备 IOC 搜索（C3.6：设备作用域可选——空 = 全网）
  const [pattern, setPattern] = useState("");
  const [searchSince, setSearchSince] = useState("");
  const [search, setSearch] = useState<NetDevLogSearchResult | null>(null);
  const [searchBusy, setSearchBusy] = useState(false);
  const [scopeOpen, setScopeOpen] = useState(false);
  const [scope, setScope] = useState<string[]>([]);

  const effectiveAddDevice = addDevice || hosts[0]?.name || "";

  const addEntry = (device: string, kind: SrcKind, target: string) => {
    const trimmed = target.trim();
    if (!device || !trimmed) { setNote(t("ndv.logwb.pickSource")); return; }
    setEntries(prev => [...prev, { id: nextId.current++, device, kind, target: trimmed, active: true }]);
    setNote("");
  };

  const fetchAll = async () => {
    const act = entries.filter(e => e.active && e.target.trim());
    if (act.length === 0) { setNote(t("ndv.logwb.pickOne")); return; }
    setBusy(true); setNote("");
    try {
      const results = await Promise.all(act.map(async e => {
        const r = await app.NetDevLogRead(e.device, sourceOf(e), tailN, since, "");
        return { e, r };
      }));
      const parts: { device: string; source: string; lines: string[] }[] = [];
      const notes: string[] = [];
      for (const { e, r } of results) {
        if (r.refused) notes.push(t("ndv.logwb.rowNote", { dev: e.device, src: sourceOf(e), msg: r.refusal ?? t("ndv.logp.refused") }));
        else if (r.is_error) notes.push(t("ndv.logwb.rowErr", { dev: e.device, src: sourceOf(e) }));
        else parts.push({ device: e.device, source: sourceOf(e), lines: (r.output ?? "").split("\n") });
      }
      // 关联源：拉时间线（失败不阻塞日志），按开关与设备集过滤后并入。
      let tlRows: Row[] = [];
      if (rel.change || rel.finding || rel.event) {
        try {
          const tl = await app.NetDevTimeline("", relHours);
          const devSet = new Set(act.map(e => e.device));
          tlRows = (tl ?? [])
            .filter(e => (e.kind === "change" && rel.change) || (e.kind === "finding" && rel.finding) || (e.kind === "event" && rel.event))
            .filter(e => devSet.size === 0 || [...devSet].some(d => e.device.includes(d)))
            .map(e => {
              const t = new Date(e.time);
              return {
                ts: t.getTime(),
                tsText: t.toTimeString().slice(0, 8),
                device: e.device,
                source: e.kind,
                line: e.title + (e.detail ? `（${e.detail}）` : ""),
              };
            });
        } catch { /* 关联源拉取失败：日志照常 */ }
      }
      const merged = [...mergeRows(parts), ...tlRows].sort((a, b) => a.ts - b.ts).slice(-MAX_ROWS);
      setRows(merged);
      setNote(notes.length ? notes.join("；") : (parts.length ? t("ndv.logwb.mergedN", { n: parts.length, rows: rowsCount(parts), tl: tlRows.length }) : (tlRows.length ? t("ndv.logwb.tlOnly", { n: tlRows.length }) : t("ndv.logwb.noOutput"))));
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
      setSearch(await app.NetDevLogSearch(pattern.trim(), scope, [], searchSince.trim()));
    } catch (e) {
      setSearch(null);
      setNote(String(e));
    } finally {
      setSearchBusy(false);
    }
  };

  // C3.2：合并时间线导出为 .txt（含来源文本化）。
  const exportRows = async () => {
    if (shown.length === 0) return;
    const text = shown.map(r => `[${r.tsText || "-"}] ${r.device} ${r.source} ${r.line}`).join("\n") + "\n";
    const p = await exportTextFile(`netdev-timeline-${new Date().toISOString().slice(0, 10)}.txt`, text);
    if (p) setNote(t("ndv.exportedTo", { path: p }));
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
    onInsertComposer(`${t("ndv.logwb.aiPrefix")}\n\n\`\`\`\n${excerpt}\n\`\`\``);
  };

  return (
    <div className="ndv-logwb" style={hidden ? { display: "none" } : undefined}>
      {/* 左栏：源选择（§10.3 契约：设备 → 源，多选） */}
      <div className="ndv-logwb__srcs">
        <div className="ndv__card-title" style={{ fontSize: 11.5 }}>{t("ndv.logwb.sources", { on: entries.filter(e => e.active).length, n: entries.length })}</div>
        {entries.map(e => (
          <div key={e.id} className="ndv-logwb__src">
            <input type="checkbox" checked={e.active} onChange={ev => setEntries(prev => prev.map(x => x.id === e.id ? { ...x, active: ev.target.checked } : x))} />
            <span title={`${e.device} ${sourceOf(e)}`} style={{ flex: 1, minWidth: 0 }}>
              <b style={{ fontWeight: 600 }}>{e.device}</b>
              <small> {sourceOf(e)}</small>
            </span>
            <span role="button" title={t("ndv.logwb.remove")} style={{ cursor: "pointer", opacity: 0.6 }} onClick={() => setEntries(prev => prev.filter(x => x.id !== e.id))}>×</span>
          </div>
        ))}
        <div style={{ display: "flex", flexDirection: "column", gap: 4, marginTop: 4 }}>
          <select className="mem-select" value={effectiveAddDevice} onChange={e => setAddDevice(e.target.value)}>
            {hosts.map(h => <option key={h.name} value={h.name}>{h.name}</option>)}
            {hosts.length === 0 && <option value="">{t("ndv.logp.noHosts")}</option>}
          </select>
          <div style={{ display: "flex", gap: 4 }}>
            <select className="mem-select" style={{ width: 74 }} value={addKind} onChange={e => setAddKind(e.target.value as SrcKind)}>
              <option value="file">{t("ndv.logp.kFile")}</option>
              <option value="journal">{t("ndv.logwb.kUnit")}</option>
              <option value="docker">{t("ndv.logwb.kContainer")}</option>
              <option value="k8s">K8s</option>
            </select>
            <input className="mem-input" style={{ flex: 1, minWidth: 0 }} value={addTarget} onChange={e => setAddTarget(e.target.value)}
              placeholder={addKind === "file" ? "/var/log/nginx/error.log" : addKind === "journal" ? "nginx" : addKind === "k8s" ? t("ndv.logwb.phK8s") : t("ndv.logp.phContainer")} />
          </div>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => addEntry(effectiveAddDevice, addKind, addTarget)}>{"＋ "}{t("ndv.logwb.addSource")}</span>
        </div>
        {(devices.length > 0 && hosts.length === 0) && <div className="ndv__hint">{t("ndv.logwb.noServerDevices")}</div>}
      </div>

      {/* 主区：合并时间线 + 底栏搜索 */}
      <div className="ndv-logwb__main">
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
          <label className="ndv__meta">{t("ndv.logp.rows")}</label>
          <input className="mem-input" type="number" style={{ width: 60 }} value={tailN} min={1} max={1000}
            onChange={e => setTailN(Math.min(1000, Math.max(1, Number(e.target.value) || 200)))} />
          <label className="ndv__meta">{t("ndv.logp.since")}</label>
          <input className="mem-input" style={{ width: 128 }} value={since} onChange={e => setSince(e.target.value)} placeholder="2026-08-27 10:00 或 -1h" />
          <span className={`btn btn--small ${busy ? "" : "btn--primary"}`} role="button" onClick={() => void fetchAll()}>{busy ? t("ndv.logp.reading") : t("ndv.logwb.readMerge")}</span>
          <span className="ndv__meta" style={{ marginLeft: 6 }}>{t("ndv.logwb.relSources")}</span>
          {(["change", "finding", "event"] as const).map(k => (
            <span key={k} className={`ndv__chip ndv-logwb__rel${rel[k] ? " ndv-logwb__rel--on" : ""}`} role="button"
              title={k === "change" ? t("ndv.logwb.relChange") : k === "finding" ? t("ndv.logwb.relFinding") : t("ndv.logwb.relEvent")}
              onClick={() => setRel(r => ({ ...r, [k]: !r[k] }))}>{t(KIND_LABEL[k] as never)}</span>
          ))}
          <select className="mem-select" style={{ width: 64 }} value={String(relHours)} title={t("ndv.logwb.relWindow")}
            onChange={e => setRelHours(Number(e.target.value))}>
            <option value="1">1h</option>
            <option value="24">24h</option>
            <option value="168">7d</option>
          </select>
          <label className="ndv__meta" style={{ marginLeft: 8 }}>{t("ndv.logp.grep")}</label>
          <input className="mem-input" style={{ width: 120 }} value={viewGrep} onChange={e => setViewGrep(e.target.value)} placeholder={t("ndv.logwb.phRegexView")} />
          <span className="ndv__meta">{t("ndv.logwb.nLines", { n: shown.length })}</span>
          {shown.length > 0 && (
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void exportRows()}>{t("ndv.logwb.exportTxt")}</span>
          )}
          {onInsertComposer && shown.length > 0 && (
            <span className="btn btn--secondary btn--small" role="button" onClick={sendToAI}>{t("ndv.sendToAI")}</span>
          )}
        </div>
        {note && <div className="ndv__hint">{note}</div>}

        <div className="ndv-logwb__rows">
          {entries.length === 0 && rows.length === 0 && (
            <div className="ndv__hint" style={{ padding: 8 }}>
              {t("ndv.logwb.emptyHint1")}
              {t("ndv.logwb.emptyHint2")}
              {hosts[0] && (
                <span style={{ display: "inline-flex", gap: 6, marginLeft: 8 }}>
                  <span className="ndv__chip" role="button" onClick={() => addEntry(hosts[0].name, "file", "/var/log/syslog")}>{"＋ "}{hosts[0].name} /var/log/syslog</span>
                  <span className="ndv__chip" role="button" onClick={() => addEntry(hosts[0].name, "file", "/var/log/auth.log")}>{"＋ "}auth.log</span>
                </span>
              )}
            </div>
          )}
          {shown.map((r, i) => {
            // 源徽标按类型着色：file 灰 / journal 紫 / docker 青绿 / k8s 赤陶
            // ——合并时间线上一眼分清每行的出处（全部 token 色）。
            const k = r.source.split(":")[0];
            const srcColor =
              k === "docker" ? "var(--shell-accent)" :
              k === "journal" ? "var(--accent-alt)" :
              k === "k8s" ? "var(--accent)" :
              k === "change" ? "var(--accent-alt)" :
              k === "finding" ? "var(--warn)" :
              k === "event" ? "var(--fg-dim)" : "var(--fg-dim)";
            const srcText = KIND_LABEL[k] ? `${t(KIND_LABEL[k] as never)}·${r.device}` : `${r.device}·${r.source.replace(/^(file|journal|docker|k8s):/, "")}`;
            return (
              <div key={i} className={`ndv-logwb__row ${levelClass(r.line)}`}>
                {r.tsText && <span className="ts">{r.tsText} </span>}
                <span className="src" style={{ color: srcColor }}>{srcText} </span>
                {r.line}
              </div>
            );
          })}
        </div>

        {/* 底栏：跨设备搜索（§3.3——一个 IP 搜全网的家） */}
        <div className="ndv-logwb__search">
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
            <span className="ndv__meta" style={{ fontWeight: 600 }}>{t("ndv.logwb.xdevSearch")}</span>
            <input className="mem-input" style={{ width: 200 }} value={pattern} onChange={e => setPattern(e.target.value)}
              placeholder={t("ndv.logwb.phSearch")} onKeyDown={e => { if (e.key === "Enter") void runSearch(); }} />
            <input className="mem-input" style={{ width: 110 }} value={searchSince} onChange={e => setSearchSince(e.target.value)} placeholder="-1h" />
            <span className="btn btn--secondary btn--small" role="button" onClick={() => setScopeOpen(v => !v)}
              title={t("ndv.logwb.scopeTip")}>{scope.length === 0 ? t("ndv.logwb.scopeAll") : t("ndv.logwb.scopeN", { n: scope.length })}</span>
            <span className={`btn btn--small ${searchBusy ? "" : "btn--secondary"}`} role="button" onClick={() => void runSearch()}>{searchBusy ? t("ndv.logwb.searching") : t("ndv.logwb.searchAll")}</span>
            {search && (
              <span className="ndv__meta">
                {t("ndv.logwb.searchSummary", { covered: search.covered_devices, total: search.total_devices, hits: search.hits.length, devs: search.devices_with_hits })}
                {search.budget_stopped && <b style={{ color: "var(--danger)" }}> · {t("ndv.logwb.budgetStopped")}</b>}
              </span>
            )}
          </div>
          {search?.note && <div className="ndv__hint">{search.note}</div>}
          {scopeOpen && (
            <div style={{ display: "flex", flexWrap: "wrap", gap: 4, padding: "4px 0" }}>
              {hosts.map(h => (
                <span key={h.name} className={`ndv__chip${scope.includes(h.name) ? " ndv-logwb__rel--on" : ""}`} role="button"
                  onClick={() => setScope(s => s.includes(h.name) ? s.filter(x => x !== h.name) : [...s, h.name])}>{h.name}</span>
              ))}
              <span className="btn btn--secondary btn--small" role="button" onClick={() => setScope([])}>{t("ndv.logwb.scopeClear")}</span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => setScope(hosts.map(h => h.name))}>{t("ndv.logwb.scopeSelectAll")}</span>
            </div>
          )}
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
            <div className="ndv__hint">{t("ndv.logwb.skipped", { list: search.skipped.slice(0, 5).join("；"), more: search.skipped.length > 5 ? t("ndv.logwb.moreN", { n: search.skipped.length }) : "" })}</div>
          )}
        </div>
      </div>
    </div>
  );
}
