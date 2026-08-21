import { useEffect, useMemo, useRef, useState } from "react";
import { useT } from "../lib/i18n";
import type { WireApproval } from "../lib/types";
import { CodeViewer } from "./CodeViewer";
import { UnifiedDiff } from "./editors/UnifiedDiff";
import { PromptAction, PromptDetailToggle, PromptShelf } from "./PromptShelf";

const KIND_MARK: Record<string, string> = { create: "A", modify: "M", delete: "D" };

// parsedArgs best-effort parses the approval's raw args JSON. Returns null when
// absent or malformed — the card then skips the command/args preview.
function parsedArgs(approval: WireApproval): Record<string, unknown> | null {
  if (!approval.args) return null;
  try {
    const v = JSON.parse(approval.args);
    return typeof v === "object" && v !== null ? (v as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

export function ApprovalModal({
  approval,
  onAnswer,
  onRevisePlan,
  onExitPlan,
}: {
  approval: WireApproval;
  onAnswer: (allow: boolean, session: boolean, persist: boolean) => void;
  onRevisePlan?: (text: string) => void;
  onExitPlan?: () => void;
}) {
  const t = useT();
  const [revisionOpen, setRevisionOpen] = useState(false);
  const [revisionText, setRevisionText] = useState("");
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [selectedFile, setSelectedFile] = useState(0);
  const [diffOpen, setDiffOpen] = useState(true);
  const cardRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const isPlanApproval = approval.tool === "exit_plan_mode";
  const subject = approval.subject.trim();
  const subjectSummary = subject.split("\n").find((line) => line.trim())?.trim() ?? "";

  const changes = approval.changes ?? [];
  const args = useMemo(() => parsedArgs(approval), [approval.args]);
  const command = typeof args?.command === "string" ? (args!.command as string) : "";
  const workdir = typeof args?.workdir === "string" ? (args!.workdir as string) : typeof args?.cwd === "string" ? (args!.cwd as string) : "";
  const totals = useMemo(
    () => changes.reduce((acc, c) => ({ added: acc.added + c.added, removed: acc.removed + c.removed }), { added: 0, removed: 0 }),
    [changes],
  );

  // Reset per-request navigation state (a new approval reuses the component).
  useEffect(() => {
    setSelectedFile(0);
    setDiffOpen(true);
  }, [approval.id]);

  const choosePlanAction = (key: string) => {
    if (key === "1") setRevisionOpen((open) => !open);
    else if (key === "2") onAnswer(true, false, false);
    else if (key === "3" || key === "Escape") (onExitPlan ?? (() => onAnswer(false, false, false)))();
  };

  const chooseToolAction = (key: string) => {
    if (key === "1") onAnswer(true, false, false);
    else if (key === "2") onAnswer(true, true, false);
    else if (key === "3") onAnswer(true, true, true);
    else if (key === "4" || key === "Escape") onAnswer(false, false, false);
    // File-list navigation over the previewed changes (spec 0-4): j/k move the
    // selection, e collapses/expands the selected file's diff. 1-4 stay the
    // decision keys, so answering never conflicts with navigating.
    else if (key === "j") setSelectedFile((i) => Math.min(i + 1, changes.length - 1));
    else if (key === "k") setSelectedFile((i) => Math.max(i - 1, 0));
    else if (key === "e") setDiffOpen((v) => !v);
  };

  useEffect(() => {
    cardRef.current?.focus();
    setRevisionOpen(false);
    setRevisionText("");
    setDetailsOpen(false);
  }, [approval.id]);

  useEffect(() => {
    const navKeys = changes.length > 0 ? ["j", "k", "e"] : [];
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const tag = target?.tagName.toLowerCase();
      if (tag === "input" || tag === "textarea" || target?.isContentEditable) return;
      if (event.key !== "1" && event.key !== "2" && event.key !== "3" && event.key !== "4" && event.key !== "Escape" && !navKeys.includes(event.key)) return;
      event.preventDefault();
      if (isPlanApproval) choosePlanAction(event.key);
      else chooseToolAction(event.key);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isPlanApproval, onAnswer, onExitPlan, changes.length]);

  useEffect(() => {
    if (revisionOpen) inputRef.current?.focus();
  }, [revisionOpen]);

  const submitRevision = () => {
    const text = revisionText.trim();
    if (!text) {
      inputRef.current?.focus();
      return;
    }
    onRevisePlan?.(text);
  };

  // The plan is already shown above as the assistant's reply; this is just the gate.
  if (isPlanApproval) {
    return (
      <PromptShelf
        barRef={cardRef}
        titleId="plan-approval-title"
        title={t("approval.planReady")}
        meta={t("approval.planReadyHint")}
        actions={
          <>
            <PromptAction keyLabel="1" label={t("approval.revisePlan")} onClick={() => setRevisionOpen((open) => !open)} />
            <PromptAction keyLabel="2" label={t("approval.startExecution")} onClick={() => onAnswer(true, false, false)} selected />
            <PromptAction
              keyLabel="3"
              label={t("approval.exitPlan")}
              onClick={() => (onExitPlan ?? (() => onAnswer(false, false, false)))()}
            />
          </>
        }
      >
        {revisionOpen && (
          <div className="plan-revision">
            <textarea
              ref={inputRef}
              className="plan-revision__input"
              value={revisionText}
              rows={3}
              placeholder={t("approval.revisePlanPlaceholder")}
              onChange={(event) => setRevisionText(event.target.value)}
              onKeyDown={(event) => {
                if ((event.metaKey || event.ctrlKey) && event.key === "Enter") submitRevision();
                event.stopPropagation();
              }}
            />
            <div className="plan-revision__actions">
              <button className="btn" onClick={() => setRevisionOpen(false)}>
                {t("common.cancel")}
              </button>
              <button className="btn btn--primary" onClick={submitRevision}>
                {t("approval.sendRevision")}
              </button>
            </div>
          </div>
        )}
      </PromptShelf>
    );
  }

  const selected = changes[selectedFile];

  return (
    <PromptShelf
      barRef={cardRef}
      titleId="tool-approval-title"
      title={t("approval.toolPending")}
      actionsWrap
      meta={
        <>
          <span className="tool__name">{approval.tool}</span>
          {subjectSummary && <span className="prompt-shelf__subject"> · {subjectSummary}</span>}
          {changes.length > 0 && (
            <span className="prompt-shelf__subject">
              {" "}&nbsp;{changes.length === 1 ? "1 file" : `${changes.length} files`} · +{totals.added} -{totals.removed}
            </span>
          )}
        </>
      }
      actions={
        <>
          {subject && (
            <PromptDetailToggle
              open={detailsOpen}
              label={t("approval.details")}
              openLabel={t("approval.hideDetails")}
              onClick={() => setDetailsOpen((open) => !open)}
            />
          )}
          <PromptAction keyLabel="1" label={t("approval.allowOnce")} onClick={() => onAnswer(true, false, false)} selected />
          <PromptAction keyLabel="2" label={t("approval.allowRuleSession")} onClick={() => onAnswer(true, true, false)} />
          <PromptAction keyLabel="3" label={t("approval.allowRulePersistent")} onClick={() => onAnswer(true, true, true)} />
          <PromptAction keyLabel="4" label={t("approval.deny")} onClick={() => onAnswer(false, false, false)} />
        </>
      }
    >
      {/* Previewed file changes (spec 0-4): per-file markers + tallies, the
          selected file's diff below. j/k navigate, e collapses. */}
      {changes.length > 0 && (
        <div className="approval-changes">
          <div className="approval-changes__list" role="listbox" aria-label="changed files">
            {changes.map((c, i) => (
              <button
                type="button"
                key={`${c.path}-${i}`}
                role="option"
                aria-selected={i === selectedFile}
                className={`approval-changes__file${i === selectedFile ? " approval-changes__file--selected" : ""}`}
                onClick={() => setSelectedFile(i)}
              >
                <span className={`approval-changes__mark approval-changes__mark--${c.kind}`}>{KIND_MARK[c.kind] ?? "M"}</span>
                <span className="approval-changes__path">{c.path}</span>
                <span className="approval-changes__stat">+{c.added} -{c.removed}</span>
              </button>
            ))}
          </div>
          {selected && diffOpen && (
            <UnifiedDiff value={selected.diff} maxHeight={280} showToggle={false} />
          )}
        </div>
      )}
      {/* No file preview (e.g. bash): show the exact command and working
          directory so the decision is made on the real payload, not a summary. */}
      {changes.length === 0 && command && (
        <div className="approval-changes">
          <CodeViewer value={command} language="bash" maxHeight={160} />
          {workdir && <div className="approval-changes__workdir">{workdir}</div>}
        </div>
      )}
      {detailsOpen && subject && (
        <pre className="approval-subject">{subject}</pre>
      )}
    </PromptShelf>
  );

}
