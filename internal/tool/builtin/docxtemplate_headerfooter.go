package builtin

// docxtemplate_headerfooter.go: header/footer text replacement for doc_write's template-fill.
//
// STRING-LEVEL (same rationale as find_replace/table_fill — encoding/xml
// normalizes namespace prefixes and breaks OOXML). We:
//   1. Scan document.xml for the first <w:headerReference w:r:id="rIdN"/> (or
//      footerReference) to get the relationship id.
//   2. Scan word/_rels/document.xml.rels for Relationship Id="rIdN" Target="...".
//   3. Rewrite the first paragraph in that header/footer part's bytes to carry
//      the new text, preserving the part's XML structure.
//
// Phase 5 scope: replace the DEFAULT header/footer text. Per-section targeting
// (first/even/default) left for later.

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// applyHeaderFooter resolves references and rewrites the matched parts.
func applyHeaderFooter(parts map[string][]byte, body, rels []byte, header, footer *headerFooterSpec, warnSink []DocError) (map[string][]byte, []DocError) {
	if header != nil {
		parts, warnSink = replaceHFText(parts, body, rels, "header", header, warnSink)
	}
	if footer != nil {
		parts, warnSink = replaceHFText(parts, body, rels, "footer", footer, warnSink)
	}
	return parts, warnSink
}

func replaceHFText(parts map[string][]byte, body, rels []byte, kind string, spec *headerFooterSpec, warnSink []DocError) (map[string][]byte, []DocError) {
	rID := findFirstRefID(body, kind+"Reference")
	if rID == "" {
		warnSink = append(warnSink, DocError{Code: ErrPlaceholderNF,
			Message:    fmt.Sprintf("no %s reference found in document; the template has no %s", kind, kind),
			Suggestion: "open the template in Word and add a " + kind + " first"})
		return parts, warnSink
	}
	target := resolveRelTarget(rels, rID)
	if target == "" {
		warnSink = append(warnSink, DocError{Code: ErrCorruptFile,
			Message: fmt.Sprintf("%s reference rId=%s not found in document.xml.rels", kind, rID)})
		return parts, warnSink
	}
	partKey := target
	if !strings.HasPrefix(partKey, "word/") {
		partKey = "word/" + partKey
	}
	data, ok := parts[partKey]
	if !ok {
		warnSink = append(warnSink, DocError{Code: ErrCorruptFile,
			Message: fmt.Sprintf("%s part %s referenced but not present in package", kind, partKey)})
		return parts, warnSink
	}
	newData, ok := rewriteFirstParagraphText(data, spec.Text, spec.Align)
	if !ok {
		warnSink = append(warnSink, DocError{Code: ErrPlaceholderNF,
			Message: fmt.Sprintf("could not write %s text into %s (no paragraph found)", kind, partKey)})
		return parts, warnSink
	}
	parts[partKey] = newData
	return parts, warnSink
}

// refIDRe captures the r:id (or relationships:id) value from a header/footer
// reference element. Matches w:r:id="rId7" / r:id='rId7' / relationships:id="rId7".
var refIDRe = regexp.MustCompile(`(?:r:id|relationships:id)\s*=\s*["']([^"']+)["']`)

// findFirstRefID returns the r:id of the first <w:{refName}> in body, or "".
func findFirstRefID(body []byte, refName string) string {
	// Find the element by tag name (string scan), then grab its r:id attr.
	tag := "<w:" + refName
	idx := strings.Index(string(body), tag)
	if idx < 0 {
		return ""
	}
	// Window from idx to the next '>' (end of the element).
	end := bytes.IndexByte(body[idx:], '>')
	if end < 0 {
		return ""
	}
	frag := body[idx : idx+end]
	m := refIDRe.FindSubmatch(frag)
	if len(m) >= 2 {
		return string(m[1])
	}
	return ""
}

// relTargetRe captures Id and Target from a <Relationship .../> element.
var relTargetRe = regexp.MustCompile(`Id\s*=\s*["']([^"']+)["'][^>]*Target\s*=\s*["']([^"']+)["']`)
var relTargetReRev = regexp.MustCompile(`Target\s*=\s*["']([^"']+)["'][^>]*Id\s*=\s*["']([^"']+)["']`)

func resolveRelTarget(rels []byte, rID string) string {
	// Try Id-first then Target-first attribute orders (OOXML doesn't fix order).
	for _, m := range relTargetRe.FindAllSubmatch(rels, -1) {
		if string(m[1]) == rID {
			return string(m[2])
		}
	}
	for _, m := range relTargetReRev.FindAllSubmatch(rels, -1) {
		if string(m[2]) == rID {
			return string(m[1])
		}
	}
	return ""
}

