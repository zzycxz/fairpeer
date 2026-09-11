// TerminalSession (upgrade spec 3-4) is one interactive PTY tab backed by the
// ConPTY bindings (PTYCreate/Write/Read/Resize/Kill/Alive) and rendered with
// xterm.js. The backend already handles the pseudoconsole lifecycle; this
// component bridges xterm's input/resize events to the bindings and polls
// output back into the xterm instance.
//
// xterm.js needs its CSS imported once. Rendering uses the xterm core's DOM
// renderer on ALL platforms — no renderer addon (canvas/webgl) is installed.
// PTY size tracks the panel via @xterm/addon-fit: fit() runs after open() and
// on a debounced ResizeObserver, and the resulting term.onResize event is what
// re-invokes PTYResize on the backend.
import { useEffect, useRef, useState } from "react";
import { XCircle } from "lucide-react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { getResolvedTheme, type ResolvedTheme } from "../lib/theme";
import { useResolvedTheme } from "../lib/useResolvedTheme";

// xterm colors follow the app theme (lib/theme.ts): Catppuccin Mocha for dark
// (the previous fixed palette), Latte for light so the panel reads on a light
// workspace. Only bg/fg are pinned — the rest of the 16-color ANSI palette
// stays xterm's default, which works on both.
const XTERM_THEMES: Record<ResolvedTheme, { background: string; foreground: string }> = {
  dark: { background: "#1e1e2e", foreground: "#cdd6f4" },
  light: { background: "#eff1f5", foreground: "#4c4f69" },
};

