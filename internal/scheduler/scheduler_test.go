package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseExpression(t *testing.T) {
	valid := []string{
		"every 30m", "every 2h", "hourly",
		"daily 09:00", "daily 09:00 Mon-Fri", "daily 09:00 Mon,Wed,Fri",
		"daily 00:00", "daily 23:59 weekend",
		"0 9 * * 1-5", "*/15 * * * *", "0 0 1 * *",
	}
	for _, expr := range valid {
		if _, err := parseExpression(expr); err != nil {
			t.Errorf("parseExpression(%q): %v", expr, err)
		}
	}
	invalid := []string{
		"", "every", "every 30", "every 30x",
		"daily", "daily 9", "daily 25:00", "daily 09:00 Funday",
		"* * *", // wrong cron field count
	}
	for _, expr := range invalid {
		if _, err := parseExpression(expr); err == nil {
			t.Errorf("parseExpression(%q) should error, got nil", expr)
		}
	}
}

// every 1m is the floor; sub-minute must be rejected to prevent hot-looping.
func TestParseEveryMinimum(t *testing.T) {
	if _, err := parseExpression("every 30s"); err == nil {
		t.Error("sub-minute interval should be rejected")
	}
}

func TestNextRunEvery(t *testing.T) {
	from := time.Date(2026, 6, 22, 12, 0, 0, 0, time.Local)
	got := nextRun("every 30m", from)
	want := from.Add(30 * time.Minute)
	if !got.Equal(want) {
		t.Errorf("every 30m: got %v, want %v", got, want)
	}
}

func TestNextRunDailyToday(t *testing.T) {
	// 10:00 from 09:00 same day → fires today at 10:00.
	from := time.Date(2026, 6, 22, 9, 0, 0, 0, time.Local) // Monday
	got := nextRun("daily 10:00", from)
	want := time.Date(2026, 6, 22, 10, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("daily 10:00 (today): got %v, want %v", got, want)
	}
}

func TestNextRunDailyTomorrow(t *testing.T) {
	// 09:00 from 10:00 same day → fires tomorrow 09:00.
	from := time.Date(2026, 6, 22, 10, 0, 0, 0, time.Local) // Monday
	got := nextRun("daily 09:00", from)
	want := time.Date(2026, 6, 23, 9, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("daily 09:00 (tomorrow): got %v, want %v", got, want)
	}
}

func TestNextRunDailyWeekday(t *testing.T) {
	// Friday 10:00, daily 09:00 Mon-Fri → next is Monday 09:00 (skip weekend).
	from := time.Date(2026, 6, 26, 10, 0, 0, 0, time.Local) // Friday
	got := nextRun("daily 09:00 Mon-Fri", from)
	want := time.Date(2026, 6, 29, 9, 0, 0, 0, time.Local) // Monday
	if !got.Equal(want) {
		t.Errorf("daily 09:00 Mon-Fri from Fri: got %v, want %v", got, want)
	}
}

func TestNextRunCron(t *testing.T) {
	// "0 9 * * 1-5" = 09:00 weekdays. From Monday 10:00 → Tuesday 09:00.
	from := time.Date(2026, 6, 22, 10, 0, 0, 0, time.Local) // Monday
	got := nextRun("0 9 * * 1-5", from)
	want := time.Date(2026, 6, 23, 9, 0, 0, 0, time.Local) // Tuesday
	if !got.Equal(want) {
		t.Errorf("cron 0 9 * * 1-5: got %v, want %v", got, want)
	}
}

func TestCronExpand(t *testing.T) {
	cases := []struct {
		field     string
		min, max  int
		wantCount int
	}{
		{"*", 0, 59, 60},
		{"5", 0, 59, 1},
		{"1-5", 0, 59, 5},
		{"*/15", 0, 59, 4}, // 0,15,30,45
		{"1,5,10", 0, 59, 3},
	}
	for _, c := range cases {
		got := cronExpand(c.field, c.min, c.max)
		if len(got) != c.wantCount {
			t.Errorf("cronExpand(%q,%d,%d) = %v (count %d), want count %d", c.field, c.min, c.max, got, len(got), c.wantCount)
		}
	}
}

