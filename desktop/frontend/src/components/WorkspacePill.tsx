// WorkspacePill — the sidebar's top-left identity slot (2026-08-19 design A).
// Replaces the static mode wordmark with the highest-value fact for that
// pixel: WHERE am I working. State machine (dev): active project → 全局 →
// 选择项目 (known list) → 打开项目 (fresh); office/netdev are static labels.
// The dropdown doubles as the mode switcher, retiring the top-right segmented
// control's monopoly on profile switching.
import { useState } from "react";
import { CalendarDays, ChevronDown, Code2, FolderPlus, Globe2, Network } from "lucide-react";
import { useT } from "../lib/i18n";
import { ContextMenu, type ContextMenuItem } from "./ContextMenu";

export interface PillProject {
  label: string;
  root: string;
  color?: string;
}

export type WorkspacePillState = "project" | "global" | "choose" | "open" | "static";

export function WorkspacePill({
  state,
  label,
  dotColor,
  projects = [],
  currentMode,
  onPickProject,
  onPickGlobal,
  onAddProject,
  onSwitchMode,
  globalHint,
}: {
  state: WorkspacePillState;
  label: string;
  dotColor?: string;
  projects?: PillProject[];
  currentMode: "dev" | "cowork" | "netdev";
  onPickProject?: (root: string) => void;
  onPickGlobal?: () => void;
  onAddProject?: () => void;
  onSwitchMode?: (mode: "dev" | "cowork" | "netdev") => void;
  // Storage-location tooltip for the 全局 entry (answers "files live where?").
  globalHint?: string;
}) {
  const t = useT();
  const [point, setPoint] = useState<{ left: number; top: number } | null>(null);

  const open = (e: React.MouseEvent<HTMLButtonElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const menuWidth = 220;
    const left = Math.max(8, Math.min(r.left, window.innerWidth - menuWidth - 8));
    setPoint({ left, top: r.bottom + 4 });
  };

  const items: ContextMenuItem[] = [];
  {
    // The dropdown is mode-independent (2026-08-21): projects/global/add show
    // wherever handlers exist — picking one from office/netdev funnels the
    // user into the coding profile with that workspace open.
    for (const p of projects) {
      items.push({
        key: `p-${p.root}`,
        icon: <span className="workspace-pill__menu-dot" style={{ background: p.color || "var(--accent)" }} aria-hidden="true" />,
        label: p.label,
        onSelect: () => onPickProject?.(p.root),
      });
    }
    if (onPickGlobal) {
      items.push({ key: "global", icon: <Globe2 size={13} />, label: t("sidebar.pillGlobal"), title: globalHint, onSelect: onPickGlobal });
    }
    if (onAddProject) {
      items.push({ key: "add-project", icon: <FolderPlus size={13} />, label: t("sidebar.pillAddProject"), onSelect: onAddProject });
    }
    if (onSwitchMode && (projects.length > 0 || onPickGlobal || onAddProject)) {
      items.push({ type: "separator", key: "sep-modes" });
    }
  }
  if (onSwitchMode) {
    const modes: Array<{ id: "dev" | "cowork" | "netdev"; label: string; icon: React.ReactNode }> = [
      { id: "dev", label: t("sidebar.modeDev"), icon: <Code2 size={13} /> },
      { id: "cowork", label: t("sidebar.modeCowork"), icon: <CalendarDays size={13} /> },
      { id: "netdev", label: t("sidebar.modeNetdev"), icon: <Network size={13} /> },
    ];
    for (const m of modes) {
      if (m.id === currentMode) continue;
      items.push({
        key: `mode-${m.id}`,
        icon: m.icon,
        label: m.label,
        onSelect: () => onSwitchMode(m.id),
      });
    }
  }

  const empty = state === "choose" || state === "open";

  return (
    <>
      <button
        type="button"
        className={`workspace-pill${empty ? " workspace-pill--empty" : ""}${state === "static" ? " workspace-pill--static" : ""}`}
        onClick={items.length > 0 ? open : undefined}
        title={label}
        aria-haspopup={items.length > 0 ? "menu" : undefined}
      >
        {state === "project" && (
          <span className="workspace-pill__dot" style={{ background: dotColor || "var(--accent)" }} aria-hidden="true" />
        )}
        {state === "global" && <Globe2 size={11} className="workspace-pill__globe" aria-hidden="true" />}
        <span className="workspace-pill__label">{label}</span>
        {items.length > 0 && <ChevronDown size={11} className="workspace-pill__chev" aria-hidden="true" />}
      </button>
      <ContextMenu
        open={point !== null}
        point={point}
        minWidth={200}
        ariaLabel={t("sidebar.pillMenu")}
        onClose={() => setPoint(null)}
        items={items.map((it) => ({ ...it, onSelect: it.type === "separator" ? undefined : () => { setPoint(null); it.onSelect?.(); } } as ContextMenuItem))}
      />
    </>
  );
}
