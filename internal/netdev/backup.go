package netdev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	internaldiff "github.com/zzycxz/fairpeer/internal/diff"
	"github.com/zzycxz/fairpeer/internal/fileutil"
)

// backup.go — the configuration backup vault: sealed reads of each device's
// running-config snapshotted as immutable versions. Only REDACTED text is
// ever stored (the same text the model sees); raw configs live solely in the
// in-memory session buffer. Any two versions diff into a unified view — the
// "what changed" backbone for diagnosis (变更↔故障关联) and drift detection.

type BackupVersion struct {
	ID     string `json:"id"` // <device>@<unixnano>
	Device string `json:"device"`
	At     string `json:"at"`    // 01-02 15:04:05
	Bytes  int    `json:"bytes"` // redacted text length
	Lines  int    `json:"lines"`
}

type storedBackup struct {
	Device string
	Nanos  int64
	Text   string // redacted
}

var (
	backupsMu   sync.Mutex
	backupsOver string // test override
)

func SetBackupsDir(p string) {
	backupsMu.Lock()
	defer backupsMu.Unlock()
	backupsOver = p
}

func backupsDir() string {
	backupsMu.Lock()
	defer backupsMu.Unlock()
	if backupsOver != "" {
		return backupsOver
	}
	return filepath.Join(netdevStateDir(), "backups")
}

// RunBackup reads one device's (or every device's, device == "") running
// config through the sealed Exec path and files each as a new version.
// Returns the versions written, in device order.
func (m *Manager) RunBackup(ctx context.Context, device string) ([]BackupVersion, error) {
	targets := make([]struct{ name string }, 0)
	if strings.TrimSpace(device) != "" {
		targets = append(targets, struct{ name string }{device})
	} else {
		if len(m.cfg.NetDev.Devices) == 0 {
			return nil, fmt.Errorf("no devices configured")
		}
		for _, d := range m.cfg.NetDev.Devices {
			targets = append(targets, struct{ name string }{d.Name})
		}
	}
	var out []BackupVersion
	var problems []string
	for _, t := range targets {
		d, ok := m.cfg.NetDevDeviceByName(t.name)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: not in inventory", t.name))
			continue
		}
		drv, ok := m.driverFor(d)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: no driver (%s/%s)", t.name, d.Vendor, d.OS))
			continue
		}
		cmd, ok := RunningConfigCommand(drv.Key())
		if !ok {
			continue
		}
		res := m.Exec(ctx, t.name, cmd)
		if res.Refused {
			problems = append(problems, fmt.Sprintf("%s: refused (%s)", t.name, res.Class))
			continue
		}
		if res.IsError {
			problems = append(problems, fmt.Sprintf("%s: device error", t.name))
			continue
		}
		v, err := saveBackup(t.name, res.Output)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: save: %v", t.name, err))
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no backups written: %s", strings.Join(problems, "；"))
	}
	return out, nil
}

// backupIDMu + lastBackupNanos keep backup IDs strictly increasing: clock
// granularity on Windows (and rapid successive saves anywhere) can hand two
// saves the same nanosecond — identical IDs would silently overwrite the
// older version and empty the diff.
var (
	backupIDMu      sync.Mutex
	lastBackupNanos int64
)

func nextBackupNanos() int64 {
	backupIDMu.Lock()
	defer backupIDMu.Unlock()
	nanos := time.Now().UnixNano()
	if nanos <= lastBackupNanos {
		nanos = lastBackupNanos + 1
	}
	lastBackupNanos = nanos
	return nanos
}

func saveBackup(device, text string) (BackupVersion, error) {
	dir := backupsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return BackupVersion{}, err
	}
	nanos := nextBackupNanos()
	sb := storedBackup{Device: device, Nanos: nanos, Text: text}
	b, err := json.Marshal(sb)
	if err != nil {
		return BackupVersion{}, err
	}
	id := fmt.Sprintf("%s@%d", device, nanos)
	if err := fileutil.AtomicWriteFile(filepath.Join(dir, id+".json"), b, 0o600); err != nil {
		return BackupVersion{}, err
	}
	mirrorToGit(device, id)
	return BackupVersion{
		ID: id, Device: device,
		At:    time.Unix(0, nanos).Format("01-02 15:04:05"),
		Bytes: len(text), Lines: strings.Count(text, "\n") + 1,
	}, nil
}

