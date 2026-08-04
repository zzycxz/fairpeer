package installsource

import (
	"os"
	"path/filepath"
	"testing"
)

// TestManifest_RecordAndLoad confirms an install is recorded and reloadable.
func TestManifest_RecordAndLoad(t *testing.T) {
	root := t.TempDir()
	entry := ManifestEntry{
		Source:      "https://example.com/skill.md",
		ContentHash: contentHash("skill body"),
		Scope:       "project",
		Mode:        "copy",
	}
	if err := recordInstall(root, "my-skill", entry); err != nil {
		t.Fatalf("recordInstall: %v", err)
	}
	// The manifest file exists.
	if _, err := os.Stat(manifestPath(root)); err != nil {
		t.Errorf("manifest file should exist: %v", err)
	}
	// Reload and confirm the entry.
	m := loadManifest(root)
	got, ok := m.Skills["my-skill"]
	if !ok {
		t.Fatal("recorded skill not found after reload")
	}
	if got.Source != entry.Source {
		t.Errorf("Source = %q, want %q", got.Source, entry.Source)
	}
	if got.ContentHash != entry.ContentHash {
		t.Errorf("ContentHash = %q, want %q", got.ContentHash, entry.ContentHash)
	}
}

// TestManifest_Forget confirms uninstall removes the entry.
func TestManifest_Forget(t *testing.T) {
	root := t.TempDir()
	if err := recordInstall(root, "gone", ManifestEntry{Source: "x", ContentHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := forgetInstall(root, "gone"); err != nil {
		t.Fatalf("forgetInstall: %v", err)
	}
	m := loadManifest(root)
	if _, ok := m.Skills["gone"]; ok {
		t.Errorf("forgotten skill should not be in manifest")
	}
}

// TestManifest_ForgetMissingIsNoop confirms forgetting a name that was never
// recorded (or already forgotten) is a no-op, not an error.
func TestManifest_ForgetMissingIsNoop(t *testing.T) {
	root := t.TempDir()
	if err := forgetInstall(root, "never-existed"); err != nil {
		t.Errorf("forgetting a missing skill should be a no-op, got %v", err)
	}
}

// TestManifest_LoadMissingReturnsEmpty confirms a missing manifest file yields
// an empty (usable) manifest, not an error — installs must never break on
// provenance read failure.
func TestManifest_LoadMissingReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	m := loadManifest(root)
	if len(m.Skills) != 0 {
		t.Errorf("missing manifest should yield empty Skills, got %d", len(m.Skills))
	}
}

// TestManifest_LoadCorruptReturnsEmpty confirms a corrupt manifest degrades to
// empty rather than breaking installs.
func TestManifest_LoadCorruptReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(manifestPath(root), []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := loadManifest(root)
	if len(m.Skills) != 0 {
		t.Errorf("corrupt manifest should degrade to empty, got %d skills", len(m.Skills))
	}
}

// TestManifest_InstalledFrom confirms the source lookup helper.
func TestManifest_InstalledFrom(t *testing.T) {
	root := t.TempDir()
	if err := recordInstall(root, "tracked", ManifestEntry{Source: "https://github.com/x/y/blob/skill.md"}); err != nil {
		t.Fatal(err)
	}
	if got := installedFrom(root, "tracked"); got != "https://github.com/x/y/blob/skill.md" {
		t.Errorf("installedFrom = %q, want the source URL", got)
	}
	if got := installedFrom(root, "untracked"); got != "" {
		t.Errorf("untracked skill should return empty source, got %q", got)
	}
}

// TestManifest_Overwrite confirms re-installing the same name overwrites the
// old entry (e.g. a content update changes the hash).
func TestManifest_Overwrite(t *testing.T) {
	root := t.TempDir()
	recordInstall(root, "s", ManifestEntry{Source: "old", ContentHash: "h1"})
	recordInstall(root, "s", ManifestEntry{Source: "new", ContentHash: "h2"})
	m := loadManifest(root)
	got := m.Skills["s"]
	if got.Source != "new" || got.ContentHash != "h2" {
		t.Errorf("overwrite should keep the latest entry, got %+v", got)
	}
}

// TestContentHash_Stable confirms the hash is deterministic.
func TestContentHash_Stable(t *testing.T) {
	if contentHash("abc") != contentHash("abc") {
		t.Error("content hash should be deterministic")
	}
	if contentHash("abc") == contentHash("abd") {
		t.Error("different content should yield different hashes")
	}
}

// TestManifest_PathIsHidden confirms the manifest filename starts with a dot so
// scanSkillRoot's .md-only walk never picks it up as a skill.
func TestManifest_PathIsHidden(t *testing.T) {
	p := manifestPath(filepath.Join("x", "skills"))
	base := filepath.Base(p)
	if base[:1] != "." {
		t.Errorf("manifest filename %q should start with a dot to stay hidden", base)
	}
}
