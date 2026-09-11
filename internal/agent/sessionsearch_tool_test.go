package agent

import (
	"context"
	"github.com/zzycxz/fairpeer/internal/provider"

	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sessToolExec(t *testing.T, args map[string]any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return sessionSearch{}.Execute(context.Background(), raw)
}

// P1-B2: the model can FIND a past session by keyword and RE-READ the actual
// decision — the "按上次的方案" flow that used to dead-end.
func TestSessionSearchFindAndRead(t *testing.T) {
	dir := t.TempDir()
	// Seed one past session holding "the decision".
	old := NewSessionPath(dir, "old")
	s := NewSession("")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "把数据库迁移方案定一下"})
	s.Add(provider.Message{Role: provider.RoleAssistant, Content: "决定用双写迁移，先写新库再切读"})
	if err := s.Save(old); err != nil {
		t.Fatal(err)
	}
	current := NewSessionPath(dir, "cur")
	SetSessionSearchScope(dir, current)
	t.Cleanup(func() { SetSessionSearchScope("", "") })

	out, err := sessToolExec(t, map[string]any{"mode": "search", "query": "迁移方案"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(out, "old") && !strings.Contains(out, filepath.Base(old)) {
		t.Fatalf("hit missing old session: %s", out)
	}
	if !strings.Contains(out, "path:") {
		t.Fatalf("hit lacks path for the read step: %s", out)
	}

	read, err := sessToolExec(t, map[string]any{"mode": "read", "path": old})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(read, "双写迁移") {
		t.Fatalf("read lost the decision: %s", read)
	}

	// The CURRENT session is excluded from search (its transcript contains the
	// query itself — a self-match is pure noise).
	cur := NewSession("")
	cur.Add(provider.Message{Role: provider.RoleUser, Content: "迁移方案 unique-cur-marker"})
	if err := cur.Save(current); err != nil {
		t.Fatal(err)
	}
	out2, err := sessToolExec(t, map[string]any{"mode": "search", "query": "unique-cur-marker"})
	if err != nil {
		t.Fatalf("search2: %v", err)
	}
	if strings.Contains(out2, filepath.Base(current)) {
		t.Fatalf("current session leaked into results: %s", out2)
	}
}

func TestSessionSearchPathGuard(t *testing.T) {
	dir := t.TempDir()
	SetSessionSearchScope(dir, "")
	t.Cleanup(func() { SetSessionSearchScope("", "") })
	// A path outside the session dir must be refused — this reads other
	// conversations; it must not become an arbitrary-file reader.
	out, err := sessToolExec(t, map[string]any{"mode": "read", "path": filepath.Join(os.TempDir(), "evil.jsonl")})
	if err == nil && !strings.Contains(strings.ToLower(out), "must be a .jsonl session") {
		t.Fatalf("outside-dir path accepted: %v %q", err, out)
	}
}
