import { useCallback, useEffect, useMemo, useState } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import { parseIOCList } from "../../lib/ioc";
import type { NetDevDeviceView, NetDevIncidentCase } from "../../lib/types";

// SecWorkbench — 主区「安全工作台」（NETDEV_SPEC_V2 §10.4）：第三工作台。
// 左栏案例列表 + 当前案例 IOC 台账；主区时间线（引用卡：Finding/日志命中/
// 体检项/人工笔记）。入侵排查向导（§10.7）是案例的出生方式之一——五步
// checklist 驱动既有桥（体检/全网搜索/导出），每步结果钉进案例时间线。
// 案例纯本地存储；复盘导出 = CaseBundle（含相关 Finding 与 24h 变更）。
// CVE 视图（completion-spec §2.3）：feed 导入 → 匹配清单 → 一键扫荡。

const KIND_LABEL: Record<string, string> = { finding: "ndv.sec.kFinding", log: "ndv.sec.kLog", audit: "ndv.sec.kAudit", triage: "ndv.sec.kTriage", note: "ndv.sec.kNote" };
const KIND_COLOR: Record<string, string> = {
  finding: "var(--warn)",
  log: "var(--accent)",
  audit: "var(--accent-alt)",
  triage: "var(--shell-accent)",
  note: "var(--fg-faint)",
};

type CVEMatch = { device: string; cve_id: string; desc: string; severity: string; product: string };

// 示例 feed（§2.3）：几条著名真实 CVE，覆盖清单常见厂商。按钮只把 JSON 填进
// 文本框——导入仍由用户触发；产品不预置、不自动导入 feed（附录 B-4 不分发原则）。
const SAMPLE_CVE_FEED = `{"cves":[
{"id":"CVE-2023-20198","desc":"Cisco IOS XE Web UI unauthenticated privilege escalation (exploited in the wild); disable Web UI or upgrade","products":["cisco"],"severity":"critical"},
{"id":"CVE-2017-17215","desc":"Huawei router UPnP TR-064 remote command execution (used by Mirai variants); disable UPnP or upgrade","products":["huawei"],"severity":"critical"},
{"id":"CVE-2020-1472","desc":"Windows domain controller Netlogon elevation of privilege (Zerologon); apply the 2020-08 patch","products":["windows"],"severity":"critical"},
{"id":"CVE-2024-1086","desc":"Linux kernel nf_tables use-after-free, local privilege escalation; update the kernel","products":["linux"],"severity":"high"}
]}`;

// runPool — bounded-concurrency map（体检并行化的最小实现，失败不中断全队）。
async function runPool<T, R>(items: T[], limit: number, fn: (x: T) => Promise<R>): Promise<(R | null)[]> {
  const out: (R | null)[] = new Array(items.length).fill(null);
  let i = 0;
  await Promise.all(Array.from({ length: Math.min(Math.max(limit, 1), items.length) }, async () => {
    for (;;) {
      const idx = i++;
      if (idx >= items.length) return;
      try { out[idx] = await fn(items[idx]); } catch { out[idx] = null; }
    }
  }));
  return out;
}

