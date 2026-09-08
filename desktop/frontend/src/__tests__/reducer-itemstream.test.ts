// reducer-itemstream.test.ts — useController.applyEvent golden tests.
// Two layers:
//   1. legacy goldens: the flat-event reducer behavior is pinned as-is
//      (text streaming, tool lifecycle, message finalize) — the safety net
//      the Spec-5 Phase 2 migration rides on.
//   2. item-stream: agent text/reasoning render FROM item events once
//      observed (itemDriven), legacy twins suppressed; interleaved
//      adapter-order sequences never double-append.
import { describe, expect, it } from "vitest";
import { applyEvent, initialState } from "../lib/useController";
import type { WireEvent, WireItemEvent } from "../lib/types";

const ev = (e: Partial<WireEvent> & { kind: WireEvent["kind"] }): WireEvent => e as WireEvent;
const item = (i: Partial<WireItemEvent> & Pick<WireItemEvent, "phase" | "itemId" | "itemKind">): WireEvent =>
  ev({ kind: "item", item: i as WireItemEvent });
const run = (events: WireEvent[]) => events.reduce(applyEvent, initialState);
const assistantItems = (s: ReturnType<typeof run>) => s.items.filter((it) => it.kind === "assistant");

describe("applyEvent legacy goldens", () => {
  it("turn_started pre-creates a streaming assistant bubble", () => {
    const s = run([ev({ kind: "turn_started" })]);
    expect(s.running).toBe(true);
    expect(s.turnActive).toBe(true);
    expect(assistantItems(s)).toHaveLength(1);
    expect(assistantItems(s)[0]).toMatchObject({ streaming: true, text: "", reasoning: "" });
  });

  it("text deltas accumulate into live and turn_done finalizes", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      ev({ kind: "text", text: "Hel" }),
      ev({ kind: "text", text: "lo" }),
      ev({ kind: "turn_done" }),
    ]);
    const a = assistantItems(s)[0] as Extract<(typeof s.items)[number], { kind: "assistant" }>;
    expect(a.text).toBe("Hello");
    expect(a.streaming).toBe(false);
    expect(s.running).toBe(false);
  });

  it("message finalizes with full text and reasoning", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      ev({ kind: "reasoning", reasoning: "thinking…" }),
      ev({ kind: "text", text: "answer" }),
      ev({ kind: "message", text: "answer", reasoning: "thinking…" }),
    ]);
    const a = assistantItems(s)[0] as Extract<(typeof s.items)[number], { kind: "assistant" }>;
    expect(a).toMatchObject({ text: "answer", reasoning: "thinking…", streaming: false });
    expect(s.live).toBeUndefined();
  });

  it("tool lifecycle: dispatch → progress → result", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      ev({ kind: "tool_dispatch", tool: { name: "bash", args: "{}", readOnly: false, id: "t1" } }),
      ev({ kind: "tool_progress", tool: { name: "bash", readOnly: false, id: "t1", output: "out1\n" } }),
      ev({ kind: "tool_progress", tool: { name: "bash", readOnly: false, id: "t1", output: "out2" } }),
      ev({ kind: "tool_result", tool: { name: "bash", readOnly: false, id: "t1", output: "out1\nout2", durationMs: 42 } }),
      ev({ kind: "turn_done" }),
    ]);
    const tool = s.items.find((it) => it.kind === "tool") as Extract<(typeof s.items)[number], { kind: "tool" }>;
    expect(tool).toMatchObject({ id: "t1", name: "bash", status: "done", output: "out1\nout2", durationMs: 42, isShell: true });
  });

  it("notice items append and level passes through", () => {
    const s = run([ev({ kind: "notice", level: "warn", text: "careful" })]);
    expect(s.items.some((it) => it.kind === "notice" && it.text === "careful" && it.level === "warn")).toBe(true);
  });
});

