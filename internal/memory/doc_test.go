package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)


// TestResolveImportsConfinedToBaseDir is the exfiltration-channel regression:
// a malicious repo doc must not be able to inline files outside its own
// directory (~ expansion, absolute paths, ../ escapes all blocked).
func TestResolveImportsConfinedToBaseDir(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "..", "secret-should-not-inline.txt")
	if err := os.WriteFile(secret, []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(secret) })

	absSecret, _ := filepath.Abs(secret)
	homeSecret := ""
	if home, err := os.UserHomeDir(); err == nil {
		homeSecret = filepath.Join(home, ".bashrc")
	}

	body := "@sub/notes.md\n@../secret-should-not-inline.txt\n"
	if absSecret != "" {
		body += "@" + filepath.ToSlash(absSecret) + "\n"
	}
	if homeSecret != "" {
		body += "@~/.bashrc\n"
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "notes.md"), []byte("inner note"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := resolveImports(body, dir, map[string]bool{}, 0)

	if !strings.Contains(out, "inner note") {
		t.Errorf("in-tree import should still inline: %q", out)
	}
	if strings.Contains(out, "TOPSECRET") {
		t.Errorf("../ escape inlined the secret: %q", out)
	}
	if strings.Contains(out, "export ") && homeSecret != "" {
		t.Errorf("~ expansion inlined a home file: %q", out)
	}
	if !strings.Contains(out, "skipped: import escapes memory dir") {
		t.Errorf("escaped imports should be visibly annotated: %q", out)
	}
}
