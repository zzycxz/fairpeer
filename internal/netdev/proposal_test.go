package netdev

import (
	"context"
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

	done, err := m.ExecuteProposal(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if done.Status != ProposalDone || !done.Steps[0].Applied {
		t.Fatalf("proposal = %+v", done)
	}
	if !strings.Contains(done.Steps[0].Backup, "current-configuration snapshot") {
		t.Fatalf("backup not captured: %.80s", done.Steps[0].Backup)
	}

	// After done: rollback runs the authored plan and returns to draft.
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
			{Device: "sw1", Commands: []string{"reboot"}, Rollback: []string{"no-op"}}, // simulator rejects: error output
		},
	}
	if err := m.ValidateProposal(p); err != nil {
		t.Fatal(err)
	}
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApproveProposal(p.ID, false); err != nil {
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
