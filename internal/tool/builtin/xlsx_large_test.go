package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

// makeLargeXLSX creates a rows×cols xlsx at path with a header row and numeric
// data, so overview/query/page can be exercised on a realistically large file.
// Column A = "id" (1..rows), column B = "val" (id*10), rest = "rNcM" labels.
func makeLargeXLSX(t *testing.T, path string, rows, cols int) {
	t.Helper()
	f := excelize.NewFile()
	sheet := "Sheet1"
	// Header row.
	header := make([]interface{}, cols)
	if cols >= 1 {
		header[0] = "id"
	}
	if cols >= 2 {
		header[1] = "val"
	}
	for c := 2; c < cols; c++ {
		header[c] = fmt.Sprintf("col%d", c)
	}
	cell, _ := excelize.CoordinatesToCellName(1, 1)
	_ = f.SetSheetRow(sheet, cell, &header)
	// Data rows.
	for r := 1; r <= rows; r++ {
		row := make([]interface{}, cols)
		if cols >= 1 {
			row[0] = r
		}
		if cols >= 2 {
			row[1] = r * 10
		}
		for c := 2; c < cols; c++ {
			row[c] = fmt.Sprintf("r%dc%d", r, c)
		}
		cell, _ := excelize.CoordinatesToCellName(1, r+1)
		_ = f.SetSheetRow(sheet, cell, &row)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// TestXLSXReadOverviewLarge verifies overview is FAST on a large file and
// reports the correct shape. This is the core fix for the 30万行 performance
// problem: overview must take seconds, not minutes.
func TestXLSXReadOverviewLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.xlsx")
	// 50000 rows × 5 cols is enough to prove streaming + dimension path; full
	// 300k takes minutes to BUILD the fixture, so we use a smaller-but-still-
	// large file for the unit test. The overview path is O(1) for dimensions
	// regardless of row count.
	makeLargeXLSX(t, path, 50000, 5)

	t0 := time.Now()
	args, _ := json.Marshal(map[string]string{"path": path, "mode": "overview"})
	out, err := xlsxRead{}.Execute(context.Background(), args)
	elapsed := time.Since(t0)
	if err != nil {
		t.Fatal(err)
	}
	// Overview must be fast (well under 30s on a 50k-row file; the old full
	// read took minutes on 300k).
	if elapsed > 30*time.Second {
		t.Errorf("overview too slow: %v (expected seconds)", elapsed)
	}
	t.Logf("overview on 50k-row file took %v", elapsed)
	// Should report the dimensions.
	if !strings.Contains(out, `"rows"`) {
		t.Errorf("overview missing rows; got: %s", firstN(out, 200))
	}
	// Should include the note pointing to page/query.
	if !strings.Contains(out, "xlsx_query") || !strings.Contains(out, "page") {
		t.Errorf("overview should point to page/query; got: %s", firstN(out, 200))
	}
	// Should include column names (A (id), B (val)).
	if !strings.Contains(out, "id") || !strings.Contains(out, "val") {
		t.Errorf("overview should include header names; got: %s", firstN(out, 300))
	}
}

// TestXLSXReadPage verifies the page mode reads a specific row range and
// reports position hints.
func TestXLSXReadPage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "paged.xlsx")
	makeLargeXLSX(t, path, 100, 3) // small file for deterministic assertions

	args, _ := json.Marshal(map[string]any{"path": path, "mode": "page", "offset": 10, "limit": 5})
	out, err := xlsxRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	// Should mention the row range somewhere (10-... or rows 10–).
	if !strings.Contains(out, "rows 10") && !strings.Contains(out, "10–") && !strings.Contains(out, "10-") {
		t.Errorf("page output should show row range starting at 10; got: %s", lastN(out, 120))
	}
}

// TestXLSXQuerySum verifies the aggregation computes a correct sum with a
// where filter. Column B = val = id*10 for ids 1..100, so sum of val where
// id <= 10 = (1+2+...+10)*10 = 550.
func TestXLSXQuerySum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agg.xlsx")
	makeLargeXLSX(t, path, 100, 3)

	args, _ := json.Marshal(map[string]any{
		"path":   path,
		"op":     "sum",
		"column": "val",
		"where":  []map[string]string{{"column": "id", "op": "<=", "value": "10"}},
	})
	out, err := xlsxQuery{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	// Sum of (1..10)*10 = 550.
	if !strings.Contains(out, "550") {
		t.Errorf("sum of val where id<=10 should be 550; got: %s", out)
	}
	// matched_rows should be 10.
	if !strings.Contains(out, `"matched_rows": 10`) {
		t.Errorf("matched_rows should be 10; got: %s", out)
	}
}

// TestXLSXQueryCount verifies count + distinct_count.
func TestXLSXQueryCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "count.xlsx")
	makeLargeXLSX(t, path, 50, 3) // 50 ids, all distinct

	// count: all 50 data rows (header excluded by the iterator starting at row 1).
	args, _ := json.Marshal(map[string]any{"path": path, "op": "count", "column": "id"})
	out, err := xlsxQuery{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"result": "50"`) {
		t.Errorf("count of id should be 50; got: %s", out)
	}

	// distinct_count of id: 50 (all unique).
	args2, _ := json.Marshal(map[string]any{"path": path, "op": "distinct_count", "column": "id"})
	out2, err := xlsxQuery{}.Execute(context.Background(), args2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, `"result": "50"`) {
		t.Errorf("distinct_count of id should be 50; got: %s", out2)
	}
}

// TestXLSXReadFullBackcompat verifies the default (no mode) still works as
// before — delegates to doc_read's full xlsx read. Regression guard.
func TestXLSXReadFullBackcompat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.xlsx")
	makeLargeXLSX(t, path, 5, 3)

	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := xlsxRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	// Full mode returns a formatted table (not JSON overview). Should contain
	// cell data like "r1c2" or the header "val".
	if !strings.Contains(out, "val") {
		t.Errorf("full read should contain header 'val'; got: %s", firstN(out, 200))
	}
}
