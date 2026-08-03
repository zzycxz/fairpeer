package openai

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/provider"
)

// newClient builds a client with the given effort and optional reasoning
// protocol. Thinking mode is now opt-in: the test-provider thinking branch is taken only
// when reasoningProtocol == "test-provider" (no URL/model auto-detection), so tests
// exercise it by declaring the protocol explicitly.
func newClient(t *testing.T, baseURL, reasoningProtocol, effort string) *client {
	t.Helper()
	extra := map[string]any{}
	if effort != "" {
		extra["effort"] = effort
	}
	if reasoningProtocol != "" {
		extra["reasoning_protocol"] = reasoningProtocol
	}
	p, err := New(provider.Config{Name: "p", BaseURL: baseURL, Model: "m", APIKey: "k", Extra: extra})
	if err != nil {
		t.Fatalf("New(%q, protocol=%q, effort=%q): %v", baseURL, reasoningProtocol, effort, err)
	}
	return p.(*client)
}

func TestEffortNormalization(t *testing.T) {
	const base = "https://example.com"

	// The wire layer only accepts the unified low/medium/high vocabulary and
	// "auto" (== ""). Legacy values (max/xhigh/adaptive/disabled/off) are no
	// longer migrated at the provider layer; they are rejected so the
	// /effort command's NormalizeEffort is the single migration point.
	// reasoning_protocol values other than openai/none normalize to "", so a
	// stray "test-provider" behaves exactly like the default OpenAI path.
	tests := []struct {
		protocol, effort, want string
	}{
		// Standard OpenAI-compatible scale (no reasoning protocol declared).
		{"", "high", "high"},
		{"", "medium", "medium"},
		{"", "low", "low"},
		{"", "HIGH", "high"}, // case-insensitive
		{"", "auto", ""},     // auto means omit provider-specific effort
		{"", "", ""},         // unset stays omitted
		// An explicit "openai" reasoning protocol is the same path.
		{"openai", "high", "high"},
		{"openai", "medium", "medium"},
		{"openai", "low", "low"},
		{"openai", "auto", ""},
		{"openai", "", ""},
	}
	for _, tc := range tests {
		if got := newClient(t, base, tc.protocol, tc.effort).effort; got != tc.want {
			t.Errorf("protocol=%q effort=%q: got %q, want %q", tc.protocol, tc.effort, got, tc.want)
		}
	}

	// Legacy / unknown levels are rejected with the standard validation error.
	for _, bad := range []string{"max", "xhigh", "adaptive", "disabled", "off", "turbo"} {
		extra := map[string]any{"effort": bad}
		_, err := New(provider.Config{Name: "p", BaseURL: base, Model: "m", APIKey: "k", Extra: extra})
		if err == nil {
			t.Errorf("effort=%q should be rejected", bad)
		}
	}
}

func TestEffortInvalidRejected(t *testing.T) {
	_, err := New(provider.Config{
		Name: "p", BaseURL: "https://example.com", Model: "m", APIKey: "k",
		Extra: map[string]any{"effort": "turbo"},
	})
	if err == nil || !strings.Contains(err.Error(), "low, medium, or high") {
		t.Fatalf("expected a low/medium/high validation error, got: %v", err)
	}
}

func TestReasoningProtocolExplicit(t *testing.T) {
	// reasoning_protocol = "none" disables thinking even when an effort is set.
	p, err := New(provider.Config{
		Name:    "none-protocol",
		BaseURL: "https://example.com",
		Model:   "test-model-a",
		APIKey:  "k",
		Extra:   map[string]any{"reasoning_protocol": "none", "effort": "max"},
	})
	if err != nil {
		t.Fatalf("New none protocol: %v", err)
	}
	c := p.(*client)
	if c.effort != "" {
		t.Fatalf("effort=%q, want empty for none protocol", c.effort)
	}
}
