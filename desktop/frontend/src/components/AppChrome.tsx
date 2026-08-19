import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { CalendarDays, Code2, Command, Minus, Network, PanelLeft, PanelRight, Square, TerminalSquare, X } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { TabMeta } from "../lib/types";
import { useT } from "../lib/i18n";

type DesktopPlatform = "darwin" | "windows" | "linux";

interface AppChromeProps {
  platform: DesktopPlatform;
  browserPreviewChrome: boolean;
  tabs: TabMeta[];
  activeTabId?: string;
  revealActiveSignal: number;
  commandCompact: boolean;
  sidebarTogglePressed: boolean;
  sidebarExpandBlocked: boolean;
  sidebarCollapsed: boolean;
  sidebarToggleTitle: string;
  workspacePanelMaximized: boolean;
  workspacePanelRenderable: boolean;
  workspaceTogglePressed: boolean;
  workspacePanelLabel: string;
  onToggleSidebar: () => void;
  onToggleWorkspacePanel: () => void;
  onTabChange: (tabId: string) => void;
  onTabClose: (tabId: string) => void;
  onTabsClose: (tabIds: string[], nextActiveTabId?: string) => void;
  onTabsReorder: (tabIds: string[]) => void;
  onNewTab: () => void;
  // Topicbar contents (title · workspace · model + actions) merged into the
  // chrome's center slot — the ZCode single-header pattern, now shared by every
  // profile: dev's topicbar, cowork's topicbar (task center only), or netdev's
  // title bar. Null when a mode has nothing to show there.
  center?: ReactNode;
  // Terminal console toggle (Ctrl+`) — the button rides left of the workspace
  // toggle; the panel itself mounts under the composer in App.
  terminalOpen?: boolean;
  onToggleTerminal?: () => void;
  // Command palette (⌘K / Ctrl+K) — icon button in the right cluster.
  onOpenPalette?: () => void;
  // Product profile (dev/cowork). profile is the active tab's mode; onSwitchProfile
  // rebuilds the controller with the new profile's bundle.
  profile: string;
  onSwitchProfile: (name: string) => void;
  // workspaceToggleHidden (netdev): the mode's right dock is intrinsic (always
  // mounted, no open/close state), so the workspace panel toggle has nothing to
  // drive there. Every other chrome control stays identical across profiles.
  workspaceToggleHidden?: boolean;
}

export function AppChrome({
  platform,
  browserPreviewChrome,
  center,
  terminalOpen,
  onToggleTerminal,
  onOpenPalette,
  sidebarTogglePressed,
  sidebarExpandBlocked,
  sidebarCollapsed,
  sidebarToggleTitle,
  workspacePanelMaximized,
  workspacePanelRenderable,
  workspaceTogglePressed,
  workspacePanelLabel,
  onToggleSidebar,
  onToggleWorkspacePanel,
  profile,
  onSwitchProfile,
  workspaceToggleHidden,
}: AppChromeProps) {
  const t = useT();
  const darwinChrome = platform === "darwin";
  const showWindowsPreviewControls = browserPreviewChrome && platform === "windows";
  const chromeClassName = [
    "app-chrome",
    "app-chrome--tabs",
    darwinChrome ? "app-chrome--darwin-tabs" : "app-chrome--native-tabs",
    !darwinChrome ? "app-chrome--identityless" : "",
    showWindowsPreviewControls ? "app-chrome--preview-window-controls" : "",
    `app-chrome--platform-${platform}`,
  ].filter(Boolean).join(" ");

  // Tab strip removed by product decision (2026-08-18): session switching goes
  // through the sidebar "最近" list (click resumes/switches; open sessions carry
  // a dot), ⌘K palette, and 新建会话 for new ones. TabBar.tsx stays intact for
  // a future opt-in setting if multi-tab power features (drag reorder, batch
  // close, mode chips) are asked for again.
  const tabBar = null;

  return (
    <header className={chromeClassName}>
      {browserPreviewChrome && darwinChrome && (
        <div className="app-chrome__traffic" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      )}
      {darwinChrome && <span className="app-chrome__drag-rail" aria-hidden="true" />}
      {/* Sidebar-expanded: the toggle lives in the sidebar brand row
          (App.tsx / the mode layouts). The chrome copy only exists while the
          sidebar is collapsed, where it anchors the chrome's left edge. */}
      {sidebarCollapsed && (
      <button
        className={[
          "app-chrome__panel-toggle",
          "app-chrome__panel-toggle--left",
          sidebarTogglePressed ? "app-chrome__panel-toggle--pressed" : "",
          sidebarExpandBlocked ? "app-chrome__panel-toggle--blocked" : "",
        ].filter(Boolean).join(" ")}
        type="button"
        onClick={sidebarExpandBlocked ? undefined : onToggleSidebar}
        aria-label={sidebarToggleTitle}
        aria-pressed={!sidebarCollapsed}
        aria-disabled={sidebarExpandBlocked}
      >
        <PanelLeft size={16} />
      </button>
      )}

      {darwinChrome ? (
        <div className="app-chrome__tab-strip app-chrome__tab-strip--darwin">
          {tabBar}
          {center}
        </div>
      ) : (
        <>
          <div className="app-chrome__tab-strip app-chrome__tab-strip--native">
            {tabBar}
            {center}
          </div>
          {/* Compact search button removed (2026-08-18): it duplicated the
              topicbar 命令 palette button — every profile's header carries one. */}
        </>
      )}

      {/* Command palette (⌘K): icon-only, same grammar as its neighbours.
          Rendered in EVERY mode — the chrome's right cluster is part of the
          shared framework (parity with the coding view). */}
      {onOpenPalette && (
        <button
          className="app-chrome__palette-toggle"
          type="button"
          onClick={onOpenPalette}
          aria-label={t("topicBar.command")}
          title={t("topicBar.command")}
        >
          <Command size={16} />
        </button>
      )}
      {/* Terminal toggle (Ctrl+`): sits left of the workspace toggle, matching
          its icon grammar (16px lucide, same hover/active treatment). */}
      {onToggleTerminal && (
        <button
          className={`app-chrome__terminal-toggle${terminalOpen ? " app-chrome__terminal-toggle--active" : ""}`}
          type="button"
          onClick={onToggleTerminal}
          aria-label={t("terminal.toggleTitle")}
          title={t("terminal.toggleTitle")}
          aria-pressed={terminalOpen}
        >
          <TerminalSquare size={16} />
        </button>
      )}
      {!workspaceToggleHidden && !workspacePanelMaximized && (
        <button
          className={[
            "app-chrome__panel-toggle",
            "app-chrome__panel-toggle--right",
            workspacePanelRenderable ? "app-chrome__panel-toggle--active" : "",
            workspaceTogglePressed ? "app-chrome__panel-toggle--pressed" : "",
          ].filter(Boolean).join(" ")}
          type="button"
          onClick={onToggleWorkspacePanel}
          aria-label={workspacePanelLabel}
          aria-pressed={workspacePanelRenderable}
        >
          <PanelRight size={16} />
        </button>
      )}
      {/* Profile segmented switcher: a pill control with a sliding highlight
          indicator. See ProfileSegmented below. Rendered in EVERY mode so mode
          navigation stays identical across profiles (framework parity with the
          coding view). */}
      <ProfileSegmented profile={profile} onSwitchProfile={onSwitchProfile} t={t} />
      {showWindowsPreviewControls && (
        <div className="app-chrome__window-controls app-chrome__window-controls--windows" aria-hidden="true">
          <span className="app-chrome__window-control app-chrome__window-control--minimize">
            <Minus size={12} strokeWidth={1.9} />
          </span>
          <span className="app-chrome__window-control app-chrome__window-control--maximize">
            <Square size={10} strokeWidth={1.8} />
          </span>
          <span className="app-chrome__window-control app-chrome__window-control--close">
            <X size={12} strokeWidth={1.9} />
          </span>
        </div>
      )}
    </header>
  );
}

