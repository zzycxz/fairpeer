// Package knowledge is the externalized knowledge-data store for the
// blue-team entry forms (BLUETEAM_SKILL_SPEC §5.2 / SKILL_ORCHESTRATION_SPEC
// §3.5-D): the heavy tables (segment priors, credential spots, host-risk
// checks) live in DATA files, skill bodies carry only the engine.
//
// Resolution order for one id (e.g. "segment-priors"):
//  1. <stateDir>/user-knowledge/<id>.yaml — the user's override (wins)
//  2. <stateDir>/knowledge/<id>.yaml      — the released builtin copy
//     (embedded data is written there on first use / version bump)
//
// Users edit user-knowledge/ without being clobbered by upgrades; builtins
// refresh freely. Validate() enforces each file's schema so a bad hand edit
// fails loudly (and testably) instead of silently steering the engine.
package knowledge

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed data/*.yaml
var embedded embed.FS

// stateDirOverride isolates the release root in tests.
var stateDirOverride string

// SetStateDir overrides the release root (tests).
func SetStateDir(p string) { stateDirOverride = p }

func stateDir() string {
	if stateDirOverride != "" {
		return stateDirOverride
	}
	base := os.Getenv("FAIRPEER_STATE_DIR") // tests / portable installs
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ".fairpeer-netdev"
		}
		base = filepath.Join(home, ".fairpeer")
	}
	return filepath.Join(base, "netdev")
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// EnsureReleased writes the embedded knowledge files into <stateDir>/knowledge/
// (first use, and whenever the embedded version is newer by content hash).
// Returns the release dir.
func EnsureReleased() (string, error) {
	dir := filepath.Join(stateDir(), "knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	entries, err := embedded.ReadDir("data")
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		want, err := embedded.ReadFile("data/" + e.Name())
		if err != nil {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		if got, err := os.ReadFile(dst); err == nil && bytes.Equal(got, want) {
			continue // already current
		}
		if err := os.WriteFile(dst, want, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// IDs lists the shipped knowledge ids (file stem).
func IDs() []string {
	entries, err := embedded.ReadDir("data")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	return out
}

// Load resolves one knowledge file: user override first, released builtin
// second. Missing id → error naming both candidate paths (teach the fix).
func Load(id string) ([]byte, error) {
	if !idRe.MatchString(id) {
		return nil, fmt.Errorf("knowledge: bad id %q", id)
	}
	user := filepath.Join(stateDir(), "user-knowledge", id+".yaml")
	if b, err := os.ReadFile(user); err == nil {
		return b, nil
	}
	if dir, err := EnsureReleased(); err == nil {
		if b, err := os.ReadFile(filepath.Join(dir, id+".yaml")); err == nil {
			return b, nil
		}
	}
	if b, err := embedded.ReadFile("data/" + id + ".yaml"); err == nil {
		return b, nil // fall back to embedded even when release failed
	}
	return nil, fmt.Errorf("knowledge %q not found (looked in user-knowledge/ and knowledge/ under %s)", id, stateDir())
}

// LoadBuiltin reads ONLY the embedded copy — the fallback when a user
// override exists but is corrupt (the engine degrades to builtin and
// surfaces the error; it never runs with zero rules).
func LoadBuiltin(id string) ([]byte, error) {
	if !idRe.MatchString(id) {
		return nil, fmt.Errorf("knowledge: bad id %q", id)
	}
	return embedded.ReadFile("data/" + id + ".yaml")
}

// SaveUser validates and stores a user override (批 3 知识反哺通道：轮内
// 确认的判据先落 user-knowledge/，升级不冲掉；验证不过整份拒收)。
func SaveUser(id string, data []byte) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("knowledge: bad id %q", id)
	}
	// 用 IDs() 纯内存判定 known-id——Load 探测会触发 EnsureReleased 落盘，
	// 拒绝路径不应带写副作用。
	known := false
	for _, knownID := range IDs() {
		if knownID == id {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("knowledge: unknown id %q (pick one of: %s)", id, strings.Join(IDs(), ", "))
	}
	if err := Validate(id, data); err != nil {
		return err
	}
	dir := filepath.Join(stateDir(), "user-knowledge")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".yaml"), data, 0o600)
}

// Path returns the LOAD path to teach in a body (informational; Load is the API).
func Path(id string) string {
	return filepath.Join(stateDir(), "user-knowledge", id+".yaml (覆盖) / "+filepath.Join(stateDir(), "knowledge", id+".yaml"))
}

// ── schema validation ────────────────────────────────────────────────────────

// Validate parses and schema-checks one knowledge file's YAML. Every shipped
// file must pass; the CI red test runs it over IDs(). User overrides surface
// the same errors through Validate (the settings/save path can call it).
func Validate(id string, data []byte) error {
	switch id {
	case "segment-priors":
		var s struct {
			Version           int               `yaml:"version"`
			Source            string            `yaml:"source"`
			GatewayCandidates []string          `yaml:"gateway_candidates"`
			SamplePoints      []string          `yaml:"sample_points"`
			MinAlive          int               `yaml:"min_alive"`
			TTLMap            map[string]string `yaml:"ttl_map"`
			RoleSignals       []struct {
				Ports           []string `yaml:"ports"`
				PrinterOUIDense bool     `yaml:"printer_oui_dense"`
				SNMPHit         bool     `yaml:"snmp_community_hit"`
				Role            string   `yaml:"role"`
				Action          string   `yaml:"action"`
			} `yaml:"role_signals"`
		}
		if err := yaml.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("segment-priors: %v", err)
		}
		if s.Version < 1 {
			return fmt.Errorf("segment-priors: version is required")
		}
		if len(s.GatewayCandidates) == 0 || len(s.SamplePoints) == 0 {
			return fmt.Errorf("segment-priors: gateway_candidates and sample_points are required")
		}
		if s.MinAlive < 1 {
			return fmt.Errorf("segment-priors: min_alive must be >= 1")
		}
		if len(s.TTLMap) == 0 {
			return fmt.Errorf("segment-priors: ttl_map is required")
		}
		for i, r := range s.RoleSignals {
			if r.Role == "" || r.Action == "" {
				return fmt.Errorf("segment-priors: role_signals[%d] needs role+action", i)
			}
			if len(r.Ports) == 0 && !r.PrinterOUIDense && !r.SNMPHit {
				return fmt.Errorf("segment-priors: role_signals[%d] needs a signal (ports/printer_oui_dense/snmp_community_hit)", i)
			}
		}
		return nil
	case "credential-spots":
		var s struct {
			Version int `yaml:"version"`
			Spots   []struct {
				ID       string            `yaml:"id"`
				OS       []string          `yaml:"os"`
				Check    map[string]string `yaml:"check"`
				Positive string            `yaml:"positive"`
			} `yaml:"spots"`
		}
		if err := yaml.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("credential-spots: %v", err)
		}
		if s.Version < 1 || len(s.Spots) == 0 {
			return fmt.Errorf("credential-spots: version and spots are required")
		}
		for i, sp := range s.Spots {
			if sp.ID == "" || sp.Positive == "" || len(sp.Check) == 0 {
				return fmt.Errorf("credential-spots: spots[%d] needs id+check+positive", i)
			}
			for _, osName := range sp.OS {
				if _, ok := sp.Check[osName]; !ok {
					return fmt.Errorf("credential-spots: spots[%d] (%s) has no check for os %q", i, sp.ID, osName)
				}
			}
		}
		return nil
	case "host-risk-checks":
		var s struct {
			Version int `yaml:"version"`
			Checks  []struct {
				ID       string            `yaml:"id"`
				OS       []string          `yaml:"os"`
				Check    map[string]string `yaml:"check"`
				Positive string            `yaml:"positive"`
				Fallback string            `yaml:"fallback"`
			} `yaml:"checks"`
		}
		if err := yaml.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("host-risk-checks: %v", err)
		}
		if s.Version < 1 || len(s.Checks) == 0 {
			return fmt.Errorf("host-risk-checks: version and checks are required")
		}
		for i, c := range s.Checks {
			if c.ID == "" || c.Positive == "" || c.Fallback == "" {
				return fmt.Errorf("host-risk-checks: checks[%d] needs id+positive+fallback (攻防同条目)", i)
			}
			if len(c.Check) == 0 {
				return fmt.Errorf("host-risk-checks: checks[%d] (%s) needs a check command (string or per-os map)", i, c.ID)
			}
		}
		return nil
	case "baseline-rules":
		var s struct {
			Version int    `yaml:"version"`
			Source  string `yaml:"source"`
			Drivers map[string][]struct {
				ID       string `yaml:"id"`
				Title    string `yaml:"title"`
				Severity string `yaml:"severity"`
				FixType  string `yaml:"fix_type"`
				FixRef   string `yaml:"fix_ref"`
				Pattern  string `yaml:"pattern"`
				Absence  bool   `yaml:"absence"`
				Presence string `yaml:"presence"`
				Hint     string `yaml:"hint"`
			} `yaml:"drivers"`
		}
		if err := yaml.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("baseline-rules: %v", err)
		}
		if s.Version < 1 || len(s.Drivers) == 0 {
			return fmt.Errorf("baseline-rules: version and drivers are required")
		}
		for drv, rules := range s.Drivers {
			if len(rules) == 0 {
				return fmt.Errorf("baseline-rules: driver %q has no rules", drv)
			}
			for i, r := range rules {
				if r.ID == "" || r.Title == "" {
					return fmt.Errorf("baseline-rules: %s rules[%d] needs id+title", drv, i)
				}
				switch r.Severity {
				case "info", "warning", "critical":
				default:
					return fmt.Errorf("baseline-rules: %s rules[%d] (%s) severity must be info|warning|critical", drv, i, r.ID)
				}
				if r.Absence {
					if r.Presence == "" {
						return fmt.Errorf("baseline-rules: %s rules[%d] (%s) absence rule needs presence", drv, i, r.ID)
					}
					if _, err := regexp.Compile(r.Presence); err != nil {
						return fmt.Errorf("baseline-rules: %s rules[%d] (%s) presence regex: %v", drv, i, r.ID, err)
					}
					continue
				}
				if r.Pattern == "" {
					return fmt.Errorf("baseline-rules: %s rules[%d] (%s) needs pattern (or absence+presence)", drv, i, r.ID)
				}
				if _, err := regexp.Compile(r.Pattern); err != nil {
					return fmt.Errorf("baseline-rules: %s rules[%d] (%s) pattern regex: %v", drv, i, r.ID, err)
				}
			}
		}
		return nil
	default:
		// Unknown ids: require at least version + a top-level table so a
		// user-added file can't be an arbitrary document.
		var probe map[string]json.RawMessage
		if err := yaml.Unmarshal(data, &probe); err != nil {
			return fmt.Errorf("%s: %v", id, err)
		}
		if len(probe) < 2 {
			return fmt.Errorf("%s: no schema for this id; require version + at least one table (or pick a known id)", id)
		}
		return nil
	}
}
