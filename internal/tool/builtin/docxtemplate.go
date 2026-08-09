package builtin

// docxtemplate.go implements the doc_template tool: read a .docx template,
// fill placeholders / table cells / header-footer, and write a NEW file
// (the template is never modified). This is the office-assistant's core
// workflow: a user has a contract/report template and asks the agent to fill
// in the names, dates, amounts, and table data.
//
// Design (absorbs the v3 plan + the audit findings):
//   - source is READ-ONLY and confined to the workspace; source != path is
//     enforced so a crash can never overwrite the template.
//   - find_replace uses a Unicode-aware placeholder regex ({{甲方}}, {{a.b}},
//     {{items[0]}}) and handles placeholders that span multiple <w:r> runs by
//     splitting the boundary runs (SplitRuns, mirroring OfficeCLI's
//     WordHandler.Helpers.FindReplace). Replacement values are NOT re-scanned
//     for placeholders (avoids the classic cascade bug).
//   - table_fill builds a grid model (gridSpan + vMerge) so row/col indices
//     resolve correctly through merged cells; filling a vMerge-continuation
//     cell returns ErrMergedCell pointing at the merge start.
//   - header/footer resolves rId → file via word/_rels/document.xml.rels
//     (the v3 plan's "just read document.xml" approach can't locate them).
//   - Writes are crash-atomic via atomicWrite (Phase 0); best-effort mode
//     collects failures as warnings instead of aborting the whole fill.
//
// The OOXML walk is a custom xml.Decoder streaming pass rather than a DOM
// library — we only need to splice text runs and tc shading, and a streaming
// re-emit keeps memory bounded for large templates.

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// --- input shapes ----------------------------------------------------------

type findReplacePair struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

type tableFillOp struct {
	Table int      `json:"table"`
	Row   int      `json:"row"`
	Col   int      `json:"col"`
	Value string   `json:"value"`
	Style DocStyle `json:"style"`
}

// paragraphReplaceOp replaces the text of the Nth <w:p> in word/document.xml.
// The index matches doc_read's structure output (block index), so the LLM can
// say "block[11] is the 总体介绍 prompt, replace it with this content" without
// needing to match the exact source text (which may be split across runs or
// use different encoding). This is far faster and more reliable than find_replace
// for long body text.
type paragraphReplaceOp struct {
	Index int    `json:"index"` // 0-based paragraph index in document order (matches doc_read blocks)
	Text  string `json:"text"`  // the new text for this paragraph
}

type headerFooterSpec struct {
	Text  string `json:"text"`
	Align string `json:"align"`
}

type fillJob struct {
	findReplace      []findReplacePair
	tableFill        []tableFillOp
	paragraphReplace []paragraphReplaceOp
	header           *headerFooterSpec
	footer           *headerFooterSpec
}

type fillResult struct {
	warnings []DocError
}

func (r *fillResult) warn(e DocError) { r.warnings = append(r.warnings, e) }

// --- the fill pipeline -----------------------------------------------------

