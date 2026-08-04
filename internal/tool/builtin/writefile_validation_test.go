package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteFile_RejectsBrokenGo confirms the pre-write syntax check (SPEC v2
// §3.3) refuses to write a .go file whose content has syntax errors, and that
// the file is NOT created on disk (no corruption). The agent gets a clear error
// mentioning the syntax problem.
func TestWriteFile_RejectsBrokenGo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.go")
	// Broken Go: unclosed paren in func declaration.
	content := "package main\n\nfunc main( {\n}\n"

	w := writeFile{}
	_, err := w.Execute(testContext(t), mustMarshalArgs(map[string]any{"path": path, "content": content}))
	if err == nil {
		t.Fatal("write_file should reject broken Go content")
	}
	if !strings.Contains(err.Error(), "syntax") && !strings.Contains(err.Error(), "expected") {
		t.Errorf("error should mention the syntax problem, got: %v", err)
	}
	// The file MUST NOT exist on disk — the guard runs before the write.
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("broken .go file should NOT have been written to disk")
	}
}

// TestWriteFile_RejectsBrokenJSON confirms the same guard for .json files.
func TestWriteFile_RejectsBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.json")
	content := `{"key": }` // missing value

	w := writeFile{}
	_, err := w.Execute(testContext(t), mustMarshalArgs(map[string]any{"path": path, "content": content}))
	if err == nil {
		t.Fatal("write_file should reject broken JSON content")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("broken .json file should NOT have been written to disk")
	}
}

// TestWriteFile_AcceptsValidGo confirms the guard does NOT block valid writes —
// a correct .go file is written normally.
func TestWriteFile_AcceptsValidGo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.go")
	content := "package main\n\nfunc main() {}\n"

	w := writeFile{}
	out, err := w.Execute(testContext(t), mustMarshalArgs(map[string]any{"path": path, "content": content}))
	if err != nil {
		t.Fatalf("valid Go should write normally, got %v", err)
	}
	if !strings.Contains(out, "wrote") {
		t.Errorf("expected a 'wrote' success message, got %q", out)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("valid .go file should exist on disk: %v", statErr)
	}
}

// TestWriteFile_AllowsUncheckedExtensions confirms non-.go/.json files are not
// checked — a .md/.txt/.py with arbitrary content writes fine.
func TestWriteFile_AllowsUncheckedExtensions(t *testing.T) {
	for _, ext := range []string{".md", ".txt", ".py", ".yaml", ".csv"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "file"+ext)
		content := "{{{ this is not valid anything }}}"
		w := writeFile{}
		_, err := w.Execute(testContext(t), mustMarshalArgs(map[string]any{"path": path, "content": content}))
		if err != nil {
			t.Errorf("%s should write unchecked (no syntax guard), got %v", ext, err)
		}
	}
}

// mustMarshalArgs builds a json.RawMessage from a map (test helper).
func mustMarshalArgs(m map[string]any) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

// testContext returns a plain background context for tool Execute calls.
func testContext(t *testing.T) context.Context {
	return context.Background()
}
