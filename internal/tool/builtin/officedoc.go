package builtin

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Binary Office document parsing and spreadsheet writing.
//   - .xlsx read + write: excelize (full style/formula/multi-sheet support).
//   - .docx / .pptx text extraction: stdlib (archive/zip + encoding/xml),
//     since there's no lightweight license-clean docx lib comparable to
//     excelize. .docx writing is handled separately in docxwrite.go (stdlib
//     zip+XML); .pptx writing remains unsupported (use the ppt tools).
//
// excelize gives xlsx robustness the earlier hand-rolled OOXML lacked (formulas,
// styles, multi-sheet, dates). docx/pptx text extraction is a contained need
// (pull <w:t>/<a:t> runs) that stdlib covers well.

// --- xlsx (excelize) --------------------------------------------------------

// readXLSX extracts cell values from a .xlsx via excelize, returning rows across
// ALL sheets (each sheet's rows concatenated, with a "--- sheet: Name ---"
// separator between sheets). Handles shared strings, formulas (cached values),
// booleans, dates, and multi-sheet workbooks. Integer-valued numbers drop the
// trailing .0 for readability.

func readXLSX(path string) ([][]string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("open xlsx (is it a valid .xlsx?): %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil
	}
	var allRows [][]string
	for si, sheet := range sheets {
		// GetRows gives the used range but truncates trailing empty cells per
		// row and returns "" for formula cells without a cached value. We use
		// it only to size the grid, then re-read each cell by reference so we
		// can (a) recover formulas as "=FORMULA" and (b) pad rows to equal
		// width so the model sees a rectangular table.
		sized, err := f.GetRows(sheet)
		if err != nil {
			return nil, fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		if si > 0 {
			allRows = append(allRows, []string{fmt.Sprintf("--- sheet: %s ---", sheet)})
		}
		// Determine the max column count across all rows so every row can be
		// padded to the same width (C-10: trailing-empty-cell padding).
		maxCols := 0
		rowCount := len(sized)
		for r := 0; r < rowCount; r++ {
			if w := len(sized[r]); w > maxCols {
				maxCols = w
			}
		}
		for r := 0; r < rowCount; r++ {
			row := make([]string, maxCols)
			for c := 0; c < maxCols; c++ {
				ref, cerr := excelize.CoordinatesToCellName(c+1, r+1)
				if cerr != nil {
					continue
				}
				val, _ := f.GetCellValue(sheet, ref, excelize.Options{RawCellValue: true})
				val = normalizeNumber(strings.TrimSpace(val))
				// Recover formulas: a formula cell with no cached value reads
				// back as "" — surface it as "=FORMULA" so the model can reason
				// about its own spreadsheet (C-2).
				if val == "" {
					if t, terr := f.GetCellType(sheet, ref); terr == nil && t == excelize.CellTypeFormula {
						if formula, ferr := f.GetCellFormula(sheet, ref); ferr == nil && formula != "" {
							val = "=" + formula
						}
					}
				}
				row[c] = val
			}
			allRows = append(allRows, row)
		}
	}
	return allRows, nil
}

// XLSXWriteRows writes rows to a .xlsx file at path via excelize (one sheet,
// "Sheet1"). Produces a fully-valid workbook openable in Excel/WPS/LibreOffice.
// The write is crash-atomic (temp + fsync + rename) so a crash mid-write can't
// leave a torn, unopenable .xlsx at the target.
func XLSXWriteRows(path string, rows [][]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f := excelize.NewFile()
	defer f.Close()
	sheet := "Sheet1"
	for ri, row := range rows {
		for ci, val := range row {
			cell, err := excelize.CoordinatesToCellName(ci+1, ri+1)
			if err != nil {
				return err
			}
			// Auto-type numeric-looking values so SUM/AVERAGE treat them as
			// numbers. Leading-zero strings ("001") and thousands-separated
			// values ("1,000") stay text (see isNumericLiteral).
			if isNumericLiteral(val) {
				if err := f.SetCellValue(sheet, cell, numericValue(val)); err != nil {
					return err
				}
			} else {
				if err := f.SetCellValue(sheet, cell, val); err != nil {
					return err
				}
			}
		}
	}
	// Write the workbook to a temp file via f.WriteTo, then atomically swap it
	// over path. atomicWrite handles fsync + rename + temp cleanup on error.
	return atomicWrite(path, func(out *os.File) error {
		_, err := f.WriteTo(out)
		return err
	})
}

// --- xlsx overview / page / query (large-spreadsheet support) ---------------
//
// readXLSX (above) materializes the whole sheet — fine for small files but
// OOMs/slow on 300k-row spreadsheets. The three functions below serve the
// large-table path that xlsx_read (mode overview/page) and xlsx_query use:
//   - readXLSXOverview: O(1) dimensions via GetSheetDimension + a 50-row sample
//     via the Rows iterator. Seconds on a 300k-row file (vs minutes for full).
//   - readXLSXPage: Rows iterator skips `offset` rows (O(offset), no seek in
//     excelize v2.11) then reads `limit`, recovering formulas only inside the
//     window. Deep pagination is intentionally slow — callers should prefer
//     xlsx_query (aggregate) over deep paging.
//   - queryXLSX: single-pass streaming aggregation (sum/avg/min/max/count/
//     distinct_count) with structured {column,op,value} where filters. Never
//     materializes the sheet; constant memory.

// xlsxOverviewSample is the number of leading rows the overview reads to infer
// column types and show a sample. Kept small so overview is fast on huge files.
const xlsxOverviewSample = 50

// xlsxDimensionScanCap bounds the fallback row-count scan when
// GetSheetDimension returns "" (some writers omit the <dimension> element).
// Without this a 1M-row sheet would scan fully just to report its size.
const xlsxDimensionScanCap = 200_000

// readXLSXOverview returns a JSON description of the workbook: each sheet's
// dimensions, column types (inferred from the first sample rows), and a small
// row sample. It does NOT read the whole sheet, so it's fast on huge files.
// sheet may be "" to mean "the first sheet".
func readXLSXOverview(path, sheet string) (string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return "", fmt.Errorf("open xlsx (is it a valid .xlsx?): %w", err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return "", fmt.Errorf("xlsx has no sheets")
	}
	if sheet == "" {
		sheet = sheets[0]
	}
	type colInfo struct {
		Name   string   `json:"name"`
		Type   string   `json:"type"`
		Sample []string `json:"sample,omitempty"`
	}
	type sheetInfo struct {
		Name      string    `json:"name"`
		Rows      int       `json:"rows"`
		Cols      int       `json:"cols"`
		RowsExact bool      `json:"rows_exact"`
		Columns   []colInfo `json:"columns,omitempty"`
		FirstRows [][]string `json:"first_rows,omitempty"`
	}
	// Just report the requested sheet in detail; list the others by name only.
	type overviewOut struct {
		File   string      `json:"file"`
		Sheet  string      `json:"sheet"`
		Sheets []string    `json:"sheets"`
		Detail sheetInfo   `json:"detail"`
		Note   string      `json:"note"`
	}

	rows, cols, exact := xlsxDimensions(f, sheet)
	// Stream the first sample rows for column inference + sample display.
	rowsIter, rerr := f.Rows(sheet)
	if rerr != nil {
		return "", fmt.Errorf("read sheet %q: %w", sheet, rerr)
	}
	var firstRows [][]string
	colTypes := make(map[int]string) // col index -> "number"/"text"/""
	rowIdx := 0
	for rowsIter.Next() {
		if rowIdx >= xlsxOverviewSample {
			break
		}
		cols2, _ := rowsIter.Columns()
		if cols2 == nil {
			cols2 = []string{}
		}
		firstRows = append(firstRows, cols2)
		// Infer type from data rows (skip header heuristic: treat all rows as
		// evidence; if any non-empty cell in a column fails ParseFloat, mark text).
		for i, v := range cols2 {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if _, perr := strconv.ParseFloat(v, 64); perr != nil {
				colTypes[i] = "text"
			} else if colTypes[i] == "" {
				colTypes[i] = "number"
			}
		}
		rowIdx++
	}
	if e := rowsIter.Close(); e != nil && rerr == nil {
		rerr = e
	}
	// Build column descriptors (use header from first row if present).
	detailCols := make([]colInfo, 0, cols)
	headerRow := map[int]string{}
	if len(firstRows) > 0 {
		for i, h := range firstRows[0] {
			hh := strings.TrimSpace(h)
			if hh != "" {
				headerRow[i] = hh
			}
		}
	}
	maxColSeen := cols
	for _, r := range firstRows {
		if len(r) > maxColSeen {
			maxColSeen = len(r)
		}
	}
	for c := 0; c < maxColSeen; c++ {
		name, _ := excelize.ColumnNumberToName(c + 1)
		display := name
		if h, ok := headerRow[c]; ok {
			display = fmt.Sprintf("%s (%s)", name, h)
		}
		t := colTypes[c]
		if t == "" {
			t = "unknown"
		}
		var sample []string
		for _, r := range firstRows {
			if c < len(r) {
				sample = append(sample, r[c])
			}
		}
		detailCols = append(detailCols, colInfo{Name: display, Type: t, Sample: sample})
	}

	out := overviewOut{
		File:   path,
		Sheet:  sheet,
		Sheets: sheets,
		Detail: sheetInfo{
			Name: sheet, Rows: rows, Cols: cols, RowsExact: exact,
			Columns:   detailCols,
			FirstRows: firstRows,
		},
		Note: "use xlsx_read mode:page (offset/limit) to read a row range, or xlsx_query to aggregate. Do NOT read the whole file.",
	}
	b, jerr := json.MarshalIndent(out, "", "  ")
	if jerr != nil {
		return "", jerr
	}
	return string(b), nil
}

// readXLSXPage reads rows [offset, offset+limit) of the given sheet via the
// streaming Rows iterator, recovering formulas only inside the window. offset
// skip is O(offset) (excelize v2.11 has no seek). Returns the formatted rows
// plus a trailer with position hints.
func readXLSXPage(path, sheet string, offset, limit int) (string, error) {
	if sheet == "" {
		// Resolve default sheet name.
		f, err := excelize.OpenFile(path)
		if err != nil {
			return "", fmt.Errorf("open xlsx: %w", err)
		}
		ss := f.GetSheetList()
		f.Close()
		if len(ss) == 0 {
			return "", fmt.Errorf("xlsx has no sheets")
		}
		sheet = ss[0]
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 10000
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()
	rows, err := f.Rows(sheet)
	if err != nil {
		return "", fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	defer rows.Close()
	// Skip to offset.
	rowIdx := -1
	var page [][]string
	for rows.Next() {
		rowIdx++
		if rowIdx < offset {
			continue
		}
		if len(page) >= limit {
			break
		}
		cols, _ := rows.Columns()
		if cols == nil {
			cols = []string{}
		}
		page = append(page, cols)
	}
	if e := rows.Error(); e != nil {
		return "", fmt.Errorf("scan sheet %q: %w", sheet, e)
	}
	// Formula recovery inside the window only: for empty cells, check via cell
	// ref if it's a formula. This is O(page cells), not O(whole sheet).
	for i, row := range page {
		absRow := offset + i + 1 // 1-based for cell refs
		for c, val := range row {
			if val != "" {
				row[c] = normalizeNumber(strings.TrimSpace(val))
				continue
			}
			ref, cerr := excelize.CoordinatesToCellName(c+1, absRow)
			if cerr != nil {
				continue
			}
			if t, terr := f.GetCellType(sheet, ref); terr == nil && t == excelize.CellTypeFormula {
				if formula, ferr := f.GetCellFormula(sheet, ref); ferr == nil && formula != "" {
					row[c] = "=" + formula
				}
			}
		}
	}
	body := formatRows(page)
	total, _, _ := xlsxDimensions(f, sheet)
	trailer := fmt.Sprintf("\n[rows %d–%d of %s; ", offset, offset+len(page)-1, rowsLabel(total))
	if total > 0 && offset+len(page) < total {
		trailer += fmt.Sprintf("next page: offset=%d]", offset+len(page))
	} else {
		trailer += "end of sheet]"
	}
	return body + trailer, nil
}

// queryXLSX runs a single-pass streaming aggregation over a sheet, applying
// structured where filters, and returns the result. It never materializes the
// whole sheet — constant memory regardless of row count.
type xlsxWhereCond struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value"`
}

func queryXLSX(path, sheet, op, column string, where []xlsxWhereCond) (string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()
	if sheet == "" {
		ss := f.GetSheetList()
		if len(ss) == 0 {
			return "", fmt.Errorf("xlsx has no sheets")
		}
		sheet = ss[0]
	}
	// Resolve the target column index: letter (A/B) or header-name match.
	// Read the first row as the header for name matching.
	rows, err := f.Rows(sheet)
	if err != nil {
		return "", fmt.Errorf("read sheet %q: %w", sheet, err)
	}
	defer rows.Close()
	// First row = header.
	if !rows.Next() {
		return "", fmt.Errorf("sheet %q is empty", sheet)
	}
	header, _ := rows.Columns()
	targetCol, err := resolveColumnIndex(column, header)
	if err != nil {
		return "", err
	}
	// Resolve where column indices once.
	type resolvedCond struct {
		col int
		op  string
		val string
	}
	conds := make([]resolvedCond, 0, len(where))
	for _, w := range where {
		ci, cerr := resolveColumnIndex(w.Column, header)
		if cerr != nil {
			return "", fmt.Errorf("where column %q: %w", w.Column, cerr)
		}
		conds = append(conds, resolvedCond{col: ci, op: strings.TrimSpace(w.Op), val: w.Value})
	}
	// Aggregate.
	var sum float64
	count := 0
	var minV, maxV string
	hasMinMax := false
	distinct := make(map[string]struct{})
	matched := 0
	scanned := 0
	for rows.Next() {
		scanned++
		cells, _ := rows.Columns()
		// Evaluate where (AND of all conds).
		ok := true
		for _, c := range conds {
			cv := cellAt(cells, c.col)
			if !evalCond(cv, c.op, c.val) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		matched++
		v := strings.TrimSpace(cellAt(cells, targetCol))
		distinct[v] = struct{}{}
		switch op {
		case "count":
			// counted via matched
		case "distinct_count":
			// counted via distinct map
		case "sum", "avg":
			if v == "" {
				continue
			}
			n, perr := strconv.ParseFloat(v, 64)
			if perr != nil {
				return "", fmt.Errorf("op %q requires numeric column, but cell %q is not a number", op, v)
			}
			sum += n
			count++
		case "min", "max":
			if v == "" {
				continue
			}
			if !hasMinMax {
				minV, maxV = v, v
				hasMinMax = true
				continue
			}
			if compareVals(v, minV) < 0 {
				minV = v
			}
			if compareVals(v, maxV) > 0 {
				maxV = v
			}
		}
	}
	if e := rows.Error(); e != nil {
		return "", fmt.Errorf("scan sheet %q: %w", sheet, e)
	}
	// Build result.
	result := ""
	switch op {
	case "count":
		result = strconv.Itoa(matched)
	case "distinct_count":
		result = strconv.Itoa(len(distinct))
	case "sum":
		result = strconv.FormatFloat(sum, 'f', -1, 64)
	case "avg":
		if count == 0 {
			result = "0"
		} else {
			result = strconv.FormatFloat(sum/float64(count), 'f', -1, 64)
		}
	case "min":
		result = minV
	case "max":
		result = maxV
	default:
		return "", fmt.Errorf("unsupported op %q (use sum/avg/min/max/count/distinct_count)", op)
	}
	out := map[string]any{
		"op":            op,
		"column":        column,
		"result":        result,
		"matched_rows":  matched,
		"total_scanned": scanned,
	}
	if len(where) > 0 {
		out["where"] = where
	}
	b, jerr := json.MarshalIndent(out, "", "  ")
	if jerr != nil {
		return "", jerr
	}
	return string(b), nil
}

// xlsxDimensions returns (rows, cols, exact) for a sheet. Tries
// GetSheetDimension first (O(1)); on empty/inexact it falls back to a capped
// streaming count. exact is false when the count is a scan estimate.
func xlsxDimensions(f *excelize.File, sheet string) (rows, cols int, exact bool) {
	if dim, derr := f.GetSheetDimension(sheet); derr == nil && dim != "" {
		if r, c, ok := parseDimensionRef(dim); ok {
			return r, c, true
		}
	}
	// Fallback: scan up to the cap counting rows; cols from the first data row.
	rowsIter, rerr := f.Rows(sheet)
	if rerr != nil {
		return 0, 0, false
	}
	defer rowsIter.Close()
	r := 0
	c := 0
	for rowsIter.Next() {
		r++
		if r == 1 {
			if cells, _ := rowsIter.Columns(); len(cells) > c {
				c = len(cells)
			}
		}
		if r >= xlsxDimensionScanCap {
			return r, c, false // capped estimate
		}
	}
	if e := rowsIter.Error(); e != nil {
		return r, c, false
	}
	return r, c, true
}

// parseDimensionRef parses "A1:K300000" into (maxRow, maxCol, ok).
func parseDimensionRef(ref string) (rows, cols int, ok bool) {
	ref = strings.TrimSpace(strings.Trim(ref, "$"))
	if ref == "" {
		return 0, 0, false
	}
	parts := strings.Split(ref, ":")
	last := parts[len(parts)-1]
	c, r, err := excelize.CellNameToCoordinates(last)
	if err != nil || c < 0 || r < 0 {
		return 0, 0, false
	}
	return r, c, true
}

// resolveColumnIndex maps a column spec to a 0-based column index. It tries:
// (1) header-name match (case-insensitive) — preferred, since column letters
// collide with short header names like "id"/"val" (excelize.ColumnNameToNumber
// silently parses "id" as base-26 → 237);
// (2) a strict column-letter form (1–3 uppercase A-Z) as a fallback.
func resolveColumnIndex(spec string, header []string) (int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return -1, fmt.Errorf("column is required")
	}
	// Header-name match first (case-insensitive).
	for i, h := range header {
		if strings.EqualFold(strings.TrimSpace(h), spec) {
			return i, nil
		}
	}
	// Strict column-letter fallback: 1–3 uppercase A-Z only. This rejects
	// lowercase/word specs that ColumnNameToNumber would mis-parse.
	if isColumnLetter(spec) {
		if n, err := excelize.ColumnNameToNumber(spec); err == nil && n > 0 {
			return n - 1, nil
		}
	}
	return -1, fmt.Errorf("column %q not found (use a header name from row 1 or an uppercase column letter like A/B)", spec)
}

// isColumnLetter reports whether s is a strict Excel column letter (1–3
// uppercase A-Z), e.g. "A", "Z", "AA", "XFD". Rejects lowercase and non-letters
// so "id"/"val"/"Name" don't get mis-parsed as base-26 column indices.
func isColumnLetter(s string) bool {
	if len(s) == 0 || len(s) > 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// cellAt returns cells[i] or "" if out of range.
func cellAt(cells []string, i int) string {
	if i >= 0 && i < len(cells) {
		return cells[i]
	}
	return ""
}

// evalCond evaluates a single where condition.
func evalCond(cellVal, op, target string) bool {
	cellVal = strings.TrimSpace(cellVal)
	target = strings.TrimSpace(target)
	// Numeric comparison if both look numeric.
	cn, cerr := strconv.ParseFloat(cellVal, 64)
	tn, terr := strconv.ParseFloat(target, 64)
	numeric := cerr == nil && terr == nil
	switch op {
	case "=", "==":
		if numeric {
			return cn == tn
		}
		return cellVal == target
	case "!=":
		if numeric {
			return cn != tn
		}
		return cellVal != target
	case ">":
		if numeric {
			return cn > tn
		}
		return cellVal > target
	case ">=":
		if numeric {
			return cn >= tn
		}
		return cellVal >= target
	case "<":
		if numeric {
			return cn < tn
		}
		return cellVal < target
	case "<=":
		if numeric {
			return cn <= tn
		}
		return cellVal <= target
	case "contains":
		return strings.Contains(cellVal, target)
	default:
		return false
	}
}

// compareVals compares two cell values: numerically if both parse, else lexically.
func compareVals(a, b string) int {
	an, aerr := strconv.ParseFloat(a, 64)
	bn, berr := strconv.ParseFloat(b, 64)
	if aerr == nil && berr == nil {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	}
	return strings.Compare(a, b)
}

// rowsLabel renders a row count for the page trailer ("300000" or ">200000 (estimate)").
func rowsLabel(total int) string {
	if total <= 0 {
		return "?"
	}
	return strconv.Itoa(total)
}

// normalizeNumber drops a trailing .0 from integer-valued floats for readability.
func normalizeNumber(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasSuffix(s, ".0") {
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return s[:len(s)-2]
		}
	}
	return s
}

// --- docx (stdlib zip+xml text extraction) ----------------------------------

// readDOCX extracts text from word/document.xml, joining paragraphs with newlines.
func readDOCX(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx (is it a valid .docx?): %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return "", err
		}
		return parseDOCXText(data), nil
	}
	return "", fmt.Errorf("docx has no word/document.xml")
}

