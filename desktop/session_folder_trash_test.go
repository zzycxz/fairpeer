package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Full trash→restore roundtrip for a per-session-folder session (2026-08-21
// layout): trash flattens artifacts into <trash>/<id>.jsonl/<id>.jsonl and
// removes the emptied folder; restore recreates <sessions>/<id>/<id>.jsonl.
func TestTrashRestoreFolderSessionRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := sessionPathForTest(t, dir)

	if err := deleteSessionFile(dir, p); err != nil {
		t.Fatalf("trash: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("transcript still exists after trash: %v", err)
	}
	folder := filepath.Dir(p)
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("emptied session folder still exists after trash: %v", err)
	}

	trashed, err := listTrashedSessionFiles(dir)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	if len(trashed) != 1 {
		t.Fatalf("trash list = %v", trashed)
	}
	if err := restoreTrashedSessionFile(dir, trashed[0]); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("transcript not restored into folder: %v", err)
	}
	if _, err := os.Stat(p + ".meta"); err != nil {
		t.Fatalf("meta not restored alongside: %v", err)
	}
}

func sessionPathForTest(t *testing.T, dir string) string {
	t.Helper()
	folder := filepath.Join(dir, "20260821-120000-abcd")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(folder, "20260821-120000-abcd.jsonl")
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+".meta", []byte(`{"topic_id":"t1","cached_turns":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
