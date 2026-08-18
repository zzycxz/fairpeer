import { type ReactNode, useCallback, useEffect, useState } from "react";
import { app } from "../lib/bridge";
import type { NetDevSettingsView, NetDevFinding, NetDevProposal, NetDevAuditEntryView } from "../lib/types";
import "../styles/netdev.css";

// NetDevLayout is the 运维 page's dedicated shell (NETDEV_SPEC §10.1): the
// diagnostic conversation runs in the center (the standard chat nodes are
// reused), with netdev-specific chrome around it — the read-only mode badge,
// the emergency stop, the inventory rail, the findings dock, and the
// audit/proposal status bar. All styling is scoped to .app--netdev; the
// coding/office views never see any of it.

const SEV_COLOR: Record<string, string> = { info: "#7ab8ff", warning: "#e0a800", critical: "#ff6b6b" };

export function NetDevLayout({
  headerNode,
  mainNode,
  footerNode,
  sessionsNode,
}: {
  headerNode: ReactNode;
  mainNode: ReactNode;
  footerNode: ReactNode;
  sessionsNode: ReactNode;
}) {
  const [settings, setSettings] = useState<NetDevSettingsView | null>(null);
  const [findings, setFindings] = useState<NetDevFinding[]>([]);
  const [proposals, setProposals] = useState<NetDevProposal[]>([]);
  const [audit, setAudit] = useState<NetDevAuditEntryView[]>([]);
  const [err, setErr] = useState("");

  const reload = useCallback(async () => {
    try {
      const [s, f, p, a] = await Promise.all([
        app.NetDevSettings(),
        app.NetDevFindings(),
        app.NetDevProposals(),
        app.NetDevAuditTail(200),
      ]);
      setSettings(s);
      setFindings(f ?? []);
      setProposals(p ?? []);
      setAudit(a ?? []);
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, []);

  useEffect(() => {
    void reload();
    const t = setInterval(() => void reload(), 30_000);
    return () => clearInterval(t);
  }, [reload]);

  const emergencyStop = useCallback(async () => {
    if (!confirm("紧急停止：立即断开全部设备连接（释放所有 VTY）？")) return;
    try {
      const n = await app.NetDevEmergencyStop();
      alert(`已断开 ${n} 个设备连接。`);
      void reload();
    } catch (e) {
      setErr(String(e));
    }
  }, [reload]);

  // Inventory grouped per configured group; ungrouped under "未分组".
  const groups = new Map<string, { name: string; address: string }[]>();
  for (const d of settings?.devices ?? []) {
    const g = d.group?.trim() || "未分组";
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g)!.push({ name: d.name, address: d.address });
  }

  // Today's audit heartbeat: read / write(=0 by construction) / proposals.
  const today = new Date().toISOString().slice(5, 10);
  const todayAudit = audit.filter(a => (a.time ?? "").slice(5, 10) === today);
  const readCount = todayAudit.filter(a => a.class === "read").length;
  const writeCount = todayAudit.filter(a => a.class === "write" || a.class === "proposal-write").length;
  const assessCount = todayAudit.filter(a => a.class === "assess").length;
  const pending = proposals.filter(p => p.status === "draft" || p.status === "approved" || p.status === "partial").length;

  return (
    <div className="ndv">
      <div className="ndv__top">
        <span className="ndv__badge">诊断 · 只读 🔒</span>
        <span className="ndv__stat">设备 {settings?.devices?.length ?? 0}</span>
        <span className="ndv__stat">跳板 {settings?.hops?.length ?? 0}</span>
        {err && <span className="ndv__stat" style={{ color: "#ff8787" }}>{err}</span>}
        <span className="ndv__stop" role="button" onClick={() => void emergencyStop()}>⏹ 紧急停止</span>
      </div>

      <div className="ndv__rail">
        <div className="ndv__sessions">{sessionsNode}</div>
        <div className="ndv__section">设备清单</div>
        {[...groups.entries()].map(([g, devices]) => (
          <div key={g} className="ndv__group">
            <div className="ndv__group-row"><span>▸ {g}</span><span>{devices.length}</span></div>
            {devices.map(d => (
              <div key={d.name} className="ndv__device"><span>{d.name}</span><span style={{ opacity: 0.6 }}>{d.address}</span></div>
            ))}
          </div>
        ))}
        {(settings?.hops?.length ?? 0) > 0 && (
          <>
            <div className="ndv__section">堡垒链</div>
            {(settings?.hops ?? []).map(h => (
              <div key={h.name} className="ndv__device"><span>{h.name}</span><span style={{ opacity: 0.6 }}>{h.host}</span></div>
            ))}
          </>
        )}
      </div>

      <div className="ndv__main">
        {headerNode}
        <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>{mainNode}</div>
        {footerNode}
      </div>

      <div className="ndv__dock">
        <div className="ndv__card">
          <div className="ndv__section" style={{ padding: "0 0 6px" }}>最近发现（{findings.length}）</div>
          {findings.length === 0 && <div style={{ opacity: 0.6 }}>暂无。诊断结论由 agent 通过 netdev_finding 记录（必带证据）；巡检结果也在此。</div>}
          {findings.slice(0, 6).map(f => (
            <div key={f.id} className="ndv__finding" style={{ "--sev": SEV_COLOR[f.severity] ?? SEV_COLOR.info } as React.CSSProperties}>
              <div className="ndv__finding-title">{f.title}</div>
              <div style={{ opacity: 0.65 }}>{(f.devices ?? []).join("、")} · 证据 {f.evidence?.length ?? 0} 条</div>
              {f.suggestion && <div style={{ color: "#e0a800", marginTop: 2 }}>建议：{f.suggestion}</div>}
            </div>
          ))}
        </div>
        <div className="ndv__card">
          <div className="ndv__section" style={{ padding: "0 0 6px" }}>提案队列</div>
          {proposals.length === 0 && <div style={{ opacity: 0.6 }}>暂无提案。对话中让 agent 用 netdev_propose 起草。</div>}
          {proposals.slice(0, 5).map(p => (
            <div key={p.id} className="ndv__finding" style={{ "--sev": p.status === "partial" || p.status === "failed" ? SEV_COLOR.critical : SEV_COLOR.warning } as React.CSSProperties}>
              <div className="ndv__finding-title">{p.id} · {p.status}</div>
              <div style={{ opacity: 0.65, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{p.intent}</div>
            </div>
          ))}
          <div style={{ opacity: 0.55, marginTop: 4 }}>批准 / 执行 / 回滚在 设置 → 运维 → 提案中心</div>
        </div>
      </div>

      <div className="ndv__bottom">
        <span className="ndv__bottom-item"><span className="ndv__ok">今日只读</span> {readCount}</span>
        <span className="ndv__bottom-item"><span className="ndv__ok">写操作(直连)</span> <span className="ndv__zero">{writeCount}</span></span>
        {assessCount > 0 && <span className="ndv__bottom-item">评估尝试 {assessCount}</span>}
        <span className="ndv__bottom-item">
          提案 {pending > 0 ? <span className="ndv__warn">{pending} 待处理</span> : <span className="ndv__zero">0</span>}
        </span>
        <span style={{ marginLeft: "auto", opacity: 0.5 }}>结构性只读：写命令无执行路径 · 全量审计中</span>
      </div>
    </div>
  );
}