// parseDOCXText walks the document XML collecting <w:t> runs, breaking at
// paragraph boundaries (<w:p>). Tabs and breaks are approximated.
func parseDOCXText(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	var inPara bool
	var inTable bool
	var firstCellInRow bool
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tbl":
				inTable = true
			case "tr":
				b.WriteByte('\n')
				firstCellInRow = true
			case "tc":
				if !firstCellInRow {
					b.WriteString(" | ")
				}
				firstCellInRow = false
			case "p":
				if inPara {
					if inTable {
						b.WriteByte(' ')
					} else {
						b.WriteByte('\n')
					}
				}
				inPara = true
			case "t":
				var txt string
				if err := dec.DecodeElement(&txt, &t); err == nil {
					b.WriteString(txt)
				}
			case "tab":
				b.WriteByte('\t')
			case "br":
				if inTable {
					b.WriteByte(' ')
				} else {
					b.WriteByte('\n')
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p":
				if !inTable {
					b.WriteByte('\n')
				}
				inPara = false
			case "tbl":
				b.WriteString("\n\n")
				inTable = false
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// --- pptx (stdlib zip+xml text extraction, best-effort) ---------------------

// readPPTX extracts text from each slide (slideN.xml <a:t> runs), one block
// per slide labeled [slide N].
func readPPTX(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open pptx (is it a valid .pptx?): %w", err)
	}
	defer zr.Close()
	var slideFiles []string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slideFiles = append(slideFiles, f.Name)
		}
	}
	sort.Slice(slideFiles, func(i, j int) bool {
		ni, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(slideFiles[i]), "slide"), ".xml"))
		nj, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(slideFiles[j]), "slide"), ".xml"))
		return ni < nj
	})
	var b strings.Builder
	for _, name := range slideFiles {
		for _, ff := range zr.File {
			if ff.Name != name {
				continue
			}
			rc, err := ff.Open()
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			fmt.Fprintf(&b, "[slide %s]\n", filepath.Base(name))
			b.WriteString(parseSlideText(data))
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String()), nil
}

