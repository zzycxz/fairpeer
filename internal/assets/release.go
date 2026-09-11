package assets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SkillVersion is bumped whenever the embedded ppt-auto payload changes in a way
// that should force a refresh of the released copy. Bump this when you update
// the embedded scripts/templates/SKILL.md and want existing users to get the
// new version on next launch.
const SkillVersion = "53" // 53: SKILL.md evidence examples de-personalized (placeholders instead of dev C:\ paths) + analyze_template/export_previews honest platform guards for macOS/Linux // 52: color pipeline rework — hue-family clustering + Office-default theme discount in extract_template_colors (new colors.brand slot), merge_vlm_style no longer lets VLM hexes displace exact extraction, preflight gains --preset + baseline snapshot restore + colors_source, china-mobile brand preset (references/brand-presets/), build_page_skeleton box-driven composite layouts (kpi_row/panel/composite) + slim chrome + brand-slot consumption in table/flow skeletons // 51: setup_python.sh uv-venv → python3-venv → --break-system-packages ladder (PEP 668) + real import verification before success // 50: setup_python.bat prefers py -3 over the broken python3 Store-alias stub and verifies the interpreter actually runs // 49: G2-6 projects dir env-redirect (FAIRPEER_PPT_PROJECTS_DIR) + one-time migration out of the skill tree // 48: decisions — CJK width is the sole overflow criterion (S-05 enforced), fast mode gains hard content-floor/overflow/overlap checks (region blocks exempt via metadata), config.py dead SVG_CONSTRAINTS removed (rules live in template_config.json)

// versionFileName is written into the released skill dir so we can tell whether
// the on-disk copy matches the embedded version.
const versionFileName = ".embedded-version"

// skillDirName is the directory name the skill is released under.
const skillDirName = "ppt-auto"

// embedRoot is the path prefix inside the embed.FS (the directory name passed
// to //go:embed, which must NOT start with a dot or Go rejects the directive).
// It differs from skillDirName (the release name) by design: the embed tree is
// a build-time staging dir, the release dir is what users see on disk.
const embedRoot = "pptauto"

// EnsurePPTAutoSkill releases the embedded ppt-auto skill to the user's global
// skills directory (~/.fairpeer/skills/ppt-auto) if it is missing or stale.
// It is idempotent: when the on-disk .embedded-version matches SkillVersion,
// it does nothing. On a version bump it overwrites the existing copy.
//
// The release target is the global-scope skill root that the skill store scans
// (internal/skill: Store.roots → home/.fairpeer/skills), so both the CLI and the
// desktop app discover the released skill without any discovery-code changes.
//
// A nil error is returned (best-effort): a failure to release is logged but does
// not abort startup, because the user may already have a working ppt-auto from a
// previous release or a manual install. The error is still propagated so callers
// can log it.
func EnsurePPTAutoSkill() error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return errors.New("assets: cannot determine user home dir")
	}
	dst := filepath.Join(home, ".fairpeer", "skills", skillDirName)

	// Skip if the on-disk copy is already at the embedded version.
	if current, ok := readVersion(dst); ok && current == SkillVersion {
		return nil
	}

	// Walk the embedded tree and write every entry under dst.
	if err := fs.WalkDir(pptauto, embedRoot, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		// Map the embed path (prefix "pptauto") onto the destination.
		rel := strings.TrimPrefix(path, embedRoot)
		rel = strings.TrimPrefix(rel, string(filepath.Separator))
		target := filepath.Join(dst, rel)
		if rel == "" {
			target = dst
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := pptauto.ReadFile(path)
		if rerr != nil {
			return fmt.Errorf("read embedded %s: %w", path, rerr)
		}
		if werr := os.MkdirAll(filepath.Dir(target), 0o755); werr != nil {
			return fmt.Errorf("mkdir for %s: %w", target, werr)
		}
		// Skip writing if the target already exists with identical content.
		// This avoids rewriting 11000+ icon files on every SkillVersion bump
		// when only scripts changed — the biggest startup-latency source.
		if existing, err := os.ReadFile(target); err == nil && bytes.Equal(existing, data) {
			return nil
		}
		// Write atomically-ish: write then chmod. Preserve executability for
		// shell scripts on POSIX (cosmetic on Windows).
		mode := os.FileMode(0o644)
		if shouldExec(rel) {
			mode = 0o755
		}
		if werr := os.WriteFile(target, data, mode); werr != nil {
			return fmt.Errorf("write %s: %w", target, werr)
		}
		return nil
	}); err != nil {
		return err
	}

	// Stamp the version so we skip the walk next launch unless the embedded
	// version changes. Best-effort: a failure here just means we re-walk once.
	_ = os.WriteFile(filepath.Join(dst, versionFileName), []byte(SkillVersion), 0o644)
	return nil
}

