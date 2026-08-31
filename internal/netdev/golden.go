package netdev

// golden.go — Golden Config 漂移检测（NetBox 式意图状态的最小落地）：
// 每设备一份「期望配置」（golden，从某个备份版本一键设定，人拥有），检查时
// 现场拉一次 running-config（密封读 + 脱敏，复用 RunBackup），与 golden 做
// 行集合对比：多出的行 = 意外配置，缺少的行 = 丢失配置。漂移生成带生命周
// 期的 Finding（source=golden:<device>，条件清除自动恢复），人可像告警一样
// 处理。golden 文件本身存 state/golden/，内容是已脱敏的备份文本。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GoldenInfo describes one device's baseline for the UI.
type GoldenInfo struct {
	Set   bool   `json:"set"`
	At    string `json:"at"`
	Lines int    `json:"lines"`
}

// GoldenDir stores one baseline per device (<name>.conf + <name>.meta).
func GoldenDir() string {
	return filepath.Join(netdevStateDir(), "golden")
}

var goldenDirOverr string

func goldenDir() string {
	if goldenDirOverr != "" {
		return goldenDirOverr
	}
	return GoldenDir()
}

func goldenFile(device string) string { return filepath.Join(goldenDir(), device+".conf") }
func goldenMeta(device string) string { return filepath.Join(goldenDir(), device+".meta") }

// SetGoldenFromBackup copies one backup version's (already redacted) text as
// the device's baseline. Human-triggered from the 备份时间线.
func SetGoldenFromBackup(device, versionID string) error {
	if _, ok := deviceAndVersion(device, versionID); !ok {
		return fmt.Errorf("backup %q for device %q not found", versionID, device)
	}
	text, err := readBackupText(device, versionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(goldenDir(), 0o700); err != nil {
		return err
	}
	StateEventSnap(StateEventGolden, device, StateActorUser, goldenFile(device), goldenMeta(device))
	if err := os.WriteFile(goldenFile(device), []byte(text), 0o600); err != nil {
		return err
	}
	at := time.Now().Format(time.RFC3339)
	return os.WriteFile(goldenMeta(device), []byte(at), 0o600)
}

// GoldenInfoOf reports the baseline's presence for the timeline header.
func GoldenInfoOf(device string) GoldenInfo {
	info := GoldenInfo{}
	b, err := os.ReadFile(goldenMeta(device))
	if err == nil {
		info.Set = true
		info.At = strings.TrimSpace(string(b))
	}
	if t, err := os.ReadFile(goldenFile(device)); err == nil {
		info.Lines = len(goldenLines(string(t)))
	}
	return info
}

// goldenLines normalizes config text to a comparable line set.
func goldenLines(text string) []string {
	out := []string{}
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, " \t\r")
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		out = append(out, l)
	}
	return out
}

// GoldenDrift is one check's outcome for one device.
type GoldenDrift struct {
	Device  string
	Extra   []string // in running, not in golden (意外配置)
	Missing []string // in golden, not in running (丢失配置)
	Note    string   // e.g. offline fallback marker
}

