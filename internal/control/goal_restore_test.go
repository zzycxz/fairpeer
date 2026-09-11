package control

import (
	"github.com/zzycxz/fairpeer/internal/event"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/agent"
)

// P1-E4: goal state rides the BranchMeta sidecar and Resume restores an
// interrupted auto-advancing goal (without auto-firing — the loop only engages
// after the user's next message).
func TestGoalPersistsAndRestores(t *testing.T) {
	dir := t.TempDir()
	path := agent.NewSessionPath(dir, "goal-e2e")
	seed := agent.NewSession("")
	if err := seed.Save(path); err != nil {
		t.Fatal(err)
	}

	c := newTestController(t, path)
	c.SetGoal("把所有测试修到绿")
	c.mu.Lock()
	c.goalTurns = 7
	c.mu.Unlock()
	c.syncModeToMeta(path)

	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("load meta: %v %v", err, ok)
	}
	if meta.Goal != "把所有测试修到绿" || meta.GoalStatus != GoalStatusRunning || meta.GoalTurns != 7 {
		t.Fatalf("sidecar goal fields = %+v", meta)
	}

	// Restore into a fresh controller (what Resume does after a restart).
	c2 := newTestController(t, path)
	c2.restoreModeFromMeta(path)
	g := c2.Goal()
	if g != "把所有测试修到绿" {
		t.Fatalf("restored goal = %q", g)
	}
	turns, max := c2.GoalTurns()
	if turns != 7 || max != maxGoalAutoTurns {
		t.Fatalf("restored budget = %d/%d, want 7/%d", turns, max, maxGoalAutoTurns)
	}

	// Clearing the goal clears the sidecar fields too.
	c2.SetGoal("")
	meta2, _, _ := agent.LoadBranchMeta(path)
	// "stopped" may persist (restore only engages on status==running) — the
	// goal TEXT must be gone so nothing resumes.
	if meta2.Goal != "" || meta2.GoalStatus == GoalStatusRunning {
		t.Fatalf("cleared goal still resumable on sidecar: %+v", meta2)
	}
	_ = strings.TrimSpace // keep import if assertions above change
}

// newTestController builds a minimal controller bound to a session path. The
// zero sink is fine — syncModeToMeta/restoreModeFromMeta only need sessionPath.
func newTestController(t *testing.T, path string) *Controller {
	t.Helper()
	return &Controller{sink: event.FuncSink(func(event.Event) {}), sessionPath: path}
}
