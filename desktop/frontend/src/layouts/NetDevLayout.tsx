import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { AlertTriangle, Activity, BookOpen, CalendarClock, ClipboardCheck, FileText, HeartPulse, MousePointerClick, Network, PanelLeft, ScanSearch, ScrollText, Server, SlidersHorizontal } from "lucide-react";
import { app, onNetdevHealth, onNetdevLive } from "../lib/bridge";
import { ProfileSegmented } from "../components/AppChrome";
import { useConfirm } from "../lib/confirm";
import { getActiveProject, setActiveProject, subscribeActiveProject, type NetDevProjectScope } from "../lib/netdevProjectStore";
import { ProposalActions } from "../components/netdev/ProposalCenter";
import { LiveOpsPanel } from "../components/netdev/LiveOpsPanel";
import { LogPanel } from "../components/netdev/LogPanel";
import { LogWorkbench } from "../components/netdev/LogWorkbench";
import { SecWorkbench } from "../components/netdev/SecWorkbench";
import { CutoverView } from "../components/netdev/CutoverView";
import { TemplateCard } from "../components/netdev/TemplateCard";
import { SrvConfCard } from "../components/netdev/SrvConfCard";
import { HealthPanel } from "../components/netdev/HealthPanel";
import { BrowserConsolePanel } from "../components/netdev/BrowserConsolePanel";
import { JobsPanel } from "../components/netdev/JobsPanel";
import { AlertSetupWizard } from "../components/netdev/AlertSetupWizard";
import { DockTabs, useDockTabState } from "../components/DockTabs";
import type { NetDevSettingsView, NetDevDeviceHealth, NetDevFinding, NetDevAggregatedFinding, NetDevProposal, NetDevAuditEntryView, NetDevTopologyGraph, NetDevBackupVersion, NetDevCutoverRun } from "../lib/types";
import { useT } from "../lib/i18n";
import logoSymbol from "../assets/logo-symbol.png";
import { Markdown } from "../components/Markdown";
import { UnifiedDiff } from "../components/editors/UnifiedDiff";
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
  const [, setName] = useState("");
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

      {err && <span className="ndv__stat" style={{ color: "var(--err)" }}>{err}</span>}
      <span className="ndv__stop" role="button" onClick={() => void stop()}>⏹ 紧急停止</span>
    </div>
  );
}