// RunGoldenCheck snapshots the device's running-config (sealed read) and
// diffs it against the baseline. Fires/resolves the golden Finding by
// source, like the alert engine. device="" sweeps every device that has a
// baseline; returns the per-device drifts (empty drifts included so callers
// can report "clean").
func (m *Manager) RunGoldenCheck(ctx context.Context, device string) ([]GoldenDrift, error) {
	targets := []string{}
	if strings.TrimSpace(device) != "" {
		targets = append(targets, strings.TrimSpace(device))
	} else {
		for _, d := range m.cfg.NetDev.Devices {
			if GoldenInfoOf(d.Name).Set {
				targets = append(targets, d.Name)
			}
		}
	}
	var out []GoldenDrift
	active, _ := m.activeFindingsBySource()
	for _, name := range targets {
		gi := GoldenInfoOf(name)
		if !gi.Set {
			return nil, fmt.Errorf("device %q has no golden baseline — set one from the 备份时间线 first", name)
		}
		goldenText, err := os.ReadFile(goldenFile(name))
		if err != nil {
			return nil, err
		}
		// Fresh sealed snapshot (same path the backup scheduler uses); on
		// failure fall back to the latest stored backup so an unreachable
		// device is still auditable against its last known state.
		fresh := true
		if _, err := m.RunBackup(ctx, name); err != nil {
			fresh = false
		}
		latest := latestBackupID(name)
		if latest == "" {
			return nil, fmt.Errorf("device %q has no backup to compare against — run a backup first", name)
		}
		runningText, err := readBackupText(name, latest)
		if err != nil {
			return nil, err
		}
		drift := diffGolden(string(goldenText), runningText)
		out = append(out, drift)

		if !fresh {
			drift.Note = "（设备当前不可达，对比基于最近一次备份）"
		}
		src := "golden:" + name
		_, hasActive := active[src]
		if len(drift.Extra) > 0 || len(drift.Missing) > 0 {
			if !hasActive {
				m.fireGoldenFinding(src, drift)
				active[src] = "new"
			}
			_ = AppendAudit(Audit{Device: name, Command: "golden check", Class: "read", Status: "device-error", OutputBytes: len(drift.Extra) + len(drift.Missing), Error: fmt.Sprintf("drift: %d extra, %d missing", len(drift.Extra), len(drift.Missing))})
		} else {
			if hasActive {
				m.resolveFindingBySource(src)
				delete(active, src)
			}
			_ = AppendAudit(Audit{Device: name, Command: "golden check", Class: "read", Status: AuditOK})
		}
	}
	return out, nil
}

// diffGolden compares two config texts as line sets.
func diffGolden(golden, running string) GoldenDrift {
	gs := goldenLines(golden)
	rs := goldenLines(running)
	gset := map[string]bool{}
	for _, l := range gs {
		gset[l] = true
	}
	rset := map[string]bool{}
	for _, l := range rs {
		rset[l] = true
	}
	var d GoldenDrift
	for _, l := range rs {
		if !gset[l] {
			d.Extra = append(d.Extra, l)
		}
	}
	for _, l := range gs {
		if !rset[l] {
			d.Missing = append(d.Missing, l)
		}
	}
	sort.Strings(d.Extra)
	sort.Strings(d.Missing)
	return d
}

func (m *Manager) fireGoldenFinding(src string, drift GoldenDrift) {
	const maxLines = 20
	trunc := func(in []string) string {
		if len(in) > maxLines {
			return strings.Join(in[:maxLines], "\n") + fmt.Sprintf("\n…（共 %d 行）", len(in))
		}
		return strings.Join(in, "\n")
	}
	f := &Finding{
		Title:    fmt.Sprintf("[漂移] 运行配置偏离基线 @ %s", drift.Device),
		Severity: SeverityWarning,
		Devices:  []string{drift.Device},
		Detail:   fmt.Sprintf("与基线对比：意外配置 %d 行，丢失配置 %d 行（未授权变更或基线过期）。%s", len(drift.Extra), len(drift.Missing), drift.Note),
		Evidence: []Evidence{
			{Device: drift.Device, Command: "golden diff (running - baseline)", Output: trunc(drift.Extra)},
			{Device: drift.Device, Command: "golden diff (baseline - running)", Output: trunc(drift.Missing)},
		},
		Source: src,
		Status: "active",
	}
	_ = SaveFinding(f)
}

// ── 备份文件读取辅助（backup.go 的最小面） ──────────────────────────────────

func deviceAndVersion(device, versionID string) (storedBackup, bool) {
	for _, v := range ListBackups(device) {
		if v.ID == versionID {
			nanos := int64(0)
			fmt.Sscanf(strings.SplitN(v.ID, "@", 2)[1], "%d", &nanos)
			return storedBackup{Device: v.Device, Nanos: nanos}, true
		}
	}
	return storedBackup{}, false
}

func readBackupText(device, versionID string) (string, error) {
	sb, ok := deviceAndVersion(device, versionID)
	if !ok {
		return "", fmt.Errorf("backup %q not found for %s", versionID, device)
	}
	path := filepath.Join(backupsDir(), fmt.Sprintf("%s@%d.json", device, sb.Nanos))
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var stored storedBackup
	if err := json.Unmarshal(b, &stored); err != nil {
		return "", fmt.Errorf("backup %s decode: %w", versionID, err)
	}
	return stored.Text, nil
}

func latestBackupID(device string) string {
	vs := ListBackups(device)
	if len(vs) == 0 {
		return ""
	}
	return vs[0].ID
}
