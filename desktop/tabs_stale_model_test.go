package main

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

// The regression that bricked the user's startup: desktop-tabs.json carried
// "mimo/mimo-v2.5-pro" from a provider that no longer exists, and the tab
// restore surfaced it as 启动错误 instead of falling back. The fix lives in
// two layers (tabs.go pre-resolution + boot.Build's internal fallback); this
// test drives the REAL buildTabController end to end and asserts both the
// error is gone and the tab lands on a working model.
func TestStaleTabModelFallsBackInsteadOfStartupError(t *testing.T) {
	isolateDesktopUserDirs(t)

	tab := &WorkspaceTab{
		ID:            "tab_stale",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		Ready:         false,
		model:         "mimo/mimo-v2.5-pro", // provider since removed
		disabledMCP:   map[string]ServerView{},
	}
	app := &App{
		tabs:        map[string]*WorkspaceTab{"tab_stale": tab},
		tabOrder:    []string{"tab_stale"},
		activeTabID: "tab_stale",
	}
	app.buildTabController(tab)
	if tab.Ctrl == nil {
		defer func() {}()
	} else {
		defer tab.Ctrl.Close()
	}

	if tab.StartupErr != "" {
		t.Fatalf("stale model must not become a startup error, got: %s", tab.StartupErr)
	}
	if tab.Ctrl == nil {
		t.Fatal("controller not built")
	}
	// The tab must have been re-pointed at a resolvable model (the first
	// usable provider — with no [[providers]] and no keys, the keyless local
	// preset chain), not left pointing at the dead reference.
	cfg, err := config.LoadForRoot(globalTabWorkspaceRoot())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.ResolveModel(tab.model); !ok {
		t.Fatalf("tab.model %q is still unresolvable", tab.model)
	}
	if !strings.Contains(tab.model, "/") {
		t.Fatalf("tab.model %q lost its provider prefix", tab.model)
	}
}
