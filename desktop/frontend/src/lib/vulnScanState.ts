// vulnScanState — module store for the netdev dock's 蓝队核查 tab.
//
// Findings arrive through the layout-level onNetdevFindingSaved subscription
// (NetDevLayout installs it, always mounted while netdev is active), not
// inside the panel, so nothing is missed while the tab/dock is closed. The
// panel also re-seeds from NetDevFindings on mount; this store keeps the
// live tail. Pure transition separated for unit tests without a DOM.
import type { NetDevFinding } from "./types";

// The tab's lens: chat-driven vuln checks (source "vulnscan") and feed sweeps
// ("cve:*"). Everything else (baseline/assess/alert/nmap lead findings) keeps
// its existing home in the 发现 tab — this page is the blue-team workbench
// view, not a second findings queue.
export function isVulnScanSource(source?: string): boolean {
  return !!source && (source === "vulnscan" || source.startsWith("cve:"));
}

export interface VulnScanState {
  recent: NetDevFinding[]; // newest-first ring buffer, capped
  lastRelevantAt: number; // unix millis of the latest in-lens finding; 0 = none yet
  seq: number; // increments on every store change
}

const RECENT_CAP = 100;

export const initialVulnScanState: VulnScanState = {
  recent: [],
  lastRelevantAt: 0,
  seq: 0,
};

// applyVulnScanFinding is the pure transition. arrived reports "an in-lens
// finding just landed" — the signal NetDevLayout uses to hot-dot and (once,
// until the user closes the dock themselves) auto-open the tab. Rolling
// re-saves (same ID, e.g. the cve:sweep summary) replace in place instead of
// piling up.
export function applyVulnScanFinding(
  state: VulnScanState,
  f: NetDevFinding,
): { state: VulnScanState; arrived: boolean } {
  const inLens = isVulnScanSource(f.source);
  const prevIdx = f.id ? state.recent.findIndex((r) => r.id === f.id) : -1;
  let recent = state.recent;
  if (prevIdx >= 0) {
    recent = state.recent.map((r, i) => (i === prevIdx ? f : r));
  } else {
    recent = [f, ...state.recent];
    if (recent.length > RECENT_CAP) recent = recent.slice(0, RECENT_CAP);
  }
  return {
    state: {
      recent,
      lastRelevantAt: inLens ? Date.now() : state.lastRelevantAt,
      seq: state.seq + 1,
    },
    arrived: inLens,
  };
}

// --- live module store ---------------------------------------------------------

let vulnScanState: VulnScanState = initialVulnScanState;
const vulnScanListeners = new Set<() => void>();

export function vulnScanSnapshot(): VulnScanState {
  return vulnScanState;
}

export function subscribeVulnScan(fn: () => void): () => void {
  vulnScanListeners.add(fn);
  return () => {
    vulnScanListeners.delete(fn);
  };
}

// pushVulnScanFinding applies one saved finding and notifies subscribers;
// returns whether it was an in-lens arrival (dot / auto-open signal).
export function pushVulnScanFinding(f: NetDevFinding): boolean {
  const result = applyVulnScanFinding(vulnScanState, f);
  vulnScanState = result.state;
  for (const listener of vulnScanListeners) listener();
  return result.arrived;
}

// resetVulnScanStore restores the initial state (tests / profile switch).
export function resetVulnScanStore(): void {
  vulnScanState = initialVulnScanState;
  for (const listener of vulnScanListeners) listener();
}
