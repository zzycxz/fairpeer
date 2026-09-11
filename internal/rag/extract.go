package rag

// extract.go implements the structured-extraction pipeline that turns imported
// documents into entities + relations (a knowledge graph), running in the
// background with rate limiting + exponential-backoff retries so it never
// overwhelms the LLM or the user's network.
//
// Why a custom pipeline instead of reusing Hyper-Extract's Python batch:
//   - We need precise control over the request cadence (the user explicitly
//     wants "low frequency, no errors"), which HE's internal batch/OMem doesn't
//     expose. Going Go-native means the queue, retry, and rate-limit logic all
//     live in one place we fully control.
//   - Zero Python/faiss/langchain dependency on the user's machine — the whole
//     feature stays pure-Go.
//   - The extraction prompt + JSON schema here are adapted from HE's
//     AutoGraph (types/graph.py), so we inherit its quality without its weight.
//
// The pipeline is: import (sync, FTS5) → enqueue chunks → worker loop
// (extract → upsert → mark done → emit progress) → done/error. Restart-safe:
// pending chunks are rehydrated from FTS5 by (path, idx).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Extractor turns one chunk of text into entities + relations. The default
// implementation calls an LLM (OpenAI-compatible /chat/completions with a
// JSON schema); a no-op stub is used in tests.
//
// nodePrompt and edgePrompt override the default extraction prompts when
// non-empty. edgePrompt must contain exactly two %s verbs (known-nodes list,
// chunk text). Pass "" for both to use the built-in general prompts.
type Extractor interface {
	Extract(ctx context.Context, chunk string, nodePrompt, edgePrompt string) (ExtractResult, error)
}

// BudgetSetter is an optional capability an Extractor may implement so boot
// can install the global RPM limiter. Extractors that talk HTTP directly
// (instead of going through the provider layer) need this to share the
// per-minute quota with all other LLM calls. Type-assert to discover support.
type BudgetSetter interface {
	SetBudget(acquirer BudgetAcquirer, key string)
}

// BudgetAcquirer gates a request through the global RPM limiter. It's the
// subset of *provider.RequestBudget this package needs, as an interface to
// avoid a rag→provider dependency.
type BudgetAcquirer interface {
	Acquire(ctx context.Context, key string, priority bool) error
}

// ExtractResult is the parsed LLM output for one chunk.
type ExtractResult struct {
	Entities  []Entity
	Relations []Relation
}

// PipelineConfig tunes the worker loop. Defaults favor stability over speed
// (concurrency=1, interval=3s) so we never trip rate limits — the user can
// raise these in [cowork] to speed extraction up on a beefy connection.
type PipelineConfig struct {
	Concurrency int           // simultaneous chunk extractions (default 1)
	Interval    time.Duration // pause between chunks (default 3s)
	MaxRetries  int           // per-chunk retry count (default 3)
	RetryBase   time.Duration // exponential backoff base (default 2s: 2/4/8s)
	ChunkSize   int           // override store.chunkDoc default for extraction (0 = 1200)
	// AttemptTimeout bounds ONE Extract call (a TwoStage extract makes two LLM
	// requests inside one attempt). Must be well under Budget so a slow first
	// attempt leaves the retry an actual chance to run.
	AttemptTimeout time.Duration // default 120s
	// Budget bounds the WHOLE per-chunk task (all attempts + backoffs).
	Budget time.Duration // default 300s
}

// DefaultPipelineConfig returns conservative defaults that prioritize "no
// errors" over throughput. Low concurrency (1) avoids API rate limits (429).
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		Concurrency:    1,
		Interval:       3 * time.Second,
		MaxRetries:     2, // 2 attempts per chunk (1 retry); fail fast so progress moves
		RetryBase:      2 * time.Second,
		ChunkSize:      0, // use chunkDoc's default (3000 chars)
		AttemptTimeout: 120 * time.Second,
		Budget:         300 * time.Second,
	}
}

// ProgressEvent is emitted to the UI on each chunk completion. The frontend
// computes the visible ETA from AvgLatencyMs × (TotalChunks - DoneChunks) so
// the backend doesn't have to push a clock that ticks every second.
// Event kind/scope constants for ProgressEvent. The frontend routes on them:
// progress events update a tree node in place (cheap, throttled), terminal
// events trigger a refresh (rare, never throttled — losing one would leave the
// UI stuck on a stale state forever).
const (
	EventKindProgress = "progress" // intermediate update, safe to drop/throttle
	EventKindTerminal = "terminal" // job reached a final state, must arrive
	EventScopeChunk   = "chunk"    // per-chunk granularity (Go pipeline)
	EventScopeFile    = "file"     // per-file granularity (Hyper-Extract)
)