func TestSchedulerCreateListDelete(t *testing.T) {
	s := New(t.TempDir() + "/sched.json")
	task, err := s.Create(ScheduledTask{
		Name:       "test",
		Expression: "every 1h",
		Prompt:     "hello",
		ConfirmHighFrequency: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" {
		t.Fatal("Create should assign an ID")
	}
	all := s.List(false)
	if len(all) != 1 {
		t.Fatalf("List = %d tasks, want 1", len(all))
	}
	if !s.Delete(task.ID) {
		t.Fatal("Delete returned false")
	}
	if len(s.List(false)) != 0 {
		t.Fatal("task not deleted")
	}
}

// TestSchedulerPersistReload confirms tasks survive a store reload (restart).
func TestSchedulerPersistReload(t *testing.T) {
	path := t.TempDir() + "/sched.json"
	s1 := New(path)
	_, err := s1.Create(ScheduledTask{Name: "persisted", Expression: "daily 09:00", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	// New scheduler instance pointing at the same file.
	s2 := New(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	all := s2.List(false)
	if len(all) != 1 || all[0].Name != "persisted" {
		t.Fatalf("reload lost the task: %+v", all)
	}
}

// TestFireDueRespectsMidRunUpdate guards the BUG#2 fix: when fireDue has already
// captured a task to run but the user Updates its Expression during the run, the
// post-run NextRun write must honor the NEW Expression, not the stale captured
// one. We simulate the race deterministically by invoking fireDue directly with
// a fake runner that performs the Update mid-run.
func TestFireDueRespectsMidRunUpdate(t *testing.T) {
	s := New(t.TempDir() + "/sched.json")
	task, err := s.Create(ScheduledTask{Name: "racy", Expression: "every 1h", Prompt: "x", ConfirmHighFrequency: true})
	if err != nil {
		t.Fatal(err)
	}
	// Force the task to be "due" so fireDue picks it up.
	s.mu.Lock()
	s.tasks[0].NextRun = time.Now().Add(-time.Minute)
	s.mu.Unlock()

	// Runner that changes the Expression DURING the run (mirrors a user editing
	// while a 10-min prompt executes). New expr is "daily 05:00" — very different
	// next-fire from "every 1h".
	s.SetRunner(fakeRunner{fn: func(ctx context.Context, profile, prompt string) (string, error) {
		if _, err := s.Update(task.ID, func(t *ScheduledTask) {
			t.Expression = "daily 05:00"
		}); err != nil {
			t.Errorf("mid-run update failed: %v", err)
		}
		return "ran", nil
	}})

	s.fireDue(time.Now())

	got := s.List(false)[0]
	// NextRun must reflect daily 05:00 (the updated expr), NOT every 1h from the
	// stale captured expression. A 1h offset from now would be wrong; 05:00 today
	// or tomorrow is right.
	wantHour := 5
	if got.NextRun.Hour() != wantHour {
		t.Errorf("NextRun hour = %d (expression=%s), want %d — mid-run Update was overwritten by stale expression",
			got.NextRun.Hour(), got.Expression, wantHour)
	}
	if got.Expression != "daily 05:00" {
		t.Errorf("Expression = %q, want daily 05:00 — Update was lost", got.Expression)
	}
	if got.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", got.RunCount)
	}
}

type fakeRunner struct {
	fn func(ctx context.Context, profile, prompt string) (string, error)
}

func (f fakeRunner) Run(ctx context.Context, profile, prompt string) (string, error) {
	return f.fn(ctx, profile, prompt)
}

// TestRunsPerDay covers the daily-fire-count estimator. Cron expressions used
// to return 0 ("unknown cadence"), so "* * * * *" (1440/day) silently
// bypassed the max_runs_per_day cap.
func TestRunsPerDay(t *testing.T) {
	cases := []struct {
		expr string
		want int
	}{
		{"at 2026-06-24 15:00", 0}, // one-shot
		{"every 30m", 48},
		{"every 2h", 12},
		{"hourly", 24},
		{"daily 09:00", 1},
		{"* * * * *", 1440},
		{"*/1 * * * *", 1440},
		{"*/15 * * * *", 96},
		{"0 9 * * *", 1},
		{"0 9 * * 1-5", 1}, // weekday cron still fires once on its day
	}
	for _, c := range cases {
		if got := runsPerDay(c.expr); got != c.want {
			t.Errorf("runsPerDay(%q) = %d, want %d", c.expr, got, c.want)
		}
	}
}

// TestCreateHighFrequencyRequiresConfirm covers the G4-1 gate: creating a task
// that fires more than 4 times/day without the explicit confirmation flag is
// rejected with an error pointing at confirm_high_frequency; with the flag the
// task is created (confirmation also lets it past the max_runs_per_day cap —
// the user explicitly knows the task is frequent).
func TestCreateHighFrequencyRequiresConfirm(t *testing.T) {
	s := New(t.TempDir() + "/hf.json")
	_, err := s.Create(ScheduledTask{Name: "hf", Expression: "*/1 * * * *", Prompt: "x"})
	if err == nil {
		t.Fatal("1440/day task without confirm_high_frequency should be rejected")
	}
	if !strings.Contains(err.Error(), "confirm_high_frequency") {
		t.Errorf("error should point at confirm_high_frequency, got: %v", err)
	}
	// Low-frequency tasks are unaffected.
	if _, err := s.Create(ScheduledTask{Name: "ok", Expression: "daily 09:00", Prompt: "x"}); err != nil {
		t.Fatalf("daily task should create without confirmation: %v", err)
	}
	// With the explicit confirmation flag the high-frequency task is created.
	if _, err := s.Create(ScheduledTask{Name: "hf2", Expression: "*/1 * * * *", Prompt: "x", ConfirmHighFrequency: true}); err != nil {
		t.Fatalf("confirmed high-frequency task should create: %v", err)
	}
}

// TestNextRunMalformedEveryBacksOff: a malformed "every ..." that reached the
// store (Load recomputes NextRun without validating) must not compute
// NextRun == now — that hot-loops immediate refires forever. It backs off one
// hour, the same fallback the malformed-cron path uses.
func TestNextRunMalformedEveryBacksOff(t *testing.T) {
	from := time.Date(2026, 6, 22, 12, 0, 0, 0, time.Local)
	for _, expr := range []string{"every bogus", "every 30x", "every 30s"} {
		got := nextRun(expr, from)
		want := from.Add(time.Hour)
		if !got.Equal(want) {
			t.Errorf("nextRun(%q) = %v, want %v (1h backoff)", expr, got, want)
		}
	}
}
