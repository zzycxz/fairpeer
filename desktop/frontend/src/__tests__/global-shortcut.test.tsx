// @vitest-environment jsdom
// global-shortcut.test.ts — deterministic verification of the useGlobalShortcut
// mechanism (upgrade spec 0-7). The browser-GUI pass couldn't drive synthetic
// input through the IAB backend, so the wiring is proven here instead: a real
// KeyboardEvent dispatched on window must reach the capture listener with
// exact-modifier matching, and unknown actions must bind nothing.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, cleanup } from "@testing-library/react";
import { useGlobalShortcut } from "../lib/keyboardShortcuts";

function Harness({ action, cb }: { action: string; cb: () => void }) {
  useGlobalShortcut(action, cb);
  return null;
}

function key(k: string, opts: KeyboardEventInit = {}) {
  window.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...opts }));
}

beforeEach(() => { vi.useRealTimers(); });
afterEach(cleanup);

describe("useGlobalShortcut", () => {
  it("fires the callback on the bound combo (Ctrl+K)", () => {
    const cb = vi.fn();
    render(<Harness action="commandPalette.open" cb={cb} />);
    key("k", { ctrlKey: true });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("accepts Cmd (metaKey) as the primary modifier", () => {
    const cb = vi.fn();
    render(<Harness action="commandPalette.open" cb={cb} />);
    key("k", { metaKey: true });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("ignores the combo without the modifier", () => {
    const cb = vi.fn();
    render(<Harness action="commandPalette.open" cb={cb} />);
    key("k");
    expect(cb).not.toHaveBeenCalled();
  });

  it("ignores extra modifiers (Ctrl+Shift+K is not Ctrl+K)", () => {
    const cb = vi.fn();
    render(<Harness action="commandPalette.open" cb={cb} />);
    key("k", { ctrlKey: true, shiftKey: true });
    expect(cb).not.toHaveBeenCalled();
  });

  it("matches key case-insensitively (Ctrl+I dashboard)", () => {
    const cb = vi.fn();
    render(<Harness action="agents.dashboard" cb={cb} />);
    key("I", { ctrlKey: true });
    expect(cb).toHaveBeenCalledTimes(1);
  });

  it("prevents default on match so the browser never sees the combo", () => {
    const cb = vi.fn();
    render(<Harness action="shell.toggle" cb={cb} />);
    const ev = new KeyboardEvent("keydown", { key: "b", ctrlKey: true, bubbles: true, cancelable: true });
    window.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
  });

  it("binds nothing for an unknown action", () => {
    const cb = vi.fn();
    render(<Harness action="no.such.action" cb={cb} />);
    key("k", { ctrlKey: true });
    key("i", { ctrlKey: true });
    expect(cb).not.toHaveBeenCalled();
  });

  it("unbinds on unmount", () => {
    const cb = vi.fn();
    const { unmount } = render(<Harness action="commandPalette.open" cb={cb} />);
    unmount();
    key("k", { ctrlKey: true });
    expect(cb).not.toHaveBeenCalled();
  });
});
