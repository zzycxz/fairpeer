package builtin

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// P1-B5: byte-slicing the 200k cap could cut a 3-byte CJK rune in half and
// hand the model invalid UTF-8. The helper must back off to a rune boundary
// and always return valid UTF-8.
func TestTruncateAtMaxRuneSafe(t *testing.T) {
	// CJK chars are 3 bytes; craft content so the cut lands mid-rune.
	cjk := strings.Repeat("汉", 70000) // 210000 bytes
	got := truncateAtMax(cjk, 200_000)
	if !utf8.ValidString(got) {
		t.Fatal("truncated string is not valid UTF-8")
	}
	if !strings.Contains(got, "已截断") {
		t.Error("truncation marker missing")
	}
	// ASCII content truncates exactly at max (minus marker).
	a := strings.Repeat("a", 200_001)
	got2 := truncateAtMax(a, 200_000)
	if !utf8.ValidString(got2) || !strings.Contains(got2, "已截断") {
		t.Error("ascii truncation broken")
	}
	// Under the cap: unchanged, no marker.
	short := "hello"
	if got3 := truncateAtMax(short, 200_000); got3 != short {
		t.Errorf("short content altered: %q", got3)
	}
}
