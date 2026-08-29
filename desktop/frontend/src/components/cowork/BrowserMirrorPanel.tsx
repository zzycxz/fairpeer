// BrowserMirrorPanel — the cowork dock's 浏览器 tab (pane-system §3.6
// companion tier): live mirror of the browser the agent drives. Screenshots
// and lifecycle states flow kernel → Wails "browser:mirror" → the App-level
// subscription → the browserMirror module store, so this panel can be closed
// or remounted freely without losing the stream.
import { useSyncExternalStore } from "react";
import { Globe, Loader2 } from "lucide-react";
import { browserMirrorSnapshot, subscribeBrowserMirror } from "../../lib/browserMirror";
import { useT } from "../../lib/i18n";

export function BrowserMirrorPanel() {
  const t = useT();
  const s = useSyncExternalStore(subscribeBrowserMirror, browserMirrorSnapshot);

  if (!s.image && !s.running) {
    return (
      <div className="browser-mirror browser-mirror--empty">
        <Globe size={22} />
        <p>{t("browserMirror.emptyHint")}</p>
      </div>
    );
  }

  return (
    <div className="browser-mirror">
      <div className="browser-mirror__bar">
        <span
          className={`browser-mirror__dot${s.running ? " browser-mirror__dot--live" : ""}`}
          aria-hidden="true"
        />
        <span className="browser-mirror__source">
          {s.source === "auto" ? t("browserMirror.sourceAuto") : t("browserMirror.sourceTool")}
        </span>
        <span className={`browser-mirror__state${s.running ? " browser-mirror__state--live" : ""}`}>
          {s.running ? t("browserMirror.running") : t("browserMirror.ended")}
        </span>
      </div>
      {s.url && (
        <div className="browser-mirror__url" title={s.url}>
          {s.url}
        </div>
      )}
      <div className="browser-mirror__viewport">
        {s.image ? (
          <img className="browser-mirror__img" src={s.image} alt={s.lastText || ""} />
        ) : (
          <div className="browser-mirror__placeholder">
            <Loader2 size={16} className="composer-phase__spin" />
            <span>{t("browserMirror.waitingFrame")}</span>
          </div>
        )}
      </div>
      {s.lastText && (
        <div className="browser-mirror__text" title={s.lastText}>
          {s.lastText}
        </div>
      )}
    </div>
  );
}
