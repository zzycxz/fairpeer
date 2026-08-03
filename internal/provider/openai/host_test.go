package openai

import "testing"

// TestIsMiniMax pins the host-matching rule for MiniMax. The spelling is
// `minimaxi`, not `minimax` — the latter is reserved for any future
// minimax-branded gateway so the two never collide.
func TestIsMiniMax(t *testing.T) {
	for _, tc := range []struct {
		baseURL string
		want    bool
	}{
		// Canonical
		{"https://api.minimaxi.com", true},
		{"https://api.minimaxi.com/v1", true},
		{"https://api.minimaxi.com/anthropic", true},
		// Regional subdomains under the apex
		{"https://eu.minimaxi.com/v1", true},
		{"https://us.minimaxi.com/v1", true},
		// Apex rejected
		{"https://minimaxi.com/v1", false},
		{"https://minimaxi.com", false},
		// Other vendors must not match
		{"https://example.com", false},
		{"https://api.openai.com/v1", false},
		// Wrong spelling — minimax, not minimaxi — must not match
		{"https://api.minimax.com/v1", false},
		{"https://api.minimax.example.com", false},
		// Garbage
		{"", false},
		{"not-a-url", false},
	} {
		if got := IsMiniMax(tc.baseURL); got != tc.want {
			t.Errorf("IsMiniMax(%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}
