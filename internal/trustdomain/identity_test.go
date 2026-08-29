package trustdomain

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIdentityFileRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "node")

	first, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 0600 is only enforceable on Unix — Windows security is ACL-based and
	// Go's perm bits don't map (spec §17 key-custody tiers handle it there).
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(IdentityKeyPath(dir)); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("key file perm: %v %v", info.Mode().Perm(), err)
		}
	}

	second, err := LoadOrCreateIdentity(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if first.ID() != second.ID() {
		t.Fatal("identity not stable across loads")
	}
}

func TestIdentityCorruptFileRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IdentityKeyPath(dir), []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateIdentity(dir); err == nil {
		t.Fatal("corrupt identity accepted")
	}
}
