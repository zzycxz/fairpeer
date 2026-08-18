package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "secrets.enc.json"))
	if err := s.Set("MAIL_PASSWORD", "abc123授权码"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := s.Get("MAIL_PASSWORD")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected secret present")
	}
	if got != "abc123授权码" {
		t.Fatalf("got %q, want %q", got, "abc123授权码")
	}
}

func TestStoreMissing(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "secrets.enc.json"))
	_, ok, err := s.Get("nope")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("expected missing")
	}
}

func TestStoreDelete(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "secrets.enc.json"))
	if err := s.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := s.Get("k"); ok {
		t.Fatal("expected deleted")
	}
	// Deleting a missing key is a no-op.
	if err := s.Delete("absent"); err != nil {
		t.Fatalf("Delete absent: %v", err)
	}
}

func TestStoreCiphertextOnDisk(t *testing.T) {
	// The whole point: the plaintext must never appear in the file.
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.enc.json")
	s := New(path)
	if err := s.Set("MAIL_PASSWORD", "supersecret-value-123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(b), "supersecret-value-123") {
		t.Fatal("plaintext leaked to disk")
	}
}

func TestStoreLoadIntoEnv(t *testing.T) {
	const key = "FAIRPEER_TEST_SECRET_XYZ_001"
	os.Unsetenv(key)
	s := New(filepath.Join(t.TempDir(), "secrets.enc.json"))
	if err := s.Set(key, "hello-env"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	n, err := s.LoadIntoEnv()
	if err != nil {
		t.Fatalf("LoadIntoEnv: %v", err)
	}
	if n < 1 {
		t.Fatal("expected at least 1 secret loaded")
	}
	if os.Getenv(key) != "hello-env" {
		t.Fatalf("env not set, got %q", os.Getenv(key))
	}
}

// TestLoadIntoEnvUntrackedSurvivesUnload pins the config-layer lifetime
// contract: an untracked injection must NOT be cleared by UnloadFromEnv, while
// a tracked one must. The desktop relies on this — rebuild() closes the old
// controller after the new one is running, so a controller teardown must never
// strip process-lifetime secrets (provider keys) from the env.
func TestLoadIntoEnvUntrackedSurvivesUnload(t *testing.T) {
	const trackedKey = "FAIRPEER_TEST_SECRET_TRACKED_1"
	const untrackedKey = "FAIRPEER_TEST_SECRET_UNTRACKED_1"
	os.Unsetenv(trackedKey)
	os.Unsetenv(untrackedKey)
	t.Cleanup(func() {
		os.Unsetenv(trackedKey)
		os.Unsetenv(untrackedKey)
	})

	s := New(filepath.Join(t.TempDir(), "secrets.enc.json"))
	if err := s.Set(trackedKey, "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set(untrackedKey, "v2"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := s.LoadIntoEnvUntracked(); err != nil {
		t.Fatalf("LoadIntoEnvUntracked: %v", err)
	}
	if os.Getenv(untrackedKey) != "v2" {
		t.Fatalf("untracked env not set, got %q", os.Getenv(untrackedKey))
	}

	// The same store still tracks vars it injected itself (simulate the
	// controller-scoped path by unsetting first).
	os.Unsetenv(trackedKey)
	if _, err := s.LoadIntoEnv(); err != nil {
		t.Fatalf("LoadIntoEnv: %v", err)
	}

	if n := s.UnloadFromEnv(); n != 1 {
		t.Fatalf("UnloadFromEnv cleared %d vars, want 1 (only the tracked one)", n)
	}
	if _, ok := os.LookupEnv(trackedKey); ok {
		t.Fatal("tracked key should be unloaded")
	}
	if os.Getenv(untrackedKey) != "v2" {
		t.Fatal("untracked key must survive UnloadFromEnv")
	}
}

func TestStoreEmptyValue(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "secrets.enc.json"))
	if err := s.Set("k", ""); err != nil {
		t.Fatalf("Set empty: %v", err)
	}
	got, ok, err := s.Get("k")
	if err != nil || !ok {
		t.Fatalf("Get empty: ok=%v err=%v", ok, err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestMigrateEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "credentials")
	// export prefix, quotes, comments, blanks, and an empty value — the same
	// lenient surface config.loadDotEnvFile parses.
	content := "# managed by fairpeer\n" +
		"OPENAI_API_KEY=sk-plain-1\n" +
		"\n" +
		"export DEEPSEEK_API_KEY=\"sk-quoted-2\"\n" +
		"EMPTY_SECRET=\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := New(filepath.Join(dir, "secrets.enc.json"))
	n, err := s.MigrateEnvFile(envPath)
	if err != nil {
		t.Fatalf("MigrateEnvFile: %v", err)
	}
	if n != 2 {
		t.Fatalf("migrated %d, want 2 (empty value skipped)", n)
	}
	for key, want := range map[string]string{
		"OPENAI_API_KEY":   "sk-plain-1",
		"DEEPSEEK_API_KEY": "sk-quoted-2",
	} {
		got, ok, err := s.Get(key)
		if err != nil || !ok {
			t.Fatalf("Get %s: ok=%v err=%v", key, ok, err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	// The plaintext file must be gone, and its values must never have landed
	// on disk in the store file.
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatalf("plaintext file still exists after successful migration (stat err=%v)", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "secrets.enc.json"))
	if err != nil {
		t.Fatalf("ReadFile store: %v", err)
	}
	if strings.Contains(string(b), "sk-plain-1") || strings.Contains(string(b), "sk-quoted-2") {
		t.Fatal("plaintext leaked into the store file")
	}
}

func TestMigrateEnvFileMissingIsNoop(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "secrets.enc.json"))
	n, err := s.MigrateEnvFile(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil || n != 0 {
		t.Fatalf("MigrateEnvFile(missing) = (%d, %v), want (0, nil)", n, err)
	}
}

func TestMigrateEnvFileKeepsExistingStoreValues(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "credentials")
	os.WriteFile(envPath, []byte("OPENAI_API_KEY=from-file\n"), 0o600)

	s := New(filepath.Join(dir, "secrets.enc.json"))
	// The user re-entered a newer key after upgrade: migration must not clobber it.
	if err := s.Set("OPENAI_API_KEY", "from-store"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if n, err := s.MigrateEnvFile(envPath); err != nil || n != 0 {
		t.Fatalf("MigrateEnvFile = (%d, %v), want (0, nil)", n, err)
	}
	got, _, _ := s.Get("OPENAI_API_KEY")
	if got != "from-store" {
		t.Fatalf("store value clobbered: %q", got)
	}
	// All entries were accounted for, so the file is still removed.
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Fatal("plaintext file kept despite a fully-covered migration")
	}
}