// parseSlideText collects all <a:t> run text from a slide XML, space-joined.
func parseSlideText(data []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "t" {
			var txt string
			if err := dec.DecodeElement(&txt, &se); err == nil {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(txt)
			}
		}
	}
	return b.String()
}

// formatRows renders a [][]string grid as an aligned table (same style as CSV read).
func formatRows(rows [][]string) string {
	if len(rows) == 0 {
		return "(empty)"
	}
	widths := make([]int, 0)
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, len(cell))
			} else if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	var b strings.Builder
	for _, row := range rows {
		for i, cell := range row {
			pad := 0
			if i < len(widths) {
				pad = widths[i] - len(cell)
			}
			b.WriteString(cell)
			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", pad+2))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- exported workbook → LLM table (download analysis) ----------------------

// ReadWorkbookAsTable reads an exported workbook (.xlsx first sheet, or .csv)
// as a markdown pipe table for LLM alert triage. Streaming row iterator with
// a maxRows cap — a 50k-row SIEM export never materializes whole, the model
// gets the first maxRows rows plus the real total so truncation is explicit.
// Cells are clipped to cellClipRunes runes (wide log-message columns would
// otherwise drown the table). Legacy .xls is rejected with a hint.
func ReadWorkbookAsTable(path string, maxRows int) (string, int, error) {
	if maxRows <= 0 {
		maxRows = 1000
	}
	ext := strings.ToLower(filepath.Ext(path))
	var rows [][]string
	var total int
	switch ext {
	case ".xlsx":
		f, err := excelize.OpenFile(path)
		if err != nil {
			return "", 0, fmt.Errorf("open xlsx (is it a valid .xlsx?): %w", err)
		}
		defer f.Close()
		sheets := f.GetSheetList()
		if len(sheets) == 0 {
			return "", 0, fmt.Errorf("xlsx has no sheets")
		}
		it, err := f.Rows(sheets[0])
		if err != nil {
			return "", 0, fmt.Errorf("read sheet %q: %w", sheets[0], err)
		}
		defer it.Close()
		for it.Next() {
			total++
			if len(rows) < maxRows+1 { // +1: header row doesn't count against the cap
				cols, _ := it.Columns()
				if cols == nil {
					cols = []string{}
				}
				rows = append(rows, cols)
			}
		}
		if e := it.Error(); e != nil {
			return "", 0, fmt.Errorf("scan sheet %q: %w", sheets[0], e)
		}
	case ".csv":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", 0, err
		}
		r := csv.NewReader(bytes.NewReader(data))
		r.FieldsPerRecord = -1
		r.LazyQuotes = true
		for {
			rec, rerr := r.Read()
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				return "", 0, fmt.Errorf("read csv: %w", rerr)
			}
			total++
			if len(rows) < maxRows+1 {
				rows = append(rows, rec)
			}
		}
	default:
		return "", 0, fmt.Errorf("不支持的导出格式 %q——请在平台上改导出 .xlsx 或 .csv", ext)
	}
	if len(rows) == 0 {
		return "(文件没有数据行)", total, nil
	}
	// Rectangular pad so the pipe table renders.
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	var b strings.Builder
	for ri, r := range rows {
		for i := 0; i < width; i++ {
			cell := ""
			if i < len(r) {
				cell = normalizeNumber(strings.TrimSpace(r[i]))
			}
			b.WriteString("| ")
			b.WriteString(clipTableCell(cell))
			b.WriteString(" ")
		}
		b.WriteString("|\n")
		if ri == 0 {
			for i := 0; i < width; i++ {
				b.WriteString("| --- ")
			}
			b.WriteString("|\n")
		}
	}
	shown := len(rows) - 1
	if shown < 0 {
		shown = 0
	}
	if total > shown+1 {
		b.WriteString(fmt.Sprintf("\n(共 %d 行数据，受分析上限仅显示前 %d 行；结论请注明基于部分数据)\n", total-1, shown))
	}
	return b.String(), total - 1, nil
}

// cellClipRunes bounds one table cell for the LLM prompt.
const cellClipRunes = 120

func clipTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	if r := []rune(s); len(r) > cellClipRunes {
		return string(r[:cellClipRunes]) + "…"
	}
	return s
}
