package builtin

// docxtemplate_findreplace.go: cross-run text substitution for doc_template's
// find_replace.
//
// PROBLEM: Word splits a paragraph's text across multiple <w:r><w:t> elements.
// A user-visible string like "some visible text" may serialize as:
//   <w:r><w:t>some </w:t></w:r><w:r><w:t>visible</w:t></w:r><w:r><w:t> text</w:t></w:r>
// A naive bytes.Contains on the raw XML misses it — the text never appears
// contiguously because of the intervening tags.
//
// SOLUTION (mirrors OfficeCLI's BuildRunTexts approach):
// For each paragraph, we build a flat text view by extracting every <w:t>'s
// text content in document order, with an offset map (flat-offset → byte
// range in the original XML). We search the flat text for the find-string.
// On a hit, we splice the replacement into the FIRST overlapping <w:t> and
// blank the text in the remaining overlapping <w:t> elements, preserving
// the original <w:rPr> (formatting inheritance). All edits are byte-level
// surgery on the raw XML — no decode/re-encode, so namespace prefixes and
// every other byte of the document are preserved exactly.
//
// SAFETY: replacement values are XML-escaped (xmlEscapeText) so they can't
// break out of <w:t>. They are NOT re-scanned for find patterns (no cascade).

import (
	"bytes"
	"strings"
)

// tSpan maps a slice of the flat paragraph text back to the raw XML bytes of
// the <w:t> element it came from. xmlStart/xmlEnd are byte offsets in the
// paragraph's raw XML (the full element, <w:t>...</w:t>); textStart/textEnd
// are offsets within that element's text content (between > and </w:t>).
type tSpan struct {
	xmlStart  int // offset in the paragraph XML where <w:t...> begins
	xmlEnd    int // offset where </w:t> ends
	textStart int // offset in flat text where this <w:t>'s content begins
	textEnd   int // offset in flat text where it ends
}

// applyFindReplace runs all pairs over one XML part. Unmatched finds append
// warnings (non-fatal — the doc still writes).
func applyFindReplace(data []byte, pairs []findReplacePair, warnSink []DocError, partName string) ([]byte, []DocError) {
	for _, p := range pairs {
		var replaced bool
		data, replaced = replaceLiteral(data, p.Find, p.Replace)
		if !replaced {
			// Try the wrapped form (caller passed a bare key "name").
			if w := wrapAsPlaceholder(p.Find); w != p.Find {
				data, replaced = replaceLiteral(data, w, p.Replace)
			}
		}
		if !replaced {
			warnSink = append(warnSink, DocError{
				Code:    ErrPlaceholderNF,
				Message: partName + ": " + p.Find + " not found",
			})
		}
	}
	return data, warnSink
}

// wrapAsPlaceholder turns a bare key into {{key}} if it isn't already wrapped.
func wrapAsPlaceholder(s string) string {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "{{") && strings.HasSuffix(t, "}}") {
		return s
	}
	return "{{" + t + "}}"
}

// replaceLiteral scans data paragraph-by-paragraph, performing cross-run
// replacement of `find` with `repl`. Returns (newBytes, didReplace).
//
// For each paragraph:
//  1. Build the flat text view: concatenate every <w:t>'s text content in
//     document order, recording (tSpan) offsets.
//  2. Search the flat text for `find` (all occurrences).
//  3. For each match, splice the replacement into the FIRST overlapping
//     <w:t> (preserving its <w:rPr>), blank the text in remaining overlapping
//     <w:t> elements.
//
// We re-scan the paragraph after each replacement (offsets shift), capping at
// a reasonable number of replacements per paragraph.
func replaceLiteral(data []byte, find, repl string) ([]byte, bool) {
	if find == "" {
		return data, false
	}
	repl = xmlEscapeText(repl)
	replaced := false

	// We walk the data, and whenever we hit a <w:p, we buffer the paragraph
	// tokens and run the cross-run replace on the paragraph's bytes. This
	// paragraph-scoped approach keeps the tSpan offsets small and correct.
	var out bytes.Buffer
	out.Grow(len(data))
	i := 0
	for i < len(data) {
		// Find the next <w:p (paragraph start).
		pStart := bytes.Index(data[i:], []byte("<w:p"))
		if pStart < 0 {
			out.Write(data[i:])
			break
		}
		pStart += i
		// Write everything before the paragraph.
		out.Write(data[i:pStart])
		// Find the paragraph's closing </w:p>.
		pClose := bytes.Index(data[pStart:], []byte("</w:p>"))
		if pClose < 0 {
			out.Write(data[pStart:])
			break
		}
		pClose += pStart + len("</w:p>")
		// Extract the paragraph and run cross-run replace on it.
		para := data[pStart:pClose]
		newPara, didReplace := replaceInParagraphBytes(para, find, repl)
		out.Write(newPara)
		if didReplace {
			replaced = true
		}
		i = pClose
	}
	return out.Bytes(), replaced
}

