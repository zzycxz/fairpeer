// DeviceTerminal — 人工终端（NETDEV_SPEC_V2 §6.1）的 xterm 面：订阅
// "netdev:humantty" 输出流、输入经 NetDevHumanTTYWrite 回传 PTY、关闭即
// NetDevHumanTTYStop（卸载即关会话——录制落盘、审计收尾；重开时重查
// NetDevHumanTTYStatus 决定是否重开 PTY，不重复占 VTY）。挂在主区终端面
// 板的设备页签里（§10.5：页签名 = 设备名 + 路由徽标，录制徽标常亮）。
import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { app, onNetdevHumanTTY } from "../lib/bridge";
import { t } from "../lib/i18n";

// The PTY opens at a fixed size (humantty.go humanTTYCols/Rows); the panel
// does not stretch device tabs, so no fit addon is needed.
const DEV_COLS = 120;
const DEV_ROWS = 30;

export function DeviceTerminal({ device }: { device: string }) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [ready, setReady] = useState(false);
  const [err, setErr] = useState("");
  const [exited, setExited] = useState(false);

  useEffect(() => {
    if (!device) return;
    let alive = true;
    setErr("");
    setExited(false);
    setReady(false);

    // Attach to an existing session if the panel was collapsed and reopened;
    // only start a fresh PTY when none is live.
    void (async () => {
      try {
        const live = (await app.NetDevHumanTTYStatus()) ?? [];
        if (!alive) return;
        if (!live.some((s) => s.device === device)) {
          await app.NetDevHumanTTYStart(device);
        }
        if (alive) setReady(true);
      } catch (e) {
        if (alive) setErr(String(e));
      }
    })();

    const term = new Terminal({
      fontSize: 12,
      cursorBlink: true,
      convertEol: false,
      cols: DEV_COLS,
      rows: DEV_ROWS,
      theme: { background: "#12131a", foreground: "#cdd6f4" },
    });
    if (hostRef.current) term.open(hostRef.current);
    term.onData((d) => {
      void app.NetDevHumanTTYWrite(device, d).catch(() => setExited(true));
    });
    term.onResize(({ cols, rows }) => {
      void app.NetDevHumanTTYResize(device, cols, rows).catch(() => {});
    });

    const off = onNetdevHumanTTY((ev) => {
      if (ev.device === device) term.write(ev.chunk);
    });

    return () => {
      alive = false;
      off();
      term.dispose();
      void app.NetDevHumanTTYStop(device).catch(() => {});
    };
  }, [device]);

  return (
    <div className="termsession" aria-label={t("ndv.tty.aria", { device })}>
      <div className="termsession__bar">
        <span className="termsession__title">{t("ndv.tty.recTitle", { device })}</span>
        {err && <span className="termsession__err">{err}</span>}
        {exited && <span className="termsession__exited">{t("ndv.tty.exited")}</span>}
        <span className="termsession__rec-note">{ready ? t("ndv.tty.online") : t("ndv.live.connecting")}</span>
      </div>
      <div ref={hostRef} className="termsession__host" />
    </div>
  );
}
