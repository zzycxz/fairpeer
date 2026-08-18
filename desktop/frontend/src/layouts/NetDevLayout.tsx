import { type ReactNode, useCallback, useEffect, useState } from "react";
import { app } from "../lib/bridge";
import type { NetDevSettingsView, NetDevFinding, NetDevProposal, NetDevAuditEntryView, NetDevTopologyGraph } from "../lib/types";
import "../styles/netdev.css";

// NetDevLayout is the 运维 page's shell (NETDEV_SPEC §10.1). Information
// architecture: the network NAME anchors the identity (like a coding
// workspace's project); the dock is TABBED (上下文/拓扑/发现/提案) so one
// thing is in focus at a time; zero devices shows a 3-step getting-started
// guide. Styling is scoped to .app--netdev / .ndv-*.

// NetdevTitleBar rides the global chrome's CENTER slot in netdev mode — the
// mode's identity bar replaces panel toggles and the profile switcher, so
// there is exactly one header (NETDEV_SPEC §10.1 sketch).
export function NetdevTitleBar() {
  const [name, setName] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    app.NetDevSettings()
      .then(s => setName(s?.networkName?.trim() || "我的网络"))
      .catch(e => setErr(String(e)));
  }, []);
  const stop = useCallback(async () => {
    if (!confirm("紧急停止：立即断开全部设备连接（释放所有 VTY）？")) return;
    try {
      const n = await app.NetDevEmergencyStop();
      alert(`已断开 ${n} 个设备连接。`);
    } catch (e) {
      setErr(String(e));
    }
  }, []);
  return (
    <div className="ndv__titlebar">
      <span className="ndv__netname" title="网络名称（设置 → 运维 可修改）">{name || "…"}</span>
      <span className="ndv__badge">诊断 · 只读 🔒</span>
      {err && <span className="ndv__stat" style={{ color: "#ff8787" }}>{err}</span>}
      <span className="ndv__stop" role="button" onClick={() => void stop()}>⏹ 紧急停止</span>
    </div>
  );
}

const SEV_COLOR: Record<string, string> = { info: "#7ab8ff", warning: "#e0a800", critical: "#ff6b6b" };

const QUICK_BATTERY: Record<string, string[]> = {
  huawei: ["display version", "display cpu-usage", "display interface brief"],
  cisco: ["show version", "show processes cpu", "show interfaces status"],
  zte: ["show version", "show processor cpu", "show interface brief"],
};

type QuickResult = { command: string; output: string; isError: boolean; refused?: string };
type DockTab = "context" | "topology" | "findings" | "proposals";

