import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
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

// Read-only "grab the running config" command per vendor — the first step of
// any 配置 task (backup / diff / proposal drafting).
const CFG_CMD: Record<string, string> = {
  huawei: "display current-configuration",
  cisco: "show running-config",
  zte: "show running-config",
};

type QuickResult = { command: string; output: string; isError: boolean; refused?: string };
type DockTab = "context" | "topology" | "findings" | "proposals";

export function NetDevLayout({
  mainNode,
  footerNode,
  sessionsNode,
  onOpenSettings,
  onInsertComposer,
}: {
  mainNode: ReactNode;
  footerNode: ReactNode;
  sessionsNode: ReactNode;
  onOpenSettings: (tab: string) => void;
  // Fills the chat composer with a starter prompt (the AI-配置 entry point on
  // the device card / topology nodes). Optional: card hides the button if absent.
  onInsertComposer?: (text: string) => void;
}) {
  const [settings, setSettings] = useState<NetDevSettingsView | null>(null);
  const [findings, setFindings] = useState<NetDevFinding[]>([]);
  const [proposals, setProposals] = useState<NetDevProposal[]>([]);
  const [audit, setAudit] = useState<NetDevAuditEntryView[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [quick, setQuick] = useState<Record<string, QuickResult>>({});
  const [topo, setTopo] = useState<NetDevTopologyGraph | null>(null);
  const [topoBusy, setTopoBusy] = useState(false);
  const [topoNotice, setTopoNotice] = useState("");
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

  // Topology node names come from LLDP sysnames, which need not equal the
  // configured device names — match by name first, then by management IP.
  const devicesRef = useRef<NetDevSettingsView["devices"]>([]);
  devicesRef.current = settings?.devices ?? [];
  const pickFromTopo = useCallback((name: string) => {
    const node = topo?.nodes.find(v => v.name === name);
    const hit = devicesRef.current.find(d => d.name === name || (node?.device_ip && d.address === node.device_ip));
    if (hit) {
      setSelected(hit.name);
      setTab("context");
      setTopoNotice("");
      return;
    }
    setTopoNotice(`「${name}」不在设备清单——邻居表发现的节点（未纳管或无凭证），到 设置 → 运维 录入后即可点击诊断。`);
  }, [topo]);

  // Entering the 拓扑 tab auto-generates once (per mount) when devices exist,
  // so the map is simply there after a probe instead of hiding behind a click.
  const topoAutoFired = useRef(false);
  useEffect(() => {
    if (tab === "topology" && !topoAutoFired.current && !topo && !topoBusy && (settings?.devices?.length ?? 0) > 0) {
      topoAutoFired.current = true;
      void genTopology();
    }
  }, [tab, topo, topoBusy, settings, genTopology]);

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
        <div className="ndv__section">诊断会话</div>
        <div className="ndv__sessions">{sessionsNode}</div>
        <div className="ndv__section">设备清单（{devices.length}，点击诊断）</div>
        {devices.length === 0 && (
          <div className="ndv__hint">还没有设备。右栏「开始使用」三步录入。</div>
        )}
        {[...groups.entries()].map(([g, list]) => (
          <div key={g} className="ndv__group">
            <div className="ndv__group-row"><span>▸ {g}</span><span>{list.length}</span></div>
            {list.map(d => (
              <div
                key={d.name}
                className={`ndv__device ndv__device--click${selected === d.name ? " ndv__device--sel" : ""}`}
                role="button"
                onClick={() => { setSelected(d.name); setTab("context"); setQuick({}); }}
              ><span className="ndv__device-name">{d.name}</span><span className="ndv__device-addr">{d.address}</span></div>
            ))}
          </div>
        ))}
        {(settings?.hops?.length ?? 0) > 0 && (
          <>
            <div className="ndv__section">堡垒链（{settings?.hops.length}）</div>
            {(settings?.hops ?? []).map(h => (
              <div key={h.name} className="ndv__device"><span className="ndv__device-name">{h.name}</span><span className="ndv__device-addr">{h.host}</span></div>
            ))}
          </>
        )}
        <div className="ndv__section">巡检</div>
        <div className="ndv__rail-actions">
          <span className="btn btn--secondary btn--small" role="button" onClick={() => void runInspection()}>
            {inspBusy ? "巡检中…" : "立即巡检"}
          </span>
        </div>
        {lastInspection && (
          <div className="ndv__hint">上次：{lastInspection.title}（{String(lastInspection.created_at ?? "").slice(5, 16).replace("T", " ")}）</div>
        )}
      </div>

      <div className="ndv__main">
        <div className="ndv__chat">{mainNode}</div>
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
              <div className="ndv__card-title">{selectedDevice.name} · {selectedDevice.vendor}/{selectedDevice.os}</div>
              <div className="ndv__meta">{selectedDevice.address} · {(selectedDevice.via ?? []).join("→") || "直连"}</div>
              <div className="ndv__quick-cmds">
                {(QUICK_BATTERY[selectedDevice.vendor] ?? ["display version"]).map(cmd => (
                  <span key={cmd} className="btn btn--secondary btn--small" role="button" onClick={() => void runQuick(selectedDevice.name, cmd)}>{cmd}</span>
                ))}
              </div>
              <div className="ndv__quick-cmds">
                <span
                  className="btn btn--secondary btn--small"
                  role="button"
                  onClick={() => void runQuick(selectedDevice.name, CFG_CMD[selectedDevice.vendor] ?? "display current-configuration")}
                >抓取当前配置</span>
                {onInsertComposer && (
                  <span
                    className="btn btn--primary btn--small"
                    role="button"
                    onClick={() => onInsertComposer(`帮我在 ${selectedDevice.name}（${selectedDevice.address}）上完成以下变更：\n变更内容：\n（请先用只读命令确认现状，再用 netdev_propose 起草提案——写命令必须包含对应的回滚命令，不会直接执行，需我在提案中心批准。）`)}
                  >AI 配置变更…</span>
                )}
              </div>
              {Object.values(quick).map(r => (
                <div key={r.command} className="ndv__quick-result">
                  <div className="ndv__quick-cmd" style={r.isError ? { color: "#ff8787" } : undefined}>{r.command}</div>
                  <pre className="ndv__pre">{r.output || "（无输出）"}</pre>
                </div>
              ))}
            </div>
          ) : (
            <div className="ndv__card ndv__card--dim">点击左栏设备查看详情与快捷诊断（只读，走与 agent 相同的密封路径）；或在对话里直接描述故障现象。</div>
          )
        )}

        {tab === "topology" && (
          <div className="ndv__card">
            <div className="ndv__card-title">拓扑</div>
            <div className="ndv__rail-actions" style={{ padding: 0, marginBottom: 8 }}>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void genTopology()}>
                {topoBusy ? "采集邻居表中…" : "生成拓扑（读取全网邻居表）"}
              </span>
            </div>
            {topoNotice && (
              <div className="ndv__hint" style={{ padding: 0, marginBottom: 8, color: "var(--accent, #7ab8ff)" }}>{topoNotice}</div>
            )}
            {topo && <TopologyMap graph={topo} selected={selected} selectedAddr={selectedDevice?.address} onPick={pickFromTopo} />}
            {!topo && !topoBusy && (
              <div className="ndv__hint" style={{ padding: 0 }}>
                生成拓扑 = 对全部纳管设备执行只读的 LLDP/CDP 邻居表读取，按连接度自动分层为核心 / 汇聚 / 接入；未纳管邻居沉底（虚线框）。
              </div>
            )}
          </div>
        )}

        {tab === "findings" && (
          <div className="ndv__card">
            <div className="ndv__card-title">发现（{findings.length}）</div>
            {findings.length === 0 && <div className="ndv__hint" style={{ padding: 0 }}>暂无。诊断结论由 agent 通过 netdev_finding 记录（必带证据）；巡检结果也在此。</div>}
            {findings.slice(0, 20).map(f => (
              <div key={f.id} className="ndv__finding" style={{ "--sev": SEV_COLOR[f.severity] ?? SEV_COLOR.info } as React.CSSProperties}>
                <div className="ndv__finding-title">{f.title}</div>
                <div className="ndv__meta">{(f.devices ?? []).join("、")} · 证据 {f.evidence?.length ?? 0} 条</div>
                {f.suggestion && <div className="ndv__finding-suggestion">建议：{f.suggestion}</div>}
              </div>
            ))}
          </div>
        )}

        {tab === "proposals" && (
          <div className="ndv__card">
            <div className="ndv__card-title">提案（{proposals.length}）</div>
            {proposals.length === 0 && <div className="ndv__hint" style={{ padding: 0 }}>暂无。对话中让 agent 用 netdev_propose 起草。</div>}
            {proposals.slice(0, 10).map(p => <ProposalRow key={p.id} p={p} />)}
            <div className="ndv__hint" style={{ padding: 0 }}>批准 / 执行 / 回滚在 设置 → 运维 → 提案中心</div>
          </div>
        )}
      </div>

      <div className="ndv__bottom">
        <span className="ndv__bottom-item"><span className="ndv__ok">今日只读</span> {readCount}</span>
        <span className="ndv__bottom-item"><span className="ndv__ok">写操作(直连)</span> <span className="ndv__zero">{writeCount}</span></span>
        <span className="ndv__bottom-item">
          提案 {pendingCount > 0 ? <span className="ndv__warn">{pendingCount} 待处理</span> : <span className="ndv__zero">0</span>}
        </span>
        <span className="ndv__bottom-note">结构性只读：写命令无执行路径 · 全量审计中</span>
      </div>
    </div>
  );
}

