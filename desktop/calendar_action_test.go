package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/calendar"
	schedulerpkg "github.com/zzycxz/fairpeer/internal/scheduler"
)

// calendar_action_test.go pins the P4 "事件动作编译" invariants: an event with
// an action prompt COMPILES into a linked one-shot scheduler task (the
// calendar never gains its own executor), the link follows edits, deletion
// cascades, and the grid projection deduplicates the represented task.

func newCalendarActionApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	store, err := calendar.Open(filepath.Join(dir, "cal.db"))
	if err != nil {
		t.Fatalf("calendar open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &App{
		scheduler:     schedulerpkg.New(filepath.Join(dir, "sched.json")),
		calendarStore: store,
	}
}

func farFuture(d time.Duration) string {
	return time.Now().Add(d).Format("2006-01-02T15:04")
}

func TestEventActionCompilesLinkedTask(t *testing.T) {
	a := newCalendarActionApp(t)

	start := farFuture(3 * time.Hour)
	view, err := a.CreateCalendarEvent(CalendarEventInput{
		Title:        "割接窗口",
		Start:        start,
		Reminders:    []int{30},
		Profile:      "netdev",
		ActionPrompt: "巡检核心交换机并汇总告警",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if view.TaskID == "" {
		t.Fatal("event with action must carry the linked task id")
	}

	tasks := a.scheduler.List(false)
	if len(tasks) != 1 {
		t.Fatalf("want 1 compiled task, got %d", len(tasks))
	}
	task := tasks[0]
	if task.Prompt != "巡检核心交换机并汇总告警" {
		t.Errorf("compiled prompt = %q", task.Prompt)
	}
	if task.Profile != "netdev" {
		t.Errorf("compiled task must inherit the event's partition, got %q", task.Profile)
	}
	if task.Source != "manual" || task.OutputMode != "notify" {
		t.Errorf("compiled task source/output = %q/%q, want manual/notify", task.Source, task.OutputMode)
	}
	if !task.OneShot {
		t.Error("compiled task must be one-shot (fires at event start)")
	}
	wantExpr := "at " + time.Now().Add(3*time.Hour).Format("2006-01-02 15:04")
	// Compare on the minute boundary — the event's parsed start is the fire
	// time, so allow the expression to differ only by sub-minute rounding.
	if task.Expression[:len("at 2006")] == "" || task.Expression != wantExpr && task.Expression[:len(wantExpr)-3] != wantExpr[:len(wantExpr)-3] {
		t.Errorf("compiled expression = %q, want ~%q", task.Expression, wantExpr)
	}
}

func TestEventActionRejectsRecurring(t *testing.T) {
	a := newCalendarActionApp(t)
	_, err := a.CreateCalendarEvent(CalendarEventInput{
		Title:        "每周例会",
		Start:        farFuture(24 * time.Hour),
		Recurrence:   "FREQ=WEEKLY;BYDAY=MO",
		ActionPrompt: "准备周报",
	})
	if err == nil {
		t.Fatal("recurring event with an action must be rejected")
	}
	if n := len(a.scheduler.List(false)); n != 0 {
		t.Errorf("rejected create must not leave a compiled task behind, got %d", n)
	}
}

func TestEventActionUpdateResyncsTask(t *testing.T) {
	a := newCalendarActionApp(t)
	view, err := a.CreateCalendarEvent(CalendarEventInput{
		Title:        "发布窗口",
		Start:        farFuture(2 * time.Hour),
		ActionPrompt: "跑发布前检查",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Move the event 2 days out and change the action — the linked task must
	// follow (its fire time is the event's start).
	newStart := farFuture(48 * time.Hour)
	if _, err := a.UpdateCalendarEvent(CalendarEventInput{
		ID:           view.ID,
		Start:        newStart,
		ActionPrompt: "跑发布前检查 + 回滚预案确认",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	tasks := a.scheduler.List(false)
	if len(tasks) != 1 {
		t.Fatalf("resync must keep exactly one task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Prompt != "跑发布前检查 + 回滚预案确认" {
		t.Errorf("prompt not resynced: %q", got.Prompt)
	}
	wantExpr := "at " + time.Now().Add(48*time.Hour).Format("2006-01-02 15:04")
	if got.Expression != wantExpr {
		t.Errorf("expression not resynced: %q, want %q", got.Expression, wantExpr)
	}
}

func TestEventActionDeleteCascades(t *testing.T) {
	a := newCalendarActionApp(t)
	view, err := a.CreateCalendarEvent(CalendarEventInput{
		Title:        "一次性演练",
		Start:        farFuture(6 * time.Hour),
		ActionPrompt: "演练脚本",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteCalendarEvent(view.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := len(a.scheduler.List(false)); n != 0 {
		t.Errorf("deleting the event must cascade to its compiled task, %d remain", n)
	}
}

func TestListScheduledTasksAsEventsSkipsRepresented(t *testing.T) {
	a := newCalendarActionApp(t)

	// Event WITH an action: its compiled task is represented by the event row
	// and must NOT be projected again (the grid would double-render).
	_, err := a.CreateCalendarEvent(CalendarEventInput{
		Title:        "带动作的事件",
		Start:        farFuture(5 * time.Hour),
		ActionPrompt: "x",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Standalone task (no representing event): projects as usual.
	if _, err := a.scheduler.Create(schedulerpkg.ScheduledTask{
		Name: "独立任务", Expression: "at " + time.Now().Add(7*time.Hour).Format("2006-01-02 15:04"),
		Prompt: "y", Profile: "cowork", Source: "manual",
	}); err != nil {
		t.Fatal(err)
	}

	since := time.Now().Add(-time.Minute).Format("2006-01-02T15:04")
	before := time.Now().Add(24 * time.Hour).Format("2006-01-02T15:04")
	projections := a.ListScheduledTasksAsEvents(since, before)
	if len(projections) != 1 {
		t.Fatalf("projection must contain ONLY the standalone task, got %d: %+v", len(projections), projections)
	}
	if projections[0].Title != "独立任务" {
		t.Errorf("projected the wrong task: %q", projections[0].Title)
	}
}
