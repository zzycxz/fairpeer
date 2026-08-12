package builtin

// docxtemplate_tablefill.go: table cell filling for doc_write's template-fill table_fill.
//
// STRING-LEVEL surgery (same rationale as find_replace: Go's encoding/xml
// normalizes namespace prefix on re-encode, which breaks OOXML — a re-encoded
// <w:tbl> comes back as <tbl xmlns="..."> and Word won't open it). We locate
// each target <w:tc> by counting table/row/cell open tags in the raw bytes,
// then replace the first <w:t>...</w:t> inside that tc with the fill value.
//
// Coordinate system: col indexes PHYSICAL <w:tc> in document order — the SAME
// coordinate system doc_read emits in its Rows arrays. A gridSpan-merged cell
// is one <w:tc>, so doc_read and table_fill agree on what (row, col) means. A
// vMerge-continuation cell (rendered empty by doc_read, visually hidden under
// the merge start) is rejected with ErrMergedCell so the agent fills the
// visible top cell instead.

import (
	"bytes"
	"fmt"
	"strings"
)

// applyTableFill applies all table_fill ops to the body XML (string-level).
func applyTableFill(body []byte, ops []tableFillOp, warnSink []DocError) ([]byte, []DocError) {
	grids, err := buildAllGrids(body)
	if err != nil {
		return body, append(warnSink, DocError{Code: ErrCorruptFile, Message: "parse tables: " + err.Error()})
	}

	type target struct {
		table, physRow, physCol int
		value                   string
		style                   DocStyle
	}
	var targets []target
	for _, op := range ops {
		if op.Table < 0 || op.Table >= len(grids) {
			warnSink = append(warnSink, DocError{Code: ErrTableIndexOOB,
				Message:    fmt.Sprintf("table index %d out of range (0..%d)", op.Table, len(grids)-1),
				Suggestion: "use doc_read to list table indices"})
			continue
		}
		g := grids[op.Table]
		if op.Row < 0 || op.Row >= g.rows {
			warnSink = append(warnSink, DocError{Code: ErrRowIndexOOB,
				Message: fmt.Sprintf("table %d row %d out of range (0..%d)", op.Table, op.Row, g.rows-1)})
			continue
		}
		// col indexes PHYSICAL <w:tc> (same coordinate system doc_read emits in
		// its Rows array). The row's physical cell count is the bound, NOT g.cols
		// (which is the visual grid width — larger when gridSpan merges exist).
		rowCells := 0
		if op.Row < len(g.rowCellCounts) {
			rowCells = g.rowCellCounts[op.Row]
		}
		if op.Col < 0 || op.Col >= rowCells {
			warnSink = append(warnSink, DocError{Code: ErrColIndexOOB,
				Message: fmt.Sprintf("table %d row %d col %d out of range (0..%d)", op.Table, op.Row, op.Col, rowCells-1)})
			continue
		}
		// vMerge-continuation guard: a tc with <w:vMerge/> (no val="restart")
		// renders as an empty cell in doc_read but is visually swallowed by the
		// merge-start cell above it — anything written here is invisible. Warn
		// and skip so the agent targets the visible merge-start cell instead.
		if op.Row < len(g.rowVMergeCont) && op.Col < len(g.rowVMergeCont[op.Row]) && g.rowVMergeCont[op.Row][op.Col] {
			warnSink = append(warnSink, DocError{Code: ErrMergedCell,
				Message:    fmt.Sprintf("table %d [%d,%d] is a vertical-merge continuation; its content is hidden under the merge-start cell above", op.Table, op.Row, op.Col),
				Suggestion: "fill the top cell of the merged column instead"})
			continue
		}
		targets = append(targets, target{table: op.Table, physRow: op.Row, physCol: op.Col, value: op.Value, style: op.Style})
	}
	if len(targets) == 0 {
		return body, warnSink
	}

	// Walk the body; when we reach a target tc, rewrite its first <w:t>.
	result := body
	tableDepth := 0
	curTable := -1
	curRow := -1
	curCol := -1
	done := make(map[int]bool) // targets index → filled, so we don't re-fill
	i := 0
	for i < len(result) {
		if result[i] == '<' {
			adv, kind, isClose, isSelfClose := matchTag(result, i)
			if adv > 0 {
				if !isClose {
					switch kind {
					case "tbl":
						if !isSelfClose {
							tableDepth++
						}
						if tableDepth == 1 {
							curTable++
							curRow = -1
						}
					case "tr":
						if tableDepth == 1 {
							curRow++
							curCol = -1
						}
					case "tc":
						if tableDepth == 1 {
							curCol++
							matched := -1
							for k, tgt := range targets {
								if done[k] {
									continue
								}
								if tgt.table == curTable && tgt.physRow == curRow && tgt.physCol == curCol {
									matched = k
								}
							}
							if matched >= 0 {
								tcStart := i
								tcEnd := findTCEnd(result, i+adv)
								tgt := targets[matched]
								newBytes, ok := rewriteTC(result[tcStart:tcEnd], tgt.value, tgt.style)
								if ok {
									result = spliceBytes(result, tcStart, tcEnd, newBytes)
									done[matched] = true
									// Advance past the rewritten tc.
									newTCEnd := bytes.Index(newBytes, []byte("</w:tc>"))
									if newTCEnd >= 0 {
										i = tcStart + newTCEnd + len("</w:tc>")
									} else {
										i = tcStart + len(newBytes)
									}
									continue
								}
							}
						}
					}
				} else if kind == "tbl" && tableDepth >= 1 {
					tableDepth--
				}
				i += adv
				continue
			}
		}
		i++
	}
	return result, warnSink
}

