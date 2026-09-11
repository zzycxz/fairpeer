import { useEffect, useState, type ReactNode, type PointerEvent as ReactPointerEvent, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { BookOpen, CalendarDays, Mail, PanelLeft, Users, SlidersHorizontal } from "lucide-react";

import { ProfileSegmented } from "../components/AppChrome";
import { useT } from "../lib/i18n";
import { app, onExpertsCollab } from "../lib/bridge";
import { CalendarTaskPanel } from "../components/calendar/CalendarTaskPanel";
import logoSymbol from "../assets/logo-symbol.png";
import { requestBrowserMirrorFocus } from "../lib/browserMirror";
import { BrowserWorkbench } from "../components/netdev/BrowserWorkbench";
import { RagPanel } from "../components/cowork/RagPanel";
import { PreferencePanel } from "../components/cowork/PreferencePanel";
import { MousePointerClick } from "lucide-react";
import { ExpertPanel } from "../components/cowork/ExpertPanel";
import { CoworkDock, requestCoworkDockTab } from "../components/cowork/CoworkDock";
import type { ContextInfo } from "../lib/types";

export type CoWorkPanel = "taskCenter" | "preference" | "calendarTask" | "rag" | "experts" | "skills";

export interface CoWorkLayoutProps {
  mainNode?: ReactNode;
  /** 插入文本到对话输入框（浏览器面板"交给 AI"等入口）。 */
  onInsertComposer?: (text: string) => void;
  footerNode?: ReactNode;
  terminalNode?: ReactNode;
  // Global banners (startup error / update notice) — rendered at the top of
  // the office main so they stay visible in this mode too (the coding chat
  // pane that used to host them is display:none under .app--cowork).
  bannersNode?: ReactNode;
  projectTreeNode?: ReactNode;
  // "Recent" sessions section above the project tree (ui-redesign §4-B4) —
  // App.tsx supplies the same SidebarSessions node used in the coding-mode
  // sidebar so both modes share one interaction grammar. searchNode rides
  // above it (the hoisted project-tree search box).
  sessionsNode?: ReactNode;
  searchNode?: ReactNode;
  rightDockOpen?: boolean;
  sidebarCollapsed?: boolean;
  onNewSession?: () => void;
  onSwitchMode?: (mode: "dev" | "cowork" | "netdev") => void;

  onPickProject?: (root: string) => void;
  onAddProject?: () => void;
  // Sidebar collapse — the brand-row toggle mirrors the coding view's
  // (toggleSidebar + its title live in App.tsx).
  onToggleSidebar?: () => void;
  sidebarToggleTitle?: string;
  dockCwd?: string;
  dockMaximized?: boolean;
  dockOnClose?: () => void;
  dockOnToggleMaximized?: () => void;
  // 侧栏「邮件」直达：dock 可能被用户关掉，先请求重开再切页签
  //（netdev 侧栏 onDockOpen 同款语义）。
  onDockOpen?: () => void;
  // Right dock width resizer — the cowork dock shares the same width state and
  // drag logic as the coding-mode workspace panel, so App.tsx hands the same
  // handlers in. Without these the cowork dock had no resizer and couldn't be
  // resized.
  dockWidth?: number;
  dockMinWidth?: number;
  dockMaxAriaWidth?: number;
  onDockResizeStart?: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onDockResizeKey?: (event: ReactKeyboardEvent<HTMLButtonElement>) => void;
  onDockResetWidth?: () => void;
  // Left sidebar width resizer — absolute-positioned (like coding-mode), driven
  // by the shared sidebarWidth state. The coding-mode .sidebar-resizer is hidden
  // under .app--cowork, so cowork renders its own.
  sidebarWidth?: number;
  sidebarMinWidth?: number;
  sidebarMaxWidth?: number;
  onSidebarResizeStart?: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onSidebarResizeKey?: (event: ReactKeyboardEvent<HTMLButtonElement>) => void;
  onSidebarResetWidth?: () => void;
  // Context overview tab data — forwarded to CoworkDock's "概览" (Overview) tab,
  // which renders the slim ContextPanel (stats strip + turn facts). dockBusy
  // disables the compact button while the active tab is streaming.
  contextInfo?: ContextInfo;
  sessionTokens?: number;
  activeTabId?: string;
  dockRefreshKey?: number;
  dockBusy?: boolean;
}

export function CoWorkLayout({
  mainNode,
  onInsertComposer,
  footerNode,
  terminalNode,
  bannersNode,
  projectTreeNode,
  sessionsNode,
  searchNode,
  rightDockOpen = false,
  sidebarCollapsed = false,
  onSwitchMode,

  onToggleSidebar,
  sidebarToggleTitle,
  dockCwd,
  dockMaximized = false,
  dockOnClose,
  dockOnToggleMaximized,
  onDockOpen,
  dockWidth,
  dockMinWidth,
  dockMaxAriaWidth,
  onDockResizeStart,
  onDockResizeKey,
  onDockResetWidth,
  sidebarWidth,
  sidebarMinWidth,
  sidebarMaxWidth,
  onSidebarResizeStart,
  onSidebarResizeKey,
  onSidebarResetWidth,
  contextInfo,
  sessionTokens,
  activeTabId,
  dockRefreshKey,
  dockBusy,
}: CoWorkLayoutProps) {
  const t = useT();
  const [activePanel, setActivePanel] = useState<CoWorkPanel>("taskCenter");
  // 浏览器工作台常驻挂载（切走只藏不卸，保留观察窗/页卡状态）。
  const [browserBenchEverOpened, setBrowserBenchEverOpened] = useState(false);

  // Esc 退出浏览器工作台回任务中心（对齐原运维工作台的 Esc 语义）。
  useEffect(() => {
    if (activePanel !== "skills") return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !(e.target instanceof HTMLInputElement) && !(e.target instanceof HTMLTextAreaElement)) {
        setActivePanel("taskCenter");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [activePanel]);

  // 进入浏览器工作台时自动展开右 dock 的 browser 页签（控制台面板就位——
  // 复用既有 mirror-focus 通道：开 dock + 切 browser 页）。

  // 迁移接线（浏览器归办公 2026-09-06）：BrowserConsolePanel/BrowserSkillEditor
  // 里的"打开观察窗""去重录"按钮派发 fairpeer:netdev-bench detail="browser"——
  // 原运维监听已撤，办公侧接管：切到浏览器工作台页签。
  useEffect(() => {
    const onBench = (e: Event) => {
      if ((e as CustomEvent<string>).detail === "browser") {
        setBrowserBenchEverOpened(true);
        setActivePanel("skills");
        requestBrowserMirrorFocus();
        onDockOpen?.();
      }
    };
    window.addEventListener("fairpeer:netdev-bench", onBench);
    return () => window.removeEventListener("fairpeer:netdev-bench", onBench);
  }, []);

  // 工作台激活时通知面板收起内联镜像（一屏一画面；netdev 侧同款机制迁来）。
  useEffect(() => {
    window.dispatchEvent(new CustomEvent("fairpeer:netdev-bench-changed", { detail: activePanel === "skills" ? "browser" : "other" }));
  }, [activePanel]);
  const [preferenceOpen, setPreferenceOpen] = useState(false);

  // When an expert-team run kicks off from the chat (the agent called
  // expert_team_run), auto-switch to the experts panel so the user sees the
  // streamed collaboration instead of staring at a frozen chat waiting on a
  // multi-minute tool call. We react only to the run-start event, not every
  // chunk, and only when the user isn't already viewing experts (don't yank
  // them away mid-edit in another panel they deliberately opened). Panel-runs
  // (started from the ExpertPanel itself) already have activePanel=="experts",
  // so this no-ops for them.
  useEffect(() => {
    return onExpertsCollab((ev) => {
      // Auto-switch to the experts panel only when NOT already viewing the
      // expert-session tab (whose ExpertSessionView shows the live stream in the
      // main area). Switching to the sidebar panel here would yank the user out
      // of the ExpertSessionView they're watching.
      if (ev.phase === "expert_start" && activePanel !== "experts" && activePanel !== "taskCenter") {
        setActivePanel("experts");
      }
    });
  }, [activePanel]);

  // Global event listener to reset view to Task Center when opening or creating a session.
  useEffect(() => {
    const handleReset = () => setActivePanel("taskCenter");
    window.addEventListener("cowork:reset-panel", handleReset);
    return () => window.removeEventListener("cowork:reset-panel", handleReset);
  }, []);

  // Cross-navigation: a caller dispatches cowork:open-experts with detail.teamId
  // to jump to that team's expert-session tab (which occupies the main chat area
  // as a group-chat view). Opening the tab makes it active, so the main area
  // swaps to ExpertSessionView automatically. We also reset to taskCenter so
  // the user actually sees the ExpertSessionView — without this, if they were
  // on another panel (preference/calendar/rag), the tab opens but stays hidden.
  useEffect(() => {
    const handleOpenExperts = (e: Event) => {
      const detail = (e as CustomEvent<{ teamId?: string; teamName?: string }>).detail;
      if (detail?.teamId) {
        void app.OpenExpertSessionTab(detail.teamId, detail.teamName ?? "").catch(() => {});
        setActivePanel("taskCenter");
        window.dispatchEvent(new CustomEvent("cowork:reset-panel"));
      }
    };
    window.addEventListener("cowork:open-experts", handleOpenExperts as EventListener);
    return () => window.removeEventListener("cowork:open-experts", handleOpenExperts as EventListener);
  }, []);

  return (
    <div className={`cowork-layout ${sidebarCollapsed ? "cowork-layout--sidebar-collapsed" : ""}`}>
      {/* Left sidebar width resizer — absolute-positioned (mirrors coding-mode
         .sidebar-resizer). Rendered only when not collapsed and handlers wired.
         The coding-mode .sidebar-resizer is display:none under .app--cowork, so
         cowork renders its own to keep the sidebar resizable. */}
      {!sidebarCollapsed && onSidebarResizeStart && (
        <button
          className="sidebar-resizer"
          type="button"
          role="separator"
          aria-orientation="vertical"
          aria-label={t("sidebar.resize")}
          aria-valuemin={sidebarMinWidth}
          aria-valuemax={sidebarMaxWidth}
          aria-valuenow={sidebarWidth}
          onPointerDown={onSidebarResizeStart}
          onKeyDown={onSidebarResizeKey}
          onDoubleClick={onSidebarResetWidth}
        />
      )}
      {/* Left: workspace / knowledge / scheduled / skills.
          Kept mounted (not conditionally removed) on collapse so the grid column
          width animates smoothly — the sidebar slot shrinks to 0px via the
          cowork-layout--sidebar-collapsed class (mirroring coding-mode .layout),
          and the aside's overflow:hidden clips its content as it collapses. */}
      <aside className="cowork-sidebar">
        {/* Brand row — identical classes/markup to the coding view's
            sidebar__brandrow (chrome.css styles them as one): logo, product
            name, new-session ghost button, collapse toggle. Sits OUTSIDE the
            scroll layer so it stays pinned while the sidebar content scrolls
            (the coding .sidebar's structure). */}
        <div className="sidebar__brandrow" title={t("sidebar.modeCowork")}>
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
            profile="cowork"
            onSwitchProfile={onSwitchMode || (() => {})}
            t={t}
          />

        </div>
        <div className="cowork-sidebar__scroll">
          {searchNode}

          {sessionsNode}

          <section className="sidebar__section sidebar__section--projects" style={{ marginBottom: '8px', minHeight: 0, display: 'flex', flexDirection: 'column' }}>
            {projectTreeNode}
          </section>
        </div>

        <section className="cowork-sidebar__group" style={{ marginBottom: 'calc(8px + var(--bottom-bar-height, 36px))', marginTop: 'auto' }}>
          <button
            className={`cowork-sidebar__item ${preferenceOpen ? "cowork-sidebar__item--active" : ""}`}
            onClick={() => setPreferenceOpen(true)}
          >
            <SlidersHorizontal size={14} />
            <span>{t("cowork.preference") || "办公偏好"}</span>
          </button>
          {/* S7-1（SCENARIO_SPEC）：文员/办公技能运行器——浏览器技能库+一键试运行，
                与运维浏览器面板同组件（单源），零 AI 门槛执行报表填报/数据导出等录好的技能。 */}
          <button
            type="button"
            className={`cowork-sidebar__item ${activePanel === "skills" ? "cowork-sidebar__item--active" : ""}`}
            onClick={() => { setBrowserBenchEverOpened(true); setActivePanel("skills"); requestBrowserMirrorFocus(); onDockOpen?.(); }}
            title={t("cowork.panel.skillsTip")}
          >
            <MousePointerClick size={14} />
            <span>{t("cowork.panel.skills")}</span>
          </button>
          <button
            className={`cowork-sidebar__item ${activePanel === "experts" ? "cowork-sidebar__item--active" : ""}`}
            onClick={() => {
              setActivePanel("experts");
              if (dockOnClose) dockOnClose();
            }}
          >
            <Users size={14} />
            <span>{t("cowork.expert") || "专家团"}</span>
          </button>
          {/* 邮件直达（侧栏第 6 项，2026-09-06）：dock 的邮件页签是办公的
              核心面（收件箱+探针），此前只能经 dock 页签到达。rag 面板激活
              时 DefaultDock 未挂载——先回工作台再请求，pending 机制兜底送达。 */}
          <button
            type="button"
            className="cowork-sidebar__item"
            onClick={() => {
              onDockOpen?.();
              setActivePanel("taskCenter");
              requestCoworkDockTab("mail");
            }}
            title={t("cowork.panel.mailTip")}
          >
            <Mail size={14} />
            <span>{t("coworkDock.mail")}</span>
          </button>
          <button
            className={`cowork-sidebar__item ${activePanel === "calendarTask" ? "cowork-sidebar__item--active" : ""}`}
            onClick={() => {
              setActivePanel("calendarTask");
              if (dockOnClose) dockOnClose();
            }}
          >
            <CalendarDays size={14} />
            <span>{t("cowork.calendarAndTasks")}</span>
          </button>
          <button
            className={`cowork-sidebar__item ${activePanel === "rag" ? "cowork-sidebar__item--active" : ""}`}
            onClick={() => setActivePanel("rag")}
          >
            <BookOpen size={14} />
            <span>{t("cowork.knowledgeBase") || "知识库"}</span>
          </button>
        </section>
      </aside>

      {/* Center: dynamic panel based on selection. The chat topicbar no longer
          renders here — it rides the global chrome's center slot and stays up
          across ALL office panels (always-on single header, same as the coding
          view). */}
      <section className="cowork-main">
        {bannersNode}
        {/* taskCenter stays mounted (hidden when inactive) so an expert-session
            run's live stream (ExpertSessionView inside mainNode) isn't torn down
            when the user peeks at another panel. The backend goroutine survives
            panel switches regardless, but the React subscription (onExpertsCollab)
            lives in ExpertSessionView — unmounting it loses mid-run chunks that
            arrive while the user is away. Keeping it mounted (like ExpertPanel
            below) preserves the stream. Hidden via display:none so it doesn't
            capture layout space. */}
        <div style={{ display: activePanel === "taskCenter" ? "contents" : "none" }}>
          <div className="cowork-main__transcript">{mainNode}</div>
          <div className="cowork-main__composer">{footerNode}</div>
          {terminalNode}
        </div>


        {activePanel === "calendarTask" && (
          <CalendarTaskPanel profile="cowork" />
        )}

        {/* ExpertPanel stays mounted (hidden when inactive) so streaming
            conversation state survives panel switches. */}
        <div style={{ display: activePanel === "experts" ? "flex" : "none", flex: 1, minHeight: 0, flexDirection: "column" }}>
          <ExpertPanel />
        </div>

        {activePanel === "rag" && (
          <RagPanel />
        )}

        {/* S7+/浏览器归属办公（用户定稿 2026-09-06）：完整浏览器工作台——
            交互/记录/技能库/巡检四页签 + 观察窗镜像（自运维界面迁入，单源组件）。 */}
        {browserBenchEverOpened && (
          <BrowserWorkbench hidden={activePanel !== "skills"} onClose={() => setActivePanel("taskCenter")} />
        )}
      </section>

      {/* Right dock width resizer — mirrors the coding-mode
         workspace-panel-resizer. Sits in grid column 3 (between main=2 and
         dock=4); dragging updates the shared dock width state. Only rendered
         when the dock is open and the resize handlers are wired. */}
      {rightDockOpen && onDockResizeStart && (
        <button
          className="workspace-panel-resizer"
          type="button"
          role="separator"
          aria-orientation="vertical"
          aria-label={t("rightDock.resize")}
          aria-valuemin={dockMinWidth}
          aria-valuemax={dockMaxAriaWidth}
          aria-valuenow={dockWidth}
          onPointerDown={onDockResizeStart}
          onKeyDown={onDockResizeKey}
          onDoubleClick={onDockResetWidth}
        />
      )}

      {/* Right: tabbed dock (今日 / 邮件 / 文件) or RAG knowledge nav. */}
      {rightDockOpen && (
        <CoworkDock
          cwd={dockCwd}
          maximized={dockMaximized}
          onClose={dockOnClose ?? (() => {})}
          onInsertComposer={onInsertComposer}
          onToggleMaximized={dockOnToggleMaximized ?? (() => {})}
          mode={activePanel === "rag" ? "rag" : "default"}
          onEntityClick={(name) => {
            // Dispatch event so the graph can highlight the node.
            window.dispatchEvent(new CustomEvent("rag:highlight-node", { detail: { name } }));
          }}
          onFileClick={(path) => {
            // Dispatch event so the main panel can open the file.
            window.dispatchEvent(new CustomEvent("rag:open-file", { detail: { path } }));
          }}
          contextInfo={contextInfo}
          sessionTokens={sessionTokens}
          activeTabId={activeTabId}
          dockRefreshKey={dockRefreshKey}
          busy={dockBusy}
        />
      )}

      {preferenceOpen && (
        <PreferencePanel mode="cowork" onClose={() => setPreferenceOpen(false)} />
      )}
    </div>
  );
}
