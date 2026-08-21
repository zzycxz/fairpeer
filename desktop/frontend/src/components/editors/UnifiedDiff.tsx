// UnifiedDiff renders a unified-diff string the way a code review expects:
// per-file sections, dual line-number gutters, +/- coloring, and word-level
// highlighting on paired single-line changes (the "1 del + 1 add" case where
// intra-line diffing points at the actual edited tokens). A Split toggle
// switches to the side-by-side CodeMirror view using the sides reconstructed
// from the diff itself, so both modes work from the server's diff text alone.
import { useMemo, useState } from "react";
import { DiffView } from "../DiffView";

type RowType = "ctx" | "add" | "del" | "note";

interface DiffRow {
  type: RowType;
  text: string;
  oldNo: number | null;
  newNo: number | null;
}

interface DiffSection {
  path: string;
  rows: DiffRow[];
}

// parseUnifiedDiff walks the standard unified format emitted by internal/diff
// (--- a/path, +++ b/path, @@ -o,n +m,n @@, then ' '/'-'/'+' rows and the
// '\ No newline' marker which renders as a muted note). Multiple '--- a/'
// headers inside one string (apply_patch concatenates per-file diffs) become
// separate sections.
function parseUnifiedDiff(text: string): DiffSection[] {
  const sections: DiffSection[] = [];
  let cur: DiffSection | null = null;
  let oldNo = 0;
  let newNo = 0;
  for (const raw of text.split("\n")) {
    if (raw.startsWith("--- a/")) {
      cur = { path: raw.slice(6).trim(), rows: [] };
      sections.push(cur);
      continue;
    }
    if (raw.startsWith("+++ b/") || raw.startsWith("--- ") || raw.startsWith("+++ ")) continue;
    const hunk = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(raw);
    if (hunk) {
      // Header-less diff (a producer that emits bare hunks): open an
      // anonymous section so the rows still render.
      if (!cur) {
        cur = { path: "", rows: [] };
        sections.push(cur);
      }
      const m = /^@@ -(\d+)(?:,\d+)? \+/.exec(raw);
      oldNo = m ? Number(m[1]) : 0;
      newNo = Number(hunk[1]);
      continue;
    }
    if (!cur) continue;
    if (raw.startsWith("\\")) {
      cur.rows.push({ type: "note", text: raw, oldNo: null, newNo: null });
      continue;
    }
    const ch = raw.charAt(0);
    const body = raw.slice(1);
    if (ch === "+") {
      cur.rows.push({ type: "add", text: body, oldNo: null, newNo: newNo++ });
    } else if (ch === "-") {
      cur.rows.push({ type: "del", text: body, oldNo: oldNo++, newNo: null });
    } else if (ch === " ") {
      cur.rows.push({ type: "ctx", text: body, oldNo: oldNo++, newNo: newNo++ });
    } else if (raw.trim() === "" && cur.rows.length > 0) {
      // A bare empty line inside a hunk is a context row whose leading space
      // was trimmed somewhere; treat it as context so it keeps its numbers.
      cur.rows.push({ type: "ctx", text: "", oldNo: oldNo++, newNo: newNo++ });
    }
  }
  return sections.filter((s) => s.rows.length > 0);
}

// tokenize splits a line into word tokens, keeping the whitespace between
// them so highlighting reproduces the line exactly when reassembled.
function tokenize(line: string): string[] {
  return line.split(/(\s+)/).filter((t) => t !== "");
}

interface Seg {
  text: string;
  changed: boolean;
}

