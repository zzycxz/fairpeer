// mermaidExport — rasterizing mermaid diagrams for file export and session
// exports. The on-screen MermaidViewer renders with the global config
// (htmlLabels on, inherited fonts); that SVG cannot be rasterized reliably:
// HTML labels live in <foreignObject>, which tends to come out blank when the
// SVG is loaded through <img> onto a canvas. Export therefore re-renders the
// chart with htmlLabels:false (plain SVG <text>, rasterizes cleanly) via a
// per-diagram %%{init}%% directive — the global config is never touched.
import mermaid from "mermaid";
import {
  EXPORT_MERMAID_FONT_FAMILY,
  buildMermaidImageMarkdown,
  collectMermaidFences,
  parseMermaidSvgSize,
  replaceMermaidFences,
  sanitizeMermaidCode,
} from "../components/mermaidLogic";

// Content width of the session-export page (EXPORT_WIDTH 920 − padding 112) —
// rasters are drawn at up to 2× this width, wider diagrams scale down.
const EXPORT_RASTER_MAX_WIDTH = 1616;

export interface MermaidRasterOptions {
  // Canvas fill behind the transparent SVG ("none" keeps transparency).
  background?: string;
  // Pixel-density multiplier (default 2 for crisp output).
  scale?: number;
  // Hard canvas edge cap — matches sessionExport's MAX_CANVAS_SIDE.
  maxSide?: number;
  // Cap on the raster's width in px, applied by shrinking the scale.
  maxRasterWidth?: number;
}

// renderMermaidSvgForExport renders chart with export-safe settings. `theme`
// overrides the app's dark theme (the session-export page passes "neutral" to
// match its white page); the chart's own %%{init}%% directive, if any, comes
// after ours and still wins per key.
export async function renderMermaidSvgForExport(chart: string, theme?: string): Promise<string> {
  const safeChart = sanitizeMermaidCode(chart);
  if (!safeChart) throw new Error("Mermaid chart is empty");
  const init: Record<string, unknown> = {
    htmlLabels: false,
    fontFamily: EXPORT_MERMAID_FONT_FAMILY,
  };
  if (theme) init.theme = theme;
  const directive = `%%{init: ${JSON.stringify(init)}}%%\n`;
  const id = `mermaid-export-${Math.random().toString(36).slice(2, 11)}`;
  const { svg } = await mermaid.render(id, directive + safeChart);
  return svg;
}

// prepareMermaidSvgForRaster pins explicit width/height on the root and strips
// the max-width style mermaid adds with useMaxWidth — without this, the SVG
// loads at 0×0 (width="100%") and draws nothing.
function prepareMermaidSvgForRaster(svg: string): string {
  const { width, height } = parseMermaidSvgSize(svg);
  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  const root = doc.documentElement;
  root.setAttribute("xmlns", "http://www.w3.org/2000/svg");
  root.setAttribute("width", String(width));
  root.setAttribute("height", String(height));
  const style = root.getAttribute("style") ?? "";
  const cleaned = style
    .split(";")
    .map((s) => s.trim())
    .filter(Boolean)
    .filter((s) => !/^(max-width|min-width|width|height)\s*:/i.test(s))
    .join("; ");
  if (cleaned) root.setAttribute("style", cleaned);
  else root.removeAttribute("style");
  return new XMLSerializer().serializeToString(root);
}

// svgWithBackground inserts a full-size colored rect as the first paint child,
// so standalone .svg files keep the diagram readable outside the app shell.
export function svgWithBackground(svg: string, background: string): string {
  const { width, height } = parseMermaidSvgSize(svg);
  const doc = new DOMParser().parseFromString(svg, "image/svg+xml");
  const root = doc.documentElement;
  const rect = doc.createElementNS("http://www.w3.org/2000/svg", "rect");
  rect.setAttribute("x", "0");
  rect.setAttribute("y", "0");
  rect.setAttribute("width", String(width));
  rect.setAttribute("height", String(height));
  rect.setAttribute("fill", background);
  root.insertBefore(rect, root.firstChild);
  return new XMLSerializer().serializeToString(root);
}

function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => resolve(img);
    img.onerror = () => reject(new Error("Could not render export image"));
    img.src = url;
  });
}

export async function mermaidSvgToCanvas(
  svg: string,
  opts: MermaidRasterOptions = {},
): Promise<HTMLCanvasElement> {
  const { background = "#ffffff", scale = 2, maxSide = 16384, maxRasterWidth } = opts;
  const { width, height } = parseMermaidSvgSize(svg);
  const safeScale = Math.max(
    0.1,
    Math.min(scale, maxSide / width, maxSide / height, maxRasterWidth ? maxRasterWidth / width : Infinity),
  );
  const serialized = prepareMermaidSvgForRaster(svg);
  const url = URL.createObjectURL(new Blob([serialized], { type: "image/svg+xml;charset=utf-8" }));
  try {
    const image = await loadImage(url);
    const canvas = document.createElement("canvas");
    canvas.width = Math.max(1, Math.floor(width * safeScale));
    canvas.height = Math.max(1, Math.floor(height * safeScale));
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("Canvas is not available");
    if (background !== "none") {
      ctx.fillStyle = background;
      ctx.fillRect(0, 0, canvas.width, canvas.height);
    }
    ctx.scale(safeScale, safeScale);
    ctx.drawImage(image, 0, 0, width, height);
    return canvas;
  } finally {
    URL.revokeObjectURL(url);
  }
}

function canvasToBlob(canvas: HTMLCanvasElement, type: string): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error("Could not encode export image"));
    }, type);
  });
}

export function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = String(reader.result ?? "");
      resolve(result.includes(",") ? result.slice(result.indexOf(",") + 1) : result);
    };
    reader.onerror = () => reject(reader.error ?? new Error("Could not read export payload"));
    reader.readAsDataURL(blob);
  });
}

export async function mermaidSvgToPngBlob(svg: string, opts?: MermaidRasterOptions): Promise<Blob> {
  const canvas = await mermaidSvgToCanvas(svg, opts);
  return canvasToBlob(canvas, "image/png");
}

export async function mermaidSvgToPngDataUrl(svg: string, opts?: MermaidRasterOptions): Promise<string> {
  const canvas = await mermaidSvgToCanvas(svg, opts);
  return canvas.toDataURL("image/png");
}

// inlineMermaidFences replaces ```mermaid fences with rasterized data-URL
// images so the session-export surface (PNG/PDF/HTML) shows diagrams instead
// of fenced source. Fences that fail to rasterize stay untouched and degrade
// to the code-text rendering of the pre-diagram-export behavior.
export async function inlineMermaidFences(markdown: string): Promise<string> {
  const fences = collectMermaidFences(markdown);
  if (fences.length === 0) return markdown;
  const replacements = await Promise.all(
    fences.map(async (fence) => {
      try {
        const svg = await renderMermaidSvgForExport(fence.code, "neutral");
        const dataUrl = await mermaidSvgToPngDataUrl(svg, {
          background: "#ffffff",
          scale: 2,
          maxRasterWidth: EXPORT_RASTER_MAX_WIDTH,
        });
        return buildMermaidImageMarkdown(fence, dataUrl);
      } catch (err) {
        console.warn("Mermaid diagram failed to rasterize for export; keeping source text", err);
        return null;
      }
    }),
  );
  return replaceMermaidFences(markdown, fences, replacements);
}
