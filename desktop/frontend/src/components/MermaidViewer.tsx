import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import mermaid from "mermaid";
import { FileCode, ImageDown, Loader2 } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { getResolvedTheme } from "../lib/theme";
import {
  APP_MERMAID_CONFIG,
  extractMermaidTitle,
  mermaidThemeFor,
  safeMermaidFilename,
  sanitizeMermaidCode,
} from "./mermaidLogic";
import {
  blobToBase64,
  mermaidSvgToPngBlob,
  renderMermaidSvgForExport,
  svgWithBackground,
} from "../lib/mermaidExport";

// Initialize mermaid with the app-wide base settings. The THEME is not set
// here — mermaid.render bakes the theme into the SVG at call time, so a
// module-load theme would freeze every diagram to the theme active at app
// boot. Each render resolves it instead (see mermaidTheme / the render effect).
mermaid.initialize(APP_MERMAID_CONFIG);

// Shared subscription for resolved-theme flips (attribute change on <html>, or
// an OS scheme flip while theme is "auto"). One MutationObserver +
// MediaQueryList serves every mounted viewer regardless of diagram count.
const themeSubs = new Set<() => void>();
let schemeQuery: MediaQueryList | null = null;
let themeAttrObserver: MutationObserver | null = null;
const notifyThemeSubs = () => themeSubs.forEach((fn) => fn());

function subscribeResolvedTheme(onChange: () => void): () => void {
  themeSubs.add(onChange);
  if (themeSubs.size === 1) {
    schemeQuery = window.matchMedia("(prefers-color-scheme: light)");
    schemeQuery.addEventListener("change", notifyThemeSubs);
    themeAttrObserver = new MutationObserver(notifyThemeSubs);
    themeAttrObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  }
  return () => {
    themeSubs.delete(onChange);
    if (themeSubs.size === 0) {
      schemeQuery?.removeEventListener("change", notifyThemeSubs);
      schemeQuery = null;
      themeAttrObserver?.disconnect();
      themeAttrObserver = null;
    }
  };
}

// The backdrop used when rasterizing: read from the on-screen card (the svg
// container itself is transparent — the background lives on .mermaid-viewer)
// so the exported file matches what the user sees; fall back to the dark
// theme's graphite when styles haven't resolved yet.
function containerBackground(el: HTMLElement | null): string {
  const host = el?.closest(".mermaid-viewer") ?? el;
  if (host) {
    const color = getComputedStyle(host).backgroundColor;
    if (color && color !== "transparent" && !/rgba\([^)]*,\s*0\s*\)/.test(color)) return color;
  }
  return "#101010";
}

export function MermaidViewer({ chart }: { chart: string }) {
  const t = useT();
  const containerRef = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState<string>("");
  const [error, setError] = useState<string>("");
  const [busyKind, setBusyKind] = useState<"png" | "svg" | null>(null);
  const [exportError, setExportError] = useState("");
  // Resolved at each render ("default" for light, "dark" for dark) so diagrams
  // follow the app theme; flipping it re-runs the render effect below.
  const mermaidTheme = useSyncExternalStore(
    subscribeResolvedTheme,
    () => mermaidThemeFor(getResolvedTheme()),
  );

  const title = extractMermaidTitle(chart);
  const safeChart = sanitizeMermaidCode(chart);

  useEffect(() => {
    let cancelled = false;

    const renderChart = async () => {
      try {
        // mermaid.render needs a unique id
        const id = `mermaid-${Math.random().toString(36).substr(2, 9)}`;
        mermaid.initialize({ ...APP_MERMAID_CONFIG, theme: mermaidTheme });
        const { svg: renderedSvg } = await mermaid.render(id, safeChart);

        if (!cancelled) {
          setSvg(renderedSvg);
          setError("");
        }
      } catch (err) {
        if (!cancelled) {
          setError((err as Error).message || "Failed to render Mermaid chart");
        }
      }
    };

    if (safeChart) {
      renderChart();
    }

    return () => {
      cancelled = true;
    };
  }, [safeChart, mermaidTheme]);

  const exportDiagram = async (kind: "png" | "svg") => {
    if (busyKind || !safeChart) return;
    setExportError("");
    setBusyKind(kind);
    try {
      const background = containerBackground(containerRef.current);
      // Pass the resolved theme explicitly — with the theme no longer baked
      // into the global config, an unset override would default exports to
      // mermaid's light palette even in dark mode.
      const exportSvg = await renderMermaidSvgForExport(safeChart, mermaidTheme);
      const base = safeMermaidFilename(title);
      if (kind === "svg") {
        const path = await app.PickExportFile(`${base}.svg`, "image/svg+xml");
        if (!path) return;
        await app.SaveExportFile(path, svgWithBackground(exportSvg, background), false);
      } else {
        const blob = await mermaidSvgToPngBlob(exportSvg, { background, scale: 2 });
        const path = await app.PickExportFile(`${base}.png`, "image/png");
        if (!path) return;
        await app.SaveExportFile(path, await blobToBase64(blob), true);
      }
    } catch (err) {
      console.error("Failed to export mermaid diagram", err);
      setExportError(err instanceof Error ? err.message : t("mermaid.exportFailed"));
    } finally {
      setBusyKind(null);
    }
  };

  return (
    <div className="mermaid-viewer">
      {error ? (
        <div className="mermaid-viewer__error">
          <strong>Mermaid Error:</strong> {error}
          <pre>{safeChart}</pre>
        </div>
      ) : (
        <>
          {(title || svg) && (
            <div className="mermaid-viewer__header">
              {title && <div className="mermaid-viewer__title">{title}</div>}
              <div className="mermaid-viewer__actions">
                <button
                  type="button"
                  className="mermaid-viewer__action"
                  onClick={() => void exportDiagram("png")}
                  disabled={!svg || busyKind !== null}
                  aria-label={t("mermaid.exportPng")}
                  title={t("mermaid.exportPng")}
                >
                  {busyKind === "png" ? (
                    <Loader2 size={13} className="composer-phase__spin" />
                  ) : (
                    <ImageDown size={13} />
                  )}
                </button>
                <button
                  type="button"
                  className="mermaid-viewer__action"
                  onClick={() => void exportDiagram("svg")}
                  disabled={!svg || busyKind !== null}
                  aria-label={t("mermaid.exportSvg")}
                  title={t("mermaid.exportSvg")}
                >
                  {busyKind === "svg" ? (
                    <Loader2 size={13} className="composer-phase__spin" />
                  ) : (
                    <FileCode size={13} />
                  )}
                </button>
              </div>
            </div>
          )}
          {exportError && <div className="mermaid-viewer__export-error">{exportError}</div>}
          <div
            ref={containerRef}
            className="mermaid-viewer__svg-container"
            dangerouslySetInnerHTML={{ __html: svg }}
          />
        </>
      )}
    </div>
  );
}
