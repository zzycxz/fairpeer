package main

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

func newStaleModelTab(model string) (*App, *WorkspaceTab) {
	tab := &WorkspaceTab{
		ID:            "tab_stale",
		Scope:         "global",
		WorkspaceRoot: globalTabWorkspaceRoot(),
		Ready:         false,
		model:         model,
		disabledMCP:   map[string]ServerView{},
	}
	app := &App{
		tabs:        map[string]*WorkspaceTab{"tab_stale": tab},
		tabOrder:    []string{"tab_stale"},
		activeTabID: "tab_stale",
	}
	return app, tab
}

// The regression that bricked the user's startup: desktop-tabs.json carried
// "mimo/mimo-v2.5-pro" from a provider that no longer exists, and the tab
// restore surfaced it as 启动错误 instead of falling back. With a usable
// provider around (here: the ollama preset, explicitly added via
// provider_access), the stale ref re-points at it and boots. This drives the
// REAL buildTabController end to end.
func TestStaleTabModelFallsBackInsteadOfStartupError(t *testing.T) {
	isolateDesktopUserDirs(t)

	cfg := config.Default()
	cfg.Desktop.ProviderAccess = []string{"ollama"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatal(err)
	}

	app, tab := newStaleModelTab("mimo/mimo-v2.5-pro")
	app.buildTabController(tab)
	if tab.Ctrl != nil {
		defer tab.Ctrl.Close()
	}

	if tab.StartupErr != "" {
		t.Fatalf("stale model must not become a startup error, got: %s", tab.StartupErr)
	}
	if tab.Ctrl == nil {
		t.Fatal("controller not built")
	}
	// The tab must have been re-pointed at a resolvable model (the first
	// usable provider — here the user-added keyless local preset chain), not
	// left pointing at the dead reference.
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

// The same stale tab with ONLY ambient presets (injected, never added): the
// tab must not silently land on local ollama either. The dead model is dropped
// and the tab parks in the Welcome state — no controller, no startup error —
// so onboarding guides the user to a provider they actually chose. Covers both
// shapes of the captured-tab bug: a ref to a removed provider, and a ref that
// still resolves against the ambient preset itself (every tab became
// "ollama/qwen3-coder:30b" though the user never chose ollama).
func TestStaleTabModelWithOnlyAmbientPresetsParksInWelcome(t *testing.T) {
	isolateDesktopUserDirs(t)

	for _, model := range []string{"mimo/mimo-v2.5-pro", "ollama/qwen3-coder:30b"} {
		app, tab := newStaleModelTab(model)
		app.buildTabController(tab)

		if tab.StartupErr != "" {
			t.Fatalf("model %q: dropped model must not become a startup error, got: %s", model, tab.StartupErr)
		}
		if tab.Ctrl != nil {
			tab.Ctrl.Close()
			t.Fatalf("model %q: ambient presets must not capture the tab (controller was built)", model)
		}
		if tab.model != "" {
			t.Fatalf("model %q: tab.model = %q, want it dropped to the Welcome state", model, tab.model)
		}
		if !tab.Ready {
			t.Fatalf("model %q: tab must be Ready after parking in the Welcome state", model)
		}
	}
}
