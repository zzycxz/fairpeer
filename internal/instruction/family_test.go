package instruction

import (
	"strings"
	"testing"
)

func TestModelFamily(t *testing.T) {
	// Family detection matches on real vendor prefixes/names. "test-provider"
	// is not a real vendor, so it resolves to "".
	tests := []struct {
		model string
		want  string
	}{
		{"qwen/qwen3-max", "qwen"},
		{"test-provider/qwen3-max", "qwen"},
		{"deepseek/deepseek-v4-flash", "deepseek"},
		{"z.ai/glm-5.1", "glm"},
		{"test-provider/z.ai/glm-5.2", "glm"},
		{"moonshotai/kimi-k2.6", "kimi"},
		{"minimax/minimax-m2.7", "minimax"},
		{"test-provider/test-model-a", ""},
		{"openai/gpt-oss-120b", "gpt"},
		{"unknown-model", ""},
		{"", ""},

		// Token-boundary precision: substrings inside a larger token must NOT
		// classify (the old Contains matcher hit these).
		{"glmw-v2", ""},            // "glmw" is one token, not glm
		{"myqwenite", ""},          // "myqwenite" is one token, not qwen
		{"mintmax-pro", ""},        // not minimax
		{"gptwrapper-llama", ""},    // "gptwrapper" is one token, not gpt

		// Fused version-digit tokens classify ("qwen3", "glm4").
		{"test-provider/qwen3-max", "qwen"},
		{"glm4-air", "glm"},
		{"gpt4o-mini", "gpt"},

		// Claude / anthropic family (recognised, no addon — see TestFamilyAddon).
		{"anthropic/claude-sonnet-4-5", "anthropic"},
		{"test-provider/claude-haiku-4.5", "anthropic"},

		// Vendor prefix beats token order.
		{"openai/qwen-turbo", "gpt"},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			if got := ModelFamily(tc.model); got != tc.want {
				t.Errorf("ModelFamily(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

func TestFamilyAddon(t *testing.T) {
	// Known families return non-empty addons.
	for _, family := range []string{"qwen", "glm", "deepseek", "kimi"} {
		if FamilyAddon(family) == "" {
			t.Errorf("FamilyAddon(%q) is empty", family)
		}
	}
	// Unknown families (including the synthetic "test-provider") return empty.
	for _, family := range []string{"unknown", "test-provider"} {
		if FamilyAddon(family) != "" {
			t.Errorf("FamilyAddon(%q) is non-empty", family)
		}
	}
}

func TestForModelIncludesFamilyAddon(t *testing.T) {
	// Non-thinking qwen model should get the qwen addon.
	got := ForModel("qwen/qwen3-max")
	if !strings.Contains(got, "tool call") {
		t.Errorf("ForModel(qwen model) should include qwen addon about tool calls, got: %q", got)
	}

	// GLM should get serial tool guidance.
	got = ForModel("z.ai/glm-5.2")
	if !strings.Contains(got, "one tool per message") && !strings.Contains(got, "sequential") {
		t.Errorf("ForModel(glm model) should include GLM addon, got: %q", got)
	}

	// Unknown model returns empty.
	if ForModel("totally-unknown") != "" {
		t.Errorf("ForModel(unknown) should be empty")
	}
}
