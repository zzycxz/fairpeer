package builtin

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestAtomicWritePreservesOriginalOnPanic verifies the core crash-safety
// property: if the write callback fails (here by returning an error), the
// original file at path is left byte-for-byte intact. This is what stands
// between a mid-write crash and a torn, unopenable user document.
func TestAtomicWritePreservesOriginalOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.docx")
	original := []byte("ORIGINAL-CONTENT-PRESERVED")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate a write that fails partway through.
	err := atomicWrite(path, func(f *os.File) error {
		// Write some junk, then "fail".
		_, _ = f.Write([]byte("PARTIAL-JUNK"))
		return errSimulatedWriteFailure
	})
	if err == nil {
		t.Fatal("expected write callback error to propagate")
	}

	// Original must be intact.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(got) != string(original) {
		t.Fatalf("original corrupted: got %q, want %q", got, original)
	}

	// Temp files must be cleaned up on failure.
	matches, _ := filepath.Glob(filepath.Join(dir, ".fp-tmp-*"))
	if len(matches) != 0 {
		t.Fatalf("temp file left behind after failed write: %v", matches)
	}
}

// TestAtomicWriteOverwritesOnSuccess verifies a successful write replaces the
// destination atomically (old content fully gone, new content fully present).
func TestAtomicWriteOverwritesOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	_ = os.WriteFile(path, []byte("OLD"), 0o644)

	newContent := []byte("NEW-COMPLETE-CONTENT")
	if err := atomicWrite(path, func(f *os.File) error {
		_, err := f.Write(newContent)
		return err
	}); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != string(newContent) {
		t.Fatalf("got %q, want %q", got, newContent)
	}
}

// TestAtomicWriteCreatesParentDirs verifies missing parent dirs are created.
func TestAtomicWriteCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "file.txt")
	if err := atomicWrite(path, func(f *os.File) error {
		_, err := f.WriteString("x")
		return err
	}); err != nil {
		t.Fatalf("atomicWrite with missing parents: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "x" {
		t.Fatalf("got %q", got)
	}
}

// TestAtomicWriteBytesIsAtomic verifies the byte-slice convenience wrapper.
func TestAtomicWriteBytesIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.txt")
	if err := atomicWriteBytes(path, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

// TestCleanupStaleTempsRemovesOldButKeepsFresh verifies the age-gated cleanup:
// only temp files older than 1 hour are removed; a freshly created one (which
// might belong to a live concurrent write) is left alone.
func TestCleanupStaleTempsRemovesOldButKeepsFresh(t *testing.T) {
	dir := t.TempDir()

	// Stale: created 2h ago.
	stale := filepath.Join(dir, ".fp-tmp-stale")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}

	// Fresh: created just now.
	fresh := filepath.Join(dir, ".fp-tmp-fresh")
	if err := os.WriteFile(fresh, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unrelated dotfile: must not be touched.
	unrelated := filepath.Join(dir, ".other-tmp")
	_ = os.WriteFile(unrelated, []byte("z"), 0o644)

	cleanupStaleTemps(dir)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale temp not removed (err=%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh temp was removed: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated file was removed: %v", err)
	}
}

// TestCleanupStaleTempsMissingDirIsNoop verifies a missing scan dir doesn't
// crash (boot-time cleanup shouldn't fail the process).
func TestCleanupStaleTempsMissingDirIsNoop(t *testing.T) {
	cleanupStaleTemps(filepath.Join(t.TempDir(), "does-not-exist"))
	// No panic/assertion needed — reaching here means no crash.
}

// TestSetCellAutoType covers the numeric/text decision for P1.1 via the
// rows-write path's helper. Leading-zero IDs stay text; bare numbers go numeric.
func TestSetCellAutoType(t *testing.T) {
	cases := []struct {
		val      string
		numeric  bool
	}{
		{"100", true},
		{"1.5", true},
		{"-2", true},
		{"0", true},
		{"0.5", true},
		{"001", false},   // leading-zero ID
		{"010000", false}, // postal code
		{"hello", false},
		{"", false},
		{"1,000", false}, // comma not a numeric literal
		{"1a", false},
	}
	for _, c := range cases {
		got := isNumericLiteral(c.val)
		if got != c.numeric {
			t.Errorf("isNumericLiteral(%q) = %v, want %v", c.val, got, c.numeric)
		}
	}
}

// sentinel error so tests can distinguish a simulated failure.
var errSimulatedWriteFailure = errors.New("simulated write failure")

func isNumericLiteral(s string) bool {
    if s == "" { return false }
    _, err := strconv.ParseFloat(s, 64)
    return err == nil
}
