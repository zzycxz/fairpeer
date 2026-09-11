// useResolvedTheme — React subscription for resolved-theme flips. Mirrors the
// observer MermaidViewer rolls locally: theme.ts's applyTheme writes the
// data-theme attribute on <html> (absent in "auto"), and an OS scheme flip
// while "auto" is active never touches the DOM — so one MutationObserver plus
// a prefers-color-scheme MediaQueryList covers every path. One shared pair
// serves all mounted subscribers (code blocks, diffs, terminals) regardless
// of instance count.

import { useSyncExternalStore } from "react";
import { getResolvedTheme, type ResolvedTheme } from "./theme";

const themeSubs = new Set<() => void>();
let schemeQuery: MediaQueryList | null = null;
let themeAttrObserver: MutationObserver | null = null;
const notifyThemeSubs = () => themeSubs.forEach((fn) => fn());

function subscribeResolvedTheme(onChange: () => void): () => void {
  themeSubs.add(onChange);
  if (themeSubs.size === 1) {
    schemeQuery = window.matchMedia("(prefers-color-scheme: light)");
    schemeQuery.addEventListener("change", notifyThemeSubs);
    themeAttrObserver = new MutationObserver(notifyThemeSubs);
    themeAttrObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  }
  return () => {
    themeSubs.delete(onChange);
    if (themeSubs.size === 0) {
      schemeQuery?.removeEventListener("change", notifyThemeSubs);
      schemeQuery = null;
      themeAttrObserver?.disconnect();
      themeAttrObserver = null;
    }
  };
}

// Returns "light" | "dark" for the current app theme and re-renders the caller
// when it flips (settings change, or an OS scheme change under "auto").
export function useResolvedTheme(): ResolvedTheme {
  return useSyncExternalStore(subscribeResolvedTheme, getResolvedTheme);
}
