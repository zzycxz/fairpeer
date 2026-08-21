// liveOpsState — the 操作实况 panel's pure state machine, extracted from the
// component so the tricky bits (per-turn budget reset, pseudo-device guard,
// ring buffers, refusal feed) are unit-testable without React.

import type { NetDevLiveEvent, NetDevLiveSnapshot } from "./types";

export interface LiveCmd {
  command: string;
  class: string;
  status: string; // running | ok | device-error | failure | refused
  ms?: number;
  bytes?: number;
  at: number;
}

export interface LiveDevice {
  device: string;
  vendor: string;
  os?: string;
  group?: string;
  state: string; // connected | connecting | reconnecting | stopped | idle-closed | ""
  vtyUse: number;
  vtyCap: number;
  tail: string[]; // terminal-textured lines (incl. command separator lines)
  current: LiveCmd | null; // in-flight command
  cmds: LiveCmd[]; // most recent first
  lastAt: number; // last activity time (ms)
}

export interface LiveGuardrail {
  device: string;
  command: string;
  reason: string;
  at: number;
}

export interface LiveOpsState {
  devices: Map<string, LiveDevice>;
  spent: number;
  budget: number; // 0 = unlimited
  guardrails: LiveGuardrail[];
}

// Ring-buffer bounds (per device / panel-wide).
export const LIVE_LIMITS = { tail: 200, cmds: 50, guardrails: 30 } as const;

// isPseudoDevice: synthetic event sources ("(turn)", "(emergency-stop)",
// "(discover)"…) carry no session state and must never grow a device card.
export function isPseudoDevice(name: string | undefined): boolean {
  return !name || name.startsWith("(");
}

export function liveStateFromSnapshot(snap: NetDevLiveSnapshot | null | undefined): LiveOpsState {
  const state: LiveOpsState = { devices: new Map(), spent: 0, budget: 0, guardrails: [] };
  if (!snap) return state;
  state.spent = snap.spent ?? 0;
  state.budget = snap.budget ?? 0;
  for (const d of snap.devices ?? []) {
    state.devices.set(d.device, {
      device: d.device, vendor: d.vendor, os: d.os, group: d.group,
      state: d.connected ? "connected" : "",
      vtyUse: d.vtyUse ?? 0, vtyCap: d.vtyCap ?? 0,
      tail: [], current: null, cmds: [], lastAt: 0,
    });
  }
  return state;
}

function pushTail(d: LiveDevice, lines: string[]) {
  for (const line of lines) d.tail.push(line);
  if (d.tail.length > LIVE_LIMITS.tail) d.tail.splice(0, d.tail.length - LIVE_LIMITS.tail);
}

function pushCmd(d: LiveDevice, cmd: LiveCmd) {
  d.cmds.unshift(cmd);
  if (d.cmds.length > LIVE_LIMITS.cmds) d.cmds.length = LIVE_LIMITS.cmds;
}

function clockOf(ms: number): string {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

// applyLiveEvent folds one event into the state (mutates). Returns nothing;
// React bumps a tick after each batch.
export function applyLiveEvent(state: LiveOpsState, ev: NetDevLiveEvent) {
  // Panel-wide events first — they never create a device card.
  if (ev.kind === "turn") {
    state.spent = 0; // per-ask budget reset (backend TurnBegin fired)
    return;
  }
  if (isPseudoDevice(ev.device)) return;

  let d = state.devices.get(ev.device);
  if (!d) {
    d = {
      device: ev.device, vendor: "", state: "", vtyUse: 0, vtyCap: 0,
      tail: [], current: null, cmds: [], lastAt: 0,
    };
    state.devices.set(ev.device, d);
  }
  d.lastAt = ev.time;

  switch (ev.kind) {
    case "conn":
      d.state = ev.state ?? d.state;
      d.vtyUse = ev.vtyUse ?? d.vtyUse;
      d.vtyCap = ev.vtyCap ?? d.vtyCap;
      break;
    case "cmd_start":
      d.current = { command: ev.command ?? "", class: ev.class ?? "read", status: "running", at: ev.time };
      pushTail(d, [`[${clockOf(ev.time)}] ${ev.device}# ${ev.command}`]);
      break;
    case "cmd_output": {
      const text = (ev.chunk ?? "").replace(/\n+$/, "");
      if (text) pushTail(d, text.split("\n"));
      break;
    }
    case "cmd_end": {
      const done: LiveCmd = {
        command: d.current?.command ?? ev.command ?? "",
        class: d.current?.class ?? ev.class ?? "read",
        status: ev.status ?? "ok",
        ms: ev.ms,
        bytes: ev.bytes,
        at: ev.time,
      };
      d.current = null;
      pushCmd(d, done);
      if (done.status === "ok") state.spent += 1;
      break;
    }
    case "cmd_refused":
      pushCmd(d, { command: ev.command ?? "", class: ev.class ?? "guardrail", status: "refused", at: ev.time });
      state.guardrails.unshift({ device: ev.device, command: ev.command ?? "", reason: ev.reason ?? "", at: ev.time });
      if (state.guardrails.length > LIVE_LIMITS.guardrails) state.guardrails.length = LIVE_LIMITS.guardrails;
      break;
  }
}

// isDeviceLive: connected/connecting/reconnecting, in-flight command, or
// activity within the last 10 minutes — otherwise it collapses to the idle row.
export function isDeviceLive(d: LiveDevice, now: number): boolean {
  return (
    d.state === "connected" ||
    d.state === "connecting" ||
    d.state === "reconnecting" ||
    d.current !== null ||
    now - d.lastAt < 10 * 60 * 1000
  );
}
