//go:build windows

package notify

import "testing"

// NEW-39: single-quoted PS literals escape embedded quotes; the encoded
// command path means metacharacters can never break out of the script.
func TestPsQuote(t *testing.T) {
	cases := map[string]string{
		"plain":   "'plain'",
		"it's":    "'it''s'",
		"'; evil": "'''; evil'",
		// Backtick passes verbatim — inside a single-quoted PS string it is
		// inert (PS escape char is backtick only OUTSIDE quotes).
		"a`nb": "'a`nb'",
	}
	for in, want := range cases {
		if got := psQuote(in); got != want {
			t.Errorf("psQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
