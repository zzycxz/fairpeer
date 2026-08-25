// TerminalSession (upgrade spec 3-4) is one interactive PTY tab backed by the
// ConPTY bindings (PTYCreate/Write/Read/Resize/Kill/Alive) and rendered with
// xterm.js. The backend already handles the pseudoconsole lifecycle; this
// component bridges xterm's input/resize events to the bindings and polls
// output back into the xterm instance.
//
// xterm.js needs its CSS imported once; Wails' webview handles the canvas
// renderer. The fallback DOM renderer is not imported to keep the bundle
// small — canvas is universally available in WebView2.
import { useEffect, useRef, useState } from "react";
import { XCircle } from "lucide-react";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";

export function TerminalSession({
  onClose,
  tabId,
}: {
  onClose: () => void;
  tabId?: string;
}) {
  const t = useT();
  const hostRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<Terminal | null>(null);
  const ptyIdRef = useRef<number>(-1);
  const [exited, setExited] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!hostRef.current || termRef.current) return;

    const term = new Terminal({
      fontSize: 13,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace',
      cursorBlink: true,
      convertEol: false,
      theme: {
        background: "#1e1e2e",
        foreground: "#cdd6f4",
      },
    });
    term.open(hostRef.current);
    termRef.current = term;
    term.focus();

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
        setError(String(e));
      }
    };
    void boot();

    return () => {
      alive = false;
      if (pollTimer !== undefined) window.clearTimeout(pollTimer);
      if (ptyIdRef.current >= 0) {
        void app.PTYKill(ptyIdRef.current).catch(() => {});
      }
      term.dispose();
      termRef.current = null;
    };
  }, []);

  return (
    <div className="termsession">
      <div className="termsession__bar">
        <span className="termsession__title">{t("terminal.title")}</span>
        {error && <span className="termsession__err">{error}</span>}
        {exited && <span className="termsession__exited">已退出</span>}
        <button className="termsession__close" onClick={onClose} title={t("common.close")}>
          <XCircle size={14} />
        </button>
      </div>
      <div ref={hostRef} className="termsession__host" />
    </div>
  );
}