type ProgressEvent struct {
	JobID        string `json:"jobId"`
	Collection   string `json:"collection"`
	Path         string `json:"path"`
	Status       string `json:"status"` // job status
	DoneChunks   int    `json:"doneChunks"`
	TotalChunks  int    `json:"totalChunks"`
	AvgLatencyMs int64  `json:"avgLatencyMs"` // sliding-average ms/chunk
	Message      string `json:"message"`      // human-readable summary
	// FailedChunks mirrors the job row at emit time so the frontend can count
	// a partially-failed job as a failure in batch summaries.
	FailedChunks int `json:"failedChunks"`
	// Kind distinguishes intermediate progress (throttled) from terminal
	// job-state changes (always delivered). Scope distinguishes chunk-grain
	// (Go pipeline) from file-grain (Hyper-Extract) payloads — Done/Total mean
	// chunks in the former and files in the latter.
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
}

// ProgressEmitter pushes a ProgressEvent to the frontend. The desktop app
// supplies one backed by runtime.EventsEmit("rag:progress"). Nil = no events.
type ProgressEmitter func(ProgressEvent)

// Pipeline owns the extraction worker(s). One per app; call Start once at boot.
type Pipeline struct {
	store     *Store
	extractor Extractor
	cfg       PipelineConfig
	emit      ProgressEmitter
	logf      func(format string, args ...any)

	mu      sync.Mutex
	queue   []chunkTask
	latency *slidingWindow
	wake    chan struct{} // signal that new work was enqueued
	stopCh  chan struct{}
	started bool

	// emitMu/lastEmit back the 1/sec progress-event throttle. Per-instance (a
	// package-global made test pipelines share timing state and made the
	// throttle untestable in parallel).
	emitMu   sync.Mutex
	lastEmit time.Time
}

// chunkTask is one unit of work: extract this chunk, upsert results, mark done.
type chunkTask struct {
	JobID      string
	Collection string
	Path       string
	ChunkIdx   int
	ChunkID    string
	Text       string
	RootPath   string
	RelPath    string
	NodePrompt string // override entity extraction prompt ("" = default)
	EdgePrompt string // override relation extraction prompt ("" = default)
}

// NewPipeline constructs a pipeline. store + extractor are required; emit/logf
// may be nil. Call Start to begin processing.
func NewPipeline(store *Store, extractor Extractor, cfg PipelineConfig, emit ProgressEmitter) *Pipeline {
	if extractor == nil {
		extractor = noopExtractor{}
	}
	if cfg.Concurrency <= 0 {
		cfg = DefaultPipelineConfig()
	}
	// Normalize ONLY the new timeout fields: Interval/RetryBase of 0 are
	// legitimate caller choices ("no rate limit" — used heavily by tests), so
	// they must not be silently replaced with defaults.
	def := DefaultPipelineConfig()
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = def.AttemptTimeout
	}
	if cfg.Budget <= 0 {
		cfg.Budget = def.Budget
	}
	return &Pipeline{
		store:     store,
		extractor: extractor,
		cfg:       cfg,
		emit:      emit,
		logf:      func(string, ...any) {},
		latency:   newSlidingWindow(50),
		// One wake slot per worker: EnqueuePathsEx broadcasts one signal each
		// so a multi-worker pipeline actually parallelizes (a single buffered
		// signal woke ONE worker, which then drained the whole queue alone).
		wake:   make(chan struct{}, max(cfg.Concurrency, 1)),
		stopCh: make(chan struct{}),
	}
}

// SetLogger installs a diagnostic logger (slog-style format string).
func (p *Pipeline) SetLogger(logf func(format string, args ...any)) {
	if logf != nil {
		p.logf = logf
	}
}

// Start launches the worker goroutine(s). Idempotent.
func (p *Pipeline) Start() {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.mu.Unlock()
	for i := 0; i < p.cfg.Concurrency; i++ {
		go p.worker(i)
	}
}

// Resume rehydrates the in-memory queue from durable state after a restart.
// It finds all jobs left in pending/extracting status (interrupted mid-run),
// re-reads each job's chunk text from FTS5, and re-enqueues the chunks that
// were still pending or errored. Call this once after Start() at boot.
//
// This fulfills the restart-safety contract documented at the top of this file
// and in Store.PendingChunksForJob. Without Resume, an interrupted extraction
// leaves jobs stuck forever (workers gone, no tasks in memory). The prompt
// overrides ARE persisted on the job row (node_prompt/edge_prompt, v2 schema),
// so resumed tasks restore the original extraction prompts (e.g. a domain
// template like finance/graph survives a restart).
//
// Returns the number of chunks re-enqueued.
func (p *Pipeline) Resume() int {
	if p.store == nil {
		return 0
	}
	jobs, err := p.store.ResumableJobs()
	if err != nil {
		p.logf("rag: resume query failed: %v", err)
		return 0
	}
	enqueued := 0
	for _, j := range jobs {
		// Re-read chunk texts from FTS5 (chunk text is not persisted on rag_chunks).
		chunks, err := p.store.ChunksByPath(j.Collection, j.Path)
		if err != nil {
			p.logf("rag: resume %s read chunks failed: %v", j.Path, err)
			continue
		}
		// Find which chunk indices are still pending/errored for this job.
		pending, err := p.store.PendingChunksForJob(j.ID)
		if err != nil {
			p.logf("rag: resume %s pending list failed: %v", j.Path, err)
			continue
		}
		p.mu.Lock()
		for _, pc := range pending {
			if pc.Idx < 0 || pc.Idx >= len(chunks) {
				continue // chunk count changed since the job was created; skip
			}
			p.queue = append(p.queue, chunkTask{
				JobID:      j.ID,
				Collection: j.Collection,
				Path:       j.Path,
				ChunkIdx:   pc.Idx,
				ChunkID:    pc.ChunkID,
				Text:       chunks[pc.Idx],
				RootPath:   j.RootPath,
				RelPath:    j.RelPath,
				NodePrompt: j.NodePrompt, // persisted on the job row (v2 schema)
				EdgePrompt: j.EdgePrompt,
			})
			enqueued++
		}
		p.mu.Unlock()
	}
	if enqueued > 0 {
		p.logf("rag: resumed %d pending chunks across %d jobs", enqueued, len(jobs))
		// Wake workers so they pick up the rehydrated tasks.
		p.wakeWorkers()
	}
	return enqueued
}

