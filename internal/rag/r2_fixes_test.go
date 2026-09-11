package rag

// Tests for the R2 explicit-recovery fixes from docs/COWORK_RAG_ISSUES_SPEC.md:
//   - R2-1 RetryFailedChunks re-runs ONLY errored chunks (cheap retry)
//   - R2-2 re-import dedup self-heals partially-failed jobs

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// A partial job (some chunks failed) retried via RetryFailedChunks must:
// re-run only the failed chunks, converge to done with failed_chunks=0, and
// leave the previously-successful chunks' state untouched.
func TestRetryFailedChunksConvergesPartial(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	fpath := dir + "/partial.md"
	// Three paragraphs → three chunks (paragraph split, each > merge threshold
	// is hard to force with tiny text; use one long body so windowChunk makes
	// three 3000-rune windows deterministically? Simpler: rely on paragraph
	// chunking with three sizable paragraphs).
	// Three paragraphs, each ~2600 chars (< 3000 chunk cap, > 1500 so the
	// merger can't fold two of them into one chunk) → exactly three chunks.
	body := repeat("AlphaCompany is a vendor. ", 100) + "\n\n" +
		repeat("BetaCompany is a partner. ", 100) + "\n\n" +
		repeat("GammaCompany is a client. ", 100)
	if err := os.WriteFile(fpath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := &fakeExtractor{
		failN: 1, // first call (chunk 0) fails; single attempt per chunk
	}
	cfg := PipelineConfig{Concurrency: 1, Interval: 0, MaxRetries: 1, RetryBase: 0}
	p := NewPipeline(s, ext, cfg, nil)
	p.Start()
	defer p.Stop()

	jobIDs, err := p.EnqueuePaths("docs", []string{fpath}, "", "", false)
	if err != nil || len(jobIDs) != 1 {
		t.Fatalf("enqueue: %v (%d jobs)", err, len(jobIDs))
	}
	waitJob(t, s, jobIDs[0])
	j, _, _ := s.JobByID(jobIDs[0])
	if j.Status != JobDone || j.FailedChunks == 0 {
		t.Fatalf("precondition: want done+partial, got status=%q failed=%d", j.Status, j.FailedChunks)
	}
	failedBefore := j.FailedChunks

	// Heal the extractor and retry only the failed chunk(s).
	atomic.StoreInt32(&ext.failN, 0)
	n, err := p.RetryFailedChunks(jobIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if n != failedBefore {
		t.Fatalf("RetryFailedChunks queued %d, want %d", n, failedBefore)
	}
	waitJob(t, s, jobIDs[0])
	j, _, _ = s.JobByID(jobIDs[0])
	if j.Status != JobDone || j.FailedChunks != 0 {
		t.Fatalf("after retry: status=%q failed=%d, want done/0", j.Status, j.FailedChunks)
	}
	// Exactly one extra extractor call per failed chunk — the successful
	// chunks were never re-run (that is the whole point: cheap retry).
	if calls := atomic.LoadInt32(&ext.calls); calls != 4 { // 3 first pass + 1 retry
		t.Errorf("extractor calls = %d, want 4 (3 initial + 1 retry, no re-run of good chunks)", calls)
	}

	// Retry on a clean job is a no-op error.
	if _, err := p.RetryFailedChunks(jobIDs[0]); err == nil {
		t.Error("RetryFailedChunks on fully-done job should error (nothing to retry)")
	}
}

// Re-importing a folder must re-queue partially-failed files instead of
// skipping them forever (dedup only short-circuits done WITH zero failures).
func TestReimportSelfHealsPartialJobs(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	fpath := dir + "/selfheal.md"
	body := repeat("DeltaCompany ships hardware. ", 100) + "\n\n" + repeat("EpsilonCompany ships software. ", 100)
	if err := os.WriteFile(fpath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ext := &fakeExtractor{failN: 1}
	cfg := PipelineConfig{Concurrency: 1, Interval: 0, MaxRetries: 1, RetryBase: 0}
	p := NewPipeline(s, ext, cfg, nil)
	p.Start()
	defer p.Stop()

	ids, _ := p.EnqueuePaths("c1", []string{fpath}, "", "", false)
	waitJob(t, s, ids[0])
	j, _, _ := s.JobByID(ids[0])
	if j.Status != JobDone || j.FailedChunks == 0 {
		t.Fatalf("precondition: want partial, got %q/%d", j.Status, j.FailedChunks)
	}

	// Same file, same stat key — previously skipped; now must re-queue.
	atomic.StoreInt32(&ext.failN, 0)
	ids2, err := p.EnqueuePaths("c1", []string{fpath}, "", "", false)
	if err != nil || len(ids2) != 1 {
		t.Fatalf("re-import: %v (%d jobs)", err, len(ids2))
	}
	waitJob(t, s, ids2[0])
	_, status, _, _, failed, _ := s.JobStatusForPath("c1", fpath)
	if status != JobDone || failed != 0 {
		t.Errorf("after self-heal re-import: status=%q failed=%d, want done/0", status, failed)
	}
}

// ChunkLatencyP50Ms is the median over SUCCESSFUL chunks only.
func TestChunkLatencyP50(t *testing.T) {
	s := newTempStore(t)
	defer s.Close()
	jobID, _ := s.CreateJob(JobRow{Collection: "c", Path: "/d/x.md", Status: JobPending},
		[]string{"a", "b", "c"})
	_ = s.MarkChunkDone(jobID+"_c0", jobID, 100, nil)
	_ = s.MarkChunkDone(jobID+"_c1", jobID, 300, nil)
	_ = s.MarkChunkDone(jobID+"_c2", jobID, 9000, errors.New("timeout")) // excluded
	p50, err := s.ChunkLatencyP50Ms(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if p50 != 300 {
		t.Errorf("p50 = %d, want 300 (median of {100,300}; timeout sample excluded)", p50)
	}
}

// waitJob polls until the job leaves pending/extracting.
func waitJob(t *testing.T, s *Store, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, _, _ := s.JobByID(jobID)
		if j.Status == JobDone || j.Status == JobError {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
