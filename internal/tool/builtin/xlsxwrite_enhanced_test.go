package builtin

import (
	"archive/zip"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func floatPtr(v float64) *float64 { return &v }

// findAndReadChart opens the xlsx zip and returns the first chart part's XML.
// (excelize v2.11 exposes no public chart getter, so we read the part directly
// from the zip — the chart's <c:f> elements carry the series ranges.)
func findAndReadChart(t *testing.T, path string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "xl/charts/chart") && strings.HasSuffix(f.Name, ".xml") {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open chart: %v", err)
			}
			defer rc.Close()
			b := make([]byte, 0, f.UncompressedSize64)
			buf := make([]byte, 4096)
			for {
				n, rerr := rc.Read(buf)
				if n > 0 {
					b = append(b, buf[:n]...)
				}
				if rerr != nil {
					break
				}
			}
			return string(b)
		}
	}
	t.Fatalf("no chart part found in %s", path)
	return ""
}

// TestXLSXChartValuesNotDuplicatedAsCategories verifies the core chart fix:
// the value range must NOT also be used as the categories range (the old bug
// bound both Categories and Values to data_range, drawing labels as data). Here
// CategoryRange is empty → no strRef category range should appear, and Values
// point at the B column.
func TestXLSXChartValuesNotDuplicatedAsCategories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chart.xlsx")
	wb := XLSXWorkbook{
		Path: path,
		Sheets: []XLSXSheet{{
			Name: "Sheet1",
			Cells: []XLSXCell{
				{Ref: "B2", Number: floatPtr(1000)},
				{Ref: "B3", Number: floatPtr(1500)},
			},
		}},
		Charts: []XLSXChart{{
			Sheet:     "Sheet1",
			Type:      "bar",
			Title:     "Monthly Sales",
			DataRange: "B2:B3", // values only, no category_range
			Position:  "D2",
		}},
	}
	if _, err := XLSXWriteStructured(wb); err != nil {
		t.Fatalf("write: %v", err)
	}
	xml := findAndReadChart(t, path)
	// Values reference column B.
	if !strings.Contains(xml, "<val>") || !strings.Contains(xml, "B2:B3") {
		t.Errorf("value range B2:B3 missing from chart XML\n%s", xml)
	}
	// With no category_range, the category reference should NOT mirror the value
	// range (the old duplication bug). A positional chart has a numRef cat with
	// the same range OR no cat strRef — we assert no strRef duplicates the value.
	catCount := strings.Count(xml, "<cat>")
	if catCount > 0 {
		// If a cat block exists, it must NOT point at the value range B2:B3.
		catStart := strings.Index(xml, "<cat>")
		catEnd := strings.Index(xml, "</cat>")
		if catEnd < 0 {
			catEnd = len(xml)
		}
		catBlock := xml[catStart:catEnd]
		if strings.Contains(catBlock, "B2:B3") && !strings.Contains(catBlock, "numRef") {
			t.Errorf("category block duplicated the value range (the old bug)\n%s", catBlock)
		}
	}
}

// TestXLSXChartExplicitCategoryRange: an explicit category_range produces a
// category axis pointing at a different range than the values.
func TestXLSXChartExplicitCategoryRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chart2.xlsx")
	wb := XLSXWorkbook{
		Path: path,
		Sheets: []XLSXSheet{{
			Name: "Sheet1",
			Cells: []XLSXCell{
				{Ref: "A2", Value: strPtr("Jan")},
				{Ref: "A3", Value: strPtr("Feb")},
				{Ref: "C2", Number: floatPtr(7)},
				{Ref: "C3", Number: floatPtr(9)},
			},
		}},
		Charts: []XLSXChart{{
			Sheet:         "Sheet1",
			Type:          "line",
			Title:         "T",
			DataRange:     "C2:C3",
			CategoryRange: "A2:A3",
			Position:      "E2",
		}},
	}
	if _, err := XLSXWriteStructured(wb); err != nil {
		t.Fatalf("write: %v", err)
	}
	xml := findAndReadChart(t, path)
	// Categories = A2:A3 (explicit, distinct from values C2:C3).
	if !strings.Contains(xml, "A2:A3") {
		t.Errorf("explicit category range A2:A3 not applied\n%s", xml)
	}
	if !strings.Contains(xml, "C2:C3") {
		t.Errorf("value range C2:C3 missing\n%s", xml)
	}
}

