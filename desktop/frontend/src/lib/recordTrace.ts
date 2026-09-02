// recordTrace — display helpers for the browser console's recording stream:
// one-line "verb + element label" summaries (DevTools-Recorder row anatomy)
// for both the live feed and the filtered-disclosure rows.
import type { BrowserConsoleRecordEvent } from "./types";

export function summarizeRecordEvent(ev: BrowserConsoleRecordEvent): string {
  const label = ev.name || ev.selector || "";
  switch (ev.type) {
    case "click":
      return `点击 ${label}`;
    case "input":
      return ev.password ? `输入 ******（${label}）` : `输入 ${truncate(ev.value ?? "", 24)}（${label}）`;
    case "change":
      return `选择 ${truncate(ev.value ?? "", 24)}（${label}）`;
    case "submit":
      return `回车（${label}）`;
    case "navigate":
      return `打开 ${truncate(ev.url ?? "", 48)}`;
    case "scroll":
      return `滚动 ${ev.value || "down"}（${label || "页面"}）`;
    default:
      return label || ev.type;
  }
}

function truncate(s: string, n: number): string {
  return s.length > n ? `${s.slice(0, n)}…` : s;
}
