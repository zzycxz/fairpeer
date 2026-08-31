// @vitest-environment jsdom
// dash-boards.test.tsx — 大屏家族前端冒烟（DASHBOARD spec §9.13/§9.14）：
// OverviewPanel 用喂入快照渲染（不落桥）、新鲜度徽章随 generated_at 切换、
// dock 档零定时器纪律（源码级断言：组件内不得出现 setInterval）、DashShell
// 挂载即渲染页签条（mock bridge 供数）。
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import OverviewPanel from "../components/netdev/OverviewPanel";
import DashShell from "../components/netdev/DashShell";
import { LocaleProvider } from "../lib/i18n";
import type { NetDevOverviewSnapshot } from "../lib/types";

const testDir = dirname(fileURLToPath(import.meta.url));

function snap(over: Partial<NetDevOverviewSnapshot> = {}): NetDevOverviewSnapshot {
  return {
    generated_at: Date.now(), stale_after_sec: 300,
    coverage: { managed: 3, discovered: 2, unreachable: 1, no_snmp: 1 },
    health: { polled: 2, reachable: 1, last_poll_at: Date.now(), flap_alerts: 1, p90_alerts: 0, uptime_spark: {} },
    risk: { critical: 1, warning: 2, info: 3, open_total: 6, weighted_score: 17, risk_level: "high", cve_matches: 0, cve_needs_feed: true, weak_creds: 1 },
    inflight: { proposals_pending: 2, proposals_watchable: 1, jobs_running: 1, jobs_paused: 0, cutovers_active: 0, terminals_open: 1 },
    events: [{ id: "F-1", severity: "warning", title: "link-flap SW-03", source: "syslog:SW-03:link-flap", at: "08-29 10:00" }],
    audit: { chain_ok: true, chain_total: 40, last_entry_at: "08-29 10:05", read_24h: 20, write_24h: 2, guardrail_24h: 0 },
    stats: {
      cve_needs_feed: true, job_done: 3, job_finished: 4,
      cmd_mix: { read: 18, write: 2 }, audit_entries: 20,
      device_by_role: { switch: 2, router: 1 }, proposal_funnel: { draft: 2, done: 1 },
    },
    scenario_cutover_active: false, scenario_discovery_run: false,
    ...over,
  };
}

describe("OverviewPanel（总览屏）", () => {
  it("renders the fed snapshot: score, x/y denominators, guide states", () => {
    const { container } = render(<LocaleProvider><OverviewPanel snapshot={snap()} /></LocaleProvider>);
    expect(screen.getByText("17")).toBeTruthy();          // weighted score
    const text = container.textContent ?? "";
    expect(/可达 1\/2|reachable 1\/2/.test(text)).toBe(true); // honest x/y
    expect(/1 台未开 SNMP 轮询|1 without SNMP polling/.test(text)).toBe(true);
    // CVE 无 feed 与弱口令走引导/紧急项，不是 0
    expect(/先导入 CVE feed|Import a CVE feed/.test(text)).toBe(true);
    expect(/弱口令 1|weak creds 1/.test(text)).toBe(true);
  });

  it("stale badge flips when generated_at passes stale_after_sec", () => {
    const stale = snap({ generated_at: Date.now() - 600_000 });
    const { container } = render(<LocaleProvider><OverviewPanel compact snapshot={stale} /></LocaleProvider>);
    const badge = container.querySelector(".ndv-ovw__fresh");
    expect(badge?.getAttribute("data-fresh")).toBe("false");
  });

  it("dock 档零定时器（§8.4）：源码不得出现 setInterval 调用", () => {
    const src = readFileSync(join(testDir, "..", "components", "netdev", "OverviewPanel.tsx"), "utf-8");
    expect(src.includes("setInterval(")).toBe(false); // 注释里的词不算，只拦真调用
  });
});

describe("DashShell（壳）", () => {
  it("mounts with the five screen tabs (mock bridge feeds overview)", () => {
    render(
      <LocaleProvider>
        <DashShell initialScreen="overview" onClose={() => {}} />
      </LocaleProvider>,
    );
    // 五枚页签键全部渲染（zh/en 任一文案命中即可）
    for (const re of [/总览|Overview/, /调查链|Chain/, /割接|Cutover/, /发现|Discovery/, /暴露面|Exposure/]) {
      expect(screen.getAllByText(re).length).toBeGreaterThan(0);
    }
  });
});
