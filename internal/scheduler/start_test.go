package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestStartActuallyLaunchesTimer confirms Start arms the precise timer (not the
// old broken stopCh logic) and a due task fires. With AfterFunc the past-due
// task should fire within ~1s, not the old 30s tick — so this also serves as a
// regression guard for both the Start() bug and the polling→timer migration.
func TestStartActuallyLaunchesTimer(t *testing.T) {
	s := New(t.TempDir() + "/start.json")
	now := time.Now()
	_, err := s.Create(ScheduledTask{
		Name:       "start-test",
		Expression: "at " + now.Add(time.Minute).Format("2006-01-02 15:04"),
		Prompt:     "x",
		OutputMode: "notify",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Backdate NextRun so it's immediately due.
	s.mu.Lock()
	s.tasks[0].NextRun = now.Add(-1 * time.Second)
	s.mu.Unlock()

	notifyCount := 0
	s.SetNotifier(testNotifierCount{&notifyCount})
	s.SetRunner(fakeRunner{fn: func(context.Context, string, string) (string, error) {
		return "ran", nil
	}})
	s.Start()
	defer s.Stop()

	// With AfterFunc, a past-due task fires within ~1s. Allow generous 10s for CI.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if notifyCount > 0 {
			return // fired promptly — timer works
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("Start() did not fire a past-due task within 10s (notifyCount=%d)", notifyCount)
}

// TestPreciseFireTiming confirms the AfterFunc timer fires at the scheduled
// second (within a small tolerance), not up to 30s late like the old poller.
// We schedule a task ~3s out and assert it fires within 3s + tolerance.
func TestPreciseFireTiming(t *testing.T) {
	s := New(t.TempDir() + "/precise.json")
	s.SetLogger(func(f string, a ...any) { t.Logf("[LOG] "+f, a...) })
	// Schedule 3 seconds out. Use a 2-minute-future base then backdate NextRun to
	// now+3s (Create needs a future expression to succeed).
	base := time.Now().Add(2 * time.Minute).Format("2006-01-02 15:04")
	_, err := s.Create(ScheduledTask{
		Name:       "precise-test",
		Expression: "at " + base,
		Prompt:     "x",
		OutputMode: "notify",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Arm precisely at now+3s.
	target := time.Now().Add(3 * time.Second)
	s.mu.Lock()
	s.tasks[0].NextRun = target
	s.mu.Unlock()

	var fireTime time.Time
	s.SetNotifier(notifierCaptureTime{&fireTime})
	s.SetRunner(fakeRunner{fn: func(context.Context, string, string) (string, error) {
		return "ran", nil
	}})
	s.Start()
	defer s.Stop()

	// Wait up to 8s. The fire should land within 3s + 2s tolerance.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if !fireTime.IsZero() {
			delta := fireTime.Sub(target)
			t.Logf("fired at %v, target %v, delta %v", fireTime.Format("15:04:05.000"), target.Format("15:04:05.000"), delta)
			// Should be within ±2s of the target — far tighter than the old 30s poll.
			if delta > 2*time.Second {
				t.Errorf("fire delta %v exceeds 2s tolerance (old poller-level imprecision)", delta)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("task did not fire within 8s")
}

type testNotifierCount struct{ count *int }

func (n testNotifierCount) Notify(name, body string) { *n.count++ }

type notifierCaptureTime struct{ t *time.Time }

func (n notifierCaptureTime) Notify(name, body string) { *n.t = time.Now() }

// TestInFlightTaskNotDoubleFired guards the concurrent double-fire fix: while
// a task's run is executing, its NextRun is still the stale past-due instant
// that launched it (it is recomputed only when the run completes). Any
// Create/Update/Delete/Start during the run used to re-arm a 0-delay timer
// that fired the SAME task again while the first run was still in-flight.
// fireDue now skips in-flight tasks and armNextTimerLocked ignores their
// stale NextRun, so the handler must run exactly once.
func TestInFlightTaskNotDoubleFired(t *testing.T) {
	s := New(t.TempDir() + "/inflight.json")
	s.SetLogger(func(f string, a ...any) { t.Logf("[LOG] "+f, a...) })
	task, err := s.Create(ScheduledTask{Name: "blocker", Expression: "every 1h", Prompt: "x", ConfirmHighFrequency: true})
	if err != nil {
		t.Fatal(err)
	}
	// Backdate so the task is immediately due when Start arms the timer.
	s.mu.Lock()
	s.tasks[0].NextRun = time.Now().Add(-time.Second)
	s.mu.Unlock()

	var calls int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	s.SetRunner(fakeRunner{fn: func(context.Context, string, string) (string, error) {
		atomic.AddInt32(&calls, 1)
		entered <- struct{}{}
		<-release // hold the task in-flight ~the test's whole duration
		return "ran", nil
	}})
	s.Start()
	defer s.Stop()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("task never fired")
	}

	// While the run holds the task in-flight, exercise the re-arm paths that
	// previously armed a 0-delay timer for the stale past-due NextRun. (No
	// mid-run Update here: Update itself recomputes NextRun to a future
	// instant, closing the race window — that path is covered by
	// TestFireDueRespectsMidRunUpdate.)
	if _, err := s.Create(ScheduledTask{Name: "other", Expression: "daily 09:00", Prompt: "y"}); err != nil {
		t.Fatal(err)
	}
	s.Stop()
	s.Start()
	// Give any (buggy) duplicate timer ample time to fire.
	time.Sleep(500 * time.Millisecond)

	// Let the first (and only) run finish, wait for its bookkeeping.
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tk, ok := s.Get(task.ID); ok && tk.RunCount == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // let any stray duplicate land

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("handler ran %d times, want exactly 1 (concurrent double-fire)", got)
	}
	tk, _ := s.Get(task.ID)
	if tk.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", tk.RunCount)
	}
	// Completion must reschedule into the future (and the timer re-arm).
	if !tk.NextRun.After(time.Now()) {
		t.Errorf("NextRun = %v, want a future instant after the completed run", tk.NextRun)
	}
}