export function NetDevLayout({
  headerNode,
  mainNode,
  footerNode,
  sessionsNode,
  onOpenSettings,
}: {
  headerNode: ReactNode;
  mainNode: ReactNode;
  footerNode: ReactNode;
  sessionsNode: ReactNode;
  onOpenSettings: (tab: string) => void;
}) {
  const [settings, setSettings] = useState<NetDevSettingsView | null>(null);
  const [findings, setFindings] = useState<NetDevFinding[]>([]);
  const [proposals, setProposals] = useState<NetDevProposal[]>([]);
  const [audit, setAudit] = useState<NetDevAuditEntryView[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [quick, setQuick] = useState<Record<string, QuickResult>>({});
  const [topo, setTopo] = useState<NetDevTopologyGraph | null>(null);
  const [topoBusy, setTopoBusy] = useState(false);
  const [inspBusy, setInspBusy] = useState(false);
  const [tab, setTab] = useState<DockTab>("context");
  const [err, setErr] = useState(""); // shown in the global title bar

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

  const runQuick = useCallback(async (device: string, command: string) => {
    try {
      const r = await app.NetDevQuickExec(device, command);
      setQuick(q => ({ ...q, [command]: { command, output: r.refused ? (r.refusal ?? "已拒绝") : r.output, isError: !!r.refused || r.is_error, refused: r.refused ? "1" : undefined } }));
    } catch (e) {
      setQuick(q => ({ ...q, [command]: { command, output: String(e), isError: true } }));
    }
  }, []);

  const genTopology = useCallback(async () => {
    setTopoBusy(true);
    try {
      setTopo(await app.NetDevTopologySnapshot());
    } catch (e) {
      setErr(String(e));
    } finally {
      setTopoBusy(false);
    }
  }, []);

  const runInspection = useCallback(async () => {
    setInspBusy(true);
    try {
      const f = await app.NetDevRunInspection();
      if (f) alert(`巡检完成：${f.title}`);
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setInspBusy(false);
    }
  }, [reload]);

  const devices = settings?.devices ?? [];
  const selectedDevice = devices.find(d => d.name === selected);
  const lastInspection = findings.find(f => f.title.startsWith("巡检"));
  const pendingCount = proposals.filter(p => p.status === "draft" || p.status === "approved" || p.status === "partial").length;

  const groups = new Map<string, { name: string; address: string; vendor: string }[]>();
  for (const d of devices) {
    const g = d.group?.trim() || "未分组";
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g)!.push({ name: d.name, address: d.address, vendor: d.vendor });
  }

  const today = new Date().toISOString().slice(5, 10);
  const todayAudit = audit.filter(a => (a.time ?? "").slice(5, 10) === today);
  const readCount = todayAudit.filter(a => a.class === "read").length;
  const writeCount = todayAudit.filter(a => a.class === "write" || a.class === "proposal-write").length;

  const TABS: { key: DockTab; label: string; badge?: number }[] = [
    { key: "context", label: "上下文" },
    { key: "topology", label: "拓扑" },
    { key: "findings", label: "发现", badge: findings.length || undefined },
    { key: "proposals", label: "提案", badge: pendingCount || undefined },
  ];

  return (
    <div className="ndv">
      <div className="ndv__rail">
        <div className="ndv__sessions">{sessionsNode}</div>
        <div className="ndv__section">设备清单（{devices.length}，点击诊断）</div>
        {devices.length === 0 && (
          <div style={{ padding: "2px 12px 8px", opacity: 0.7, fontSize: 12 }}>
            还没有设备。右栏「开始使用」三步录入。
          </div>
        )}
        {[...groups.entries()].map(([g, list]) => (
          <div key={g} className="ndv__group">
            <div className="ndv__group-row"><span>▸ {g}</span><span>{list.length}</span></div>
            {list.map(d => (
              <div
                key={d.name}
                className="ndv__device ndv__device--click"
                style={selected === d.name ? { background: "var(--accent-dim, rgba(122,184,255,.15))" } : undefined}
                role="button"
                onClick={() => { setSelected(d.name); setTab("context"); setQuick({}); }}
              ><span>{d.name}</span><span style={{ opacity: 0.6 }}>{d.address}</span></div>
            ))}
          </div>
        ))}
        {(settings?.hops?.length ?? 0) > 0 && (
          <>
            <div className="ndv__section">堡垒链（{settings?.hops.length}）</div>
            {(settings?.hops ?? []).map(h => (
              <div key={h.name} className="ndv__device"><span>{h.name}</span><span style={{ opacity: 0.6 }}>{h.host}</span></div>
            ))}
          </>
        )}
        <div className="ndv__section">巡检</div>
        <div style={{ padding: "4px 12px" }}>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => void runInspection()}>
            {inspBusy ? "巡检中…" : "立即巡检"}
          </span>
          {lastInspection && (
            <div style={{ marginTop: 6, opacity: 0.6, fontSize: 12 }}>
              上次：{lastInspection.title}（{String(lastInspection.created_at ?? "").slice(5, 16).replace("T", " ")}）
            </div>
          )}
        </div>
      </div>

      <div className="ndv__main">
        {headerNode}
        <div style={{ flex: 1, minHeight: 0, overflow: "auto" }}>{mainNode}</div>
        {footerNode}
      </div>

      <div className="ndv__dock">
        {err && <div className="banner banner--error" style={{ marginBottom: 8 }}>{err}</div>}
        <div className="ndv__tabs" role="tablist">
          {TABS.map(t => (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={tab === t.key}
              className={`ndv__tab${tab === t.key ? " ndv__tab--active" : ""}`}
              onClick={() => setTab(t.key)}
            >
              {t.label}{t.badge ? <span className="ndv__tab-badge">{t.badge}</span> : null}
            </button>
          ))}
        </div>

        {tab === "context" && (
          devices.length === 0 ? <GettingStarted onOpenSettings={onOpenSettings} /> :
          selectedDevice ? (
            <div className="ndv__card">
              <div className="ndv__section" style={{ padding: "0 0 6px" }}>
                {selectedDevice.name} · {selectedDevice.vendor}/{selectedDevice.os}
              </div>
              <div style={{ opacity: 0.65, marginBottom: 8 }}>{selectedDevice.address} · {(selectedDevice.via ?? []).join("→") || "直连"}</div>
              <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginBottom: 8 }}>
                {(QUICK_BATTERY[selectedDevice.vendor] ?? ["display version"]).map(cmd => (
                  <span key={cmd} className="btn btn--secondary btn--small" role="button" onClick={() => void runQuick(selectedDevice.name, cmd)}>{cmd}</span>
                ))}
              </div>
              {Object.values(quick).map(r => (
                <div key={r.command} style={{ marginBottom: 8 }}>
                  <div style={{ fontWeight: 600, color: r.isError ? "#ff8787" : undefined }}>{r.command}</div>
                  <pre className="ndv__pre">{r.output || "（无输出）"}</pre>
                </div>
              ))}
            </div>
          ) : (
            <div className="ndv__card" style={{ opacity: 0.7 }}>点击左栏设备查看详情与快捷诊断（只读，走与 agent 相同的密封路径）；或在对话里直接描述故障现象。</div>
          )
        )}

        {tab === "topology" && (
          <div className="ndv__card">
            <div className="ndv__section" style={{ padding: "0 0 6px" }}>拓扑</div>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void genTopology()}>
              {topoBusy ? "采集邻居表中…" : "生成拓扑（读取全网邻居表）"}
            </span>
            {topo && <TopologyMap graph={topo} />}
          </div>
        )}

        {tab === "findings" && (
          <div className="ndv__card">
            <div className="ndv__section" style={{ padding: "0 0 6px" }}>发现（{findings.length}）</div>
            {findings.length === 0 && <div style={{ opacity: 0.6 }}>暂无。诊断结论由 agent 通过 netdev_finding 记录（必带证据）；巡检结果也在此。</div>}
            {findings.slice(0, 20).map(f => (
              <div key={f.id} className="ndv__finding" style={{ "--sev": SEV_COLOR[f.severity] ?? SEV_COLOR.info } as React.CSSProperties}>
                <div className="ndv__finding-title">{f.title}</div>
                <div style={{ opacity: 0.65 }}>{(f.devices ?? []).join("、")} · 证据 {f.evidence?.length ?? 0} 条</div>
                {f.suggestion && <div style={{ color: "#e0a800", marginTop: 2 }}>建议：{f.suggestion}</div>}
              </div>
            ))}
          </div>
        )}

        {tab === "proposals" && (
          <div className="ndv__card">
            <div className="ndv__section" style={{ padding: "0 0 6px" }}>提案（{proposals.length}）</div>
            {proposals.length === 0 && <div style={{ opacity: 0.6 }}>暂无。对话中让 agent 用 netdev_propose 起草。</div>}
            {proposals.slice(0, 10).map(p => <ProposalRow key={p.id} p={p} />)}
            <div style={{ opacity: 0.55, marginTop: 4 }}>批准 / 执行 / 回滚在 设置 → 运维 → 提案中心</div>
          </div>
        )}
      </div>

      <div className="ndv__bottom">
        <span className="ndv__bottom-item"><span className="ndv__ok">今日只读</span> {readCount}</span>
        <span className="ndv__bottom-item"><span className="ndv__ok">写操作(直连)</span> <span className="ndv__zero">{writeCount}</span></span>
        <span className="ndv__bottom-item">
          提案 {pendingCount > 0 ? <span className="ndv__warn">{pendingCount} 待处理</span> : <span className="ndv__zero">0</span>}
        </span>
        <span style={{ marginLeft: "auto", opacity: 0.5 }}>结构性只读：写命令无执行路径 · 全量审计中</span>
      </div>
    </div>
  );
}

