package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// NewSessionPath must produce the per-session folder layout (2026-08-21):
// <dir>/<yyyymmdd-hhmmss-xxxx>/<same>.jsonl, with the folder actually created
// and the file name matching the folder name (BranchID uniqueness).
func TestNewSessionPathFolderLayout(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPath(dir, "test-model")
	if filepath.Ext(p) != ".jsonl" {
		t.Fatalf("not a jsonl path: %s", p)
	}
	folder := filepath.Dir(p)
	if folder == dir {
		t.Fatalf("flat layout, want per-session folder: %s", p)
	}
	id := filepath.Base(folder)
	if filepath.Base(p) != id+".jsonl" {
		t.Fatalf("file name %q must equal folder name %q + .jsonl (BranchID uniqueness)", filepath.Base(p), id)
	}
	if fi, err := os.Stat(folder); err != nil || !fi.IsDir() {
		t.Fatalf("session folder not created: %v", err)
	}
	// Two calls in the same second must not collide.
	p2 := NewSessionPath(dir, "test-model")
	if p == p2 {
		t.Fatalf("collision: %s", p)
	}
}

// ListSessions must see BOTH layouts: legacy flat *.jsonl and the per-session
// folder form. Sessions with zero turns stay hidden in both.
func TestListSessionsDualLayout(t *testing.T) {
	dir := t.TempDir()

	mk := func(path string, turns int) {
		sidecar := map[string]any{"cached_turns": turns, "cached_preview": "hello", "topic_id": filepath.Base(path)}
		b, err2 := json.Marshal(sidecar)
		if err2 != nil {
			t.Fatal(err2)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".meta", b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Flat legacy
	flat := filepath.Join(dir, "20260101-100000-aaaa.jsonl")
	mk(flat, 3)
	// Folder layout
	folderP := NewSessionPath(dir, "m")
	mk(folderP, 5)
	// Zero-turn folder session must stay hidden
	zeroP := NewSessionPath(dir, "m")
	os.WriteFile(zeroP+".meta", []byte(`{"cached_turns":0}`), 0o644)

	list, err := ListSessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]int{}
	for _, s := range list {
		byPath[s.Path] = s.Turns
	}
	if byPath[flat] != 3 {
		t.Fatalf("flat session missing: %+v", byPath)
	}
	if byPath[folderP] != 5 {
		t.Fatalf("folder session missing: %+v", byPath)
	}
	if _, ok := byPath[zeroP]; ok {
		t.Fatalf("zero-turn session visible: %s", zeroP)
	}
	if strings.Contains(time.Now().String(), "never") {
		t.Fatal("unreachable")
	}
}
