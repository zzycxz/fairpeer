import { describe, expect, it } from "vitest";
import { applyLiveEvent, isDeviceLive, isPseudoDevice, liveStateFromSnapshot, LIVE_LIMITS, type LiveOpsState } from "../lib/liveOpsState";
import type { NetDevLiveEvent } from "../lib/types";

function newState(): LiveOpsState {
  return liveStateFromSnapshot({ devices: [], spent: 0, budget: 30 });
}

const t = () => 1_787_000_000_000;

function ev(partial: Partial<NetDevLiveEvent>): NetDevLiveEvent {
  return { kind: "cmd_start", device: "sw1", time: t(), ...partial } as NetDevLiveEvent;
}

describe("live ops state (操作实况)", () => {
  it("a new user turn resets the per-ask budget counter", () => {
    const s = newState();
    applyLiveEvent(s, ev({ kind: "cmd_start", command: "display version" }));
    applyLiveEvent(s, ev({ kind: "cmd_end", command: "display version", status: "ok" }));
    applyLiveEvent(s, ev({ kind: "cmd_end", command: "display clock", status: "ok" }));
    expect(s.spent).toBe(2);
    applyLiveEvent(s, ev({ kind: "turn", device: "(turn)" }));
    expect(s.spent).toBe(0);
    // …and keeps counting from the next ok command, not from a stale value.
    applyLiveEvent(s, ev({ kind: "cmd_end", command: "display ip routing-table", status: "ok" }));
    expect(s.spent).toBe(1);
  });

  it("pseudo-device events never grow a device card", () => {
    const s = newState();
    applyLiveEvent(s, ev({ kind: "conn", device: "(emergency-stop)", state: "stopped" }));
    applyLiveEvent(s, ev({ kind: "cmd_refused", device: "(discover)", command: "x", reason: "r" }));
    applyLiveEvent(s, ev({ kind: "cmd_end", device: "", command: "x", status: "ok" }));
    expect(s.devices.size).toBe(0);
    expect(isPseudoDevice("(turn)")).toBe(true);
    expect(isPseudoDevice("core-sw-1")).toBe(false);
  });

  it("folds a full command lifecycle: start → output lines → end(ok), chips and tail in order", () => {
    const s = newState();
    applyLiveEvent(s, ev({ kind: "cmd_start", command: "display ospf peer" }));
    applyLiveEvent(s, ev({ kind: "cmd_output", chunk: " Area 0.0.0.0 neighbors\n" }));
    applyLiveEvent(s, ev({ kind: "cmd_output", chunk: " 10.0.0.2 Full GE0/0/1\n" }));
    applyLiveEvent(s, ev({ kind: "cmd_end", command: "display ospf peer", status: "ok", ms: 880, bytes: 168 }));

    const d = s.devices.get("sw1")!;
    expect(d.current).toBeNull(); // in-flight cleared
    expect(d.cmds[0]).toMatchObject({ command: "display ospf peer", status: "ok", ms: 880 });
    expect(d.tail.some((l) => l.includes("sw1# display ospf peer"))).toBe(true);
    expect(d.tail.some((l) => l.includes("10.0.0.2"))).toBe(true);
    expect(s.spent).toBe(1);
  });

  it("device-error ends spend no budget; refusals are loud", () => {
    const s = newState();
    applyLiveEvent(s, ev({ kind: "cmd_end", command: "display xxx", status: "device-error" }));
    expect(s.spent).toBe(0);
    applyLiveEvent(s, ev({ kind: "cmd_refused", command: "save", class: "write", reason: "写命令——只读" }));
    const d = s.devices.get("sw1")!;
    expect(d.cmds[0]).toMatchObject({ status: "refused", class: "write" });
    expect(s.guardrails[0]).toMatchObject({ device: "sw1", command: "save" });
  });

  it("tail and guardrail buffers are bounded (ring semantics)", () => {
    const s = liveStateFromSnapshot({ devices: [], spent: 0, budget: 0 });
    applyLiveEvent(s, ev({ kind: "cmd_start", command: "x" }));
    const d = s.devices.get("sw1")!;
    for (let i = 0; i < LIVE_LIMITS.tail + 50; i++) {
      applyLiveEvent(s, ev({ kind: "cmd_output", chunk: `line-${i}\n` }));
    }
    expect(d.tail.length).toBeLessThanOrEqual(LIVE_LIMITS.tail);
    expect(d.tail[d.tail.length - 1]).toContain("line-249");
    for (let i = 0; i < LIVE_LIMITS.guardrails + 5; i++) {
      applyLiveEvent(s, ev({ kind: "cmd_refused", command: `bad-${i}`, reason: "r" }));
    }
    expect(s.guardrails.length).toBe(LIVE_LIMITS.guardrails);
    expect(s.guardrails[0].command).toContain("bad-34"); // newest wins, oldest dropped
  });

  it("conn events update state/VTY; isDeviceLive follows state and recency", () => {
    const s = liveStateFromSnapshot({
      devices: [{ device: "sw1", vendor: "huawei", connected: false, vtyUse: 0, vtyCap: 2 }],
      spent: 0, budget: 30,
    });
    const d = s.devices.get("sw1")!;
    expect(isDeviceLive(d, t())).toBe(false); // untouched, unconnected
    applyLiveEvent(s, ev({ kind: "conn", state: "connected", vtyUse: 1, vtyCap: 2 }));
    expect(d.state).toBe("connected");
    expect(d.vtyUse).toBe(1);
    expect(isDeviceLive(d, t())).toBe(true);
    // Ten minutes later with no activity and a reaped session → idle.
    applyLiveEvent(s, ev({ kind: "conn", state: "idle-closed", vtyUse: 0 }));
    expect(isDeviceLive(d, t() + 11 * 60 * 1000)).toBe(false);
  });
});
