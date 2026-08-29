package config

import (
	"strings"
	"testing"
)

// Reserved plugin names: the IM bot gateway channels and built-in runtimes may
// not be used as NEW MCP server names — otherwise the Bots tab and the MCP tab
// both show a bare "feishu" for two unrelated things (the 2026-08-21 同名异物
// ambiguity fix). Legacy pre-guard entries may still be replaced in place.
func TestUpsertPluginRejectsReservedBotChannelNames(t *testing.T) {
	for _, name := range []string{"feishu", "FEISHU", " lark ", "qq", "weixin", "telegram", "codegraph", "context7"} {
		c := &Config{}
		err := c.UpsertPlugin(PluginEntry{Name: strings.TrimSpace(name), Type: "stdio", Command: "x"})
		if err == nil {
			t.Fatalf("UpsertPlugin(%q) accepted a reserved name, want rejection", name)
		}
		if !strings.Contains(err.Error(), "reserved") || !strings.Contains(err.Error(), "e.g. feishu-docs") {
			t.Fatalf("error should be actionable (reserved + rename hint), got: %v", err)
		}
	}
}

// A legacy entry that predates the guard stays editable (replace-in-place) so
// existing setups are not bricked by the guard.
func TestUpsertPluginAllowsReplacingLegacyReservedEntry(t *testing.T) {
	c := &Config{Plugins: []PluginEntry{{Name: "feishu", Type: "stdio", Command: "old"}}}
	if err := c.UpsertPlugin(PluginEntry{Name: "feishu", Type: "stdio", Command: "new"}); err != nil {
		t.Fatalf("replacing a legacy reserved entry failed: %v", err)
	}
	if c.Plugins[0].Command != "new" {
		t.Fatalf("legacy entry not replaced: %+v", c.Plugins[0])
	}
}

func TestIsReservedPluginName(t *testing.T) {
	if reason, ok := IsReservedPluginName("Feishu"); !ok || !strings.Contains(reason, "bot") {
		t.Fatalf("Feishu should be reserved with a bot reason, got %q ok=%v", reason, ok)
	}
	if _, ok := IsReservedPluginName("feishu-docs"); ok {
		t.Fatalf("feishu-docs must be installable")
	}
}