// Sparkline — 24h 迷你趋势图（§5.3）：时序面数据的设备卡呈现。无数据不渲染
// （渐进披露），有则一条 60x16 的折线。
function Sparkline({ points, bad }: { points: { t: number; v: number }[]; bad?: boolean }) {
  if (points.length < 2) return null;
  const vs = points.map(p => p.v);
  const min = Math.min(...vs), max = Math.max(...vs);
  const span = max - min || 1;
  const w = 60, h = 16;
  const d = points.map((p, i) => {
    const x = (i / (points.length - 1)) * w;
    const y = h - ((p.v - min) / span) * h;
    return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(" ");
  return (
    <svg width={w} height={h} style={{ verticalAlign: "middle", marginLeft: 6 }} aria-hidden>
      <path d={d} fill="none" stroke={bad ? "var(--danger)" : "var(--ok)"} strokeWidth="1.3" />
    </svg>
  );
}

// Severity colors ride the THEME TOKENS (accent/warn/danger) — never literal
// hexes, so every theme reads consistently (user direction: 配色跟随全局).
const SEV_COLOR: Record<string, string> = { info: "var(--accent)", warning: "var(--warn)", critical: "var(--danger)" };

// Audit table helpers — same class vocabulary as the live panel.
function auditClassLabel(cls: string): string {
  return { read: "读", write: "写", dangerous: "危险", unknown: "未知", guardrail: "护栏", assess: "评估", proposal: "提案", "proposal-write": "提案写", "proposal-rollback": "提案回滚", job: "作业", cutover: "割接", oob: "带外" }[cls] ?? cls;
}
function classColorForAudit(cls: string): string {
  if (cls === "read" || cls === "proposal") return "var(--accent)";
  if (cls === "guardrail") return "var(--danger)";
  return "var(--warn)";
}

const QUICK_BATTERY: Record<string, string[]> = {
  huawei: ["display version", "display cpu-usage", "display interface brief"],
  cisco: ["show version", "show processes cpu", "show interfaces status"],
  zte: ["show version", "show processor cpu", "show interface brief"],
  vmware: ["esxcli system version get", "esxcli network nic list", "esxcfg-vswitch -l", "vim-cmd vmsvc/getallvms"],
  linux: ["ip addr", "ss -tlnp", "systemctl --failed", "df -h", "cat /proc/net/dev"],
  windows: ["Get-NetAdapter", "Get-NetTCPConnection", "Get-Service", "systeminfo"],
};

// Web 服务器只读预设（NETDEV_SPEC_V2 §2.5 v1）：nginx/apache 的配置自检与
// 模块/虚拟机清单——web kind 的先头部队，全部在既有读表白名单内。
const WEB_QUICK = ["nginx -t", "nginx -T", "apache2ctl -M", "apache2ctl -S"];

// kind=docker / kind=k8s 设备卡快捷（NETDEV_SPEC_V2 §2.2/§2.3 + §10.5 矩阵）。
const DOCKER_QUICK: { what: string; label: string }[] = [
  { what: "ping", label: "引擎连通" },
  { what: "ps", label: "容器列表" },
  { what: "images", label: "镜像" },
  { what: "info", label: "引擎信息" },
];
const K8S_QUICK: { what: string; label: string }[] = [
  { what: "version", label: "集群版本" },
  { what: "nodes", label: "节点" },
  { what: "pods", label: "Pods" },
  { what: "deployments", label: "Deployments" },
  { what: "events", label: "事件" },
];
const FW_QUICK: { what: string; label: string }[] = [
  { what: "status", label: "系统状态" },
  { what: "resource", label: "资源" },
  { what: "interfaces", label: "接口" },
  { what: "conns", label: "会话表" },
  { what: "policies", label: "策略（只读）" },
  { what: "routes", label: "路由" },
];

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
// ?bench=<workbench> deep-links the main-area workbench (mirror of ?dock=).
function benchParam(): "logs" | "sec" | null {
  try {
    const v = new URLSearchParams(window.location.search).get("bench");
    return v === "logs" || v === "sec" ? v : null;
  } catch { return null; }
}

type DockTab = "live" | "devices" | "context" | "topology" | "findings" | "proposals" | "audit" | "logs" | "health" | "jobs" | "browser";
// Fresh installs open a curated set — 操作实况 leads (supervision first), the
// rest join via the "+" dropdown or the bottom-nav entries on demand. Stored
// state always wins after the user customizes.
const DOCK_TAB_DEFAULT_OPEN: readonly DockTab[] = ["live", "health", "findings"];
const NETDEV_DOCK_TABS_KEY = "fairpeer.netdevDockTabs";
const NETDEV_DOCK_TABS_SEEDED = "fairpeer.netdevDockTabs.seeded";
const NETDEV_DOCK_TABS_LIVE_SEEDED = "fairpeer.netdevDockTabs.live-seeded";

// ?dock=<tab> (browser dev mock only — same affordance as bridge.ts's
// ?profile=) boots the dock with a given tab focused (e.g. ?dock=live,
// ?dock=audit) so panels are screenshot-testable without driving the tab
// strip first. ?live=1 stays as a ?dock=live alias. The open-tabs correction
// effect OPENs this tab instead of correcting away from it.
const DOCK_PARAM_KEYS: readonly string[] = ["live", "devices", "context", "topology", "findings", "proposals", "audit", "logs", "health", "jobs", "browser"];
function dockParam(): DockTab | null {
  try {
    if (typeof window !== "undefined" && !window.runtime) {
      const q = new URLSearchParams(window.location.search);
      const v = q.get("dock") ?? (q.get("live") === "1" ? "live" : "");
      if (DOCK_PARAM_KEYS.includes(v)) return v as DockTab;
    }
  } catch { /* not a browser */ }
  return null;
}

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

  onPickProject?: (root: string) => void;
  onAddProject?: () => void;
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
  const t = useT();
  const [settings, setSettings] = useState<NetDevSettingsView | null>(null);
  const [findings, setFindings] = useState<NetDevFinding[]>([]);
  const [proposals, setProposals] = useState<NetDevProposal[]>([]);
  const [audit, setAudit] = useState<NetDevAuditEntryView[]>([]);
  // 健康快照联动：设备页卡/拓扑/健康 tab 共用一份 live 数据；
  // hotTabs 是右栏 tab 的活动点信号（实况有流、健康有变更、发现有新条）。
  const [healthMap, setHealthMap] = useState<Record<string, NetDevDeviceHealth>>({});

  const healthDownCount = Object.values(healthMap).filter(h => !h.reachable || (h.interfaces ?? []).some(i => i.adminUp && !i.operUp)).length;
  const [selected, setSelected] = useState<string>("");
  const [quick, setQuick] = useState<Record<string, QuickResult>>({});
  const [topo, setTopo] = useState<NetDevTopologyGraph | null>(null);
  const [topoBusy, setTopoBusy] = useState(false);
  const [topoNotice, setTopoNotice] = useState("");
  const [inspBusy, setInspBusy] = useState(false);
  // ?dock=<tab> boots the dock with a given tab focused (see dockParam at
  // module scope); the open-tabs correction effect OPENs that tab instead of
  // correcting away from it.
  const [tab, setTab] = useState<DockTab>(() => dockParam() ?? "context");
  const [hotTabs, setHotTabs] = useState<Partial<Record<DockTab, boolean>>>({});
  const [alertWizardOpen, setAlertWizardOpen] = useState(false);
  const markHot = useCallback((k: DockTab) => setHotTabs(prev => ({ ...prev, [k]: true })), []);
  // Visiting a tab clears its activity dot.
  useEffect(() => { setHotTabs(prev => (prev[tab] ? { ...prev, [tab]: false } : prev)); }, [tab]);
  useEffect(() => {
    let alive = true;
    app.NetDevHealthSnapshot().then(snap => {
      if (!alive) return;
      const m: Record<string, NetDevDeviceHealth> = {};
      for (const d of snap?.devices ?? []) m[d.device] = d;
      setHealthMap(m);
    }).catch(() => {});
    const off1 = onNetdevHealth(h => {
      setHealthMap(prev => ({ ...prev, [h.device]: h }));
      markHot("health");
    });
    const off2 = onNetdevLive(() => markHot("live"));
    return () => { alive = false; off1(); off2(); };
  }, [markHot]);
  // Browser-style dock tabs (coding workbench-dock pattern): only OPEN tabs
  // show, each closable, "+" re-opens from the catalog, order persists.
  // Seed the curated default ONCE (localStorage flag); afterwards the user's
  // own open/close set is authoritative. A second one-time flag back-fills
  // the 实况 tab for installs that seeded BEFORE it existed — the stored set
  // wins afterwards, so closing it again sticks.
  const [openTabs, setOpenTabs] = useDockTabState(NETDEV_DOCK_TABS_KEY, (() => {
    try {
      if (!localStorage.getItem(NETDEV_DOCK_TABS_SEEDED)) {
        localStorage.setItem(NETDEV_DOCK_TABS_SEEDED, "1");
        localStorage.setItem(NETDEV_DOCK_TABS_KEY, JSON.stringify(DOCK_TAB_DEFAULT_OPEN));
      } else if (!localStorage.getItem(NETDEV_DOCK_TABS_LIVE_SEEDED)) {
        localStorage.setItem(NETDEV_DOCK_TABS_LIVE_SEEDED, "1");
        const stored: string[] = JSON.parse(localStorage.getItem(NETDEV_DOCK_TABS_KEY) || "[]");
        if (Array.isArray(stored) && !stored.includes("live")) {
          localStorage.setItem(NETDEV_DOCK_TABS_KEY, JSON.stringify(["live", ...stored]));
        }
      }
    } catch { /* storage unavailable */ }
    return DOCK_TAB_DEFAULT_OPEN;
  })());
  const [err, setErr] = useState(""); // shown in the global title bar

  // Main-area workbenches (NETDEV_SPEC_V2 §10.2): 对话 is the default view and
  // the return anchor; the 日志 workbench opens on demand and STAYS MOUNTED
  // once opened (closing = switching back to chat, state survives — §10.2).
  // The switch bar renders only after the first workbench open, so a
  // chat-only session keeps the exact v1.1 layout (§10.8: 单工作台零新增 chrome).
  const [bench, setBench] = useState<"chat" | "logs" | "sec">(() => (benchParam() === "sec" ? "sec" : benchParam() ? "logs" : "chat"));
  const [logsBenchEverOpened, setLogsBenchEverOpened] = useState(() => benchParam() === "logs");
  const [secBenchEverOpened, setSecBenchEverOpened] = useState(() => benchParam() === "sec");
  // 割接视图（§7.2）：对话主区里的任务过程视图（不开第四个工作台）。
  // null = 关闭；"" = 创建表单；其余 = 正在查看的 run id。
  const [cutoverId, setCutoverId] = useState<string | null>(null);
  const [cutovers, setCutovers] = useState<NetDevCutoverRun[]>([]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (bench !== "chat") {
        setBench("chat");
        return;
      }
      if (cutoverId !== null) setCutoverId(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [bench, cutoverId]);

  const openLogsBench = useCallback(() => {
    setLogsBenchEverOpened(true);
    setBench("logs");
  }, []);

  const openSecBench = useCallback(() => {
    setSecBenchEverOpened(true);
    setBench("sec");
  }, []);

  // 命令面板入口（§10.7 第 5 层）：palette 在 App 层，经自定义事件抵达——
  // 与 ?bench= 深链同效，不做状态提升。
  useEffect(() => {
    const onBench = (e: Event) => {
      const d = (e as CustomEvent<string>).detail;
      if (d === "logs") openLogsBench();
      if (d === "sec") openSecBench();
    };
    window.addEventListener("fairpeer:netdev-bench", onBench);
    return () => window.removeEventListener("fairpeer:netdev-bench", onBench);
  }, [openLogsBench]);

  const reload = useCallback(async () => {
    try {
      const [s, f, p, a, cs] = await Promise.all([
        app.NetDevSettings(),
        app.NetDevFindings(),
        app.NetDevProposals(),
        app.NetDevAuditTail(200),
        app.NetDevCutovers().catch(() => [] as NetDevCutoverRun[]),
      ]);
      setSettings(s);
      setFindings(f ?? []);
      setProposals(p ?? []);
      setAudit(a ?? []);
      setCutovers(cs ?? []);
      setErr("");
      setReloadTick(t => t + 1);
    } catch (e) {
      setErr(String(e));
    }
  }, []);

  useEffect(() => {
    void reload();
    const t = setInterval(() => void reload(), 30_000);
    return () => clearInterval(t);
  }, [reload]);

  // 聚合视图随 reload 刷新（告警队列，§4.10）——reload 闭包内拉取，避免声明序依赖。
  useEffect(() => {
    app.NetDevAggregatedFindings().then(list => setAggs(list ?? [])).catch(() => {});
  }, [findings]);

  const [reloadTick, setReloadTick] = useState(0);

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

  // 巡检家族（NETDEV_SPEC_V2 §10.6）：一个家四件事——网络巡检 / 主机体检 /
  // 基线核查 / 弱口令核查（弱口令后端早已就绪，这里补 UI 接线）。
  const [inspMenuOpen, setInspMenuOpen] = useState(false);
  const [triageBusy, setTriageBusy] = useState(false);
  const [weakBusy, setWeakBusy] = useState(false);
  const [triageOneBusy, setTriageOneBusy] = useState("");
  // §6.2/§10.5 文件下载对话框：设备卡「文件」打开的瞬时对话框（无常驻面板）。
  const [filePicker, setFilePicker] = useState("");
  const [filePath, setFilePath] = useState("/var/log");
  const [fileListing, setFileListing] = useState<string[] | null>(null);
  const [fileBusy, setFileBusy] = useState("");
  const [fileNote, setFileNote] = useState("");
  const [cardSeries, setCardSeries] = useState<Record<string, { t: number; v: number }[]>>({});

  // 设备卡打开时拉 24h 时序（有数据才显示 sparkline，§10.5 渐进披露）。
  useEffect(() => {
    if (!selected) { setCardSeries({}); return; }
    let alive = true;
    app.NetDevSeries(selected, 24).then(m => {
      if (!alive) return;
      const out: Record<string, { t: number; v: number }[]> = {};
      for (const [metric, pts] of Object.entries(m ?? {})) out[metric] = (pts ?? []).map(p => ({ t: p.t, v: p.v }));
      setCardSeries(out);
    }).catch(() => {});
    return () => { alive = false; };
  }, [selected, reloadTick]);
  const [locateTarget, setLocateTarget] = useState("");
  const [expBusy, setExpBusy] = useState(false);
  const [handoffMd, setHandoffMd] = useState("");
  const [aggView, setAggView] = useState(true); // 聚合视图开关（§4.10）
  const [aggs, setAggs] = useState<NetDevAggregatedFinding[]>([]);
  const [handoffBusy, setHandoffBusy] = useState(false);
  const [locateBusy, setLocateBusy] = useState(false);

  // 「这个 IP 接在哪」——清单内 ARP 扇出定位（§4.11）。
  const runLocate = useCallback(async () => {
    const t = locateTarget.trim();
    if (!t) return;
    setLocateBusy(true);
    try {
      const r = await app.NetDevLocate(t);
      const where = r.hits.map(h => `${h.device}${h.interface ? ":" + h.interface : ""}`).join("、");
      setErr(`[SYS] LOCATE ${t}: 覆盖 ${r.covered_devices}/${r.total_devices} 台，命中 ${r.hits.length} 处${where ? "——" + where : ""}${r.budget_stopped ? "（中途停止，未覆盖≠不存在）" : ""}`);
    } catch (e) {
      setErr(String(e));
    } finally {
      setLocateBusy(false);
    }
  }, [locateTarget]);

  // 期望状态对比（§5.4）：清单声明 vs 健康采集——「掉电恢复后缺的 13 台是谁」。
  const runExpected = useCallback(async () => {
    setExpBusy(true);
    try {
      const v = await app.NetDevExpectedState();
      const miss = (v.missing ?? []).map(m => m.device).join("、");
      setErr(`[SYS] 期望状态: 采集面 ${v.total} 台，可达 ${v.reachable}${miss ? `，缺失：${miss}` : "，无缺失"}${(v.noProbe ?? []).length ? `；${v.noProbe.length} 台无采集面` : ""}`);
    } catch (e) {
      setErr(String(e));
    } finally {
      setExpBusy(false);
    }
  }, []);

  // kind=docker / kind=k8s 的设备卡快捷：结果落进与 CLI 快捷同一个展示区。
  const runApiQuick = useCallback(async (label: string, run: () => Promise<string>) => {
    try {
      const out = await run();
      setQuick(q => ({ ...q, [label]: { command: label, output: out, isError: false } }));
    } catch (e) {
      setQuick(q => ({ ...q, [label]: { command: label, output: String(e), isError: true } }));
    }
  }, []);

  // 设备卡的「一键体检」：单台跑电池，摘要直接回报，异常进「发现」。
  const runTriageOne = useCallback(async (device: string) => {
    setTriageOneBusy(device);
    try {
      const rep = await app.NetDevTriageRun(device);
      const anoms = rep.anomalies ?? [];
      setErr(`[SYS] TRIAGE ${device}: ${rep.summary}${anoms.length ? "——" + anoms.join("；") : ""}`);
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setTriageOneBusy("");
    }
  }, [reload]);

  const runTriageAll = useCallback(async () => {
    const hosts = (settings?.devices ?? []).filter(d => d.vendor === "linux" || d.vendor === "windows");
    if (hosts.length === 0) { setErr("清单里没有 linux/windows 主机——先在 设置 → 运维 录入"); return; }
    setTriageBusy(true);
    let anomalies = 0;
    try {
      for (const h of hosts) {
        const rep = await app.NetDevTriageRun(h.name);
        anomalies += (rep.anomalies ?? []).length;
      }
      setErr(`[SYS] TRIAGE COMPLETE: ${hosts.length} 台主机体检完成，${anomalies} 项异常（见「发现」）`);
      await reload();
    } catch (e) {
      setErr(String(e));
    } finally {
      setTriageBusy(false);
    }
  }, [settings, reload]);

  const runWeakCredAll = useCallback(async () => {
    const targets = (settings?.devices ?? []).filter(d => d.vendor !== "snmp");
    if (targets.length === 0) { setErr("清单里没有可核查的设备"); return; }
    setWeakBusy(true);
    const weak: string[] = [];
    let failed = 0;
    try {
      for (const d of targets) {
        try {
          const r = await app.NetDevWeakCredCheck(d.name, "basic");
          if (r.weak) weak.push(d.name);
        } catch {
          failed++; // 无凭证/不可达的设备计入未完成，不中断全队
        }
      }
      setErr(`[SYS] WEAK-CRED COMPLETE: ${weak.length}/${targets.length} 台命中默认/空口令${weak.length ? "：" + weak.join("、") : ""}${failed ? `；${failed} 台未完成` : ""}`);
      await reload();
    } finally {
      setWeakBusy(false);
    }
  }, [settings, reload]);


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

  const groups = new Map<string, { name: string; address: string; vendor: string; kind: string }[]>();
  for (const d of devices) {
    const g = d.group?.trim() || "未分组";
    if (!groups.has(g)) groups.set(g, []);
    groups.get(g)!.push({ name: d.name, address: d.address, vendor: d.vendor, kind: d.kind ?? "" });
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

  const findingsCountRef = useRef(0);
  useEffect(() => {
    if (scopedFindings.length > findingsCountRef.current && findingsCountRef.current > 0) markHot("findings");
    findingsCountRef.current = scopedFindings.length;
  }, [scopedFindings, markHot]);
  const liveHot = !!hotTabs.live;
  const healthHot = !!hotTabs.health;
  const findingsHot = !!hotTabs.findings;
  // Tab order = the operator's workflow (正在发生 → 什么状态 → 需要我决策
  // → 留档备查); the "+" catalog renders the same grouping as headers.
  const TABS: { key: DockTab; label: string; badge?: number; dot?: boolean; group: string; icon: React.ReactNode }[] = [
    { key: "live", label: "实况", group: "正在发生", dot: liveHot, icon: <Activity size={13} /> },
    { key: "logs", label: "日志", group: "正在发生", icon: <FileText size={13} /> },
    { key: "health", label: "健康", group: "什么状态", dot: healthHot, badge: healthDownCount || undefined, icon: <HeartPulse size={13} /> },
    { key: "devices", label: "设备", group: "什么状态", badge: devices.length || undefined, icon: <Server size={13} /> },
    { key: "topology", label: "拓扑", group: "什么状态", icon: <Network size={13} /> },
    { key: "context", label: "手册", group: "什么状态", icon: <BookOpen size={13} /> },
    { key: "findings", label: "发现", group: "需要我决策", dot: findingsHot, badge: scopedFindings.length || undefined, icon: <AlertTriangle size={13} /> },
    { key: "proposals", label: "提案", group: "需要我决策", badge: pendingCount || undefined, icon: <ClipboardCheck size={13} /> },
    { key: "audit", label: "审计", group: "留档备查", icon: <ScrollText size={13} /> },
    { key: "jobs", label: "作业", group: "留档备查", icon: <CalendarClock size={13} /> },
    { key: "browser", label: "浏览器", group: "什么状态", icon: <MousePointerClick size={13} /> },
  ];

  // Active tab closed (or restored state desyncs) → fall back to the last
  // open one — the coding dock's dockTabs effect. The ?dock= deep link is the
  // one initializer allowed to OPEN a not-yet-open tab instead of correcting
  // away from it (dev-mock smoke entry).
  useEffect(() => {
    if (!openTabs.includes(tab)) {
      if (tab === dockParam()) {
        setOpenTabs((prev) => (prev.includes(tab) ? prev : [...prev, tab]));
        return;
      }
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
              <div className="brand-toggle-group">
                <img src={logoSymbol} alt="" className="brand-toggle-logo" draggable={false} />
                <PanelLeft size={16} className="brand-toggle-icon" />
              </div>
            </button>
          )}
          <ProfileSegmented
            profile="netdev"
            onSwitchProfile={onSwitchMode || (() => {})}
            t={t}
          />

        </div>
        {/* Scroll lives BELOW the pinned brand row (the coding sidebar's
            structure: root never scrolls, brand row stays put). */}
        <div className="ndv__rail-scroll">
          {searchNode}
          {sessionsNode}
          {/* 项目工作区 — the same ProjectTree node the coding/office sidebars
              render (profile="netdev" partition: 运维 has its own project index,
              empty until the user adds projects). Same left-nav grammar as the
              coding view: search → 最近会话 → 项目工作区. */}
          {projectTreeNode && (
            <section className="sidebar__section sidebar__section--projects" style={{ marginBottom: '8px', minHeight: 0, display: 'flex', flexDirection: 'column' }}>
              {projectTreeNode}
            </section>
          )}
        {/* 运维专属导航 — the pinned bottom-left group, mirroring the
            coding/office sidebars' bottom nav (编码偏好 / 办公偏好…). The
            device inventory itself lives in the right dock's 设备 tab;
            巡检 is a direct action; 运维偏好 opens settings. */}
        </div>
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
          <div style={{ position: "relative" }}>
            <button
              className="cowork-sidebar__item"
              onClick={() => setInspMenuOpen(o => !o)}
              title="网络巡检 / 主机体检 / 基线核查 / 弱口令核查"
            >
              <ScanSearch size={14} />
              <span>{inspBusy || triageBusy ? "体检中…" : weakBusy ? "核查中…" : baseBusy ? "基线核查中…" : "立即巡检"}</span>
            </button>
            {inspMenuOpen && (
              <span className="ndv__project-menu" role="menu" style={{ position: "absolute", left: "calc(100% + 6px)", bottom: 0, minWidth: 200, zIndex: 40 }}>
                <span role="menuitem" onClick={() => { setInspMenuOpen(false); void runInspection(); }}>网络巡检（只读电池）</span>
                <span role="menuitem" onClick={() => { setInspMenuOpen(false); void runTriageAll(); }}>主机体检（登录/持久化/水位）</span>
                <span role="menuitem" onClick={() => { setInspMenuOpen(false); void runBaseline(); }}>基线核查（配置合规）</span>
                <span role="menuitem" onClick={() => { setInspMenuOpen(false); void runWeakCredAll(); }}>弱口令核查（默认档）</span>
              </span>
            )}
          </div>
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

      <div className="ndv__main">
        {bannersNode}
        {logsBenchEverOpened && (
          <div className="ndv-bench__bar" role="tablist" aria-label="主区工作台">
            <span role="tab" aria-selected={bench === "chat"} className={`ndv-bench__chip${bench === "chat" ? " ndv-bench__chip--on" : ""}`} onClick={() => setBench("chat")}>对话</span>
            <span role="tab" aria-selected={bench === "logs"} className={`ndv-bench__chip${bench === "logs" ? " ndv-bench__chip--on" : ""}`} onClick={openLogsBench}>日志</span>
            <span role="tab" aria-selected={bench === "sec"} className={`ndv-bench__chip${bench === "sec" ? " ndv-bench__chip--on" : ""}`} onClick={openSecBench}>安全</span>
            <span className="ndv-bench__hint"><kbd>Esc</kbd> 返回对话</span>
          </div>
        )}
        <div className="ndv__chat" style={bench !== "chat" ? { display: "none" } : undefined}>
          {cutoverId !== null ? (
            <CutoverView
              runId={cutoverId}
              proposals={scopedProposals}
              devices={(settings?.devices ?? []).map(d => ({ name: d.name, vendor: d.vendor }))}
              onClose={() => setCutoverId(null)}
            />
          ) : (
            mainNode
          )}
        </div>
        {logsBenchEverOpened && <LogWorkbench devices={settings?.devices ?? []} onInsertComposer={onInsertComposer} hidden={bench !== "logs"} />}
        {secBenchEverOpened && <SecWorkbench devices={settings?.devices ?? []} hidden={bench !== "sec"} />}
        <div style={bench !== "chat" ? { display: "none" } : { display: "contents" }}>{footerNode}</div>
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

        {tab === "audit" && (
          <div style={{ marginBottom: 8 }}>
            <span className="btn btn--secondary btn--small" role="button" onClick={() => void (async () => {
              setHandoffBusy(true);
              try { setHandoffMd(await app.NetDevHandoffReport()); } catch (e) { setErr(String(e)); } finally { setHandoffBusy(false); }
            })()}>{handoffBusy ? "生成中…" : "生成值班交接报告"}</span>
            <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: 6 }} onClick={() => void (async () => { try { setHandoffMd(await app.NetDevWeeklyReport()); } catch (e) { setErr(String(e)); } })()}>周报</span>
            <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: 6 }} onClick={() => void (async () => { try { setHandoffMd(await app.NetDevCredentialInventory()); } catch (e) { setErr(String(e)); } })()}>凭证盘点</span>
            <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: 6 }} onClick={() => void (async () => {
              try {
                const p = await app.NetDevExportState();
                setErr(`[SYS] EXPORT: 状态包已导出 → ${p}`);
              } catch (e) { setErr(String(e)); }
            })()}>导出状态包</span>
            {handoffMd && <div className="ndv__card" style={{ marginTop: 8, maxHeight: 300, overflow: "auto" }}><Markdown text={handoffMd} /></div>}
          </div>
        )}

        {tab === "live" && <LiveOpsPanel />}

        {tab === "logs" && <LogPanel devices={settings?.devices ?? []} dbSources={settings?.dbSources ?? []} onInsertComposer={onInsertComposer} onOpenWorkbench={openLogsBench} />}

        {tab === "health" && <HealthPanel onOpenSettings={onOpenSettings} />}
        {tab === "browser" && <BrowserConsolePanel onInsertComposer={onInsertComposer} />}

        {tab === "jobs" && <JobsPanel />}

        {tab === "devices" && (
          <div className="ndv__card">
            <div className="ndv__card-title">设备清单{project ? <span style={{ fontWeight: 400, fontSize: 11 }}> · {project.name}</span> : ""}（{devices.length}）</div>
            <div style={{ display: "flex", gap: 6, marginBottom: 8 }}>
              <input className="mem-input" style={{ flex: 1, minWidth: 0 }} value={locateTarget}
                onChange={e => setLocateTarget(e.target.value)} onKeyDown={e => { if (e.key === "Enter") void runLocate(); }}
                placeholder="定位 IP / MAC —— 接在哪台设备哪个端口（全网 ARP 扇出）" />
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void runLocate()}>{locateBusy ? "定位中…" : "定位"}</span>
              <span className="btn btn--secondary btn--small" role="button" title="清单期望 vs 健康采集：缺谁一眼可见" onClick={() => void runExpected()}>{expBusy ? "比对中…" : "期望状态"}</span>
            </div>
            {lastInspection && (
              <div className="ndv__meta">上次巡检：{lastInspection.title}（{String(lastInspection.created_at ?? "").slice(5, 16).replace("T", " ")}）</div>
            )}
            {devices.length === 0 && (
              <div className="ndv__empty">
                <div className="ndv__empty-title">还没有设备</div>
                <div className="ndv__empty-desc">录入设备后这里按分组展示全网资产——未录入的地址 AI 不可见、不可连。</div>
                <span className="btn btn--primary btn--small" role="button" onClick={() => onOpenSettings("netdev")}>打开 设置 → 运维</span>
              </div>
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
                  >{healthMap[d.name] && (
                    <span className={`ndv__dot ${!healthMap[d.name].reachable ? "ndv__dot--down" : (healthMap[d.name].interfaces ?? []).some(i => i.adminUp && !i.operUp) ? "ndv__dot--warn" : "ndv__dot--ok"}`} title={healthMap[d.name].reachable ? "健康轮询：在线" : "健康轮询：不可达"} />
                  )}<span className="ndv__device-name">{d.name}{d.kind ? <span style={{ opacity: 0.65, fontSize: 10, marginLeft: 4 }}>·{d.kind}</span> : ""}</span><span className="ndv__device-addr">{d.address}</span></div>
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
            {selected && <BackupTimeline device={selected} />}
          </div>
        )}

        {tab === "context" && (
          allDevices.length === 0 ? <GettingStarted onOpenSettings={onOpenSettings} /> :
          devices.length === 0 ? (
            <div className="ndv__card ndv__card--dim">项目「{project?.name}」的设备组（{project?.groups.join("、")}）内还没有设备——标题栏可切回「全部」，或在 设置 → 运维 给设备分组。</div>
          ) :
          selectedDevice ? (
            <div className="ndv__card">
              <div className="ndv__card-title">{selectedDevice.name}
                {(cardSeries["if_down"] ?? []).length > 1 && <Sparkline points={cardSeries["if_down"]} bad />}
                {(cardSeries["reachable"] ?? []).length > 1 && <Sparkline points={cardSeries["reachable"]} />}
                <span className="ndv__card-sub">· {selectedDevice.vendor}/{selectedDevice.os} · {selectedDevice.address}{(selectedDevice.via ?? []).length ? " · 经 " + (selectedDevice.via ?? []).join("→") : ""}</span></div>
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
              {/* 带外启动器（§6.3）：只启动本地浏览器/RDP 客户端，点击入审计。 */}
              {(selectedDevice.vendor === "windows" || selectedDevice.vendor === "vmware" || selectedDevice.vendor === "redfish" || (selectedDevice.oobUrl ?? "") !== "") && (
                <>
                  <div className="ndv__group-label">带外（§6.3）</div>
                  <div className="ndv__quick-cmds">
                    <span
                      className="btn btn--secondary btn--small"
                      role="button"
                      title="启动本地工具/浏览器直达带外面（产品内不实现 RDP/VNC）；点击入审计"
                      onClick={() => void app.NetDevOOBLaunch(selectedDevice.name).then(what => setErr(`[SYS] 带外已启动：${what}`)).catch(e => setErr(String(e)))}
                    >
                      {selectedDevice.vendor === "windows" ? "🖥 RDP 直达"
                        : selectedDevice.vendor === "vmware" ? "ESXi Web UI"
                        : selectedDevice.vendor === "redfish" ? "BMC Web 控制台"
                        : "Web 控制台"}
                    </span>
                  </div>
                </>
              )}
              {(selectedDevice.vendor === "linux" || selectedDevice.vendor === "windows") && (
                <>
                  <div className="ndv__group-label">主机体检与 Web 服务</div>
                  <div className="ndv__quick-cmds">
                    <span
                      className="btn btn--primary btn--small"
                      role="button"
                      title="一键体检电池（登录/持久化/水位/时钟），异常自动进「发现」"
                      onClick={() => void runTriageOne(selectedDevice.name)}
                    >{triageOneBusy === selectedDevice.name ? "体检中…" : "一键体检"}</span>
                    <span
                      className="btn btn--secondary btn--small"
                      role="button"
                      title="人工终端（§6.1）：PTY 直达设备，全程审计录制——在主区终端面板开设备页签"
                      onClick={() => {
                        // §10.5：设备卡「终端」→ 主区终端面板设备页签。App 层监听
                        // 该事件并打开面板；PTY 生命周期由 DeviceTerminal 管理。
                        window.dispatchEvent(new CustomEvent("fairpeer:netdev-terminal", { detail: { device: selectedDevice.name } }));
                      }}
                    >⌨ 终端</span>
                    {selectedDevice.vendor === "linux" && (
                      <span
                        className="btn btn--secondary btn--small"
                        role="button"
                        title="文件下载（§6.2 只读）：白名单路径浏览 + 拉取落盘，全程审计"
                        onClick={() => setFilePicker(filePicker === selectedDevice.name ? "" : selectedDevice.name)}
                      >📁 文件</span>
                    )}
                    {selectedDevice.vendor === "linux" && WEB_QUICK.map(cmd => (
                      <span key={cmd} className="btn btn--secondary btn--small" role="button" onClick={() => void runQuick(selectedDevice.name, cmd)}>{cmd}</span>
                    ))}
                  </div>
                  {filePicker === selectedDevice.name && (
                    <div className="ndv__card" style={{ marginTop: 6 }}>
                      <div className="ndv__card-title">
                        📁 文件下载（只读） — {selectedDevice.name}
                        <span className="btn btn--secondary btn--small" role="button" style={{ marginLeft: "auto" }} onClick={() => { setFilePicker(""); setFileListing(null); setFileNote(""); }}>关闭</span>
                      </div>
                      <div className="ndv__hint">路径限白名单（/var/log 与设备 log_paths 配置项）；拉取后选位置落盘，全程审计。</div>
                      <div style={{ display: "flex", gap: 6, marginTop: 6 }}>
                        <input
                          className="mem-input"
                          value={filePath}
                          onChange={(e) => setFilePath(e.target.value)}
                          placeholder="/var/log 或白名单内目录"
                          spellCheck={false}
                          style={{ flex: 1 }}
                        />
                        <span className="btn btn--secondary btn--small" role="button" onClick={() => void (async () => {
                          setFileBusy("browse");
                          try {
                            setFileListing(await app.NetDevSFTPBrowse(selectedDevice.name, filePath));
                            setFileNote("");
                          } catch (e) { setFileNote(String(e)); }
                          finally { setFileBusy(""); }
                        })()}>{fileBusy === "browse" ? "浏览中…" : "浏览"}</span>
                        <span className="btn btn--primary btn--small" role="button" onClick={() => void (async () => {
                          setFileBusy("download");
                          try {
                            const saved = await app.NetDevSFTPDownload(selectedDevice.name, filePath);
                            setFileNote(saved ? `已保存到 ${saved}` : "已取消");
                          } catch (e) { setFileNote(String(e)); }
                          finally { setFileBusy(""); }
                        })()}>{fileBusy === "download" ? "下载中…" : "下载"}</span>
                      </div>
                      {fileListing && fileListing.length > 0 && (
                        <div className="ndv__audit-scroll" style={{ maxHeight: 140, marginTop: 6 }}>
                          {fileListing.map(f => (
                            <div key={f} className="ndv__audit-row" role="button" style={{ cursor: "pointer" }} onClick={() => setFilePath(f)}>
                              <span className="ndv__audit-cmd">{f}</span>
                            </div>
                          ))}
                        </div>
                      )}
                      {fileNote && <div className="ndv__hint" style={{ marginTop: 6 }}>{fileNote}</div>}
                    </div>
                  )}
                  {/* 配置文件管理（§7.3）：变更区——快照/两版本 diff/环境 Drift；修改走 file-upload 提案。 */}
                  {selectedDevice.vendor === "linux" && (
                    <>
                      <div className="ndv__group-label" style={{ marginTop: 10 }}>配置文件管理（§7.3）</div>
                      <SrvConfCard device={selectedDevice} peers={(settings?.devices ?? []).filter(d => (d.configPaths ?? []).length > 0)} />
                    </>
                  )}
                </>
              )}
              {(selectedDevice.kind ?? "") === "docker" && (
                <>
                  <div className="ndv__group-label">Docker（只读 API）</div>
                  <div className="ndv__quick-cmds">
                    {DOCKER_QUICK.map(q => (
                      <span key={q.what} className="btn btn--secondary btn--small" role="button"
                        onClick={() => void runApiQuick(q.label, () => app.NetDevDockerGet(selectedDevice.name, q.what, "", 100))}>{q.label}</span>
                    ))}
                  </div>
                </>
              )}
              {(selectedDevice.kind ?? "") === "k8s" && (
                <>
                  <div className="ndv__group-label">Kubernetes（只读 API）</div>
                  <div className="ndv__quick-cmds">
                    {K8S_QUICK.map(q => (
                      <span key={q.what} className="btn btn--secondary btn--small" role="button"
                        onClick={() => void runApiQuick(q.label, () => app.NetDevK8sGet(selectedDevice.name, q.what, "", "", 100))}>{q.label}</span>
                    ))}
                  </div>
                </>
              )}
              {(selectedDevice.kind ?? "") === "firewall" && (
                <>
                  <div className="ndv__group-label">防火墙（FortiOS REST 只读）</div>
                  <div className="ndv__quick-cmds">
                    {FW_QUICK.map(q => (
                      <span key={q.what} className="btn btn--secondary btn--small" role="button"
                        onClick={() => void runApiQuick(q.label, () => app.NetDevFirewallGet(selectedDevice.name, q.what))}>{q.label}</span>
                    ))}
                  </div>
                </>
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
            <ScenarioHub
              onOpenAlertWizard={() => setAlertWizardOpen(true)}
              onDiagnose={() => { onInsertComposer?.("请对以下故障现象做诊断，先给结论再给证据链："); openDockTabFn("live"); }}
              onInspect={() => { void runInspection(); openDockTabFn("findings"); }}
              onLogs={() => openDockTabFn("logs")}
              onBaseline={() => { void runBaseline(); openDockTabFn("findings"); }}
              onGolden={() => openDockTabFn("devices")}
            />
            {alertWizardOpen && settings && (
              <AlertSetupWizard
                settings={settings}
                onClose={() => setAlertWizardOpen(false)}
                onSaved={() => void reload()}
                onOpenSettings={onOpenSettings}
                onFinish={() => openDockTabFn("health")}
              />
            )}
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
            <div className="ndv__panel-actions">
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void loadPlan()}>
                刷新 IP 规划视图
              </span>
              <span className="btn btn--secondary btn--small" role="button" onClick={() => void genTopology()}>
                {topoBusy ? "采集邻居表中…" : "LLDP 实测校准（逐台读取邻居表）"}
              </span>
            </div>
            {topoNotice && (
              <div className="ndv__hint ndv__hint--flush" style={{ marginBottom: 8, color: "var(--accent)" }}>{topoNotice}</div>
            )}
            {topo && <TopologyMap graph={scopedTopo ?? topo} selected={selected} selectedAddr={selectedDevice?.address} onPick={pickFromTopo} health={healthMap} />}
            {!topo && !topoBusy && (
              <div className="ndv__hint ndv__hint--flush">
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
            <div className="ndv__panel-actions">
              <span
                className={`btn btn--small ${aggView ? "btn--primary" : "btn--secondary"}`}
                role="button"
                title="同根因键收拢成一条队列项（syslog:设备:类 / triage:设备…），展开看成员；误报学习计数 ≥2 显示降级徽标"
                onClick={() => setAggView(v => !v)}
              >{aggView ? "聚合队列" : "平铺列表"}</span>
              <span
                className="btn btn--secondary btn--small"
                role="button"
                title="逐台读取 running-config（只读密封路径，已脱敏）并用本地规则核查：Telnet/SNMPv1v2c/明文密码/SSHv1/NTP/Syslog。命中项进入发现，附证据与修复建议（修复走提案）。"
                onClick={() => void runBaseline()}
              >{baseBusy ? "核查中…" : "安全基线核查"}</span>
              <span
                className="btn btn--secondary btn--small"
                role="button"
                title="清单 vendor/os/model × 已导入 CVE feed → 命中 Finding（feed 在设置 → 运维 高级页粘贴导入，产品不分发）"
                onClick={() => void (async () => {
                  setBaseBusy(true);
                  try {
                    const f = await app.NetDevCVESweep();
                    if (f) setErr(`[SYS] CVE SWEEP: ${f.title}`);
                    await reload();
                  } catch (e) { setErr(String(e)); } finally { setBaseBusy(false); }
                })()}
              >CVE 匹配</span>
            </div>
            {scopedFindings.length === 0 && <div className="ndv__hint ndv__hint--flush">{project ? `>> 项目「${project.name}」暂无数据。` : <>&gt;&gt; NULL_DATA: 证据池为空。Agent 诊断输出及全量巡检报告将在此落盘 (Enforced Evidence-Based)。</>}</div>}
            {aggView && aggs.length > 0 ? aggs.map(a => <AggRow key={a.key} a={a} onChanged={() => void reload()} />) : scopedFindings.slice(0, 20).map(f => <FindingRow key={f.id} f={f} onResolved={() => void reload()} />)}
          </div>
        )}

        {tab === "proposals" && (
          <>
          <div className="ndv__card">
            <div className="ndv__card-title">提案（{scopedProposals.length}）{project && <span style={{ fontWeight: 400, fontSize: 11 }}> · {project.name}</span>}</div>
            {scopedProposals.length === 0 && <div className="ndv__hint ndv__hint--flush">{project ? `>> 项目「${project.name}」暂无提案。` : <>&gt;&gt; NULL_DATA: 无挂起提案。在交互终端向 Agent 下达变更意图 (netdev_propose) 进入审批流。</>}</div>}
            {scopedProposals.slice(0, 10).map(p => <ProposalRow key={p.id} p={p} onDone={() => void reload()} />)}
            <div className="ndv__hint ndv__hint--flush">批准 / 执行 / 回滚在行内直接操作；agent 只能起草，执行权永远在人。</div>
          </div>
          <div className="ndv__card">
            <div className="ndv__card-title">
              🌗 割接（§7.2）{cutovers.some(c => c.status === "running" || c.status === "hold") && <span className="ndv__warn"> · 进行中</span>}
              <span
                className="btn btn--secondary btn--small"
                role="button"
                style={{ marginLeft: "auto" }}
                title="倒计时 runbook + 语义验证门 + 回退决策点（步骤引用已批准提案）"
                onClick={() => { setCutoverId(""); setBench("chat"); }}
              >发起割接</span>
            </div>
            {cutovers.length === 0 && <div className="ndv__hint ndv__hint--flush">把深夜割接从「Word 文档 + 对讲机」变成可执行、可回退、可复盘的流程——结束后自动出前后基线对比报告。</div>}
            {cutovers.slice(0, 5).map(c => (
              <div key={c.id} className="ndv__device" style={{ gap: 8, alignItems: "center" }}>
                <span className={`ndv__dot ${c.status === "running" ? "ndv__dot--warn" : c.status === "done" ? "ndv__dot--ok" : c.status === "hold" ? "ndv__dot--down" : ""}`} style={{ background: c.status === "hold" ? "var(--warn)" : undefined }} />
                <span className="ndv__device-name" role="button" onClick={() => { setCutoverId(c.id); setBench("chat"); }}>{c.name}</span>
                <span className="ndv__device-addr">
                  {c.status === "running" || c.status === "hold"
                    ? `⏱ ${Math.max(0, Math.round((new Date(c.deadline).getTime() - Date.now()) / 60000))} 分钟`
                    : c.status}
                  {(c.steps ?? []).length > 0 ? ` · ${(c.steps ?? []).length} 步` : ""}
                </span>
              </div>
            ))}
          </div>
          <TemplateCard devices={devices} onDrafted={() => void reload()} />
          </>
        )}

        {tab === "audit" && (
          <div className="ndv__card">
            <div className="ndv__card-title">审计（最近 {audit.length} 条）<AuditChainBadge /></div>
            
            <div className="ndv__audit-stats">
              <span className="ndv__bottom-item"><span className="ndv__ok">今日只读</span> {readCount}</span>
              <span className="ndv__bottom-item"><span className="ndv__ok">写操作(直连)</span> <span className="ndv__zero">{writeCount}</span></span>
              <span className="ndv__bottom-item">
                提案 {pendingCount > 0 ? <span className="ndv__warn">{pendingCount} 待处理</span> : <span className="ndv__zero">0</span>}
              </span>
            </div>

            <div className="ndv__audit-scroll">
              <div className="ndv__audit-table">
                <div className="ndv__audit-row ndv__audit-row--head">
                  <span>时间</span><span>设备</span><span>命令</span><span>分类</span><span>状态</span>
                </div>
                {audit.length === 0 ? (
                  <div className="ndv__audit-empty">
                    还没有操作记录。<br />
                    每条设备命令（含拒绝）都会在这里留下时间、命令与字节数。
                  </div>
                ) : audit.slice(0, 100).map((a, i) => (
                  <div key={`${a.time}-${i}`} className="ndv__audit-row" title={a.error || a.command}>
                    <span className="ndv__audit-time">{String(a.time ?? "").slice(11, 19) || String(a.time ?? "").slice(5, 16)}</span>
                    <span className="ndv__audit-dev">{a.device}</span>
                    <span className="ndv__audit-cmd">{a.command}</span>
                    <span style={{ color: classColorForAudit(a.class) }}>{auditClassLabel(a.class)}</span>
                    <span style={{ color: a.status === "ok" ? "var(--ok)" : a.status === "refused" ? "var(--danger)" : "var(--warn)" }}>{a.status}</span>
                  </div>
                ))}
              </div>
            </div>
            <div className="ndv__hint ndv__hint--flush" style={{ marginTop: 8 }}>结构性只读：写命令无执行路径 · 全量审计中。审计只记命令与字节数，输出原文不入档（脱敏在进入上下文之前完成）。</div>
          </div>
        )}
        </div>
      </div>
      )}
    </div>
  );
}

// BackupTimeline: the selected device's config version vault — list, one-click
// backup now, and a two-pick diff. Restore stays proposal-shaped on purpose:
// "从此版本恢复" hands the version to the agent as DRAFT context (the human
// approves the actual change in the 提案 pipeline).
function BackupTimeline({ device }: { device: string }) {
  const [versions, setVersions] = useState<{ id: string; at: string; bytes: number; lines: number }[] | null>(null);
  const [pick, setPick] = useState<string[]>([]);
  const [diff, setDiff] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [golden, setGolden] = useState<{ set: boolean; at: string; lines: number } | null>(null);
  const [driftNote, setDriftNote] = useState("");
  const reload = useCallback(async () => {
    try {
      setVersions(await app.NetDevBackups(device));
      setGolden(await app.NetDevGoldenInfo(device));
      setErr("");
    } catch (e) {
      setErr(String(e));
    }
  }, [device]);
  useEffect(() => { setVersions(null); setPick([]); setDiff(""); setDriftNote(""); void reload(); }, [reload]);
  const togglePick = (id: string) => {
    setDiff("");
    setPick(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id].slice(-2));
  };
  const showDiff = async () => {
    if (pick.length !== 2) return;
    setBusy(true);
    try {
      setDiff(await app.NetDevBackupDiff(device, pick[0], pick[1]));
    } catch (e) {
      setErr(String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="ndv__section-row" style={{ flexDirection: "column", alignItems: "stretch", gap: 6 }}>
      <div className="ndv__section-row">
        <div className="ndv__section">配置版本 · {device}</div>
        <span className="btn btn--secondary btn--small ndv__section-btn" role="button"
          onClick={() => { setBusy(true); void app.NetDevRunBackup(device).then(() => reload()).finally(() => setBusy(false)); }}>
          {busy ? "执行中…" : "立即备份"}
        </span>
        {golden?.set && (
          <span className="btn btn--secondary btn--small ndv__section-btn" role="button"
            title="现场拉取运行配置并与基线对比；漂移自动生成/恢复「发现」"
            onClick={() => { setBusy(true); setDriftNote(""); void app.NetDevGoldenCheck(device).then(r => setDriftNote(r)).catch(e => setErr(String(e))).finally(() => setBusy(false)); }}>
            {busy ? "对比中…" : "检查漂移"}
          </span>
        )}
      </div>
      {golden?.set && (
        <div className="ndv__meta">基线：{String(golden.at).slice(0, 16).replace("T", " ")} · {golden.lines} 行（从下方任一版本「设为基线」可更换）</div>
      )}
      {driftNote && <div className="ndv__meta" style={{ whiteSpace: "pre-wrap" }}>{driftNote}</div>}
      {err && <div className="ndv__hint">{err}</div>}
      {(versions ?? []).length === 0 && <div className="ndv__hint">暂无版本——「立即备份」或等备份周期（设置 → 定时任务）。</div>}
      {(versions ?? []).slice(0, 10).map(v => (
        <div key={v.id} className="ndv__device" role="button" onClick={() => togglePick(v.id)}
          style={{ cursor: "pointer", outline: pick.includes(v.id) ? "1px solid var(--accent)" : "none" }}>
          <span className="ndv__device-addr">{String(v.at ?? "").slice(5, 16).replace("T", " ")}</span>
          <span className="ndv__device-addr">{v.lines} 行 · {v.bytes} B</span>
          {pick.includes(v.id) && <span className="ndv__meta" style={{ marginLeft: "auto" }}>已选 {pick.indexOf(v.id) + 1}/2</span>}
          <span className="btn btn--secondary btn--small" role="button"
            title="以此版本为期望配置（golden），后续「检查漂移」以它为准"
            onClick={() => { setBusy(true); void app.NetDevSetGoldenFromBackup(device, v.id).then(() => reload()).catch(e => setErr(String(e))).finally(() => setBusy(false)); }}>
            设为基线
          </span>
        </div>
      ))}
      {pick.length === 2 && (
        <>
          <span className="btn btn--secondary btn--small" role="button" onClick={() => void showDiff()}>{busy ? "对比中…" : "对比两个版本"}</span>
          {diff && <pre className="ndv__diff ndv__pre" style={{ maxHeight: 220 }}>{diff}</pre>}
        </>
      )}
    </div>
  );
}

// AuditChainBadge: hash-chain integrity verdict (B-batch) — one query on
// mount; green = chain intact, red = tampering suspected (firstBroken says where).
function AuditChainBadge() {
  const [st, setSt] = useState<{ total: number; chained: number; ok: boolean; firstBroken?: string } | null>(null);
  useEffect(() => {
    app.NetDevAuditVerify().then(setSt).catch(() => setSt(null));
  }, []);
  if (!st) return null;
  if (st.chained === 0) return <span style={{ marginLeft: 8, fontSize: 11, fontWeight: 400, opacity: 0.7 }}>链校验：尚无链式条目（新条目自动上链）</span>;
  return (
    <span style={{ marginLeft: 8, fontSize: 11, fontWeight: 400, color: st.ok ? "var(--ok)" : "var(--danger)" }} title={st.firstBroken ?? ""}>
      {st.ok ? `链校验通过（${st.chained} 条已上链）` : `链校验失败：${st.firstBroken}`}
    </span>
  );
}

// ScenarioHub — 场景引导中心：每个场景一句话说明 + 一个动作，动作要么打开
// 向导（告警接入）、要么填好提示词并打开对应页卡（诊断/巡检/基线）。
function ScenarioHub({ onOpenAlertWizard, onDiagnose, onInspect, onLogs, onBaseline, onGolden }: {
  onOpenAlertWizard: () => void;
  onDiagnose: () => void;
  onInspect: () => void;
  onLogs: () => void;
  onBaseline: () => void;
  onGolden: () => void;
}) {
  const cards: { icon: string; title: string; desc: string; action: string; primary?: boolean; run: () => void }[] = [
    { icon: "⏰", title: "告警接入", desc: "五步向导：轮询 → 规则 → 通知 → 测试。设备出事，群里收到消息。", action: "开始向导", primary: true, run: onOpenAlertWizard },
    { icon: "🔍", title: "故障诊断", desc: "描述现象，AI 用只读命令收集证据给出结论；变更走提案。", action: "开始诊断", run: onDiagnose },
    { icon: "🧪", title: "全网巡检", desc: "对全部设备跑只读电池，汇总成一条带证据的发现。", action: "立即巡检", run: onInspect },
    { icon: "📜", title: "日志排查", desc: "六源日志（文件/journald/容器/K8s/数据库/syslog）读取与实时跟踪。", action: "打开日志", run: onLogs },
    { icon: "🛡", title: "安全基线", desc: "核查 Telnet/明文密码/SSHv1 等弱配置，命中进发现附修复建议。", action: "运行核查", run: onBaseline },
    { icon: "📐", title: "配置漂移", desc: "把某个备份设为基线，巡检自动对比运行配置，漂移即告警。", action: "设备页卡设置", run: onGolden },
  ];
  return (
    <div className="ndv__card">
      <div className="ndv__card-title">场景引导</div>
      <div className="mem-hint" style={{ marginBottom: 8 }}>从这里开始——每张卡是一个完整场景，点动作直达。</div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
        {cards.map(c => (
          <div key={c.title} style={{ border: "1px solid var(--border)", borderRadius: "var(--radius-sm)", padding: 8, display: "flex", flexDirection: "column", gap: 4 }}>
            <div style={{ fontSize: 12.5, fontWeight: 600 }}>{c.icon} {c.title}</div>
            <div style={{ fontSize: 11, opacity: 0.75, flex: 1 }}>{c.desc}</div>
            <span className={`btn btn--small ${c.primary ? "btn--primary" : "btn--secondary"}`} role="button" style={{ alignSelf: "flex-start" }} onClick={c.run}>{c.action}</span>
          </div>
        ))}
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
        <UnifiedDiff value={diff} maxHeight={360} showToggle={false} />
      )}
    </div>
  );
}