// wakeWorkers posts one wake signal per pipeline worker (non-blocking). The
// wake channel is buffered to Concurrency, so every sleeping worker gets its
// own signal and multi-worker pipelines actually parallelize.
func (p *Pipeline) wakeWorkers() {
	n := cap(p.wake)
	for i := 0; i < n; i++ {
		select {
		case p.wake <- struct{}{}:
		default:
			return
		}
	}
}

// RetryFailedChunks re-enqueues ONLY the errored chunks of one job — the
// "cheap retry" entrypoint. Unlike EnqueuePaths(force) it does not recreate
// the job row, so successful chunks and their entities survive untouched; a
// 40-chunk document with 2 failed chunks costs 2 more LLM calls, not 40.
// Chunk text is re-read from FTS5 (body_raw) exactly like Resume. Returns the
// number of chunks queued; 0 when the job has nothing to retry.
func (p *Pipeline) RetryFailedChunks(jobID string) (int, error) {
	j, ok, err := p.store.JobByID(jobID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("job %s not found", jobID)
	}
	if j.Status != JobError && !(j.Status == JobDone && j.FailedChunks > 0) {
		return 0, fmt.Errorf("job %s has no failed chunks to retry (status %s)", jobID, j.Status)
	}
	chunks, err := p.store.ChunksByPath(j.Collection, j.Path)
	if err != nil {
		return 0, fmt.Errorf("read chunks: %w", err)
	}
	pending, err := p.store.PendingChunksForJob(jobID)
	if err != nil {
		return 0, fmt.Errorf("pending list: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}
	if err := p.store.SetJobRetrying(jobID); err != nil {
		return 0, err
	}
	p.mu.Lock()
	// Skip chunks already sitting in the queue: a manual retry racing an
	// auto-retry tick would otherwise double-enqueue the same ChunkIDs and
	// burn duplicate LLM calls (MarkChunkDone's done-guard keeps the books
	// straight, but the extra calls are pure waste).
	queued := make(map[string]bool, len(p.queue))
	for _, q := range p.queue {
		queued[q.ChunkID] = true
	}
	enqueued := 0
	for _, pc := range pending {
		if pc.Idx < 0 || pc.Idx >= len(chunks) {
			continue // chunk count changed since the job ran; skip mismatches
		}
		if queued[pc.ChunkID] {
			continue
		}
		enqueued++
		p.queue = append(p.queue, chunkTask{
			JobID:      jobID,
			Collection: j.Collection,
			Path:       j.Path,
			ChunkIdx:   pc.Idx,
			ChunkID:    pc.ChunkID,
			Text:       chunks[pc.Idx],
			RootPath:   j.RootPath,
			RelPath:    j.RelPath,
			NodePrompt: j.NodePrompt, // persisted template prompts survive retry
			EdgePrompt: j.EdgePrompt,
		})
	}
	p.mu.Unlock()
	p.wakeWorkers()
	p.logf("rag: retry %d failed chunks of %s (job %s)", enqueued, j.Path, jobID)
	return enqueued, nil
}

// Stop signals workers to drain and exit. Pending tasks remain in the queue
// (rehydrated on next Start via Resume).
func (p *Pipeline) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.stopCh:
	default:
		close(p.stopCh)
	}
}

// LatencyAvgMs exposes the in-memory sliding-average chunk latency, for the UI's
// ETA tooltip. Returns 0 when no chunks have completed yet (callers fall back to
// the persisted AvgChunkLatencyMs on the store).
func (p *Pipeline) LatencyAvgMs() int64 {
	return p.latency.Avg().Milliseconds()
}

// EnqueuePaths scans files under each path (recursing folders), imports them
// into FTS5 (synchronous, seconds), then enqueues their chunks for extraction.
// Returns the job IDs created. A path may be a file or a folder.
//
// This is the "import" entrypoint from the UI: the user gets the file tree +
// FTS5 search immediately, and extraction runs in the background with progress.
func (p *Pipeline) EnqueuePaths(collection string, paths []string, nodePrompt, edgePrompt string, force bool) ([]string, error) {
	ids, _, err := p.EnqueuePathsEx(collection, paths, nodePrompt, edgePrompt, force)
	return ids, err
}

