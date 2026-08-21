// Profile-scoped localStorage: the three workspaces (dev/cowork/netdev) are
// independent surfaces, so their persisted UI state must not leak across
// modes. Keys are suffixed per named profile (`key::cowork`, `key::netdev`);
// dev keeps the LEGACY unsuffixed key so existing installs migrate for free
// and the default mode is byte-identical to before.
//
// App registers the active tab's profile here (setActiveStorageProfile) from
// its profile-sync effects; storage helpers below pick it up without prop
// drilling through non-React modules (composerHistory, ProjectTree, …).

export type StorageProfile = "dev" | "cowork" | "netdev";

export function normalizeStorageProfile(profile: string | undefined | null): StorageProfile {
  const p = (profile ?? "").trim().toLowerCase();
  return p === "cowork" || p === "netdev" ? p : "dev";
}

let activeProfile: StorageProfile = "dev";

// setActiveStorageProfile is called by App whenever the active tab's profile
// changes (tabMetas sync, profile:changed event, optimistic switch flip).
export function setActiveStorageProfile(profile: string | undefined | null): void {
  activeProfile = normalizeStorageProfile(profile);
}

export function activeStorageProfile(): StorageProfile {
  return activeProfile;
}

export function scopedStorageKey(key: string, profile: StorageProfile = activeProfile): string {
  return profile === "dev" ? key : `${key}::${profile}`;
}

// getScopedItem reads the active profile's bucket. fallbackToDev lets a fresh
// named-profile bucket inherit dev's value as its initial default (layout
// sizes); content keys (composer history, topic-seen, preview URL) pass false
// so one mode never surfaces another mode's data.
export function getScopedItem(key: string, fallbackToDev = false): string | null {
  if (typeof window === "undefined") return null;
  try {
    const v = window.localStorage.getItem(scopedStorageKey(key));
    if (v !== null) return v;
    if (fallbackToDev && activeProfile !== "dev") return window.localStorage.getItem(key);
  } catch {
    /* storage unavailable */
  }
  return null;
}

export function setScopedItem(key: string, value: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(scopedStorageKey(key), value);
  } catch {
    /* storage unavailable */
  }
}

export function removeScopedItem(key: string): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.removeItem(scopedStorageKey(key));
  } catch {
    /* storage unavailable */
  }
}