// ── git 镜像审计层（WRITE_AUTHZ_SPEC §7.2） ──────────────────────────────────
//
// vault 保持运行时权威；镜像层把每次 saveBackup 顺手 commit 进备份目录的
// git 仓库——白拿内容寻址去重、blame 式审计、人类可读历史（git log -p
// 直接看每台设备的配置演进）。缺 git 二进制或任何失败都静默跳过：镜像
// 是审计增强，不是依赖（优雅降级）。

var (
	backupsGitMirrorMu sync.Mutex
	backupsGitMirror   bool
)

// SetBackupGitMirror toggles the mirror (boot/settings call it after config
// loads and after every settings save — same discipline as SetBackupsDir).
func SetBackupGitMirror(on bool) {
	backupsGitMirrorMu.Lock()
	backupsGitMirror = on
	backupsGitMirrorMu.Unlock()
}

func backupGitMirrorOn() bool {
	backupsGitMirrorMu.Lock()
	defer backupsGitMirrorMu.Unlock()
	return backupsGitMirror
}

func mirrorToGit(device, id string) {
	if !backupGitMirrorOn() {
		return
	}
	dir := backupsDir()
	git := func(args ...string) error {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fairpeer", "GIT_AUTHOR_EMAIL=netdev@fairpeer.local",
			"GIT_COMMITTER_NAME=fairpeer", "GIT_COMMITTER_EMAIL=netdev@fairpeer.local")
		return c.Run()
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err := git("init", "-q"); err != nil {
			return
		}
	}
	if err := git("add", "-A"); err != nil {
		return
	}
	_ = git("commit", "-q", "-m", "vault: "+id)
}

// FileDriftFindings compares each just-written version against the same
// device's PREVIOUS vault version and files a drift Finding when the config
// actually changed (WRITE_AUTHZ_SPEC §7.1 drift 看护——定时快照持续回答
// "谁改了什么"；正当变更由提案记录对账，这里的 finding 是线索不是定罪).
// Returns the findings filed.
func FileDriftFindings(vers []BackupVersion) []Finding {
	var out []Finding
	for _, v := range vers {
		all := ListBackups(v.Device) // newest first
		idx := -1
		for i, x := range all {
			if x.ID == v.ID {
				idx = i
				break
			}
		}
		if idx < 0 || idx+1 >= len(all) {
			continue // no previous version to compare against
		}
		prev := all[idx+1]
		diff, err := DiffBackups(v.Device, prev.ID, v.ID)
		if err != nil || diff == "" {
			continue
		}
		added, removed := 0, 0
		var head []string
		for _, l := range strings.Split(diff, "\n") {
			switch {
			case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
				continue
			case strings.HasPrefix(l, "+"):
				added++
			case strings.HasPrefix(l, "-"):
				removed++
			}
			if len(head) < 8 && (strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-")) {
				head = append(head, Redact(l))
			}
		}
		if added == 0 && removed == 0 {
			continue
		}
		f := &Finding{
			Title:    fmt.Sprintf("配置漂移：%s 较上一份快照 +%d/-%d 行", v.Device, added, removed),
			Severity: SeverityInfo,
			Source:   "drift:" + v.Device,
			Devices:  []string{v.Device},
			Detail: fmt.Sprintf("定时快照比对发现配置变化（prev=%s → now=%s）。若此变更未走提案流程，请核实来源。\n%s",
				prev.ID, v.ID, strings.Join(head, "\n")),
			// 无证据不立案是全局纪律——drift 的证据就是 vault 两版的 diff 本身。
			Evidence: []Evidence{{
				Device:  v.Device,
				Command: fmt.Sprintf("backup vault diff %s %s", prev.ID, v.ID),
				Output:  strings.Join(head, "\n"),
			}},
		}
		if err := SaveFinding(f); err == nil {
			out = append(out, *f)
		}
	}
	return out
}

