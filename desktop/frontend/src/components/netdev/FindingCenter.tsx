import { useCallback, useEffect, useState } from "react";
import { app } from "../../lib/bridge";
import type { NetDevFinding } from "../../lib/types";

// FindingCenter renders diagnosis conclusions with their evidence chains —
// the review surface for "它帮我找到了问题". Severity colors the row; each
// finding expands to the exact commands and outputs that back it.

const SEV_STYLE: Record<string, { label: string; color: string }> = {
  info: { label: "ℹ 提示", color: "#7ab8ff" },
  warning: { label: "⚠ 警告", color: "#e0a800" },
  critical: { label: "🔴 严重", color: "#ff6b6b" },
};

export function FindingCenter() {
  const [items, setItems] = useState<NetDevFinding[]>([]);
  const [err, setErr] = useState("");
  const [openID, setOpenID] = useState("");

  const reload = useCallback(async () => {
    try {
      const list = await app.NetDevFindings();
      setItems(list ?? []);
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, []);

  useEffect(() => { void reload(); }, [reload]);

  return (
    <div>
      <div className="set-label" style={{ margin: "14px 0 6px" }}>诊断发现（{items.length}）</div>
      {err && <div className="banner banner--error" style={{ marginBottom: 6 }}>{err}</div>}
      {items.length === 0 && (
        <div className="mem-hint">暂无发现。诊断结论由 agent 通过 netdev_finding 记录，必须附带命令证据；巡检结果也存档于此。</div>
      )}
      {items.map(f => {
        const sev = SEV_STYLE[f.severity] ?? SEV_STYLE.info;
        return (
          <div key={f.id} className="mem-hint" style={{ borderLeft: `3px solid ${sev.color}`, padding: "6px 10px", marginBottom: 6, background: "var(--bg, #1e222a)" }}>
            <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
              <span style={{ color: sev.color, minWidth: 56 }}>{sev.label}</span>
              <span style={{ flex: 1, fontWeight: 600 }} title={f.detail}>{f.title}</span>
              <span style={{ opacity: 0.6 }}>{(f.devices ?? []).join(", ")}</span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => setOpenID(openID === f.id ? "" : f.id)}>
                证据 {f.evidence?.length ?? 0} {openID === f.id ? "▲" : "▼"}
              </span>
            </div>
            {openID === f.id && (
              <div style={{ marginTop: 8 }}>
                {f.detail && <div style={{ marginBottom: 6, opacity: 0.85 }}>{f.detail}</div>}
                {(f.evidence ?? []).map((e, i) => (
                  <div key={i} style={{ marginBottom: 6, marginLeft: 12 }}>
                    <div style={{ opacity: 0.8 }}>{e.device} ▸ <code>{e.command}</code></div>
                    <pre style={{ margin: "4px 0 0", padding: 8, background: "rgba(0,0,0,.25)", borderRadius: 6, maxHeight: 160, overflow: "auto", fontSize: 12 }}>{e.output}</pre>
                  </div>
                ))}
                {f.suggestion && (
                  <div style={{ marginLeft: 12, marginTop: 6, color: "var(--text-warn, #e0a800)" }}>
                    建议：{f.suggestion}（可让 agent 用 netdev_propose 起草提案）
                  </div>
                )}
                <div style={{ marginLeft: 12, marginTop: 4, opacity: 0.5 }}>
                  {String(f.created_at ?? "").slice(0, 19).replace("T", " ")}
                </div>
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