describe("applyEvent item stream (Spec-5 Phase 2)", () => {
  it("interleaved adapter-order stream never double-appends", () => {
    // ItemAdapter emits legacy first, then the item twin; item_started
    // carries no delta — so "Hel" applies once via legacy, "lo" once via
    // the item delta.
    const s = run([
      ev({ kind: "turn_started" }),
      ev({ kind: "text", text: "Hel" }),
      item({ phase: "item_started", itemId: "a-1", itemKind: "agent_message" }),
      ev({ kind: "text", text: "lo" }),
      item({ phase: "item_delta", itemId: "a-1", itemKind: "agent_message", delta: "lo" }),
      ev({ kind: "message", text: "Hello", reasoning: "" }),
      item({ phase: "item_completed", itemId: "a-1", itemKind: "agent_message", item: { text: "Hello", reasoning: "" } }),
      ev({ kind: "turn_done" }),
    ]);
    const a = assistantItems(s)[0] as Extract<(typeof s.items)[number], { kind: "assistant" }>;
    expect(a.text).toBe("Hello");
    expect(a.streaming).toBe(false);
  });

  it("suppression: after itemDriven, stray legacy text does not double", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      item({ phase: "item_started", itemId: "a-1", itemKind: "agent_message" }),
      item({ phase: "item_delta", itemId: "a-1", itemKind: "agent_message", delta: "abc" }),
      ev({ kind: "text", text: "abc" }), // duplicate legacy twin — must be suppressed
    ]);
    expect(s.live?.text).toBe("abc");
  });

  it("pure item stream (no legacy events) renders and finalizes", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      item({ phase: "item_started", itemId: "a-9", itemKind: "agent_message" }),
      item({ phase: "item_delta", itemId: "a-9", itemKind: "agent_message", delta: "ite" }),
      item({ phase: "item_delta", itemId: "a-9", itemKind: "agent_message", delta: "mstream" }),
      item({ phase: "item_completed", itemId: "a-9", itemKind: "agent_message", item: { text: "itemstream", reasoning: "" } }),
      ev({ kind: "turn_done" }),
    ]);
    const a = assistantItems(s)[0] as Extract<(typeof s.items)[number], { kind: "assistant" }>;
    expect(a.text).toBe("itemstream");
    expect(a.streaming).toBe(false);
  });

  it("reasoning item deltas accumulate into live.reasoning", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      item({ phase: "item_started", itemId: "r-1", itemKind: "reasoning" }),
      item({ phase: "item_delta", itemId: "r-1", itemKind: "reasoning", delta: "step1 " }),
      item({ phase: "item_delta", itemId: "r-1", itemKind: "reasoning", delta: "step2" }),
    ]);
    expect(s.live?.reasoning).toBe("step1 step2");
  });

  it("itemDriven resets each turn", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      item({ phase: "item_delta", itemId: "a-1", itemKind: "agent_message", delta: "x" }),
      ev({ kind: "turn_done" }),
      ev({ kind: "turn_started" }),
    ]);
    expect(s.itemDriven).toBe(false);
  });

  it("interleaved tool lifecycle renders once with output streaming", () => {
    // Adapter order: legacy first, item twin second — the first dispatch
    // creates the card, the item twin flips itemDriven, subsequent legacy
    // twins are suppressed, output arrives via categorized item deltas.
    const s = run([
      ev({ kind: "turn_started" }),
      ev({ kind: "tool_dispatch", tool: { name: "bash", args: "{}", readOnly: false, id: "t1" } }),
      item({ phase: "item_started", itemId: "t1", itemKind: "tool_call", item: { name: "bash", args: "{}", read_only: false, status: "running" } }),
      ev({ kind: "tool_progress", tool: { name: "bash", readOnly: false, id: "t1", output: "out1" } }),
      item({ phase: "item_delta", itemId: "t1", itemKind: "tool_call", delta: "out1", deltaKind: "output" }),
      ev({ kind: "tool_progress", tool: { name: "bash", readOnly: false, id: "t1", output: "out2" } }),
      item({ phase: "item_delta", itemId: "t1", itemKind: "tool_call", delta: "out2", deltaKind: "output" }),
      ev({ kind: "tool_result", tool: { name: "bash", readOnly: false, id: "t1", output: "out1out2", durationMs: 9 } }),
      item({ phase: "item_completed", itemId: "t1", itemKind: "tool_call", item: { name: "bash", output: "out1out2", duration_ms: 9, status: "done" } }),
      ev({ kind: "turn_done" }),
    ]);
    const tools = s.items.filter((it) => it.kind === "tool");
    expect(tools).toHaveLength(1);
    const tool = tools[0] as Extract<(typeof s.items)[number], { kind: "tool" }>;
    expect(tool).toMatchObject({ id: "t1", name: "bash", status: "done", output: "out1out2", durationMs: 9, isShell: true });
  });

  it("pure item tool stream: started → args delta → output delta → completed", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      item({ phase: "item_started", itemId: "t9", itemKind: "tool_call", item: { name: "apply_patch", args: "{}", read_only: false, status: "running", parent_id: "p1" } }),
      item({ phase: "item_delta", itemId: "t9", itemKind: "tool_call", delta: "*** Begin Patch", deltaKind: "args" }),
      item({ phase: "item_delta", itemId: "t9", itemKind: "tool_call", delta: "patched", deltaKind: "output" }),
      item({ phase: "item_completed", itemId: "t9", itemKind: "tool_call", item: { name: "apply_patch", status: "done", output: "patched", truncated: true } }),
    ]);
    const tool = s.items.find((it) => it.kind === "tool") as Extract<(typeof s.items)[number], { kind: "tool" }>;
    expect(tool).toMatchObject({ id: "t9", name: "apply_patch", status: "done", output: "patched", truncated: true, parentId: "p1" });
    expect(s.itemDriven).toBe(true);
  });

  it("tool item error status finalizes as error with message", () => {
    const s = run([
      ev({ kind: "turn_started" }),
      item({ phase: "item_started", itemId: "t2", itemKind: "tool_call", item: { name: "web_fetch", read_only: true, status: "running" } }),
      item({ phase: "item_completed", itemId: "t2", itemKind: "tool_call", item: { name: "web_fetch", status: "error", err: "boom" } }),
    ]);
    const tool = s.items.find((it) => it.kind === "tool") as Extract<(typeof s.items)[number], { kind: "tool" }>;
    expect(tool).toMatchObject({ status: "error", error: "boom" });
  });
});
