package netdev

import (
	"path/filepath"
	"testing"
	"time"
)

// P0-5 regression: the in-process ID counters reset on restart, so the first
// job/cutover minted after a restart silently overwrote the day's existing
// J/C<day>-1.json. The counters must seed from the store dirs.
func TestNewJobAndCutoverIDResumePastExisting(t *testing.T) {
	jobsDirOverride = filepath.Join(t.TempDir(), "jobs")
	cutoversDirOverride = filepath.Join(t.TempDir(), "cutovers")
	t.Cleanup(func() { jobsDirOverride, cutoversDirOverride = "", "" })
	resetSeqs := func() {
		jobMu.Lock()
		jobSeq, jobSeqDay = 0, ""
		jobMu.Unlock()
		cutoverMu.Lock()
		cutoverSeq, cutoverSeqDay = 0, ""
		cutoverMu.Unlock()
	}
	resetSeqs()
	t.Cleanup(resetSeqs)

	today := time.Now().Format("20060102")
	// Yesterday's files must not influence today's numbering.
	if err := saveJob(&Job{ID: "J20200101-9", Name: "old", Status: JobDone}); err != nil {
		t.Fatal(err)
	}
	if err := saveJob(&Job{ID: "J" + today + "-1", Name: "today", Status: JobDone}); err != nil {
		t.Fatal(err)
	}
	if err := saveCutover(&CutoverRun{ID: "C20200101-9"}); err != nil {
		t.Fatal(err)
	}
	if err := saveCutover(&CutoverRun{ID: "C" + today + "-1"}); err != nil {
		t.Fatal(err)
	}

	if got := newJobID(); got != "J"+today+"-2" {
		t.Errorf("newJobID = %q, want J%s-2 (restart must not collide)", got, today)
	}
	if got := newCutoverID(); got != "C"+today+"-2" {
		t.Errorf("newCutoverID = %q, want C%s-2 (restart must not collide)", got, today)
	}
}

// P0-5 regression: a persisted status=running job with no live runner (backend
// restarted mid-run) must be swept to `interrupted` by ListJobs — the dashboard
// used to keep ticking a dead countdown while every action but Abort refused.
func TestListJobsMarksOrphanedRunningInterrupted(t *testing.T) {
	jobsDirOverride = filepath.Join(t.TempDir(), "jobs")
	t.Cleanup(func() { jobsDirOverride = "" })

	if err := saveJob(&Job{ID: "J20200101-1", Name: "orphan", Status: JobRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := ListJobs(); err != nil {
		t.Fatal(err)
	}
	j, err := GetJob("J20200101-1")
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != JobInterrupted {
		t.Fatalf("orphaned running job status = %q, want interrupted", j.Status)
	}
	if j.PauseNote == "" {
		t.Error("interrupted job should carry a note explaining the backend restart")
	}

	// A job with a LIVE runner entry must NOT be swept.
	if err := saveJob(&Job{ID: "J20200101-2", Name: "live", Status: JobRunning}); err != nil {
		t.Fatal(err)
	}
	jobRunsMu.Lock()
	jobRuns["J20200101-2"] = &jobRun{cancel: func() {}, pauseReq: make(chan struct{})}
	jobRunsMu.Unlock()
	t.Cleanup(func() {
		jobRunsMu.Lock()
		delete(jobRuns, "J20200101-2")
		jobRunsMu.Unlock()
	})
	if _, err := ListJobs(); err != nil {
		t.Fatal(err)
	}
	j2, _ := GetJob("J20200101-2")
	if j2.Status != JobRunning {
		t.Errorf("live runner job was swept: status = %q, want running", j2.Status)
	}
}

// Same sweep for cutovers: orphaned running → interrupted, and Continue must
// accept the interrupted state so the operator can resume after a restart.
func TestListCutoversMarksOrphanedRunningInterrupted(t *testing.T) {
	cutoversDirOverride = filepath.Join(t.TempDir(), "cutovers")
	t.Cleanup(func() { cutoversDirOverride = "" })

	if err := saveCutover(&CutoverRun{ID: "C20200101-1", Status: CutoverRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := ListCutovers(); err != nil {
		t.Fatal(err)
	}
	c, err := GetCutover("C20200101-1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != CutoverInterrupted {
		t.Fatalf("orphaned running cutover status = %q, want interrupted", c.Status)
	}
}
