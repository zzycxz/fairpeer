import { useEffect } from "react";

export interface KeyboardShortcut {
  key: string;
  ctrl?: boolean;
  shift?: boolean;
  alt?: boolean;
  description: string;
  action: () => void;
}

// ShortcutCombo represents a parsed key combination.
export interface ShortcutCombo {
  key: string;
  ctrl?: boolean;
  shift?: boolean;
  alt?: boolean;
  meta?: boolean;
}

// ShortcutPlatform determines display style (macOS uses symbols, others use names).
export type ShortcutPlatform = "darwin" | "win32" | "linux";

// DEFAULT_BINDINGS is the action → default combo table. `ctrl: true` means the
// platform's primary modifier — Ctrl on win/linux, Cmd on macOS — so both feel
// native. Matching is exact: an unlisted shift/alt never triggers the action.
const DEFAULT_BINDINGS: Record<string, ShortcutCombo> = {
  "commandPalette.open": { key: "k", ctrl: true },
  "shortcuts.show": { key: "/", ctrl: true },
  "settings.open": { key: ",", ctrl: true },
  "app.newSession": { key: "n", ctrl: true },
  "shell.toggle": { key: "b", ctrl: true },
  "agents.dashboard": { key: "i", ctrl: true },
};

export function registerShortcut(_shortcut: KeyboardShortcut) {
  // Reserved for user-customisable bindings (upgrade spec backlog F6); the
  // action-level hook below covers the built-in defaults for now.
}

export function unregisterShortcut(_key: string) {
  // See registerShortcut.
}

// useGlobalShortcut binds an action's default combo as a capture-phase window
// listener, so the shortcut works regardless of focus (including inside the
// composer) and fires before component-level handlers can swallow it. Unknown
// actions bind nothing — call sites stay valid as the table grows.
export function useGlobalShortcut(action: string, callback: () => void, deps?: unknown[]) {
  useEffect(() => {
    const combo = DEFAULT_BINDINGS[action];
    if (!combo) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return;
      if (event.repeat) return;
      if (event.key.toLowerCase() !== combo.key.toLowerCase()) return;
      // ctrl here means the primary modifier: Ctrl or Cmd, whichever the OS uses.
      const primary = event.ctrlKey || event.metaKey;
      if (!!combo.ctrl !== primary) return;
      if (!!combo.shift !== event.shiftKey) return;
      if (!!combo.alt !== event.altKey) return;
      event.preventDefault();
      event.stopPropagation();
      callback();
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [action, ...(deps ?? [])]);
}

// formatShortcutCombo renders a combo as a human-readable string.
export function formatShortcutCombo(combo: ShortcutCombo, platform: ShortcutPlatform): string {
  const parts = formatShortcutComboParts(combo, platform);
  return platform === "darwin" ? parts.join("") : parts.join("+");
}

// formatShortcutComboParts returns individual key-cap segments.
export function formatShortcutComboParts(combo: ShortcutCombo, platform: ShortcutPlatform): string[] {
  const parts: string[] = [];
  const isDarwin = platform === "darwin";
  if (combo.ctrl) parts.push(isDarwin ? "⌃" : "Ctrl");
  if (combo.alt) parts.push(isDarwin ? "⌥" : "Alt");
  if (combo.shift) parts.push(isDarwin ? "⇧" : "Shift");
  if (combo.meta) parts.push(isDarwin ? "⌘" : "Win");
  parts.push(combo.key.length === 1 ? combo.key.toUpperCase() : combo.key);
  return parts;
}