export function SecWorkbench({ devices, hidden }: {
  devices: NetDevDeviceView[];
  hidden?: boolean;
}) {
  const t = useT();
  const [view, setView] = useState<"cases" | "cve">("cases");
  const [cases, setCases] = useState<NetDevIncidentCase[]>([]);
  const [currentId, setCurrentId] = useState("");
  const [note, setNote] = useState("");
  const [newTitle, setNewTitle] = useState("");
  const [noteText, setNoteText] = useState("");
  const [iocValue, setIocValue] = useState("");
  const [iocType, setIocType] = useState("ip");
  const [iocBulk, setIocBulk] = useState(false);
  const [iocBulkText, setIocBulkText] = useState("");
  const [iocNoteIdx, setIocNoteIdx] = useState(-1);
  const [iocNoteDraft, setIocNoteDraft] = useState("");
  const [wizPattern, setWizPattern] = useState("");
  const [busy, setBusy] = useState("");
  const [hostFilter, setHostFilter] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [renameDraft, setRenameDraft] = useState("");

  // CVE 视图状态：feed 粘贴导入 + 匹配清单 + 扫荡。
  const [cveFeed, setCveFeed] = useState("");
  const [cveBusy, setCveBusy] = useState("");
  const [cveMatches, setCveMatches] = useState<CVEMatch[] | null>(null);
  const [cveSweepHint, setCveSweepHint] = useState("");

  const current = cases.find(c => c.id === currentId);
  const hosts = useMemo(() => devices.filter(d => d.vendor === "linux" || d.vendor === "windows"), [devices]);
  const shownHosts = useMemo(() => {
    const f = hostFilter.trim().toLowerCase();
    return f ? hosts.filter(h => h.name.toLowerCase().includes(f)) : hosts;
  }, [hosts, hostFilter]);

  const refresh = useCallback(async () => {
    try {
      const list = await app.NetDevCases();
      setCases(list ?? []);
    } catch (e) {
      setNote(String(e));
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  // 场景卡「CVE 清单匹配」直达：切换到 CVE 视图（feed 导入在左栏）。
  useEffect(() => {
    const onCVE = () => setView("cve");
    window.addEventListener("fairpeer:netdev-cve", onCVE);
    return () => window.removeEventListener("fairpeer:netdev-cve", onCVE);
  }, []);

  // Finding 卡「建案例」入口（findings 页签 → 本工作台）：事件带首条条目。
  useEffect(() => {
    const onNew = (ev: Event) => {
      const d = (ev as CustomEvent<{ title?: string; device?: string; text?: string }>).detail ?? {};
      const c: NetDevIncidentCase = {
        id: "", title: d.title || t("ndv.sec.defaultCaseTitle"), status: "open",
        devices: d.device ? [d.device] : [],
        entries: d.text ? [{ time: new Date().toISOString(), kind: "finding", device: d.device ?? "", text: d.text }] : [],
        iocs: [], created_at: "", updated_at: "",
      };
      void app.NetDevCaseSave(c).then(saved => {
        void refresh().then(() => setCurrentId(saved.id));
      });
    };
    window.addEventListener("fairpeer:netdev-case", onNew);
    return () => window.removeEventListener("fairpeer:netdev-case", onNew);
  }, [refresh]);

  const save = useCallback(async (c: NetDevIncidentCase) => {
    try {
      const saved = await app.NetDevCaseSave(c);
      setCases(prev => {
        const i = prev.findIndex(x => x.id === saved.id);
        if (i >= 0) { const cp = [...prev]; cp[i] = saved; return cp; }
        return [saved, ...prev];
      });
      setCurrentId(saved.id);
      setNote(t("ndv.sec.saved"));
    } catch (e) { setNote(String(e)); }
  }, []);

  const newCase = () => {
    if (!newTitle.trim()) { setNote(t("ndv.sec.needTitle")); return; }
    void save({ id: "", title: newTitle.trim(), status: "open", devices: [], entries: [], iocs: [], created_at: "", updated_at: "" });
    setNewTitle("");
  };

  // 一键示例案例：本地生成带时间线 + IOC 台账的演示数据，让空环境一眼看懂
  // 工作台全貌（头部/向导/时间线/台账），可随手删除。IOC 用文档保留网段与
  // example.com，避免指向真实资产。
  const makeExampleCase = useCallback(() => {
    const at = (minAgo: number) => new Date(Date.now() - minAgo * 60_000).toISOString();
    const dev = hosts[0]?.name ?? "";
    void save({
      id: "", title: t("ndv.sec.exampleCaseTitle"), status: "open",
      devices: hosts.slice(0, 2).map(h => h.name),
      entries: [
        { time: at(95), kind: "finding", device: dev, text: t("ndv.sec.exEntryFinding") },
        { time: at(60), kind: "log", device: dev, text: t("ndv.sec.exEntryLog") },
        { time: at(32), kind: "triage", device: dev, text: t("ndv.sec.exEntryTriage") },
        { time: at(8), kind: "note", device: "", text: t("ndv.sec.exEntryNote") },
      ],
      iocs: [
        { value: "198.51.100.77", type: "ip", note: "", added_at: at(60) },
        { value: "beacon.badhost.example.com", type: "domain", note: "", added_at: at(58) },
        { value: "d41d8cd98f00b204e9800998ecf8427e", type: "hash", note: "", added_at: at(30) },
      ],
      created_at: at(95), updated_at: "",
    });
  }, [hosts, save, t]);

  const addEntry = (kind: string, device: string, text: string, ref?: string) => {
    if (!current) return;
    void save({ ...current, entries: [...(current.entries ?? []), { time: new Date().toISOString(), kind, device, text, ref }] });
  };
  const delEntry = (idxFromEnd: number) => {
    if (!current) return;
    const entries = [...(current.entries ?? [])];
    // 渲染是倒序的：换算回正序下标。
    entries.splice(entries.length - 1 - idxFromEnd, 1);
    void save({ ...current, entries });
  };
  const addIOC = () => {
    if (!current || !iocValue.trim()) return;
    void save({ ...current, iocs: [...(current.iocs ?? []), { value: iocValue.trim(), type: iocType, note: "", added_at: new Date().toISOString() }] });
    setIocValue("");
  };
  // IOC 批量导入（§4.6 外部 feed 来源）：粘贴清单 → 类型自动识别 → 并入台账。
  const importIOCs = () => {
    if (!current) return;
    const parsed = parseIOCList(iocBulkText);
    if (parsed.length === 0) { setNote(t("ndv.sec.iocBulkEmpty")); return; }
    const now = new Date().toISOString();
    void save({ ...current, iocs: [...(current.iocs ?? []), ...parsed.map(p => ({ ...p, added_at: now }))] });
    setNote(t("ndv.sec.iocBulkDone", { n: parsed.length }));
    setIocBulkText("");
    setIocBulk(false);
  };

  // ── {t("ndv.sec.wizTitle")}（§10.7）：五步，各驱动既有桥，结果钉入时间线 ──────────
  const wizTriage = async () => {
    if (!current) return;
    const targets = (current.devices ?? []).filter(d => hosts.some(h => h.name === d));
    if (targets.length === 0) { setNote(t("ndv.sec.needScope")); return; }
    setBusy("triage");
    try {
      const reps = await runPool(targets, 4, d => app.NetDevTriageRun(d));
      const entries = [...(current.entries ?? [])];
      const stamp = new Date().toISOString();
      targets.forEach((d, i) => {
        const rep = reps[i];
        if (!rep) { entries.push({ time: stamp, kind: "note", device: d, text: t("ndv.sec.triageFailed") }); return; }
        const anoms = rep.anomalies ?? [];
        entries.push({ time: stamp, kind: "triage", device: d, text: rep.summary + (anoms.length ? "：" + anoms.join("；") : "") });
      });
      await save({ ...current, entries });
    } catch (e) { setNote(String(e)); } finally { setBusy(""); }
  };
  const wizSearch = async () => {
    if (!current || !wizPattern.trim()) { setNote(t("ndv.sec.needKeyword")); return; }
    setBusy("search");
    try {
      const r = await app.NetDevLogSearch(wizPattern.trim(), current.devices ?? [], [], "");
      const entries = [...(current.entries ?? [])];
      for (const h of (r.hits ?? []).slice(0, 10)) {
        entries.push({ time: new Date().toISOString(), kind: "log", device: h.device, text: h.line.slice(0, 200), ref: h.source });
      }
      entries.push({ time: new Date().toISOString(), kind: "note", device: "", text: t("ndv.sec.searchEntry", { kw: wizPattern.trim(), hits: r.hits?.length ?? 0, covered: r.covered_devices, total: r.total_devices }) });
      await save({ ...current, entries });
    } catch (e) { setNote(String(e)); } finally { setBusy(""); }
  };
  const wizExport = async () => {
    if (!current) return;
    setBusy("export");
    try {
      const p = await app.NetDevCaseBundle(current.id);
      setNote(t("ndv.exportedTo", { path: p }));
    } catch (e) { setNote(String(e)); } finally { setBusy(""); }
  };

  const toggleDevice = (name: string) => {
    if (!current) return;
    const ds = current.devices ?? [];
    void save({ ...current, devices: ds.includes(name) ? ds.filter(d => d !== name) : [...ds, name] });
  };

  // ── CVE 视图（§2.3）：feed 导入 → 匹配清单 → 扫荡 ──────────────────────
  const cveImport = async () => {
    if (!cveFeed.trim()) return;
    setCveBusy("import");
    setCveSweepHint("");
    try {
      const n = await app.NetDevImportCVEs(cveFeed);
      setNote(t("ndv.sec.feedImported", { n }));
      setCveFeed("");
    } catch (e) { setNote(String(e)); } finally { setCveBusy(""); }
  };
  const cveList = async () => {
    setCveBusy("list");
    try {
      setCveMatches(await app.NetDevCVEMatches());
    } catch (e) { setNote(String(e)); } finally { setCveBusy(""); }
  };
  const cveSweep = async () => {
    setCveBusy("sweep");
    setCveSweepHint("");
    try {
      const f = await app.NetDevCVESweep();
      if (!f) {
        // 空结果断头路引导：常见原因是清单未匹配或 feed 未导入（§3.4）。
        setCveSweepHint(t("ndv.sec.sweepEmptyHint"));
      } else {
        setCveSweepHint(t("ndv.sec.sweepDone", { title: f.title }));
      }
    } catch (e) { setNote(String(e)); } finally { setCveBusy(""); }
  };

  return (
    <div className="ndv-sec" style={hidden ? { display: "none" } : undefined}>
      {/* 左栏：案例列表 + IOC 台账 */}
      <div className="ndv-sec__side">
        <div className="ndv-sec__views" style={{ display: "flex", gap: 4, marginBottom: 6 }}>
          <span className={`btn btn--small ${view === "cases" ? "btn--primary" : "btn--secondary"}`} role="button" onClick={() => setView("cases")}>{t("ndv.sec.tabCases")}</span>
          <span className={`btn btn--small ${view === "cve" ? "btn--primary" : "btn--secondary"}`} role="button" onClick={() => setView("cve")}>{t("ndv.sec.tabCve")}</span>
        </div>
        {view === "cases" && (
          <>
            <div className="ndv__card-title" style={{ fontSize: 11.5 }}>{t("ndv.sec.casesTitle", { n: cases.length })}</div>
            <div style={{ display: "flex", gap: 4 }}>
              <input className="mem-input" style={{ flex: 1, minWidth: 0 }} value={newTitle} onChange={e => setNewTitle(e.target.value)}
                placeholder={t("ndv.sec.phNewCase")} onKeyDown={e => { if (e.key === "Enter") newCase(); }} />
              <span className="btn btn--primary btn--small" role="button" onClick={newCase}>＋</span>
            </div>
            {cases.map(c => (
              <div key={c.id} className={`ndv-sec__case${c.id === currentId ? " ndv-sec__case--on" : ""}`} role="button"
                onClick={() => setCurrentId(c.id)}>
                <span className="ndv-sec__case-title">{c.title}</span>
                <span className="ndv-sec__case-meta">{c.status === "closed" ? "✓" : "●"} {c.iocs?.length ?? 0} IOC</span>
              </div>
            ))}
            {current && (
              <>
                <div className="ndv__group-label" style={{ marginTop: 10, display: "flex", alignItems: "center", gap: 4 }}>
                  {t("ndv.sec.iocLedger", { n: current.iocs?.length ?? 0 })}
                  <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }}
                    onClick={() => setIocBulk(!iocBulk)}>{t("ndv.sec.iocBulk")}</span>
                </div>
                {iocBulk && (
                  <>
                    <textarea className="mem-input" rows={4} style={{ width: "100%", fontSize: 10.5, marginTop: 4, fontFamily: "var(--font-mono, monospace)" }}
                      placeholder={t("ndv.sec.phIocBulk")} value={iocBulkText} onChange={e => setIocBulkText(e.target.value)} />
                    <div style={{ display: "flex", gap: 4, marginTop: 4 }}>
                      <span className="btn btn--primary btn--small" role="button" onClick={importIOCs}>{t("ndv.sec.iocBulkGo")}</span>
                    </div>
                  </>
                )}
                <div style={{ display: "flex", gap: 4 }}>
                  <select className="mem-select" style={{ width: 66 }} value={iocType} onChange={e => setIocType(e.target.value)}>
                    {["ip", "domain", "hash", "keyword"].map(t => <option key={t} value={t}>{t}</option>)}
                  </select>
                  <input className="mem-input" style={{ flex: 1, minWidth: 0 }} value={iocValue} onChange={e => setIocValue(e.target.value)}
                    placeholder={t("ndv.bse.phValue")} onKeyDown={e => { if (e.key === "Enter") addIOC(); }} />
                  <span className="btn btn--secondary btn--small" role="button" onClick={addIOC}>＋</span>
                </div>
                {(current.iocs ?? []).map((i, idx) => (
                  <div key={idx} className="ndv-sec__ioc">
                    <span className="ndv-sec__ioc-type">{i.type}</span>
                    <span className="ndv-sec__ioc-value" title={i.value}>{i.value}</span>
                    {iocNoteIdx === idx ? (
                      <input className="mem-input" autoFocus style={{ width: 70, fontSize: 10.5 }} value={iocNoteDraft}
                        onChange={e => setIocNoteDraft(e.target.value)}
                        onBlur={() => {
                          const iocs = [...(current.iocs ?? [])];
                          iocs[idx] = { ...iocs[idx], note: iocNoteDraft.trim() };
                          void save({ ...current, iocs });
                          setIocNoteIdx(-1);
                        }}
                        onKeyDown={e => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); }} />
                    ) : (
                      <span role="button" style={{ cursor: "pointer", opacity: 0.6, fontSize: 10.5 }}
                        title={i.note || t("ndv.sec.addNoteTip")}
                        onClick={() => { setIocNoteIdx(idx); setIocNoteDraft(i.note ?? ""); }}>{i.note || "✎"}</span>
                    )}
                    <span role="button" style={{ cursor: "pointer", opacity: 0.5 }} onClick={() => void save({ ...current, iocs: (current.iocs ?? []).filter((_, j) => j !== idx) })}>×</span>
                  </div>
                ))}
              </>
            )}
          </>
        )}
        {view === "cve" && (
          <>
            <div className="ndv__card-title" style={{ fontSize: 11.5 }}>{t("ndv.sec.cveFeed")}</div>
            <textarea className="mem-input" rows={4} style={{ width: "100%", fontSize: 10.5, fontFamily: "var(--font-mono, monospace)" }}
              placeholder={t('ndv.sec.phFeed')}
              value={cveFeed} onChange={e => setCveFeed(e.target.value)} />
            <div style={{ display: "flex", gap: 4, marginTop: 4 }}>
              <span className="btn btn--secondary btn--small" role="button"
                title={t("ndv.sec.fillExampleTip")}
                onClick={() => setCveFeed(SAMPLE_CVE_FEED)}>{t("ndv.sec.fillExample")}</span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void cveImport()}>{cveBusy === "import" ? t("ndv.sec.importing") : t("ndv.sec.importFeed")}</span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void cveList()}>{cveBusy === "list" ? t("ndv.sec.refreshing") : t("ndv.sec.refreshMatches")}</span>
              <span className="btn btn--primary btn--small" role="button" onClick={() => void cveSweep()}>{cveBusy === "sweep" ? t("ndv.sec.sweeping") : t("ndv.sec.cveSweep")}</span>
            </div>
            {cveSweepHint && <div className="ndv__hint" style={{ padding: "4px 0" }}>{cveSweepHint}</div>}
          </>
        )}
      </div>

      {/* 主区：案例头部 + 向导 + 时间线 / CVE 匹配表 */}
      <div className="ndv-sec__main">
        {view === "cve" ? (
          <>
            <div className="ndv__card-title" style={{ fontSize: 11.5 }}>{t("ndv.sec.cveMatches")}</div>
            {cveMatches === null ? (
              <div className="ndv__empty" style={{ flex: 1 }}>
                <div className="ndv__empty-title">{t("ndv.sec.noMatchesLoaded")}</div>
                <div className="ndv__empty-desc">{t("ndv.sec.matchesHint")}</div>
              </div>
            ) : cveMatches.length === 0 ? (
              <div className="ndv__empty" style={{ flex: 1 }}>
                <div className="ndv__empty-title">{t("ndv.sec.noMatches")}</div>
                <div className="ndv__empty-desc">{t("ndv.sec.noMatchesHint")}</div>
              </div>
            ) : (
              <table className="mem-hint" style={{ width: "100%", borderCollapse: "collapse", fontSize: 11 }}>
                <thead>
                  <tr style={{ textAlign: "left" }}><th>{t("ndv.sec.colDevice")}</th><th>CVE</th><th>{t("ndv.sec.colSeverity")}</th><th>{t("ndv.sec.colProduct")}</th><th>{t("ndv.sec.colDesc")}</th></tr>
                </thead>
                <tbody>
                  {cveMatches.map((m, i) => (
                    <tr key={i}>
                      <td>{m.device}</td>
                      <td style={{ fontFamily: "var(--font-mono, monospace)" }}>{m.cve_id}</td>
                      <td style={{ color: m.severity === "high" || m.severity === "critical" ? "var(--err, #e5484d)" : undefined }}>{m.severity}</td>
                      <td>{m.product}</td>
                      <td style={{ opacity: 0.75 }}>{m.desc?.slice(0, 80)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        ) : !current ? (
          <div className="ndv__empty" style={{ flex: 1 }}>
            <div className="ndv__empty-title">{t("ndv.sec.noCases")}</div>
            <div className="ndv__empty-desc">{t("ndv.sec.noCasesHint")}</div>
            {cases.length === 0 && (
              <span className="btn btn--secondary btn--small" role="button" style={{ marginTop: 8 }}
                onClick={() => void makeExampleCase()}>{t("ndv.sec.exampleCaseBtn")}</span>
            )}
          </div>
        ) : (
          <>
            <div className="ndv-sec__head">
              {renaming ? (
                <input className="mem-input" autoFocus style={{ width: 240 }} value={renameDraft}
                  onChange={e => setRenameDraft(e.target.value)}
                  onBlur={() => { if (renameDraft.trim() && renameDraft.trim() !== current.title) void save({ ...current, title: renameDraft.trim() }); setRenaming(false); }}
                  onKeyDown={e => { if (e.key === "Enter") (e.target as HTMLInputElement).blur(); if (e.key === "Escape") setRenaming(false); }} />
              ) : (
                <span className="ndv-sec__title" role="button" title={t("ndv.sec.renameTip")}
                  onClick={() => { setRenaming(true); setRenameDraft(current.title); }}>{current.title}</span>
              )}
              <span className={`ndv-sec__st${current.status === "open" ? "" : " ndv-sec__st--closed"}`}
                role="button" onClick={() => void save({ ...current, status: current.status === "open" ? "closed" : "open" })}>
                {current.status === "open" ? t("ndv.sec.stOpen") : t("ndv.sec.stClosed")}
              </span>
              <span style={{ marginLeft: "auto" }} />
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void app.NetDevCaseDelete(current.id).then(refresh)}>{t("ndv.tpl.delete")}</span>
            </div>

            {/* {t("ndv.sec.wizTitle")}（五步 checklist，§10.7） */}
            <div className="ndv-sec__wiz">
              <span className="ndv__meta" style={{ fontWeight: 700 }}>{t("ndv.sec.wizTitle")}</span>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">1</span>
                <span className="ndv__meta">{t("ndv.sec.wizScope", { n: (current.devices ?? []).length })}</span>
                {hosts.length > 12 && (
                  <>
                    <input className="mem-input" style={{ width: 110 }} placeholder={t("ndv.sec.phFilterHosts")} value={hostFilter} onChange={e => setHostFilter(e.target.value)} />
                    <span className="btn btn--secondary btn--small" role="button"
                      onClick={() => void save({ ...current, devices: [...new Set([...(current.devices ?? []), ...shownHosts.map(h => h.name)])] })}>{t("ndv.sec.selectFiltered")}</span>
                    <span className="btn btn--secondary btn--small" role="button"
                      onClick={() => void save({ ...current, devices: (current.devices ?? []).filter(d => !shownHosts.some(h => h.name === d)) })}>{t("ndv.sec.unselectFiltered")}</span>
                  </>
                )}
                <span style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
                  {shownHosts.map(h => (
                    <span key={h.name} className={`ndv__chip${(current.devices ?? []).includes(h.name) ? " ndv-logwb__rel--on" : ""}`}
                      role="button" onClick={() => toggleDevice(h.name)}>{h.name}</span>
                  ))}
                  {shownHosts.length === 0 && <span className="ndv__meta" style={{ opacity: 0.6 }}>{t("ndv.sec.noHostsMatch")}</span>}
                </span>
              </div>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">2</span>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => void wizTriage()}>{busy === "triage" ? t("ndv.sec.triaging") : t("ndv.sec.parallelTriage")}</span>
                <span className="ndv__meta">{t("ndv.sec.anomaliesNote")}</span>
              </div>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">3</span>
                <input className="mem-input" style={{ width: 200 }} value={wizPattern} onChange={e => setWizPattern(e.target.value)} placeholder={t("ndv.sec.phIoc")} />
                <span className="btn btn--secondary btn--small" role="button" onClick={() => void wizSearch()}>{busy === "search" ? t("ndv.logwb.searching") : t("ndv.logwb.searchAll")}</span>
                <span className="ndv__meta">{t("ndv.sec.pinHits")}</span>
              </div>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">4</span>
                <input className="mem-input" style={{ flex: 1, minWidth: 120 }} value={noteText} onChange={e => setNoteText(e.target.value)} placeholder={t("ndv.sec.phNote")} />
                <span className="btn btn--secondary btn--small" role="button" onClick={() => { if (noteText.trim()) { addEntry("note", "", noteText.trim()); setNoteText(""); } }}>{t("ndv.sec.pin")}</span>
              </div>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">5</span>
                <span className="btn btn--primary btn--small" role="button" onClick={() => void wizExport()}>{busy === "export" ? t("ndv.exporting") : t("ndv.sec.exportBundle")}</span>
                <span className="ndv__meta">{t("ndv.sec.bundleHint")}</span>
              </div>
            </div>

            {note && <div className="ndv__hint">{note}</div>}

            {/* 时间线：引用卡 */}
            <div className="ndv-sec__tl">
              {(current.entries ?? []).length === 0 && <div className="ndv__hint" style={{ padding: 8 }}>{t("ndv.sec.tlEmpty")}</div>}
              {[...(current.entries ?? [])].reverse().map((e, i) => (
                <div key={i} className={`ndv-sec__entry ndv-sec__entry--${e.kind}`}>
                  <span className="ndv-sec__entry-dot" style={{ background: KIND_COLOR[e.kind] ?? "var(--fg-faint)" }} />
                  <span className="ndv-sec__entry-time">{String(e.time ?? "").replace("T", " ").slice(5, 19)}</span>
                  <span className="ndv-sec__entry-kind" style={{ color: KIND_COLOR[e.kind] ?? "var(--fg-faint)" }}>{t(KIND_LABEL[e.kind] as never) ?? e.kind}{e.device ? `·${e.device}` : ""}</span>
                  <span className="ndv-sec__entry-text">{e.text}</span>
                  <span role="button" style={{ cursor: "pointer", opacity: 0.4, paddingLeft: 4 }} title={t("ndv.sec.deleteEntry")}
                    onClick={() => delEntry(i)}>×</span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
