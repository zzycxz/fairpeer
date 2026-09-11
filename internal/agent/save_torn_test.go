package agent

import (
	"os"

	"github.com/zzycxz/fairpeer/internal/provider"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// P1-E5: a torn final line (power loss after rename, before data blocks) must
// salvage every cleanly-decoded message instead of failing the whole session.
func TestLoadSessionToleratesTornTail(t *testing.T) {
	dir := t.TempDir()
	good := NewSession("")
	good.Add(provider.Message{Role: provider.RoleUser, Content: "first"})
	good.Add(provider.Message{Role: provider.RoleAssistant, Content: "second"})
	path := filepath.Join(dir, "s.jsonl")
	if err := good.Save(path); err != nil {
		t.Fatal(err)
	}
	// Slice the file mid-JSON on the LAST message only: keep everything up to
	// the start of the final line, plus half of that line.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.LastIndexByte(string(data[:len(data)-1]), '\n')
	if idx < 0 {
		t.Fatal("expected multi-line session")
	}
	torn := append([]byte{}, data[:idx+1+len("  ")]...) // half of the last line
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}
	// The .sig from the good save now mismatches the torn file — remove it so
	// the salvage path (not the integrity path) is what's under test.
	_ = os.Remove(path + ".sig")

	s, err := LoadSession(path)
	if err != nil {
		t.Fatalf("torn tail should salvage, got: %v", err)
	}
	msgs := s.Snapshot()
	if len(msgs) < 1 || msgs[0].Content != "first" {
		t.Errorf("salvaged messages = %+v, want at least the first", msgs)
	}
}

// NEW-01 regression: a session saved with the pre-fix non-atomic .sig (stale
// signature, JSONL itself intact) must LOAD instead of being bricked by the
// integrity check.
func TestLoadSessionToleratesStaleSig(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "intact content"})
	path := filepath.Join(dir, "s.jsonl")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	// Simulate the crash window: jsonl rewritten (content changed → sig stale),
	// then the .sig write never happened. Backdate the .sig to guarantee it is
	// older than the jsonl.
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"changed content\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path+".sig", past, past); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("stale sig must not brick a good session: %v", err)
	}
	if len(loaded.Snapshot()) == 0 {
		t.Fatal("expected the salvaged message to load")
	}
}
