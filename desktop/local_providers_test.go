package main

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

// Keyless local providers (built-in ollama/llamacpp presets) must be fully
// selectable once the user adds them — no API-key gate — while keyed providers
// without their env keep being rejected.

func TestSelectableDesktopModelRefAllowsKeylessProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = append(cfg.Providers, config.BuiltinLocalProviders()...)
	cfg.Desktop.ProviderAccess = []string{"ollama"}

	ref, err := selectableDesktopModelRef(cfg, "ollama/qwen3-coder:30b")
	if err != nil {
		t.Fatalf("keyless provider selection failed: %v", err)
	}
	if ref != "ollama/qwen3-coder:30b" {
		t.Fatalf("ref = %q, want ollama/qwen3-coder:30b", ref)
	}

	cfg.Providers = append(cfg.Providers, config.ProviderEntry{
		Name: "keyed", Kind: "openai", BaseURL: "https://x.example.com/v1",
		Models: []string{"m"}, APIKeyEnv: "FAIRPEER_TEST_MISSING_KEY",
	})
	cfg.Desktop.ProviderAccess = append(cfg.Desktop.ProviderAccess, "keyed")
	_, err = selectableDesktopModelRef(cfg, "keyed/m")
	if err == nil || !strings.Contains(err.Error(), "no key") {
		t.Fatalf("keyed provider without env must be rejected, got %v", err)
	}
}

func TestProviderViewFlagsKeylessProviderReady(t *testing.T) {
	keyless := config.ProviderEntry{Name: "llamacpp", APIKeyEnv: "", Models: []string{"local-model"}}
	if !providerViewFromEntry(keyless, false, true).KeySet {
		t.Error("keyless provider must report KeySet (no key needed), or pickers filter it out")
	}

	keyed := config.ProviderEntry{Name: "x", APIKeyEnv: "FAIRPEER_TEST_MISSING_KEY", Models: []string{"m"}}
	if providerViewFromEntry(keyed, false, true).KeySet {
		t.Error("provider with unresolved key must not report KeySet")
	}
}

// NeedsOnboarding must stay true for a fresh install even though the keyless
// local presets exist, and turn off once the user adds one of them.
func TestNeedsOnboardingIgnoresBuiltinLocalPresets(t *testing.T) {
	isolateDesktopUserDirs(t)
	t.Setenv("FAIRPEER_API_KEY", "")

	cfg := config.Default()
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if !NewApp().NeedsOnboarding() {
		t.Fatal("fresh install with only built-in local presets must still onboard")
	}

	cfg.Desktop.ProviderAccess = []string{"ollama"}
	if err := cfg.SaveTo(config.UserConfigPath()); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if NewApp().NeedsOnboarding() {
		t.Fatal("user-added keyless local provider must finish onboarding")
	}
}