// EnqueuePathsEx is EnqueuePaths with per-file skip reporting: skipped carries
// "basename (reason)" for every supported file that could not be read, so the
// importer can tell the user "imported 4, 1 failed: x.pdf" instead of the
// file vanishing silently.
func (p *Pipeline) EnqueuePathsEx(collection string, paths []string, nodePrompt, edgePrompt string, force bool) ([]string, []string, error) {
	collection = normalizeCollection(collection)
	if collection == "" {
		collection = "default"
	}
	var jobIDs []string
	var skippedFiles []string
	for _, root := range paths {
		files, err := walkDocs(root)
		if err != nil {
			p.logf("rag: walk %s failed: %v", root, err)
			skippedFiles = append(skippedFiles, fmt.Sprintf("%s (%v)", filepath.Base(root), err))
			continue
		}
		for _, fpath := range files {
			jid, err := p.enqueueFile(collection, root, fpath, nodePrompt, edgePrompt, force)
			if err != nil {
				p.logf("rag: enqueue %s failed: %v", fpath, err)
				skippedFiles = append(skippedFiles, fmt.Sprintf("%s (%v)", filepath.Base(fpath), err))
				continue
			}
			if jid != "" {
				jobIDs = append(jobIDs, jid)
			}
		}
	}
	p.wakeWorkers()
	// Log skipped files so they show in the app log and can be surfaced to the user.
	if len(skippedFiles) > 0 {
		p.logf("rag: %d files imported, %d skipped: %s", len(jobIDs), len(skippedFiles), strings.Join(skippedFiles, "; "))
	}
	return jobIDs, skippedFiles, nil
}

// enqueueFile imports one file into FTS5 + creates an extraction job + queues
// chunk tasks. Returns "" if the file can't be read (skipped, not an error).
func (p *Pipeline) enqueueFile(collection, root, fpath, nodePrompt, edgePrompt string, force bool) (string, error) {
	// 0. Cheap stat-based dedup (runs BEFORE the expensive readDoc): if a prior
	// job already completed AND the file's on-disk size+mtime are unchanged,
	// the body is guaranteed identical — skip re-reading (no markitdown/OCR
	// subprocess, no CMD flash) and re-extraction entirely. This is what makes
	// re-importing a folder containing already-extracted files a no-op instead
	// of resetting every done job back to pending. Content-hash dedup (below)
	// is the second line: it catches edits that keep size+mtime stable (rare)
	// or files whose extractor output is nondeterministic (e.g. markitdown on
	// the SAME PDF producing slightly different text on a second pass — the
	// stat key matches so we never even reach that nondeterministic read).
	if !force {
		if jobID, status, _, _, failed, qerr := p.store.JobStatusForPath(collection, fpath); qerr == nil && jobID != "" && status == JobDone && failed == 0 {
			if statKey, serr := fileStatKey(fpath); serr == nil {
				if prevKey, kerr := p.store.JobStatKeyForPath(collection, fpath); kerr == nil && prevKey != "" && prevKey == statKey {
					p.logf("rag: skip re-extract %s (job %s done, file size+mtime unchanged)", fpath, jobID)
					return jobID, nil
				}
			}
		}
	}
	// 1. Read document once (markitdown for binary formats, direct read for text).
	body, ext, err := readDoc(fpath)
	if err != nil {
		return "", err
	}
	// 2. FTS5 import using pre-read content (avoids re-reading binary files).
	n, err := p.store.ImportContent(collection, fpath, body, ext)
	if err != nil {
		return "", fmt.Errorf("fts5 import %s: %w", fpath, err)
	}
	if n == 0 {
		return "", nil // nothing to extract
	}
	// 2b. Content-hash dedup: compute a hash over the chunked body and skip
	// re-extraction when a prior job already completed with the SAME hash.
	// This is the fallback when the stat key differs (file was touched) but the
	// extracted body is actually identical — e.g. a re-save that didn't change
	// content, or a nondeterministic extractor whose output happened to match.
	// Using a hash (not chunk count) catches content edits that don't change
	// the chunk count (e.g. a few characters added within a 1200-char chunk).
	// FTS5 (above) is already refreshed so text search stays current either way.
	chunks := chunkDoc(body, ext)
	h := sha256.New()
	for _, c := range chunks {
		h.Write([]byte(c))
	}
	contentHash := hex.EncodeToString(h.Sum(nil))
	if !force {
		if jobID, status, _, _, failed, qerr := p.store.JobStatusForPath(collection, fpath); qerr == nil && jobID != "" && status == JobDone && failed == 0 {
			if prevHash, herr := p.store.JobContentHashForPath(collection, fpath); herr == nil && prevHash == contentHash {
				p.logf("rag: skip re-extract %s (job %s done, content hash unchanged)", fpath, jobID)
				return jobID, nil
			}
		}
	}
	// Capture the stat key for THIS version of the file so the next re-import
	// can short-circuit at step 0. Computed after readDoc so it reflects the
	// exact bytes we extracted (mtime may have rolled forward during a slow
	// markitdown run, but size is stable; together they remain a reliable
	// "is this the same file?" signal for the common re-import case).
	statKey, _ := fileStatKey(fpath)
	rel := relPath(root, fpath)
	isDir := isDirPath(root)
	jobID, err := p.store.CreateJob(JobRow{
		Collection:  collection,
		Path:        fpath,
		RelPath:     rel,
		RootPath:    root,
		IsDir:       isDir,
		Status:      JobPending,
		ContentHash: contentHash,
		StatKey:     statKey,
		NodePrompt:  nodePrompt,
		EdgePrompt:  edgePrompt,
	}, chunks)
	if err != nil {
		return "", err
	}
	// 3. Queue tasks.
	p.mu.Lock()
	for i, text := range chunks {
		p.queue = append(p.queue, chunkTask{
			JobID:      jobID,
			Collection: collection,
			Path:       fpath,
			ChunkIdx:   i,
			ChunkID:    fmt.Sprintf("%s_c%d", jobID, i),
			Text:       text,
			RootPath:   root,
			RelPath:    rel,
			NodePrompt: nodePrompt,
			EdgePrompt: edgePrompt,
		})
	}
	p.mu.Unlock()
	p.logf("rag: enqueued %s (%d chunks) job=%s", fpath, len(chunks), jobID)
	return jobID, nil
}

