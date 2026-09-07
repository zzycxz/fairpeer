package netdev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

func proposalTestManager(t *testing.T) *Manager {
	t.Helper()
	sim := startSimDevice(t)
	host, portStr, _ := splitHostPortT(sim.addr)
	var port int
	fmtSscan(portStr, &port)
	m, _ := testManager(t, sim) // provides device "sw1" + isolated audit/known_hosts

	// Add a core group with read-only policy containing sw1? No — sw1 must be
	// proposal-able. Instead register a second (inventory-only) device in a
	// read-only group for policy tests.
	m.cfg.NetDev.Groups = []config.NetDevGroup{
		{Name: "core", Policy: config.NetDevPolicyReadOnly},
		{Name: "open", Policy: config.NetDevPolicyProposal},
		{Name: "strict", Policy: config.NetDevPolicyProposalConf, ChangeWindow: "sun 00:00-23:59"},
	}
	m.cfg.NetDev.Devices = append(m.cfg.NetDev.Devices, config.NetDevDevice{
		Name: "ro-device", Vendor: "huawei", OS: "vrp8",
		Address: host, Port: port, Group: "core",
	})
	_ = m
	return m
}

func draftProposal(m *Manager) *Proposal {
	return &Proposal{
		Intent: "add VLAN 100 on access switches",
		Steps: []ProposalStep{{
			Device:   "sw1",
			Commands: []string{"vlan 100", "description IoT"},
			Rollback: []string{"undo vlan 100"},
		}},
	}
}

func TestProposalValidate(t *testing.T) {
	m := proposalTestManager(t)

	if err := m.ValidateProposal(draftProposal(m)); err != nil {
		t.Fatalf("valid draft rejected: %v", err)
	}

	noRollback := draftProposal(m)
	noRollback.Steps[0].Rollback = nil
	if err := m.ValidateProposal(noRollback); err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("draft without rollback accepted: %v", err)
	}

	ro := draftProposal(m)
	ro.Steps[0].Device = "ro-device"
	if err := m.ValidateProposal(ro); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only group accepted: %v", err)
	}

	ghost := draftProposal(m)
	ghost.Steps[0].Device = "ghost"
	if err := m.ValidateProposal(ghost); err == nil {
		t.Fatal("unknown device accepted")
	}
}

func TestChangeWindow(t *testing.T) {
	if _, err := parseChangeWindow("garbage"); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := parseChangeWindow("tue,thu 22:00-21:00"); err == nil {
		t.Fatal("inverted range accepted")
	}
	w, err := parseChangeWindow("tue,thu 22:00-24:00")
	if err != nil {
		t.Fatal(err)
	}
	tueNight := time.Date(2026, 8, 18, 23, 0, 0, 0, time.Local) // a Tuesday
	tueNoon := time.Date(2026, 8, 18, 12, 0, 0, 0, time.Local)
	if !w.contains(tueNight) {
		t.Fatal("Tue 23:00 should be inside")
	}
	if w.contains(tueNoon) {
		t.Fatal("Tue 12:00 should be outside")
	}
	var nilW *changeWindow
	if !nilW.contains(tueNoon) {
		t.Fatal("nil window = always open")
	}
}

