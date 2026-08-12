package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocReadTextDefaultsToPaginated verifies that doc_read on a plain-text
// file (.md/.txt) defaults to paginated output (first 2000 lines, with line
// numbers) like read_file — not the old 200k-char unnumbered blob.
func TestDocReadTextDefaultsToPaginated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	// 3000 lines — exceeds the default 2000-line window so pagination kicks in.
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	// Should be line-numbered (the scanLines format), not a raw blob.
	if !strings.Contains(out, "1→line 1") {
		t.Errorf("expected line-numbered output; got (first 80): %q", firstN(out, 80))
	}
	// Should be truncated at 2000 lines with a pagination hint.
	if !strings.Contains(out, "more lines below") {
		t.Errorf("expected pagination hint for a 3000-line file; got (last 120): %q", lastN(out, 120))
	}
}

// TestDocReadTextStreamsLargeFile verifies doc_read streams a large text file
// (line-by-line) instead of refusing it. Before the streaming fix, doc_read
// rejected any text file > 50 MiB (maxDocReadBytes) with a stat-gate error.
// Now it should page through it like read_file.
func TestDocReadTextStreamsLargeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	// Write ~60 MiB of text (> maxDocReadBytes) by repeating a 120-byte line
	// ~525k times. Each line is short so the line count is high but the file
	// is genuinely over the old cap.
	line := strings.Repeat("x", 110) + "\n" // 111 bytes
	const targetBytes = 60 * 1024 * 1024     // 60 MiB, above the 50 MiB old cap
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	written := 0
	for written < targetBytes {
		n, werr := f.Write([]byte(line))
		if werr != nil {
			f.Close()
			t.Fatal(werr)
		}
		written += n
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Read the first page — should succeed (no size refusal) and return the
	// first 2000 lines with a pagination hint.
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("doc_read should stream a large text file, not refuse it: %v", err)
	}
	if !strings.Contains(out, "more lines below") {
		t.Errorf("expected pagination hint for a 60 MiB file; got (last 120): %q", lastN(out, 120))
	}
}

// TestDocReadDirectoryRejected verifies doc_read rejects a directory with an
// actionable message pointing at ls (mirrors read_file), instead of a confusing
// parse error downstream.
func TestDocReadDirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	args, _ := json.Marshal(map[string]string{"path": dir})
	_, err := docRead{}.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error reading a directory")
	}
	if !strings.Contains(err.Error(), "directory") || !strings.Contains(err.Error(), "ls") {
		t.Errorf("error should mention 'directory' and 'ls'; got: %v", err)
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// TestDocReadHTMLMindMapExtractsMarkdown verifies doc_read on a markmap .html
// returns the embedded tree as Markdown (heading levels), not the raw HTML
// source with line numbers. This is the read side of the mindmap loop.
func TestDocReadHTMLMindMapExtractsMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.html")
	if _, err := writeMindMap(MMInput{
		Path:  path,
		Title: "规划",
		Branches: []MMNode{
			{Text: "Q1", Children: []MMNode{{Text: "新功能"}}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 规划", "## Q1", "### 新功能", "[extracted from mindmap HTML]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot (first 200): %q", want, firstN(out, 200))
		}
	}
	// Must NOT be line-numbered streaming output nor raw <script> tags.
	if strings.Contains(out, "1→") || strings.Contains(out, "<script") {
		t.Errorf("markmap html should yield extracted markdown, not raw/numbered source; got (first 200): %q", firstN(out, 200))
	}
}

// TestDocReadPlainHTMLStillStreams verifies a NON-markmap .html still streams as
// line-numbered text — the new markmap branch must not swallow plain HTML.
func TestDocReadPlainHTMLStillStreams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.html")
	html := "<!DOCTYPE html>\n<html>\n<body>\n<h1>Title</h1>\n<p>hello</p>\n</body>\n</html>\n"
	if err := os.WriteFile(path, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": path})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "1→") {
		t.Errorf("plain HTML should stream with line numbers; got (first 200): %q", firstN(out, 200))
	}
	if strings.Contains(out, "[extracted from mindmap HTML]") {
		t.Errorf("plain HTML must not be misdetected as markmap; got (first 200): %q", firstN(out, 200))
	}
}

// TestDocReadProMindmapFormatFriendlyHint verifies doc_read on a professional
// mind-map format (.xmind/.opml) returns an actionable hint instead of a raw
// binary error or a silent wall of XML. .opml (XML) also appends the raw source
// so the model is not empty-handed; .xmind gets only the hint.
func TestDocReadProMindmapFormatFriendlyHint(t *testing.T) {
	dir := t.TempDir()

	// .xmind — hint only, no crash on binary-looking bytes.
	xpath := filepath.Join(dir, "m.xmind")
	if err := os.WriteFile(xpath, []byte("PK\x03\x04fake zip content"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]string{"path": xpath})
	out, err := docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf(".xmind should return a hint, not error: %v", err)
	}
	if !strings.Contains(out, "professional mind-map format") || !strings.Contains(out, "OPML") {
		t.Errorf(".xmind hint should mention professional format + OPML export; got: %q", firstN(out, 200))
	}

	// .opml (XML) — hint + raw source appended.
	opath := filepath.Join(dir, "m.opml")
	opmlSrc := `<?xml version="1.0"?><opml><body><outline text="root"/></body></opml>`
	if err := os.WriteFile(opath, []byte(opmlSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]string{"path": opath})
	out, err = docRead{}.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf(".opml should return a hint, not error: %v", err)
	}
	if !strings.Contains(out, "professional mind-map format") {
		t.Errorf(".opml should carry the friendly hint; got: %q", firstN(out, 200))
	}
	if !strings.Contains(out, "raw .opml source") || !strings.Contains(out, `<outline text="root"/>`) {
		t.Errorf(".opml should append the raw XML source; got: %q", firstN(out, 300))
	}
}