// MigratePPTProjects moves ppt-auto WORK PRODUCTS out of the released skill
// tree (skills/ppt-auto/projects) into ~/.fairpeer/ppt-projects and pins that
// location via FAIRPEER_PPT_PROJECTS_DIR for every child python process
// (SCENARIO_SPEC G2-6 — artifacts inside the skill dir die on version refresh
// or reinstall). Idempotent: existing destinations win, leftovers are skipped.
func MigratePPTProjects() error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return errors.New("assets: cannot determine user home dir")
	}
	dst := filepath.Join(home, ".fairpeer", "ppt-projects")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	os.Setenv("FAIRPEER_PPT_PROJECTS_DIR", dst)

	old := filepath.Join(home, ".fairpeer", "skills", "ppt-auto", "projects")
	entries, err := os.ReadDir(old)
	if err != nil {
		return nil // nothing to migrate (or already gone) — fine
	}
	for _, e := range entries {
		target := filepath.Join(dst, e.Name())
		if _, err := os.Stat(target); err == nil {
			continue // destination exists — keep it, drop the stale copy
		}
		_ = os.Rename(filepath.Join(old, e.Name()), target)
	}
	// Remove the old dir when empty (best-effort; a failed rename leaves it).
	if empty, _ := isEmptyDir(old); empty {
		_ = os.Remove(old)
	}
	return nil
}

func isEmptyDir(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// PPTAutoSkillDir returns the absolute path where EnsurePPTAutoSkill releases
// the embedded skill (~/.fairpeer/skills/ppt-auto), regardless of whether it
// has been released yet. Useful for callers that want the canonical location.
func PPTAutoSkillDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("assets: cannot determine user home dir")
	}
	return filepath.Join(home, ".fairpeer", "skills", skillDirName), nil
}

// PPTAutoTemplatesDir returns the released skill's templates/ directory, or ""
// if it doesn't exist. Used by the settings page to surface bundled templates.
func PPTAutoTemplatesDir() string {
	dir, err := PPTAutoSkillDir()
	if err != nil {
		return ""
	}
	t := filepath.Join(dir, "templates")
	if _, err := os.Stat(t); err == nil {
		return t
	}
	return ""
}

// PPTAutoConfigPath returns the released skill's template_config.json path, or ""
// if it doesn't exist. Used by the settings page to read/update PPT config.
func PPTAutoConfigPath() string {
	dir, err := PPTAutoSkillDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(dir, "template_config.json")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// SyncPPTMode writes the user's ppt_mode setting (from config.toml) into the
// released template_config.json's "mode" field. This must be called after
// EnsurePPTAutoSkill at startup, because the embedded config ships with
// mode="fast" and would overwrite the user's "validate" choice on every
// SkillVersion bump.
func SyncPPTMode(pptMode string) error {
	if pptMode != "fast" && pptMode != "validate" {
		return nil // ignore invalid values
	}
	configPath := PPTAutoConfigPath()
	if configPath == "" {
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	cfg["mode"] = pptMode
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0o644)
}

// readVersion reads the .embedded-version marker from a released skill dir.
// Returns ("", false) if the dir or marker is absent.
func readVersion(skillDir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(skillDir, versionFileName))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// shouldExec reports whether a released file should be marked executable.
// Only the shell setup script needs the bit; .bat is a no-op on Windows.
func shouldExec(rel string) bool {
	return strings.HasSuffix(rel, ".sh")
}

// helperScriptsDir is where EnsureHelperScripts releases the embedded helper
// scripts (~/.fairpeer/scripts) — a stable location probed by
// docconv.ScriptCandidates regardless of where the binary runs from.
func helperScriptsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("assets: cannot determine user home dir")
	}
	return filepath.Join(home, ".fairpeer", "scripts"), nil
}

// EnsureHelperScripts releases the embedded helper scripts (scripts/ tree) to
// ~/.fairpeer/scripts/, writing each file only when missing or content differs
// (so edits to the embedded copy propagate on upgrade). Best-effort: errors
// surface to the caller but a missing script just means the Go-side fallback
// probes (CWD / exe-relative) still apply.
func EnsureHelperScripts() error {
	dir, err := helperScriptsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("assets: create %s: %w", dir, err)
	}
	return fs.WalkDir(scripts, "scripts", func(path string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		data, rerr := scripts.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dir, filepath.Base(path))
		if existing, ferr := os.ReadFile(target); ferr == nil && bytes.Equal(existing, data) {
			return nil // unchanged — skip the write
		}
		return os.WriteFile(target, data, 0o644)
	})
}
