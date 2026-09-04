package netdev

// job.go — Job 引擎（NETDEV_SPEC v1 §10.5 C 批 → v2 §九 R4）：多步骤长任务
// harness。诊断 runbook 的步骤带 expect/timeout/retry/on-fail 与断点，执行
// 经 m.Exec 的只读密封（Job 是诊断 harness——写动作仍然只存在于变更，本
// 引擎不给任何命令开分类器旁路）；watchdog 预算（墙钟/命令数/连续失败熔
// 断）在每步之间裁决，超限即暂停等人。断点续跑：paused 状态持久化到盘，
// JobResume 从断点继续；割接模式（cutover.go）与 R5 入侵排查向导复用本引
// 擎的步骤语义（runJobStep）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Job statuses.
const (
	JobRunning = "running"
	JobPaused  = "paused"
	JobDone    = "done"
	JobFailed  = "failed"  // a step failed with on-fail=abort — later steps untouched
	JobAborted = "aborted" // human pressed stop
)

// Step statuses.
const (
	JobStepPending = "pending"
	JobStepRunning = "running"
	JobStepOK      = "ok"
	JobStepFailed  = "failed"
	JobStepSkipped = "skipped"
)

// OnFail policies.
const (
	JobOnFailPause    = "pause"    // default — freeze for a human, resumable
	JobOnFailAbort    = "abort"    // mark the job failed, skip the rest
	JobOnFailContinue = "continue" // log and move on (fail streak still counts)
)

// JobStep is one runbook entry: a single sealed read command with its own
// success criterion (Expect), timing, and failure policy.
type JobStep struct {
	Name        string `json:"name"`
	Device      string `json:"device"`
	Command     string `json:"command"`
	Expect      string `json:"expect,omitempty"`       // regex that must appear in the output within the timeout
	TimeoutSec  int    `json:"timeout_sec,omitempty"`  // per attempt (default 60)
	Retries     int    `json:"retries,omitempty"`      // extra attempts after the first failure (≤4)
	OnFail      string `json:"on_fail,omitempty"`      // pause | abort | continue (default pause)
	PauseBefore bool   `json:"pause_before,omitempty"` // 断点：human confirms before this step runs
}

// JobStepState is the step's execution trail.
type JobStepState struct {
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts,omitempty"`
	Output    string     `json:"output,omitempty"` // last attempt tail, already redacted by Exec
	Error     string     `json:"error,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// Job watchdog budgets (v1 C 批). Zero fields take the defaults; a paused job
// does not burn wall clock.
type JobBudget struct {
	MaxWallSec  int `json:"max_wall_sec,omitempty"` // default 1800
	MaxCommands int `json:"max_commands,omitempty"` // default 200
	FailStreak  int `json:"fail_streak,omitempty"`  // default 3 consecutive failures → circuit-break pause
}

// Job is one runbook run.
type Job struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Steps     []JobStep      `json:"steps"`
	StepState []JobStepState `json:"step_state"`
	Status    string         `json:"status"`
	Budget    JobBudget      `json:"budget"`

	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`

	ActiveMS     int64  `json:"active_ms"`            // wall clock actually spent running (pauses excluded)
	Commands     int    `json:"commands"`             // attempts executed against the budget
	Cursor       int    `json:"cursor"`               // next step index (断点续跑 anchor)
	BreakpointOK int    `json:"breakpoint_ok"`        // cursor whose PauseBefore was human-confirmed
	PauseNote    string `json:"pause_note,omitempty"` // why it paused — shown to the human
	CreatedBy    string `json:"created_by,omitempty"`
}

// jobsDirOverride isolates job storage in tests.
var jobsDirOverride string

func jobsDir() string {
	if jobsDirOverride != "" {
		return jobsDirOverride
	}
	return filepath.Join(netdevStateDir(), "jobs")
}

var (
	jobMu  sync.Mutex
	jobSeq int
)

func newJobID() string {
	jobMu.Lock()
	defer jobMu.Unlock()
	jobSeq++
	return fmt.Sprintf("J%s-%d", time.Now().Format("20060102"), jobSeq)
}

