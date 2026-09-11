package scheduler

import (
	"strings"
	"testing"
)

// Partition tests for the calendar/scheduler globalization: the store keeps
// ONE engine, tasks carry a profile cabinet, and the ForProfile surface is
// what the agent-facing tools see (the human UI keeps the unscoped methods).

func mkPartitionScheduler(t *testing.T) *Scheduler {
	t.Helper()
	s := New(t.TempDir() + "/sched.json")
	t.Cleanup(func() { s.Stop() })
	return s
}

func TestListForProfilePartitions(t *testing.T) {
	s := mkPartitionScheduler(t)
	for _, p := range []string{"dev", "cowork", "netdev"} {
		if _, err := s.Create(ScheduledTask{Name: "t-" + p, Expression: "daily 09:00", Prompt: "x", Profile: p}); err != nil {
			t.Fatal(err)
		}
	}
	// Legacy task with no profile: the scheduler's historical default is
	// cowork, so the empty value must land in the cowork cabinet.
	if _, err := s.Create(ScheduledTask{Name: "t-legacy", Expression: "daily 10:00", Prompt: "x"}); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ profile, want string }{
		{"dev", "t-dev"},
		{"netdev", "t-netdev"},
	} {
		got := s.ListForProfile(c.profile, false)
		if len(got) != 1 || got[0].Name != c.want {
			t.Errorf("ListForProfile(%q) = %v, want exactly [%s]", c.profile, got, c.want)
		}
	}
	// cowork: its own task PLUS the legacy "" task, and only those two.
	cow := s.ListForProfile("cowork", false)
	if len(cow) != 2 {
		t.Errorf("legacy empty-profile task should count as cowork: got %d cowork tasks", len(cow))
	}
	for _, tk := range cow {
		if tk.Name != "t-cowork" && tk.Name != "t-legacy" {
			t.Errorf("unexpected task in cowork partition: %q", tk.Name)
		}
	}
	// Case-insensitive partition keys.
	if got := s.ListForProfile("NetDev", false); len(got) != 1 {
		t.Errorf("ListForProfile is case-sensitive: got %d", len(got))
	}
	// The unscoped list still sees everything (human UI path).
	if got := s.List(false); len(got) != 4 {
		t.Errorf("unscoped List = %d, want 4", len(got))
	}
}

func TestMutationsArePartitionScoped(t *testing.T) {
	s := mkPartitionScheduler(t)
	cow, err := s.Create(ScheduledTask{Name: "cow", Expression: "daily 09:00", Prompt: "x", Profile: "cowork"})
	if err != nil {
		t.Fatal(err)
	}

	// Cross-partition delete reports absence and removes nothing.
	if s.DeleteForProfile(cow.ID, "netdev") {
		t.Fatal("netdev DeleteForProfile of a cowork task must report not-found")
	}
	if _, ok := s.Get(cow.ID); !ok {
		t.Fatal("cross-partition delete removed the task")
	}

	// Cross-partition update is not-found, not a permission error.
	if _, err := s.UpdateForProfile(cow.ID, "netdev", func(t *ScheduledTask) { t.Name = "hijacked" }); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-partition update: want not-found, got %v", err)
	}
	if got, _ := s.Get(cow.ID); got.Name != "cow" {
		t.Errorf("cross-partition update mutated the task: %q", got.Name)
	}

	// Cross-partition RunNow is not-found; same-partition works (no runner →
	// "skipped", which still appends history — enough to prove routing).
	if _, err := s.RunNowForProfile(cow.ID, "dev"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-partition RunNow: want not-found, got %v", err)
	}
	if _, err := s.RunNowForProfile(cow.ID, "cowork"); err != nil {
		t.Fatalf("same-partition RunNow: %v", err)
	}
	if recs := s.HistoryForProfile("cowork", ""); len(recs) != 1 {
		t.Errorf("history after run: %d records, want 1", len(recs))
	}
	if recs := s.HistoryForProfile("dev", ""); len(recs) != 0 {
		t.Errorf("dev history should not see the cowork run: %v", recs)
	}
	// The unscoped history keeps everything (human UI path).
	if recs := s.History(""); len(recs) != 1 {
		t.Errorf("unscoped history = %d, want 1", len(recs))
	}
}

func TestRunRecordCarriesProfile(t *testing.T) {
	s := mkPartitionScheduler(t)
	task, err := s.Create(ScheduledTask{Name: "ndv", Expression: "daily 09:00", Prompt: "x", Profile: "netdev"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunNowForProfile(task.ID, "netdev"); err != nil {
		t.Fatal(err)
	}
	recs := s.History("")
	if len(recs) != 1 || recs[0].Profile != "netdev" {
		t.Fatalf("run record should carry the task's partition: %+v", recs)
	}
}

func TestCreateRecordsSource(t *testing.T) {
	s := mkPartitionScheduler(t)
	agentTask, err := s.Create(ScheduledTask{Name: "a", Expression: "daily 09:00", Prompt: "x", Source: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if agentTask.Source != "agent" {
		t.Errorf("Source = %q, want agent", agentTask.Source)
	}
	uiTask, err := s.Create(ScheduledTask{Name: "m", Expression: "daily 09:00", Prompt: "x", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if uiTask.Source != "manual" {
		t.Errorf("Source = %q, want manual", uiTask.Source)
	}
}
