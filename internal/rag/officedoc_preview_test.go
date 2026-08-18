package rag

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsOfficeDoc(t *testing.T) {
	for _, ext := range []string{".docx", ".doc", ".rtf", ".xlsx", ".xls", ".pptx", ".ppt", ".epub", ".msg", ".DOCX", ".Doc"} {
		if !IsOfficeDoc("dir/file" + ext) {
			t.Errorf("IsOfficeDoc(%q) = false, want true", ext)
		}
	}
	for _, name := range []string{"a.pdf", "a.png", "a.txt", "a.md", "a.mp3", "readme"} {
		if IsOfficeDoc(name) {
			t.Errorf("IsOfficeDoc(%q) = true, want false", name)
		}
	}
}

func TestNormalizeDocText(t *testing.T) {
	got := normalizeDocText("第一段\x07\x07第二行\x0b换行\x0c分页\r结尾")
	want := "第一段\t\t第二行\n换行\n分页\n结尾"
	if got != want {
		t.Errorf("normalizeDocText = %q, want %q", got, want)
	}
}

// TestReadDocumentForPreviewDOCXGoFallback builds a minimal synthetic .docx
// (zip with word/document.xml) and verifies the pure-Go parser path — no
// markitdown/Word dependency, so it runs anywhere.
func TestReadDocumentForPreviewDOCXGoFallback(t *testing.T) {
	if findDocConverterScript() != "" {
		t.Skip("markitdown present: preview would use it instead of the Go parser")
	}
	docXML := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>智慧教育平台需求说明书</w:t></w:r></w:p>
<w:p><w:r><w:t>第一章 </w:t></w:r><w:r><w:t>总体目标</w:t></w:r></w:p>
</w:body></w:document>`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(docXML)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "spec.docx")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := ReadDocumentForPreview(path, 5*time.Second)
	if err != nil {
		t.Fatalf("ReadDocumentForPreview: %v", err)
	}
	for _, want := range []string{"智慧教育平台需求说明书", "第一章 总体目标"} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q; got: %q", want, text)
		}
	}
}

func TestReadDocumentForPreviewRejectsNonDoc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDocumentForPreview(path, time.Second); err == nil {
		t.Error("expected error for non-document extension")
	}
}

// TestReadDocumentForPreviewEPUB builds a minimal synthetic .epub (zip of
// XHTML chapters) and verifies the pure-Go fallback — no markitdown needed.
func TestReadDocumentForPreviewEPUB(t *testing.T) {
	if findDocConverterScript() != "" {
		t.Skip("markitdown present: preview would use it instead of the Go parser")
	}
	ch1 := "<html><body><h1>第一章 起源</h1><p>开源协作的种子从这里萌发。</p></body></html>"
	ch2 := "<html><body><h1>第二章 成长</h1><p>社区在争论与共识中壮大。</p></body></html>"
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"mimetype":        "application/epub+zip",
		"OEBPS/ch1.xhtml": ch1,
		"OEBPS/ch2.xhtml": ch2,
		"OEBPS/style.css": "h1 { color: red }", // not html — must be skipped
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := ReadDocumentForPreview(path, 5*time.Second)
	if err != nil {
		t.Fatalf("ReadDocumentForPreview: %v", err)
	}
	for _, want := range []string{"第一章 起源", "开源协作的种子从这里萌发", "第二章 成长", "社区在争论与共识中壮大"} {
		if !strings.Contains(text, want) {
			t.Errorf("extracted text missing %q; got: %q", want, text)
		}
	}
	if strings.Contains(text, "color: red") {
		t.Error("css entry leaked into extracted text")
	}
}