func saveJob(j *Job) error {
	if err := os.MkdirAll(jobsDir(), 0o700); err != nil {
		return err
	}
	jobMu.Lock()
	defer jobMu.Unlock()
	return saveJobLocked(j)
}

// saveJobLocked is saveJob with the caller holding jobMu — the write half of
// an atomic load→check→set→save transition.
func saveJobLocked(j *Job) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(jobsDir(), j.ID+".json"), b, 0o600)
}

// GetJob loads one job.
func GetJob(id string) (*Job, error) {
	b, err := os.ReadFile(filepath.Join(jobsDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

// ListJobs returns jobs newest-first.
func ListJobs() ([]*Job, error) {
	entries, err := os.ReadDir(jobsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Job
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if j, err := GetJob(strings.TrimSuffix(e.Name(), ".json")); err == nil {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].CreatedAt.After(out[k].CreatedAt) })
	return out, nil
}

// ── validation ───────────────────────────────────────────────────────────────

func (j *Job) normalize() error {
	if strings.TrimSpace(j.Name) == "" {
		return fmt.Errorf("job: name is required")
	}
	if len(j.Steps) == 0 {
		return fmt.Errorf("job %q: no steps", j.Name)
	}
	for i := range j.Steps {
		s := &j.Steps[i]
		if s.Name == "" {
			s.Name = fmt.Sprintf("step-%d", i+1)
		}
		if s.Device == "" {
			return fmt.Errorf("job %q step %q: device is required", j.Name, s.Name)
		}
		if s.Command == "" {
			return fmt.Errorf("job %q step %q: command is required", j.Name, s.Name)
		}
		if strings.ContainsAny(s.Command, "\n\r\x00") {
			return fmt.Errorf("job %q step %q: one command per step (no newlines)", j.Name, s.Name)
		}
		if s.Expect != "" {
			if _, err := regexp.Compile(s.Expect); err != nil {
				return fmt.Errorf("job %q step %q: expect: %v", j.Name, s.Name, err)
			}
		}
		if s.TimeoutSec <= 0 {
			s.TimeoutSec = 60
		}
		if s.Retries < 0 {
			s.Retries = 0
		}
		if s.Retries > 4 {
			s.Retries = 4
		}
		switch s.OnFail {
		case "", JobOnFailPause:
			s.OnFail = JobOnFailPause
		case JobOnFailAbort, JobOnFailContinue:
		default:
			return fmt.Errorf("job %q step %q: on_fail must be pause|abort|continue", j.Name, s.Name)
		}
	}
	if j.Budget.MaxWallSec <= 0 {
		j.Budget.MaxWallSec = 1800
	}
	if j.Budget.MaxCommands <= 0 {
		j.Budget.MaxCommands = 200
	}
	if j.Budget.FailStreak <= 0 {
		j.Budget.FailStreak = 3
	}
	if j.StepState == nil {
		j.StepState = make([]JobStepState, len(j.Steps))
		for i := range j.StepState {
			j.StepState[i].Status = JobStepPending
		}
	}
	return nil
}

// ── runner registry ──────────────────────────────────────────────────────────

type jobRun struct {
	cancel     context.CancelFunc
	pauseReq   chan struct{} // user pause — honored between steps/retries
	activeFrom time.Time     // wall-clock accounting anchor
	done       chan struct{} // closed when the runner goroutine exits
}

var (
	jobRunsMu sync.Mutex
	jobRuns   = map[string]*jobRun{}
)

// JobStart validates the definition, persists it as running, and launches the
// runner goroutine. The definition arrives from the desktop bridge (UI /
// runbook presets) — the agent has no tool that creates jobs.
func (m *Manager) JobStart(def *Job) (*Job, error) {
	if def.ID != "" {
		return nil, fmt.Errorf("job: new runs take no ID (assigned here)")
	}
	if err := def.normalize(); err != nil {
		return nil, err
	}
	def.ID = newJobID()
	StateEventSnap(StateEventJobStart, def.ID, StateActorUser, filepath.Join(jobsDir(), def.ID+".json"))
	def.Status = JobRunning
	def.CreatedAt = time.Now()
	now := time.Now()
	def.StartedAt = &now
	def.Cursor = 0
	if err := saveJob(def); err != nil {
		return nil, err
	}
	_ = AppendAudit(Audit{Device: "(job)", Command: "start " + def.ID + " " + def.Name, Class: "job", Status: AuditOK})
	m.jobLaunch(def)
	return def, nil
}

// RunJobSync starts a job and BLOCKS until its runner exits (terminal status,
// pause, or ctx end — on ctx end the job is aborted). This is how synchronous
// callers ride the engine (§4.1 体检电池 runbook): same step semantics,
// budgets, and persisted job trail, minus the async interaction.
func (m *Manager) RunJobSync(ctx context.Context, def *Job) (*Job, error) {
	j, err := m.JobStart(def)
	if err != nil {
		return nil, err
	}
	jobRunsMu.Lock()
	run := jobRuns[j.ID]
	jobRunsMu.Unlock()
	if run == nil {
		return GetJob(j.ID) // finished before we looked (tiny battery)
	}
	select {
	case <-ctx.Done():
		aborted, _ := JobAbort(j.ID)
		if aborted != nil {
			return aborted, nil
		}
		return GetJob(j.ID)
	case <-run.done:
	}
	return GetJob(j.ID)
}

// jobLaunch (re)starts the runner goroutine for a running job.
func (m *Manager) jobLaunch(j *Job) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &jobRun{cancel: cancel, pauseReq: make(chan struct{}), activeFrom: time.Now(), done: make(chan struct{})}
	jobRunsMu.Lock()
	if old, ok := jobRuns[j.ID]; ok {
		old.cancel()
	}
	jobRuns[j.ID] = run
	jobRunsMu.Unlock()
	go m.jobRunner(ctx, run, j.ID)
}

// JobPause asks a running job to freeze at the next boundary (between steps or
// retries). The in-flight attempt finishes — commands are short and read-only.
func JobPause(id string) error {
	j, err := GetJob(id)
	if err != nil {
		return err
	}
	if j.Status != JobRunning {
		return fmt.Errorf("job %s: status %s — only running jobs pause", id, j.Status)
	}
	jobRunsMu.Lock()
	run, ok := jobRuns[id]
	jobRunsMu.Unlock()
	if !ok {
		return fmt.Errorf("job %s: no live runner (state drift — reload the list)", id)
	}
	close(run.pauseReq)
	return nil
}

// JobResume continues a paused job from its cursor.
func (m *Manager) JobResume(id string) (*Job, error) {
	jobMu.Lock()
	j, err := GetJob(id)
	if err != nil {
		jobMu.Unlock()
		return nil, err
	}
	if j.Status != JobPaused {
		jobMu.Unlock()
		return nil, fmt.Errorf("job %s: status %s — only paused jobs resume", id, j.Status)
	}
	StateEventSnap(StateEventJobResume, id, StateActorUser, filepath.Join(jobsDir(), id+".json"))
	j.Status = JobRunning
	j.PauseNote = ""
	// Resuming past a breakpoint records the human's confirmation on that
	// cursor (the runner would otherwise re-freeze at the same step).
	if j.Cursor < len(j.Steps) && j.Steps[j.Cursor].PauseBefore {
		j.BreakpointOK = j.Cursor
	}
	if err := saveJobLocked(j); err != nil {
		jobMu.Unlock()
		return nil, err
	}
	jobMu.Unlock()
	_ = AppendAudit(Audit{Device: "(job)", Command: "resume " + id, Class: "job", Status: AuditOK})
	m.jobLaunch(j)
	return j, nil
}

// JobAbort stops a running/paused job for good; remaining steps are skipped.
func JobAbort(id string) (*Job, error) {
	jobMu.Lock()
	j, err := GetJob(id)
	if err != nil {
		jobMu.Unlock()
		return nil, err
	}
	if j.Status != JobRunning && j.Status != JobPaused {
		jobMu.Unlock()
		return nil, fmt.Errorf("job %s: status %s — only running/paused jobs abort", id, j.Status)
	}
	StateEventSnap(StateEventJobAbort, id, StateActorUser, filepath.Join(jobsDir(), id+".json"))
	jobRunsMu.Lock()
	if run, ok := jobRuns[id]; ok {
		j.ActiveMS += time.Since(run.activeFrom).Milliseconds()
		run.cancel()
	}
	jobRunsMu.Unlock()
	j.Status = JobAborted
	j.PauseNote = ""
	for i := range j.StepState {
		if j.StepState[i].Status == JobStepPending {
			j.StepState[i].Status = JobStepSkipped
		}
	}
	now := time.Now()
	j.EndedAt = &now
	if err := saveJobLocked(j); err != nil {
		jobMu.Unlock()
		return nil, err
	}
	jobMu.Unlock()
	_ = AppendAudit(Audit{Device: "(job)", Command: "abort " + id, Class: "job", Status: AuditFailure})
	return j, nil
}

// ── the runner ───────────────────────────────────────────────────────────────

func (m *Manager) jobRunner(ctx context.Context, run *jobRun, id string) {
	defer func() {
		jobRunsMu.Lock()
		if jobRuns[id] == run {
			delete(jobRuns, id)
		}
		jobRunsMu.Unlock()
		run.cancel()
		close(run.done)
	}()

	failStreak := 0
	for {
		j, err := GetJob(id)
		if err != nil || j.Status != JobRunning {
			return
		}

		// Watchdog budgets (v1 C 批): wall clock, command count, fail streak.
		activeMS := j.ActiveMS + time.Since(run.activeFrom).Milliseconds()
		why := ""
		switch {
		case activeMS > int64(j.Budget.MaxWallSec)*1000:
			why = fmt.Sprintf("watchdog: wall clock %.1fm exceeds budget %dm", float64(activeMS)/60000, j.Budget.MaxWallSec/60)
		case j.Commands >= j.Budget.MaxCommands:
			why = fmt.Sprintf("watchdog: %d commands executed, budget is %d", j.Commands, j.Budget.MaxCommands)
		case failStreak >= j.Budget.FailStreak:
			why = fmt.Sprintf("watchdog: %d consecutive failed steps — circuit breaker", failStreak)
		}
		if why != "" {
			m.jobFreeze(run, id, why)
			return
		}

		if j.Cursor >= len(j.Steps) {
			m.jobFinish(run, id, JobDone, "")
			return
		}
		step := j.Steps[j.Cursor]

		// 断点：a human confirms before this step runs (spec C 批). Resume
		// records the confirmed cursor in BreakpointOK so the same breakpoint
		// does not immediately re-fire.
		if step.PauseBefore && j.BreakpointOK != j.Cursor {
			m.jobFreeze(run, id, "断点 "+step.Name+" — 确认后继续")
			return
		}

		// Run the step (shared with cutover gates).
		state := m.runJobStep(ctx, j, j.Cursor, step)

		// Fold the result back into the persisted job.
		j, err = GetJob(id)
		if err != nil || j.Status != JobRunning {
			return
		}
		if j.Cursor < len(j.StepState) {
			j.StepState[j.Cursor] = state
		}
		j.Commands += state.Attempts

		// Honor a user pause requested during the step.
		select {
		case <-run.pauseReq:
			j.ActiveMS = activeMS
			if state.Status == JobStepOK {
				j.Cursor++
			}
			j.Status = JobPaused
			j.PauseNote = "人工暂停"
			_ = saveJob(j)
			return
		default:
		}

		if state.Status == JobStepOK {
			failStreak = 0
			j.Cursor++
			// ActiveMS is only folded in on exit paths (freeze/finish/pause) —
			// persisting it mid-run would double-count against activeFrom.
			if err := saveJob(j); err != nil {
				return
			}
			continue
		}

		failStreak++
		switch step.OnFail {
		case JobOnFailAbort:
			j.ActiveMS = activeMS
			j.Status = JobFailed
			for i := j.Cursor + 1; i < len(j.StepState); i++ {
				if j.StepState[i].Status == JobStepPending {
					j.StepState[i].Status = JobStepSkipped
				}
			}
			now := time.Now()
			j.EndedAt = &now
			_ = saveJob(j)
			_ = AppendAudit(Audit{Device: "(job)", Command: "end " + id + " failed @" + step.Name, Class: "job", Status: AuditFailure})
			return
		case JobOnFailContinue:
			j.Cursor++
			_ = saveJob(j)
			continue
		default: // pause — the human decides resume/abort
			jobMu.Lock()
			j.ActiveMS = activeMS
			m.jobFreezeLocked(run, j, "步骤 "+step.Name+" 失败："+firstLine(state.Error)+" — on-fail=pause")
			jobMu.Unlock()
			return
		}
	}
}

// runJobStep executes one step with retries and returns its state. It never
// mutates the persisted job — the caller folds the result in (so a user pause
// arriving mid-step wins over the step's own transition).
func (m *Manager) runJobStep(ctx context.Context, j *Job, idx int, step JobStep) JobStepState {
	state := JobStepState{Status: JobStepRunning, Attempts: 0}
	now := time.Now()
	state.StartedAt = &now

	var expectRe *regexp.Regexp
	if step.Expect != "" {
		expectRe = regexp.MustCompile(step.Expect)
	}

	lastErr := ""
	for attempt := 0; attempt <= step.Retries; attempt++ {
		state.Attempts++
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(step.TimeoutSec)*time.Second)
		res := m.Exec(attemptCtx, step.Device, step.Command)
		cancel()
		switch {
		case res.Refused:
			lastErr = "refused: " + res.Refusal
		case res.IsError:
			lastErr = "device error: " + firstLine(res.Output)
			state.Output = tailStr(res.Output, 4096)
		case expectRe != nil && !expectRe.MatchString(res.Output):
			lastErr = fmt.Sprintf("expect %q not matched in output (tail: %.120s)", step.Expect, tailStr(res.Output, 120))
			state.Output = tailStr(res.Output, 4096)
		default:
			state.Output = tailStr(res.Output, 4096)
			state.Status = JobStepOK
			state.Error = ""
			end := time.Now()
			state.EndedAt = &end
			return state
		}
		if ctx.Err() != nil {
			break
		}
	}
	if state.Status != JobStepOK {
		state.Status = JobStepFailed
		state.Error = lastErr
		end := time.Now()
		state.EndedAt = &end
	}
	return state
}

