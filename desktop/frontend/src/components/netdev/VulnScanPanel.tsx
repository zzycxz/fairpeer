import { useMemo, useState, useSyncExternalStore } from "react";
import { app } from "../../lib/bridge";
import { useT } from "../../lib/i18n";
import type { NetDevFinding } from "../../lib/types";
import { isVulnScanSource, subscribeVulnScan, vulnScanSnapshot } from "../../lib/vulnScanState";

// VulnScanPanel — 右侧 dock「蓝队核查」页卡：对话驱动的 netdev-vulnscan
// 技能（source=vulnscan）与 CVE 扫查（cve:*）的发现实时落卡处。种子数据
// 来自布局传入的全量 findings（30s 轮询），实时尾部来自 vulnScanState 模块
// store（onNetdevFindingSaved 推送，页签未开也不丢）。指纹→候选→验证→立案
// 的过程在对话里看；这里只呈现结果与下一步动作（建案例/修复变更/跳发现）。

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
  // 覆盖（滚动更新），按时间新→旧，截断展示。
  const rows = useMemo(() => {
    const byId = new Map<string, NetDevFinding>();
    for (const f of findings) {
      if (isVulnScanSource((f as { source?: string }).source)) byId.set(f.id, f);
    }
    for (const f of scan.recent) byId.set(f.id, f);
    return [...byId.values()]
      .sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""))
      .slice(0, DISPLAY_CAP);
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

  return (
    <div className="ndv__card">
      <div className="ndv__card-title">{t("ndv.vs.title")}</div>
      <div style={{ display: "flex", gap: 4, marginBottom: 6, flexWrap: "wrap" }}>
        <span className="btn btn--primary btn--small" role="button" title={t("ndv.vs.startTip")} onClick={startChatScan}>{t("ndv.vs.start")}</span>
        <span className="btn btn--secondary btn--small" role="button" title={t("ndv.vs.sweepTip")} onClick={() => void runSweep()}>{sweepBusy ? t("ndv.vs.sweeping") : t("ndv.vs.sweep")}</span>
      </div>
      {sweepHint && <div className="ndv__hint" style={{ padding: "2px 0 6px" }}>{sweepHint}</div>}

      {rows.length === 0 ? (
        <div className="ndv__empty" style={{ flex: 1 }}>
          <div className="ndv__empty-title">{t("ndv.vs.emptyTitle")}</div>
          <div className="ndv__empty-desc">{t("ndv.vs.emptyDesc")}</div>
          <div className="ndv__pre" style={{ marginTop: 8, whiteSpace: "pre-wrap" }}>{t("ndv.vs.examplePrompt")}</div>
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 6, overflowY: "auto" }}>
          {rows.map(f => {
            const open = openId === f.id;
            const src = (f as { source?: string }).source ?? "";
            return (
              <div key={f.id} className="ndv__finding" style={{ ["--sev" as string]: SEV_COLOR[f.severity] ?? SEV_COLOR.info } as React.CSSProperties}>
                <div className="ndv__finding-title" role="button" onClick={() => setOpenId(open ? "" : f.id)}>
                  <span style={{ color: SEV_COLOR[f.severity] ?? SEV_COLOR.info, marginRight: 6 }}>{t(`ndv.sev.${f.severity || "info"}`)}</span>
                  {f.title}
                  <span style={{ fontWeight: 400, opacity: 0.6, marginLeft: 6 }}>{fmtTime(f.created_at)} {open ? "▲" : "▼"}</span>
                  {src === "vulnscan" && <span className="ndv__badge" style={{ marginLeft: 6 }}>{t("ndv.vs.srcChat")}</span>}
                  {src.startsWith("cve:") && <span className="ndv__badge" style={{ marginLeft: 6 }}>CVE</span>}
                </div>
                {open && (
                  <div style={{ display: "flex", flexDirection: "column", gap: 6, padding: "4px 0" }}>
                    {(f.devices ?? []).length > 0 && (
                      <div className="ndv__meta">{t("ndv.vs.devices")}: {(f.devices ?? []).join(", ")}</div>
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
                      <span className="btn btn--secondary btn--small" role="button" onClick={() => onInsertComposer?.(t("ndv.vs.proposePrompt", { title: f.title, id: f.id }))}>{t("ndv.vs.propose")}</span>
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