// fillDocxTemplate runs the full fill job: open source, rewrite each text
// part, write dst atomically. Parts not touched are copied verbatim so the
// output preserves the template's styles/numbering/media/relationships.
func fillDocxTemplate(src, dst string, job fillJob) (fillResult, error) {
	res := fillResult{}

	zr, err := zip.OpenReader(src)
	if err != nil {
		return res, DocError{Code: ErrCorruptFile, Message: fmt.Sprintf("open template: %v", err)}
	}
	defer zr.Close()

	// Read every part into memory. Templates are small (the bomb guard already
	// capped total size at 2GB, real office docs are a few MB). This lets us
	// rewrite parts in any order and re-emit a clean zip.
	parts := make(map[string][]byte, len(zr.File))
	order := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return res, fmt.Errorf("read part %s: %w", f.Name, err)
		}
		data, err := readAllClose(rc)
		if err != nil {
			return res, fmt.Errorf("read part %s: %w", f.Name, err)
		}
		parts[f.Name] = stripBOM(data)
		order = append(order, f.Name)
	}

	// find_replace applies to every text-bearing part (body + headers/footers
	// + footnotes/endnotes/comments). A placeholder in a header should be
	// filled just like one in the body.
	if len(job.findReplace) > 0 {
		for name, data := range parts {
			if !isXMLTextPart(name) {
				continue
			}
			parts[name], res.warnings = applyFindReplace(data, job.findReplace, res.warnings, name)
		}
	}

	// table_fill targets the body's tables.
	if len(job.tableFill) > 0 {
		body, ok := parts["word/document.xml"]
		if !ok {
			res.warn(DocError{Code: ErrCorruptFile, Message: "template has no word/document.xml"})
		} else {
			parts["word/document.xml"], res.warnings = applyTableFill(body, job.tableFill, res.warnings)
		}
	}

	// paragraph_replace: replace the Nth <w:p>'s text by index. Much faster and
	// more reliable than find_replace for long body text — no need to match the
	// exact source string. The index matches doc_read's structure blocks.
	if len(job.paragraphReplace) > 0 {
		body, ok := parts["word/document.xml"]
		if !ok {
			res.warn(DocError{Code: ErrCorruptFile, Message: "template has no word/document.xml"})
		} else {
			parts["word/document.xml"] = applyParagraphReplace(body, job.paragraphReplace)
		}
	}

	// header/footer: resolve rId → filename via .rels, then rewrite those parts.
	if job.header != nil || job.footer != nil {
		body := parts["word/document.xml"]
		rels := parts["word/_rels/document.xml.rels"]
		parts, res.warnings = applyHeaderFooter(parts, body, rels, job.header, job.footer, res.warnings)
	}

	// Atomic write: serialize all parts to a temp zip, then rename. A crash
	// leaves the template (source) intact and at most abandons the temp.
	if err := atomicWrite(dst, func(tmp *os.File) error {
		return writeZipParts(tmp, order, parts)
	}); err != nil {
		return res, err
	}
	return res, nil
}

// isXMLTextPart reports whether a part carries user-visible text that
// find_replace should touch. We're conservative: only the well-known word/*
// prose parts. Styles/numbering/settings/themes carry markup, not prose.
func isXMLTextPart(name string) bool {
	switch {
	case name == "word/document.xml":
		return true
	case strings.HasPrefix(name, "word/header"):
		return true
	case strings.HasPrefix(name, "word/footer"):
		return true
	case strings.HasPrefix(name, "word/footnotes"):
		return true
	case strings.HasPrefix(name, "word/endnotes"):
		return true
	case strings.HasPrefix(name, "word/comments"):
		return true
	}
	return false
}

// readAllClose reads all bytes then closes, ignoring close errors.
func readAllClose(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}

// writeZipParts re-emit a zip with the given part order and (possibly modified)
// contents. Compression level left at the default; template fidelity matters
// more than output size.
func writeZipParts(dst *os.File, order []string, parts map[string][]byte) error {
	zw := zip.NewWriter(dst)
	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write(parts[name]); err != nil {
			return err
		}
	}
	return zw.Close()
}

