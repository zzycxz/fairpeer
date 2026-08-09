package builtin

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTemplateDocx writes a minimal but valid .docx with the given body XML,
// so tests can construct templates with specific structures (placeholders,
// tables, merge cells) without hand-rolling binary.
func makeTemplateDocx(t *testing.T, path, bodyXML string) {
	t.Helper()
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body>` + bodyXML +
		`<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr>` +
		`</w:body></w:document>`
	relsXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`</Relationships>`
	ctXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`</Types>`
	stylesXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults/></w:styles>`

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{
		"[Content_Types].xml":           ctXML,
		"_rels/.rels":                   `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":             docXML,
		"word/_rels/document.xml.rels":  relsXML,
		"word/styles.xml":               stylesXML,
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
}

// readPartFromDocx reads one zip entry from a .docx for assertions.
func readPartFromDocx(t *testing.T, path, name string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			defer rc.Close()
			data, _ := io.ReadAll(rc)
			return string(data)
		}
	}
	return ""
}

// TestDocTemplateOmitsPathAutoCopy verifies that when `path` is omitted, the
// tool generates a "<name>-filled.docx" alongside the source and fills it.
// This is the default "fill my word" behavior — the user gets a filled copy,
// the original template is untouched.
func TestDocTemplateOmitsPathAutoCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "template.docx")
	makeTemplateDocx(t, src, `<w:p><w:r><w:t>{{title}}</w:t></w:r></w:p>`)

	args := mustJSONArgs(t, map[string]any{
		// NOTE: no "path" — tool should default to template-filled.docx.
		"source": src,
		"find_replace": []map[string]string{{"find": "{{title}}", "replace": "My Title"}},
	})
	out, err := (docWrite{}).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("omitted path should succeed: %v", err)
	}
	// The output should be template-filled.docx next to the source.
	expected := filepath.Join(dir, "template-filled.docx")
	if !strings.Contains(out, expected) {
		t.Errorf("expected auto output %s; got: %s", expected, out)
	}
	body := readPartFromDocx(t, expected, "word/document.xml")
	if !strings.Contains(body, "My Title") {
		t.Errorf("fill didn't land in the auto-copy; body:\n%s", body)
	}
	// Original must be unchanged (no "My Title" in it).
	origBody := readPartFromDocx(t, src, "word/document.xml")
	if strings.Contains(origBody, "My Title") {
		t.Errorf("ORIGINAL template was modified! body:\n%s", origBody)
	}
}

// TestDocTemplateFindReplaceSimple verifies a placeholder within a single run
// is replaced.
func TestDocTemplateFindReplaceSimple(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateDocx(t, src, `<w:p><w:r><w:t>Hello {{name}}!</w:t></w:r></w:p>`)
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src,
		"path":   dst,
		"find_replace": []map[string]string{
			{"find": "{{name}}", "replace": "World"},
		},
	})
	out, err := (docWrite{}).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v\n%s", err, out)
	}
	body := readPartFromDocx(t, dst, "word/document.xml")
	if !strings.Contains(body, "Hello World!") {
		t.Errorf("placeholder not replaced; body:\n%s", body)
	}
	if strings.Contains(body, "{{name}}") {
		t.Errorf("placeholder still present; body:\n%s", body)
	}
}

// TestDocTemplateFindReplaceChinese verifies a Chinese placeholder key works
// (the v3 plan's "letters/digits/underscores only" would have rejected this).
func TestDocTemplateFindReplaceChinese(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateDocx(t, src, `<w:p><w:r><w:t>甲方：{{甲方}}</w:t></w:r></w:p>`)
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"find_replace": []map[string]string{{"find": "{{甲方}}", "replace": "张三"}},
	})
	if _, err := (docWrite{}).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	body := readPartFromDocx(t, dst, "word/document.xml")
	if !strings.Contains(body, "张三") || strings.Contains(body, "{{甲方}}") {
		t.Errorf("Chinese placeholder not replaced; body:\n%s", body)
	}
}

// TestDocTemplateFindReplaceCrossRun verifies a placeholder SPLIT across
// multiple <w:r> runs is still matched and replaced. This is the core hard
// case — Word fragments runs on save, so "{{name}}" may serialize as
// "{{" + "name" + "}}" in three separate runs.
func TestDocTemplateFindReplaceCrossRun(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	// "{{name}}" split across 3 runs.
	makeTemplateDocx(t, src,
		`<w:p>`+
			`<w:r><w:t>before </w:t></w:r>`+
			`<w:r><w:t>{{</w:t></w:r>`+
			`<w:r><w:t>name</w:t></w:r>`+
			`<w:r><w:t>}}</w:t></w:r>`+
			`<w:r><w:t> after</w:t></w:r>`+
			`</w:p>`)
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"find_replace": []map[string]string{{"find": "{{name}}", "replace": "World"}},
	})
	if _, err := (docWrite{}).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	body := readPartFromDocx(t, dst, "word/document.xml")
	if !strings.Contains(body, "before") || !strings.Contains(body, "World") || !strings.Contains(body, "after") {
		t.Errorf("cross-run replacement failed; body:\n%s", body)
	}
	// No placeholder fragments should remain.
	if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
		t.Errorf("placeholder fragments remain; body:\n%s", body)
	}
}

// TestDocTemplateFindReplaceNotFoundWarns verifies an unmatched placeholder
// returns a warning rather than failing the whole fill.
func TestDocTemplateFindReplaceNotFoundWarns(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateDocx(t, src, `<w:p><w:r><w:t>no placeholders here</w:t></w:r></w:p>`)
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"find_replace": []map[string]string{{"find": "{{missing}}", "replace": "x"}},
	})
	out, err := (docWrite{}).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unmatched placeholder should warn, not error: %v", err)
	}
	if !strings.Contains(out, "placeholder_not_found") && !strings.Contains(out, "not found") {
		t.Errorf("expected a not-found warning; got: %s", out)
	}
}

// TestDocTemplateTableFill verifies filling a cell by index lands in the right
// <w:tc>.
func TestDocTemplateTableFill(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateDocx(t, src,
		`<w:tbl><w:tblPr/><w:tblGrid><w:gridCol/><w:gridCol/></w:tblGrid>`+
			`<w:tr><w:tc><w:tcPr/><w:p><w:r><w:t>H1</w:t></w:r></w:p></w:tc><w:tc><w:tcPr/><w:p><w:r><w:t>H2</w:t></w:r></w:p></w:tc></w:tr>`+
			`<w:tr><w:tc><w:tcPr/><w:p><w:r><w:t></w:t></w:r></w:p></w:tc><w:tc><w:tcPr/><w:p><w:r><w:t></w:t></w:r></w:p></w:tc></w:tr>`+
			`</w:tbl>`)
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"table_fill": []map[string]any{
			{"table": 0, "row": 1, "col": 0, "value": "Apple"},
			{"table": 0, "row": 1, "col": 1, "value": "100"},
		},
	})
	if _, err := (docWrite{}).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	body := readPartFromDocx(t, dst, "word/document.xml")
	if !strings.Contains(body, "Apple") || !strings.Contains(body, "100") {
		t.Errorf("table_fill values missing; body:\n%s", body)
	}
}

// TestDocTemplateRejectsSourceEqualsPath verifies the source==path guard.
func TestDocTemplateRejectsSourceEqualsPath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "same.docx")
	makeTemplateDocx(t, src, `<w:p><w:r><w:t>x</w:t></w:r></w:p>`)

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": src, // same!
		"find_replace": []map[string]string{{"find": "x", "replace": "y"}},
	})
	_, err := (docWrite{}).Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for source==path")
	}
	if !strings.Contains(err.Error(), "must differ") {
		t.Errorf("expected source==path error; got: %v", err)
	}
}

// TestDocTemplateFindReplaceNoReScan verifies a replacement value containing
// "{{...}}" is inserted literally, not re-scanned (prevents the cascade bug).
func TestDocTemplateFindReplaceNoReScan(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateDocx(t, src, `<w:p><w:r><w:t>{{a}}</w:t></w:r></w:p>`)
	dst := filepath.Join(dir, "out.docx")

	// Replace {{a}} with the literal text "{{b}}" — {{b}} must NOT then be
	// treated as a placeholder (it's data, not a template instruction).
	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"find_replace": []map[string]string{{"find": "{{a}}", "replace": "{{b}}"}},
	})
	if _, err := (docWrite{}).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	body := readPartFromDocx(t, dst, "word/document.xml")
	// {{b}} should be present as literal text.
	if !strings.Contains(body, "{{b}}") {
		t.Errorf("literal {{b}} should be in output; body:\n%s", body)
	}
}

// TestDocTemplateTableFillAppliesStyle verifies that a table_fill op with a
// style injects the matching <w:rPr> (bold/color) into the target cell's run.
func TestDocTemplateTableFillAppliesStyle(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateDocx(t, src,
		`<w:tbl><w:tblPr/><w:tblGrid><w:gridCol/></w:tblGrid>`+
			`<w:tr><w:tc><w:tcPr/><w:p><w:r><w:t></w:t></w:r></w:p></w:tc></w:tr>`+
			`</w:tbl>`)
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"table_fill": []map[string]any{
			{"table": 0, "row": 0, "col": 0, "value": "Important", "style": map[string]any{"bold": true, "color": "#FF0000"}},
		},
	})
	if _, err := (docWrite{}).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	body := readPartFromDocx(t, dst, "word/document.xml")
	if !strings.Contains(body, "<w:b/>") {
		t.Errorf("bold not applied; body:\n%s", body)
	}
	if !strings.Contains(body, "FF0000") {
		t.Errorf("color not applied; body:\n%s", body)
	}
	if !strings.Contains(body, "Important") {
		t.Errorf("value missing; body:\n%s", body)
	}
}

// makeTemplateWithHeader writes a .docx that has a header part wired up via
// .rels, so header/footer replacement can be tested. The header carries the
// given initial text.
func makeTemplateWithHeader(t *testing.T, path, headerText string) {
	t.Helper()
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<w:body><w:p><w:r><w:t>body</w:t></w:r></w:p>` +
		`<w:sectPr><w:headerReference w:type="default" r:id="rId3"/></w:sectPr>` +
		`</w:body></w:document>`
	headerXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:p><w:r><w:t>` + headerText + `</w:t></w:r></w:p></w:hdr>`
	relsXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
		`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/>` +
		`</Relationships>`
	ctXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`<Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/>` +
		`</Types>`
	stylesXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults/></w:styles>`
	parts := map[string]string{
		"[Content_Types].xml":          ctXML,
		"_rels/.rels":                  `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":            docXML,
		"word/_rels/document.xml.rels": relsXML,
		"word/styles.xml":              stylesXML,
		"word/header1.xml":             headerXML,
	}
	if err := atomicWrite(path, func(tmp *os.File) error {
		return writeZipPartsFromMap(tmp, parts)
	}); err != nil {
		t.Fatal(err)
	}
}

// writeZipPartsFromMap writes a zip from a name→string map (deterministic order
// via sorted keys). Test helper for constructing bespoke docx packages.
func writeZipPartsFromMap(dst *os.File, parts map[string]string) error {
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	sortStrings(names)
	zw := zip.NewWriter(dst)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(parts[n])); err != nil {
			return err
		}
	}
	return zw.Close()
}

func sortStrings(s []string) {
	// tiny insertion sort to avoid importing sort just here
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestDocTemplateHeaderReplace verifies header text replacement: the rId in
// document.xml is resolved via .rels to header1.xml, and that part's first
// paragraph text is rewritten.
func TestDocTemplateHeaderReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateWithHeader(t, src, "OLD HEADER")
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"header": map[string]string{"text": "New Company", "align": "center"},
	})
	if _, err := (docWrite{}).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	hdr := readPartFromDocx(t, dst, "word/header1.xml")
	if !strings.Contains(hdr, "New Company") {
		t.Errorf("header text not replaced; header part:\n%s", hdr)
	}
	if strings.Contains(hdr, "OLD HEADER") {
		t.Errorf("old header text still present; header part:\n%s", hdr)
	}
	if !strings.Contains(hdr, `w:jc w:val="center"`) {
		t.Errorf("header align not applied; header part:\n%s", hdr)
	}
}

// TestDocTemplateFooterPageField verifies the footer's {PAGE} placeholder
// expands into a Word field run (<w:fldChar>) for dynamic page numbers.
func TestDocTemplateFooterPageField(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	// Reuse header template but we'll target footer — build one with a footer ref.
	makeTemplateWithHeader(t, src, "x") // has header1.xml; footer test still works by adding a footer ref via document.xml edit
	// For simplicity, patch the source to add a footer reference too.
	parts, _ := readAllDocxParts(src)
	doc := string(parts["word/document.xml"])
	doc = strings.Replace(doc,
		`<w:sectPr><w:headerReference w:type="default" r:id="rId3"/></w:sectPr>`,
		`<w:sectPr><w:headerReference w:type="default" r:id="rId3"/><w:footerReference w:type="default" r:id="rId4"/></w:sectPr>`, 1)
	// Add footer rel + part.
	rels := string(parts["word/_rels/document.xml.rels"])
	rels = strings.Replace(rels, "</Relationships>",
		`<Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/></Relationships>`, 1)
	parts["word/document.xml"] = []byte(doc)
	parts["word/_rels/document.xml.rels"] = []byte(rels)
	parts["word/footer1.xml"] = []byte(`<?xml version="1.0" encoding="UTF-8"?><w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>old</w:t></w:r></w:p></w:ftr>`)
	// Add footer override to content types.
	ct := string(parts["[Content_Types].xml"])
	ct = strings.Replace(ct, "</Types>",
		`<Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/></Types>`, 1)
	parts["[Content_Types].xml"] = []byte(ct)
	// Re-write source with the extra parts.
	order := make([]string, 0, len(parts))
	for n := range parts {
		order = append(order, n)
	}
	sortStrings(order)
	if err := atomicWrite(src, func(tmp *os.File) error {
		zw := zip.NewWriter(tmp)
		for _, n := range order {
			w, _ := zw.Create(n)
			w.Write(parts[n])
		}
		return zw.Close()
	}); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "out.docx")
	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"footer": map[string]string{"text": "Page {PAGE} of {NUMPAGES}", "align": "center"},
	})
	if _, err := (docWrite{}).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	ftr := readPartFromDocx(t, dst, "word/footer1.xml")
	if !strings.Contains(ftr, "fldChar") {
		t.Errorf("{PAGE} did not expand to a field run; footer:\n%s", ftr)
	}
	if !strings.Contains(ftr, "instrText") {
		t.Errorf("field instrText missing; footer:\n%s", ftr)
	}
}

// TestDocTemplateTableFillGridSpanCountsAsOneCell verifies the coordinate
// system: a gridSpan=2 cell is ONE physical <w:tc>, so doc_read emits it as
// one array entry and table_fill accepts only col 0 for that row. col 1 is
// out of range (the row has 1 physical cell, not 2).
func TestDocTemplateTableFillGridSpanCountsAsOneCell(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateDocx(t, src,
		`<w:tbl><w:tblPr/><w:tblGrid><w:gridCol/><w:gridCol/></w:tblGrid>`+
			`<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/></w:tcPr><w:p><w:r><w:t>merged</w:t></w:r></w:p></w:tc></w:tr>`+
			`<w:tr><w:tc><w:tcPr/><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc><w:tc><w:tcPr/><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc></w:tr>`+
			`</w:tbl>`)
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		// Fill (0,0) — the single physical cell in the merged row.
		"table_fill": []map[string]any{
			{"table": 0, "row": 0, "col": 0, "value": "FILLED"},
		},
	})
	out, err := (docWrite{}).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("filling the merged cell should succeed: %v", err)
	}
	body := readPartFromDocx(t, dst, "word/document.xml")
	if !strings.Contains(body, "FILLED") {
		t.Errorf("merged cell not filled; body:\n%s", body)
	}
	if strings.Contains(body, ">merged<") {
		t.Errorf("original 'merged' text should have been replaced; body:\n%s", body)
	}
	_ = out
}

// TestDocTemplateTableFillVMergeContinuationRejected verifies that filling a
// vMerge-continuation cell (visually hidden under the merge start above)
// returns an ErrMergedCell warning instead of silently writing invisible text.
func TestDocTemplateTableFillVMergeContinuationRejected(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	// Row 0 col 0 = vMerge restart (visible); row 1 col 0 = vMerge continuation
	// (hidden — its content is swallowed by the cell above).
	makeTemplateDocx(t, src,
		`<w:tbl><w:tblPr/><w:tblGrid><w:gridCol/></w:tblGrid>`+
			`<w:tr><w:tc><w:tcPr><w:vMerge w:val="restart"/></w:tcPr><w:p><w:r><w:t>top</w:t></w:r></w:p></w:tc></w:tr>`+
			`<w:tr><w:tc><w:tcPr><w:vMerge/></w:tcPr><w:p><w:r><w:t></w:t></w:r></w:p></w:tc></w:tr>`+
			`</w:tbl>`)
	dst := filepath.Join(dir, "out.docx")

	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		// Fill (1,0) — the vMerge-continuation cell. Should be REJECTED.
		"table_fill": []map[string]any{
			{"table": 0, "row": 1, "col": 0, "value": "HIDDEN"},
		},
	})
	out, err := (docWrite{}).Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("tool should not hard-error on a merge continuation: %v", err)
	}
	if !strings.Contains(out, "merge") || !strings.Contains(out, "continuation") {
		t.Errorf("expected a merge-continuation warning in output, got: %s", out)
	}
	body := readPartFromDocx(t, dst, "word/document.xml")
	if strings.Contains(body, "HIDDEN") {
		t.Errorf("continuation cell should NOT have been written; body:\n%s", body)
	}
}

// TestDocTemplateRejectsSourceOutsideWorkspace verifies doc_template with
// roots configured refuses a source path outside the workspace (information
// exfiltration guard: a template read from outside could smuggle host file
// content into the output).
func TestDocTemplateRejectsSourceOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tpl.docx")
	makeTemplateDocx(t, src, `<w:p><w:r><w:t>x</w:t></w:r></w:p>`)
	dst := filepath.Join(dir, "out.docx")

	// Bind docTemplate to roots that EXCLUDE `dir` — both src and dst land
	// outside the configured workspace.
	tmpl := docWrite{roots: []string{filepath.Join(dir, "workspace")}}
	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"find_replace": []map[string]string{{"find": "x", "replace": "y"}},
	})
	_, err := tmpl.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for source outside workspace roots, got nil")
	}
}

// TestDocTemplateAcceptsSourceInsideWorkspace verifies the happy path: when
// roots DO contain the source path, the fill proceeds.
func TestDocTemplateAcceptsSourceInsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	os.MkdirAll(ws, 0o755)
	src := filepath.Join(ws, "tpl.docx")
	makeTemplateDocx(t, src, `<w:p><w:r><w:t>{{n}}</w:t></w:r></w:p>`)
	dst := filepath.Join(ws, "out.docx")

	tmpl := docWrite{roots: []string{ws}}
	args := mustJSONArgs(t, map[string]any{
		"source": src, "path": dst,
		"find_replace": []map[string]string{{"find": "{{n}}", "replace": "ok"}},
	})
	if _, err := tmpl.Execute(context.Background(), args); err != nil {
		t.Fatalf("source inside workspace should succeed: %v", err)
	}
}

// --- helpers ---

func mustJSONArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
