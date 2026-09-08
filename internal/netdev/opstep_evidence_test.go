package netdev

// opstep_evidence_test.go — NETDEV_OPSTEP_EVIDENCE_SPEC 红测试：appendOpStep
// 把台账行镜像成证据回执（伪路径 device:<name>），ok 行才带 Write/Success。

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zzycxz/fairpeer/internal/evidence"
)

func TestAppendOpStepMirrorsEvidenceReceipt(t *testing.T) {
	writeAuthTestEnv(t)
	m := &Manager{}
	ledger := evidence.NewLedger()
	ctx := evidence.WithLedger(context.Background(), ledger)

	m.appendOpStep(ctx, OpStep{
		At: "2026-09-08T22:00:00+08:00", Actor: "agent", Device: "SW-01",
		Command: "interface GigabitEthernet0/1", Status: "ok",
		PreID: "b1", PostID: "b2", DiffSummary: "+ description uplink",
	})
	m.appendOpStep(ctx, OpStep{
		At: "2026-09-08T22:01:00+08:00", Actor: "agent", Device: "SW-02",
		Command: "vlan 10", Status: "failure", Error: "connection lost",
	})

	if !ledger.HasSuccessfulWrite([]string{"device:SW-01"}) {
		t.Fatal("ok op-step row should verify as a device write receipt")
	}
	if ledger.HasSuccessfulWrite([]string{"device:SW-02"}) {
		t.Fatal("failure op-step row must never authorize a sign-off")
	}
	if ledger.HasSuccessfulWrite([]string{"device:SW-03"}) {
		t.Fatal("uncited device must not verify")
	}

	// Cross-turn query sees both rows on disk regardless of the ledger.
	rows := ListOpSteps("", 10)
	if len(rows) != 2 {
		t.Fatalf("op-step ledger rows = %d, want 2", len(rows))
	}
}

func TestAppendOpStepWithoutLedgerStillPersists(t *testing.T) {
	dir := t.TempDir()
	SetOpStepsDir(filepath.Join(dir, "opsteps"))
	t.Cleanup(func() { SetOpStepsDir("") })

	m := &Manager{}
	// No evidence ledger in ctx (e.g. headless sweep) — persistence only.
	m.appendOpStep(context.Background(), OpStep{Device: "SW-09", Command: "x", Status: "ok"})
	if rows := ListOpSteps("SW-09", 5); len(rows) != 1 {
		t.Fatalf("row should persist without an evidence ledger, got %d", len(rows))
	}
}
