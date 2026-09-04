// DockTabs — the coding view's workbench-dock tab strip, extracted verbatim so
// the cowork and netdev docks can render the exact same control (browser-style
// tab management, 2026-08-19: only OPEN tabs show, each closable, "+" offers
// the remaining catalog in a dropdown). The markup/classes are identical to
// App.tsx's inline strip (workbench-dock__tools/__tabs/__tabwrap/__tab/...),
// so all three docks read as one control; the coding view itself stays as-is.
import { useEffect, useState, useRef, useMemo, type ReactNode } from "react";
import { Plus, X, ChevronDown, Search } from "lucide-react";
import { ContextMenu, type ContextMenuItem } from "./ContextMenu";
import { AnchoredPopover } from "./AnchoredPopover";

// loadDockTabState reads a persisted open-tab list, falling back to the full
// catalog when absent/corrupt — the coding dock's DOCK_TABS_KEY pattern.
export function loadDockTabState<K extends string>(key: string, catalog: readonly K[]): K[] {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return [...catalog];
    const v = JSON.parse(raw);
    if (Array.isArray(v)) {
      const filtered = v.filter((x): x is K => catalog.includes(x));
      if (filtered.length > 0) return filtered;
    }
  } catch { /* storage unavailable */ }
  return [...catalog];
}

// useDockTabState: open-tab list persisted to localStorage under `key`.
export function useDockTabState<K extends string>(key: string, catalog: readonly K[]) {
  const [openTabs, setOpenTabs] = useState<K[]>(() => loadDockTabState(key, catalog));
  useEffect(() => {
    try {
      localStorage.setItem(key, JSON.stringify(openTabs));
    } catch { /* storage unavailable */ }
  }, [key, openTabs]);
  return [openTabs, setOpenTabs] as const;
}

export interface DockTabDef<K extends string> {
  key: K;
  label: string;
  title?: string;
  icon: ReactNode;
  // Numeric count chip (netdev 发现/变更 pending counts).
  badge?: number;
  // Activity dot, shown while the tab is NOT active (coding's changed-dirty).
  dot?: boolean;
  // Optional catalog group header (netdev's 正在发生/什么状态/需要我决策/留档备查).
  group?: string;
}

export function DockTabs<K extends string>({
  tabs,
  openTabs,
  active,
  onSelect,
  onClose,
  listLabel,
  closeLabel,
  addLabel,
}: {
  // Full catalog, in display order; openTabs references its keys.
  tabs: ReadonlyArray<DockTabDef<K>>;
  openTabs: readonly K[];
  active: K;
  onSelect: (key: K) => void;
  onClose: (key: K) => void;
  listLabel: string;
  closeLabel: string;
  addLabel: string;
}) {
  const [addPoint, setAddPoint] = useState<{ left: number; top: number } | null>(null);
  
  const searchButtonRef = useRef<HTMLButtonElement>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const searchInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (searchOpen) {
      setTimeout(() => searchInputRef.current?.focus(), 50);
    }
  }, [searchOpen]);

  const isOpen = (key: K) => openTabs.includes(key);

  const filteredTabs = useMemo(() => {
    const q = searchQuery.toLowerCase();
    return tabs.filter(def => isOpen(def.key) && (def.label.toLowerCase().includes(q) || (def.title && def.title.toLowerCase().includes(q))));
  }, [tabs, openTabs, searchQuery]);

  return (
    <div className="workbench-dock__tools">
      <button
        ref={searchButtonRef}
        type="button"
        className="workbench-dock__tab-search-btn"
        onClick={() => { setSearchOpen(!searchOpen); setSearchQuery(""); }}
        aria-label="搜索标签页"
        title="搜索标签页"
      >
        <ChevronDown size={14} />
      </button>

      <AnchoredPopover
        open={searchOpen}
        anchorRef={searchButtonRef}
        onClose={() => setSearchOpen(false)}
        className="dock-search-popover"
        align="start"
      >
        <div className="dock-search-popover__header">
          <Search size={14} className="dock-search-popover__icon" />
          <input 
            ref={searchInputRef}
            className="dock-search-popover__input" 
            placeholder="搜索标签页" 
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
          />
        </div>
        <div className="dock-search-popover__list">
          {filteredTabs.length === 0 ? (
            <div className="dock-search-popover__empty">无匹配项</div>
          ) : (
            filteredTabs.map(def => (
              <button
                key={def.key}
                className="dock-search-popover__item"
                onClick={() => {
                  setSearchOpen(false);
                  onSelect(def.key);
                }}
              >
                {def.icon}
                <span className="dock-search-popover__item-label">{def.label}</span>
                <button
                  type="button"
                  className="dock-search-popover__item-close"
                  onClick={(e) => {
                    e.stopPropagation();
                    onClose(def.key);
                  }}
                  title="关闭标签页"
                >
                  <X size={14} />
                </button>
              </button>
            ))
          )}
        </div>
      </AnchoredPopover>

      <div className="workbench-dock__tabs" role="tablist" aria-label={listLabel}>
        {tabs.filter((def) => isOpen(def.key)).map((def) => (
          <div
            key={def.key}
            className={`workbench-dock__tabwrap${active === def.key ? " workbench-dock__tabwrap--active" : ""}`}
          >
            <button
              type="button"
              role="tab"
              aria-selected={active === def.key}
              className={`workbench-dock__tab${active === def.key ? " workbench-dock__tab--active" : ""}`}
              onClick={() => onSelect(def.key)}
              title={def.title ?? def.label}
            >
              <span className="workbench-dock__tab-label">{def.label}</span>
              {def.badge ? (
                <span className="workbench-dock__tab-badge">{def.badge}</span>
              ) : null}
              {def.dot && active !== def.key && (
                <span className="workbench-dock__tab-dot" aria-hidden="true" />
              )}
            </button>
            <button
              type="button"
              className="workbench-dock__tab-close"
              onClick={() => onClose(def.key)}
              aria-label={closeLabel}
              title={closeLabel}
            >
              <X size={15} />
            </button>
          </div>
        ))}
      </div>
      <button
        type="button"
        className="workbench-dock__tab-add"
        onClick={(e) => {
          const r = (e.currentTarget as HTMLButtonElement).getBoundingClientRect();
          // Pre-clamp so the first paint is already on-screen — the menu's own
          // flip logic otherwise corrects one frame late and the dropdown
          // visibly jumps in from the right (same fix as App.tsx's strip).
          const menuWidth = 176;
          const left = Math.max(8, Math.min(r.left, window.innerWidth - menuWidth - 8));
          setAddPoint({ left, top: r.bottom + 4 });
        }}
        aria-label={addLabel}
        title={addLabel}
      >
        <Plus size={13} />
      </button>
      <ContextMenu
        open={addPoint !== null}
        point={addPoint ?? { left: 0, top: 0 }}
        minWidth={160}
        ariaLabel={addLabel}
        onClose={() => setAddPoint(null)}
        items={(() => {
          const items: ContextMenuItem[] = [];
          let lastGroup = "";
          for (const def of tabs) {
            if (def.group && def.group !== lastGroup) {
              items.push({ type: "separator", key: "grp-" + def.group });
              items.push({
                key: "grph-" + def.group,
                label: <span className="workbench-dock__group-header">{def.group}</span>,
                disabled: true,
                onSelect: () => {},
              });
              lastGroup = def.group;
            }
            items.push({
              key: def.key,
              icon: def.icon,
              label: isOpen(def.key) ? `${def.label} (打开)` : def.label,
              onSelect: () => {
                setAddPoint(null);
                onSelect(def.key);
              },
            });
          }
          return items;
        })()}
      />
    </div>
  );
}
