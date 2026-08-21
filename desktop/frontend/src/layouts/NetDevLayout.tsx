import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { AlertTriangle, BookOpen, ClipboardCheck, Network, PanelLeft, ScanSearch, ScrollText, Server, SlidersHorizontal, SquarePen } from "lucide-react";
import { app } from "../lib/bridge";
import { WorkspacePill } from "../components/WorkspacePill";
import { useConfirm } from "../lib/confirm";
import { getActiveProject, setActiveProject, subscribeActiveProject, type NetDevProjectScope } from "../lib/netdevProjectStore";
import { ProposalActions } from "../components/netdev/ProposalCenter";
import { DockTabs, useDockTabState } from "../components/DockTabs";
import type { NetDevSettingsView, NetDevFinding, NetDevProposal, NetDevAuditEntryView, NetDevTopologyGraph, NetDevBackupVersion } from "../lib/types";
import { Markdown } from "../components/Markdown";
import "../styles/netdev.css";

// NetDevLayout is the 运维 page's shell (NETDEV_SPEC §10.1). The FRAME follows
// the coding view exactly: a full-height left rail (spans the chrome row) with
// the shared sidebar__brandrow at the top, the global AppChrome over the main
// area (title bar in its center slot + the standard right control cluster),
// and a tabbed right dock. Styling is scoped to .app--netdev / .ndv-*.

// NetdevTitleBar rides the global chrome's CENTER slot in netdev mode. To keep
// the header identical to the coding view's, it LEADS with the same topicbar
// (session title · rename · workspace subtitle, supplied by App) and appends
// the 运维-specific chips after it — title on the left, mode bits on the right.
export function NetdevTitleBar({ leading, onOpenSettings }: { leading?: ReactNode; onOpenSettings?: (tab: string) => void }) {
  const [name, setName] = useState("");
  const [projects, setProjects] = useState<{ name: string; groups: string[]; note?: string }[]>([]);
  const [menuOpen, setMenuOpen] = useState(false);
  const [active, setActive] = useState<{ name: string; groups: string[] } | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => {
    app.NetDevSettings()
      .then(s => {
        setName(s?.networkName?.trim() || "我的网络");
        setProjects((s?.projects ?? []).map((p: any) => ({ ...p, groups: p.groups ?? [] })));
      })
      .catch(e => setErr(String(e)));
    const sync = () => setActive(getActiveProject());
    sync();
    return subscribeActiveProject(sync);
  }, []);
  const confirm = useConfirm();
  const stop = useCallback(async () => {
    const ok = await confirm({
      title: "SYSTEM HALT",
      message: "中止所有挂起的运维任务、断开代理会话并锁定守护进程？",
      danger: true,
      confirmLabel: "立即中止",
    });
    if (!ok) return;
    try {
      await app.NetDevEmergencyStop();
      setErr("[SYS] STOP SIGNAL SENT");
    } catch (e) {
      setErr(String(e));
    }
  }, [confirm]);
  return (
    <div className="ndv__titlebar">
      {leading}
      <span className="ndv__netname" title="网络名称（设置 → 运维 可修改）">{name || "…"}</span>
      {projects.length > 0 && (
        <span
          className="ndv__project"
          role="button"
          title={active ? `当前项目：${active.name}（设备组：${active.groups.join("、")}）` : "当前范围：全部设备。点击切换项目（站点/机房）。"}
          onClick={() => setMenuOpen(o => !o)}
        >项目：{active ? active.name : "全部"} ▾</span>
      )}
      {menuOpen && (
        <span className="ndv__project-menu" role="menu">
          <span role="menuitem" onClick={() => { setActiveProject(null); setMenuOpen(false); }}>全部设备</span>
          {projects.map(p => (
            <span key={p.name} role="menuitem" title={p.note || `设备组：${p.groups.join("、")}`}
              onClick={() => { setActiveProject({ name: p.name, groups: p.groups }); setMenuOpen(false); }}>
              {p.name}{active?.name === p.name ? " ✓" : ""}
            </span>
          ))}
          <span role="menuitem" onClick={() => { setMenuOpen(false); onOpenSettings?.("netdev"); }}>管理项目…</span>
        </span>
      )}
      <span
        className="ndv__badge"
        role="button"
        title="安全策略入口：结构性只读（无开关，地板）· 设备组策略（只读/提案/双确认）· 提案审批与变更窗口 · 安全评估 engagement · 点击打开设置"
        onClick={() => onOpenSettings?.("netdev")}
      >[ 诊断·只读 ]</span>
      {err && <span className="ndv__stat" style={{ color: "var(--err)" }}>{err}</span>}
      <span className="ndv__stop" role="button" onClick={() => void stop()}>⏹ 紧急停止</span>
    </div>
  );
}

// Severity colors ride the THEME TOKENS (accent/warn/danger) — never literal
// hexes, so every theme reads consistently (user direction: 配色跟随全局).
const SEV_COLOR: Record<string, string> = { info: "var(--accent)", warning: "var(--warn)", critical: "var(--danger)" };

const QUICK_BATTERY: Record<string, string[]> = {
  huawei: ["display version", "display cpu-usage", "display interface brief"],
  cisco: ["show version", "show processes cpu", "show interfaces status"],
  zte: ["show version", "show processor cpu", "show interface brief"],
  vmware: ["esxcli system version get", "esxcli network nic list", "esxcfg-vswitch -l", "vim-cmd vmsvc/getallvms"],
  linux: ["ip addr", "ss -tlnp", "systemctl --failed", "df -h", "cat /proc/net/dev"],
  windows: ["Get-NetAdapter", "Get-NetTCPConnection", "Get-Service", "systeminfo"],
};

