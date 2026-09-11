package jobs

import (
	"os"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/spill"
)

// P1-B1: once the cap engages, everything written afterwards lands in a spill
// file and the truncation marker carries its path — the discarded middle is
// retrievable instead of gone.
func TestCappedBufferSpillsOverflow(t *testing.T) {
	spill.SetBase(t.TempDir())
	t.Cleanup(func() { spill.SetBase("") })

	b := newCappedBuffer(1 << 10) // 1KiB cap: 512B head + 512B tail
	b.EnableSpill("test-job")
	middle := strings.Repeat("M", 4096)
	b.Write([]byte(strings.Repeat("H", 500)))  // head fills to its half-cap
	b.Write([]byte(middle))                    // overflow middle
	b.Write([]byte(strings.Repeat("T", 100)))  // recent tail
	b.closeSpill()

	out := b.String()
	if !strings.Contains(out, "truncated") || !strings.Contains(out, "read_file") {
		t.Fatalf("marker missing spill path:\n%s", out[:200])
	}
	idx := strings.Index(out, "saved to ")
	if idx < 0 {
		t.Fatal("no spill path in marker")
	}
	path := out[idx+len("saved to "):]
	if end := strings.Index(path, ";"); end >= 0 {
		path = path[:end]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spill file unreadable (%v) — path was %q", err, path)
	}
	if !strings.Contains(string(data), "MMMM") {
		t.Error("spill file does not contain the overflow middle")
	}
	if !strings.Contains(string(data), strings.Repeat("T", 100)) {
		t.Error("spill file should carry post-overflow bytes too")
	}
	if strings.Contains(string(data), strings.Repeat("H", 500)) {
		t.Error("pre-overflow head bytes belong to the buffer, not the spill")
	}
}