export function TerminalSession({
  onClose,
  tabId,
  embedded = false,
  onPtyUnavailable,
}: {
  onClose?: () => void;
  tabId?: string;
  // Embedded inside a TerminalPanel tab: no own bar/close chrome — the panel
  // tab's × handles the kill (unmount cleanup already PTYKills).
  embedded?: boolean;
  // Called once when the PTY backend refuses creation (e.g. no ConPTY/unix
  // pty support in this build). The parent should degrade the tab to a
  // non-PTY mode; the raw error stays in the console, not the UI.
  onPtyUnavailable?: () => void;
}) {
  const t = useT();
  const appTheme = useResolvedTheme();
  const hostRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<Terminal | null>(null);
  const ptyIdRef = useRef<number>(-1);
  const [exited, setExited] = useState(false);
  const [error, setError] = useState("");
  // The boot effect runs once ([] deps); mirror the latest callback so a
  // fallback call always reaches the parent's current closure.
  const onPtyUnavailableRef = useRef(onPtyUnavailable);
  onPtyUnavailableRef.current = onPtyUnavailable;

  useEffect(() => {
    if (!hostRef.current || termRef.current) return;

    const term = new Terminal({
      fontSize: 13,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace',
      cursorBlink: true,
      convertEol: false,
      theme: XTERM_THEMES[getResolvedTheme()],
    });
    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(hostRef.current);
    // IME composition guard (WebKitGTK has known composition quirks): while
    // an IME candidate window is active, keydown events must NOT reach the
    // PTY — otherwise Enter-to-commit or candidate navigation leaks into the
    // remote shell. Mirrors Composer.tsx's isComposing/keyCode-229 triple.
    let imeGraceUntil = 0;
    hostRef.current.addEventListener("compositionend", () => {
      // WebKitGTK/Safari fire the confirm-Enter with isComposing already
      // false — give the committed IME text a short grace before any key
      // reaches the PTY (mirrors Composer.tsx IME_CONFIRM_GRACE_MS).
      imeGraceUntil = performance.now() + 100;
    });
    term.attachCustomKeyEventHandler((ev) => {
      if (ev.type === "keydown" && (ev.isComposing || ev.keyCode === 229)) {
        return false;
      }
      if (ev.type === "keydown" && performance.now() < imeGraceUntil) {
        return false;
      }
      return true;
    });
    termRef.current = term;
    term.focus();

    // Keep the PTY size in sync with the panel: fit() only acts when the
    // proposed grid actually differs from the current one, so a hidden/zero
    // host or a no-op resize never spams PTYResize. term.onResize (registered
    // in boot) forwards the new cols/rows to the backend.
    const fitToHost = () => {
      try {
        const proposed = fitAddon.proposeDimensions();
        if (proposed && (proposed.cols !== term.cols || proposed.rows !== term.rows)) {
          // addon-fit clamps a zero-size host to a degenerate 2x1 grid during
          // panel open — don't fit (or boot a PTY) from that.
          if (proposed.cols < 4 || proposed.rows < 2) return;
          fitAddon.fit();
        }
      } catch {
        // host has no dimensions yet (e.g. panel hidden) — skip this pass
      }
    };
    fitToHost();

    // Debounced host-size tracking (panel resizes / dock toggles).
    let resizeTimer: number | undefined;
    const resizeObserver = new ResizeObserver(() => {
      if (resizeTimer !== undefined) window.clearTimeout(resizeTimer);
      resizeTimer = window.setTimeout(fitToHost, 150);
    });
    resizeObserver.observe(hostRef.current);

    let pollTimer: number | undefined;
    let alive = true;

    const boot = async () => {
      try {
        const cols = term.cols || 120;
        const rows = term.rows || 30;
        const scopeTabId = typeof tabId === "string" ? tabId : "";
        const ptyId = scopeTabId
          ? await app.PTYCreateForTab(scopeTabId, cols, rows).catch(() => app.PTYCreate(cols, rows))
          : await app.PTYCreate(cols, rows);
        if (!alive) {
          void app.PTYKill(ptyId).catch(() => {});
          return;
        }
        ptyIdRef.current = ptyId;

        // Forward xterm input (keystrokes) to the PTY stdin.
        term.onData((data) => {
          void app.PTYWrite(ptyId, data).catch(() => {});
        });

        // Forward resizes.
        term.onResize(({ cols, rows }) => {
          void app.PTYResize(ptyId, cols, rows).catch(() => {});
        });

        // Poll output — 50ms interval keeps typing responsive without
        // hammering the bridge.
        const poll = async () => {
          if (!alive) return;
          try {
            const [data, still] = await app.PTYRead(ptyId);
            if (data) term.write(data);
            if (!still) {
              setExited(true);
              return;
            }
          } catch {
            // PTY closed
          }
          if (alive) pollTimer = window.setTimeout(poll, 50);
        };
        void poll();
      } catch (e) {
        // PTY creation failed (backend without pty support, tab env missing,
        // resource limits…). Log the raw Go error for diagnosis and let the
        // parent fall the tab back to pipe mode with a localized note.
        console.warn("PTY create failed", e);
        setError(String(e));
        if (alive) onPtyUnavailableRef.current?.();
      }
    };
    void boot();

    return () => {
      alive = false;
      if (pollTimer !== undefined) window.clearTimeout(pollTimer);
      if (resizeTimer !== undefined) window.clearTimeout(resizeTimer);
      resizeObserver.disconnect();
      if (ptyIdRef.current >= 0) {
        void app.PTYKill(ptyIdRef.current).catch(() => {});
      }
      term.dispose();
      termRef.current = null;
    };
  }, []);

  // Re-apply colors when the app theme flips (settings change, or an OS scheme
  // change under "auto") — xterm takes option updates live, no rebuild needed.
  useEffect(() => {
    if (termRef.current) termRef.current.options.theme = XTERM_THEMES[appTheme];
  }, [appTheme]);

  if (embedded) {
    return (
      <div className="termsession termsession--embedded">
        <div ref={hostRef} className="termsession__host" role="region" aria-label={t("terminal.title")} />
        {(error || exited) && (
          <div className="termsession__overlay">
            {error && <span className="termsession__err">{error}</span>}
            {exited && !error && <span className="termsession__exited">{t("terminal.exited")}</span>}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="termsession">
      <div className="termsession__bar">
        <span className="termsession__title">{t("terminal.title")}</span>
        {error && <span className="termsession__err">{error}</span>}
        {exited && <span className="termsession__exited">{t("terminal.exited")}</span>}
        {onClose && (
          <button className="termsession__close" onClick={onClose} title={t("common.close")}>
            <XCircle size={14} />
          </button>
        )}
      </div>
      <div ref={hostRef} className="termsession__host" role="region" aria-label={t("terminal.title")} />
    </div>
  );
}