// SNMP quick queries for the device card (vendor=snmp): label → (oid, mode).
const SNMP_QUICK: { label: string; oid: string; mode: string }[] = [
  { label: "系统描述", oid: "1.3.6.1.2.1.1.1.0", mode: "get" },
  { label: "运行时长", oid: "1.3.6.1.2.1.1.3.0", mode: "get" },
  { label: "接口状态表", oid: "1.3.6.1.2.1.2.2.1.8", mode: "walk" },
  { label: "接口流量计数", oid: "1.3.6.1.2.1.31.1.1.1.6", mode: "walk" },
];

// Read-only "grab the running config" command per vendor — the first step of
// any 配置 task (backup / diff / proposal drafting).
const CFG_CMD: Record<string, string> = {
  huawei: "display current-configuration",
  cisco: "show running-config",
  zte: "show running-config",
  vmware: "esxcli system version get",
};

// BMC quick queries for the device card (vendor=redfish): label → resource.
const REDFISH_QUICK: { label: string; path: string }[] = [
  { label: "BMC 概览", path: "/redfish/v1/Managers" },
  { label: "系统/硬件清单", path: "/redfish/v1/Systems" },
  { label: "机箱（温度/电源）", path: "/redfish/v1/Chassis" },
  { label: "事件日志 SEL", path: "/redfish/v1/Managers/1/LogServices" },
];

type QuickResult = { command: string; output: string; isError: boolean; refused?: string; refusedUnknown?: boolean };
type DockTab = "devices" | "context" | "topology" | "findings" | "proposals" | "audit";
// Fresh installs open a curated trio — the dock stays calm; the rest join via
// the "+" dropdown or the bottom-nav entries (拓扑/审计) on demand. Stored
// state always wins after the user customizes.
const DOCK_TAB_DEFAULT_OPEN: readonly DockTab[] = ["devices", "findings", "proposals"];
const NETDEV_DOCK_TABS_KEY = "fairpeer.netdevDockTabs";
const NETDEV_DOCK_TABS_SEEDED = "fairpeer.netdevDockTabs.seeded";

