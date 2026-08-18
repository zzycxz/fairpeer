package main

import "testing"

// The embedded snapshot is the offline source of truth for the vendor picker;
// these tests keep new presets and the trackedVendors mapping consistent.

func TestDefaultRegistrySnapshotHasKeylessLocalPresets(t *testing.T) {
	byName := snapshotByName(loadEmbedSnapshot())
	for _, name := range []string{"ollama", "llamacpp"} {
		tpl, ok := byName[name]
		if !ok {
			t.Fatalf("preset %q missing from default_registry.json", name)
		}
		if !tpl.Local {
			t.Errorf("preset %q must set local = true", name)
		}
		if tpl.APIKeyEnv != "" {
			t.Errorf("local preset %q must not declare apiKeyEnv (got %q)", name, tpl.APIKeyEnv)
		}
		if tpl.Category != "local" {
			t.Errorf("local preset %q must use category \"local\" (got %q)", name, tpl.Category)
		}
		if tpl.BaseURL == "" || tpl.DefaultModel == "" || len(tpl.Models) == 0 {
			t.Errorf("local preset %q is incomplete: %+v", name, tpl)
		}
	}
}

func TestDefaultRegistrySnapshotHasXai(t *testing.T) {
	byName := snapshotByName(loadEmbedSnapshot())
	tpl, ok := byName["xai"]
	if !ok {
		t.Fatal("preset \"xai\" missing from default_registry.json")
	}
	if tpl.Kind != "openai" || tpl.BaseURL == "" || tpl.APIKeyEnv == "" || tpl.DefaultModel == "" {
		t.Errorf("xai preset is incomplete: %+v", tpl)
	}
}

// trackedVendors entries only enrich snapshots — a mapping without a matching
// snapshot entry silently does nothing, so keep the two in sync.
func TestTrackedVendorsTargetsExistInSnapshot(t *testing.T) {
	byName := snapshotByName(loadEmbedSnapshot())
	for devID, ourName := range trackedVendors {
		if _, ok := byName[ourName]; !ok {
			t.Errorf("trackedVendors[%q] = %q but no snapshot preset named %q", devID, ourName, ourName)
		}
	}
}

// models.dev lists image/video-generation models next to chat models (xAI's
// grok-imagine-*); the merge must drop them before they reach the wizard.
func TestMergeRemoteDropsNonChatModels(t *testing.T) {
	snap := ProviderTemplate{Name: "xai", DefaultModel: "grok-4.6"}
	vendor := modelsDevVendor{
		API: "https://api.x.ai/v1",
		Models: map[string]modelsDevModel{
			"grok-4.6":               {Limit: modelsDevLimit{Context: 500000}},
			"grok-4.5":               {Limit: modelsDevLimit{Context: 500000}},
			"grok-imagine-video":     {},
			"grok-imagine-image":     {},
			"grok-imagine-image-2.0": {},
		},
	}
	got := mergeRemote(snap, vendor)
	if len(got.Models) != 2 || got.Models[0] != "grok-4.5" || got.Models[1] != "grok-4.6" {
		t.Errorf("mergeRemote models = %v, want [grok-4.5 grok-4.6]", got.Models)
	}
	if got.BaseURL != "https://api.x.ai/v1" {
		t.Errorf("mergeRemote baseURL = %q, want override applied", got.BaseURL)
	}
	if got.ContextWindow != 500000 {
		t.Errorf("mergeRemote contextWindow = %d, want 500000", got.ContextWindow)
	}
}

// When models.dev carries ONLY non-chat models the merged list becomes empty;
// the snapshot's curated list must survive as the offline fallback.
func TestMergeRemoteKeepsSnapshotModelsWhenAllFiltered(t *testing.T) {
	snap := ProviderTemplate{
		Name:         "xai",
		DefaultModel: "grok-4.6",
		Models:       []string{"grok-4.6", "grok-4.5"},
	}
	vendor := modelsDevVendor{
		API:    "https://api.x.ai/v1",
		Models: map[string]modelsDevModel{"grok-imagine-video": {}},
	}
	got := mergeRemote(snap, vendor)
	if len(got.Models) != 2 {
		t.Errorf("mergeRemote models = %v, want snapshot fallback [grok-4.6 grok-4.5]", got.Models)
	}
}