// replaceInParagraphBytes does the cross-run replace on one paragraph's raw
// bytes. Returns (newBytes, didReplace). Handles the case where `find` spans
// multiple <w:t> elements by building a flat text view with offset mapping.
func replaceInParagraphBytes(para []byte, find, repl string) ([]byte, bool) {
	spans, flatText := buildFlatText(para)
	if len(spans) == 0 || flatText == "" {
		return para, false
	}

	result := para
	didReplace := false
	// Cap replacements per paragraph to avoid infinite loops on pathological
	// inputs (find==repl would loop forever without this).
	const maxReplacements = 200
	count := 0
	searchOffset := 0

	for count < maxReplacements {
		// Rebuild spans/flatText after each edit (byte offsets shift).
		spans, flatText = buildFlatText(result)
		if len(spans) == 0 {
			break
		}
		if searchOffset >= len(flatText) {
			break
		}
		idx := strings.Index(flatText[searchOffset:], find)
		if idx < 0 {
			break
		}
		idx += searchOffset
		matchEnd := idx + len(find)
		result = spliceReplacementBySpans(result, spans, idx, matchEnd, repl)
		didReplace = true
		count++
		searchOffset = idx + len(repl)
	}
	return result, didReplace
}

// buildFlatText extracts all <w:t>...</w:t> text from the paragraph XML,
// concatenating them into a single flat string with a span map recording
// where each <w:t>'s text lives in both the flat string and the raw XML.
//
// This is the Go equivalent of OfficeCLI's BuildRunTexts — it gives us a
// contiguous text view that we can search, plus the mapping to splice edits
// back into the correct XML locations.
func buildFlatText(para []byte) ([]tSpan, string) {
	var spans []tSpan
	var flat strings.Builder
	s := string(para)
	searchFrom := 0
	for {
		// Find the next <w:t tag (must be followed by '>' or ' ' to distinguish
		// from <w:tblPr>, <w:tabs>, etc.).
		idx := indexOfWT(s, searchFrom)
		if idx < 0 {
			break
		}
		// Find the '>' that closes the opening <w:t ...> tag.
		gt := strings.IndexByte(s[idx:], '>')
		if gt < 0 {
			break
		}
		textStart := idx + gt + 1
		// Find </w:t> that closes this element.
		closeIdx := strings.Index(s[textStart:], "</w:t>")
		if closeIdx < 0 {
			break
		}
		textContent := s[textStart : textStart+closeIdx]
		xmlEnd := textStart + closeIdx + len("</w:t>")
		spans = append(spans, tSpan{
			xmlStart:  idx,
			xmlEnd:    xmlEnd,
			textStart: flat.Len(),
			textEnd:   flat.Len() + len(textContent),
		})
		flat.WriteString(textContent)
		searchFrom = xmlEnd
	}
	return spans, flat.String()
}

// indexOfWT finds the next "<w:t" at/after pos that is a real <w:t> tag
// (followed by '>' or ' ', so we don't match <w:tbl>/<w:tblPr>/<w:tabs>).
func indexOfWT(s string, pos int) int {
	for {
		idx := strings.Index(s[pos:], "<w:t")
		if idx < 0 {
			return -1
		}
		p := pos + idx
		if p+4 < len(s) {
			c := s[p+4]
			if c == '>' || c == ' ' {
				return p
			}
		}
		pos = p + 4
	}
}

// spliceReplacementBySpans rewrites the paragraph XML so the text in
// [matchStart, matchEnd) (flat-text offsets) is replaced by `repl`. The
// replacement text lands in the FIRST overlapping <w:t> (inheriting that
// run's <w:rPr> formatting), and the text in remaining overlapping <w:t>
// elements is blanked.
//
// This is a byte-level operation on the paragraph XML:
//   - For the first overlapping span: keep [0, matchStart-textStart) of its
//     text + repl + [matchEnd-textStart, ...) of its text.
//   - For subsequent overlapping spans: keep only the part after matchEnd
//     (if any). Text inside the match window is dropped.
func spliceReplacementBySpans(para []byte, spans []tSpan, matchStart, matchEnd int, repl string) []byte {
	// Find the first and last spans overlapping [matchStart, matchEnd).
	firstIdx, lastIdx := -1, -1
	for i, sp := range spans {
		if sp.textEnd <= matchStart || sp.textStart >= matchEnd {
			continue
		}
		if firstIdx < 0 {
			firstIdx = i
		}
		lastIdx = i
	}
	if firstIdx < 0 {
		return para
	}

	// For each overlapping span, compute its new text content, then rebuild
	// the paragraph by splicing. We process from LAST to FIRST so byte offsets
	// of earlier spans don't shift.
	s := string(para)
	for i := lastIdx; i >= firstIdx; i-- {
		sp := spans[i]
		// Extract the original text content of this span.
		gt := strings.IndexByte(s[sp.xmlStart:], '>')
		if gt < 0 {
			continue
		}
		textStart := sp.xmlStart + gt + 1
		textEnd := sp.xmlEnd - len("</w:t>")
		origText := s[textStart:textEnd]

		var newText string
		if i == firstIdx && firstIdx == lastIdx {
			// Match entirely within one span.
			relStart := matchStart - sp.textStart
			relEnd := matchEnd - sp.textStart
			newText = origText[:relStart] + repl + origText[relEnd:]
		} else if i == firstIdx {
			// First of multiple: keep prefix before match + repl.
			relStart := matchStart - sp.textStart
			newText = origText[:relStart] + repl
			// If this span also extends past matchEnd, keep the tail.
			if sp.textEnd > matchEnd {
				relEnd := matchEnd - sp.textStart
				newText += origText[relEnd:]
			}
		} else if i == lastIdx {
			// Last overlapping: keep only the part past matchEnd.
			relEnd := matchEnd - sp.textStart
			if relEnd < len(origText) {
				newText = origText[relEnd:]
			} else {
				newText = ""
			}
		} else {
			// Middle span: entirely inside match window — drop all text.
			newText = ""
		}

		// Splice: replace origText with newText in the XML.
		s = s[:textStart] + newText + s[textEnd:]
	}

	return []byte(s)
}
