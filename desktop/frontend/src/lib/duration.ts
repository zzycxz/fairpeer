// Duration formatting for telemetry readouts (usage chip hover, turns tab).
// Compact "1h 23m" / "23m 45s" / "45s" style — monospace-friendly, locale-neutral.

export function fmtDuration(ms: number): string {
  const totalSeconds = Math.max(1, Math.round(ms / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}