export function NetDevLayout({
  mainNode,
  footerNode,
  bannersNode,
  terminalNode,
  sessionsNode,
  searchNode,
  projectTreeNode,
  onOpenSettings,
  onInsertComposer,
  onNewSession,
  onToggleSidebar,
  onSwitchMode,
  sidebarToggleTitle,
  sidebarCollapsed = false,
  sidebarWidth,
  sidebarMinWidth,
  sidebarMaxWidth,
  onSidebarResizeStart,
  onSidebarResizeKey,
  onSidebarResetWidth,
  dockOpen = true,
  dockOnClose,
  onDockOpen,
  dockWidth,
  dockMinWidth,
  dockMaxAriaWidth,
  onDockResizeStart,
  onDockResizeKey,
  onDockResetWidth,
}: {
  mainNode: ReactNode;
  footerNode: ReactNode;
  // Global banners (startup error / update notice) — rendered at the top of
  // the 运维 main so they stay visible here too (the coding chat pane that
  // used to host them is display:none under .app--netdev).
  bannersNode?: ReactNode;
  // Terminal console (Ctrl+`) — App builds the panel node once and lends it to
  // the mode layout, so the chrome's terminal toggle works in 运维 too.
  terminalNode?: ReactNode;
  sessionsNode: ReactNode;
  // Sidebar search + project tree — the same nodes the coding/office sidebars
  // render, so the 运维 left nav carries the full grammar (search → 最近会话 →
  // 项目工作区) on top of its device list.
  searchNode?: ReactNode;
  projectTreeNode?: ReactNode;
  onOpenSettings: (tab: string) => void;
  // Fills the chat composer with a starter prompt (the AI-配置 entry point on
  // the device card / topology nodes). Optional: card hides the button if absent.
  onInsertComposer?: (text: string) => void;
  // New diagnostic session — mirrors the coding view's brand-row ghost button.
  onNewSession?: () => void;
  // Sidebar collapse + resize — same affordances as the coding sidebar.
  onToggleSidebar?: () => void;
  onSwitchMode?: (mode: "dev" | "cowork" | "netdev") => void;
  sidebarToggleTitle?: string;
  sidebarCollapsed?: boolean;
  sidebarWidth?: number;
  sidebarMinWidth?: number;
  sidebarMaxWidth?: number;
  onSidebarResizeStart?: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onSidebarResizeKey?: (event: ReactKeyboardEvent<HTMLButtonElement>) => void;
  onSidebarResetWidth?: () => void;
  // Right dock open/close + width resize — the coding workbench-dock's exact
  // affordances (chrome workspace toggle, drag resizer, drag-to-edge close).
  // dockOnClose also fires when the last strip tab is closed.
  dockOpen?: boolean;
  dockOnClose?: () => void;
  // Re-opens a closed dock (the bottom-left 设备清单 nav entry) — App owns the
  // open state.
  onDockOpen?: () => void;
  dockWidth?: number;
  dockMinWidth?: number;
  dockMaxAriaWidth?: number;
  onDockResizeStart?: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onDockResizeKey?: (event: ReactKeyboardEvent<HTMLButtonElement>) => void;
  onDockResetWidth?: () => void;
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
  // Browser-style dock tabs (coding workbench-dock pattern): only OPEN tabs
  // show, each closable, "+" re-opens from the catalog, order persists.
  // Seed the curated default ONCE (localStorage flag); afterwards the user's
  // own open/close set is authoritative.
  const [openTabs, setOpenTabs] = useDockTabState(NETDEV_DOCK_TABS_KEY, (() => {
    try {
      if (!localStorage.getItem(NETDEV_DOCK_TABS_SEEDED)) {
        localStorage.setItem(NETDEV_DOCK_TABS_SEEDED, "1");
        localStorage.setItem(NETDEV_DOCK_TABS_KEY, JSON.stringify(DOCK_TAB_DEFAULT_OPEN));
      }
    } catch { /* storage unavailable */ }
    return DOCK_TAB_DEFAULT_OPEN;
  })());
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
      setQuick(q => ({ ...q, [command]: {
        command,
        output: r.refused ? (r.refusal ?? "已拒绝") : r.output,
        isError: !!r.refused || r.is_error,
        refused: r.refused ? "1" : undefined,
        refusedUnknown: !!r.refused && r.class === "unknown",
      } }));
    } catch (e) {
      setQuick(q => ({ ...q, [command]: { command, output: String(e), isError: true } }));
    }
  }, []);

  // SNMP panel: quick allowlisted queries land in the same result area.
  const runSnmp = useCallback(async (device: string, label: string, oid: string, mode: string) => {
    try {
      const out = await app.NetDevSnmpQuery(device, oid, mode);
      setQuick(q => ({ ...q, [label]: { command: `SNMP ${mode} ${oid}`, output: out, isError: false } }));
    } catch (e) {
      setQuick(q => ({ ...q, [label]: { command: `SNMP ${mode} ${oid}`, output: String(e), isError: true } }));
    }
  }, []);

  // BMC panel: quick Redfish GETs land in the same result area as CLI runs.
  const runRedfish = useCallback(async (device: string, label: string, path: string) => {
    try {
      const out = await app.NetDevRedfishQuery(device, path);
      setQuick(q => ({ ...q, [label]: { command: `GET ${path}`, output: out, isError: false } }));
    } catch (e) {
      setQuick(q => ({ ...q, [label]: { command: `GET ${path}`, output: String(e), isError: true } }));
    }
  }, []);

  // One-click knowledge growth: an unknown-but-plausibly-read command refused
  // by the classifier gets a chip that teaches the read table — no TOML
  // editing, no model self-declaration (B-1: only the user extends the table).
  const teachRead = useCallback(async (vendor: string, command: string, device: string) => {
    try {
      await app.NetDevAddExtraRead(vendor, command);
      await runQuick(device, command);
    } catch (e) {
      setErr(String(e));
    }
  }, [runQuick]);

  const genTopology = useCallback(async () => {
    setTopoBusy(true);
    try {
      const g = await app.NetDevTopologySnapshot();
      if (g) {
        setTopo({ ...g, nodes: g.nodes ?? [], edges: g.edges ?? [] });
        setTopoSource("measured");
      }
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
      setOpenTabs((prev) => (prev.includes("context") ? prev : [...prev, "context"]));
      setTopoNotice("");
      return;
    }
    setTopoNotice(`「${name}」不在设备清单——邻居表发现的节点（未纳管或无凭证），到 设置 → 运维 录入后即可点击诊断。`);
  }, [topo]);

  // Rendering doctrine for the map: click-to-render, program-computed, IP
  // plan first. Entering the 拓扑 tab loads the LOCAL view instantly (pure
  // computation over the inventory — zero device sessions, zero model calls);
  // the measured LLDP/CDP sweep only runs on the explicit 校准 click.
  const [topoSource, setTopoSource] = useState<"plan" | "measured">("plan");
  const loadPlan = useCallback(async () => {
    try {
      const g = await app.NetDevTopologyPlan();
      if (g) {
        setTopo(g);
        setTopoSource("plan");
      }
    } catch (e) {
      setErr(String(e));
    }
  }, []);
  const topoAutoFired = useRef(false);
  useEffect(() => {
    if (tab === "topology" && !topoAutoFired.current) {
      topoAutoFired.current = true;
      void loadPlan();
    }
  }, [tab, loadPlan]);

  const runInspection = useCallback(async () => {
    setInspBusy(true);
    try {
      const f = await app.NetDevRunInspection();
      if (f) setErr(`[SYS] INSPECTION COMPLETE: ${f.title}`);
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setInspBusy(false);
    }
  }, [reload]);

  // Security posture: sealed config reads + local rule battery → Findings.
  const [baseBusy, setBaseBusy] = useState(false);
  const runBaseline = useCallback(async () => {
    setBaseBusy(true);
    try {
      const f = await app.NetDevRunBaseline();
      if (f) setErr(`[SYS] BASELINE COMPLETE: ${f.title}`);
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBaseBusy(false);
    }
  }, [reload]);


  // Active project scope (site switcher in the title bar): null = 全部.
  const [project, setProject] = useState<NetDevProjectScope>(getActiveProject());
  useEffect(() => subscribeActiveProject(() => setProject(getActiveProject())), []);

  const allDevices = settings?.devices ?? [];
  const inScope = useCallback((group: string | undefined) => {
    if (!project || project.groups.length === 0) return true;
    return project.groups.includes(group?.trim() || "未分组");
  }, [project]);
  const devices = allDevices.filter(d => inScope(d.group));
  const scopedDeviceNames = new Set(devices.map(d => d.name));
  const selectedDevice = devices.find(d => d.name === selected);
  const scopedFindings = findings.filter(f => !project || (f.devices ?? []).some(n => scopedDeviceNames.has(n)));
  const scopedProposals = proposals.filter(p => !project || (p.steps ?? []).some(s => scopedDeviceNames.has(s.device)));
  const lastInspection = scopedFindings.find(f => f.title.startsWith("巡检"));
  const pendingCount = scopedProposals.filter(p => p.status === "draft" || p.status === "approved" || p.status === "partial").length;

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

  // Topology filtered to the active project: managed nodes must be in scope;
  // unmanaged neighbors survive only while an in-scope edge touches them.
  const scopedTopo = useMemo(() => {
    if (!project || !topo) return topo;
    const nameIn = (n: string) => scopedDeviceNames.has(n);
    const nodes = topo.nodes ?? [];
    const edges = (topo.edges ?? []).filter(e => nameIn(e.local_device) || nameIn(e.remote_device));
    const touched = new Set<string>(edges.flatMap(e => [e.local_device, e.remote_device]));
    return { ...topo, nodes: nodes.filter(n => nameIn(n.name) || (!n.managed && touched.has(n.name))), edges };
  }, [project, topo, scopedDeviceNames]);

  const TABS: { key: DockTab; label: string; badge?: number; icon: React.ReactNode }[] = [
    { key: "devices", label: "设备", badge: devices.length || undefined, icon: <Server size={13} /> },
    { key: "context", label: "手册", icon: <BookOpen size={13} /> },
    { key: "topology", label: "拓扑", icon: <Network size={13} /> },
    { key: "findings", label: "发现", badge: scopedFindings.length || undefined, icon: <AlertTriangle size={13} /> },
    { key: "proposals", label: "提案", badge: pendingCount || undefined, icon: <ClipboardCheck size={13} /> },
    { key: "audit", label: "审计", icon: <ScrollText size={13} /> },
  ];

  // Active tab closed (or restored state desyncs) → fall back to the last
  // open one — the coding dock's dockTabs effect.
  useEffect(() => {
    if (!openTabs.includes(tab)) {
      setTab(openTabs[openTabs.length - 1] ?? "context");
    }
  }, [openTabs, tab]);

  const closeDockTabFn = (key: DockTab) => {
    setOpenTabs((prev) => {
      const next = prev.filter((m) => m !== key);
      if (next.length === 0) {
        // Closing the last tab closes the whole dock (coding-dock behavior).
        dockOnClose?.();
        return prev;
      }
      setTab((active) => (active === key ? next[next.length - 1] : active));
      return next;
    });
  };

  // Strip click / "+" catalog pick: ensure the tab is open and focus it.
  const openDockTabFn = (key: DockTab) => {
    setOpenTabs((prev) => (prev.includes(key) ? prev : [...prev, key]));
    setTab(key);
  };

  return (
    <div className="ndv">
      {/* Rail width resizer — same class/handlers as the coding sidebar's
          (the .app--netdev rule hides the app-level copy; this one re-shows). */}
      {!sidebarCollapsed && onSidebarResizeStart && (
        <button
          className="sidebar-resizer"
          type="button"
          role="separator"
          aria-orientation="vertical"
          aria-label="拖动调整侧栏宽度"
          aria-valuemin={sidebarMinWidth}
          aria-valuemax={sidebarMaxWidth}
          aria-valuenow={sidebarWidth}
          onPointerDown={onSidebarResizeStart}
          onKeyDown={onSidebarResizeKey}
          onDoubleClick={onSidebarResetWidth}
        />
      )}
      <div className="ndv__rail">
        {/* Brand row — identical classes/markup to the coding view's
            sidebar__brandrow: logo + FairPeer + new-session ghost button +
            collapse toggle. Spans the chrome row (top-left of the window). */}
        <div className="sidebar__brandrow" title="运维模式">
          {/* Logo retired; collapse toggle leads, mode name follows (2026-08-19). */}
          {onToggleSidebar && (
            <button
              type="button"
              className="app-chrome__panel-toggle app-chrome__panel-toggle--left sidebar__brand-toggle sidebar__brand-toggle--lead"
              onClick={onToggleSidebar}
              aria-label={sidebarToggleTitle}
              aria-pressed={!sidebarCollapsed}
            >
              <PanelLeft size={16} />
            </button>
          )}
          <WorkspacePill
            state="static"
            label={settings?.networkName?.trim() || "我的网络"}
            currentMode="netdev"
            {...(onSwitchMode ? { onSwitchMode } : {})}
          />
          {onNewSession && (
            <button
              type="button"
              className="sidebar__brand-iconbtn"
              onClick={onNewSession}
              aria-label="新建诊断会话"
              title="新建诊断会话"
            >
              <SquarePen size={15} />
            </button>
          )}
        </div>
        {/* Scroll lives BELOW the pinned brand row (the coding sidebar's
            structure: root never scrolls, brand row stays put). */}
        <div className="ndv__rail-scroll">
        {searchNode}
        <div className="ndv__section-row"><div className="ndv__section">诊断会话</div></div>
        <div className="ndv__sessions">{sessionsNode}</div>
        {/* 项目工作区 — the same ProjectTree node the coding/office sidebars
            render (profile="netdev" partition: 运维 has its own project index,
            empty until the user adds projects). Same left-nav grammar as the
            coding view: search → 最近会话 → 项目工作区. */}
        {projectTreeNode && (
          <section className="sidebar__section sidebar__section--projects">
            {projectTreeNode}
          </section>
        )}
        {/* 运维专属导航 — the pinned bottom-left group, mirroring the
            coding/office sidebars' bottom nav (编码偏好 / 办公偏好…). The
            device inventory itself lives in the right dock's 设备 tab;
            巡检 is a direct action; 运维偏好 opens settings. */}
        <section className="cowork-sidebar__group" style={{ marginBottom: '0px', marginTop: 'auto' }}>
          <button
            className={`cowork-sidebar__item ${dockOpen && tab === "devices" ? "cowork-sidebar__item--active" : ""}`}
            onClick={() => {
              onDockOpen?.();
              openDockTabFn("devices");
            }}
          >
            <Server size={14} />
            <span>设备清单</span>
          </button>
          <button
            className="cowork-sidebar__item"
            onClick={() => void runInspection()}
          >
            <ScanSearch size={14} />
            <span>{inspBusy ? "巡检中…" : "立即巡检"}</span>
          </button>
          <button
            className={`cowork-sidebar__item ${dockOpen && tab === "audit" ? "cowork-sidebar__item--active" : ""}`}
            onClick={() => { onDockOpen?.(); openDockTabFn("audit"); }}
          >
            <ScrollText size={14} />
            <span>审计（{todayAudit.length}）</span>
          </button>
          <button
            className="cowork-sidebar__item"
            onClick={() => onOpenSettings("netdev")}
          >
            <SlidersHorizontal size={14} />
            <span>运维偏好</span>
          </button>
        </section>
        </div>
      </div>

      <div className="ndv__main">
        {bannersNode}
        <div className="ndv__chat">{mainNode}</div>
        {footerNode}
        {terminalNode}
      </div>

      {/* Right dock width resizer — same class/handlers as the coding
          workbench-dock's (the .app--netdev rule hides the app-level copy;
          this one re-shows). */}
      {dockOpen && onDockResizeStart && (
        <button
          className="workspace-panel-resizer"
          type="button"
          role="separator"
          aria-orientation="vertical"
          aria-label="拖动调整右栏宽度"
          aria-valuemin={dockMinWidth}
          aria-valuemax={dockMaxAriaWidth}
          aria-valuenow={dockWidth}
          onPointerDown={onDockResizeStart}
          onKeyDown={onDockResizeKey}
          onDoubleClick={onDockResetWidth}
        />
      )}
      {dockOpen && (
      <div className="ndv__dock">
        {/* The coding workbench-dock's tab strip, verbatim (DockTabs): only
            open tabs show, each closable, "+" offers the catalog. */}
        <DockTabs
          tabs={TABS}
          openTabs={openTabs}
          active={tab}
          onSelect={openDockTabFn}
          onClose={closeDockTabFn}
          listLabel="运维视图"
          closeLabel="关闭页签"
          addLabel="打开页签"
        />
        <div className="ndv__dock-body">
        {err && <div className="banner banner--error" style={{ marginBottom: 8 }}>{err}</div>}

        {tab === "devices" && (
          <div className="ndv__card">
            <div className="ndv__card-title">设备清单{project ? <span style={{ fontWeight: 400, fontSize: 11 }}> · {project.name}</span> : ""}（{devices.length}）</div>
            {lastInspection && (
              <div className="ndv__meta">上次巡检：{lastInspection.title}（{String(lastInspection.created_at ?? "").slice(5, 16).replace("T", " ")}）</div>
            )}
            {devices.length === 0 && (
              <div className="ndv__hint">还没有设备。「手册」页签的 开始使用 三步录入，或 设置 → 运维。</div>
            )}
            {[...groups.entries()].map(([g, list]) => (
              <div key={g} className="ndv__group">
                <div className="ndv__group-row"><span>▸ {g}</span><span>{list.length}</span></div>
                {list.map(d => (
                  <div
                    key={d.name}
                    className={`ndv__device ndv__device--click${selected === d.name ? " ndv__device--sel" : ""}`}
                    role="button"
                    onClick={() => { setSelected(d.name); setTab("context"); setOpenTabs((prev) => (prev.includes("context") ? prev : [...prev, "context"])); setQuick({}); }}
                  ><span className="ndv__device-name">{d.name}</span><span className="ndv__device-addr">{d.address}</span></div>
                ))}
              </div>
            ))}
            {(settings?.hops?.length ?? 0) > 0 && (
              <>
                <div className="ndv__section-row"><div className="ndv__section">堡垒链</div><span className="ndv__section-meta">{settings?.hops.length} 跳</span></div>
                {(settings?.hops ?? []).map(h => (
                  <div key={h.name} className="ndv__device"><span className="ndv__device-name">{h.name}</span><span className="ndv__device-addr">{h.host}</span></div>
                ))}
              </>
            )}
          </div>
        )}

        {tab === "context" && (
          allDevices.length === 0 ? <GettingStarted onOpenSettings={onOpenSettings} /> :
          devices.length === 0 ? (
            <div className="ndv__card ndv__card--dim">项目「{project?.name}」的设备组（{project?.groups.join("、")}）内还没有设备——标题栏可切回「全部」，或在 设置 → 运维 给设备分组。</div>
          ) :
          selectedDevice ? (
            <div className="ndv__card">
              <div className="ndv__card-title">{selectedDevice.name} <span className="ndv__card-sub">· {selectedDevice.vendor}/{selectedDevice.os} · {selectedDevice.address}{(selectedDevice.via ?? []).length ? " · 经 " + (selectedDevice.via ?? []).join("→") : ""}</span></div>
              <div className="ndv__group-label">快捷诊断</div>
              {selectedDevice.vendor === "redfish" ? (
                <div className="ndv__quick-cmds">
                  {REDFISH_QUICK.map(q => (
                    <span key={q.label} className="btn btn--secondary btn--small" role="button" title={q.path} onClick={() => void runRedfish(selectedDevice.name, q.label, q.path)}>{q.label}</span>
                  ))}
                </div>
              ) : selectedDevice.vendor === "snmp" ? (
                <div className="ndv__quick-cmds">
                  {SNMP_QUICK.map(q => (
                    <span key={q.label} className="btn btn--secondary btn--small" role="button" title={`${q.mode} ${q.oid}`} onClick={() => void runSnmp(selectedDevice.name, q.label, q.oid, q.mode)}>{q.label}</span>
                  ))}
                </div>
              ) : (
                <div className="ndv__quick-cmds">
                  {(QUICK_BATTERY[selectedDevice.vendor] ?? ["display version"]).map(cmd => (
                    <span key={cmd} className="btn btn--secondary btn--small" role="button" onClick={() => void runQuick(selectedDevice.name, cmd)}>{cmd}</span>
                  ))}
                </div>
              )}
              <div className="ndv__group-label">诊断组合</div>
              {(settings?.presets ?? []).filter(p => (p.vendors ?? []).length === 0 || (p.vendors ?? []).includes(selectedDevice.vendor)).length > 0 && (
                <div className="ndv__quick-cmds">
                  {(settings?.presets ?? []).filter(p => (p.vendors ?? []).length === 0 || (p.vendors ?? []).includes(selectedDevice.vendor)).map(p => (
                    <span
                      key={p.name}
                      className="btn btn--secondary btn--small"
                      role="button"
                      title={`${p.commands.join("；")}（逐条经密封路径执行）`}
                      onClick={() => { for (const c of p.commands) void runQuick(selectedDevice.name, c); }}
                    >▶ {p.name}</span>
                  ))}
                </div>
              )}
              <div className="ndv__group-label">配置与备份</div>
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
              <BackupHistory device={selectedDevice.name} />
              {Object.values(quick).map(r => (
                <div key={r.command} className="ndv__quick-result">
                  <div className="ndv__quick-cmd" style={r.isError ? { color: "#ff8787" } : undefined}>{r.command}</div>
                  {r.refusedUnknown && selectedDevice && (
                    <div style={{ marginBottom: 4 }}>
                      <span
                        className="btn btn--secondary btn--small"
                        role="button"
                        title="此命令不在驱动读表中（保守拒绝）。确认它只读后加入读表——只有用户能扩展知识，模型不能自我声明。"
                        onClick={() => void teachRead(selectedDevice.vendor, r.command, selectedDevice.name)}
                      >允许此命令（加入读表）</span>
                    </div>
                  )}
                  <pre className="ndv__pre">{r.output || "（无输出）"}</pre>
                </div>
              ))}
            </div>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
            <div className="ndv__card ndv__card--dim">&gt;&gt; SYS.READY: 选中目标节点获取实时遥测。支持全封闭沙箱通道执行靶向诊断。或在交互终端直接下达自然语言指令。</div>
            <DailyBriefing />
          </div>
          )
        )}

        {tab === "topology" && (
          <div className="ndv__card">
            <div className="ndv__card-title">
              拓扑 <span style={{ fontWeight: 400, fontSize: 11, color: topoSource === "measured" ? "var(--ok)" : "var(--accent)" }}>
                {topoSource === "measured" ? "[ LLDP/CDP 实测 ]" : "[ IP 规划推断（本地计算，未连接设备） ]"}
              </span>
            </div>
            <div className="ndv__rail-actions" style={{ padding: 0, marginBottom: 8, display: "flex", gap: 6, flexWrap: "wrap" }}>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void loadPlan()}>
                刷新 IP 规划视图
              </span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void genTopology()}>
                {topoBusy ? "采集邻居表中…" : "LLDP 实测校准（逐台读取邻居表）"}
              </span>
            </div>
            {topoNotice && (
              <div className="ndv__hint" style={{ padding: 0, marginBottom: 8, color: "var(--accent)" }}>{topoNotice}</div>
            )}
            {topo && <TopologyMap graph={scopedTopo ?? topo} selected={selected} selectedAddr={selectedDevice?.address} onPick={pickFromTopo} />}
            {!topo && !topoBusy && (
              <div className="ndv__hint" style={{ padding: 0 }}>
                &gt;&gt; TOPO.ENGINE: 基于 IP 规划/命名/网段的本地聚类推演 (AI-Free)。拓扑链路强制数据保真——未证实链路不予绘制。请求全景态势，请执行 LLDP/CDP 靶向实测校准。
              </div>
            )}
          </div>
        )}

        {tab === "findings" && (
          <div className="ndv__card">
            <div className="ndv__card-title">
              发现（{scopedFindings.length}）{project && <span style={{ fontWeight: 400, fontSize: 11 }}> · {project.name}</span>}
            </div>
            <div className="ndv__rail-actions" style={{ padding: 0, marginBottom: 8 }}>
              <span
                className="btn btn--secondary btn--small"
                role="button"
                title="逐台读取 running-config（只读密封路径，已脱敏）并用本地规则核查：Telnet/SNMPv1v2c/明文密码/SSHv1/NTP/Syslog。命中项进入发现，附证据与修复建议（修复走提案）。"
                onClick={() => void runBaseline()}
              >{baseBusy ? "核查中…" : "安全基线核查"}</span>
            </div>
            {scopedFindings.length === 0 && <div className="ndv__hint" style={{ padding: 0 }}>{project ? `>> 项目「${project.name}」暂无数据。` : <>&gt;&gt; NULL_DATA: 证据池为空。Agent 诊断输出及全量巡检报告将在此落盘 (Enforced Evidence-Based)。</>}</div>}
            {scopedFindings.slice(0, 20).map(f => <FindingRow key={f.id} f={f} />)}
          </div>
        )}

        {tab === "proposals" && (
          <div className="ndv__card">
            <div className="ndv__card-title">提案（{scopedProposals.length}）{project && <span style={{ fontWeight: 400, fontSize: 11 }}> · {project.name}</span>}</div>
            {scopedProposals.length === 0 && <div className="ndv__hint" style={{ padding: 0 }}>{project ? `>> 项目「${project.name}」暂无提案。` : <>&gt;&gt; NULL_DATA: 无挂起提案。在交互终端向 Agent 下达变更意图 (netdev_propose) 进入审批流。</>}</div>}
            {scopedProposals.slice(0, 10).map(p => <ProposalRow key={p.id} p={p} onDone={() => void reload()} />)}
            <div className="ndv__hint" style={{ padding: 0 }}>批准 / 执行 / 回滚在行内直接操作；agent 只能起草，执行权永远在人。</div>
          </div>
        )}
        </div>
      </div>
      )}

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
    { text: "「设备」页签点设备快捷诊断，或在对话里描述故障现象（如「core-sw-1 的 OSPF 邻居一直 down」）" },
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