// rewriteFirstParagraphText replaces the text of the first <w:p>...</w:p> in
// data with the given text (preserving the paragraph's <w:pPr> when present
// for indent/spacing, and setting alignment). Returns (newBytes, ok).
//
// Strategy (string-level): find the first <w:p ...>...</w:p>, then within it:
//   - keep the <w:pPr>...</w:pPr> if present (insert/replace <w:jc> for align)
//   - drop everything else (existing runs) and emit one fresh run with text.
//
// {PAGE}/{NUMPAGES} in the text become Word field runs for dynamic page nums.
func rewriteFirstParagraphText(data []byte, text, align string) ([]byte, bool) {
	pStart := bytes.Index(data, []byte("<w:p"))
	if pStart < 0 {
		return nil, false
	}
	// Find the opening tag end.
	openEnd := bytes.IndexByte(data[pStart:], '>')
	if openEnd < 0 {
		return nil, false
	}
	openEnd += pStart + 1
	// Find matching </w:p> (handle nested <w:p> inside structs — rare for
	// headers; we take the FIRST </w:p> at this level).
	pClose := bytes.Index(data[openEnd:], []byte("</w:p>"))
	if pClose < 0 {
		return nil, false
	}
	pClose += openEnd

	inner := data[openEnd:pClose]
	// Extract <w:pPr>...</w:pPr> if present.
	var pPr string
	ppStart := bytes.Index(inner, []byte("<w:pPr"))
	if ppStart >= 0 {
		ppEnd := bytes.Index(inner[ppStart:], []byte("</w:pPr>"))
		if ppEnd >= 0 {
			pPr = string(inner[ppStart : ppStart+ppEnd+len("</w:pPr>")])
		}
	}
	pPr = ensureAlignInPPr(pPr, align)

	// Build the new paragraph body: pPr + one run carrying the text (with
	// field expansion for {PAGE}/{NUMPAGES}).
	var newInner bytes.Buffer
	if pPr != "" {
		newInner.WriteString(pPr)
	}
	newInner.WriteString(buildRunXMLForText(text))

	// Splice: data[:openEnd] + newInner + data[pClose:]
	var out bytes.Buffer
	out.Write(data[:openEnd])
	out.Write(newInner.Bytes())
	out.Write(data[pClose:])
	return out.Bytes(), true
}

// ensureAlignInPPr inserts/replaces <w:jc w:val="..."/> in a pPr string.
func ensureAlignInPPr(pPr, align string) string {
	if align == "" {
		return pPr
	}
	jc := fmt.Sprintf(`<w:jc w:val="%s"/>`, align)
	if pPr == "" {
		return fmt.Sprintf(`<w:pPr>%s</w:pPr>`, jc)
	}
	stripped := stripJCTag(pPr)
	return strings.Replace(stripped, "</w:pPr>", jc+"</w:pPr>", 1)
}

// stripJCTag removes any existing <w:jc .../> self-closing element.
var jcRe = regexp.MustCompile(`<w:jc\s+[^/]*/>`)

func stripJCTag(pPr string) string {
	return jcRe.ReplaceAllString(pPr, "")
}

// buildRunXMLForText emits <w:r><w:t>text</w:t></w:r>, expanding {PAGE} and
// {NUMPAGES} into Word field runs so the footer shows dynamic page numbers.
func buildRunXMLForText(text string) string {
	var b strings.Builder
	parts := splitOnFields(text)
	for _, p := range parts {
		if p.isField {
			b.WriteString(fieldRunXML(p.field))
		} else {
			b.WriteString(fmt.Sprintf(`<w:r><w:t xml:space="preserve">%s</w:t></w:r>`, xmlEscapeText(p.text)))
		}
	}
	return b.String()
}

type textPart struct {
	text    string
	isField bool
	field   string
}

func splitOnFields(s string) []textPart {
	var parts []textPart
	rest := s
	for {
		page := strings.Index(rest, "{PAGE}")
		num := strings.Index(rest, "{NUMPAGES}")
		idx := -1
		field := ""
		if page >= 0 && (num < 0 || page < num) {
			idx, field = page, "PAGE"
		} else if num >= 0 {
			idx, field = num, "NUMPAGES"
		}
		if idx < 0 {
			if rest != "" {
				parts = append(parts, textPart{text: rest})
			}
			break
		}
		if idx > 0 {
			parts = append(parts, textPart{text: rest[:idx]})
		}
		parts = append(parts, textPart{isField: true, field: field})
		rest = rest[idx+len(field)+2:]
	}
	return parts
}

// fieldRunXML emits a Word field as run XML:
//   <w:r><w:fldChar w:fldCharType="begin"/></w:r>
//   <w:r><w:instrText> PAGE </w:instrText></w:r>
//   <w:r><w:fldChar w:fldCharType="end"/></w:r>
func fieldRunXML(field string) string {
	return fmt.Sprintf(`<w:r><w:fldChar w:fldCharType="begin"/></w:r>`+
		`<w:r><w:instrText xml:space="preserve"> %s </w:instrText></w:r>`+
		`<w:r><w:fldChar w:fldCharType="end"/></w:r>`, field)
}
