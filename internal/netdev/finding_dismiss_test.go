package netdev

import "testing"

// Dismiss/Clear hygiene: per-id delete rejects hostile ids, unknown ids error;
// clear-all removes exactly the persisted JSON files and reports the count.
func TestDismissAndClearFindings(t *testing.T) {
	dir := t.TempDir()
	oldFind := findingsDirOverr
	defer func() { findingsDirOverr = oldFind }()
	findingsDirOverr = dir

	a := &Finding{Title: "notify-test-warn", Severity: SeverityWarning,
		Devices: []string{"x"}, Source: "notify-test",
		Evidence: []Evidence{{Device: "x", Command: "test", Output: "o"}}}
	b := &Finding{Title: "baseline: telnet", Severity: SeverityCritical,
		Devices: []string{"sw-1"}, Source: "baseline:sw-1",
		Evidence: []Evidence{{Device: "sw-1", Command: "t", Output: "o"}}}
	if err := SaveFinding(a); err != nil {
		t.Fatal(err)
	}
	if err := SaveFinding(b); err != nil {
		t.Fatal(err)
	}

	if err := DismissFinding("../" + a.ID); err == nil {
		t.Fatal("path-shaped id must be rejected")
	}
	if err := DismissFinding(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := DismissFinding(a.ID); err == nil {
		t.Fatal("second dismiss of the same id must error")
	}
	left, err := ListFindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != b.ID {
		t.Fatalf("after dismiss want exactly %s, got %d findings", b.ID, len(left))
	}

	n, err := ClearFindings()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("clear removed %d, want 1", n)
	}
	if again, _ := ClearFindings(); again != 0 {
		t.Fatalf("clear on empty dir removed %d, want 0", again)
	}
}