func (m *Manager) jobFreeze(run *jobRun, id, note string) {
	jobMu.Lock()
	defer jobMu.Unlock()
	j, err := GetJob(id)
	if err != nil || j.Status != JobRunning {
		return
	}
	j.ActiveMS += time.Since(run.activeFrom).Milliseconds()
	m.jobFreezeLocked(run, j, note)
}

func (m *Manager) jobFreezeLocked(run *jobRun, j *Job, note string) {
	StateEventSnap(StateEventJobPause, j.ID, StateActorSystem, filepath.Join(jobsDir(), j.ID+".json"))
	j.Status = JobPaused
	j.PauseNote = note
	_ = saveJobLocked(j)
	_ = AppendAudit(Audit{Device: "(job)", Command: "pause " + j.ID, Class: "job", Status: AuditFailure, Error: note})
}

func (m *Manager) jobFinish(run *jobRun, id, status, note string) {
	jobMu.Lock()
	defer jobMu.Unlock()
	j, err := GetJob(id)
	if err != nil || j.Status != JobRunning {
		return
	}
	StateEventSnap(StateEventJobFinish, id, StateActorSystem, filepath.Join(jobsDir(), id+".json"))
	j.ActiveMS += time.Since(run.activeFrom).Milliseconds()
	j.Status = status
	j.PauseNote = note
	now := time.Now()
	j.EndedAt = &now
	_ = saveJobLocked(j)
	_ = AppendAudit(Audit{Device: "(job)", Command: "end " + id + " " + status, Class: "job", Status: AuditOK})
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
