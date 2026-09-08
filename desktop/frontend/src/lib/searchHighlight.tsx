// searchHighlight (FAIRPEER_CODEX_GAP_SPEC Spec-2 Phase 2): the in-conversation
// search query flows to text renderers through this context so matched
// substrings render as <mark> — no prop drilling through the card tree. The
// provider lives on Transcript; components that render PLAIN text (user
// messages, notices) opt in via HighlightText. Markdown-rendered assistant
// text and syntax-highlighted tool output stay unhighlighted (Phase 3).
import { createContext, useContext, type ReactNode } from "react";

export const SearchHighlightContext = createContext<string>("");

export function HighlightText({ text }: { text: string }) {
  const q = useContext(SearchHighlightContext).trim();
  if (!q || !text) return <>{text}</>;
  const lowerText = text.toLowerCase();
  const lowerQ = q.toLowerCase();
  const parts: ReactNode[] = [];
  let key = 0;
  let cursor = 0;
  let at = lowerText.indexOf(lowerQ);
  while (at >= 0) {
    if (at > cursor) parts.push(text.slice(cursor, at));
    parts.push(<mark key={key++} className="ts-mark">{text.slice(at, at + q.length)}</mark>);
    cursor = at + q.length;
    at = lowerText.indexOf(lowerQ, cursor);
  }
  if (parts.length === 0) return <>{text}</>;
  if (cursor < text.length) parts.push(text.slice(cursor));
  return <>{parts}</>;
}
