package netdev

import (
	"os"
	"path/filepath"
	"testing"
)

// stateHistTestEnv points the netdev state dir (and thus the state-history
// store root) at a fresh temp dir for one test.
func stateHistTestEnv(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	prev := netdevStateDirOverr
	netdevStateDirOverr = filepath.Join(base, "netdev")
	t.Cleanup(func() { netdevStateDirOverr = prev })
	return base
}

func saveTestProposal(t *testing.T, id, status string) {
	t.Helper()
	if err := SaveProposal(&Proposal{ID: id, Status: status, Intent: "test"}); err != nil {
		t.Fatal(err)
	}
}

// Snapshot before a mutation, mutate, suffix-restore returns the file to its
// pre-event content; the restore itself leaves a redoable reverse event.
func TestStateHistoryRestoreRoundtripAndRedo(t *testing.T) {
	stateHistTestEnv(t)
	saveTestProposal(t, "P-1", ProposalDraft)
	path := filepath.Join(ProposalsDir(), "P-1.json")

	id := StateEventSnap(StateEventApprove, "P-1", StateActorUser, path)
	if id < 0 {
		t.Fatal("no event recorded")
	}
	saveTestProposal(t, "P-1", ProposalApproved)

	metas := StateEventMetas()
	if len(metas) != 1 {
		t.Fatalf("events = %d, want 1", len(metas))
	}
	m := metas[0]
	if m.Kind != StateEventApprove || m.Entity != "P-1" || m.Actor != StateActorUser || !m.CanRestore {
		t.Fatalf("meta = %+v", m)
	}
	if len(m.Paths) != 1 || !filepath.IsAbs(path) && m.Paths[0] == "" {
		t.Fatalf("paths = %v", m.Paths)
	}

	res, err := StateRestore(id, StateActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Written) != 1 {
		t.Fatalf("written = %v, want 1 path", res.Written)
	}
	if res.ReverseEventID < 0 {
		t.Fatalf("no reverse event: %+v", res)
	}
	got, err := GetProposal("P-1")
	if err != nil || got.Status != ProposalDraft {
		t.Fatalf("after restore status = %v (err %v), want draft", got, err)
	}

	// The restore wrote a redoable reverse event as the newest timeline entry.
	metas = StateEventMetas()
	if metas[0].Kind != StateEventRestoreKeep || !metas[0].CanRedo {
		t.Fatalf("newest = %+v, want redoable restore-keep", metas[0])
	}

	// Redo = restore back to the reverse event.
	if _, err := StateRestore(metas[0].ID, StateActorUser); err != nil {
		t.Fatal(err)
	}
	got, err = GetProposal("P-1")
	if err != nil || got.Status != ProposalApproved {
		t.Fatalf("after redo status = %v (err %v), want approved", got, err)
	}
}

// A file absent at snapshot time is recorded as a create marker; restoring
// deletes it again.
func TestStateHistoryCreateMarkerDeletesOnRestore(t *testing.T) {
	base := stateHistTestEnv(t)
	path := filepath.Join(netdevStateDir(), "topology-design.json")

	id := StateEventSnap(StateEventTopo, "", StateActorUser, path)
	if id < 0 {
		t.Fatal("no event recorded for absent file")
	}
	if err := os.MkdirAll(netdevStateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"nodes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := StateRestore(id, StateActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 {
		t.Fatalf("deleted = %v, want the created file", res.Deleted)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "nothing")); err == nil {
		t.Fatal("unrelated")
	}
}

// Live entities (executing proposal / running job / holding cutover) touched by
// a candidate suffix make the event unrestorable, in both the preview and the
// restore-time re-check.
func TestStateHistoryLiveEntityBlocks(t *testing.T) {
	stateHistTestEnv(t)
	saveTestProposal(t, "P-live", ProposalExecuting)

	path := filepath.Join(ProposalsDir(), "P-live.json")
	id := StateEventSnap(StateEventRollback, "P-live", StateActorUser, path)
	if id < 0 {
		t.Fatal("no event")
	}

	metas := StateEventMetas()
	if metas[0].CanRestore {
		t.Fatalf("executing proposal must block restore: %+v", metas[0])
	}
	if len(metas[0].Live) != 1 || metas[0].Live[0].Type != "proposal" || metas[0].Live[0].ID != "P-live" {
		t.Fatalf("live = %+v", metas[0].Live)
	}
	if _, err := StateRestore(id, StateActorUser); err == nil {
		t.Fatal("StateRestore must refuse while the proposal is executing")
	}
}

// Redo is only offered while the restore-keep event is the newest one; any
// later event disables it (a redo would collateral-revert that event's suffix).
func TestStateHistoryRedoGuardNewestOnly(t *testing.T) {
	stateHistTestEnv(t)
	saveTestProposal(t, "P-1", ProposalDraft)
	p1 := filepath.Join(ProposalsDir(), "P-1.json")
	StateEventSnap(StateEventApprove, "P-1", StateActorUser, p1)
	saveTestProposal(t, "P-1", ProposalApproved)

	if _, err := StateRestore(StateEventMetas()[0].ID, StateActorUser); err != nil { // only the approve event so far
		t.Fatal(err)
	}

	// A later, unrelated event lands on top → redo gone.
	saveTestProposal(t, "P-2", ProposalDraft)
	StateEventSnap(StateEventPropose, "P-2", StateActorAgent, filepath.Join(ProposalsDir(), "P-2.json"))

	metas := StateEventMetas() // newest first: propose, restore-keep, approve
	if len(metas) != 3 {
		t.Fatalf("events = %d, want 3", len(metas))
	}
	if metas[1].Kind != StateEventRestoreKeep || metas[1].CanRedo {
		t.Fatalf("restore-keep must not be redoable once newer events exist: %+v", metas[1])
	}
	if !metas[2].CanRestore || metas[2].Kind != StateEventApprove {
		t.Fatalf("older event still restorable: %+v", metas[2])
	}
}

// config.toml lives beside the netdev dir, under the same snapshot root, and
// participates in events like any state file.
func TestStateHistoryCoversConfigBesideStateDir(t *testing.T) {
	base := stateHistTestEnv(t)
	cfg := filepath.Join(base, "config.toml")
	if err := os.WriteFile(cfg, []byte("[netdev]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	id := StateEventSnap(StateEventSettings, "", StateActorUser, cfg)
	if id < 0 {
		t.Fatal("config.toml must be snapshottable (same root as netdev dir)")
	}
	if err := os.WriteFile(cfg, []byte("[netdev]\ndevices = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StateRestore(id, StateActorUser); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfg)
	if string(b) != "[netdev]\n" {
		t.Fatalf("config.toml = %q, want original", string(b))
	}
}
