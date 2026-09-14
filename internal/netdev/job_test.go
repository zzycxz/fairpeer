package netdev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

func jobTestManager(t *testing.T) *Manager {
	t.Helper()
	m, _ := guardrailManager(t, config.NetDevGuardrails{})
	jobsDirOverride = t.TempDir()
	t.Cleanup(func() { jobsDirOverride = "" })
	return m
}

func waitJobStatus(t *testing.T, id, want string, notePart string) *Job {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		j, err := GetJob(id)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if j.Status == want && (notePart == "" || strings.Contains(j.PauseNote, notePart)) {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s: status %s (note %q), want %s/%q; steps %+v", id, j.Status, j.PauseNote, want, notePart, j.StepState)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A clean two-step runbook goes to done: expect matched, outputs captured,
// commands counted against the budget.
func TestJobRunsToCompletion(t *testing.T) {
	m := jobTestManager(t)
	j, err := m.JobStart(&Job{
		Name: "health-check",
		Steps: []JobStep{
			{Name: "version", Device: "sw1", Command: "display version", Expect: "Versatile"},
			{Name: "ifaces", Device: "sw1", Command: "display interface brief"},
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitJobStatus(t, j.ID, JobDone, "")
	if len(done.StepState) != 2 || done.StepState[0].Status != JobStepOK || done.StepState[1].Status != JobStepOK {
		t.Fatalf("step states: %+v", done.StepState)
	}
	if !strings.Contains(done.StepState[0].Output, "Versatile") {
		t.Fatalf("step output tail missing: %.120s", done.StepState[0].Output)
	}
	if done.Commands < 2 {
		t.Fatalf("commands = %d, want ≥2", done.Commands)
	}
	if done.EndedAt == nil {
		t.Fatal("done job has no EndedAt")
	}
}

// A breakpoint freezes before its step; resume confirms it and the run
// finishes without re-freezing at the same breakpoint.
func TestJobBreakpointPauseResume(t *testing.T) {
	m := jobTestManager(t)
	j, err := m.JobStart(&Job{
		Name: "with-breakpoint",
		Steps: []JobStep{
			{Name: "version", Device: "sw1", Command: "display version"},
			{Name: "risky-look", Device: "sw1", Command: "display interface brief", PauseBefore: true},
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	paused := waitJobStatus(t, j.ID, JobPaused, "断点")
	if paused.Cursor != 1 || paused.StepState[0].Status != JobStepOK {
		t.Fatalf("paused at wrong point: cursor=%d states=%+v", paused.Cursor, paused.StepState)
	}
	resumed, err := m.JobResume(j.ID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.BreakpointOK != 1 {
		t.Fatalf("BreakpointOK = %d, want 1", resumed.BreakpointOK)
	}
	waitJobStatus(t, j.ID, JobDone, "")
}

// on-fail default is pause: a device-error step freezes the job for a human,
// and abort afterwards skips the untouched steps.
func TestJobOnFailPauseThenAbort(t *testing.T) {
	m := jobTestManager(t)
	j, err := m.JobStart(&Job{
		Name: "fails-midway",
		Steps: []JobStep{
			{Name: "version", Device: "sw1", Command: "display version"},
			{Name: "bogus", Device: "sw1", Command: "display bogus", Retries: 1},
			{Name: "after", Device: "sw1", Command: "display version"},
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	paused := waitJobStatus(t, j.ID, JobPaused, "失败")
	if paused.StepState[1].Status != JobStepFailed || paused.StepState[1].Attempts != 2 {
		t.Fatalf("failed step state: %+v", paused.StepState[1])
	}
	if paused.StepState[2].Status != JobStepPending {
		t.Fatalf("later step touched: %+v", paused.StepState[2])
	}
	aborted, err := JobAbort(j.ID)
	if err != nil {
		t.Fatalf("abort: %v", err)
	}
	if aborted.Status != JobAborted || aborted.StepState[2].Status != JobStepSkipped {
		t.Fatalf("aborted job: %+v", aborted.StepState)
	}
}

// The command-count watchdog pauses the run before it exceeds its budget.
func TestJobWatchdogCommandBudget(t *testing.T) {
	m := jobTestManager(t)
	j, err := m.JobStart(&Job{
		Name:   "budget-capped",
		Budget: JobBudget{MaxCommands: 1},
		Steps: []JobStep{
			{Name: "one", Device: "sw1", Command: "display version"},
			{Name: "two", Device: "sw1", Command: "display version"},
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// P1-E6: budget pause notes are operator-facing Chinese with a next step
	// (the raw "watchdog: ..." string read like a crash with no way out).
	waitJobStatus(t, j.ID, JobPaused, "预算暂停")
}

// Expect gates the step: a never-matching pattern fails the step even though
// the command itself ran fine.
func TestJobExpectMismatchFails(t *testing.T) {
	m := jobTestManager(t)
	j, err := m.JobStart(&Job{
		Name: "expect-miss",
		Steps: []JobStep{
			{Name: "version", Device: "sw1", Command: "display version", Expect: "NX-OS"},
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	paused := waitJobStatus(t, j.ID, JobPaused, "失败")
	if !strings.Contains(paused.StepState[0].Error, "expect") {
		t.Fatalf("error = %q, want expect mismatch", paused.StepState[0].Error)
	}
}

// Definitions are validated before anything runs: bad regex, newline command,
// unknown on_fail.
func TestJobValidation(t *testing.T) {
	m := jobTestManager(t)
	if _, err := m.JobStart(&Job{Name: "x", Steps: []JobStep{{Name: "s", Device: "sw1", Command: "display version", Expect: "["}}}); err == nil {
		t.Fatal("bad expect regex accepted")
	}
	if _, err := m.JobStart(&Job{Name: "x", Steps: []JobStep{{Name: "s", Device: "sw1", Command: "display version\ndisplay clock"}}}); err == nil {
		t.Fatal("newline command accepted")
	}
	if _, err := m.JobStart(&Job{Name: "x", Steps: []JobStep{{Name: "s", Device: "sw1", Command: "display version", OnFail: "explode"}}}); err == nil {
		t.Fatal("bad on_fail accepted")
	}
	if _, err := m.JobStart(&Job{Name: "x"}); err == nil {
		t.Fatal("empty steps accepted")
	}
}

// 轮3审查 P2：Windows 文件锁 flake 的并发回归——保存方（rename-vs-reader）
// 与轮询读者对打：读端允许瞬时失败/空读（AtomicWriteFile 语义内），但禁止
// 持续不可读或坏 JSON；保存端重试后必须全部成功。
func TestSaveJobConcurrentReads(t *testing.T) {
	jobTestManager(t)
	j := &Job{ID: "J-concurrent", Name: "concurrent", Steps: []JobStep{
		{Name: "s1", Device: "sw1", Command: "display version"},
	}}
	if err := saveJob(j); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			j.Commands = i
			if err := saveJob(j); err != nil {
				t.Errorf("save %d: %v", i, err)
				return
			}
		}
	}()
	reads, transient := 0, 0
	for {
		select {
		case <-done:
			// 写端收尾后文件必须稳定为最后一次成功保存（saveJob 重试保证）；
			// copyOnto 原地回退允许循环期瞬态半行，但收尾后短重试内必读到
			// 完整 JSON。
			var probe Job
			ok := false
			for r := 0; r < 25 && !ok; r++ {
				b, err := os.ReadFile(filepath.Join(jobsDir(), j.ID+".json"))
				if err == nil && json.Unmarshal(b, &probe) == nil && probe.Commands == 49 {
					ok = true
				} else {
					time.Sleep(20 * time.Millisecond)
				}
			}
			if !ok {
				t.Fatalf("final state unreadable after concurrent saves (reads=%d transient=%d)", reads, transient)
			}
			if reads == 0 {
				t.Fatal("reader never ran")
			}
			return
		default:
		}
		b, err := os.ReadFile(filepath.Join(jobsDir(), j.ID+".json"))
		reads++
		if err == nil {
			var probe Job
			if json.Unmarshal(b, &probe) != nil {
				transient++ // copyOnto 原地写的瞬态半行——允许，不计失败
			}
		}
	}
}
