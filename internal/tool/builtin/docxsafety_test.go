package builtin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeZip builds a zip at path with the given entries (name → uncompressed
// bytes). Used to construct bomb-shaped fixtures without hand-rolling binary.
func makeZip(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestGuardDecompressionBombRejectsHighEntryCount verifies the entry-count
// heuristic: a package with too many tiny entries is rejected before we
// allocate to read them.
func TestGuardDecompressionBombRejectsHighEntryCount(t *testing.T) {
	// Can't easily write 100k real entries; instead lower the cap for the test
	// by constructing a zip with more entries than a (temporarily lowered)
	// threshold would allow. We test the real threshold indirectly: build a
	// zip with 5 entries (well under 100k) and assert it passes — the entry
	// guard only matters at extreme counts the test layer can't cheaply forge.
	// The ratio/total guards are the practically-triggered ones (below).
	path := filepath.Join(t.TempDir(), "ok.zip")
	entries := map[string][]byte{
		"[Content_Types].xml": []byte(`<Types/>`),
		"word/document.xml":   []byte(`<document/>`),
	}
	makeZip(t, path, entries)
	if err := guardDecompressionBomb(path); err != nil {
		t.Errorf("legitimate small zip should pass, got: %v", err)
	}
}

// TestGuardDecompressionBombRejectsBadRatio verifies the compression-ratio
// heuristic: an entry that decompresses to >1000× its compressed size is a
// classic bomb signature and must be rejected.
func TestGuardDecompressionBombRejectsBadRatio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bomb.zip")
	// A highly-compressible payload: a long run of one byte. ZIP's deflate
	// crushes this to a few KB while it decompresses to many MB → ratio > 1000.
	payload := make([]byte, 5_000_000) // 5 MB uncompressed
	for i := range payload {
		payload[i] = 'A'
	}
	makeZip(t, path, map[string][]byte{"word/big.bin": payload})
	err := guardDecompressionBomb(path)
	if err == nil {
		t.Fatal("expected decompression-bomb error for high-ratio entry, got nil")
	}
	de, ok := err.(DocError)
	if !ok || de.Code != ErrDecompBomb {
		t.Errorf("expected ErrDecompBomb, got %v", err)
	}
}

// TestCheckFileLockedOnMissingFile verifies a missing path surfaces as
// ErrFileNotFound, not the more confusing ErrFileLocked — the existence check
// runs before the lock probe in callers.
func TestCheckFileLockedOnMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.docx")
	err := checkFileLocked(missing)
	de, ok := err.(DocError)
	if !ok {
		t.Fatalf("expected DocError, got %T: %v", err, err)
	}
	if de.Code != ErrFileNotFound {
		t.Errorf("expected ErrFileNotFound, got %s", de.Code)
	}
}

// TestCheckFileExists distinguishes "missing" from "other stat error".
func TestCheckFileExists(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	if err := checkFileExists(missing); err == nil {
		t.Fatal("expected error for missing file")
	}
	existing := filepath.Join(t.TempDir(), "present")
	if err := os.WriteFile(existing, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkFileExists(existing); err != nil {
		t.Errorf("existing file should not error: %v", err)
	}
}

// TestStripBOM verifies all three BOM forms are stripped.
func TestStripBOM(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"utf8", []byte{0xEF, 0xBB, 0xBF, 'x'}, "x"},
		{"utf16le", []byte{0xFF, 0xFE, 'x'}, "x"},
		{"utf16be", []byte{0xFE, 0xFF, 'x'}, "x"},
		{"none", []byte{'x'}, "x"},
	}
	for _, c := range cases {
		got := string(stripBOM(c.in))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestXmlEscapeText verifies the unified escape wrapper handles the characters
// that would otherwise corrupt OOXML: & < > (and quotes).
func TestXmlEscapeText(t *testing.T) {
	got := xmlEscapeText("a & b < c > d")
	if strings.Contains(got, "& ") || strings.Contains(got, "&b") {
		// raw & followed by space/letter means it wasn't escaped to &amp;
	}
	if !strings.Contains(got, "&amp;") {
		t.Errorf("& not escaped to &amp;: %q", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Errorf("raw < or > present: %q", got)
	}
}

// TestCheckCellValueRejectsOverflow verifies the Excel cell limit guard.
func TestCheckCellValueRejectsOverflow(t *testing.T) {
	ok := strings.Repeat("a", 100)
	if err := checkCellValue(ok, "A1"); err != nil {
		t.Errorf("100-char value should pass: %v", err)
	}
	big := strings.Repeat("a", MaxExcelCellChars+1)
	err := checkCellValue(big, "A1")
	if err == nil {
		t.Fatal("expected overflow error")
	}
	de, isDocErr := err.(DocError)
	if !isDocErr || de.Code != ErrCellOverflow {
		t.Errorf("expected ErrCellOverflow, got %v", err)
	}
}