// matchTag inspects result at pos (must be '<'). Returns (byteLength, localName
// without namespace prefix, isClosingTag). (0,"",false) if not a recognized tag.
// Handles self-closing tags (<w:gridCol/>) — they report as a non-closing tag
// of the local name (e.g. "gridCol") so callers counting opens don't need a
// matching close.
func matchTag(result []byte, pos int) (adv int, local string, isClose bool, isSelfClose bool) {
	end := bytes.IndexByte(result[pos:], '>')
	if end < 0 {
		return 0, "", false, false
	}
	adv = end + 1
	inner := string(result[pos+1 : pos+end])
	selfClosing := strings.HasSuffix(inner, "/")
	if selfClosing {
		inner = strings.TrimSuffix(inner, "/")
	}
	tag := strings.TrimSpace(inner)
	if strings.HasPrefix(tag, "/") {
		isClose = true
		tag = tag[1:]
	}
	if sp := strings.IndexByte(tag, ' '); sp >= 0 {
		tag = tag[:sp]
	}
	if colon := strings.Index(tag, ":"); colon >= 0 {
		tag = tag[colon+1:]
	}
	return adv, tag, isClose, selfClosing
}

// findTCEnd returns the byte index just past the closing </w:tc> for the tc
// whose opening tag ended at tagEnd. Handles nested tcs.
func findTCEnd(result []byte, tagEnd int) int {
	depth := 1
	i := tagEnd
	for i < len(result) {
		if result[i] == '<' {
			if adv, kind, isClose, isSelfClose := matchTag(result, i); adv > 0 {
				if kind == "tc" {
					if isClose {
						depth--
						if depth == 0 {
							return i + adv
						}
					} else if !isSelfClose {
						depth++
					}
				}
				i += adv
				continue
			}
		}
		i++
	}
	return len(result)
}

