package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func docWriteExec(t *testing.T, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return (docWrite{}).Execute(context.Background(), raw)
}

// P1-F7 regression: docx with zero sections used to write a structurally-valid
// EMPTY document and report success — the model had put body text into
// `content`, which docx ignores. It must refuse, and when content is non-empty
// the error must say where the text actually went.
func TestDocWriteRejectsEmptyDocxSections(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.docx")

	_, err := docWriteExec(t, map[string]any{
		"path": p, "content": "This body text does not belong in content for docx.",
	})
	if err == nil {
		t.Fatal("empty-sections docx with content should be rejected, got success")
	}
	if !strings.Contains(err.Error(), "sections") {
		t.Errorf("error should point at the sections field, got: %v", err)
	}
	if _, statErr := os.Stat(p); statErr == nil {
		t.Error("no file should be written on rejection")
	}

	// Zero sections and zero content is still a blank document — reject too.
	if _, err := docWriteExec(t, map[string]any{"path": p}); err == nil {
		t.Error("empty-sections docx without content should also be rejected")
	}
}

// P1-F7 regression: xlsx + append=true used to fall through this branch and
// silently OVERWRITE the existing workbook (the csv-style "append not
// supported" notice is emitted after the switch, which xlsx returns before).
// It must refuse while the previous file exists; a fresh create is fine.
func TestDocWriteRejectsXlsxAppendOverwrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ledger.xlsx")

	rows := [][]string{{"month", "amount"}, {"Jan", "100"}}
	raw, _ := json.Marshal(rows)
	if _, err := docWriteExec(t, map[string]any{"path": p, "content": json.RawMessage(raw)}); err != nil {
		t.Fatalf("initial xlsx write failed: %v", err)
	}
	before, _ := os.ReadFile(p)

	if _, err := docWriteExec(t, map[string]any{"path": p, "append": true, "content": json.RawMessage(raw)}); err == nil {
		t.Fatal("xlsx append over an existing file should be rejected, got success")
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) {
		t.Error("refused append must leave the existing workbook untouched")
	}

	// append=true with NO existing file is the normal first-chapter case.
	p2 := filepath.Join(dir, "fresh.xlsx")
	if _, err := docWriteExec(t, map[string]any{"path": p2, "append": true, "content": json.RawMessage(raw)}); err != nil {
		t.Fatalf("xlsx append creating a fresh file should succeed: %v", err)
	}
}

// P1-F7 wiring: the 32767-char per-cell guard existed but was never called.
// An oversized cell must be rejected before a workbook Excel refuses to open
// is produced.
func TestXlsxWriteRejectsOversizedCell(t *testing.T) {
	big := strings.Repeat("x", 32768)
	err := XLSXWriteRows(filepath.Join(t.TempDir(), "a.xlsx"), [][]string{{big}})
	if err == nil {
		t.Fatal("oversized cell should be rejected")
	}
	if !strings.Contains(err.Error(), "cell_overflow") {
		t.Errorf("want cell_overflow DocError, got: %v", err)
	}

	v := big
	_, err2 := XLSXWriteStructured(XLSXWorkbook{
		Path:   filepath.Join(t.TempDir(), "b.xlsx"),
		Sheets: []XLSXSheet{{Name: "S", Cells: []XLSXCell{{Ref: "A1", Value: &v}}}},
	})
	if err2 == nil {
		t.Fatal("structured writer must reject oversized cells too")
	}
}
