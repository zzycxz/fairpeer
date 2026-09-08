// TranscriptSearch (FAIRPEER_CODEX_GAP_SPEC Spec-2) — the in-conversation
// Ctrl+F bar. codex has no equivalent; fairpeer jumps through the same
// question-anchor machinery the JumpBar uses, so matches inside collapsed
// warm turns expand their turn before scrolling. Phase 1 shows the match
// snippet in the bar; mark-tag highlighting inside rendered content is
// Phase 2 (it cuts across Markdown/tool card renderers).
import { useEffect, useRef } from "react";
import { ChevronDown, ChevronUp, X } from "lucide-react";
import { useT } from "../lib/i18n";

export function TranscriptSearch({
  query,
  onQuery,
  matchCount,
  matchIndex,
  snippet,
  onPrev,
  onNext,
  onClose,
}: {
  query: string;
  onQuery: (q: string) => void;
  matchCount: number;
  // 0-based; -1 when there is no current match.
  matchIndex: number;
  // Context preview of the current match ("" when none).
  snippet: string;
  onPrev: () => void;
  onNext: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  return (
    <div className="tsearch" role="search" aria-label={t("transcript.searchTitle")}>
      <input
        ref={inputRef}
        className="tsearch__input"
        value={query}
        placeholder={t("transcript.searchPlaceholder")}
        onChange={(e) => onQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            if (e.shiftKey) onPrev();
            else onNext();
          }
          if (e.key === "Escape") {
            e.preventDefault();
            onClose();
          }
        }}
        spellCheck={false}
        autoComplete="off"
      />
      <span className="tsearch__count">
        {query.trim() === ""
          ? ""
          : matchCount === 0
            ? t("transcript.searchNoMatch")
            : `${matchIndex + 1}/${matchCount}`}
      </span>
      {snippet && <span className="tsearch__snippet" title={snippet}>{snippet}</span>}
      <button type="button" className="tsearch__btn" onClick={onPrev} aria-label={t("transcript.searchPrev")} title={t("transcript.searchPrev")}>
        <ChevronUp size={13} />
      </button>
      <button type="button" className="tsearch__btn" onClick={onNext} aria-label={t("transcript.searchNext")} title={t("transcript.searchNext")}>
        <ChevronDown size={13} />
      </button>
      <button type="button" className="tsearch__btn" onClick={onClose} aria-label={t("transcript.searchClose")} title={t("transcript.searchClose")}>
        <X size={13} />
      </button>
    </div>
  );
}