// FindingRow: one diagnosis conclusion with its expandable evidence chain
// (device ▸ command ▸ output) — the review surface, ported from the settings
// FindingCenter so operations never leave the 运维 page.
const SEV_LABEL: Record<string, string> = { info: "ℹ 提示", warning: "⚠ 警告", critical: "🔴 严重" };
// AggRow — 聚合队列行（§4.10 同类聚合）：「link-flap ×17（3 台）」一条队列项
// 展开看成员；ack/误报操作直达成员 Finding。
function AggRow({ a, onChanged }: { a: NetDevAggregatedFinding; onChanged?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="ndv__finding" style={{ "--sev": SEV_COLOR[a.severity] ?? SEV_COLOR.info } as React.CSSProperties}>
      <div className="ndv__finding-title" role="button" onClick={() => setOpen(!open)}>
        <span style={{ color: SEV_COLOR[a.severity] ?? SEV_COLOR.info, marginRight: 6 }}>{SEV_LABEL[a.severity] ?? SEV_LABEL.info}</span>
        {a.title} <span style={{ fontWeight: 700 }}>×{a.count}</span>
        <span style={{ fontWeight: 400, opacity: 0.7 }}>{a.devices.length} 台 · 未闭环 {a.open}{open ? " ▲" : " ▼"}</span>
        {a.suppressed >= 2 && <span className="ndv__badge ndv__badge--warn" style={{ marginLeft: 6 }} title={`已被误报学习 ${a.suppressed} 次，新告警自动降级`}>已降级</span>}
      </div>
      {open && (
        <div style={{ marginTop: 4, display: "flex", flexDirection: "column", gap: 4 }}>
          {(a.members ?? []).map(f => (
            <FindingRow key={f.id} f={f} onResolved={onChanged} />
          ))}
        </div>
      )}
    </div>
  );
}