// wordSegments diffs two lines at token granularity and returns the
// highlighted segments for each side. Tokens present in the LCS are common;
// the rest are marked changed. Falls back to a single unchanged segment when
// either line is long (the DP is O(n·m) — 300 tokens is already generous for
// a source line).
function wordSegments(a: string, b: string): { left: Seg[]; right: Seg[] } | null {
  const at = tokenize(a);
  const bt = tokenize(b);
  if (at.length === 0 || bt.length === 0 || at.length > 300 || bt.length > 300) return null;
  // LCS table.
  const n = at.length;
  const m = bt.length;
  const dp: Uint16Array[] = Array.from({ length: n + 1 }, () => new Uint16Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = at[i] === bt[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const left: Seg[] = [];
  const right: Seg[] = [];
  const push = (arr: Seg[], text: string, changed: boolean) => {
    const last = arr[arr.length - 1];
    if (last && last.changed === changed) last.text += text;
    else arr.push({ text, changed });
  };
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (at[i] === bt[j]) {
      push(left, at[i], false);
      push(right, bt[j], false);
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      push(left, at[i], true);
      i++;
    } else {
      push(right, bt[j], true);
      j++;
    }
  }
  while (i < n) push(left, at[i++], true);
  while (j < m) push(right, bt[j++], true);
  return { left, right };
}

// pairRuns marks 1-del/1-add adjacency: within a hunk, a single deleted line
// directly followed by a single added line gets word-level highlighting. Runs
// of 2+ on either side are line moves/replacements where intra-line diffing
// would be noise, so they stay plain.
function decoratePairs(rows: DiffRow[]): void {
  for (let i = 0; i < rows.length; i++) {
    if (rows[i].type !== "del") continue;
    let delEnd = i;
    while (delEnd + 1 < rows.length && rows[delEnd + 1].type === "del") delEnd++;
    let addEnd = delEnd;
    while (addEnd + 1 < rows.length && rows[addEnd + 1].type === "add") addEnd++;
    const delCount = delEnd - i + 1;
    const addCount = addEnd - delEnd;
    if (delCount === 1 && addCount === 1) {
      const seg = wordSegments(rows[i].text, rows[delEnd + 1].text);
      if (seg) {
        (rows[i] as DiffRow & { segs?: Seg[] }).segs = seg.left;
        (rows[delEnd + 1] as DiffRow & { segs?: Seg[] }).segs = seg.right;
      }
    }
    i = addEnd;
  }
}

// sidesOf reconstructs the (approximate) full old/new texts from the hunks —
// context lines plus deletions form the old side, context plus additions the
// new side. Hunks only carry `context` surrounding lines, so the result is
// hunk-complete rather than file-complete: exactly what a split view needs
// for review, and clearly labelled as such via the hunk count.
function sidesOf(section: DiffSection): { original: string; modified: string } {
  const oldLines: string[] = [];
  const newLines: string[] = [];
  for (const r of section.rows) {
    if (r.type === "ctx") {
      oldLines.push(r.text);
      newLines.push(r.text);
    } else if (r.type === "del") {
      oldLines.push(r.text);
    } else if (r.type === "add") {
      newLines.push(r.text);
    }
  }
  return { original: oldLines.join("\n"), modified: newLines.join("\n") };
}

function LineNos({ row }: { row: DiffRow }) {
  return (
    <>
      <span className="udiff__no">{row.oldNo ?? ""}</span>
      <span className="udiff__no">{row.newNo ?? ""}</span>
    </>
  );
}

export function UnifiedDiff({
  value,
  maxHeight = 320,
  defaultMode = "unified",
  showToggle = true,
}: {
  value: string;
  maxHeight?: number;
  defaultMode?: "unified" | "split";
  showToggle?: boolean;
}) {
  const [mode, setMode] = useState<"unified" | "split">(defaultMode);
  const [splitSection, setSplitSection] = useState<string | null>(null);
  const sections = useMemo(() => {
    const parsed = parseUnifiedDiff(value);
    for (const s of parsed) decoratePairs(s.rows);
    return parsed;
  }, [value]);

  if (sections.length === 0) return null;

  // Split mode renders one section at a time (a multi-file diff in
  // side-by-side needs a file picker; the single-file case — the common one —
  // just switches over).
  const activeSplit =
    sections.find((s) => s.path === splitSection) ?? sections[0];

  if (mode === "split") {
    const sides = sidesOf(activeSplit);
    return (
      <div className="udiff">
        {showToggle && (
          <div className="udiff__modes">
            {sections.length > 1 && (
              <select
                className="udiff__filepick"
                value={activeSplit.path}
                onChange={(e) => setSplitSection(e.target.value)}
                aria-label="file"
              >
                {sections.map((s) => (
                  <option key={s.path} value={s.path}>{s.path}</option>
                ))}
              </select>
            )}
            <button type="button" className="udiff__mode" onClick={() => setMode("unified")}>Unified</button>
            <button type="button" className="udiff__mode udiff__mode--on" disabled>Split</button>
          </div>
        )}
        <DiffView original={sides.original} modified={sides.modified} maxHeight={maxHeight} />
      </div>
    );
  }

  return (
    <div className="udiff">
      {showToggle && (
        <div className="udiff__modes">
          <button type="button" className="udiff__mode udiff__mode--on" disabled>Unified</button>
          <button type="button" className="udiff__mode" onClick={() => setMode("split")}>Split</button>
        </div>
      )}
      <div className="udiff__scroll" style={{ maxHeight }}>
        {sections.map((s, si) => {
          let added = 0;
          let removed = 0;
          for (const r of s.rows) {
            if (r.type === "add") added++;
            else if (r.type === "del") removed++;
          }
          return (
            <div key={`${s.path}-${si}`} className="udiff__section">
              <div className="udiff__file">
                <span className="udiff__path">{s.path}</span>
                <span className="udiff__stat">+{added} -{removed}</span>
              </div>
              <pre className="udiff__rows">
                {s.rows.map((r, ri) => {
                  const segs = (r as DiffRow & { segs?: Seg[] }).segs;
                  return (
                    <div key={ri} className={`udiff__row udiff__row--${r.type}`}>
                      <LineNos row={r} />
                      <span className="udiff__marker">{r.type === "add" ? "+" : r.type === "del" ? "-" : ""}</span>
                      <code>
                        {segs
                          ? segs.map((sg, k) => (
                              <span key={k} className={sg.changed ? `udiff__tok udiff__tok--${r.type}` : undefined}>
                                {sg.text}
                              </span>
                            ))
                          : r.text}
                      </code>
                    </div>
                  );
                })}
              </pre>
            </div>
          );
        })}
      </div>
    </div>
  );
}
