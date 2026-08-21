package agent

import (
	"os"
	"strings"
	"path/filepath"
	"testing"
)

// TestSearchSessionsFindsContent (upgrade spec 4-5): a query matching a
// session's transcript body — not its title or preview — is found, with an
// excerpt centered on the match; a too-short query returns nothing.
func TestSearchSessionsFindsContent(t *testing.T) {
	dir := t.TempDir()
	// One session folder layout whose body mentions the needle.
	id := "20990101-000000-abcd"
	folder := filepath.Join(dir, id)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"role":"user","content":"please fix the flurbishWidget handler"}`
	if err := os.WriteFile(filepath.Join(folder, id+".jsonl"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hits := SearchSessions(dir, "flurbishwidget")
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	if filepath.Base(filepath.Dir(hits[0].Path)) != id {
		t.Fatalf("hit path = %s, want session %s", hits[0].Path, id)
	}
	if len(hits[0].Excerpts) != 1 || !strings.Contains(hits[0].Excerpts[0], "flurbishWidget") {
		t.Fatalf("excerpts = %v", hits[0].Excerpts)
	}
	if got := SearchSessions(dir, "f"); len(got) != 0 {
		t.Fatalf("short query should return no hits, got %d", len(got))
	}
	if got := SearchSessions(dir, "not-present-anywhere"); len(got) != 0 {
		t.Fatalf("no-match query should return no hits, got %d", len(got))
	}
}
