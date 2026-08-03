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

	tests := []struct {
		protocol, effort, want string
	}{
		// Standard OpenAI-compatible scale (no reasoning protocol declared).
		{"", "max", "high"},  // max is a test-provider-ism; clamp to the OpenAI ceiling
		{"", "high", "high"}, // pass through
		{"", "medium", "medium"},
		{"", "low", "low"},
		{"", "MAX", "high"}, // case-insensitive
		{"", "auto", ""},    // auto means omit provider-specific effort
		{"", "", ""},        // unset stays omitted
		// Explicit reasoning_protocol = "test-provider" takes the test-provider thinking branch.
		{"test-provider", "max", "high"},  // max rejected by most test-provider models; clamp to high
		{"test-provider", "high", "high"}, // pass through
		{"test-provider", "medium", "medium"},
		{"test-provider", "low", "medium"}, // low rejected by some test-provider models; clamp to medium
		{"test-provider", "auto", "high"},  // auto → default depth
		{"test-provider", "", "high"},      // unset → default test-provider depth
		{"test-provider", "off", "high"},   // retired level → default depth
	}
	for _, tc := range tests {
		if got := newClient(t, base, tc.protocol, tc.effort).effort; got != tc.want {
			t.Errorf("protocol=%q effort=%q: got %q, want %q", tc.protocol, tc.effort, got, tc.want)
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