// CancelJob marks a job cancelled and drops its pending tasks from the queue.
// In-flight chunks finish (we don't interrupt an LLM call mid-flight).
func (p *Pipeline) CancelJob(jobID string) error {
	if err := p.store.SetJobStatus(jobID, JobCancelled); err != nil {
		return err
	}
	p.mu.Lock()
	out := p.queue[:0]
	for _, t := range p.queue {
		if t.JobID != jobID {
			out = append(out, t)
		}
	}
	p.queue = out
	p.mu.Unlock()
	return nil
}

// worker is the extraction loop. It pulls tasks from the queue, extracts with
// retry/backoff, upserts results, marks done, emits progress, then sleeps the
// rate-limit interval before the next chunk.
func (p *Pipeline) worker(id int) {
	for {
		select {
		case <-p.stopCh:
			return
		default:
		}
		task, ok := p.dequeue()
		if !ok {
			// Queue empty — wait for a wake signal or stop.
			select {
			case <-p.stopCh:
				return
			case <-p.wake:
			}
			continue
		}
		p.processTask(id, task)
		// Rate limit: pause between chunks to avoid hammering the LLM.
		if p.cfg.Interval > 0 {
			select {
			case <-p.stopCh:
				return
			case <-time.After(p.cfg.Interval):
			}
		}
	}
}

// dequeue pops the next task; returns ok=false if queue is empty.
func (p *Pipeline) dequeue() (chunkTask, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) == 0 {
		return chunkTask{}, false
	}
	t := p.queue[0]
	p.queue = p.queue[1:]
	return t, true
}

// processTask runs one chunk through extract → upsert → mark done with retries.
//
// Timeout structure (R0-1): each attempt gets its OWN attempt-timeout context,
// nested under one per-task budget. The old design gave the whole task a single
// 180s context equal to the HTTP client timeout, so a slow first attempt
// exhausted the budget and the retry never actually ran — the stored error was
// a bare "context deadline exceeded" with the real cause lost.
func (p *Pipeline) processTask(workerID int, t chunkTask) {
	// A task dequeued before the user cancelled its job must not resurrect the
	// cancelled state (the conditional update below is the second guard).
	if job, ok, _ := p.store.JobByID(t.JobID); ok && job.Status == JobCancelled {
		return
	}
	// Mark job as extracting — only from pending; the conditional UPDATE makes
	// cancelled/done/error states immutable from here.
	_ = p.store.MarkJobExtracting(t.JobID)

	budget := p.cfg.Budget
	budgetCtx, cancelBudget := context.WithTimeout(context.Background(), budget)
	defer cancelBudget()

	start := time.Now()
	var lastErr error
	attempts := p.cfg.MaxRetries
	if attempts < 1 {
		attempts = 1 // a misconfigured 0 must not mark unprocessed chunks done
	}
	for attempt := 0; attempt < attempts; attempt++ {
		attemptCtx, cancelAttempt := context.WithTimeout(budgetCtx, p.cfg.AttemptTimeout)
		res, err := p.extractor.Extract(attemptCtx, t.Text, t.NodePrompt, t.EdgePrompt)
		cancelAttempt()
		if err == nil {
			// Drop relations whose endpoints aren't in this chunk's entity set
			// (LLM hallucinations) before upsert — mirrors HE's
			// _prune_dangling_edges. Scoped to this chunk's entities; cross-
			// chunk entity reuse still happens via name normalization at upsert.
			before := len(res.Relations)
			res.Relations = pruneDanglingRelations(res)
			if dropped := before - len(res.Relations); dropped > 0 {
				p.logf("rag: pruned %d dangling relations in %s chunk %d", dropped, t.Path, t.ChunkIdx)
			}
			src := Source{Path: t.Path, Chunk: t.ChunkIdx}
			for _, e := range res.Entities {
				if e := p.upsertEntity(t.Collection, e, src); e != nil {
					p.logf("rag: upsert entity in %s failed: %v", t.Path, e)
				}
			}
			for _, r := range res.Relations {
				if e := p.upsertRelation(t.Collection, r, src); e != nil {
					p.logf("rag: upsert relation in %s failed: %v", t.Path, e)
				}
			}
			lastErr = nil
			break
		}
		// Keep the attempt's real error (e.g. "extract http: ...", HTTP 429
		// bodies). It is never overwritten with a bare ctx.Err() below.
		lastErr = err
		p.logf("rag: extract %s chunk %d attempt %d failed: %v", t.Path, t.ChunkIdx, attempt+1, err)
		if attempt < p.cfg.MaxRetries-1 {
			backoff := p.cfg.RetryBase << uint(attempt)
			select {
			case <-budgetCtx.Done():
				goto done // budget exhausted — surface lastErr as-is
			case <-time.After(backoff):
			}
		}
	}
done:
	latencyMs := time.Since(start).Milliseconds()
	if err := p.store.MarkChunkDone(t.ChunkID, t.JobID, latencyMs, lastErr); err != nil {
		p.logf("rag: mark chunk done failed: %v", err)
	}
	// Only successful attempts feed the ETA window — a 180s timeout sample
	// would inflate the projected remaining time several-fold.
	if lastErr == nil {
		p.latency.Add(time.Duration(latencyMs) * time.Millisecond)
	}
	p.emitProgress(t)
}

