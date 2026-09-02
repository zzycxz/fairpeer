package netdev

import "testing"

// TestMain disables the proposal-done baseline-recheck goroutine for the
// whole package (seam comment on proposalAutoRecheck): a finished proposal's
// async recheck must never outlive its test into a LATER test's overridden
// findings dir — it raced TestWatchDegradationRaisesFinding's finding-count
// assertions roughly one run in four. A test that exercises the hook itself
// re-enables the flag locally and restores it in t.Cleanup.
func TestMain(m *testing.M) {
	proposalAutoRecheck = false
	m.Run()
}
