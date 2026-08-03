package openai

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/provider"
)

// newClient builds a client with the given effort and optional reasoning
// protocol. Thinking mode is now opt-in: the MoMA thinking branch is taken only
// when reasoningProtocol == "moma" (no URL/model auto-detection), so tests
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
		{"", "max", "high"},  // max is a MoMA-ism; clamp to the OpenAI ceiling
		{"", "high", "high"}, // pass through
		{"", "medium", "medium"},
		{"", "low", "low"},
		{"", "MAX", "high"}, // case-insensitive
		{"", "auto", ""},    // auto means omit provider-specific effort
		{"", "", ""},        // unset stays omitted
		// Explicit reasoning_protocol = "moma" takes the MoMA thinking branch.
		{"moma", "max", "high"},  // max rejected by most MoMA models; clamp to high
		{"moma", "high", "high"}, // pass through
		{"moma", "medium", "medium"},
		{"moma", "low", "medium"}, // low rejected by some MoMA models; clamp to medium
		{"moma", "auto", "high"},  // auto → default depth
		{"moma", "", "high"},      // unset → default MoMA depth
		{"moma", "off", "high"},   // retired level → default depth
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
	// An explicit reasoning_protocol = "moma" enables thinking mode regardless
	// of the base URL — there is no longer any URL-based auto-detection.
	p, err := New(provider.Config{
		Name:    "momaproxy",
		BaseURL: "https://proxy.example.com/v1",
		Model:   "qwen3.6-35b",
		APIKey:  "k",
		Extra:   map[string]any{"reasoning_protocol": "MoMA"},
	})
	if err != nil {
		t.Fatalf("New MoMA protocol: %v", err)
	}
	c := p.(*client)
	if !c.moma || c.effort != "high" {
		t.Fatalf("moma=%v effort=%q, want true/high", c.moma, c.effort)
	}

	// reasoning_protocol = "none" disables thinking even when an effort is set.
	p, err = New(provider.Config{
		Name:    "none-protocol",
		BaseURL: "https://example.com",
		Model:   "qwen3.6-35b",
		APIKey:  "k",
		Extra:   map[string]any{"reasoning_protocol": "none", "effort": "max"},
	})
	if err != nil {
		t.Fatalf("New none protocol: %v", err)
	}
	c = p.(*client)
	if c.moma || c.effort != "" {
		t.Fatalf("moma=%v effort=%q, want false/empty", c.moma, c.effort)
	}
}