// DailyBriefing: objective 24h data in, model-judged report out — the
// content comes from one designed prompt on a headless netdev controller,
// never a hardcoded template (user direction). Falls back to a nudge when
// there is nothing to summarize yet.
function DailyBriefing() {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const run = useCallback(async () => {
    setBusy(true);
    setErr("");
    try {
      setText(await app.NetDevDailyBriefing());
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }, []);
  return (
    <div className="ndv__card">
      <div className="ndv__card-title">今日简报</div>
      <div style={{ display: "flex", gap: 6, marginBottom: text || err ? 8 : 0 }}>
        <span className="btn btn--secondary btn--small" role="button" onClick={() => void run()}>
          {busy ? "汇总中…" : "生成今日简报"}
        </span>
      </div>
      {err && <div className="ndv__meta" style={{ color: "var(--err)" }}>{err}</div>}
      {text && (
        <div className="ndv__md">
          <Markdown text={text} />
        </div>
      )}
    </div>
  );
}

// BackupHistory: the device card's version vault — newest versions first,
// pick any two to see the redacted unified diff (变更↔故障关联的入口).
function BackupHistory({ device }: { device: string }) {
  const [versions, setVersions] = useState<NetDevBackupVersion[]>([]);
  const [pick, setPick] = useState<string[]>([]);
  const [diff, setDiff] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    try {
      setVersions(await app.NetDevBackups(device));
      setPick([]);
      setDiff("");
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, [device]);
  useEffect(() => { void load(); }, [load]);
  const runBackup = useCallback(async () => {
    setBusy(true);
    try {
      const vers = await app.NetDevRunBackup(device);
      if (vers.length === 0) setErr("[SYS] BACKUP FAILED: ACCESS DENIED OR DEVICE ERROR");
      await load();
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  }, [device, load]);
  const toggle = (id: string) => {
    setPick(p => p.includes(id) ? p.filter(x => x !== id) : [...p, id].slice(-2));
    setDiff("");
  };
  const showDiff = async () => {
    if (pick.length !== 2) return;
    try {
      // pick[0] is newer, pick[1] older → diff(old → new)
      setDiff(await app.NetDevBackupDiff(device, pick[1], pick[0]));
    } catch (e) {
      setErr(String(e));
    }
  };
  return (
    <div style={{ marginTop: 8 }}>
      <div style={{ display: "flex", gap: 6, alignItems: "center", marginBottom: 4 }}>
        <span
          className="btn btn--secondary btn--small"
          role="button"
          title="经密封路径读取 running-config，脱敏后存为版本；可与任意历史版本 diff。"
          onClick={() => void runBackup()}
        >{busy ? "备份中…" : "备份配置"}</span>
        <span className="ndv__meta" style={{ marginBottom: 0 }}>版本 {versions.length}，勾选两个看 diff</span>
      </div>
      {err && <div className="ndv__meta" style={{ color: "var(--err)" }}>{err}</div>}
      {versions.slice(0, 8).map(v => (
        <div key={v.id} className="ndv__backup-row" role="button" onClick={() => toggle(v.id)}>
          <span style={{ opacity: pick.includes(v.id) ? 1 : 0.45 }}>{pick.includes(v.id) ? "☑" : "☐"}</span>
          <span>{v.at}</span>
          <span style={{ opacity: 0.6 }}>{v.lines} 行</span>
        </div>
      ))}
      {pick.length === 2 && (
        <div style={{ margin: "4px 0" }}>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => void showDiff()}>对比所选版本</span>
        </div>
      )}
      {diff && (
        <pre className="ndv__pre ndv__diff">
          {diff.split("\n").map((l, i) => (
            <span key={i} className={l.startsWith("+") ? "ndv__dl-add" : l.startsWith("-") ? "ndv__dl-del" : undefined}>{l}{"\n"}</span>
          ))}
        </pre>
      )}
    </div>
  );
}

// FindingRow: one diagnosis conclusion with its expandable evidence chain
// (device ▸ command ▸ output) — the review surface, ported from the settings
// FindingCenter so operations never leave the 运维 page.
const SEV_LABEL: Record<string, string> = { info: "ℹ 提示", warning: "⚠ 警告", critical: "🔴 严重" };
function FindingRow({ f }: { f: NetDevFinding }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="ndv__finding" style={{ "--sev": SEV_COLOR[f.severity] ?? SEV_COLOR.info } as React.CSSProperties}>
      <div className="ndv__finding-title" role="button" onClick={() => setOpen(!open)}>
        <span style={{ color: SEV_COLOR[f.severity] ?? SEV_COLOR.info, marginRight: 6 }}>{SEV_LABEL[f.severity] ?? SEV_LABEL.info}</span>
        {f.title} <span style={{ fontWeight: 400, opacity: 0.7 }}>证据 {f.evidence?.length ?? 0} 条 {open ? "▲" : "▼"}</span>
      </div>
      <div className="ndv__meta">{(f.devices ?? []).join("、")}{f.suggestion ? "" : ""}</div>
      {f.suggestion && !open && <div className="ndv__finding-suggestion">建议：{f.suggestion}</div>}
      {open && (
        <div style={{ marginTop: 4 }}>
          {f.detail && <div className="ndv__meta">{f.detail}</div>}
          {(f.evidence ?? []).map((e, i) => (
            <div key={i} style={{ marginBottom: 6, marginLeft: 8 }}>
              <div style={{ opacity: 0.8, fontSize: 11.5 }}>{e.device} ▸ <code>{e.command}</code></div>
              <pre className="ndv__pre" style={{ maxHeight: 140 }}>{e.output}</pre>
            </div>
          ))}
          {f.suggestion && <div className="ndv__finding-suggestion">建议：{f.suggestion}（可让 agent 起草提案）</div>}
          <div className="ndv__meta">{String(f.created_at ?? "").slice(0, 19).replace("T", " ")}</div>
        </div>
      )}
    </div>
  );
}

