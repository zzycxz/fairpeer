// Pure logic tests for the browser-mirror store transition (no DOM needed).
//
// Run: tsx src/__tests__/browser-mirror.test.ts

import { applyBrowserMirrorFrame, type BrowserMirrorState } from "../lib/browserMirror";
import type { BrowserMirrorFrame } from "../lib/types";

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

const initial: BrowserMirrorState = { image: "", url: "", source: "", running: false, lastText: "", seq: 0 };

console.log("\nrun lifecycle (browser_auto)");
const started = applyBrowserMirrorFrame(initial, {
  kind: "status",
  source: "auto",
  phase: "start",
  text: "Chrome",
} as BrowserMirrorFrame);
eq(started.state.running, true, "start marks running");
eq(started.state.source, "auto", "start records source");
eq(started.state.lastText, "Chrome", "start records browser label");
eq(started.startedActivity, true, "start reports activity onset");

const framed = applyBrowserMirrorFrame(started.state, {
  kind: "frame",
  source: "auto",
  text: "click('Login')",
  image: "data:image/png;base64,QQ",
} as BrowserMirrorFrame);
eq(framed.state.image, "data:image/png;base64,QQ", "frame stores screenshot");
eq(framed.state.lastText, "click('Login')", "frame captions with latest action");
eq(framed.startedActivity, false, "frame mid-run is not activity onset");
eq(framed.state.running, true, "frame keeps running");
eq(framed.state.seq, started.state.seq + 1, "seq increments");

const stepped = applyBrowserMirrorFrame(framed.state, {
  kind: "status",
  source: "auto",
  phase: "step",
  text: "goto https://example.com",
} as BrowserMirrorFrame);
eq(stepped.state.lastText, "goto https://example.com", "step updates caption text");
eq(stepped.state.running, true, "step does not change running");
eq(stepped.startedActivity, false, "step is not activity onset");

const ended = applyBrowserMirrorFrame(stepped.state, {
  kind: "status",
  source: "auto",
  phase: "end",
  text: "task complete",
} as BrowserMirrorFrame);
eq(ended.state.running, false, "end clears running");
eq(ended.state.lastText, "task complete", "end keeps the summary");
eq(ended.state.image, framed.state.image, "end keeps the last frame visible");
eq(ended.startedActivity, false, "end is not activity onset");

const restarted = applyBrowserMirrorFrame(ended.state, {
  kind: "status",
  source: "tool",
  phase: "start",
  text: "Edge",
} as BrowserMirrorFrame);
eq(restarted.startedActivity, true, "a new start after end is activity onset again");
eq(restarted.state.source, "tool", "source switches between runs");

console.log("\nlate-joining frames (app restarted into an open session)");
const late = applyBrowserMirrorFrame(initial, {
  kind: "frame",
  source: "tool",
  url: "https://example.com",
  image: "data:image/png;base64,AA",
} as BrowserMirrorFrame);
eq(late.startedActivity, true, "frame with no start seen counts as activity onset");
eq(late.state.url, "https://example.com", "tool frame records page url");
eq(late.state.running, false, "running stays false until a start arrives");

console.log("\nunknown payloads");
const unknown = applyBrowserMirrorFrame(initial, { kind: "status", source: "tool" } as BrowserMirrorFrame);
eq(unknown.state === initial, true, "status without phase is a no-op");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);

// Per-session frames: the ops viewer lists the console session plus
// agent-driven sessions — frames tagged with session_id land in `sessions`
// while the aggregate fields keep their historical behavior.
let ps = 0;
function st() {
  return applyBrowserMirrorFrame(base(), { kind: "frame", source: "tool", image: "i" + ++ps, url: "u" + ps, session_id: "br_" + ps });
}
function base(): BrowserMirrorState {
  return { image: "", url: "", source: "", running: false, lastText: "", seq: 0, sessions: {} };
}
const s1 = st();
eq(s1.state.sessions["br_1"] !== undefined && s1.state.sessions["br_1"].image === "i1", true, "frame lands in its session bucket");
const s2 = applyBrowserMirrorFrame(s1.state, { kind: "frame", source: "tool", image: "i1b", url: "u1b", session_id: "br_1" }).state;
eq(s2.sessions["br_1"].image, "i1b", "latest frame per session wins");
const s3 = applyBrowserMirrorFrame(s2, { kind: "status", source: "tool", phase: "start", text: "Chrome", session_id: "br_9" }).state;
eq(s3.sessions["br_9"] !== undefined, true, "start status registers the session");
const unt = applyBrowserMirrorFrame(s3, { kind: "frame", source: "tool", image: "noSession" }).state;
eq(Object.keys(unt.sessions).length, 2, "untagged frames do not create buckets");

console.log("\nsession end evicts its bucket (ghost chips fix)");
const withSess = applyBrowserMirrorFrame(base(), {
  kind: "frame", source: "tool", session_id: "br_7", image: "i7", url: "https://a",
} as BrowserMirrorFrame);
eq(withSess.state.sessions["br_7"] !== undefined, true, "session bucket exists while live");
const sessEnded = applyBrowserMirrorFrame(withSess.state, {
  kind: "status", source: "tool", phase: "end", session_id: "br_7", text: "done",
} as BrowserMirrorFrame);
eq(sessEnded.state.sessions["br_7"], undefined, "end drops the session bucket (no ghost chips)");
eq(sessEnded.state.image, "i7", "aggregate image still keeps the last frame");
