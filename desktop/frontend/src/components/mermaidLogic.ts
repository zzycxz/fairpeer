export function extractMermaidTitle(code: string): string {
  const match = code.match(/---\n\s*title:\s*(.+?)\s*\n---/);
  return match ? match[1].trim() : "";
}

export function sanitizeMermaidCode(code: string): string {
  // Mermaid's parser can be sensitive to trailing/leading whitespace and empty lines
  return code.replace(/\n\s*\n$/g, '\n').trim();
}

// The app-wide mermaid initialization: one definition shared by the on-screen
// MermaidViewer and the export rasterizer (lib/mermaidExport), so the two can
// never drift into different base configs.
export const APP_MERMAID_CONFIG = {
  startOnLoad: false,
  theme: "dark",
  securityLevel: "strict",
  fontFamily: "inherit",
} as const;

// System-only stack: an SVG loaded through <img> cannot fetch webfonts, so
// export renders must rely on fonts installed on the machine.
export const EXPORT_MERMAID_FONT_FAMILY =
  "'Segoe UI', 'Noto Sans SC', 'Microsoft YaHei', -apple-system, sans-serif";

// parseMermaidSvgSize recovers the diagram's natural pixel size. Mermaid emits
// viewBox="0 0 W H" plus a style max-width (useMaxWidth default), or explicit
// width/height attrs; percent widths carry no size and fall through.
export function parseMermaidSvgSize(svg: string): { width: number; height: number } {
  const viewBox = /\bviewBox\s*=\s*"([^"]*)"/.exec(svg);
  if (viewBox) {
    const parts = viewBox[1].trim().split(/[\s,]+/).map(Number);
    if (parts.length === 4 && parts.every((n) => Number.isFinite(n) && n >= 0) && parts[2] > 0 && parts[3] > 0) {
      return { width: parts[2], height: parts[3] };
    }
  }
  const width = /\bwidth\s*=\s*"([\d.]+)(?:px)?"/.exec(svg);
  const height = /\bheight\s*=\s*"([\d.]+)(?:px)?"/.exec(svg);
  const w = width ? Number(width[1]) : 0;
  const h = height ? Number(height[1]) : 0;
  if (w > 0 && h > 0) return { width: w, height: h };
  return { width: 800, height: 600 };
}

// safeMermaidFilename mirrors App.tsx's session-export sanitizer for diagram
// titles: strip filesystem-hostile characters, collapse spaces, cap length.
export function safeMermaidFilename(title: string): string {
  const cleaned = title.trim().replace(/[\\/:*?"<>|]+/g, "-").replace(/\s+/g, " ").slice(0, 80);
  return cleaned || "mermaid-diagram";
}

export interface MermaidFenceMatch {
  // Chart source between the fences (title directive included).
  code: string;
  // Title from the chart's front-matter, when present ("" otherwise).
  title: string;
  // Replacement span in the original markdown: [start, end). `start` sits
  // AFTER the leading newline of the match, `end` at the closing fence's end.
  start: number;
  end: number;
  // Indentation of the opening fence, re-applied to the image replacement.
  indent: string;
}

const MERMAID_FENCE_RE = /(^|\n)([ \t]*)(`{3,})mermaid[ \t]*\n([\s\S]*?)\n[ \t]*\3[ \t]*(?=\n|$)/g;

export function collectMermaidFences(markdown: string): MermaidFenceMatch[] {
  const matches: MermaidFenceMatch[] = [];
  for (const m of markdown.matchAll(MERMAID_FENCE_RE)) {
    const leading = m[1] ?? "";
    matches.push({
      code: m[4] ?? "",
      title: extractMermaidTitle(m[4] ?? ""),
      start: (m.index ?? 0) + leading.length,
      end: (m.index ?? 0) + m[0].length,
      indent: m[2] ?? "",
    });
  }
  return matches;
}

export function buildMermaidImageMarkdown(match: MermaidFenceMatch, dataUrl: string): string {
  const alt = (match.title || "Mermaid diagram").replace(/[[\]]/g, "");
  return `${match.indent}![${alt}](${dataUrl})`;
}

// replaceMermaidFences splices image markdown over each fence; a null entry
// keeps the original span untouched (rasterization failed → source text, the
// pre-diagram-export behavior).
export function replaceMermaidFences(
  markdown: string,
  fences: MermaidFenceMatch[],
  replacements: (string | null)[],
): string {
  let out = "";
  let cursor = 0;
  fences.forEach((fence, i) => {
    const replacement = replacements[i];
    if (replacement === null) return;
    out += markdown.slice(cursor, fence.start) + replacement;
    cursor = fence.end;
  });
  if (cursor === 0) return markdown;
  return out + markdown.slice(cursor);
}
