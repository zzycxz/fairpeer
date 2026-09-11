import { useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import type { NetDevFinding } from "../../lib/types";
import { isVulnScanSource, subscribeVulnScan, vulnScanSnapshot } from "../../lib/vulnScanState";

// VulnScanPanel — 右侧 dock「蓝队核查」页卡：netdev-seccheck-auto sweep
// 子代理（source=vulnscan 数据标签）与 CVE 扫查（cve:*）的发现实时落卡处。种子数据
// 来自布局传入的全量 findings（30s 轮询），实时尾部来自 vulnScanState 模块
// store（onNetdevFindingSaved 推送，页签未开也不丢）。范围排序→单机闭环
// （指纹+暴露面→候选→验证→立案）的过程在对话里看；这里只呈现结果与
// 下一步动作（建案例/修复变更/跳发现）。

const SEV_COLOR: Record<string, string> = { info: "var(--accent)", warning: "var(--warn)", critical: "var(--danger)" };
const DISPLAY_CAP = 50;

function fmtTime(iso?: string): string {
  if (!iso) return "";
  try {
    const d = new Date(iso);
    return `${(d.getMonth() + 1).toString().padStart(2, "0")}-${d.getDate().toString().padStart(2, "0")} ${d.getHours().toString().padStart(2, "0")}:${d.getMinutes().toString().padStart(2, "0")}`;
  } catch {
    return "";
  }
}

export function VulnScanPanel({ findings, onInsertComposer }: {
  findings: NetDevFinding[];
  onInsertComposer?: (text: string) => void;
}) {
  const t = useT();
  const scan = useSyncExternalStore(subscribeVulnScan, vulnScanSnapshot);
  const [sweepBusy, setSweepBusy] = useState(false);
  const [sweepHint, setSweepHint] = useState("");
  const [openId, setOpenId] = useState("");

  // 合并视角：磁盘种子（findings 过滤）× 实时尾部（store 环）——同 ID 实时侧
  // 覆盖（滚动更新），按时间新→旧。实时尾同样按透镜过滤（vulnscan/cve:*），
  // 仅放行 segmap（段地图工件走实时落卡）——否则 baseline/syslog 等发现会
  // 滞留本面板整个会话，与「只呈现核查结果」的面板定位相悖。
  const merged = useMemo(() => {
    const byId = new Map<string, NetDevFinding>();
    for (const f of findings) {
      // 种子与实时尾同一判定：透镜来源 + segmap 工件（否则刷新后磁盘上的
      // 段地图卡被排除，置顶组整个消失）。
      const src = (f as { source?: string }).source ?? "";
      if (isVulnScanSource(src) || src === "segmap") byId.set(f.id, f);
    }
    for (const f of scan.recent) {
      const src = (f as { source?: string }).source ?? "";
      if (isVulnScanSource(src) || src === "segmap") byId.set(f.id, f);
    }
    return [...byId.values()].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""));
  }, [findings, scan.recent, scan.seq]);

  const runSweep = async () => {
    setSweepBusy(true);
    setSweepHint("");
    try {
      const f = await app.NetDevCVESweep();
      setSweepHint(f?.title ? t("ndv.vs.sweepDone", { title: f.title }) : t("ndv.vs.sweepEmpty"));
    } catch (e) {
      setSweepHint(String(e));
    } finally {
      setSweepBusy(false);
    }
  };

  const startChatScan = () => {
    onInsertComposer?.(t("ndv.vs.examplePrompt"));
  };

  // 批 1.5②：segmap 段地图工件组——source=segmap 的卡置顶折叠。地图组从
  // 截断前的全量取（DISPLAY_CAP 截掉最老的卡时，段地图不能跟着静默消失）；
  // 首到自动展开一次，兑现「地图先于队列看见」。
  const [segmapOpen, setSegmapOpen] = useState(false);
  const segmapSeen = useRef(false);
  const segmaps = useMemo(() => merged.filter(f => (f.source ?? "") === "segmap"), [merged]);
  const rest = useMemo(
    () => merged.filter(f => (f.source ?? "") !== "segmap").slice(0, DISPLAY_CAP),
    [merged],
  );
  useEffect(() => {
    if (!segmapSeen.current && segmaps.length > 0) {
      segmapSeen.current = true;
      setSegmapOpen(true);
    }
  }, [segmaps.length]);

  // 批 3①排查卡回填 + ③知识反哺：回填=把未纳管主机的输出贴进对话由 agent
  // 立案；回填知识=轮内确认的判据落 user-knowledge/（Validate 过才写，
  // 哈希入审计链），升级不冲掉。
  const [kbOpen, setKbOpen] = useState(false);
  const [kbId, setKbId] = useState("segment-priors");
  const [kbYaml, setKbYaml] = useState("");
  const [kbNote, setKbNote] = useState("");
  const knowledgeSave = async () => {
    if (!kbYaml.trim()) return;
    try {
      await app.NetDevKnowledgeSave(kbId, kbYaml);
      setKbNote(t("ndv.vs.kbSaved", { id: kbId }));
      setKbYaml("");
    } catch (e) { setKbNote(String(e)); }
  };

  const renderCard = (f: NetDevFinding) => {
    const open = openId === f.id;
    const src = (f as { source?: string }).source ?? "";
    const resolved = f.status === "resolved";
    return (
      <div key={f.id} className="ndv__finding" style={{ ["--sev" as string]: SEV_COLOR[f.severity] ?? SEV_COLOR.info, opacity: resolved ? 0.55 : undefined } as React.CSSProperties}>
        <div className="ndv__finding-title" role="button" onClick={() => setOpenId(open ? "" : f.id)}>
          <span style={{ color: SEV_COLOR[f.severity] ?? SEV_COLOR.info, marginRight: 6 }}>{t(`ndv.sev.${f.severity || "info"}`)}</span>
          {f.title}
          <span style={{ fontWeight: 400, opacity: 0.6, marginLeft: 6 }}>{fmtTime(f.created_at)} {open ? "▲" : "▼"}</span>
          {resolved && <span className="ndv__badge" style={{ marginLeft: 6 }}>{t("ndv.fnd.resolvedBadge")}</span>}
          {src === "vulnscan" && <span className="ndv__badge" style={{ marginLeft: 6 }}>{t("ndv.vs.srcChat")}</span>}
          {src.startsWith("cve:") && <span className="ndv__badge" style={{ marginLeft: 6 }}>CVE</span>}
        </div>
        {open && (
          <div style={{ display: "flex", flexDirection: "column", gap: 6, padding: "4px 0" }}>
            {(f.devices ?? []).length > 0 && (
              <div className="ndv__meta">{t("ndv.vs.devices")}: {(f.devices ?? []).join(", ")}</div>
            )}
            {f.fix && (
              <div className="ndv__meta">fix：{f.fix.type} → {f.fix.ref}{f.fix.confidence === "verified" ? " ✓" : " ⚠（须验证）"}{f.fix.link ? <> · <a href={f.fix.link} target="_blank" rel="noreferrer">{t("ndv.vs.fixRef")}</a></> : null}</div>
            )}
            {(f.detail ?? "") && <div className="ndv__pre" style={{ whiteSpace: "pre-wrap", maxHeight: 160, overflowY: "auto" }}>{f.detail}</div>}
            {(f.evidence ?? []).slice(0, 3).map((ev, i) => (
              <div key={i} className="ndv__pre" style={{ whiteSpace: "pre-wrap", maxHeight: 120, overflowY: "auto" }}>
                <span style={{ opacity: 0.6 }}>{ev.device} $ {ev.command}</span>{"\n"}{ev.output}
              </div>
            ))}
            <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => {
                window.dispatchEvent(new CustomEvent("fairpeer:netdev-case", { detail: { title: f.title, device: (f.devices ?? [])[0] ?? "", text: `${f.severity}｜${f.title}｜${(f.detail ?? "").slice(0, 120)}`, ref: f.id } }));
                window.dispatchEvent(new CustomEvent("fairpeer:netdev-bench", { detail: "sec" }));
              }}>{t("ndv.vs.case")}</span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => {
                window.dispatchEvent(new CustomEvent("fairpeer:netdev-open-screen", { detail: { tab: "findings", filter: `id:${f.id}` } }));
              }}>{t("ndv.vs.gotoFindings")}</span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => onInsertComposer?.(t("ndv.vs.proposePrompt", { title: f.title, id: f.id }) + (f.fix ? `（结构化修复建议：${f.fix.type} → ${f.fix.ref}${f.fix.confidence === "verified" ? "（已核实出处）" : "（模型推断，须验证）"}）` : ""))}>{t("ndv.vs.propose")}</span>
            </div>
          </div>
        )}
      </div>
    );
  };

  return (
    <div className="ndv__card">
      <div className="ndv__card-title">{t("ndv.vs.title")}</div>
      <div style={{ display: "flex", gap: 4, marginBottom: 6, flexWrap: "wrap" }}>
        <span className="btn btn--primary btn--small" role="button" title={t("ndv.vs.startTip")} onClick={startChatScan}>{t("ndv.vs.start")}</span>
        <span className="btn btn--secondary btn--small" role="button" title={t("ndv.vs.sweepTip")} onClick={() => void runSweep()}>{sweepBusy ? t("ndv.vs.sweeping") : t("ndv.vs.sweep")}</span>
        <span className="btn btn--secondary btn--small" role="button" title={t("ndv.vs.backfillTip")} onClick={() => onInsertComposer?.(t("ndv.vs.backfillPrompt"))}>{t("ndv.vs.backfill")}</span>
        <span className="btn btn--secondary btn--small" role="button" title={t("ndv.vs.kbTip")} onClick={() => { setKbOpen(o => !o); setKbNote(""); }}>{t("ndv.vs.kb")}</span>
      </div>
      {kbOpen && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4, marginBottom: 6 }}>
          <div style={{ display: "flex", gap: 4 }}>
            <select className="mem-select" style={{ flex: 1 }} value={kbId} onChange={e => { setKbId(e.target.value); setKbNote(""); }}>
              <option value="segment-priors">segment-priors（网段先验/职能信号）</option>
              <option value="credential-spots">credential-spots（凭据存放点）</option>
              <option value="host-risk-checks">host-risk-checks（本机风险检查）</option>
              <option value="baseline-rules">baseline-rules（基线规则表）</option>
            </select>
            <span className="btn btn--primary btn--small" role="button" onClick={() => void knowledgeSave()}>{t("ndv.vs.kbSave")}</span>
          </div>
          <textarea className="mem-input" rows={5} style={{ width: "100%", fontSize: 10.5, fontFamily: "var(--font-mono, monospace)" }}
            placeholder={t("ndv.vs.kbPh")} value={kbYaml} onChange={e => setKbYaml(e.target.value)} />
          {kbNote && <div className="ndv__hint" style={{ padding: "2px 0" }}>{kbNote}</div>}
        </div>
      )}
      {sweepHint && <div className="ndv__hint" style={{ padding: "2px 0 6px" }}>{sweepHint}</div>}

      {merged.length === 0 ? (
        <div className="ndv__empty" style={{ flex: 1 }}>
          <div className="ndv__empty-title">{t("ndv.vs.emptyTitle")}</div>
          <div className="ndv__empty-desc">{t("ndv.vs.emptyDesc")}</div>
          <div className="ndv__pre" style={{ marginTop: 8, whiteSpace: "pre-wrap" }}>{t("ndv.vs.examplePrompt")}</div>
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 6, overflowY: "auto" }}>
          {segmaps.length > 0 && (
            <div>
              <div className="ndv__card-title" role="button" style={{ fontSize: 11.5, cursor: "pointer" }} onClick={() => setSegmapOpen(o => !o)}>
                {t("ndv.vs.segmapGroup", { n: segmaps.length })} {segmapOpen ? "▲" : "▼"}
              </div>
              {segmapOpen && <div style={{ display: "flex", flexDirection: "column", gap: 6, marginTop: 4 }}>{segmaps.map(renderCard)}</div>}
            </div>
          )}
          {rest.map(renderCard)}
        </div>
      )}
    </div>
  );
}
