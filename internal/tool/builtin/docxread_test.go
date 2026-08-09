package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocReadStructureMode verifies mode:"structure" returns JSON with
// headings/paragraphs/tables and indices aligned with doc_template.
func TestDocReadStructureMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.docx")
	// Build a doc with a heading, a paragraph, and a 2x2 table.
	makeTemplateDocx(t, path,
		`<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Title</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>Body text</w:t></w:r></w:p>`+
			`<w:tbl><w:tblPr/><w:tblGrid><w:gridCol/><w:gridCol/></w:tblGrid>`+
			`<w:tr><w:tc><w:tcPr/><w:p><w:r><w:t>A</w:t></w:r></w:p></w:tc><w:tc><w:tcPr/><w:p><w:r><w:t>B</w:t></w:r></w:p></w:tc></w:tr>`+
			`<w:tr><w:tc><w:tcPr/><w:p><w:r><w:t>1</w:t></w:r></w:p></w:tc><w:tc><w:tcPr/><w:p><w:r><w:t>2</w:t></w:r></w:p></w:tc></w:tr>`+
			`</w:tbl>`)

	args, _ := json.Marshal(map[string]string{"path": path, "mode": "structure"})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var s docxStructure
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	// Should have heading + paragraph + table blocks.
	foundHeading, foundPara, foundTable := false, false, false
	for _, b := range s.Blocks {
		switch b.Type {
		case "heading":
			if b.Level == 1 && b.Text == "Title" {
				foundHeading = true
			}
		case "paragraph":
			if b.Text == "Body text" {
				foundPara = true
			}
		case "table":
			foundTable = true
			// 2 <w:tr> elements → 2 rows (doc_read no longer separates headers;
			// all rows are in Rows).
			if b.Table == nil || len(b.Table.Rows) != 2 {
				t.Errorf("table rows wrong: %+v", b.Table)
			}
		}
	}
	if !foundHeading {
		t.Errorf("heading block missing; blocks: %+v", s.Blocks)
	}
	if !foundPara {
		t.Errorf("paragraph block missing")
	}
	if !foundTable {
		t.Errorf("table block missing")
	}
}


// TestDocReadLegacyTablesModeRoutesToStructure verifies the legacy "tables"
// mode name now routes to structure (the single read mode), so old prompts
// don't break — they just get the fuller structure view.
func TestDocReadLegacyTablesModeRoutesToStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.docx")
	makeTemplateDocx(t, path,
		`<w:tbl><w:tblPr/><w:tblGrid><w:gridCol/></w:tblGrid>`+
			`<w:tr><w:tc><w:tcPr/><w:p><w:r><w:t>Hdr</w:t></w:r></w:p></w:tc></w:tr>`+
			`<w:tr><w:tc><w:tcPr/><w:p><w:r><w:t>val</w:t></w:r></w:p></w:tc></w:tr>`+
			`</w:tbl>`)

	args, _ := json.Marshal(map[string]string{"path": path, "mode": "tables"})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy "tables" now returns the full structure JSON (with a "blocks"
	// array), not a bare table array. The table content must still be present.
	var s docxStructure
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("legacy tables mode should return structure JSON: %v\n%s", err, out)
	}
	foundTable := false
	for _, b := range s.Blocks {
		if b.Type == "table" {
			foundTable = true
		}
	}
	if !foundTable {
		t.Errorf("table content missing from legacy-tables route; blocks: %+v", s.Blocks)
	}
}

// TestDocReadLegacyMetadataModeRoutesToStructure verifies the legacy "metadata"
// mode routes to structure, with the metadata field populated.
func TestDocReadLegacyMetadataModeRoutesToStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.docx")
	makeTemplateDocx(t, path, `<w:p><w:r><w:t>x</w:t></w:r></w:p>`)
	addCoreProps(t, path, "Alice", "My Report")

	args, _ := json.Marshal(map[string]string{"path": path, "mode": "metadata"})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var s docxStructure
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("legacy metadata mode should return structure JSON: %v\n%s", err, out)
	}
	if s.Metadata.Author != "Alice" {
		t.Errorf("author: got %q, want Alice", s.Metadata.Author)
	}
	if s.Metadata.Title != "My Report" {
		t.Errorf("title: got %q, want My Report", s.Metadata.Title)
	}
}

// TestDocReadDefaultIsStructure verifies omitting mode (the default) returns
// the structure JSON view — the default and primary read mode.
func TestDocReadDefaultIsStructure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d.docx")
	makeTemplateDocx(t, path, `<w:p><w:r><w:t>hello</w:t></w:r></w:p>`)

	args, _ := json.Marshal(map[string]string{"path": path}) // no mode
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	// Default returns structure JSON (starts with "{"), content in a block.
	var s docxStructure
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("default mode should return structure JSON: %v\n%s", err, out)
	}
	found := false
	for _, b := range s.Blocks {
		if b.Text == "hello" {
			found = true
		}
	}
	if !found {
		t.Errorf("content 'hello' missing from default structure read; blocks: %+v", s.Blocks)
	}
}

// TestDocReadTextMode verifies doc_read returns structured JSON (the default
// for .docx). The old plain-text "text" mode was replaced by readDOCXStructure
// so the agent can target table_fill by row/col index.
func TestDocReadTextMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "d.docx")
	makeTemplateDocx(t, path, `<w:p><w:r><w:t>hello world</w:t></w:r></w:p>`)

	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("content lost: %s", out)
	}
	// Should be structured JSON (not plain text).
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("doc_read should return JSON for .docx: %s", out[:80])
	}
}

// addCoreProps rewrites the docx zip to include a docProps/core.xml part
// carrying creator/title. (Tests need this because makeTemplateDocx doesn't
// emit core props.)
func addCoreProps(t *testing.T, path, author, title string) {
	t.Helper()
	parts, err := readAllDocxParts(path)
	if err != nil {
		t.Fatal(err)
	}
	core := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:creator>` + author + `</dc:creator><dc:title>` + title + `</dc:title></cp:coreProperties>`
	parts["docProps/core.xml"] = []byte(core)
	order := make([]string, 0, len(parts))
	for name := range parts {
		order = append(order, name)
	}
	if err := atomicWrite(path, func(tmp *os.File) error {
		return writeZipParts(tmp, order, parts)
	}); err != nil {
		t.Fatal(err)
	}
}
