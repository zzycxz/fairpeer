// cjk.ts — CJK-aware word boundary navigation (gap analysis §2).
// Uses Intl.Segmenter (Chrome 87+, WebView2 ✓) to find word boundaries in
// mixed CJK/Latin text. For pure Latin segments, we return the segment
// boundary so the browser's native word-jump can take over.
//
// The mental model: Ctrl+← should jump to the START of the previous "word"
// (a CJK word/phrase or a Latin word), Ctrl+→ to the END of the next one.

// Minimal structural type for Intl.Segmenter (not in TS lib until es2022).
interface SegmentData {
  segment: string;
  index: number;
  isWordLike: boolean;
}
interface SegmenterInstance {
  segment(input: string): Iterable<SegmentData>;
}
type SegmenterCtor = new (locale: string, options: { granularity: string }) => SegmenterInstance;

// Cached segmenter instance — creating one per keypress is wasteful.
let segmenter: SegmenterInstance | null = null;

function getSegmenter(): SegmenterInstance | null {
  if (typeof Intl === "undefined" || !("Segmenter" in Intl)) return null;
  if (!segmenter) {
    try {
      const Ctor = (Intl as unknown as { Segmenter: SegmenterCtor }).Segmenter;
      segmenter = new Ctor("zh", { granularity: "word" });
    } catch {
      return null;
    }
  }
  return segmenter;
}

/** Segments text into word-like and non-word-like segments. */
function segments(text: string): SegmentData[] {
  const seg = getSegmenter();
  if (!seg) return [];
  const out: SegmentData[] = [];
  for (const s of seg.segment(text)) {
    out.push(s);
  }
  return out;
}

function isCJK(ch: string): boolean {
  const c = ch.codePointAt(0) ?? 0;
  return (
    (c >= 0x4e00 && c <= 0x9fff) ||   // CJK Unified
    (c >= 0x3400 && c <= 0x4dbf) ||   // CJK Ext A
    (c >= 0x3000 && c <= 0x303f) ||   // CJK Symbols & Punctuation
    (c >= 0xff00 && c <= 0xffef)      // Fullwidth Forms
  );
}

/**
 * cjkWordStart returns the start position of the word before `pos`.
 * If pos is at the start of a word, jumps to the start of the previous word.
 * Returns pos unchanged when no CJK-aware boundary applies (caller falls
 * back to the browser's native behavior).
 */
export function cjkWordStart(text: string, pos: number): number {
  if (pos <= 0) return pos;
  const segs = segments(text);
  if (segs.length === 0) return pos;

  // Walk segments backward from pos, skip non-word segments, find the
  // start of the last word-like segment that begins before pos.
  let result = pos;
  let seen = false;
  for (let i = segs.length - 1; i >= 0; i--) {
    const seg = segs[i];
    const start = seg.index;
    const end = start + seg.segment.length;
    if (end > pos || start >= pos) continue;
    if (seg.isWordLike || isCJK(seg.segment[0])) {
      result = start;
      seen = true;
      break;
    }
    // Non-word separator — continue past it.
  }
  return seen ? result : pos;
}

/**
 * cjkWordEnd returns the end position of the word after `pos`.
 * If pos is inside or at the start of a word, jumps to its end.
 * Returns pos unchanged when no CJK-aware boundary applies.
 */
export function cjkWordEnd(text: string, pos: number): number {
  if (pos >= text.length) return pos;
  const segs = segments(text);
  if (segs.length === 0) return pos;

  // Walk segments forward from pos, find the first word-like segment
  // that starts at or after pos; return its end.
  let result = pos;
  let seen = false;
  for (let i = 0; i < segs.length; i++) {
    const seg = segs[i];
    const start = seg.index;
    const end = start + seg.segment.length;
    if (end <= pos) continue;
    if (seg.isWordLike || isCJK(seg.segment[0])) {
      // If we're already at the end of this word, find the next one.
      if (start >= pos && end > pos) {
        result = end;
        seen = true;
        break;
      }
    }
  }
  return seen ? result : pos;
}