// Full happy path: draft → approve → execute → done, through the SIMULATED
// CLI (backup + writes land on the fake device, each audited).
func TestProposalLifecycle(t *testing.T) {
	m := proposalTestManager(t)
	p := draftProposal(m)
	if err := m.ValidateProposal(p); err != nil {
		t.Fatal(err)
	}
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Fatal("no id assigned")
	}

	approved, err := m.ApproveProposal(p.ID, false)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != ProposalApproved {
		t.Fatalf("status = %s", approved.Status)
	}

	// Double-approve refused.
	if _, err := m.ApproveProposal(p.ID, false); err == nil {
		t.Fatal("double approve accepted")
	}

	watching, err := m.ExecuteProposal(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Full success enters the observation period (watching), never bare done:
	// the 30-min auto-close goroutine, CloseProposalWatch and the degradation
	// checks all key off watching.
	if watching.Status != ProposalWatching || !watching.Steps[0].Applied {
		t.Fatalf("proposal = %+v", watching)
	}
	if watching.WatchUntil == nil || !watching.WatchUntil.After(time.Now()) {
		t.Fatalf("watch deadline = %v", watching.WatchUntil)
	}
	if !strings.Contains(watching.Steps[0].Backup, "current-configuration snapshot") {
		t.Fatalf("backup not captured: %.80s", watching.Steps[0].Backup)
	}

	// After the run: rollback runs the authored plan and returns to draft.
	rb, err := m.RollbackProposal(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Status != ProposalDraft || rb.Steps[0].Applied {
		t.Fatalf("rollback result = %+v", rb)
	}

	// Only drafts re-approve — re-approve then check list persistence.
	if _, err := m.ApproveProposal(p.ID, false); err != nil {
		t.Fatalf("re-approve after rollback: %v", err)
	}
	list, err := ListProposals()
	if err != nil || len(list) == 0 {
		t.Fatalf("list = %v err = %v", list, err)
	}
}

// A failing step mid-proposal freezes the rest: partial, nothing after the
// failure applied, human decides.
func TestProposalPartialFreeze(t *testing.T) {
	m := proposalTestManager(t)
	p := &Proposal{
		Intent: "two devices, second fails",
		Steps: []ProposalStep{
			{Device: "sw1", Commands: []string{"vlan 100"}, Rollback: []string{"undo vlan 100"}},
			{Device: "sw1", Commands: []string{"reboot"}, Rollback: []string{"no-op"}}, // simulator rejects: error output; §7.1 dangerous verb ⇒ confirm2
		},
	}
	if err := m.ValidateProposal(p); err != nil {
		t.Fatal(err)
	}
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApproveProposal(p.ID, true); err != nil {
		t.Fatal(err)
	}
	part, err := m.ExecuteProposal(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if part.Status != ProposalPartial {
		t.Fatalf("status = %s, want partial (frozen)", part.Status)
	}
	if !part.Steps[0].Applied || part.Steps[1].Applied {
		t.Fatalf("applied flags wrong: %+v", part.Steps)
	}
	if part.Steps[1].Error == "" {
		t.Fatal("failure not recorded")
	}
	if !strings.Contains(part.Note, "human decides") {
		t.Fatalf("note = %q", part.Note)
	}

	// Rollback unwinds the APPLIED step only.
	rb, err := m.RollbackProposal(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rb.Status != ProposalDraft || rb.Steps[0].Applied {
		t.Fatalf("rollback after partial = %+v", rb)
	}
}

// A command failing MID-STEP leaves the executed prefix on the device: the
// step records AppliedCmds (how many commands landed) and the rollback runs
// for it — the old Applied-only check skipped the step and the prefix stayed.
func TestProposalMidStepFailureRollbackRunsForPrefix(t *testing.T) {
	m := proposalTestManager(t)
	p := &Proposal{
		Intent: "second command fails mid-step",
		Steps: []ProposalStep{{
			Device:   "sw1",
			Commands: []string{"vlan 100", "reboot"}, // sim: vlan ok, reboot rejected
			Rollback: []string{"undo vlan 100"},
		}},
	}
	if err := m.ValidateProposal(p); err != nil {
		t.Fatal(err)
	}
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApproveProposal(p.ID, true); err != nil { // reboot ⇒ dangerous ⇒ confirm2
		t.Fatal(err)
	}
	part, err := m.ExecuteProposal(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if part.Status != ProposalPartial {
		t.Fatalf("status = %s, want partial (frozen)", part.Status)
	}
	s := part.Steps[0]
	if s.Applied || s.AppliedCmds != 1 {
		t.Fatalf("applied=%v appliedCmds=%d — the executed prefix must be recorded", s.Applied, s.AppliedCmds)
	}
	if !strings.Contains(s.Error, "1/2") {
		t.Fatalf("error = %q — must record how much of the step applied", s.Error)
	}

	// Rollback covers the partially-applied step; a clean unwind clears the
	// prefix count alongside Applied.
	rb, err := m.RollbackProposal(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Status != ProposalDraft || rb.Steps[0].Applied || rb.Steps[0].AppliedCmds != 0 {
		t.Fatalf("rollback after mid-step failure = %+v", rb.Steps[0])
	}
}

// helper shims (avoid importing fmt in this file's helpers twice).
func splitHostPortT(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	return addr[:i], addr[i+1:], nil
}

func fmtSscan(s string, v *int) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	*v = n
}

// TestProposalRejectDelete covers the human veto (completion-spec §4.1):
// draft/approved rejectable with a persisted reason the agent's next turn
// reads; live-pipeline states refuse both veto and delete.
func TestProposalRejectDelete(t *testing.T) {
	m := proposalTestManager(t)

	// Reject a draft with a reason.
	p := draftProposal(m)
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}
	rejected, err := m.RejectProposal(p.ID, "回滚命令覆盖不完整，重写后再提")
	if err != nil {
		t.Fatalf("reject draft: %v", err)
	}
	if rejected.Status != ProposalRejected || rejected.RejectReason == "" || !strings.Contains(rejected.Note, "驳回") {
		t.Fatalf("rejected = %+v", rejected)
	}
	// Persisted: reload sees the reason (the agent's visibility path).
	again, err := GetProposal(p.ID)
	if err != nil || again.RejectReason != "回滚命令覆盖不完整，重写后再提" {
		t.Fatalf("reason not persisted: %v %+v", err, again)
	}
	// Rejected proposals cannot be approved (terminal).
	if _, err := m.ApproveProposal(p.ID, false); err == nil {
		t.Fatal("approve after reject accepted")
	}

	// Approved-but-unexecuted can also be vetoed.
	p2 := draftProposal(m)
	if err := SaveProposal(p2); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApproveProposal(p2.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RejectProposal(p2.ID, "change window 临近，改期再执行"); err != nil {
		t.Fatalf("reject approved: %v", err)
	}

	// Delete: draft and rejected are removable.
	if err := m.DeleteProposal(p.ID); err != nil {
		t.Fatalf("delete rejected: %v", err)
	}
	if _, err := GetProposal(p.ID); err == nil {
		t.Fatal("deleted proposal still readable")
	}
	// Approved is live pipeline — delete refused.
	p3 := draftProposal(m)
	if err := SaveProposal(p3); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApproveProposal(p3.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteProposal(p3.ID); err == nil || !strings.Contains(err.Error(), "live pipeline") {
		t.Fatalf("delete approved accepted: %v", err)
	}
}

// A crash mid-execution leaves status=executing forever — every lifecycle op
// refuses it. The lazy sweep in ListProposals turns an executing file that
// went quiet for staleExecutingAge into partial with a manual-verify note;
// fresh executions and ones with a live in-flight transition stay untouched.
func TestRecoverStaleExecuting(t *testing.T) {
	proposalsDirOverride = filepath.Join(t.TempDir(), "proposals")
	t.Cleanup(func() { proposalsDirOverride = "" })
	netdevStateDirOverr = t.TempDir()
	t.Cleanup(func() { netdevStateDirOverr = "" })

	stale := &Proposal{ID: "P-stale-1", Status: ProposalExecuting, Intent: "runner crashed"}
	if err := SaveProposal(stale); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-staleExecutingAge - 5*time.Minute)
	if err := os.Chtimes(filepath.Join(proposalsDirOverride, "P-stale-1.json"), old, old); err != nil {
		t.Fatal(err)
	}

	fresh := &Proposal{ID: "P-fresh-1", Status: ProposalExecuting, Intent: "running now"}
	if err := SaveProposal(fresh); err != nil {
		t.Fatal(err)
	}

	// A claimed in-flight transition is a LIVE run — old mtime or not.
	busy := &Proposal{ID: "P-busy-1", Status: ProposalExecuting, Intent: "slow but alive"}
	if err := SaveProposal(busy); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(proposalsDirOverride, "P-busy-1.json"), old, old); err != nil {
		t.Fatal(err)
	}
	if !claimProposalInflight("P-busy-1") {
		t.Fatal("claim inflight")
	}
	t.Cleanup(func() { releaseProposalInflight("P-busy-1") })

	if _, err := ListProposals(); err != nil {
		t.Fatal(err)
	}
	got, _ := GetProposal("P-stale-1")
	if got.Status != ProposalPartial || !strings.Contains(got.Note, "runner died") {
		t.Fatalf("stale executing not recovered: status=%s note=%q", got.Status, got.Note)
	}
	if got, _ = GetProposal("P-fresh-1"); got.Status != ProposalExecuting {
		t.Fatalf("fresh executing wrongly recovered: %+v", got)
	}
	if got, _ = GetProposal("P-busy-1"); got.Status != ProposalExecuting {
		t.Fatalf("in-flight execution wrongly recovered: %+v", got)
	}
}

// The proposal sequence is in-memory: a same-day restart used to regenerate
// P<date>-1… and SaveProposal silently overwrote the existing file. The ID
// generator re-seeds from the highest suffix on disk, so today's ids continue
// past whatever an earlier process already created.
func TestNewProposalIDResumesPastExisting(t *testing.T) {
	proposalsDirOverride = filepath.Join(t.TempDir(), "proposals")
	t.Cleanup(func() { proposalsDirOverride = "" })
	// Simulate the fresh process (and leave a clean counter for later tests).
	proposalMu.Lock()
	proposalSeq, proposalSeqDay = 0, ""
	proposalMu.Unlock()
	t.Cleanup(func() {
		proposalMu.Lock()
		proposalSeq, proposalSeqDay = 0, ""
		proposalMu.Unlock()
	})

	// Yesterday's files must not influence today's numbering.
	if err := SaveProposal(&Proposal{ID: "P20200101-9", Status: ProposalDraft, Intent: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveProposal(&Proposal{ID: "P" + time.Now().Format("20060102") + "-1", Status: ProposalDraft, Intent: "seeded today"}); err != nil {
		t.Fatal(err)
	}

	want := "P" + time.Now().Format("20060102") + "-2"
	if got := newProposalID(); got != want {
		t.Fatalf("newProposalID = %q, want %q (restart must not collide with today's -1)", got, want)
	}

	// The next generated id continues the sequence without clobbering seeds.
	p := &Proposal{Intent: "new process's first proposal", Status: ProposalDraft}
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}
	if p.ID != "P"+time.Now().Format("20060102")+"-3" {
		t.Fatalf("SaveProposal id = %q, want today's -3 (continuing past -2)", p.ID)
	}
	seeded, err := GetProposal("P" + time.Now().Format("20060102") + "-1")
	if err != nil || seeded.Intent != "seeded today" {
		t.Fatalf("seeded proposal overwritten: %+v (%v)", seeded, err)
	}
}