// upsertEntity/Relation are thin wrappers so processTask can ignore the store's
// mu concerns (already handled inside Upsert*).
func (p *Pipeline) upsertEntity(collection string, e Entity, src Source) error {
	return p.store.UpsertEntity(collection, e, src)
}
func (p *Pipeline) upsertRelation(collection string, r Relation, src Source) error {
	return p.store.UpsertRelation(collection, r, src)
}

// emitProgress sends a ProgressEvent to the UI. Intermediate progress is
// throttled to 1/sec to avoid flooding the webview on large folders; terminal
// events (job done/error) bypass the throttle entirely — with many small jobs
// finishing back-to-back, the old unconditional throttle could swallow the
// LAST event of a batch and leave the UI stuck on a stale state. The throttle
// window lives on the Pipeline (not a package global) so test pipelines don't
// share timing state.
func (p *Pipeline) emitProgress(t chunkTask) {
	if p.emit == nil {
		return
	}
	job, ok, err := p.store.JobByID(t.JobID)
	if err != nil || !ok {
		return
	}
	kind := EventKindProgress
	if job.Status == JobDone || job.Status == JobError {
		kind = EventKindTerminal
	}
	if kind == EventKindProgress {
		p.emitMu.Lock()
		if time.Since(p.lastEmit) < time.Second {
			p.emitMu.Unlock()
			return // throttled
		}
		p.lastEmit = time.Now()
		p.emitMu.Unlock()
	}
	avg := p.latency.Avg()
	remaining := job.TotalChunks - job.DoneChunks
	if remaining < 0 {
		remaining = 0
	}
	eta := avg * time.Duration(remaining)
	msg := fmt.Sprintf("抽取中… %d/%d · 平均 %s/块 · 预计还需 %s",
		job.DoneChunks, job.TotalChunks, durStr(avg), durStr(eta))
	switch job.Status {
	case JobDone:
		if job.FailedChunks > 0 {
			msg = fmt.Sprintf("部分完成：%d/%d 块已抽取，%d 块失败", job.DoneChunks-job.FailedChunks, job.TotalChunks, job.FailedChunks)
		} else {
			msg = fmt.Sprintf("完成：%d/%d 块已抽取", job.DoneChunks, job.TotalChunks)
		}
	case JobError:
		msg = fmt.Sprintf("出错：%s", job.ErrorMsg)
	}
	p.emit(ProgressEvent{
		JobID:        t.JobID,
		Collection:   job.Collection,
		Path:         job.Path,
		Status:       job.Status,
		DoneChunks:   job.DoneChunks,
		TotalChunks:  job.TotalChunks,
		AvgLatencyMs: avg.Milliseconds(),
		Message:      msg,
		FailedChunks: job.FailedChunks,
		Kind:         kind,
		Scope:        EventScopeChunk,
	})
}

// durStr renders a duration as a compact human string (e.g. "1分12秒", "8s").
func durStr(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	rem := s % 60
	if m < 60 {
		return fmt.Sprintf("%d分%02d秒", m, rem)
	}
	h := m / 60
	return fmt.Sprintf("%dh%02dm", h, m%60)
}

// --- slidingWindow: rolling avg of the last N chunk latencies --------------

type slidingWindow struct {
	mu  sync.Mutex
	buf []time.Duration
	pos int
	n   int
}

func newSlidingWindow(cap int) *slidingWindow { return &slidingWindow{buf: make([]time.Duration, cap)} }

func (s *slidingWindow) Add(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf[s.pos] = d
	s.pos = (s.pos + 1) % len(s.buf)
	if s.n < len(s.buf) {
		s.n++
	}
}