// ProposalRow: one queue row, expandable to steps (commands + rollback plan)
// with the human actions inline — approve / execute / rollback happen where
// the user is looking, not in a settings round-trip.
function ProposalRow({ p, onDone }: { p: NetDevProposal; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const sev = p.status === "partial" || p.status === "failed" ? SEV_COLOR.critical : SEV_COLOR.warning;
  return (
    <div className="ndv__finding" style={{ "--sev": sev } as React.CSSProperties}>
      <div className="ndv__finding-title" role="button" onClick={() => setOpen(!open)}>
        {p.id} · {p.status} {open ? "▲" : "▼"}
      </div>
      <div className="ndv__meta ndv__ellipsis">{p.intent}</div>
      <ProposalActions p={p} onDone={onDone} />
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
  // Null-hardening (2026-08-19 exe crash: "e.nodes is not iterable"): the
  // measured snapshot can arrive with null nodes/edges when no devices are
  // configured — normalize once, never iterate raw.
  const nodes = graph?.nodes ?? [];
  const edges = graph?.edges ?? [];
  // Degree per node (managed nodes only drive tiering).
  const degree = new Map<string, number>();
  for (const n of nodes) degree.set(n.name, 0);
  for (const e of edges) {
    if (e.local_device !== e.remote_device) {
      degree.set(e.local_device, (degree.get(e.local_device) ?? 0) + 1);
      degree.set(e.remote_device, (degree.get(e.remote_device) ?? 0) + 1);
    }
  }
  const managed = nodes.filter(v => v.managed);
  // Max degree for the fallback tiering.
  const maxDeg = Math.max(1, ...managed.map(v => degree.get(v.name) ?? 0));
  // Tier: the backend's local inference wins when present (IP-plan view);
  // otherwise fall back to the degree heuristic (measured view).
  const tierOfNode = (v: { name: string; managed: boolean; tier?: number }): number => {
    if (v.tier !== undefined && v.tier >= 0) return v.tier;
    if (!v.managed) return 3;
    const d = degree.get(v.name) ?? 0;
    if (d >= maxDeg * 0.6) return 0;
    if (d >= maxDeg * 0.3) return 1;
    return 2;
  };
  // Unmanaged neighbors get their own bottom band (tier 3).
  const bands: string[][] = [[], [], [], []];
  for (const v of nodes) bands[tierOfNode(v)].push(v.name);
  const pos = new Map<string, { x: number; y: number }>();
  const bandY = [46, 148, 250, 322];
  bands.forEach((band, bi) => {
    const n = band.length;
    band.forEach((name, i) => {
      pos.set(name, { x: n === 1 ? W / 2 : 36 + ((W - 72) * i) / Math.max(n - 1, 1), y: bandY[bi] });
    });
  });
  const node = (name: string) => nodes.find(v => v.name === name);
  const TIER_LABEL = ["核心层", "汇聚层", "接入层", "未纳管邻居"];
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: "100%", maxWidth: 640, marginTop: 8, display: "block" }}>
        {bandY.map((y, i) =>
          bands[i].length > 0 ? (
            <text key={i} x={10} y={y - 20} fontSize={10} fill="var(--fg-faint)" opacity={0.8}>
              {TIER_LABEL[i]}
            </text>
          ) : null,
        )}
        {edges.map((e, i) => {
          const pa = pos.get(e.local_device), pb = pos.get(e.remote_device);
          if (!pa || !pb) return null;
          return (
            <line
              key={i}
              x1={pa.x} y1={pa.y} x2={pb.x} y2={pb.y}
              stroke="var(--border)"
              strokeWidth={1}
              opacity={0.85}
            >
              <title>{`${e.local_device}:${e.local_port} ⇄ ${e.remote_device}${e.remote_port ? ":" + e.remote_port : ""} (${e.source})`}</title>
            </line>
          );
        })}
        {nodes.map(v => {
          const p = pos.get(v.name);
          if (!p) return null;
          const tier = tierOfNode(v);
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
                  ? "var(--accent-soft)"
                  : v.managed ? "var(--bg-elev)" : "transparent"}
                stroke={sel
                  ? "var(--accent)"
                  : v.managed ? (tier === 0 ? "var(--accent)" : "var(--border)") : "var(--border)"}
                strokeWidth={sel ? 2 : v.managed && tier === 0 ? 1.6 : 1}
                strokeDasharray={v.managed ? undefined : "3 3"}
              />
              <text x={p.x} y={p.y + 3.5} textAnchor="middle" fontSize={9} fill={v.managed ? "var(--fg)" : "var(--fg-faint)"}>
                {v.name.length > 9 ? v.name.slice(0, 9) + "…" : v.name}
              </text>
              <title>{`${v.name}${nv?.device_ip ? " · " + nv.device_ip : ""}${v.subnet ? " · " + v.subnet : ""}${v.managed ? " · 纳管" : " · 未纳管"}${v.tier !== undefined && v.tier >= 0 ? " · 分层为本地推断" : ` · 连接 ${degree.get(v.name) ?? 0}`}${v.managed && onPick ? " · 点击查看设备" : ""}`}</title>
            </g>
          );
        })}
      </svg>
      <div style={{ opacity: 0.6, fontSize: 11 }}>
        {nodes.filter(v => v.managed).length} 纳管 · {nodes.filter(v => !v.managed).length} 未纳管（虚线框,不可连） · {edges.length} 链路 · 悬停链路看端口 · {graph.at}
      </div>
    </div>
  );
}