// ListBackups returns a device's versions, newest first ("" = every device).
func ListBackups(device string) []BackupVersion {
	dir := backupsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []BackupVersion
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		var sb storedBackup
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || json.Unmarshal(data, &sb) != nil {
			continue
		}
		if device != "" && sb.Device != device {
			continue
		}
		out = append(out, BackupVersion{
			ID: fmt.Sprintf("%s@%d", sb.Device, sb.Nanos), Device: sb.Device,
			At:    time.Unix(0, sb.Nanos).Format("01-02 15:04:05"),
			Bytes: len(sb.Text), Lines: strings.Count(sb.Text, "\n") + 1,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// GetBackupText returns a version's redacted config text.
func GetBackupText(device, id string) (string, error) {
	if !validStoreID(id) {
		return "", fmt.Errorf("backup %s: invalid id", id)
	}
	dir := backupsDir()
	var sb storedBackup
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil || json.Unmarshal(data, &sb) != nil {
		return "", fmt.Errorf("backup %s: not found or unreadable", id)
	}
	if sb.Device != device {
		return "", fmt.Errorf("backup %s does not belong to device %s", id, device)
	}
	return sb.Text, nil
}

// DiffBackups returns the unified diff between two of a device's versions
// (a = old, b = new), reusing the repo's myers diff.
func DiffBackups(device, idA, idB string) (string, error) {
	oldText, err := GetBackupText(device, idA)
	if err != nil {
		return "", err
	}
	newText, err := GetBackupText(device, idB)
	if err != nil {
		return "", err
	}
	ch := internaldiff.Build(device+" running-config", oldText, newText, internaldiff.Modify)
	return ch.Diff, nil
}

// BackupDiffCurrent diffs a stored version (old) against the device's CURRENT
// running-config (new) — the restore-drafting backbone: "what must change to
// return to this version". The current side is a fresh sealed read (classifier
// / budget / redaction / audit all apply, same as any netdev_exec).
func (m *Manager) BackupDiffCurrent(ctx context.Context, device, id string) (string, error) {
	oldText, err := GetBackupText(device, id)
	if err != nil {
		return "", err
	}
	d, ok := m.cfg.NetDevDeviceByName(device)
	if !ok {
		return "", fmt.Errorf("device %q is not in the inventory", device)
	}
	drv, ok := m.driverFor(d)
	if !ok {
		return "", fmt.Errorf("%s: no driver (%s/%s)", device, d.Vendor, d.OS)
	}
	cmd, ok := RunningConfigCommand(drv.Key())
	if !ok {
		return "", fmt.Errorf("%s: platform %s has no running-config backup support", device, drv.Key())
	}
	res := m.Exec(ctx, device, cmd)
	if res.Refused {
		return "", fmt.Errorf("current-config read refused: %s", res.Refusal)
	}
	if res.IsError {
		return "", fmt.Errorf("%s: device error reading current config", device)
	}
	ch := internaldiff.Build(device+" running-config", oldText, res.Output, internaldiff.Modify)
	return ch.Diff, nil
}

// ValidateRestoreFrom checks a restore proposal's source version (备份→恢复
// 闭环): the id must exist in the vault, belong to its own device prefix, and
// that device must carry a step in the proposal — a restore proposal touches
// the device the version came from.
func ValidateRestoreFrom(id string, steps []ProposalStep) error {
	i := strings.LastIndexByte(id, '@')
	if i <= 0 {
		return fmt.Errorf("restore_from %q: not a backup version id (want device@nanos)", id)
	}
	dev := id[:i]
	if _, err := GetBackupText(dev, id); err != nil {
		return fmt.Errorf("restore_from: %v", err)
	}
	for _, s := range steps {
		if s.Device == dev {
			return nil
		}
	}
	return fmt.Errorf("restore_from: version %s belongs to device %q, which has no step in this proposal", id, dev)
}

// backupTool — netdev_backup: read-only access to the config backup vault.
// Actions: list a device's versions, read one version's redacted text,
// diff a version against the CURRENT running-config, or run a whole-network
// drift sweep (action=drift: snapshot every managed device now, diff each
// against its previous vault version, file drift Findings, return the
// summary). Restores NEVER execute here — they are drafted through
// netdev_propose (restore_from = the version id) and executed only by a
// human-approved proposal.
type backupTool struct{ m *Manager }

func (t *backupTool) Name() string { return "netdev_backup" }

func (t *backupTool) Description() string {
	return "Read the config backup vault (read-only). action=list: a device's versions, newest first. " +
		"action=read: one version's redacted config text. action=diff-current: unified diff between one version (old) and the device's CURRENT " +
		"running-config (fresh sealed read) — the backbone for drafting a RESTORE proposal. action=drift (no device needed): whole-network drift sweep — " +
		"snapshot every managed device NOW, diff each against its previous vault version, file drift Findings (source=drift:<device>), return the per-device " +
		"change summary (answers 谁改了什么 across the fleet). A restore never executes here: draft it with netdev_propose and set restore_from to the " +
		"version id; the human approves and executes the whole proposal."
}

func (t *backupTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"device": {"type": "string", "description": "device name from netdev_devices (not needed for action=drift)"},
			"action": {"type": "string", "enum": ["list", "read", "diff-current", "drift"], "description": "default list"},
			"id": {"type": "string", "description": "version id (device@nanos); required for read and diff-current"}
		},
		"required": []
	}`)
}

func (t *backupTool) ReadOnly() bool { return true }

func (t *backupTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Device string `json:"device"`
		Action string `json:"action"`
		ID     string `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	// action=drift 是全网 sweep（SKILL_ORCHESTRATION §3.4：确定性比对沉入
	// 本工具，config-vault 的批量段调它，不再是子代理的活）。
	if a.Action == "drift" {
		vers, err := t.m.RunBackup(ctx, "")
		if err != nil {
			return "", err
		}
		fs := FileDriftFindings(vers)
		var b strings.Builder
		fmt.Fprintf(&b, "drift sweep：快照 %d 台，变化 %d 台", len(vers), len(fs))
		if len(fs) == 0 {
			b.WriteString("（全部与上一版一致）")
		} else {
			b.WriteString("\n")
			for _, f := range fs {
				fmt.Fprintf(&b, "- %s\n", f.Title)
			}
			b.WriteString("每条变化已立案（source=drift:<device>，含 vault diff 证据）；恢复走 restore_from 提案。")
		}
		return b.String(), nil
	}
	if strings.TrimSpace(a.Device) == "" {
		return "", errors.New("netdev_backup: device is required (only action=drift runs without one)")
	}
	switch a.Action {
	case "", "list":
		versions := ListBackups(a.Device)
		if len(versions) == 0 {
			return "no backup versions for " + a.Device + " — backups are human/cycle triggered (立即备份 or backup_interval), not an agent action", nil
		}
		b, err := json.MarshalIndent(versions, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	case "read":
		if strings.TrimSpace(a.ID) == "" {
			return "", errors.New("netdev_backup: id is required for action=read")
		}
		text, err := GetBackupText(a.Device, a.ID)
		if err != nil {
			return "", err
		}
		_ = AppendAudit(Audit{Device: a.Device, Command: "backup-vault read " + a.ID, Class: "read", Status: AuditOK, OutputBytes: len(text)})
		return text, nil
	case "diff-current":
		if strings.TrimSpace(a.ID) == "" {
			return "", errors.New("netdev_backup: id is required for action=diff-current")
		}
		diff, err := t.m.BackupDiffCurrent(ctx, a.Device, a.ID)
		if err != nil {
			return "", err
		}
		_ = AppendAudit(Audit{Device: a.Device, Command: "backup-vault diff-current " + a.ID, Class: "read", Status: AuditOK, OutputBytes: len(diff)})
		return diff, nil
	default:
		return "", fmt.Errorf("netdev_backup: unknown action %q (list | read | diff-current)", a.Action)
	}
}
