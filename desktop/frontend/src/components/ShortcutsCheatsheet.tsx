// Minimal cheatsheet shell: title, platform note, real open-binding summary,
// close. The full per-action table lives in the settings/shortcuts surfaces —
// this only has to stop leaking internal GOOS values and quote the REAL
// binding (Ctrl/Cmd+/, see lib/keyboardShortcuts.ts DEFAULT_BINDINGS).
const PLATFORM_LABELS: Record<string, string> = {
  darwin: "macOS",
  windows: "Windows",
  linux: "Linux",
};

export function ShortcutsCheatsheet({ open, platform, onClose, t }: { open: boolean; platform?: string; onClose: () => void; t?: unknown }) {
  if (!open) return null;
  const translate = t as ((key: string) => string) | undefined;
  const platformLabel = PLATFORM_LABELS[platform ?? ""] ?? platform ?? "unknown";
  return (
    <div className="shortcuts-cheatsheet">
      <h3>{translate?.("shortcuts.cheatsheetTitle") || translate?.("shortcuts.title") || "Keyboard Shortcuts"}</h3>
      <p>{translate?.("shortcuts.cheatsheetSummary") || "Press Ctrl/Cmd+/ anywhere to open this panel."}</p>
      <p>Platform: {platformLabel}</p>
      <button onClick={onClose}>{translate?.("common.close") || "Close"}</button>
    </div>
  );
}