// GettingStarted: the zero-device onboarding — the answer to "从哪里开始".
function GettingStarted({ onOpenSettings }: { onOpenSettings: (tab: string) => void }) {
  const steps: { text: string; action?: { label: string; run: () => void } }[] = [
    { text: "录入设备：地址 / 厂商 / 登录凭证", action: { label: "打开 设置 → 运维", run: () => onOpenSettings("netdev") } },
    { text: "设备编辑里点「测试连接」——首次弹出主机密钥指纹，确认即信任（TOFU）" },
    { text: "左栏点设备快捷诊断，或在对话里描述故障现象（如「core-sw-1 的 OSPF 邻居一直 down」）" },
  ];
  return (
    <div className="ndv__card ndv__gs">
      <div className="ndv__card-title">开始使用（三步）</div>
      {steps.map((s, i) => (
        <div key={i} className="ndv__step">
          <span className="ndv__step-n">{i + 1}</span>
          <div className="ndv__step-body">
            {s.text}
            {s.action && (
              <span className="btn btn--primary btn--small ndv__step-btn" role="button" onClick={s.action.run}>{s.action.label}</span>
            )}
          </div>
        </div>
      ))}
      <div className="ndv__hint" style={{ padding: 0, marginTop: 4 }}>
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
      <div className="ndv__meta ndv__ellipsis">{p.intent}</div>
      {open && (
        <div style={{ marginTop: 4 }}>
          {(p.steps ?? []).map((s, i) => (
            <div key={i} className="ndv__step-detail">
              <div>{s.device} — {s.applied ? "✅ 已下发" : s.error ? "❌ " + s.error : "⬜ 未执行"}</div>
              <div className="ndv__meta">变更：{(s.commands ?? []).join("；")}</div>
              <div className="ndv__meta">回滚：{(s.rollback ?? []).join("；") || "（无）"}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// TopologyMap: tiered layout — connection degree infers 核心/汇聚/接入 bands
// (real ops shape), unmanaged neighbors sink to the bottom in grey. Ports ride
// the link as a hover tooltip; clicking a managed node selects the device
// (onPick); `selected` highlights the picked node.
function TopologyMap({ graph, selected, selectedAddr, onPick }: { graph: NetDevTopologyGraph; selected?: string; selectedAddr?: string; onPick?: (name: string) => void }) {
  const W = 520, H = 360;
  // Degree per node (managed nodes only drive tiering).
  const degree = new Map<string, number>();
  for (const n of graph.nodes) degree.set(n.name, 0);
  for (const e of graph.edges) {
    if (e.local_device !== e.remote_device) {
      degree.set(e.local_device, (degree.get(e.local_device) ?? 0) + 1);
      degree.set(e.remote_device, (degree.get(e.remote_device) ?? 0) + 1);
    }
  }
  const managed = graph.nodes.filter(v => v.managed);
  // Tier thresholds relative to the busiest node: core ≥ 60% of max degree,
  // aggregation ≥ 30%, rest access. Managed nodes without links join access.
  const maxDeg = Math.max(1, ...managed.map(v => degree.get(v.name) ?? 0));
  const tierOf = (name: string): number => {
    const d = degree.get(name) ?? 0;
    if (d >= maxDeg * 0.6) return 0;
    if (d >= maxDeg * 0.3) return 1;
    return 2;
  };
  // Unmanaged neighbors get their own bottom band (tier 3).
  const bands: string[][] = [[], [], [], []];
  for (const v of graph.nodes) bands[v.managed ? tierOf(v.name) : 3].push(v.name);
  const pos = new Map<string, { x: number; y: number }>();
  const bandY = [46, 148, 250, 322];
  bands.forEach((band, bi) => {
    const n = band.length;
    band.forEach((name, i) => {
      pos.set(name, { x: n === 1 ? W / 2 : 36 + ((W - 72) * i) / Math.max(n - 1, 1), y: bandY[bi] });
    });
  });
  const node = (name: string) => graph.nodes.find(v => v.name === name);
  const TIER_LABEL = ["核心层", "汇聚层", "接入层", "未纳管邻居"];
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: "100%", maxWidth: 640, marginTop: 8, display: "block" }}>
        {bandY.map((y, i) =>
          bands[i].length > 0 ? (
            <text key={i} x={10} y={y - 20} fontSize={10} fill="var(--text-dim, #888)" opacity={0.8}>
              {TIER_LABEL[i]}
            </text>
          ) : null,
        )}
        {graph.edges.map((e, i) => {
          const pa = pos.get(e.local_device), pb = pos.get(e.remote_device);
          if (!pa || !pb) return null;
          return (
            <line
              key={i}
              x1={pa.x} y1={pa.y} x2={pb.x} y2={pb.y}
              stroke="var(--border, #555)"
              strokeWidth={1}
              opacity={0.85}
            >
              <title>{`${e.local_device}:${e.local_port} ⇄ ${e.remote_device}${e.remote_port ? ":" + e.remote_port : ""} (${e.source})`}</title>
            </line>
          );
        })}
        {graph.nodes.map(v => {
          const p = pos.get(v.name);
          if (!p) return null;
          const tier = v.managed ? tierOf(v.name) : 3;
          const nv = node(v.name);
          const sel = !!v.managed && (!!selected && (v.name === selected || nv?.device_ip === selectedAddr));
          return (
            <g
              key={v.name}
              style={{ cursor: v.managed && onPick ? "pointer" : "default" }}
              onClick={() => v.managed && onPick?.(v.name)}
            >
              <rect
                x={p.x - 30} y={p.y - 10} width={60} height={20} rx={5}
                fill={sel
                  ? "var(--accent-dim, rgba(122,184,255,.2))"
                  : v.managed ? "var(--bg-elev, #2a2a2e)" : "transparent"}
                stroke={sel
                  ? "var(--accent, #7ab8ff)"
                  : v.managed ? (tier === 0 ? "var(--accent, #7ab8ff)" : "var(--border, #555)") : "var(--border, #555)"}
                strokeWidth={sel ? 2 : v.managed && tier === 0 ? 1.6 : 1}
                strokeDasharray={v.managed ? undefined : "3 3"}
              />
              <text x={p.x} y={p.y + 3.5} textAnchor="middle" fontSize={9} fill={v.managed ? "var(--text, #ccc)" : "var(--text-dim, #888)"}>
                {v.name.length > 9 ? v.name.slice(0, 9) + "…" : v.name}
              </text>
              <title>{`${v.name}${nv?.device_ip ? " · " + nv.device_ip : ""}${v.managed ? " · 纳管" : " · 未纳管"} · 连接 ${degree.get(v.name) ?? 0}${v.managed && onPick ? " · 点击查看设备" : ""}`}</title>
            </g>
          );
        })}
      </svg>
      <div style={{ opacity: 0.6, fontSize: 11 }}>
        {graph.nodes.filter(v => v.managed).length} 纳管 · {graph.nodes.filter(v => !v.managed).length} 未纳管（虚线框,不可连） · {graph.edges.length} 链路 · 悬停链路看端口 · {graph.at}
      </div>
    </div>
  );
}