// rewriteTC robustly fills a table cell with a new value.
// - If the cell has top-level <w:t> tags, it replaces the FIRST one's content
//   with the value, and EMPTIES the content of all subsequent <w:t> tags. This
//   prevents leftover placeholder text from breaking layout.
// - If the cell has ZERO <w:t> tags (a completely empty cell), it injects a
//   fresh <w:r><w:t>VALUE</w:t></w:r> right before the cell's last </w:p>.
func rewriteTC(tcFragment []byte, value string, style DocStyle) ([]byte, bool) {
	hasT := false
	tblDepth := 0
	i := 0
	for i < len(tcFragment) {
		if tcFragment[i] == '<' {
			adv, kind, isClose, _ := matchTag(tcFragment, i)
			if adv > 0 {
				if kind == "tbl" {
					if !isClose {
						tblDepth++
					} else {
						tblDepth--
					}
				} else if kind == "t" && tblDepth == 0 && !isClose {
					hasT = true
					break
				}
				i += adv
				continue
			}
		}
		i++
	}

	var result []byte

	if hasT {
		var out bytes.Buffer
		tblDepth = 0
		tCount := 0
		i = 0
		for i < len(tcFragment) {
			if tcFragment[i] == '<' {
				adv, kind, isClose, isSelfClose := matchTag(tcFragment, i)
				if adv > 0 {
					if kind == "tbl" {
						if !isClose {
							tblDepth++
						} else {
							tblDepth--
						}
					} else if kind == "t" && tblDepth == 0 && !isClose {
						tCount++
						if isSelfClose {
							openTag := bytes.Replace(tcFragment[i:i+adv], []byte("/>"), []byte(">"), 1)
							out.Write(openTag)
							if tCount == 1 {
								out.WriteString(xmlEscapeText(value))
							}
							out.WriteString("</w:t>")
							i += adv
							continue
						} else {
							tEnd := bytes.Index(tcFragment[i+adv:], []byte("</w:t>"))
							if tEnd >= 0 {
								tEnd += i + adv
								out.Write(tcFragment[i : i+adv])
								if tCount == 1 {
									out.WriteString(xmlEscapeText(value))
								}
								out.WriteString("</w:t>")
								i = tEnd + len("</w:t>")
								continue
							}
						}
					}
					out.Write(tcFragment[i : i+adv])
					i += adv
					continue
				}
			}
			out.WriteByte(tcFragment[i])
			i++
		}
		result = out.Bytes()
	} else {
		// No top-level <w:t>. Inject <w:r><w:t> before the last </w:p>
		lastP := bytes.LastIndex(tcFragment, []byte("</w:p>"))
		if lastP < 0 {
			lastP = bytes.LastIndex(tcFragment, []byte("</w:tc>"))
		}
		if lastP < 0 {
			return nil, false
		}
		var out bytes.Buffer
		out.Write(tcFragment[:lastP])
		out.WriteString("<w:r><w:t>")
		out.WriteString(xmlEscapeText(value))
		out.WriteString("</w:t></w:r>")
		out.Write(tcFragment[lastP:])
		result = out.Bytes()
	}

	if !styleEmptyForTC(style) {
		result = applyRunStyle(result, style)
	}
	return result, true
}

// styleEmptyForTC is a local emptiness check for the subset of DocStyle fields
// table_fill honors at the run level (bold/italic/color/size/font/underline).
func styleEmptyForTC(s DocStyle) bool {
	return !s.Bold && !s.Italic && s.Color == "" && s.Size == 0 &&
		s.Font == ""
}

// applyRunStyle injects or replaces the <w:rPr> in the first <w:r> of the
// fragment. It locates "<w:r" → its ">", then either replaces an existing
// <w:rPr>...</w:rPr> or inserts a fresh one right after the <w:r> open tag.
func applyRunStyle(fragment []byte, style DocStyle) []byte {
	rPr := runPropsXML(style) // reuse docxwrite's rPr builder (handles size/underline/etc.)
	if rPr == "" {
		return fragment
	}
	rStart := bytes.Index(fragment, []byte("<w:r"))
	if rStart < 0 {
		return fragment
	}
	openEnd := bytes.IndexByte(fragment[rStart:], '>')
	if openEnd < 0 {
		return fragment
	}
	insertAt := rStart + openEnd + 1
	// Is there already a <w:rPr> right after <w:r>?
	if bytes.HasPrefix(fragment[insertAt:], []byte("<w:rPr")) {
		adv, kind, _, _ := matchTag(fragment, insertAt)
		if kind == "rPr" && adv > 0 {
			if fragment[insertAt+adv-2] == '/' {
				// Self-closing <w:rPr/>
				var out bytes.Buffer
				out.Write(fragment[:insertAt])
				out.WriteString(rPr)
				out.Write(fragment[insertAt+adv:])
				return out.Bytes()
			}
			// Standard <w:rPr>...</w:rPr>
			rPrEnd := bytes.Index(fragment[insertAt:], []byte("</w:rPr>"))
			if rPrEnd >= 0 {
				rPrEnd += insertAt + len("</w:rPr>")
				var out bytes.Buffer
				out.Write(fragment[:insertAt])
				out.WriteString(rPr)
				out.Write(fragment[rPrEnd:])
				return out.Bytes()
			}
		}
	}
	// No existing rPr: insert the new one.
	var out bytes.Buffer
	out.Write(fragment[:insertAt])
	out.WriteString(rPr)
	out.Write(fragment[insertAt:])
	return out.Bytes()
}

