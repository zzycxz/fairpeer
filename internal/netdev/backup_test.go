package netdev

import (
	"context"
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
