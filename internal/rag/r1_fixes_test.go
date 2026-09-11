package rag

// Tests for the R1 status-honesty fixes from docs/COWORK_RAG_ISSUES_SPEC.md:
//   - R1-1 failed_chunks column (v8 migration) kept in sync by MarkChunkDone
//   - R1-6 terminal progress events bypass the 1/sec throttle

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A done job with some failed chunks must expose the count so the tree can
// show 部分失败 instead of a misleading 已抽取.
func TestFailedChunksCountedOnPartialDone(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	jobID, err := s.CreateJob(JobRow{Collection: "c", Path: "/d/doc.md", Status: JobPending},
		[]string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.MarkChunkDone(jobID+"_c0", jobID, 1, nil)
	_ = s.MarkChunkDone(jobID+"_c1", jobID, 1, errors.New("timeout"))
	_ = s.MarkChunkDone(jobID+"_c2", jobID, 1, errors.New("timeout"))
	j, ok, _ := s.JobByID(jobID)
	if !ok {
		t.Fatal("job vanished")
	}
	if j.Status != JobDone {
		t.Fatalf("status = %q, want done (partial, not all failed)", j.Status)
	}
	if j.FailedChunks != 2 {
		t.Errorf("failed_chunks = %d, want 2", j.FailedChunks)
	}
	if j.DoneChunks != 3 {
		t.Errorf("done_chunks = %d, want 3 (done+error both counted)", j.DoneChunks)
	}

	// A retry flips one errored chunk to done → counters follow.
	_ = s.MarkChunkDone(jobID+"_c1", jobID, 2, nil)
	j, _, _ = s.JobByID(jobID)
	if j.FailedChunks != 1 || j.DoneChunks != 3 {
		t.Errorf("after retry: failed=%d done=%d, want 1/3", j.FailedChunks, j.DoneChunks)
	}

	// AllJobs carries the column too (the tree builds from it).
	jobs, _ := s.AllJobs()
	if len(jobs) != 1 || jobs[0].FailedChunks != 1 {
		t.Errorf("AllJobs failed_chunks = %+v", jobs)
	}
}

// JobsByPath used to select fewer columns than scanJobs scans, silently
// returning an empty slice for every caller.
func TestJobsByPathReturnsRows(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	_, err := s.CreateJob(JobRow{Collection: "c", Path: "/d/doc.md", Status: JobPending}, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.JobsByPath("c", "/d/doc.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("JobsByPath returned %d rows, want 1 (column-list regression)", len(jobs))
	}
}

// R1-6: terminal events (job done/error) must bypass the 1/sec progress
// throttle — a batch of small jobs finishing back-to-back previously lost its
// last event, freezing the UI on a stale state.
func TestTerminalEventsBypassThrottle(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()

	var mu sync.Mutex
	var events []ProgressEvent
	emit := func(ev ProgressEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	ext := &fakeExtractor{}
	cfg := PipelineConfig{Concurrency: 1, Interval: 0, MaxRetries: 1, RetryBase: 0}
	p := NewPipeline(s, ext, cfg, emit)
	p.Start()
	defer p.Stop()

	var jobIDs []string
	for i := 0; i < 3; i++ {
		fpath := filepath.Join(dir, "doc"+string(rune('0'+i))+".md")
		if err := os.WriteFile(fpath, []byte("content "+string(rune('0'+i))), 0o644); err != nil {
			t.Fatal(err)
		}
		ids, err := p.EnqueuePaths("docs", []string{fpath}, "", "", false)
		if err != nil || len(ids) != 1 {
			t.Fatalf("enqueue %d: %v (%d)", i, err, len(ids))
		}
		jobIDs = append(jobIDs, ids[0])
	}

	// All three jobs are single-chunk and the extractor is instant — they
	// finish within the same 1s throttle window. Wait for BOTH the job states
	// AND the terminal events: MarkChunkDone flips the row before
	// emitProgress appends the event, so breaking on job state alone races
	// the last event into the counter.
	deadline := time.Now().Add(5 * time.Second)
	terminals := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, ev := range events {
			if ev.Kind == EventKindTerminal {
				n++
			}
		}
		return n
	}
	for time.Now().Before(deadline) {
		if terminals() >= 3 {
			break
		}
		allDone := true
		for _, id := range jobIDs {
			j, _, _ := s.JobByID(id)
			if j.Status != JobDone && j.Status != JobError {
				allDone = false
				break
			}
		}
		if allDone && terminals() >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	terminalCount := 0
	for _, ev := range events {
		if ev.Kind == EventKindTerminal {
			terminalCount++
		}
		if ev.Kind == "" {
			t.Error("event missing Kind field")
		}
		if ev.Scope != EventScopeChunk {
			t.Errorf("scope = %q, want chunk", ev.Scope)
		}
	}
	if terminalCount != 3 {
		t.Errorf("terminal events = %d, want 3 (one per job, throttle must not drop them); total events %d", terminalCount, len(events))
	}
}
