// ContextPanel — slim session stats + per-turn facts table for the right dock.
// The old overview (donut, token breakdown, health badge, read/changed file
// previews) was retired in 2026-08-27: the composer UsageChip hover detail
// covers usage/cost/cache and the workspace tabs cover files. What stayed is
// the content nothing else shows — the per-turn history (duration / retries /
// tool volume / token+cache split) plus a compact stat strip, which also
// serves cowork mode where no UsageChip exists; the strip carries the two
// facts cowork can't see anywhere else (context-window pressure bar and
// session cost). Mounted as the "turns" tab by BOTH docks (coding and
// cowork). Data comes straight from App state (ContextInfo with session
// telemetry); only the turn facts still poll their own bridge. All visible
// text is routed through the i18n dictionary.
import { useCallback, useEffect, useState } from "react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { useToast } from "../lib/toast";
import { fmtDuration } from "../lib/duration";
import type { ContextInfo } from "../lib/types";

interface ContextPanelProps {
  tabId?: string;
  context?: ContextInfo;
  sessionTokens?: number;
  refreshKey?: number;
  // Active tab is streaming a turn — compact stays available but disabled
  // until the turn finishes (avoids compacting a half-written context).
  busy?: boolean;
}

interface TurnFact {
  seq: number;
  durationMs: number;
  retries: number;
  toolCalls: number;
  toolErrors: number;
  promptTokens: number;
  completionTokens: number;
  cacheHitTokens: number;
  cacheMissTokens: number;
  err?: string;
}

