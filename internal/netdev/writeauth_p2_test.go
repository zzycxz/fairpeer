package netdev

// writeauth_p2_test.go — WRITE_AUTHZ P2 后端红测试：finding 代数（L4 合同
// 校验的地基）、drift 看护立案、git 镜像（有 git 才跑）、OpStep Turn 锚定。

import (
	"os/exec"

	"github.com/zzycxz/fairpeer/internal/config"
	"strings"
	"testing"
	"time"
)

func TestFindingGenBumpsOnSave(t *testing.T) {
	writeAuthTestEnv(t)
	before := FindingGen()
	f := &Finding{Title: "gen 测试", Severity: SeverityInfo, Source: "test:gen", Devices: []string{"sw1"}, Detail: "x",
		Evidence: []Evidence{{Device: "sw1", Command: "display version", Output: "stub"}}}
	if err := SaveFinding(f); err != nil {
		t.Fatalf("save: %v", err)
	}
	if FindingGen() != before+1 {
		t.Fatalf("FindingGen must bump by exactly 1 per save: %d -> %d", before, FindingGen())
	}
}

func TestDriftFindingsFiledOnChange(t *testing.T) {
	writeAuthTestEnv(t)
	v1, err := saveBackup("sw1", "sysname OLD\nvlan 10\n")
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	// 第一份快照没有上一版可比——不立案。
	if got := FileDriftFindings([]BackupVersion{v1}); len(got) != 0 {
		t.Fatalf("first snapshot must not file drift, got %d", len(got))
	}
	v2, err := saveBackup("sw1", "sysname NEW\nvlan 10\n")
	if err != nil {
		t.Fatalf("v2: %v", err)
	}
	fs := FileDriftFindings([]BackupVersion{v2})
	if len(fs) != 1 {
		t.Fatalf("changed snapshot must file exactly one drift finding, got %d", len(fs))
	}
	if !strings.Contains(fs[0].Source, "drift:sw1") || !strings.Contains(fs[0].Title, "+1/-1") {
		t.Fatalf("drift finding wrong: %+v", fs[0])
	}
	// 无变化不立案。
	v3, err := saveBackup("sw1", "sysname NEW\nvlan 10\n")
	if err != nil {
		t.Fatalf("v3: %v", err)
	}
	if got := FileDriftFindings([]BackupVersion{v3}); len(got) != 0 {
		t.Fatalf("unchanged snapshot must not file drift, got %d", len(got))
	}
}

func TestBackupGitMirrorCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary absent — mirror degrades gracefully by design")
	}
	writeAuthTestEnv(t)
	SetBackupGitMirror(true)
	t.Cleanup(func() { SetBackupGitMirror(false) })
	if _, err := saveBackup("sw1", "sysname A\n"); err != nil {
		t.Fatalf("save: %v", err)
	}
	dir := backupsDir()
	out, err := exec.Command("git", "-C", dir, "log", "--oneline").CombinedOutput()
	if err != nil {
		t.Fatalf("git log failed (mirror did not commit): %v: %s", err, out)
	}
	if !strings.Contains(string(out), "vault: sw1@") {
		t.Fatalf("commit message wrong: %s", out)
	}
}

func TestOpStepTurnAnchor(t *testing.T) {
	m := newWriteAuthManager(t, "auto", labDevice())
	m.TurnBegin() // turn 1
	m.appendOpStep(OpStep{Device: "sw1", Command: "sysname A", Status: "ok"})
	m.TurnBegin() // turn 2
	m.appendOpStep(OpStep{Device: "sw1", Command: "sysname B", Status: "ok"})
	steps := ListOpSteps("sw1", 10)
	if len(steps) != 2 {
		t.Fatalf("expected 2 ledger rows, got %d", len(steps))
	}
	// newest first: turn2 的行在前。
	if steps[0].Turn == steps[1].Turn {
		t.Fatalf("rows must carry distinct turn anchors: %d vs %d", steps[0].Turn, steps[1].Turn)
	}
	if steps[0].Turn <= 0 || steps[1].Turn <= 0 {
		t.Fatalf("turn anchors must be positive (bumped by TurnBegin): %d/%d", steps[0].Turn, steps[1].Turn)
	}
}

func TestAutoTierTimeboxDegrades(t *testing.T) {
	m := newWriteAuthManager(t, "auto", labDevice())
	if err := m.ConfirmWriteTier("sw1", "auto", "test"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// 无时间盒 → auto 生效。
	if got := m.EffectiveWriteTier(labDevice()); got != config.NetDevWriteAuto {
		t.Fatalf("no timebox → auto, got %s", got)
	}
	// 组加 1h 时间盒，确认发生在 2h 前（直接改存档伪造旧时间戳）。
	m.cfg.NetDev.Groups[0].AutoExpires = "1h"
	m.waMu.Lock()
	m.waConfirmed["sw1"] = confirmedLock{Tier: "auto", At: time.Now().Add(-2 * time.Hour), By: "test"}
	m.waMu.Unlock()
	if got := m.EffectiveWriteTier(labDevice()); got != config.NetDevWriteConfirm {
		t.Fatalf("elapsed timebox must degrade to confirm, got %s", got)
	}
	if w := m.WriteTierWarnings(); len(w) != 1 || !strings.Contains(w[0], "时间盒") {
		t.Fatalf("expected one timebox warning, got %v", w)
	}
}
