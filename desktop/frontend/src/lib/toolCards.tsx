// toolCards is the per-tool card spec registry (upgrade spec 1-1): tools whose
// result deserves better than the generic args/output JSON card register here,
// and ToolCard swaps the registered body in while keeping its shell (status,
// subject, stat, duration, collapse) shared across every tool. A spec may also
// tweak shell behaviour: forceOpen opens the card by default once it settles,
// noQuiet keeps a read-only card from fading after completion.
//
// This is deliberately per-name, not per-profile: dev/cowork/netdev share the
// ToolCard pipeline, so one registration serves all three layouts.
import type { ReactNode } from "react";
import { Markdown } from "../components/Markdown";
import type { Item } from "./useController";

export type ToolItem = Extract<Item, { kind: "tool" }>;

export interface ToolCardSpec {
  // body replaces the card's default args/output body. Return undefined to
  // keep the default for that state (e.g. while running, before output).
  body?: (item: ToolItem) => ReactNode;
  forceOpen?: boolean;
  noQuiet?: boolean;
}

const registry: Record<string, ToolCardSpec> = {
  // Search results read as links and snippets, not as a JSON args dump.
  web_search: {
    body: (item) => (item.output ? <Markdown text={item.output} /> : undefined),
  },
  // Ops evidence stays readable: the command output is the point of the card,
  // so it opens by default and never fades to quiet after completion.
  netdev_exec: { forceOpen: true, noQuiet: true },
  netdev_netconf: { forceOpen: true, noQuiet: true },
  netdev_discover: { forceOpen: true, noQuiet: true },
  netdev_baseline: { forceOpen: true, noQuiet: true },
};

export function toolCardSpec(name: string): ToolCardSpec | undefined {
  return registry[name];
}
