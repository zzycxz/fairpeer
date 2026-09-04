package netdev

// escalate_test.go — escalation sweep logic with an injected clock.

import (
	"testing"
	"time"
)


// TestEscalationSweep exercises the pure sweep logic with an injected clock:
// old critical actives escalate once; resolved/young/non-critical never do.
func TestEscalationSweep(t *testing.T) {
	ResetEscalationStateForTest()

	// SaveFinding writes through to the findings dir: without the override
	// the fixtures land in the user's real state dir and light the sidebar
	// risk dot with fake criticals (found in the wild, 2026-09-04).
	oldFind := findingsDirOverr
	t.Cleanup(func() { findingsDirOverr = oldFind })
	findingsDirOverr = t.TempDir()

	old := &Finding{
		ID: "esc-old", Title: "link down", Severity: SeverityCritical,
		Status: FindingActive, CreatedAt: time.Now().Add(-2 * EscalationTimeout),
		Evidence: []Evidence{{Device: "sw1", Command: "show interface", Output: "down"}},
	}
	if err := SaveFinding(old); err != nil {
		t.Fatal(err)
	}
	// Same finding resolved → never escalates.
	resolved := &Finding{
		ID: "esc-resolved", Title: "handled", Severity: SeverityCritical,
		Status: "resolved", CreatedAt: time.Now().Add(-2 * EscalationTimeout),
		Evidence: []Evidence{{Device: "sw1", Command: "show interface", Output: "down"}},
	}
	if err := SaveFinding(resolved); err != nil {
		t.Fatal(err)
	}
	// Young critical → not yet.
	young := &Finding{
		ID: "esc-young", Title: "fresh", Severity: SeverityCritical,
		Status: FindingActive, CreatedAt: time.Now(),
		Evidence: []Evidence{{Device: "sw1", Command: "show interface", Output: "up"}},
	}
	if err := SaveFinding(young); err != nil {
		t.Fatal(err)
	}
	// Old but only warning → no.
	warn := &Finding{
		ID: "esc-warn", Title: "warn", Severity: SeverityWarning,
		Status: FindingActive, CreatedAt: time.Now().Add(-2 * EscalationTimeout),
		Evidence: []Evidence{{Device: "sw1", Command: "show interface", Output: "up"}},
	}
	if err := SaveFinding(warn); err != nil {
		t.Fatal(err)
	}

	sweepEscalationsAt(time.Now())
	escalateMu.Lock()
	got := escalatedIDs["esc-old"]
	none := !escalatedIDs["esc-resolved"] && !escalatedIDs["esc-young"] && !escalatedIDs["esc-warn"]
	escalateMu.Unlock()
	if !got {
		t.Fatal("old critical active not escalated")
	}
	if !none {
		t.Fatal("resolved/young/warning findings escalated")
	}

	// Second sweep: once-only guard.
	sweepEscalationsAt(time.Now())
	escalateMu.Lock()
	if len(escalatedIDs) != 1 {
		t.Fatalf("escalation repeated or leaked: %v", escalatedIDs)
	}
	escalateMu.Unlock()
}
