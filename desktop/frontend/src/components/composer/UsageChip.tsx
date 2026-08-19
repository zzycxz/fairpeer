// UsageChip — compact context-window meter for the composer param row
// (ui-redesign spec §4-C3): a 46px fill bar plus inline token readings. The
// chip itself carries the numbers (session tokens · used/window), so no hover
// tooltip is needed; aria-label keeps the reading screen-reader friendly. The
// fill turns warn-coloured past 85% so compaction pressure is visible at a
// glance. Data comes from App state (ContextInfo), the same source StatusBar
// reads.
import { useT } from "../../lib/i18n";
import type { ContextInfo } from "../../lib/types";

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

export function UsageChip({ context }: { context?: ContextInfo }) {
  const t = useT();
  if (!context || !context.window || context.window <= 0) return null;
  const pct = Math.min(100, Math.round((context.used / context.window) * 100));
  const hot = pct >= 85;
  const session = context.sessionTokens > 0 ? context.sessionTokens : context.used;
  return (
    <div
      className={`composer-usage${hot ? " composer-usage--hot" : ""}`}
      aria-label={`${t("composer.contextUsage")} ${pct}% · ${t("composer.sessionTokens")} ${fmtTokens(session)}`}
    >
      <span className="composer-usage__bar" aria-hidden="true">
        <i style={{ width: `${pct}%` }} />
      </span>
      <span className="composer-usage__text">
        {fmtTokens(session)} · {fmtTokens(context.used)}/{fmtTokens(context.window)}
      </span>
    </div>
  );
}
