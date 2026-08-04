package builtin

// xlsxwrite_structured.go upgrades xlsx generation beyond XLSXWriteRows (which
// only dumps a 2D string array into Sheet1). XLSXWriteStructured accepts a
// full workbook description: multiple sheets, per-cell value/formula, cell
// styles (font/color/bold/background/alignment/number format), merged ranges,
// and column widths — all via excelize (already a dependency).
//
// The input is a structured JSON object (see XLSXWorkbook) so the agent can
// express rich spreadsheets the way doc_write/docx expresses rich documents.
// XLSXWriteRows stays for the simple rows-array case (back-compat).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// XLSXCell is one cell: a number, a value, or a formula, plus an optional
// style and number format. Ref is the A1 reference (e.g. "B3"); required.
// Precedence when more than one is set: Number > Formula > Value. Number is
// strongly typed so numeric cells stay numeric (formulas like =SUM() ignore
// text-stored numbers, so always use Number for numeric data you intend to sum).
type XLSXCell struct {
	Ref     string    `json:"ref"`     // A1 reference, e.g. "B3" (required)
	Value   *string   `json:"value"`   // literal value (string form; for text/labels)
	Number  *float64  `json:"number"`  // numeric value (stored as a real number cell)
	Formula *string   `json:"formula"` // e.g. "=SUM(B2:B5)" (overrides value/number when set)
	Format  string    `json:"format"`  // number format code, e.g. "#,##0", "0.00%", "yyyy-mm-dd"
	Style   XLSXStyle `json:"style"`
}

// XLSXStyle mirrors the run/cell style vocabulary shared with docx, plus a few
// xlsx-specifics (vertical align, border). Colors are "#RRGGBB" (we strip #).
type XLSXStyle struct {
	Bold   bool   `json:"bold"`
	Italic bool   `json:"italic"`
	Color  string `json:"color"`  // font color "#RRGGBB"
	Bg     string `json:"bg"`     // cell fill "#RRGGBB"
	Size   int    `json:"size"`   // font size in points (not half-points; xlsx uses real pts)
	Font   string `json:"font"`   // font family
	Align  string `json:"align"`  // "left"|"center"|"right"
	Wrap   bool   `json:"wrap"`   // wrap text in cell
	Border bool   `json:"border"` // thin border all sides
}

// XLSXMerge is a merged range, A1 notation (e.g. "A1:C1").
type XLSXMerge struct {
	Range string `json:"range"`
}

// XLSXColWidth sets a column's width by letter (e.g. {"A": 20}).
type XLSXColWidth struct {
	Col   string  `json:"col"`
	Width float64 `json:"width"`
}

// XLSXSheet is one worksheet.
type XLSXSheet struct {
	Name      string         `json:"name"`  // sheet tab name (default "Sheet1")
	Cells     []XLSXCell     `json:"cells"` // sparse cells by ref
	Merges    []XLSXMerge    `json:"merges"`
	ColWidths []XLSXColWidth `json:"col_widths"`
	CondFmt   []XLSXCondFmt  `json:"cond_fmt,omitempty"` // conditional formatting
}

// XLSXCondFmt defines conditional formatting for a range. Supported types are
// "cell", "data_bar", and "color_scale". For "cell", Format fills the matching
// cells; for "data_bar"/"color_scale", Format.Bg (or a sensible default) is
// used as the bar/gradient color.
type XLSXCondFmt struct {
	Range    string    `json:"range"`    // cell range (e.g., "A1:A10")
	Type     string    `json:"type"`     // "cell"|"data_bar"|"color_scale"
	Criteria string    `json:"criteria"` // cell type only: "greater_than"|"less_than"|"equal"|"between"
	Value    string    `json:"value"`    // threshold (for "between" use "min,max")
	Format   XLSXStyle `json:"format"`   // style to apply (bg drives bar/gradient color)
}

// XLSXWorkbook is the structured-write payload.
type XLSXWorkbook struct {
	Path   string      `json:"path"`
	Sheets []XLSXSheet `json:"sheets"`
	Charts []XLSXChart `json:"charts,omitempty"` // charts to add
}

