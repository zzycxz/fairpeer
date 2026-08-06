package installsource

// manifest.go persists a per-scope record of skills installed via
// install_source (SPEC v2 §3.4B). Each entry captures the source URL/path and
// the content hash at install time, enabling: (a) an audit trail of where each
// skill came from, (b) duplicate-install detection beyond the existing
// name-conflict check, and (c) the foundation for a future remote update-check
// (compare installed_hash to the current remote hash).
//
// The manifest is a single JSON file at <skills-root>/.installed.json, written
// best-effort (a missing/unreadable manifest degrades gracefully — installs
// still work, we just lose the provenance trail). It only tracks skills the
// installer itself wrote; hand-authored or builtin skills are not recorded.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// manifestFilename is the per-scope install provenance file, written into the
// skills root alongside the installed <name>/SKILL.md entries. The leading dot
// keeps it out of the skill discovery walk (scanSkillRoot only reads .md).
const manifestFilename = ".installed.json"

// ManifestEntry is one installed skill's provenance record.
type ManifestEntry struct {
	Source      string    `json:"source"`       // URL or local path the skill came from
	ContentHash string    `json:"content_hash"` // sha256 of the installed SKILL.md content
	InstalledAt time.Time `json:"installed_at"` // when install_source wrote it
	Scope       string    `json:"scope"`        // "project" | "global"
	Mode        string    `json:"mode"`         // "copy" | "link"
}

// Manifest is the on-disk map of skill-name → provenance. Loaded/saved per
// scope (project skills-root vs global skills-root).
type Manifest struct {
	Skills map[string]ManifestEntry `json:"skills"`
}

// contentHash returns the sha256 hex of a skill's content, used as the
// installed-version fingerprint. Two installs of the same source content share
// a hash; a changed upstream yields a different hash (the update signal).
func contentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// manifestPath returns the .installed.json location for a given skills root.
func manifestPath(skillsRoot string) string {
	return filepath.Join(skillsRoot, manifestFilename)
}

// loadManifest reads the per-scope manifest, returning an empty manifest when
// the file is missing or unreadable (graceful degradation — never block an
// install on a provenance read failure).
func loadManifest(skillsRoot string) Manifest {
	m := Manifest{Skills: map[string]ManifestEntry{}}
	b, err := os.ReadFile(manifestPath(skillsRoot))
	if err != nil {
		return m
	}
	// A corrupt manifest must not break installs; treat as empty. Unmarshal into
	// the Manifest wrapper (the file is {"skills": {...}}), not the bare map.
	var wrapper struct {
		Skills map[string]ManifestEntry `json:"skills"`
	}
	if err := json.Unmarshal(b, &wrapper); err != nil {
		return m
	}
	if wrapper.Skills != nil {
		m.Skills = wrapper.Skills
	}
	return m
}

// saveManifest writes the manifest atomically-enough (write-then-rename is not
// used to keep it dependency-free; a torn write loses provenance but never
// corrupts the installed skills themselves). Sorted keys for stable diffs.
func saveManifest(skillsRoot string, m Manifest) error {
	if m.Skills == nil {
		m.Skills = map[string]ManifestEntry{}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return err
	}
	return os.WriteFile(manifestPath(skillsRoot), b, 0o644)
}

// recordInstall adds/updates an entry in the manifest and persists it. Called
// after a successful skill copy/link apply. Best-effort: a write failure is
// returned but callers should treat it as non-fatal (log + continue).
func recordInstall(skillsRoot, name string, entry ManifestEntry) error {
	m := loadManifest(skillsRoot)
	m.Skills[name] = entry
	return saveManifest(skillsRoot, m)
}

// forgetInstall removes an entry from the manifest (called on uninstall).
// Missing name is a no-op. Best-effort like recordInstall.
func forgetInstall(skillsRoot, name string) error {
	m := loadManifest(skillsRoot)
	if _, ok := m.Skills[name]; !ok {
		return nil
	}
	delete(m.Skills, name)
	return saveManifest(skillsRoot, m)
}

// installedFrom returns the recorded source for a skill, or "" if not tracked.
// Used to surface "installed from <source>" in listings.
func installedFrom(skillsRoot, name string) string {
	m := loadManifest(skillsRoot)
	if e, ok := m.Skills[name]; ok {
		return e.Source
	}
	return ""
}

// stableMarshalManifest returns the manifest with sorted skill names so two
// writes of equivalent content produce byte-identical files (stable diffs in
// version control). Exposed for tests.
func stableMarshalManifest(m Manifest) ([]byte, error) {
	names := make([]string, 0, len(m.Skills))
	for n := range m.Skills {
		names = append(names, n)
	}
	sort.Strings(names)
	// Build an ordered map representation for stable JSON.
	type orderedEntry struct {
		Name  string `json:"name"`
		Entry ManifestEntry
	}
	var ordered []orderedEntry
	for _, n := range names {
		ordered = append(ordered, orderedEntry{Name: n, Entry: m.Skills[n]})
	}
	return json.MarshalIndent(struct {
		Skills []orderedEntry `json:"skills"`
	}{ordered}, "", "  ")
}