// defaultFilledPath generates the default output path for a doc_template fill
// when the caller omits path: the source's name with "-filled" inserted before
// the extension, in the same directory. e.g. "C:\docs\申报书.docx" →
// "C:\docs\申报书-filled.docx". If that name already exists, append "-2", "-3",
// ... until a free name is found (best-effort; the actual write is still
// atomic, so a race only costs the suffix).
func defaultFilledPath(src string) string {
	dir := filepath.Dir(src)
	ext := filepath.Ext(src)
	base := strings.TrimSuffix(filepath.Base(src), ext)
	for n := 0; n <= 100; n++ {
		var name string
		if n == 0 {
			name = base + "-filled"
		} else {
			name = fmt.Sprintf("%s-filled-%d", base, n+1)
		}
		cand := filepath.Join(dir, name+ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return filepath.Join(dir, base+"-filled"+ext)
}

// applyParagraphReplace replaces the text content of specific <w:p> elements
// by their document-order index. The index matches doc_read's structure blocks
// (0-based, counting every <w:p> in the body, NOT counting <w:tbl> blocks).
//
// This is the preferred way to fill long body text: the LLM reads the structure
// (which gives each paragraph an index), then says "paragraph[11] = new text"
// without needing to match the exact source string. It avoids the cross-run
// matching problem entirely and is O(N) in the number of paragraphs.
//
// The replacement preserves the paragraph's <w:pPr> (style/indent/spacing) and
// the first run's <w:rPr> (font/size/bold). Only the <w:t> text changes.
func applyParagraphReplace(body []byte, ops []paragraphReplaceOp) []byte {
	s := string(body)
	// Build a map of index → new text for O(1) lookup.
	replacements := make(map[int]string, len(ops))
	for _, op := range ops {
		replacements[op.Index] = op.Text
	}

	var out strings.Builder
	out.Grow(len(s))
	paraIdx := -1 // incremented for each top-level <w:p>
	tableDepth := 0
	i := 0
	for i < len(s) {
		if s[i] == '<' {
			if strings.HasPrefix(s[i:], "<w:tbl>") || strings.HasPrefix(s[i:], "<w:tbl ") || strings.HasPrefix(s[i:], "<w:tbl/>") {
				tableDepth++
			} else if strings.HasPrefix(s[i:], "</w:tbl>") {
				if tableDepth > 0 {
					tableDepth--
				}
			} else if strings.HasPrefix(s[i:], "<w:p") {
				// Check it's a real <w:p> not <w:pPr>.
				afterTag := s[i+4:]
				if len(afterTag) > 0 && (afterTag[0] == '>' || afterTag[0] == ' ') {
					if tableDepth == 0 {
						paraIdx++
						// Is this paragraph targeted for replacement?
						newText, targeted := replacements[paraIdx]
						if targeted {
							// Find </w:p> and replace the entire paragraph's text content.
							pClose := strings.Index(s[i:], "</w:p>")
							if pClose < 0 {
								out.WriteByte(s[i])
								i++
								continue
							}
							pEnd := i + pClose + len("</w:p>")
							// Extract the paragraph, rewrite its text.
							para := s[i:pEnd]
							newPara := replaceParagraphText(para, newText)
							out.WriteString(newPara)
							i = pEnd
							continue
						}
					}
				}
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return []byte(out.String())
}

// replaceParagraphText rewrites a <w:p>...</w:p> fragment so its visible text
// becomes newText. It preserves <w:pPr> (paragraph properties) and the first
// <w:r>'s <w:rPr> (run properties — font/size/bold). All existing runs are
// replaced by a single run carrying the new text (with \n → <w:br/> expansion).
func replaceParagraphText(para, newText string) string {
	// Extract pPr if present (everything from <w:pPr> to </w:pPr>).
	pPr := ""
	ppStart := strings.Index(para, "<w:pPr")
	if ppStart >= 0 {
		ppEnd := strings.Index(para[ppStart:], "</w:pPr>")
		if ppEnd >= 0 {
			pPr = para[ppStart : ppStart+ppEnd+len("</w:pPr>")]
		}
	}

	// Extract the first run's rPr for format inheritance.
	rPr := ""
	rPrStart := strings.Index(para, "<w:rPr>")
	if rPrStart < 0 {
		rPrStart = strings.Index(para, "<w:rPr ")
	}
	if rPrStart >= 0 {
		rPrEnd := strings.Index(para[rPrStart:], "</w:rPr>")
		if rPrEnd >= 0 {
			rPr = para[rPrStart : rPrStart+rPrEnd+len("</w:rPr>")]
		}
	}

	// Find the <w:p ...> opening tag end (where properties + runs begin).
	openEnd := strings.Index(para, ">")
	if openEnd < 0 {
		return para // malformed
	}

	// Build the new paragraph: original opening tag + pPr + single run with new text.
	var b strings.Builder
	b.WriteString(para[:openEnd+1]) // <w:p ...>
	if pPr != "" {
		b.WriteString(pPr)
	}
	// Single run with rPr (if any) + text (with \n → <w:br/> and \t → <w:tab/>).
	b.WriteString("<w:r>")
	if rPr != "" {
		b.WriteString(rPr)
	}
	// Expand newlines/tabs in the text.
	lines := strings.Split(newText, "\n")
	for li, line := range lines {
		if li > 0 {
			b.WriteString("<w:br/>")
		}
		segments := strings.Split(line, "\t")
		for si, seg := range segments {
			if si > 0 {
				b.WriteString("<w:tab/>")
			}
			b.WriteString(`<w:t xml:space="preserve">`)
			b.WriteString(xmlEscapeText(seg))
			b.WriteString(`</w:t>`)
		}
	}
	b.WriteString("</w:r>")
	b.WriteString("</w:p>")
	return b.String()
}
