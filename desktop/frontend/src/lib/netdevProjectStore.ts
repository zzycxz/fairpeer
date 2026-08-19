// netdevProjectStore — the active 运维 project (site scope) shared between the
// NetdevTitleBar's switcher and the NetDevLayout's filtered views. The two
// mount separately (chrome center slot vs. layout body), so a tiny
// subscribe-store replaces prop drilling through App.
export type NetDevProjectScope = { name: string; groups: string[] } | null; // null = 全部

let current: NetDevProjectScope = null;
const listeners = new Set<() => void>();

export function getActiveProject(): NetDevProjectScope {
  return current;
}

export function setActiveProject(p: NetDevProjectScope) {
  current = p && p.name ? p : null;
  listeners.forEach(fn => fn());
}

export function subscribeActiveProject(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