function fmtTokensShort(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

function fmtCost(cost: number, currency?: string): string {
  const sym = currency || "$";
  return `${sym}${cost < 0.01 ? cost.toFixed(4) : cost.toFixed(3)}`;
}

export function ContextPanel({ tabId, context, sessionTokens, refreshKey, busy }: ContextPanelProps) {
  const t = useT();
  const { showToast } = useToast();
  const [compacting, setCompacting] = useState(false);

  // Compact entry for surfaces without the composer UsageChip (the cowork
  // dock's turns tab is its only entry; the coding-mode tab shares it for
  // convenience alongside the UsageChip hot-state button).
  const handleCompact = useCallback(() => {
    if (compacting) return;
    setCompacting(true);
    app.Compact()
      .then(() => showToast(t("composer.usageDetail.compactDone")))
      .catch(() => showToast(t("composer.usageDetail.compactFail"), "error"))
      .finally(() => setCompacting(false));
  }, [compacting, showToast, t]);

  // Per-turn lifecycle facts (upgrade spec 4-4): duration / retries / tool
  // volume / cache split for the last few turns.
  const [turnFacts, setTurnFacts] = useState<TurnFact[]>([]);
  useEffect(() => {
    if (!tabId) return;
    let cancelled = false;
    const load = () => {
      app.TurnFactsForTab(tabId)
        .then((f) => { if (!cancelled) setTurnFacts((f ?? []).slice(-8).reverse()); })
        .catch(() => {});
    };
    load();
    const id = window.setInterval(load, 3000);
    return () => { cancelled = true; window.clearInterval(id); };
  }, [tabId, refreshKey]);

  const cacheHit = context?.sessionCacheHitTokens ?? 0;
  const cacheMiss = context?.sessionCacheMissTokens ?? 0;
  const cachePct = cacheHit + cacheMiss > 0
    ? Math.round((cacheHit / (cacheHit + cacheMiss)) * 100)
    : 0;
  const elapsedMs = context?.sessionElapsedMs ?? 0;
  const tokens = sessionTokens && sessionTokens > 0 ? sessionTokens : context?.sessionTokens ?? 0;
  const requests = context?.requestCount ?? 0;

  // Context-window pressure — cowork mode has no UsageChip, so this card is
  // its only view of compaction pressure; thresholds mirror the chip (≥85%
  // hot). Coding mode tolerates the duplication for an at-a-glance summary.
  const used = context?.used ?? 0;
  const ctxWindow = context?.window ?? 0;
  const ctxPct = ctxWindow > 0 ? used / ctxWindow : null;
  const ctxTone = ctxPct == null ? undefined : ctxPct >= 0.85 ? "warn" : ctxPct >= 0.6 ? "notice" : "good";
  const cost = context?.sessionCost;
  // Session token split on hover — the same breakdown the UsageChip detail
  // shows in coding mode; reasoning only appears when the model emits it.
  const tokensTitle = context && (context.sessionPromptTokens || context.sessionCompletionTokens)
    ? `${t("composer.usageDetail.input")} ${(context.sessionPromptTokens ?? 0).toLocaleString()}`
      + ` · ${t("composer.usageDetail.output")} ${(context.sessionCompletionTokens ?? 0).toLocaleString()}`
      + (context.sessionReasoningTokens
        ? ` · ${t("composer.usageDetail.reasoning")} ${context.sessionReasoningTokens.toLocaleString()}`
        : "")
    : undefined;

  return (
    <div className="context-panel">
      <div className="context-panel__body">
        <section className="context-panel__section">
          <SectionHeading title={t("context.runtimeMetrics")} />
          <div className="context-panel__stats">
            <MetricCard
              label={t("context.context")}
              value={ctxPct == null ? "-" : `${Math.round(ctxPct * 100)}%`}
              tone={ctxTone}
              meter={ctxPct ?? undefined}
              title={ctxWindow > 0 ? `${used.toLocaleString()} / ${ctxWindow.toLocaleString()} tokens` : undefined}
            />
            <MetricCard label={t("context.sessionTokens")} value={tokens > 0 ? tokens.toLocaleString() : "-"} title={tokensTitle} />
            <MetricCard label={t("context.cacheHit")} value={cachePct > 0 ? `${cachePct}%` : "-"} tone="accent" />
            <MetricCard label={t("context.cost")} value={cost != null && cost > 0 ? fmtCost(cost, context?.sessionCostCurrency) : "-"} />
            <MetricCard label={t("composer.usageDetail.elapsed")} value={elapsedMs > 0 ? fmtDuration(elapsedMs) : "-"} />
            <MetricCard label={t("context.requests")} value={requests > 0 ? String(requests) : "-"} />
          </div>
          <button
            className="btn btn--small context-panel__compact-btn"
            onClick={handleCompact}
            disabled={compacting || busy}
            title={t("composer.usageDetail.compactHint")}
          >
            {t("composer.usageDetail.compactLabel")}
          </button>
        </section>

        {turnFacts.length > 0 && (
          <section className="ctx-facts">
            <div className="ctx-facts__head">{t("turns.title")}</div>
            <div className="ctx-facts__row ctx-facts__row--head">
              <span>#</span>
              <span>{t("turns.colDuration")}</span>
              <span>{t("turns.colRetries")}</span>
              <span>{t("turns.colTools")}</span>
              <span>{t("turns.colTokens")}</span>
              <span>{t("turns.colCache")}</span>
            </div>
            {turnFacts.map((f) => {
              const turnCachePct = f.cacheHitTokens + f.cacheMissTokens > 0
                ? Math.round((f.cacheHitTokens / (f.cacheHitTokens + f.cacheMissTokens)) * 100)
                : null;
              return (
                <div key={f.seq} className={`ctx-facts__row${f.err ? " ctx-facts__row--err" : ""}`} title={f.err || undefined}>
                  <span>{f.seq}</span>
                  <span>{(f.durationMs / 1000).toFixed(1)}s</span>
                  <span>{f.retries > 0 ? `↻${f.retries}` : "—"}</span>
                  <span>{f.toolCalls}{f.toolErrors > 0 ? ` !${f.toolErrors}` : ""}</span>
                  <span>{fmtTokensShort(f.promptTokens)}/{fmtTokensShort(f.completionTokens)}</span>
                  <span>{turnCachePct === null ? "—" : `${turnCachePct}%`}</span>
                </div>
              );
            })}
          </section>
        )}
      </div>
    </div>
  );
}

function SectionHeading({ title, meta }: { title: string; meta?: string }) {
  return (
    <header className="context-panel__section-head">
      <h3>{title}</h3>
      {meta && <span>{meta}</span>}
    </header>
  );
}

function MetricCard({ label, value, tone, meter, title }: { label: string; value: string; tone?: "accent" | "good" | "notice" | "warn"; meter?: number; title?: string }) {
  const toneClass = tone ? ` context-panel__metric--${tone}` : "";
  return (
    <div className={`context-panel__metric${toneClass}`} title={title}>
      <span>{label}</span>
      <strong>{value}</strong>
      {meter != null && (
        <i className="context-panel__bar" aria-hidden="true">
          <i style={{ width: `${Math.min(100, Math.max(0, meter * 100))}%` }} />
        </i>
      )}
    </div>
  );
}
