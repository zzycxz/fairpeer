// ansi.ts — strip ANSI escape sequences from tool/shell output before it is
// rendered (X10a). Agent bash output frequently carries color SGR codes
// (\x1b[31m), cursor movement (\x1b[2K, \x1b[1;2H), and OSC payloads
// (hyperlinks \x1b]8;;url\x1b\\, window-title sets) — in a <pre> these show up
// as raw garbage. Stripping (rather than mapping to colors) is enough for the
// read-only display path; diffs and file previews never pass through here.

// CSI: ESC [ … final byte — params are digits/;/:/? and an optional
// intermediate-byte range, terminated by @-~. Covers SGR colors, cursor
// movement, erase-line/screen, and private-mode (\x1b[?25l) sequences.
const CSI = "\\u001b\\[[0-9;:?]*[ -/]*[@-~]";
// OSC: ESC ] payload terminated by BEL or ST (ESC \) — hyperlink starts/ends
// (\x1b]8;;https://…\x1b\\), title sets, clipboard writes.
const OSC = "\\u001b\\][^\\u0007\\u001b]*(?:\\u0007|\\u001b\\\\)";
// Leftovers: other escape sequences — ESC, optional intermediate bytes
// (0x20–0x2F, e.g. the "(" of charset selection ESC ( B), then a final byte —
// so a lone ESC never survives and nothing after it is orphaned.
const ANSI_RE = new RegExp(`${CSI}|${OSC}|\\u001b[ -/]*.`, "g");

/** True when the text contains at least one ANSI escape sequence. */
export function containsAnsi(text: string): boolean {
  if (!text.includes("\u001b")) return false;
  ANSI_RE.lastIndex = 0;
  return ANSI_RE.test(text);
}

/** Remove all ANSI escape sequences (CSI/OSC/standalone ESC) from the text. */
export function stripAnsi(text: string): string {
  if (!text.includes("\u001b")) return text;
  return text.replace(ANSI_RE, "");
}