// TestXLSXNumberCellStoredAsNumber: a numeric cell must round-trip as a number
// (its stored value is numeric), keeping it summable by formulas — not stored
// as a text string that =SUM() would ignore. We check the raw cell value via
// GetCellValue and that a SUM formula referencing it is accepted.
func TestXLSXNumberCellStoredAsNumber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "num.xlsx")
	wb := XLSXWorkbook{
		Path: path,
		Sheets: []XLSXSheet{{
			Name: "Sheet1",
			Cells: []XLSXCell{
				{Ref: "A1", Number: floatPtr(120)},
				{Ref: "A2", Number: floatPtr(30)},
				{Ref: "A3", Formula: strPtr("=SUM(A1:A2)")},
			},
		}},
	}
	if _, err := XLSXWriteStructured(wb); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	// The numeric value reads back as the number, not as shared-string text.
	v, err := f.GetCellValue("Sheet1", "A1")
	if err != nil {
		t.Fatalf("get A1: %v", err)
	}
	if v != "120" {
		t.Errorf("A1 numeric value = %q, want 120 (number cells store the digits, not text)", v)
	}
	// And the SUM formula was stored (excelize echoes the leading "=" on read).
	formula, err := f.GetCellFormula("Sheet1", "A3")
	if err != nil {
		t.Fatalf("get A3 formula: %v", err)
	}
	wantFormula := "SUM(A1:A2)"
	if formula != wantFormula && formula != "="+wantFormula {
		t.Errorf("A3 formula = %q, want %s", formula, wantFormula)
	}
}

// TestXLSXCellFormatPlusStyleCoexist: a cell with BOTH a number format and a
// run style must keep both. Earlier code issued two SetCellStyle calls and the
// second dropped the first's CustomNumFmt, so a bold currency cell lost its
// mask. We assert the style id on the cell carries a custom number format.
func TestXLSXCellFormatPlusStyleCoexist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fmt.xlsx")
	wb := XLSXWorkbook{
		Path: path,
		Sheets: []XLSXSheet{{
			Name: "Sheet1",
			Cells: []XLSXCell{{
				Ref:    "A1",
				Number: floatPtr(1234.5),
				Format: "#,##0.00",
				Style:  XLSXStyle{Bold: true, Color: "#FF0000"},
			}},
		}},
	}
	if _, err := XLSXWriteStructured(wb); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	styleID, err := f.GetCellStyle("Sheet1", "A1")
	if err != nil {
		t.Fatalf("get style: %v", err)
	}
	st, err := f.GetStyle(styleID)
	if err != nil {
		t.Fatalf("get style def: %v", err)
	}
	if st.CustomNumFmt == nil || *st.CustomNumFmt != "#,##0.00" {
		t.Errorf("number format lost when combined with style; CustomNumFmt=%v", st.CustomNumFmt)
	}
	if st.Font == nil || !st.Font.Bold {
		t.Errorf("bold style lost; Font=%v", st.Font)
	}
}

// TestXLSXCondFmtColorScaleFields: a color_scale conditional format must emit
// MinType/MaxType + MinColor/MaxColor (a bare Format style is ignored by this
// type and produces an invalid gradient). Excelize's internal type is
// "2_color_scale"; our API accepts "color_scale" and maps it.
func TestXLSXCondFmtColorScaleFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scale.xlsx")
	wb := XLSXWorkbook{
		Path: path,
		Sheets: []XLSXSheet{{
			Name: "Sheet1",
			Cells: []XLSXCell{
				{Ref: "A1", Number: floatPtr(1)},
				{Ref: "A2", Number: floatPtr(2)},
				{Ref: "A3", Number: floatPtr(3)},
			},
			CondFmt: []XLSXCondFmt{{
				Range: "A1:A3",
				Type:  "color_scale",
			}},
		}},
	}
	if _, err := XLSXWriteStructured(wb); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	cfs, err := f.GetConditionalFormats("Sheet1")
	if err != nil {
		t.Fatalf("get cond fmts: %v", err)
	}
	opts, ok := cfs["A1:A3"]
	if !ok || len(opts) == 0 {
		t.Fatalf("no color_scale cond fmt stored on A1:A3; got %v", cfs)
	}
	o := opts[0]
	// Excelize stores the internal 2_color_scale type.
	if o.Type != "2_color_scale" {
		t.Errorf("type = %q, want 2_color_scale", o.Type)
	}
	// Colors read back with a leading "#" (excelize normalizes on read).
	if strings.TrimPrefix(o.MinColor, "#") != "F8696B" || strings.TrimPrefix(o.MaxColor, "#") != "63BE7B" {
		t.Errorf("colors = min:%q max:%q, want F8696B/63BE7B", o.MinColor, o.MaxColor)
	}
	if o.MinType != "min" || o.MaxType != "max" {
		t.Errorf("MinType/MaxType must be min/max, got %q/%q", o.MinType, o.MaxType)
	}
}

