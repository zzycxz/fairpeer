// PreviewPane — the right dock's "预览" tab (pane-system spec §3.2, P1).
// Renders a local dev-server page in an iframe: zero native code, identical on
// WebView2 / WKWebView / WebKitGTK. The URL is normally auto-detected from the
// agent's tool output (App.tsx) and may also be entered manually; sites that
// refuse embedding (X-Frame-Options) show the browser's own blank/refused
// state — the toolbar's external-open button is the escape hatch.
import { useEffect, useState } from "react";
import { ExternalLink, Globe, Loader2, MonitorPlay, RefreshCw } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";

function normalizeUrl(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return "";
  if (/^https?:\/\//i.test(trimmed)) return trimmed;
  return `http://${trimmed}`;
}

export function PreviewPane({ url, onUrlCommit }: { url: string; onUrlCommit?: (url: string) => void }) {
  const t = useT();
  const [draft, setDraft] = useState(url);
  const [nonce, setNonce] = useState(0);
  const [managedBusy, setManagedBusy] = useState(false);

  useEffect(() => {
    setDraft(url);
  }, [url]);

  const commit = (raw: string) => {
    const next = normalizeUrl(raw);
    if (!next) return;
    setNonce((v) => v + 1);
    onUrlCommit?.(next);
  };

  if (!url) {
    return (
      <div className="preview-pane preview-pane--empty">
        <Globe size={22} />
        <div className="preview-pane__hint">{t("preview.emptyHint")}</div>
        <form
          className="preview-pane__form"
          onSubmit={(e) => {
            e.preventDefault();
            commit(draft);
          }}
        >
          <input
            className="preview-pane__input"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={t("preview.addressPlaceholder")}
            spellCheck={false}
            autoComplete="off"
            aria-label={t("preview.addressPlaceholder")}
          />
          <button type="submit" className="btn btn--small">{t("preview.manualOpen")}</button>
        </form>
      </div>
    );
  }

  return (
    <div className="preview-pane">
      <div className="preview-pane__toolbar">
        <button
          type="button"
          className="preview-pane__btn"
          onClick={() => setNonce((v) => v + 1)}
          aria-label={t("preview.refresh")}
          title={t("preview.refresh")}
        >
          <RefreshCw size={12} />
        </button>
        <form
          className="preview-pane__addressbar"
          onSubmit={(e) => {
            e.preventDefault();
            commit(draft);
          }}
        >
          <input
            className="preview-pane__address"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            spellCheck={false}
            autoComplete="off"
            aria-label={t("preview.addressPlaceholder")}
          />
        </form>
        <button
          type="button"
          className="preview-pane__btn preview-pane__btn--managed"
          onClick={() => {
            if (managedBusy) return;
            setManagedBusy(true);
            void app.OpenURLInManagedBrowser(url)
              .catch(() => { /* surfaced by the settings panel's browser state */ })
              .finally(() => setManagedBusy(false));
          }}
          disabled={managedBusy}
          aria-label={t("preview.openManaged")}
          title={t("preview.openManaged")}
        >
          {managedBusy ? <Loader2 size={12} className="composer-phase__spin" /> : <MonitorPlay size={12} />}
        </button>
        <button
          type="button"
          className="preview-pane__btn"
          onClick={() => window.open(url, "_blank", "noopener")}
          aria-label={t("preview.openExternal")}
          title={t("preview.openExternal")}
        >
          <ExternalLink size={12} />
        </button>
      </div>
      <iframe
        key={nonce}
        className="preview-pane__frame"
        src={url}
        title={t("preview.tabTitle")}
      />
    </div>
  );
}