/* Profile segmented switcher: a pill control with a sliding highlight indicator.
   Config-driven — to add a third entry (e.g. "academic"), push a segment to the
   array and the indicator auto-splits to fit (width = 100%/N via --seg-count);
   the slider tracks activeIdx with translateX(idx * 100%). No CSS/JS change.

   Visual feedback: keeps a LOCAL optimistic active index so clicking a segment
   instantly moves the highlight and flips it to the accent color, without
   waiting for the backend's profile:changed event to round-trip. An effect
   re-syncs the local index whenever the `profile` prop changes (e.g. when the
   switch lands, or when the active tab changes elsewhere), so the indicator
   never desyncs from the real state. */
const PROFILE_SEGMENTS: ReadonlyArray<{
  key: string;
  labelKey: string;
  titleKey: string;
  Icon: LucideIcon;
}> = [
  { key: "dev", labelKey: "cowork.badgeDev", titleKey: "cowork.switchToDev", Icon: Code2 },
  { key: "cowork", labelKey: "cowork.badgeCoWork", titleKey: "cowork.switchToCoWork", Icon: CalendarDays },
  { key: "netdev", labelKey: "cowork.badgeNetDev", titleKey: "cowork.switchToNetDev", Icon: Network },
];

function ProfileSegmented({
  profile,
  onSwitchProfile,
  t,
}: {
  profile: string;
  onSwitchProfile: (name: string) => void;
  t: (key: never, vars?: Record<string, string | number>) => string;
}) {
  const p = profile.toLowerCase();
  const activeKey = p === "cowork" || p === "netdev" ? p : "dev";
  const propIdx = Math.max(
    0,
    PROFILE_SEGMENTS.findIndex((s) => s.key === activeKey),
  );
  // Optimistic local index — updates instantly on click; re-synced from prop.
  const [activeIdx, setActiveIdx] = useState(propIdx);
  useEffect(() => {
    setActiveIdx(propIdx);
  }, [propIdx]);

  return (
    <div
      className="app-chrome__profile-seg"
      role="tablist"
      aria-label="Profile"
      style={{ "--seg-count": PROFILE_SEGMENTS.length } as Record<string, number>}
    >
      <span
        className="app-chrome__profile-seg-indicator"
        style={{ transform: `translateX(${activeIdx * 100}%)` }}
        aria-hidden="true"
      />
      {PROFILE_SEGMENTS.map((seg, i) => (
        <button
          key={seg.key}
          type="button"
          role="tab"
          aria-selected={i === activeIdx}
          className={[
            "app-chrome__profile-seg-item",
            i === activeIdx ? "app-chrome__profile-seg-item--active" : "",
          ].filter(Boolean).join(" ")}
          onClick={() => {
            // Optimistic: move highlight immediately for instant feedback.
            setActiveIdx(i);
            onSwitchProfile(seg.key);
          }}
          title={t(seg.titleKey as never)}
        >
          <seg.Icon size={13} className="app-chrome__profile-seg-icon" />
          {t(seg.labelKey as never)}
        </button>
      ))}
    </div>
  );
}