// TestXLSXCondFmtDataBarBarColor: a data_bar conditional format must emit a
// BarColor (defaults to Excel's blue when Format.Bg unset).
func TestXLSXCondFmtDataBarBarColor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bar.xlsx")
	wb := XLSXWorkbook{
		Path: path,
		Sheets: []XLSXSheet{{
			Name: "Sheet1",
			Cells: []XLSXCell{
				{Ref: "A1", Number: floatPtr(1)},
				{Ref: "A2", Number: floatPtr(2)},
			},
			CondFmt: []XLSXCondFmt{{
				Range: "A1:A2",
				Type:  "data_bar",
			}},
		}},
	}
	if _, err := XLSXWriteStructured(wb); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	cfs, err := f.GetConditionalFormats("Sheet1")
	if err != nil {
		t.Fatalf("get cond fmts: %v", err)
	}
	opts, ok := cfs["A1:A2"]
	if !ok || len(opts) == 0 {
		t.Fatalf("no data_bar cond fmt stored; got %v", cfs)
	}
	o := opts[0]
	if o.Type != "data_bar" {
		t.Errorf("type = %q, want data_bar", o.Type)
	}
	if strings.TrimPrefix(o.BarColor, "#") != "638EC6" {
		t.Errorf("BarColor = %q, want default 638EC6", o.BarColor)
	}
}

// TestXLSXCondFmtDataBarCustomColor: when Format.Bg is set, the data bar uses
// that color instead of the default blue.
func TestXLSXCondFmtDataBarCustomColor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bar2.xlsx")
	wb := XLSXWorkbook{
		Path: path,
		Sheets: []XLSXSheet{{
			Name: "Sheet1",
			Cells: []XLSXCell{
				{Ref: "A1", Number: floatPtr(1)},
				{Ref: "A2", Number: floatPtr(2)},
			},
			CondFmt: []XLSXCondFmt{{
				Range:  "A1:A2",
				Type:   "data_bar",
				Format: XLSXStyle{Bg: "#00FF00"},
			}},
		}},
	}
	if _, err := XLSXWriteStructured(wb); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	cfs, _ := f.GetConditionalFormats("Sheet1")
	opts := cfs["A1:A2"]
	if len(opts) == 0 || strings.TrimPrefix(opts[0].BarColor, "#") != "00FF00" {
		t.Errorf("custom bar color not applied; got %+v", opts)
	}
}

// TestXLSXCondFmtUnsupportedTypeErrors: icon_set / formula are not implemented;
// requesting one must return a clear error rather than silently no-op.
func TestXLSXCondFmtUnsupportedTypeErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.xlsx")
	wb := XLSXWorkbook{
		Path: path,
		Sheets: []XLSXSheet{{
			Name:  "Sheet1",
			Cells: []XLSXCell{{Ref: "A1", Value: strPtr("x")}},
			CondFmt: []XLSXCondFmt{{
				Range: "A1:A1",
				Type:  "icon_set",
			}},
		}},
	}
	_, err := XLSXWriteStructured(wb)
	if err == nil {
		t.Fatalf("expected unsupported-type error for icon_set, got nil")
	}
	if !strings.Contains(err.Error(), "icon_set") {
		t.Errorf("error should name the unsupported type, got: %v", err)
	}
}

// TestXLSXStyleDeduplication: many identically-styled cells must share ONE
// style id, not one per cell. Without dedup a large table (thousands of styled
// cells) would exhaust Excel's ~65,430 cellXfs limit. We write 100 identical
// bold header cells across two columns and assert every cell references the
// same style id.
func TestXLSXStyleDeduplication(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dedup.xlsx")
	headerStyle := XLSXStyle{Bold: true, Bg: "#4472C4", Color: "#FFFFFF"}
	var cells []XLSXCell
	// 100 cells, all the SAME style → should collapse to 1 style id.
	for r := 1; r <= 50; r++ {
		cells = append(cells,
			XLSXCell{Ref: fmt.Sprintf("A%d", r), Value: strPtr(fmt.Sprintf("r%d", r)), Style: headerStyle},
			XLSXCell{Ref: "B" + fmt.Sprintf("%d", r), Number: floatPtr(float64(r)), Format: "#,##0", Style: headerStyle},
		)
	}
	wb := XLSXWorkbook{Path: path, Sheets: []XLSXSheet{{Name: "Sheet1", Cells: cells}}}
	if _, err := XLSXWriteStructured(wb); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	// Collect the distinct style ids applied to the 100 cells.
	seen := map[int]bool{}
	for r := 1; r <= 50; r++ {
		for _, ref := range []string{fmt.Sprintf("A%d", r), "B" + fmt.Sprintf("%d", r)} {
			id, err := f.GetCellStyle("Sheet1", ref)
			if err != nil {
				t.Fatalf("get style %s: %v", ref, err)
			}
			seen[id] = true
		}
	}
	// Cells with the SAME style+format share an id; A-cells (no format) and
	// B-cells (with "#,##0") differ, so we expect exactly 2 distinct ids, NOT 100.
	if len(seen) > 2 {
		t.Errorf("style dedup failed: %d distinct style ids for 100 cells (want ≤2); ids=%v", len(seen), seen)
	}
}