function FindingRow({ f, onResolved }: { f: NetDevFinding; onResolved?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="ndv__finding" style={{ "--sev": SEV_COLOR[f.severity] ?? SEV_COLOR.info, opacity: f.status === "resolved" ? 0.5 : 1 } as React.CSSProperties}>
      <div className="ndv__finding-title" role="button" onClick={() => setOpen(!open)}>
        <span style={{ color: SEV_COLOR[f.severity] ?? SEV_COLOR.info, marginRight: 6 }}>{SEV_LABEL[f.severity] ?? SEV_LABEL.info}</span>
        {f.title} <span style={{ fontWeight: 400, opacity: 0.7 }}>证据 {f.evidence?.length ?? 0} 条 {open ? "▲" : "▼"}</span>
        {f.status === "active" && <span className="ndv__badge ndv__badge--warn" style={{ marginLeft: 6 }}>告警中</span>}
        {f.status === "resolved" && <span style={{ marginLeft: 6, fontSize: 10, color: "var(--ok)", border: "1px solid var(--ok)", borderRadius: "var(--radius-pill)", padding: "0 6px" }}>已恢复</span>}
      </div>
      <div style={{ display: "flex", gap: 6, alignSelf: "flex-end", marginBottom: 4 }}>
        {f.status === "active" && (
          <span className="btn btn--secondary btn--small" role="button" title="确认收到，进入处理中（§4.10 状态机）"
            onClick={() => { void app.NetDevAckFinding(f.id).then(() => onResolved?.()); }}>确认</span>
        )}
        {(f.status === "active" || f.status === "ack") && (
          <span className="btn btn--secondary btn--small" role="button" title="标记误报：登记抑制键，同类自动告警此后降级（误报学习）"
            onClick={() => { void app.NetDevFalsePositiveFinding(f.id).then(() => onResolved?.()); }}>误报</span>
        )}
        <span className="btn btn--secondary btn--small" role="button" title="以此 Finding 为首条时间线条目开一个排查案例"
          onClick={() => {
            window.dispatchEvent(new CustomEvent("fairpeer:netdev-case", { detail: { title: f.title, device: (f.devices ?? [])[0] ?? "", text: `${f.severity}｜${f.title}｜${(f.detail ?? "").slice(0, 120)}`, ref: f.id } }));
            window.dispatchEvent(new CustomEvent("fairpeer:netdev-bench", { detail: "sec" }));
          }}>建案例</span>
        {f.status === "active" && (
          <span className="btn btn--secondary btn--small" role="button"
            onClick={() => { void app.NetDevResolveFinding(f.id).then(() => onResolved?.()); }}>标记已处理</span>
        )}
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
function TopologyMap({ graph, selected, selectedAddr, onPick, health }: { graph: NetDevTopologyGraph; selected?: string; selectedAddr?: string; onPick?: (name: string) => void; health?: Record<string, NetDevDeviceHealth> }) {
  const W = 520, H_MAX = 360;
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
  // Height ends at the last USED band — unused bottom bands must not inflate
  // the viewBox (the SVG scales to dock width, so excess height magnifies the
  // empty tail below the graph).
  const lastUsedBand = bands.reduce((acc, b, i) => (b.length > 0 ? i : acc), -1);
  const H = lastUsedBand >= 0 ? Math.min(H_MAX, Math.max(150, bandY[lastUsedBand] + 34)) : 110;
  const node = (name: string) => nodes.find(v => v.name === name);
  const TIER_LABEL = ["核心层", "汇聚层", "接入层", "未纳管邻居"];
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: "100%", marginTop: 8, display: "block" }}>
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
              stroke="var(--fg-faint)"
              strokeWidth={1.2}
              opacity={0.4}
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
                  : v.managed ? "color-mix(in srgb, var(--fg) 5%, var(--bg-elev))" : "transparent"}
                stroke={(() => {
                  const hv = v.managed ? health?.[v.name] : undefined;
                  if (hv && !hv.reachable) return "var(--danger)";
                  if (hv && (hv.interfaces ?? []).some(i => i.adminUp && !i.operUp)) return "var(--warn)";
                  return sel
                    ? "var(--accent)"
                    : v.managed ? (tier === 0 ? "var(--accent)" : "var(--fg-faint)") : "var(--border)";
                })()}
                strokeWidth={sel ? 1.6 : v.managed && tier === 0 ? 1.5 : 1}
                strokeDasharray={v.managed ? undefined : "3 3"}
              />
              <text x={p.x} y={p.y + 3.5} textAnchor="middle" fontSize={9} fill={v.managed ? "var(--fg)" : "var(--fg-faint)"}>
                {v.name.length > 9 ? v.name.slice(0, 9) + "…" : v.name}
              </text>
              <title>{`${v.name}${nv?.device_ip ? " · " + nv.device_ip : ""}${v.subnet ? " · " + v.subnet : ""}${v.managed ? " · 纳管" : " · 未纳管"}${health?.[v.name] ? (health[v.name].reachable ? " · 健康在线" : " · 不可达") : ""}${v.tier !== undefined && v.tier >= 0 ? " · 分层为本地推断" : ` · 连接 ${degree.get(v.name) ?? 0}`}${v.managed && onPick ? " · 点击查看设备" : ""}`}</title>
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
