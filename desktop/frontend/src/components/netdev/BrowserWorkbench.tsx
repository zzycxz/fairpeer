// BrowserWorkbench — the ops browser's center workbench (第五个工作台，
// 与日志/安全/大屏同机制): Chrome tab switching + the big mirror + the
// element list bound together. Tab switches matter because they invalidate
// element refs — this view re-fetches elements on every switch, so 拿元素
// and 切页卡 live on one screen. Closes via Esc / 对话 chip / the close
// button, like every other bench.
import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { Monitor, RefreshCw, X } from "lucide-react";
import { app } from "../../lib/bridge";
import { browserMirrorSnapshot, subscribeBrowserMirror } from "../../lib/browserMirror";
import { useT } from "../../lib/i18n";
import type { BrowserConsoleLogEntry, BrowserConsoleTab, BrowserNetEntry } from "../../lib/types";


export function BrowserWorkbench({ hidden, onClose }: { hidden: boolean; onClose: () => void }) {
  const t = useT();
  const mirror = useSyncExternalStore(subscribeBrowserMirror, browserMirrorSnapshot);

  const [tabs, setTabs] = useState<BrowserConsoleTab[]>([]);
  const [consoleId, setConsoleId] = useState("");
  const [img, setImg] = useState<string | undefined>(undefined);
  // Bottom pane (F12 slice): page console messages + network requests.
  const [paneTab, setPaneTab] = useState<"console" | "net">("console");
  const [logs, setLogs] = useState<BrowserConsoleLogEntry[]>([]);
  const [netEntries, setNetEntries] = useState<BrowserNetEntry[]>([]);
  const [source, setSource] = useState<string>("console");
  const [busy, setBusy] = useState("");

  // Tabs + session: refresh on mount, after switches, and periodically while
  // visible (agent auto-follow / manual browsing in the controlled browser
  // both change the tab set without this view acting).
  const refreshTabs = useCallback(async () => {
    try {
      const [st, tb] = await Promise.all([
        app.BrowserConsoleState(),
        app.BrowserConsoleTabs().catch(() => [] as BrowserConsoleTab[]),
      ]);
      setConsoleId(st.session_id);
      setTabs(st.open ? tb : []);
    } catch { /* closed session → empty list */ }
  }, []);

  useEffect(() => {
    if (hidden) return;
    void refreshTabs();
    const timer = window.setInterval(() => void refreshTabs(), 8000);
    return () => window.clearInterval(timer);
  }, [hidden, refreshTabs]);

  useEffect(() => {
    if (hidden) return;
    const load = () => void app.BrowserConsoleDevTools().then((v) => {
      setLogs(v.logs ?? []);
      setNetEntries(v.net ?? []);
    }).catch(() => undefined);
    load();
    const timer = window.setInterval(load, 2000);
    return () => window.clearInterval(timer);
  }, [hidden]);

  // Console mirror: poll a fresh screenshot while visible (manual browsing in
  // the controlled browser shows up near-live, not only after fairpeer acts).
  useEffect(() => {
    if (hidden || source !== "console") return;
    void app.BrowserConsoleScreenshot().then(setImg).catch(() => undefined);
    const timer = window.setInterval(() => {
      void app.BrowserConsoleScreenshot().then(setImg).catch(() => undefined);
    }, 5000);
    return () => window.clearInterval(timer);
  }, [hidden, source]);

  // A watched agent session that ended drops its bucket (browserMirror evicts
  // on the kernel's end event) — fall back to the console view instead of
  // showing the empty-state for a source that no longer exists.
  useEffect(() => {
    if (source !== "console" && !mirror.sessions[source]) setSource("console");
  }, [source, mirror.sessions]);

  const switchTab = (index: number) => {
    setBusy("switch");
    void app.BrowserConsoleSwitchTab(index)
      .then(() => {
        // Elements live in the right dock; refs die with the old page, so
        // broadcast the switch and let the dock re-fetch its element list.
        // The 1-based index + title ride along so the panel can record a
        // switch_tab step (a real flow action, not bookkeeping).
        const switched = tabs.find((tb) => tb.index === index);
        window.dispatchEvent(new CustomEvent("fairpeer:browser-console-changed", {
          detail: { index, title: switched?.title ?? "" },
        }));
        void refreshTabs();
      })
      .catch(() => undefined)
      .finally(() => setBusy(""));
  };

  const agentIds = Object.keys(mirror.sessions).filter((id) => id && id !== consoleId);

// fmtTime renders an entry stamp as HH:MM:SS local time (the F12 convention:
// when it happened matters more than the date for a live ops view).
function fmtTime(unixMillis: number): string {
  if (!unixMillis) return "";
  const d = new Date(unixMillis);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}:${String(d.getSeconds()).padStart(2, "0")}`;
}

  if (hidden) return null;

  return (
    <div className="ndv-wb">
      <div className="ndv-wb__head">
        <div className="ndv-brc__tabs ndv-wb__tabs">
          {tabs.map((tb) => (
            <button
              key={tb.index}
              type="button"
              className={`ndv-brc__tab${tb.current ? " ndv-brc__tab--cur" : ""}`}
              title={tb.url}
              disabled={tb.current || busy === "switch"}
              onClick={() => switchTab(tb.index)}
            >
              <span className="ndv-brc__tab-n">{tb.index}</span>
              <span className="ndv-brc__tab-title">{tb.title || tb.url}</span>
            </button>
          ))}
          {tabs.length === 0 && <span className="ndv-wb__hint">{t("brc.sessionClosed")}</span>}
        </div>
        {agentIds.length > 0 && (
          <div className="ndv-brc__tabs">
            <button
              type="button"
              className={`ndv-brc__tab${source === "console" ? " ndv-brc__tab--cur" : ""}`}
              onClick={() => setSource("console")}
            >
              <span className="ndv-brc__tab-title">{t("brc.srcConsole")}</span>
            </button>
            {agentIds.map((id) => (
              <button
                key={id}
                type="button"
                className={`ndv-brc__tab${source === id ? " ndv-brc__tab--cur" : ""}`}
                title={mirror.sessions[id].url}
                onClick={() => setSource(id)}
              >
                <span className="ndv-brc__tab-n">{t("brc.srcAgent")}</span>
                <span className="ndv-brc__tab-title">{id}</span>
              </button>
            ))}
          </div>
        )}
        <div className="ndv-wb__tools">
          <button type="button" className="btn btn--secondary btn--small" title={t("brc.viewerRefresh")} onClick={() => {
            void refreshTabs();
            if (source === "console") void app.BrowserConsoleScreenshot().then(setImg).catch(() => undefined);
          }}>
            <RefreshCw size={12} />
          </button>
          <button type="button" className="btn btn--secondary btn--small" title={t("brc.viewerClose")} onClick={onClose}>
            <X size={12} />
          </button>
        </div>
      </div>
      <div className="ndv-wb__body">
        <div className="ndv-wb__mirror" style={{ flex: "3 1 0", minHeight: 0 }}>
          {source === "console" ? (
            (mirror.sessions[consoleId]?.image || img) ? (
              <img src={mirror.sessions[consoleId]?.image || img} alt="" />
            ) : (
              <div className="ndv-wb__empty"><Monitor size={16} />{t("brc.viewerEmpty")}</div>
            )
          ) : mirror.sessions[source]?.image ? (
            <img src={mirror.sessions[source].image} alt="" />
          ) : (
            <div className="ndv-wb__empty">{t("brc.srcAgentEmpty")}</div>
          )}
        </div>
        <div className="ndv-wb__pane">
          <div className="ndv-wb__pane-head">
            <button type="button" className={`ndv-brc__subtab${paneTab === "console" ? " ndv-brc__subtab--active" : ""}`} onClick={() => setPaneTab("console")}>
              <span>{t("brc.wbConsole")}</span>
              {logs.filter((l) => l.type === "error" || l.type === "exception").length > 0 && (
                <span className="ndv-wb__errcount">{logs.filter((l) => l.type === "error" || l.type === "exception").length}</span>
              )}
            </button>
            <button type="button" className={`ndv-brc__subtab${paneTab === "net" ? " ndv-brc__subtab--active" : ""}`} onClick={() => setPaneTab("net")}>
              <span>{t("brc.wbNetwork")}</span>
              {netEntries.filter((n) => n.status === "FAIL" || +n.status >= 400).length > 0 && (
                <span className="ndv-wb__errcount">{netEntries.filter((n) => n.status === "FAIL" || +n.status >= 400).length}</span>
              )}
            </button>
          </div>
          <div className="ndv-wb__pane-list">
            {paneTab === "console"
              ? logs.length === 0
                ? <div className="ndv-wb__pane-empty">{t("brc.wbConsoleEmpty")}</div>
                : logs.map((l, i) => (
                    <div key={i} className={`ndv-wb__log ndv-wb__log--${l.type}`}>
                      <span className="ndv-wb__time">{fmtTime(l.time)}</span>
                      <span className="ndv-wb__log-type">{l.type}</span>
                      <span className="ndv-wb__log-text">{l.text}</span>
                    </div>
                  ))
              : netEntries.length === 0
                ? <div className="ndv-wb__pane-empty">{t("brc.wbNetEmpty")}</div>
                : netEntries.map((n, i) => (
                    <div key={i} className={`ndv-wb__net${n.status === "FAIL" || +n.status >= 400 ? " ndv-wb__net--bad" : ""}`}>
                      <span className="ndv-wb__time">{fmtTime(n.time)}</span>
                      <span className="ndv-wb__net-status">{n.status}</span>
                      <span className="ndv-wb__net-method">{n.method}</span>
                      <span className="ndv-wb__net-url">{n.url}</span>
                    </div>
                  ))}
          </div>
        </div>
      </div>
    </div>
  );
}