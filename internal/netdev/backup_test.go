package netdev

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Full path: backup reads go through the sealed Exec (audit rows appear) and
// only the REDACTED text lands in the vault.
func TestRunBackupSim(t *testing.T) {
	SetBackupsDir(t.TempDir())
	t.Cleanup(func() { SetBackupsDir("") })
	m, auditPath := testManager(t, startSimDevice(t))

	vers, err := m.RunBackup(context.Background(), "sw1")
	if err != nil {
		t.Fatalf("RunBackup: %v", err)
	}
	if len(vers) != 1 || vers[0].Device != "sw1" || vers[0].Lines < 1 {
		t.Fatalf("versions = %+v", vers)
	}
	if entries := readAudit(t, auditPath); len(entries) == 0 {
		t.Fatal("backup read left no audit row — must go through the sealed path")
	}

	list := ListBackups("sw1")
	if len(list) != 1 || list[0].ID != vers[0].ID {
		t.Fatalf("list = %+v", list)
	}
	if _, err := GetBackupText("sw1", vers[0].ID); err != nil {
		t.Fatalf("GetBackupText: %v", err)
	}
}

// Two versions → unified diff shows the changed lines and nothing else.
func TestDiffBackups(t *testing.T) {
	dir := t.TempDir()
	SetBackupsDir(dir)
	t.Cleanup(func() { SetBackupsDir("") })

	v1, err := saveBackup("sw1", "hostname sw1\nvlan 10\nntp-service unicast-server 10.0.0.253\n")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := saveBackup("sw1", "hostname sw1\nvlan 20\nntp-service unicast-server 10.0.0.253\n")
	if err != nil {
		t.Fatal(err)
	}
	d, err := DiffBackups("sw1", v1.ID, v2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "-vlan 10") || !strings.Contains(d, "+vlan 20") {
		t.Fatalf("diff missing changed lines:\n%s", d)
	}
	if strings.Contains(d, "-hostname") || strings.Contains(d, "+hostname") ||
		strings.Contains(d, "-ntp-service") || strings.Contains(d, "+ntp-service") {
		t.Fatalf("unchanged lines marked as changes:\n%s", d)
	}

	// Cross-device guard: a version belongs to its device.
	if _, err := GetBackupText("other", v1.ID); err == nil {
		t.Fatal("backup from another device was served")
	}
}

// All-devices sweep skips nothing silently: the dead port device lands in the
// problems, the live one is backed up.
func TestRunBackupAllDevices(t *testing.T) {
	SetBackupsDir(t.TempDir())
	t.Cleanup(func() { SetBackupsDir("") })
	m, _ := testManager(t, startSimDevice(t))

	vers, err := m.RunBackup(context.Background(), "")
	if err != nil || len(vers) != 1 || vers[0].Device != "sw1" {
		t.Fatalf("all-sweep = %+v err=%v (dead device expected in problems, sw1 backed up)", vers, err)
	}
}

// 备份→恢复闭环：diff-current 把库存版本与现拉 running-config 对上，且当前侧
// 走密封 Exec（审计可见）；restore_from 校验把来源版本钉死在它所属的设备上。
func TestBackupDiffCurrentAndRestoreFrom(t *testing.T) {
	SetBackupsDir(t.TempDir())
	t.Cleanup(func() { SetBackupsDir("") })
	m, auditPath := testManager(t, startSimDevice(t))

	// 先备份一版拿到现网文本，再造一个“多了一行 vlan 999”的旧版本，
	// 模拟“恢复到改坏之前”的起草场景。
	live, err := m.RunBackup(context.Background(), "sw1")
	if err != nil || len(live) != 1 {
		t.Fatalf("seed backup = %+v err=%v", live, err)
	}
	liveText, err := GetBackupText("sw1", live[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	vOld, err := saveBackup("sw1", liveText+"\nvlan 999\n")
	if err != nil {
		t.Fatal(err)
	}

	diff, err := m.BackupDiffCurrent(context.Background(), "sw1", vOld.ID)
	if err != nil {
		t.Fatalf("BackupDiffCurrent: %v", err)
	}
	if !strings.Contains(diff, "-vlan 999") {
		t.Fatalf("diff missing the restore delta (-vlan 999):\n%s", diff)
	}
	audited := false
	for _, a := range readAudit(t, auditPath) {
		if a.Command == "display current-configuration" && a.Status == AuditOK {
			audited = true // the current side was a sealed read, not a side door
		}
	}
	if !audited {
		t.Fatal("diff-current's live read left no audit row — must go through the sealed path")
	}

	// restore_from：好路径 + 三种拒绝（坏格式/库里没有/设备不在步骤里）。
	ok := []ProposalStep{{Device: "sw1", Commands: []string{"vlan 999"}, Rollback: []string{"undo vlan 999"}}}
	if err := ValidateRestoreFrom(vOld.ID, ok); err != nil {
		t.Fatalf("valid restore_from rejected: %v", err)
	}
	if err := ValidateRestoreFrom("no-at-sign", ok); err == nil {
		t.Fatal("malformed version id accepted")
	}
	if err := ValidateRestoreFrom("ghost@123", ok); err == nil {
		t.Fatal("version missing from the vault accepted")
	}
	wrongDev := []ProposalStep{{Device: "dead", Commands: []string{"vlan 999"}, Rollback: []string{"undo vlan 999"}}}
	if err := ValidateRestoreFrom(vOld.ID, wrongDev); err == nil {
		t.Fatal("restore version pinned to a device with no step in the proposal")
	}

	// 工具面：list/read/diff-current 三个动作都通，未知动作拒绝。
	tool := &backupTool{m: m}
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"device":"sw1","action":"diff-current","id":"`+vOld.ID+`"}`))
	if err != nil || !strings.Contains(out, "-vlan 999") {
		t.Fatalf("tool diff-current = %q err=%v", out, err)
	}
	if out, err = tool.Execute(context.Background(), json.RawMessage(`{"device":"sw1","action":"list"}`)); err != nil || !strings.Contains(out, vOld.ID) {
		t.Fatalf("tool list = %q err=%v", out, err)
	}
	if out, err = tool.Execute(context.Background(), json.RawMessage(`{"device":"sw1","action":"read","id":"`+vOld.ID+`"}`)); err != nil || !strings.Contains(out, "vlan 999") {
		t.Fatalf("tool read = %q err=%v", out, err)
	}
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"device":"sw1","action":"explode"}`)); err == nil {
		t.Fatal("unknown action accepted")
	}
}