// XLSXChart defines a chart to add to a sheet. DataRange is the VALUE range
// (the numbers to plot); CategoryRange (optional) is the axis-label range. When
// CategoryRange is empty the chart uses positional labels (1, 2, 3 …). Pass an
// explicit CategoryRange whenever you want named labels on the category axis
// (e.g. month names).
type XLSXChart struct {
	Sheet         string `json:"sheet"`          // sheet name
	Type          string `json:"type"`           // "bar"|"line"|"pie"|"scatter"
	Title         string `json:"title"`          // chart title
	DataRange     string `json:"data_range"`     // value range (e.g., "B1:B10")
	CategoryRange string `json:"category_range"` // optional axis-label range (e.g., "A1:A10"); empty = positional
	Position      string `json:"position"`       // cell position for chart (e.g., "D2")
}

// XLSXWriteStructured writes a multi-sheet styled workbook via excelize.
// Produces a fully-valid .xlsx openable in Excel/WPS/LibreOffice. Returns the
// sheet count for the success message.
func XLSXWriteStructured(wb XLSXWorkbook) (int, error) {
	if err := os.MkdirAll(filepath.Dir(wb.Path), 0o755); err != nil {
		return 0, err
	}
	f := excelize.NewFile()
	defer f.Close()
	// styleCache dedupes cell styles across the whole workbook: identical
	// (style + number format) combos share one excelize style id. Without this,
	// a thousand identically-styled cells create a thousand styles and can blow
	// past Excel's ~65,430 cellXfs limit on a large table. Style ids are scoped
	// to the workbook (one styles.xml), so one cache serves every sheet.
	styleCache := make(map[string]int)
	// Rename the default "Sheet1" to the first requested sheet, or add new
	// sheets for subsequent ones. Excelize creates a default Sheet1 we reuse.
	for i, sh := range wb.Sheets {
		name := strings.TrimSpace(sh.Name)
		if name == "" {
			name = fmt.Sprintf("Sheet%d", i+1)
		}
		var sheet string
		var err error
		if i == 0 {
			// Reuse the default sheet by renaming it.
			if name != "Sheet1" {
				if err = f.SetSheetName("Sheet1", name); err != nil {
					return 0, err
				}
			}
			sheet = name
		} else {
			idx, addErr := f.NewSheet(name)
			if addErr != nil {
				return 0, addErr
			}
			sheet = name
			f.SetActiveSheet(idx)
		}
		if err := writeSheet(f, sheet, sh, styleCache); err != nil {
			return 0, err
		}
	}

	// Add charts
	for _, chart := range wb.Charts {
		if err := addChart(f, chart); err != nil {
			return 0, fmt.Errorf("add chart: %w", err)
		}
	}

	if err := f.SaveAs(wb.Path); err != nil {
		return 0, err
	}
	return len(wb.Sheets), nil
}

// writeSheet applies cells/merges/col-widths to one sheet. styleCache dedupes
// identical (style + number format) combos to one excelize style id so large
// tables don't exhaust Excel's ~65,430 cellXfs limit.
func writeSheet(f *excelize.File, sheet string, sh XLSXSheet, styleCache map[string]int) error {
	// Column widths first (so styled cells land in correctly-sized columns).
	for _, cw := range sh.ColWidths {
		col := strings.TrimSpace(cw.Col)
		if col == "" || cw.Width <= 0 {
			continue
		}
		if err := f.SetColWidth(sheet, col, col, cw.Width); err != nil {
			return fmt.Errorf("col width %s: %w", col, err)
		}
	}
	// Cells.
	for _, c := range sh.Cells {
		ref := strings.TrimSpace(c.Ref)
		if ref == "" {
			continue
		}
		// Value/formula/number precedence: Formula > Number > Value. A real
		// number cell (SetCellValue with a float) keeps numeric data summable;
		// string values stay text. Formula wins so an explicit formula cell is
		// never clobbered by a stale Number/Value.
		if c.Formula != nil && *c.Formula != "" {
			if err := f.SetCellFormula(sheet, ref, *c.Formula); err != nil {
				return fmt.Errorf("formula %s: %w", ref, err)
			}
		} else if c.Number != nil {
			if err := f.SetCellValue(sheet, ref, *c.Number); err != nil {
				return fmt.Errorf("number %s: %w", ref, err)
			}
		} else if c.Value != nil {
			if err := f.SetCellValue(sheet, ref, *c.Value); err != nil {
				return fmt.Errorf("value %s: %w", ref, err)
			}
		}
		// Style + number format: build ONE style that carries both the run
		// formatting (font/fill/border) and the number format, then apply it in
		// a single SetCellStyle. Earlier code applied number format and style in
		// two separate SetCellStyle calls, and the second silently dropped the
		// first's CustomNumFmt — so a bold header cell lost its currency format.
		// The cache key folds style + fmt together; identical combos share an id.
		fmtCode := strings.TrimSpace(c.Format)
		if !isStyleEmpty(c.Style) || fmtCode != "" {
			styleID, err := cachedStyleID(f, c.Style, fmtCode, styleCache)
			if err != nil {
				return fmt.Errorf("style %s: %w", ref, err)
			}
			if err := f.SetCellStyle(sheet, ref, ref, styleID); err != nil {
				return fmt.Errorf("apply style %s: %w", ref, err)
			}
		}
	}
	// Merges.
	for _, m := range sh.Merges {
		r := strings.TrimSpace(m.Range)
		if r == "" || !strings.Contains(r, ":") {
			continue
		}
		parts := strings.SplitN(r, ":", 2)
		if err := f.MergeCell(sheet, strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])); err != nil {
			return fmt.Errorf("merge %s: %w", r, err)
		}
	}

	// Conditional formatting.
	for _, cf := range sh.CondFmt {
		if err := addConditionalFormat(f, sheet, cf); err != nil {
			return fmt.Errorf("conditional format: %w", err)
		}
	}

	return nil
}

