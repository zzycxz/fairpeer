// browserMirror — module store for the cowork dock's 浏览器 mirror tab.
//
// Frames arrive through the App-level onBrowserMirror subscription (always
// mounted), not inside the panel, so nothing is missed while the tab/dock is
// closed; the panel and the dock's tab auto-switch both read this snapshot.
// The pure transition (applyBrowserMirrorFrame) is separated from the live
// store so it can be unit-tested without a DOM.
import type { BrowserMirrorFrame } from "./types";

export interface BrowserMirrorState {
  image: string; // latest screenshot (data URL); "" before the first frame
  url: string; // latest page URL (tool source)
  source: "tool" | "auto" | "";
  running: boolean; // between status start and end
  lastText: string; // browser name / latest action / run summary
  seq: number; // increments on every store change
}

const initialBrowserMirrorState: BrowserMirrorState = {
  image: "",
  url: "",
  source: "",
  running: false,
  lastText: "",
  seq: 0,
};

// applyBrowserMirrorFrame is the pure state transition. startedActivity
// reports "onset of a new browsing activity" — the signal App uses to
// auto-open the dock: a run/session start, or the first frame after the app
// (re)started into an already-open browser session whose start was never seen.
export function applyBrowserMirrorFrame(
  state: BrowserMirrorState,
  frame: BrowserMirrorFrame,
): { state: BrowserMirrorState; startedActivity: boolean } {
  if (frame.kind === "frame") {
    return {
      state: {
        ...state,
        image: frame.image ?? "",
        url: frame.url || state.url,
        source: frame.source,
        lastText: frame.text || state.lastText,
        seq: state.seq + 1,
      },
      startedActivity: !state.running,
    };
  }
  switch (frame.phase) {
    case "start":
      return {
        state: { ...state, source: frame.source, running: true, lastText: frame.text ?? "", seq: state.seq + 1 },
        startedActivity: true,
      };
    case "step":
      // browser_auto action captions — text only, run state untouched.
      return {
        state: { ...state, source: frame.source, lastText: frame.text ?? "", seq: state.seq + 1 },
        startedActivity: false,
      };
    case "end":
      return {
        state: { ...state, running: false, lastText: frame.text || state.lastText, seq: state.seq + 1 },
        startedActivity: false,
      };
    default:
      return { state, startedActivity: false };
  }
}

// --- live module store --------------------------------------------------------

let browserMirrorState: BrowserMirrorState = initialBrowserMirrorState;
const browserMirrorListeners = new Set<() => void>();

export function browserMirrorSnapshot(): BrowserMirrorState {
  return browserMirrorState;
}

export function subscribeBrowserMirror(fn: () => void): () => void {
  browserMirrorListeners.add(fn);
  return () => {
    browserMirrorListeners.delete(fn);
  };
}

// pushBrowserMirrorFrame applies a frame and notifies subscribers; returns
// whether this frame marked new browsing activity (see applyBrowserMirrorFrame).
export function pushBrowserMirrorFrame(frame: BrowserMirrorFrame): boolean {
  const result = applyBrowserMirrorFrame(browserMirrorState, frame);
  browserMirrorState = result.state;
  for (const listener of browserMirrorListeners) listener();
  return result.startedActivity;
}

// --- focus requests ------------------------------------------------------------
// App auto-opens the cowork dock, but the dock's active tab is internal to
// CoworkDock — a focus request lets it switch to the mirror tab itself.

const browserMirrorFocusListeners = new Set<() => void>();

export function requestBrowserMirrorFocus(): void {
  for (const listener of browserMirrorFocusListeners) listener();
}

export function subscribeBrowserMirrorFocus(fn: () => void): () => void {
  browserMirrorFocusListeners.add(fn);
  return () => {
    browserMirrorFocusListeners.delete(fn);
  };
}
