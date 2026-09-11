package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// B-2 regression: xlsx_read full mode used to silently ignore `sheet` and
// return EVERY sheet (the requested one could fall past the 200k cap).
func TestXLSXReadFullModeHonorsSheet(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "book.xlsx")
	v := "a"
	v2 := "b"
	if _, err := XLSXWriteStructured(XLSXWorkbook{Path: p, Sheets: []XLSXSheet{
		{Name: "Sales", Cells: []XLSXCell{{Ref: "A1", Value: &v}}},
		{Name: "Inventory", Cells: []XLSXCell{{Ref: "A1", Value: &v2}}},
	}}); err != nil {
		t.Fatal(err)
	}

	run := func(args map[string]any) string {
		raw, _ := json.Marshal(args)
		out, err := (xlsxRead{}).Execute(context.Background(), raw)
		if err != nil {
			t.Fatalf("execute: %v", err)
		}
		return out
	}

	// Single-sheet output carries the cells (formatRows prints values; the
	// "--- sheet:" header only appears from the 2nd sheet on) — assert on the
	// VALUES: Inventory's "b" in, Sales's "a" out.
	got := run(map[string]any{"path": p, "mode": "full", "sheet": "Inventory"})
	if strings.Contains(got, "a") {
		t.Errorf("full mode returned the unrequested sheet's cells too:\n%s", got)
	}
	if !strings.Contains(got, "b") {
		t.Errorf("requested sheet's cells missing:\n%s", got)
	}

	// Unknown sheet → actionable error listing what exists.
	raw, _ := json.Marshal(map[string]any{"path": p, "mode": "full", "sheet": "Nope"})
	if _, err := (xlsxRead{}).Execute(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "Sales") {
		t.Errorf("unknown sheet should error with available names, got: %v", err)
	}

	// Omitted sheet keeps the every-sheet behavior (both values + separator).
	all := run(map[string]any{"path": p, "mode": "full"})
	if !strings.Contains(all, "a") || !strings.Contains(all, "b") || !strings.Contains(all, "--- sheet:") {
		t.Errorf("no-sheet full mode should return every sheet:\n%s", all)
	}
}