// addChart adds a chart to a sheet. DataRange is the VALUE range (the numbers
// to plot); CategoryRange (optional) is the axis-label range. When
// CategoryRange is empty the chart uses positional categories (1, 2, 3 …).
// (Previously both Categories and Values pointed at the same range, producing a
// chart that drew its labels as data.)
func addChart(f *excelize.File, chart XLSXChart) error {
	sheet := strings.TrimSpace(chart.Sheet)
	if sheet == "" {
		sheet = "Sheet1"
	}

	// Parse chart type
	chartType := excelize.Bar
	switch strings.ToLower(strings.TrimSpace(chart.Type)) {
	case "bar":
		chartType = excelize.Bar
	case "line":
		chartType = excelize.Line
	case "pie":
		chartType = excelize.Pie
	case "scatter":
		chartType = excelize.Scatter
	}

	// Parse value range (e.g., "B1:B10") — required.
	dataRange := strings.TrimSpace(chart.DataRange)
	if dataRange == "" {
		return fmt.Errorf("data_range is required")
	}

	// Categories are optional. An explicit CategoryRange plots labels on the
	// category axis; when empty, the chart falls back to positional labels.
	categories := strings.TrimSpace(chart.CategoryRange)

	// Add chart
	return f.AddChart(sheet, chart.Position, &excelize.Chart{
		Type: chartType,
		Series: []excelize.ChartSeries{
			{
				Name:       chart.Title,
				Categories: categories,
				Values:     dataRange,
			},
		},
		Title: excelize.ChartTitle{
			Paragraph: []excelize.RichTextRun{
				{
					Text: chart.Title,
				},
			},
		},
	})
}

// addConditionalFormat adds conditional formatting to a sheet. "cell" types
// apply the Format style to matching cells; "data_bar"/"color_scale" ignore the
// cell Format and instead read Format.Bg for the bar/gradient color (falling
// back to sensible defaults), because those types carry their own color stops.
func addConditionalFormat(f *excelize.File, sheet string, cf XLSXCondFmt) error {
	rng := strings.TrimSpace(cf.Range)
	if rng == "" {
		return fmt.Errorf("range is required")
	}
	cfType := strings.ToLower(strings.TrimSpace(cf.Type))

	switch cfType {
	case "cell":
		// Cell value condition: build the matching-cell style and apply it.
		styleID, err := f.NewStyle(xlsxStyleFrom(cf.Format))
		if err != nil {
			return fmt.Errorf("style: %w", err)
		}
		format := excelize.ConditionalFormatOptions{
			Type:     "cell",
			Criteria: cf.Criteria,
			Format:   &styleID,
			Value:    cf.Value, // "min,max" for "between"; single value otherwise
		}
		return f.SetConditionalFormat(sheet, rng, []excelize.ConditionalFormatOptions{format})

	case "data_bar":
		// Data bars ignore the cell Format style and need a BarColor plus the
		// "=" criteria sentinel excelize's validator requires for this type.
		// MinType/MaxType anchor the bar at the range min/max.
		barColor := hexNoHash(cf.Format.Bg)
		if barColor == "" {
			barColor = "638EC6" // Excel's default data-bar blue
		}
		return f.SetConditionalFormat(sheet, rng, []excelize.ConditionalFormatOptions{
			{
				Type:     "data_bar",
				Criteria: "=",
				MinType:  "min",
				MaxType:  "max",
				BarColor: barColor,
			},
		})

	case "color_scale":
		// Excelize's valid types are "2_color_scale"/"3_color_scale" (not
		// "color_scale"). A 2-color scale anchored at min/max is the safe
		// default; the caller's Format.Bg overrides both stops when given.
		minColor := "F8696B" // red
		maxColor := "63BE7B" // green
		if bg := hexNoHash(cf.Format.Bg); bg != "" {
			minColor = bg
			maxColor = bg
		}
		return f.SetConditionalFormat(sheet, rng, []excelize.ConditionalFormatOptions{
			{
				Type:     "2_color_scale",
				Criteria: "=",
				MinType:  "min",
				MaxType:  "max",
				MinColor: minColor,
				MaxColor: maxColor,
			},
		})

	default:
		return fmt.Errorf("unsupported conditional format type %q (supported: cell, data_bar, color_scale)", cf.Type)
	}
}