// GettingStarted: the zero-device onboarding — the answer to "从哪里开始".
function GettingStarted({ onOpenSettings }: { onOpenSettings: (tab: string) => void }) {
  return (
    <div className="ndv__card">
      <div className="ndv__section" style={{ padding: "0 0 6px" }}>开始使用（三步）</div>
      <ol style={{ margin: 0, paddingLeft: 18, fontSize: 12, lineHeight: 2 }}>
        <li>
          录入设备：填写地址/厂商/登录凭证
          <span className="btn btn--primary btn--small" role="button" style={{ marginLeft: 8 }} onClick={() => onOpenSettings("netdev")}>
            打开 设置 → 运维
          </span>
        </li>
        <li>在设备编辑里点「测试连接」——首次会弹出主机密钥指纹，确认即信任（TOFU）</li>
        <li>回到本页：点左栏设备快捷诊断，或直接在对话里描述故障现象（如「core-sw-1 的 OSPF 邻居一直 down」）</li>
      </ol>
      <div style={{ marginTop: 8, opacity: 0.6, fontSize: 11.5 }}>
        网络名称、探测范围、提案审批策略都在 设置 → 运维。写操作永远走人工审批的提案，agent 只有只读权限。
      </div>
    </div>
  );
}

// ProposalRow: one queue row, expandable to steps (commands + rollback plan).
function ProposalRow({ p }: { p: NetDevProposal }) {
  const [open, setOpen] = useState(false);
  const sev = p.status === "partial" || p.status === "failed" ? SEV_COLOR.critical : SEV_COLOR.warning;
  return (
    <div className="ndv__finding" style={{ "--sev": sev } as React.CSSProperties}>
      <div className="ndv__finding-title" role="button" onClick={() => setOpen(!open)}>
        {p.id} · {p.status} {open ? "▲" : "▼"}
      </div>
      <div style={{ opacity: 0.65, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{p.intent}</div>
      {open && (
        <div style={{ marginTop: 4 }}>
          {(p.steps ?? []).map((s, i) => (
            <div key={i} style={{ marginLeft: 8, marginBottom: 4 }}>
              <div>{s.device} — {s.applied ? "✅ 已下发" : s.error ? "❌ " + s.error : "⬜ 未执行"}</div>
              <div style={{ opacity: 0.7 }}>变更：{(s.commands ?? []).join("；")}</div>
              <div style={{ opacity: 0.7 }}>回滚：{(s.rollback ?? []).join("；") || "（无）"}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// TopologyMap: deterministic circular layout (hierarchical layout arrives
// with the flowchart-engine integration).
function TopologyMap({ graph }: { graph: NetDevTopologyGraph }) {
  const W = 270, H = 190, R = Math.min(W, H) / 2 - 22;
  const n = graph.nodes.length;
  const pos = (i: number) => ({
    x: W / 2 + R * Math.cos((2 * Math.PI * i) / Math.max(n, 1) - Math.PI / 2),
    y: H / 2 + R * Math.sin((2 * Math.PI * i) / Math.max(n, 1) - Math.PI / 2),
  });
  const idx = new Map(graph.nodes.map((v, i) => [v.name, i]));
  return (
    <div>
      <svg width={W} height={H} style={{ maxWidth: "100%", marginTop: 8 }}>
        {graph.edges.map((e, i) => {
          const a = idx.get(e.local_device), b = idx.get(e.remote_device);
          if (a === undefined || b === undefined) return null;
          const pa = pos(a), pb = pos(b);
          return <line key={i} x1={pa.x} y1={pa.y} x2={pb.x} y2={pb.y} stroke="var(--border)" strokeWidth={1} />;
        })}
        {graph.nodes.map((v, i) => {
          const p = pos(i);
          return (
            <g key={v.name}>
              <circle cx={p.x} cy={p.y} r={5.5} fill={v.managed ? "var(--accent, #7ab8ff)" : "var(--text-dim, #888)"} />
              <text x={p.x} y={p.y - 9} textAnchor="middle" fontSize={8.5} fill="var(--text, #ccc)">
                {v.name.length > 10 ? v.name.slice(0, 10) + "…" : v.name}
              </text>
            </g>
          );
        })}
      </svg>
      <div style={{ opacity: 0.6, fontSize: 11 }}>
        {graph.nodes.filter(v => v.managed).length} 纳管 · {graph.nodes.filter(v => !v.managed).length} 未纳管（灰，不可连） · {graph.edges.length} 链路 · {graph.at}
      </div>
    </div>
  );
}
