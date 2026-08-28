import { useCallback, useEffect, useMemo, useState } from "react";
import { app } from "../../lib/bridge";
import type { NetDevDeviceView, NetDevIncidentCase } from "../../lib/types";

// SecWorkbench — 主区「安全工作台」（NETDEV_SPEC_V2 §10.4）：第三工作台。
// 左栏案例列表 + 当前案例 IOC 台账；主区时间线（引用卡：Finding/日志命中/
// 体检项/人工笔记）。入侵排查向导（§10.7）是案例的出生方式之一——五步
// checklist 驱动既有桥（体检/全网搜索/导出），每步结果钉进案例时间线。
// 案例纯本地存储；复盘导出 = CaseBundle（含相关 Finding 与 24h 变更）。

const KIND_LABEL: Record<string, string> = { finding: "发现", log: "日志", audit: "审计", triage: "体检", note: "笔记" };
const KIND_COLOR: Record<string, string> = {
  finding: "var(--warn)",
  log: "var(--accent)",
  audit: "var(--accent-alt)",
  triage: "var(--shell-accent)",
  note: "var(--fg-faint)",
};

export function SecWorkbench({ devices, hidden }: {
  devices: NetDevDeviceView[];
  hidden?: boolean;
}) {
  const [cases, setCases] = useState<NetDevIncidentCase[]>([]);
  const [currentId, setCurrentId] = useState("");
  const [note, setNote] = useState("");
  const [newTitle, setNewTitle] = useState("");
  const [noteText, setNoteText] = useState("");
  const [iocValue, setIocValue] = useState("");
  const [iocType, setIocType] = useState("ip");
  const [wizPattern, setWizPattern] = useState("");
  const [busy, setBusy] = useState("");

  const current = cases.find(c => c.id === currentId);
  const hosts = useMemo(() => devices.filter(d => d.vendor === "linux" || d.vendor === "windows"), [devices]);

  const refresh = useCallback(async () => {
    try {
      const list = await app.NetDevCases();
      setCases(list ?? []);
    } catch (e) {
      setNote(String(e));
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  // Finding 卡「建案例」入口（findings 页签 → 本工作台）：事件带首条条目。
  useEffect(() => {
    const onNew = (ev: Event) => {
      const d = (ev as CustomEvent<{ title?: string; device?: string; text?: string }>).detail ?? {};
      const c: NetDevIncidentCase = {
        id: "", title: d.title || "入侵排查", status: "open",
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
      setNote("已保存");
    } catch (e) { setNote(String(e)); }
  }, []);

  const newCase = () => {
    if (!newTitle.trim()) { setNote("先填案例标题"); return; }
    void save({ id: "", title: newTitle.trim(), status: "open", devices: [], entries: [], iocs: [], created_at: "", updated_at: "" });
    setNewTitle("");
  };

  const addEntry = (kind: string, device: string, text: string, ref?: string) => {
    if (!current) return;
    void save({ ...current, entries: [...(current.entries ?? []), { time: new Date().toISOString(), kind, device, text, ref }] });
  };
  const addIOC = () => {
    if (!current || !iocValue.trim()) return;
    void save({ ...current, iocs: [...(current.iocs ?? []), { value: iocValue.trim(), type: iocType, note: "", added_at: new Date().toISOString() }] });
    setIocValue("");
  };

  // ── 入侵排查向导（§10.7）：五步，各驱动既有桥，结果钉入时间线 ──────────
  const wizTriage = async () => {
    if (!current) return;
    const targets = (current.devices ?? []).filter(d => hosts.some(h => h.name === d));
    if (targets.length === 0) { setNote("先在下方勾选范围设备（linux/windows）"); return; }
    setBusy("triage");
    const entries = [...(current.entries ?? [])];
    try {
      for (const d of targets) {
        const rep = await app.NetDevTriageRun(d);
        const anoms = rep.anomalies ?? [];
        entries.push({ time: new Date().toISOString(), kind: "triage", device: d, text: rep.summary + (anoms.length ? "：" + anoms.join("；") : "") });
      }
      await save({ ...current, entries });
    } catch (e) { setNote(String(e)); } finally { setBusy(""); }
  };
  const wizSearch = async () => {
    if (!current || !wizPattern.trim()) { setNote("向导第 3 步需要关键字"); return; }
    setBusy("search");
    try {
      const r = await app.NetDevLogSearch(wizPattern.trim(), current.devices ?? [], [], "");
      const entries = [...(current.entries ?? [])];
      for (const h of (r.hits ?? []).slice(0, 10)) {
        entries.push({ time: new Date().toISOString(), kind: "log", device: h.device, text: h.line.slice(0, 200), ref: h.source });
      }
      entries.push({ time: new Date().toISOString(), kind: "note", device: "", text: `全网搜索 ${wizPattern.trim()}：命中 ${r.hits?.length ?? 0} 条（覆盖 ${r.covered_devices}/${r.total_devices} 台）` });
      await save({ ...current, entries });
    } catch (e) { setNote(String(e)); } finally { setBusy(""); }
  };
  const wizExport = async () => {
    if (!current) return;
    setBusy("export");
    try {
      const p = await app.NetDevCaseBundle(current.id);
      setNote(`复盘包已导出 → ${p}`);
    } catch (e) { setNote(String(e)); } finally { setBusy(""); }
  };

  const toggleDevice = (name: string) => {
    if (!current) return;
    const ds = current.devices ?? [];
    void save({ ...current, devices: ds.includes(name) ? ds.filter(d => d !== name) : [...ds, name] });
  };

  return (
    <div className="ndv-sec" style={hidden ? { display: "none" } : undefined}>
      {/* 左栏：案例列表 + IOC 台账 */}
      <div className="ndv-sec__side">
        <div className="ndv__card-title" style={{ fontSize: 11.5 }}>案例（{cases.length}）</div>
        <div style={{ display: "flex", gap: 4 }}>
          <input className="mem-input" style={{ flex: 1, minWidth: 0 }} value={newTitle} onChange={e => setNewTitle(e.target.value)}
            placeholder="新案例标题，如「支付服务入侵排查」" onKeyDown={e => { if (e.key === "Enter") newCase(); }} />
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
            <div className="ndv__group-label" style={{ marginTop: 10 }}>IOC 台账（{current.iocs?.length ?? 0}）</div>
            <div style={{ display: "flex", gap: 4 }}>
              <select className="mem-select" style={{ width: 66 }} value={iocType} onChange={e => setIocType(e.target.value)}>
                {["ip", "domain", "hash", "keyword"].map(t => <option key={t} value={t}>{t}</option>)}
              </select>
              <input className="mem-input" style={{ flex: 1, minWidth: 0 }} value={iocValue} onChange={e => setIocValue(e.target.value)}
                placeholder="值" onKeyDown={e => { if (e.key === "Enter") addIOC(); }} />
              <span className="btn btn--secondary btn--small" role="button" onClick={addIOC}>＋</span>
            </div>
            {(current.iocs ?? []).map((i, idx) => (
              <div key={idx} className="ndv-sec__ioc">
                <span className="ndv-sec__ioc-type">{i.type}</span>
                <span className="ndv-sec__ioc-value" title={i.value}>{i.value}</span>
                <span role="button" style={{ cursor: "pointer", opacity: 0.5 }} onClick={() => void save({ ...current, iocs: (current.iocs ?? []).filter((_, j) => j !== idx) })}>×</span>
              </div>
            ))}
          </>
        )}
      </div>

      {/* 主区：案例头部 + 向导 + 时间线 */}
      <div className="ndv-sec__main">
        {!current ? (
          <div className="ndv__empty" style={{ flex: 1 }}>
            <div className="ndv__empty-title">还没有案例</div>
            <div className="ndv__empty-desc">三条出生路径：左栏新建；「发现」页签的 Finding 卡点「建案例」；或直接用下方向导开一个入侵排查。</div>
          </div>
        ) : (
          <>
            <div className="ndv-sec__head">
              <span className="ndv-sec__title">{current.title}</span>
              <span className={`ndv-sec__st${current.status === "open" ? "" : " ndv-sec__st--closed"}`}
                role="button" onClick={() => void save({ ...current, status: current.status === "open" ? "closed" : "open" })}>
                {current.status === "open" ? "进行中" : "已关闭"}
              </span>
              <span style={{ marginLeft: "auto" }} />
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void app.NetDevCaseDelete(current.id).then(refresh)}>删除</span>
            </div>

            {/* 入侵排查向导（五步 checklist，§10.7） */}
            <div className="ndv-sec__wiz">
              <span className="ndv__meta" style={{ fontWeight: 700 }}>入侵排查向导</span>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">1</span>
                <span className="ndv__meta">圈定范围（{(current.devices ?? []).length} 台）</span>
                <span style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
                  {hosts.slice(0, 12).map(h => (
                    <span key={h.name} className={`ndv__chip${(current.devices ?? []).includes(h.name) ? " ndv-logwb__rel--on" : ""}`}
                      role="button" onClick={() => toggleDevice(h.name)}>{h.name}</span>
                  ))}
                </span>
              </div>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">2</span>
                <span className="btn btn--secondary btn--small" role="button" onClick={() => void wizTriage()}>{busy === "triage" ? "体检中…" : "并行主机体检"}</span>
                <span className="ndv__meta">异常自动进时间线</span>
              </div>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">3</span>
                <input className="mem-input" style={{ width: 200 }} value={wizPattern} onChange={e => setWizPattern(e.target.value)} placeholder="IOC / 关键字（正则）" />
                <span className="btn btn--secondary btn--small" role="button" onClick={() => void wizSearch()}>{busy === "search" ? "搜索中…" : "全网日志搜索"}</span>
                <span className="ndv__meta">命中前 10 条钉入时间线</span>
              </div>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">4</span>
                <input className="mem-input" style={{ flex: 1, minWidth: 120 }} value={noteText} onChange={e => setNoteText(e.target.value)} placeholder="人工笔记（时间线引用卡）" />
                <span className="btn btn--secondary btn--small" role="button" onClick={() => { if (noteText.trim()) { addEntry("note", "", noteText.trim()); setNoteText(""); } }}>钉入</span>
              </div>
              <div className="ndv-sec__wiz-row">
                <span className="ndv-sec__wiz-n">5</span>
                <span className="btn btn--primary btn--small" role="button" onClick={() => void wizExport()}>{busy === "export" ? "导出中…" : "导出复盘包"}</span>
                <span className="ndv__meta">案例 + 相关 Finding + 24h 变更（markdown）</span>
              </div>
            </div>

            {note && <div className="ndv__hint">{note}</div>}

            {/* 时间线：引用卡 */}
            <div className="ndv-sec__tl">
              {(current.entries ?? []).length === 0 && <div className="ndv__hint" style={{ padding: 8 }}>时间线为空——从向导第 2/3/4 步钉入第一批条目。</div>}
              {[...(current.entries ?? [])].reverse().map((e, i) => (
                <div key={i} className={`ndv-sec__entry ndv-sec__entry--${e.kind}`}>
                  <span className="ndv-sec__entry-dot" style={{ background: KIND_COLOR[e.kind] ?? "var(--fg-faint)" }} />
                  <span className="ndv-sec__entry-time">{String(e.time ?? "").replace("T", " ").slice(5, 19)}</span>
                  <span className="ndv-sec__entry-kind" style={{ color: KIND_COLOR[e.kind] ?? "var(--fg-faint)" }}>{KIND_LABEL[e.kind] ?? e.kind}{e.device ? `·${e.device}` : ""}</span>
                  <span className="ndv-sec__entry-text">{e.text}</span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
