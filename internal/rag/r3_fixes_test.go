package rag

// Tests for the R3 opt-in auto-retry layer from docs/COWORK_RAG_ISSUES_SPEC.md:
//   - retry_rounds persist, cap the eligible set, and reset on clean success
//   - FailedJobsForAutoRetry / ExhaustedAutoRetryJobs partition correctly

import (
	"errors"
	"testing"
)

func TestAutoRetryRoundsLifecycle(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	errJob, _ := s.CreateJob(JobRow{Collection: "c", Path: "/d/err.md", Status: JobPending}, []string{"a"})
	partJob, _ := s.CreateJob(JobRow{Collection: "c", Path: "/d/part.md", Status: JobPending}, []string{"a", "b"})
	okJob, _ := s.CreateJob(JobRow{Collection: "c", Path: "/d/ok.md", Status: JobPending}, []string{"a"})

	// errJob: all chunks fail → status error.
	_ = s.MarkChunkDone(errJob+"_c0", errJob, 1, errors.New("timeout"))
	// partJob: one done one failed → status done + failed_chunks=1 (partial).
	_ = s.MarkChunkDone(partJob+"_c0", partJob, 1, nil)
	_ = s.MarkChunkDone(partJob+"_c1", partJob, 1, errors.New("429"))
	// okJob: clean.
	_ = s.MarkChunkDone(okJob+"_c0", okJob, 1, nil)

	// Both damaged jobs are eligible; the clean one is not.
	jobs, err := s.FailedJobsForAutoRetry(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("eligible = %d jobs, want 2 (error + partial)", len(jobs))
	}

	// Round 1 consumed → still eligible (cap 2).
	if r, err := s.IncrementJobRetryRounds(errJob); err != nil || r != 1 {
		t.Fatalf("increment: rounds=%d err=%v, want 1/nil", r, err)
	}
	// Round 2 consumed → no longer eligible, appears in the exhausted set.
	if r, err := s.IncrementJobRetryRounds(errJob); err != nil || r != 2 {
		t.Fatalf("increment: rounds=%d err=%v, want 2/nil", r, err)
	}
	jobs, _ = s.FailedJobsForAutoRetry(2)
	if len(jobs) != 1 || jobs[0].ID != partJob {
		t.Fatalf("after 2 rounds: eligible = %+v, want only partJob", jobs)
	}
	exhausted, _ := s.ExhaustedAutoRetryJobs(2)
	if len(exhausted) != 1 || exhausted[0].ID != errJob {
		t.Fatalf("exhausted = %+v, want errJob", exhausted)
	}

	// partJob heals on a later retry → terminal flip resets its rounds.
	_ = s.MarkChunkDone(partJob+"_c1", partJob, 2, nil)
	j, _, _ := s.JobByID(partJob)
	if j.Status != JobDone || j.FailedChunks != 0 {
		t.Fatalf("partJob after heal: %q/%d", j.Status, j.FailedChunks)
	}
	if j.RetryRounds != 0 {
		t.Errorf("rounds = %d after clean convergence, want 0 (reset)", j.RetryRounds)
	}
	jobs, _ = s.FailedJobsForAutoRetry(2)
	if len(jobs) != 0 {
		t.Errorf("eligible after heal = %d, want 0", len(jobs))
	}
}

// A retry round that FAILS AGAIN must preserve retry_rounds: the regression
// where every late chunk reset the counter to 0 made the auto-retry cap never
// bite (infinite re-burns every 5 minutes).
func TestRetryRoundPreservedOnPartialConvergence(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	jobID, _ := s.CreateJob(JobRow{Collection: "c", Path: "/d/p.md", Status: JobPending}, []string{"a", "b"})
	// Round 1: chunk a ok, chunk b fails → done, failed=1.
	_ = s.MarkChunkDone(jobID+"_c0", jobID, 1, nil)
	_ = s.MarkChunkDone(jobID+"_c1", jobID, 1, errors.New("timeout"))
	if _, err := s.IncrementJobRetryRounds(jobID); err != nil {
		t.Fatal(err)
	}
	// Round 2 (retry): chunk b fails AGAIN → SetJobRetrying flips extracting,
	// the retried chunk re-marks... both chunks re-mark as the round replays.
	if err := s.SetJobRetrying(jobID); err != nil {
		t.Fatal(err)
	}
	_ = s.MarkChunkDone(jobID+"_c0", jobID, 2, nil) // kept chunk re-confirms
	_ = s.MarkChunkDone(jobID+"_c1", jobID, 2, errors.New("timeout"))
	j, _, _ := s.JobByID(jobID)
	if j.Status != JobDone || j.FailedChunks != 1 {
		t.Fatalf("after failed retry round: %q/%d, want done/1", j.Status, j.FailedChunks)
	}
	if j.RetryRounds != 1 {
		t.Errorf("rounds = %d after still-failing retry, want 1 (cap must stay effective)", j.RetryRounds)
	}
}

// JobByID must surface retry_rounds (the engine logs and the UI badge read it).
func TestJobRowCarriesRetryRounds(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	jobID, _ := s.CreateJob(JobRow{Collection: "c", Path: "/d/x.md", Status: JobPending}, []string{"a"})
	_ = s.MarkChunkDone(jobID+"_c0", jobID, 1, errors.New("x"))
	if _, err := s.IncrementJobRetryRounds(jobID); err != nil {
		t.Fatal(err)
	}
	j, _, _ := s.JobByID(jobID)
	if j.RetryRounds != 1 {
		t.Errorf("JobByID rounds = %d, want 1", j.RetryRounds)
	}
	all, _ := s.AllJobs()
	if len(all) != 1 || all[0].RetryRounds != 1 {
		t.Errorf("AllJobs rounds = %+v", all)
	}
}

// Databases upgraded from pre-v8 get failed_chunks as DEFAULT 0 — historical
// partial jobs would keep masquerading as clean. Open() backfills the count
// from the chunk rows, so a simulated stale row self-heals on reopen.
func TestOpenBackfillsFailedChunks(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/rag.db"
	{
		s, err := Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		jobID, _ := s.CreateJob(JobRow{Collection: "c", Path: "/d/hist.md", Status: JobPending}, []string{"a", "b", "c"})
		_ = s.MarkChunkDone(jobID+"_c0", jobID, 1, errors.New("boom"))
		_ = s.MarkChunkDone(jobID+"_c1", jobID, 1, errors.New("timeout"))
		_ = s.MarkChunkDone(jobID+"_c2", jobID, 1, errors.New("timeout"))
		// Simulate a pre-v8/pre-R1 row: counters and the job-level reason
		// zeroed while the chunk rows still carry the truth.
		if _, err := s.db.Exec(`UPDATE rag_jobs SET failed_chunks = 0, error_msg = '' WHERE id = ?`, jobID); err != nil {
			t.Fatal(err)
		}
		// And a pre-R1 row: job-level error_msg never persisted.
		if _, err := s.db.Exec(`UPDATE rag_jobs SET error_msg = '' WHERE id = ?`, jobID); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	jobs, _ := s.AllJobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].FailedChunks != 3 {
		t.Errorf("failed_chunks after reopen = %d, want 3 (backfilled from chunk rows)", jobs[0].FailedChunks)
	}
	if jobs[0].ErrorMsg == "" {
		t.Error("error_msg after reopen empty — want backfilled from chunk rows")
	}
}