// Avg returns the mean of the buffered samples. Returns a 3-second default
// when empty so the UI shows a plausible ETA before the first chunk completes.
func (s *slidingWindow) Avg() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n == 0 {
		return 3 * time.Second // fallback before any measurement
	}
	var total time.Duration
	for i := 0; i < s.n; i++ {
		total += s.buf[i]
	}
	return total / time.Duration(s.n)
}

// --- helpers ----------------------------------------------------------------

// skipDirNames are non-dot directories that hold dependency/metadata trees no
// user ever wants ingested into the knowledge base (R0-6).
var skipDirNames = map[string]bool{"node_modules": true}

// walkDocs returns all text-like files under root (recursively). root may be a
// single file (returns [root] if supported) or a directory. Hidden
// (dot-prefixed) directories — .git, .fairpeer, .idea — and dependency trees
// are pruned: importing a project folder previously ingested .git/objects
// binaries and fairpeer's own .fairpeer/*.json metadata as "documents".
func walkDocs(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if isSupportedExt(root) {
			return []string{root}, nil
		}
		// An explicit single-file import of an unsupported type is reported,
		// not silently dropped (folder walks prune quietly — different case).
		return nil, fmt.Errorf("unsupported file type: .%s", strings.TrimPrefix(filepath.Ext(root), "."))
	}
	var out []string
	err = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if fi.IsDir() {
			if path == root {
				return nil
			}
			name := fi.Name()
			if strings.HasPrefix(name, ".") || skipDirNames[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if isSupportedExt(path) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}

// isSupportedExt mirrors readDoc's text-format whitelist. Extension-less files
// are NOT supported: on real project folders they are mostly .git leftovers and
// lockfiles whose bytes would be force-decoded into garbage chunks.
func isSupportedExt(path string) bool {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch ext {
	case "txt", "md", "markdown", "csv", "tsv", "json", "html", "htm",
		"py", "go", "js", "ts", "tsx", "java", "c", "cpp", "h", "rs", "yaml", "yml",
		"docx", "xlsx", "xls", "pptx", "pdf", "epub", "doc", "ppt", "msg": // office formats via markitdown
		return true
	}
	return false
}

func relPath(root, fpath string) string {
	rel, err := filepath.Rel(filepath.Dir(root), fpath)
	if err != nil {
		return fpath
	}
	return rel
}

func isDirPath(root string) bool {
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

// FilterJobsForExtraction picks which jobs (re-)run for a collection-wide
// extract. full=true (mode "full") re-runs EVERY job; otherwise jobs already
// done are skipped (incremental/silent). Paths are deduped — AllJobs returns
// one row per (collection, path) already, but history edits make that cheap
// insurance. Shared by the Go pipeline and the Hyper-Extract paths so both
// honor the same incremental contract (the HE path previously re-ran done
// files — a silent full re-burn on every template extract).
func FilterJobsForExtraction(jobs []JobRow, full bool) []JobRow {
	seen := make(map[string]bool, len(jobs))
	out := make([]JobRow, 0, len(jobs))
	for _, j := range jobs {
		if seen[j.Path] {
			continue
		}
		seen[j.Path] = true
		if !full && j.Status == JobDone {
			continue
		}
		out = append(out, j)
	}
	return out
}

// fileStatKey returns a cheap "is this the same file?" fingerprint
// ("size:mtimeNanos") for re-import dedup. It is intentionally NOT a content
// hash — it exists so we can short-circuit BEFORE the expensive readDoc
// (markitdown/OCR subprocess). size+mtime together are a reliable sameness
// signal on all major OSes: a content edit changes size or mtime, and a pure
// mtime touch (e.g. re-save) leaves size stable but bumps mtime — both are
// caught. Returns "" + error if the file can't be statted (caller treats that
// as "no stat key available" and falls through to content-hash dedup).
func fileStatKey(fpath string) (string, error) {
	fi, err := os.Stat(fpath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano()), nil
}

// noopExtractor is a no-op Extractor for tests / when no LLM is configured.
// It returns no entities so the pipeline is exercised but produces no data.
type noopExtractor struct{}

func (noopExtractor) Extract(_ context.Context, _ string, _, _ string) (ExtractResult, error) {
	return ExtractResult{}, nil
}

// pruneDanglingRelations drops relations whose source or target isn't among
// this chunk's extracted entities. LLMs sometimes emit a relation pointing at an
// entity they never actually extracted (a hallucinated endpoint); keeping those
// pollutes the graph with edges to non-existent nodes. Both sides are compared
// by normalized name so casing/whitespace differences don't cause false drops.
// This is a Go port of HE's _prune_dangling_edges (graph.py:624), simplified to
// check only the in-chunk entity set rather than the whole store — cross-chunk
// legitimate reuse still happens because upsert merges by normalized name.
func pruneDanglingRelations(res ExtractResult) []Relation {
	if len(res.Entities) == 0 {
		// No entities at all → all relations are dangling by definition.
		return nil
	}
	known := make(map[string]bool, len(res.Entities))
	for _, e := range res.Entities {
		known[normalizeName(e.NameRaw)] = true
	}
	kept := res.Relations[:0]
	for _, r := range res.Relations {
		srcNorm := normalizeName(r.Source)
		tgtNorm := normalizeName(r.Target)
		// Drop self-loops (source == target) — these are LLM noise, not real
		// knowledge (e.g. "故障处置 负责 故障处置" carries no information).
		if srcNorm == tgtNorm {
			continue
		}
		if known[srcNorm] && known[tgtNorm] {
			kept = append(kept, r)
		}
	}
	return kept
}

// ParseExtractJSON unmarshals an LLM JSON response into ExtractResult. The LLM
// may return either the canonical {entities:[...], relations:[...]} shape or
// wrap it; we tolerate both. Exposed so the LLM extractor impl can share
// parsing logic.
func ParseExtractJSON(b []byte) (ExtractResult, error) {
	var raw struct {
		Entities []struct {
			Name        string `json:"name"`
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"entities"`
		Relations []struct {
			Source      string  `json:"source"`
			Target      string  `json:"target"`
			Type        string  `json:"type"`
			Description string  `json:"description"`
			Strength    float64 `json:"strength"`
		} `json:"relations"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return ExtractResult{}, fmt.Errorf("parse extract json: %w", err)
	}
	res := ExtractResult{}
	for _, e := range raw.Entities {
		res.Entities = append(res.Entities, Entity{
			NameRaw:     e.Name,
			Type:        e.Type,
			Description: e.Description,
		})
	}
	for _, r := range raw.Relations {
		res.Relations = append(res.Relations, Relation{
			Source:      r.Source,
			Target:      r.Target,
			Type:        r.Type,
			Description: r.Description,
			Strength:    r.Strength,
		})
	}
	return res, nil
}

// ExtractionPrompt is the system+user prompt sent to the LLM for each chunk.
// Adapted from Hyper-Extract's AutoGraph default (types/graph.py:41). Kept as
// a constant so the LLM impl and any future provider impl share it.
const ExtractionPrompt = `你是知识抽取助手。从下面这段文本中抽取所有实体（人/组织/项目/产品/概念/地点/事件/主题）和它们之间的关系。

要求：
1. 实体 name 用规范化的简称（如"中国移动"而非"中国移动通信集团有限公司"），全文保持一致
2. 关系只连接已抽取的实体，不要凭空造实体
3. 描述简洁，控制在 50 字内
4. 只抽取文本明确提到的事实，不要推理或脑补
5. 不要抽取纯代词或泛指（如"他/该产品/相关人员"），必须有独立指代意义
6. 关系 type 用简短谓词，如 is_a/part_of/负责/属于/包含/相关/位于
7. 每条关系给出 strength 评分(1-10整数)：10=核心/直接/明确的关系（如"负责""属于""包含"），5=一般关联，1=弱/间接/模糊关系

只返回 JSON，格式如下：
{"entities":[{"name":"张三","type":"person","description":"..."}],"relations":[{"source":"张三","target":"项目Alpha","type":"负责","description":"...","strength":8}]}

type 可选值：person, organization, project, product, concept, location, event, topic, other

### 文本：
%s`

// NodeExtractionPrompt is stage 1 of the two-stage extraction (borrowed from
// HE graph.py:510): extract ONLY entities first. The returned entity list then
// seeds stage 2's {known_nodes} so relations are forced to reference real
// entities, dramatically cutting hallucinated edges. %s = the chunk text.
const NodeExtractionPrompt = `你是知识抽取助手。从下面这段文本中抽取所有实体（人/组织/项目/产品/概念/地点/事件/主题）。

要求：
1. 实体 name 用规范化的简称（如"中国移动"而非"中国移动通信集团有限公司"），全文保持一致
2. 只抽取文本明确提到的实体，不要推理或脑补
3. 不要抽取纯代词或泛指（如"他/该产品/相关人员"），必须有独立指代意义
4. 描述简洁，控制在 50 字内

只返回 JSON：{"entities":[{"name":"张三","type":"person","description":"..."}]}
type 可选值：person, organization, project, product, concept, location, event, topic, other

### 文本：
%s`

// EdgeExtractionPrompt is stage 2: given the entities already extracted from
// THIS chunk (injected as {known_nodes}), extract relations constrained to
// those endpoints — mirroring HE's graph.py:611 pattern. %s = the known-nodes
// list, %s = the chunk text.
const EdgeExtractionPrompt = `你是关系抽取助手。下面已给出本段文本中抽取出的实体列表。请只在这些已知实体之间抽取关系。

关键约束：
1. 关系的 source 和 target 必须出现在下面的"已知实体"列表中，严禁引用列表外的实体
2. 只抽取文本明确提到的事实，不要推理或脑补
3. 关系 type 用简短谓词，如 is_a/part_of/负责/属于/包含/相关/位于
4. 描述简洁，控制在 50 字内
5. 每条关系给出 strength 评分(1-10整数)：10=核心/直接/明确的关系（如"负责""属于""包含"），5=一般关联，1=弱/间接/模糊关系

已知实体：
%s

只返回 JSON：{"relations":[{"source":"张三","target":"项目Alpha","type":"负责","description":"...","strength":8}]}

### 文本：
%s`
