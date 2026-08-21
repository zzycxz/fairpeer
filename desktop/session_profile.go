package main

import (
	"strings"

	"github.com/zzycxz/fairpeer/internal/config"
)

// desktopSessionDirFor returns the session directory for a workspace root
// partitioned by profile. The default profile (empty / "dev") falls back to the
// un-profiled per-project session dir (backward compatible with
// desktopSessionDir). A named profile (e.g. "cowork") routes to
// <userDir>/projects/<slug>/<profileKey>/sessions so conversations stay in their
// own partition. When workspaceRoot is empty, the global profile session dir is
// used instead. Used by tabSessionDir so a tab's pre-build path already lands
// in the right profile partition.
func desktopSessionDirFor(workspaceRoot, profile string) string {
	root := strings.TrimSpace(workspaceRoot)
	key := config.ProfileNameKey(profile)
	if root != "" {
		if dir := config.ProjectSessionDirFor(root, key); dir != "" {
			return dir
		}
	}
	// GLOBAL sessions (empty root) have ONE fixed, profile-partitioned home:
	// <userDir>/sessions for dev, <userDir>/sessions/<key> for named profiles
	// (cowork/netdev). This deliberately bypasses desktopSessionDir — its empty
	// root fallback used to resolve the process CWD, scattering dev globals
	// into CWD-derived project dirs (2026-08-21 fix).
	if dir := config.SessionDirFor(key); dir != "" {
		return dir
	}
	return desktopSessionDir(root)
}

// activeProfileKey returns the profile key of the currently active tab, or the
// default ("dev") when no tab is active or the active tab has no profile. The
// returned value is normalized via config.ProfileNameKey so it is safe to use
// directly as a projects-file path segment.
func (a *App) activeProfileKey() string {
	a.mu.RLock()
	tab := a.activeTabLocked()
	a.mu.RUnlock()
	if tab == nil {
		return config.ProfileDev
	}
	key := config.ProfileNameKey(tab.profile)
	if key == "" {
		return config.ProfileDev
	}
	return key
}

// activeProfileKeyRaw returns the active tab's profile string verbatim (no
// normalization, no defaulting). Used where the caller needs to preserve an
// empty profile to indicate "inherit the default" rather than forcing dev —
// e.g. CreateTopic on a new workspace should match whatever profile the tab is
// actually in, including the un-profiled default.
func (a *App) activeProfileKeyRaw() string {
	a.mu.RLock()
	tab := a.activeTabLocked()
	a.mu.RUnlock()
	if tab == nil {
		return ""
	}
	return strings.TrimSpace(tab.profile)
}

// activeProfileKeyLocked is the lock-free variant of activeProfileKey for use
// when the caller already holds a.mu (Lock or RLock). Calling activeProfileKey()
// from within a held lock causes a deadlock (RLock blocks on held Lock).
func (a *App) activeProfileKeyLocked() string {
	tab := a.activeTabLocked()
	if tab == nil {
		return config.ProfileDev
	}
	key := config.ProfileNameKey(tab.profile)
	if key == "" {
		return config.ProfileDev
	}
	return key
}

// profileDisplayName maps a profile key to its Chinese product name for error
// messages shown to the user; unknown keys pass through as-is.
func profileDisplayName(profile string) string {
	switch config.ProfileNameKey(profile) {
	case config.ProfileCowork:
		return "办公"
	case config.ProfileNetDev:
		return "运维"
	default:
		return "编码"
	}
}
