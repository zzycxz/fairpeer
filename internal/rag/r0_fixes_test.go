package rag

// Tests for the R0 correctness fixes from docs/COWORK_RAG_ISSUES_SPEC.md:
//   - R0-1 per-attempt timeout + per-task budget (retry actually runs)
//   - R0-2 MarkChunkDone guard: only DONE chunks are immutable
//   - R0-3 WAL + busy_timeout + foreign_keys actually enabled via DSN
//   - R0-4 cancelled jobs are never resurrected
//   - R0-5 SetJobExtracted / SetJobFailed (Hyper-Extract persistence)
//   - R0-6 walkDocs prunes dot-dirs / node_modules / extension-less files
//   - job-level error_msg persisted on the all-chunks-failed flip

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// --- R0-3: pragmas actually applied -----------------------------------------

func TestStorePragmasEnabled(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal (modernc ignores the legacy DSN form)", mode)
	}
	var busy int
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

// --- R0-2: errored chunks are retryable --------------------------------------

func TestMarkChunkDoneRetryOverwritesError(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	jobID, err := s.CreateJob(JobRow{Collection: "c", Path: "/d/doc.md", Status: JobPending},
		[]string{"chunk zero", "chunk one"})
	if err != nil {
		t.Fatal(err)
	}
	// First attempt of chunk 0 fails.
	if err := s.MarkChunkDone(jobID+"_c0", jobID, 10, errors.New("HTTP 429: quota")); err != nil {
		t.Fatal(err)
	}
	// Retry succeeds — the old guard skipped errored chunks here, so the
	// retry result was swallowed and the job could never converge.
	if err := s.MarkChunkDone(jobID+"_c0", jobID, 20, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkChunkDone(jobID+"_c1", jobID, 5, nil); err != nil {
		t.Fatal(err)
	}
	j, ok, _ := s.JobByID(jobID)
	if !ok || j.Status != JobDone {
		t.Fatalf("job status = %q (ok=%v), want done after error chunk retried", j.Status, ok)
	}
	if j.DoneChunks != 2 {
		t.Errorf("done_chunks = %d, want 2", j.DoneChunks)
	}
	var st string
	if err := s.db.QueryRow(`SELECT status FROM rag_chunks WHERE id = ?`, jobID+"_c0").Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != ChunkDone {
		t.Errorf("retried chunk status = %q, want done", st)
	}

	// Done chunks stay immutable: a late duplicate mark must not change state.
	_ = s.MarkChunkDone(jobID+"_c1", jobID, 5, errors.New("late failure"))
	j, _, _ = s.JobByID(jobID)
	if j.Status != JobDone {
		t.Errorf("done chunk re-mark changed job status to %q", j.Status)
	}
}

// Job-level error_msg persistence (F-B1 minimal): when every chunk fails, the
// job row records WHY; on success the stale message is cleared.
func TestJobErrorMsgPersistedOnAllFailed(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	jobID, err := s.CreateJob(JobRow{Collection: "c", Path: "/d/doc.md", Status: JobPending},
		[]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.MarkChunkDone(jobID+"_c0", jobID, 1, errors.New("extract http: context deadline exceeded"))
	_ = s.MarkChunkDone(jobID+"_c1", jobID, 1, errors.New("extract HTTP 429: quota"))
	j, ok, _ := s.JobByID(jobID)
	if !ok || j.Status != JobError {
		t.Fatalf("job status = %q (ok=%v), want error", j.Status, ok)
	}
	if j.ErrorMsg == "" {
		t.Error("job error_msg empty — UI would show a bare 出错 with no reason")
	}

	// Re-run succeeds → error_msg cleared.
	_ = s.MarkChunkDone(jobID+"_c0", jobID, 2, nil)
	_ = s.MarkChunkDone(jobID+"_c1", jobID, 2, nil)
	j, _, _ = s.JobByID(jobID)
	if j.Status != JobDone {
		t.Fatalf("job status = %q, want done after retry", j.Status)
	}
	if j.ErrorMsg != "" {
		t.Errorf("error_msg = %q, want cleared on success", j.ErrorMsg)
	}
}

// --- R0-4: cancelled jobs stay cancelled --------------------------------------

func TestCancelledJobNotFlippedByLateChunk(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	jobID, err := s.CreateJob(JobRow{Collection: "c", Path: "/d/doc.md", Status: JobPending},
		[]string{"only"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SetJobStatus(jobID, JobCancelled)
	// In-flight chunk finishes after CancelJob — the job must keep its
	// terminal state instead of flipping to done.
	if err := s.MarkChunkDone(jobID+"_c0", jobID, 1, nil); err != nil {
		t.Fatal(err)
	}
	j, _, _ := s.JobByID(jobID)
	if j.Status != JobCancelled {
		t.Errorf("job status = %q after late chunk on cancelled job, want cancelled", j.Status)
	}
}

func TestProcessTaskSkipsCancelledJob(t *testing.T) {
	s := newTestStore(t)
	ext := &fakeExtractor{}
	p := NewPipeline(s, ext, PipelineConfig{Concurrency: 1, Interval: 0, MaxRetries: 1, RetryBase: 0}, nil)
	jobID, err := s.CreateJob(JobRow{Collection: "c", Path: "/d/doc.md", Status: JobPending},
		[]string{"text"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SetJobStatus(jobID, JobCancelled)
	// A task already dequeued when the user cancelled must be a no-op: no LLM
	// call, no extracting resurrection, no chunk state change.
	p.processTask(0, chunkTask{JobID: jobID, Collection: "c", Path: "/d/doc.md", ChunkIdx: 0, ChunkID: jobID + "_c0", Text: "text"})
	if calls := atomic.LoadInt32(&ext.calls); calls != 0 {
		t.Errorf("extractor called %d times for cancelled job, want 0", calls)
	}
	j, _, _ := s.JobByID(jobID)
	if j.Status != JobCancelled {
		t.Errorf("job status = %q, want cancelled (resurrection guard failed)", j.Status)
	}
}

// --- R0-1: the retry survives a slow first attempt ------------------------------

func TestPipelineRetriesAfterAttemptTimeout(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	fpath := dir + "/slow.md"
	if err := os.WriteFile(fpath, []byte("slow chunk content."), 0o644); err != nil {
		t.Fatal(err)
	}
	// Every attempt blocks past the per-attempt timeout. Under the old
	// shared-180s-context design the first attempt consumed the whole budget
	// and the retry never ran (calls stuck at 1).
	ext := &fakeExtractor{delay: 60 * time.Millisecond}
	cfg := PipelineConfig{
		Concurrency:    1,
		Interval:       0,
		MaxRetries:     2,
		RetryBase:      time.Millisecond,
		AttemptTimeout: 20 * time.Millisecond,
		Budget:         time.Second,
	}
	p := NewPipeline(s, ext, cfg, nil)
	p.Start()
	defer p.Stop()

	jobIDs, err := p.EnqueuePaths("docs", []string{fpath}, "", "", false)
	if err != nil || len(jobIDs) != 1 {
		t.Fatalf("enqueue: %v (%d jobs)", err, len(jobIDs))
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, _, _ := s.JobByID(jobIDs[0])
		if j.Status == JobDone || j.Status == JobError {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if calls := atomic.LoadInt32(&ext.calls); calls < 2 {
		t.Errorf("extractor called %d times, want ≥2 (attempt timeout must not eat the retry budget)", calls)
	}
	j, _, _ := s.JobByID(jobIDs[0])
	if j.Status != JobError {
		t.Fatalf("job status = %q, want error after all attempts timed out", j.Status)
	}
	if j.ErrorMsg == "" {
		t.Error("job error_msg empty after all attempts failed")
	}
	// Semantics note: rag_chunks.attempts counts MarkChunkDone rounds (one
	// processTask = one mark), NOT individual LLM calls — the extractor fake's
	// calls counter above is the observable for "both attempts ran".
	var att int
	if err := s.db.QueryRow(`SELECT attempts FROM rag_chunks WHERE id = ?`, jobIDs[0]+"_c0").Scan(&att); err != nil {
		t.Fatal(err)
	}
	if att != 1 {
		t.Errorf("stored attempts = %d, want 1 (one retry round)", att)
	}
}

// --- R0-5: Hyper-Extract job persistence ----------------------------------------

func TestSetJobExtractedAndFailed(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	jobID, err := s.CreateJob(JobRow{Collection: "c", Path: "/d/doc.pptx", Status: JobPending},
		[]string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetJobFailed(jobID, "extract: connect refused"); err != nil {
		t.Fatal(err)
	}
	j, _, _ := s.JobByID(jobID)
	if j.Status != JobError || j.ErrorMsg != "extract: connect refused" {
		t.Errorf("after SetJobFailed: status=%q error_msg=%q", j.Status, j.ErrorMsg)
	}
	if err := s.SetJobExtracted(jobID); err != nil {
		t.Fatal(err)
	}
	j, _, _ = s.JobByID(jobID)
	if j.Status != JobDone {
		t.Errorf("status = %q, want done", j.Status)
	}
	if j.DoneChunks != j.TotalChunks || j.TotalChunks != 3 {
		t.Errorf("done_chunks=%d/%d, want all 3 credited so the tree shows 已抽取", j.DoneChunks, j.TotalChunks)
	}
	if j.ErrorMsg != "" {
		t.Errorf("error_msg = %q, want cleared", j.ErrorMsg)
	}
}

// --- R0-6: walkDocs exclusions ---------------------------------------------------

func TestWalkDocsSkipsHiddenDepDirsAndNoExt(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.md", "real doc")
	write("sub/nested.md", "nested doc")
	write(".git/objects/ab/noext", "git leftover")
	write(".fairpeer/meta.json", "{\"own\":\"metadata\"}")
	write("node_modules/lib.js", "dep code")
	write("noextfile", "no extension")

	got, err := walkDocs(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"keep.md": false, filepath.Join("sub", "nested.md"): false}
	for _, p := range got {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := want[rel]; !ok {
			t.Errorf("unexpected file ingested: %s", rel)
			continue
		}
		want[rel] = true
	}
	for rel, seen := range want {
		if !seen {
			t.Errorf("expected %s in walk results, got %v", rel, got)
		}
	}
	if !isSupportedExt("x/keep.MD") {
		t.Error("uppercase extensions should still be supported")
	}
	if isSupportedExt("x/README") {
		t.Error("extension-less files must be rejected (git leftovers/lockfiles)")
	}
}

// --- R0-7: shared incremental/full job selection ---------------------------------

func TestFilterJobsForExtraction(t *testing.T) {
	jobs := []JobRow{
		{Path: "/a", Status: JobDone},
		{Path: "/b", Status: JobPending},
		{Path: "/c", Status: JobError},
		{Path: "/a", Status: JobDone}, // duplicate path — deduped
	}
	incremental := FilterJobsForExtraction(jobs, false)
	if len(incremental) != 2 || incremental[0].Path != "/b" || incremental[1].Path != "/c" {
		t.Errorf("incremental = %v, want [/b /c] (done skipped, deduped)", incremental)
	}
	full := FilterJobsForExtraction(jobs, true)
	if len(full) != 3 {
		t.Errorf("full = %v, want 3 unique jobs (done re-run)", full)
	}
}