// cachedStyleID returns a shared excelize style id for a (style + number
// format) combo, creating it on first request and reusing it thereafter. The
// cache key is a string snapshot of the style + format, so a thousand
// identically-styled cells produce one style, not a thousand — keeping large
// tables under Excel's ~65,430 cellXfs limit.
func cachedStyleID(f *excelize.File, s XLSXStyle, numFmt string, cache map[string]int) (int, error) {
	key := styleCacheKey(s, numFmt)
	if id, ok := cache[key]; ok {
		return id, nil
	}
	id, err := f.NewStyle(xlsxStyleWithFmt(s, numFmt))
	if err != nil {
		return 0, err
	}
	cache[key] = id
	return id, nil
}

// styleCacheKey builds a deterministic string key for a style + format combo.
// The exact bytes don't matter — only that identical combos collide and
// distinct combos differ.
func styleCacheKey(s XLSXStyle, numFmt string) string {
	return fmt.Sprintf("b%v|i%v|c%s|bg%s|d%d|f%s|a%s|w%v|bd%v|%s",
		s.Bold, s.Italic, s.Color, s.Bg, s.Size, s.Font, s.Align, s.Wrap, s.Border, numFmt)
}

// xlsxStyleWithFmt compiles our XLSXStyle plus an optional number-format code
// into one excelize.Style. Merging the format here (instead of a separate
// SetCellStyle) means a single style id carries both run formatting and the
// number format — the only way to keep e.g. a bold "$1,000" header from losing
// its currency mask. An empty numFmt makes this equivalent to xlsxStyleFrom.
func xlsxStyleWithFmt(s XLSXStyle, numFmt string) *excelize.Style {
	st := xlsxStyleFrom(s)
	numFmt = strings.TrimSpace(numFmt)
	if numFmt != "" {
		st.CustomNumFmt = &numFmt
	}
	return st
}

// xlsxStyleFrom compiles our XLSXStyle into an excelize.Style. Colors drop the
// leading # (excelize wants RRGGBB). Fill uses a solid pattern.
func xlsxStyleFrom(s XLSXStyle) *excelize.Style {
	st := &excelize.Style{}
	font := &excelize.Font{Bold: s.Bold, Italic: s.Italic}
	if s.Color != "" {
		font.Color = hexNoHash(s.Color)
	}
	if s.Font != "" {
		font.Family = s.Font
	}
	if s.Size > 0 {
		font.Size = float64(s.Size)
	}
	st.Font = font
	if s.Bg != "" {
		st.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{hexNoHash(s.Bg)}}
	}
	if s.Align != "" || s.Wrap {
		al := &excelize.Alignment{WrapText: s.Wrap}
		switch strings.ToLower(s.Align) {
		case "left":
			al.Horizontal = "left"
		case "center":
			al.Horizontal = "center"
		case "right":
			al.Horizontal = "right"
		}
		st.Alignment = al
	}
	if s.Border {
		st.Border = []excelize.Border{
			{Type: "left", Color: "999999", Style: 1},
			{Type: "right", Color: "999999", Style: 1},
			{Type: "top", Color: "999999", Style: 1},
			{Type: "bottom", Color: "999999", Style: 1},
		}
	}
	return st
}

func isStyleEmpty(s XLSXStyle) bool {
	return !s.Bold && !s.Italic && !s.Wrap && !s.Border &&
		s.Color == "" && s.Bg == "" && s.Size == 0 && s.Font == "" && s.Align == ""
}
