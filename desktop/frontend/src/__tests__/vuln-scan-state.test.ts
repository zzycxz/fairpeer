// Pure logic tests for the vuln-scan store transition (no DOM needed).
//
// Run: tsx src/__tests__/vuln-scan-state.test.ts

import {
  applyVulnScanFinding,
  initialVulnScanState,
  isVulnScanSource,
  type VulnScanState,
} from "../lib/vulnScanState";
import type { NetDevFinding } from "../lib/types";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function finding(id: string, source?: string): NetDevFinding {
  return {
    id,
    title: `t-${id}`,
    severity: "warning",
    devices: ["sw1"],
    detail: "",
    evidence: [],
    created_at: new Date().toISOString(),
    source,
  } as NetDevFinding;
}

console.log("\nsource lens");
eq(isVulnScanSource("vulnscan"), true, "vulnscan source is in lens");
eq(isVulnScanSource("cve:sweep"), true, "cve: prefix is in lens");
eq(isVulnScanSource("cve"), false, "bare cve (no colon) is not a real source");
eq(isVulnScanSource("assess:weak-cred:edge:gw-1"), false, "assess findings stay out");
eq(isVulnScanSource("nmap:sweep:10.0.0.0/24"), false, "nmap lead findings stay out");
eq(isVulnScanSource(""), false, "human/AI findings (no source) stay out");
eq(isVulnScanSource(undefined), false, "undefined source stays out");

console.log("\ntransition");
let state: VulnScanState = initialVulnScanState;
const r1 = applyVulnScanFinding(state, finding("F1", "vulnscan"));
eq(r1.arrived, true, "in-lens finding reports arrival");
eq(r1.state.recent.length, 1, "in-lens finding lands in the ring");
eq(r1.state.lastRelevantAt > 0, true, "arrival stamps lastRelevantAt");
eq(r1.state.seq, 1, "seq increments");
state = r1.state;

const r2 = applyVulnScanFinding(state, finding("F2", "baseline"));
eq(r2.arrived, false, "out-of-lens finding does not report arrival");
eq(r2.state.recent.length, 2, "out-of-lens finding still lands (context)");
eq(r2.state.lastRelevantAt, state.lastRelevantAt, "lastRelevantAt untouched by out-of-lens");
state = r2.state;

const r3 = applyVulnScanFinding(state, finding("F1", "vulnscan"));
eq(r3.state.recent.length, 2, "rolling re-save with same ID replaces, not piles");
eq(
  r3.state.recent.filter((f) => f.id === "F1").length,
  1,
  "exactly one F1 after re-save",
);
eq(r3.arrived, true, "in-lens re-save still counts as arrival (dot refresh)");
state = r3.state;

const r4 = applyVulnScanFinding(state, finding("", "vulnscan"));
eq(r4.state.recent.length, 3, "no-ID finding prepends (no dedup possible)");
state = r4.state;

for (let i = 0; i < 120; i++) {
  state = applyVulnScanFinding(state, finding(`X${i}`, "cve:sweep")).state;
}
eq(state.recent.length, 100, "ring caps at 100");
eq(state.recent[0].id, "X119", "newest stays first after cap");

if (failed > 0) {
  console.error(`\n${failed} FAILED, ${passed} passed\n`);
  process.exit(1);
}
console.log(`\nall ${passed} passed\n`);