func spliceBytes(result []byte, start, end int, repl []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(result) - (end - start) + len(repl))
	out.Write(result[:start])
	out.Write(repl)
	out.Write(result[end:])
	return out.Bytes()
}

// --- table model (read-only pre-pass: per-row physical cell count + vMerge flags) ---

type gridModel struct {
	rows           int        // number of <w:tr> in this table
	cols           int        // visual grid width (max gridSpan sum across rows) — used only for bounds on row
	rowCellCounts  []int      // rowCellCounts[r] = number of physical <w:tc> in row r
	rowVMergeCont  [][]bool   // rowVMergeCont[r][c] = true if cell c in row r is a vMerge continuation
}

// buildAllGrids parses body to construct a gridModel per top-level table.
//
// Coordinate-system contract (IMPORTANT — must match doc_read):
//   - col indexes PHYSICAL <w:tc> elements in document order, exactly as
//     doc_read emits them in its Rows array. A gridSpan-merged cell (one <w:tc>
//     spanning N visual columns) counts as ONE physical cell, so doc_read and
//     table_fill agree on what (row, col) means.
//   - rowCellCounts gives the per-row bound (rows with merges have FEWER
//     physical cells than the visual width).
//   - rowVMergeCont flags cells that are vertically merged into the row above
//     (<w:vMerge/> without val="restart"). doc_read still emits them as empty
//     strings, but writes there are invisible — applyTableFill skips them with
//     an ErrMergedCell warning so the agent targets the visible top cell.
func buildAllGrids(body []byte) ([]gridModel, error) {
	var models []gridModel
	s := string(body)
	i := 0
	tableDepth := 0

	var cur *gridModel
	var gridColCount int // from tblGrid
	curRow := -1
	hasTblGrid := false

	// per-row scratch, flushed at </w:tr>
	var curRowCellCount int
	var curRowVMerge []bool
	var curTCHasVMerge bool // tracks <w:vMerge .../> inside the current tcPr

	resetTable := func() {
		gridColCount = 0
		hasTblGrid = false
		curRow = -1
		curRowCellCount = 0
		curRowVMerge = nil
	}

	for i < len(s) {
		adv, kind, isClose, isSelfClose, attr := scanTag(s, i)
		if adv == 0 {
			i++
			continue
		}
		if !isClose {
			switch kind {
			case "tbl":
				if !isSelfClose {
					tableDepth++
				}
				if tableDepth == 1 {
					cur = &gridModel{}
					resetTable()
				}
			case "tblGrid":
				if tableDepth == 1 {
					hasTblGrid = true
				}
			case "gridCol":
				if tableDepth == 1 && hasTblGrid {
					gridColCount++
				}
			case "tr":
				if tableDepth == 1 {
					curRow++
					curRowCellCount = 0
					curRowVMerge = curRowVMerge[:0]
				}
			case "tc":
				if tableDepth == 1 && curRow >= 0 {
					curRowCellCount++
					curTCHasVMerge = false
				}
			case "vMerge":
				// <w:vMerge/> (continuation) or <w:vMerge w:val="restart"/> (start).
				if tableDepth == 1 && curRow >= 0 {
					val := attrVal(attr, "val")
					if val != "restart" {
						curTCHasVMerge = true
					}
				}
			}
		} else {
			switch kind {
			case "tc":
				if tableDepth == 1 && curRow >= 0 {
					curRowVMerge = append(curRowVMerge, curTCHasVMerge)
				}
			case "tr":
				if tableDepth == 1 && curRow >= 0 {
					cur.rowCellCounts = append(cur.rowCellCounts, curRowCellCount)
					cur.rowVMergeCont = append(cur.rowVMergeCont, append([]bool(nil), curRowVMerge...))
				}
			case "tbl":
				if tableDepth >= 1 {
					tableDepth--
					if tableDepth == 0 && cur != nil {
						cols := gridColCount
						if cols == 0 {
							for _, cc := range cur.rowCellCounts {
								if cc > cols {
									cols = cc
								}
							}
						}
						cur.cols = cols
						cur.rows = len(cur.rowCellCounts)
						models = append(models, *cur)
						cur = nil
					}
				}
			}
		}
		i += adv
	}
	return models, nil
}

// (table_fill applies both the value and an optional style: when style is
// non-empty, applyRunStyle injects/replaces the cell's <w:rPr> using the same
// runPropsXML builder as doc_write, so bold/color/size/font/underline land on
// the filled cell.)
