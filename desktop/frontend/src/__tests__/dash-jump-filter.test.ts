import { describe, expect, it } from "vitest";
import { findingMatchesJump, proposalMatchesJump } from "../layouts/NetDevLayout";
import type { NetDevFinding, NetDevProposal } from "../lib/types";

// dash-jump-filter — 深链过滤语义（DASHBOARD spec §12 偏差 1 的兑现）。
const f = (over: Partial<NetDevFinding> = {}): NetDevFinding => ({
  id: "F-1", title: "OSPF 邻居 Down", severity: "critical", devices: ["SW-03"],
  detail: "…", evidence: [], created_at: "2026-08-29T10:00:00", ...over,
});

describe("findingMatchesJump", () => {
  it("parses severity:/id:/device: and semantic keywords", () => {
    expect(findingMatchesJump(f(), "severity:critical")).toBe(true);
    expect(findingMatchesJump(f(), "severity:warning")).toBe(false);
    expect(findingMatchesJump(f(), "id:F-1")).toBe(true);
    expect(findingMatchesJump(f(), "id:F-9")).toBe(false);
    expect(findingMatchesJump(f(), "device:SW-03")).toBe(true);
    expect(findingMatchesJump(f({ source: "assess:x", title: "弱口令确认" }), "assess")).toBe(true);
    expect(findingMatchesJump(f({ title: "弱口令确认" }), "assess")).toBe(true);
    expect(findingMatchesJump(f({ title: "基线：xxx" }), "baseline")).toBe(true);
    expect(findingMatchesJump(f({ source: "syslog:SW-03:x" }), "syslog")).toBe(true);
  });
  it("unknown filter falls back to substring (never an empty list by accident)", () => {
    expect(findingMatchesJump(f(), "OSPF")).toBe(true);
    expect(findingMatchesJump(f(), "zzz-not-there")).toBe(false);
    expect(findingMatchesJump(f(), "")).toBe(true);
  });
});

const p = (over: Partial<NetDevProposal> = {}): NetDevProposal => ({
  id: "P-1", intent: "调整 hello 定时器", status: "done",
  steps: [{ device: "SW-03", commands: [] }], created_at: "2026-08-29T10:00:00", ...over,
});

describe("proposalMatchesJump", () => {
  it("parses id:/device: and falls back to substring", () => {
    expect(proposalMatchesJump(p(), "id:P-1")).toBe(true);
    expect(proposalMatchesJump(p(), "device:SW-03")).toBe(true);
    expect(proposalMatchesJump(p(), "hello")).toBe(true);
    expect(proposalMatchesJump(p(), "")).toBe(true);
  });
});
