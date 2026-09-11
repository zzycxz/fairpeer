package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zzycxz/fairpeer/internal/scheduler"
	"github.com/zzycxz/fairpeer/internal/tool"
)

// Scheduled-task tools. These wrap a process-global scheduler.Scheduler so the
// agent can create recurring prompts ("every weekday at 9am, compile the news
// digest"). The scheduler itself is app-level and persists across restarts;
// these tools are the create/remind/list/delete/update surface, registered per
// profile via SchedulerTools(profile).
//
// SECURITY INVARIANTS (see the calendar/scheduler globalization review):
//   - Identity: every tool instance is bound to the registering profile at
//     boot; tasks it creates carry that profile, and list/update/delete/
//     history/run-now are partitioned to it. An agent never picks a profile.
//   - Field whitelist: create parses an explicit arg struct — smuggled JSON
//     keys (profile, confirm_high_frequency, …) cannot reach the task struct.
//   - Delivery downgrade: agent-created/updated tasks deliver locally
//     (""/notify only); email/IM/file routing is a user-only UI action.
//
// The scheduler instance is injected via SetScheduler (desktop startup). When
// nil (CLI/TUI without the desktop backend), the tools return a clear error.

var globalScheduler *scheduler.Scheduler

// SetScheduler injects the app-level scheduler the tools drive. Called once at
// desktop startup; passing nil disables the tools (they return "scheduler
// offline").
func SetScheduler(s *scheduler.Scheduler) { globalScheduler = s }

func requireScheduler() (*scheduler.Scheduler, error) {
	if globalScheduler == nil {
		return nil, errors.New("scheduler is offline (no scheduler bound to this process)")
	}
	return globalScheduler, nil
}

// SchedulerTools returns the scheduled-task tools BOUND to the calling
// profile. The binding pins every task the tools create to the caller's
// identity (a netdev agent's tasks run under netdev — never a profile it
// picked itself) and scopes list/update/delete/history/run-now to that
// profile's partition: one profile's agent cannot see or touch another
// partition's tasks. The human UI keeps the unpartitioned App methods.
func SchedulerTools(profile string) []tool.Tool {
	return []tool.Tool{
		scheduleCreate{profile: profile},
		scheduleRemind{profile: profile},
		scheduleList{profile: profile},
		scheduleDelete{profile: profile},
		scheduleUpdate{profile: profile},
		scheduleHistory{profile: profile},
		scheduleRunNow{profile: profile},
	}
}

// --- schedule_create --------------------------------------------------------

type scheduleCreate struct{ profile string }

func (scheduleCreate) Name() string { return "schedule_create" }

func (scheduleCreate) Description() string {
	return "Create a scheduled task that fires an agent prompt on a schedule, independent of any open chat tab. Expression formats: \"every 30m\", \"every 2h\", \"hourly\", \"daily 09:00\", \"daily 09:00 Mon-Fri\" (weekdays), \"daily 09:00 Mon,Wed,Fri\", \"at 2026-06-24 15:00\" (one-shot absolute time, auto-disables after firing), \"in 2h30m\" / \"in 3d\" (one-shot relative offset, normalized to at-form), or a 5-field cron (\"0 9 * * 1-5\"). " +
		"IMPORTANT for one-shot times: prefer relative words so the system resolves the correct date — \"明天下午3点\", \"下周一9点\", \"in 2h\", \"3号10点\" — instead of guessing an absolute \"at YYYY-MM-DD HH:MM\" (your year may be wrong). " +
		"The task runs under YOUR current profile and creating it requires the user's approval (an approval card shows the full task before it is saved). " +
		"The run result is stored on the task and delivered as a local notification; email/IM/file routing can only be configured by the user in the calendar panel. " +
		"For a plain reminder that just pops text without running any AI, prefer schedule_remind. Tasks persist across restarts."
}

func (scheduleCreate) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "name":{"type":"string","description":"Human-readable task name"},
  "expression":{"type":"string","description":"Schedule: \"every 30m\", \"daily 09:00\", \"daily 09:00 Mon-Fri\", \"at 2026-06-24 15:00\" (one-shot), \"in 2h\" (one-shot relative), or 5-field cron"},
  "prompt":{"type":"string","description":"The agent prompt to run on each fire"},
  "output_mode":{"type":"string","description":"Result routing: \"\" (store only) or \"notify\" (in-app + OS notification). Email/IM/file delivery is user-configurable only, in the calendar panel."},
  "output_dest":{"type":"string","description":"Unused — local notification has no destination. Kept for schema stability."}
},
"required":["name","expression","prompt"]
}`)
}

func (scheduleCreate) ReadOnly() bool { return false }

func (t scheduleCreate) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	// SECURITY: parse an explicit field whitelist, NOT the full ScheduledTask.
	// Unmarshalling raw args straight into the task struct let any extra JSON
	// key ride along — notably "profile" (cross-profile escalation: a sealed
	// netdev agent scheduling a bash-capable dev task) and
	// "confirm_high_frequency" (bypassing the >4/day runaway gate). Both are
	// server-pinned below and can never come from the model.
	var p struct {
		Name       string `json:"name"`
		Expression string `json:"expression"`
		Prompt     string `json:"prompt"`
		OutputMode string `json:"output_mode"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	s, err := requireScheduler()
	if err != nil {
		return "", err
	}
	created, err := s.Create(scheduler.ScheduledTask{
		Name:       p.Name,
		Expression: p.Expression,
		Prompt:     p.Prompt,
		Profile:    t.profile, // server-pinned caller identity
		Source:     "agent",
		// Delivery downgrade: email/IM/file routing is a persistent outward
		// channel that fires OUTSIDE the tool permission gate (the scheduler's
		// own delivery bridge), so it is reserved for tasks the user creates
		// or edits in the calendar UI. Anything outward degrades to "notify".
		OutputMode: sanitizeAgentOutputMode(p.OutputMode),
	})
	if err != nil {
		return "", enrichPastTimeErr(err)
	}
	out := formatTask(created)
	if downgradedAgentOutput(p.OutputMode) {
		out += "\nnote: outward delivery (email/IM/file) is not available for agent-created tasks — the result surfaces as a local notification. The user can add routing in the calendar panel."
	}
	return out, nil
}

// sanitizeAgentOutputMode clamps an agent-requested delivery mode to the
// local-only set ("" store-only, "notify" toast). See the call site for why.
func sanitizeAgentOutputMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" || m == "notify" {
		return m
	}
	return "notify"
}

func downgradedAgentOutput(requested string) bool {
	m := strings.ToLower(strings.TrimSpace(requested))
	return m != "" && m != "notify"
}

// enrichPastTimeErr appends the current date/time to a past-one-shot error so
// the model can self-correct a wrong-year absolute date on its retry
// (shared by create/remind/update — the trap is identical).
func enrichPastTimeErr(err error) error {
	if err == nil || !strings.Contains(err.Error(), "past") && !strings.Contains(err.Error(), "one-shot") {
		return err
	}
	now := time.Now()
	return fmt.Errorf(
		"%w\n\n当前时间是 %s。请改用相对时间词（如「明天下午3点」「下周一9点」「in 2h」）让系统自动换算成正确的未来时间，或用 at %s 这样的未来绝对时间（注意年份必须是 %d）。",
		err, now.Format("2006-01-02 15:04 (周一)"),
		now.Add(2*time.Hour).Format("2006-01-02 15:04"),
		now.Year(),
	)
}

// --- schedule_remind --------------------------------------------------------

// scheduleRemind creates a PLAIN reminder: the text pops verbatim (in-app
// toast + OS notification) at fire time — no agent runs, nothing leaves the
// machine. Because nothing executes, plain reminders skip the approval card
// that schedule_create requires; only the shared >4/day frequency gate
// applies (its confirm retry is a UI action, so agents must relay the
// request to the user instead of retrying).
type scheduleRemind struct{ profile string }

func (scheduleRemind) Name() string { return "schedule_remind" }

func (scheduleRemind) Description() string {
	return "Create a plain reminder that pops the given text verbatim at the scheduled time (in-app toast + OS notification) WITHOUT running any AI. Use for \"到点提醒我X\" style requests; use schedule_create when something must actually RUN at fire time. " +
		"Prefer relative time words (\"明天下午3点\", \"in 2h\", \"每天18:00\") so the date resolves correctly. The reminder stays local — it never emails or pushes IM."
}

func (scheduleRemind) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "name":{"type":"string","description":"Short reminder title, e.g. \"下班打卡\""},
  "expression":{"type":"string","description":"When to remind: \"in 2h\", \"at 2026-06-24 15:00\", \"daily 09:00\", \"every 30m\", or 5-field cron"},
  "text":{"type":"string","description":"The reminder text shown verbatim in the notification"}
},
"required":["name","expression","text"]
}`)
}

func (scheduleRemind) ReadOnly() bool { return false }

func (t scheduleRemind) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Name       string `json:"name"`
		Expression string `json:"expression"`
		Text       string `json:"text"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(p.Text) == "" {
		return "", errors.New("text is required (the reminder body shown in the notification)")
	}
	s, err := requireScheduler()
	if err != nil {
		return "", err
	}
	created, err := s.Create(scheduler.ScheduledTask{
		Name:       p.Name,
		Expression: p.Expression,
		Prompt:     p.Text, // plain tasks surface the prompt verbatim — it IS the reminder body
		Profile:    t.profile,
		Plain:      true,
		Source:     "agent",
		// Local notification only — see sanitizeAgentOutputMode: outward
		// routing of reminder bodies is a user-only action.
		OutputMode: "notify",
	})
	if err != nil {
		if strings.Contains(err.Error(), "confirm_high_frequency") {
			return "", fmt.Errorf("%w（纯提醒的高频确认需要用户在日历面板中操作——请转告用户）", err)
		}
		return "", enrichPastTimeErr(err)
	}
	return formatTask(created), nil
}

// --- schedule_list ----------------------------------------------------------

type scheduleList struct{ profile string }

func (scheduleList) Name() string { return "schedule_list" }

func (scheduleList) Description() string {
	return "List scheduled tasks in YOUR profile's partition (other profiles' tasks are invisible). Set enabled_only=true to exclude paused tasks. Each task shows its expression, next fire time, last run, and run count."
}

func (scheduleList) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "enabled_only":{"type":"boolean","description":"Only list enabled (active) tasks (default false)"}
},
"required":[]
}`)
}

func (scheduleList) ReadOnly() bool { return true }

func (t scheduleList) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		EnabledOnly bool `json:"enabled_only"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	s, err := requireScheduler()
	if err != nil {
		return "", err
	}
	tasks := s.ListForProfile(t.profile, p.EnabledOnly)
	if len(tasks) == 0 {
		return "no scheduled tasks", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d task(s):\n", len(tasks))
	for _, t := range tasks {
		b.WriteString(formatTask(t) + "\n")
	}
	return b.String(), nil
}

// --- schedule_delete --------------------------------------------------------

type scheduleDelete struct{ profile string }

func (scheduleDelete) Name() string { return "schedule_delete" }

func (scheduleDelete) Description() string {
	return "Delete a scheduled task by id (permanently) — only tasks in YOUR profile's partition. To pause without deleting, use schedule_update with enabled=false."
}

func (scheduleDelete) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "id":{"type":"string","description":"Task id from schedule_list"}
},
"required":["id"]
}`)
}

func (scheduleDelete) ReadOnly() bool { return false }

func (t scheduleDelete) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	s, err := requireScheduler()
	if err != nil {
		return "", err
	}
	if !s.DeleteForProfile(p.ID, t.profile) {
		return "", fmt.Errorf("task %q not found", p.ID)
	}
	return fmt.Sprintf("deleted task %q", p.ID), nil
}

// --- schedule_update --------------------------------------------------------

type scheduleUpdate struct{ profile string }

func (scheduleUpdate) Name() string { return "schedule_update" }

func (scheduleUpdate) Description() string {
	return "Update a scheduled task in YOUR profile's partition. Pass any of name/expression/prompt/enabled/output_mode/output_dest to change; omitted fields keep their current value. Set enabled=false to pause, true to resume (recomputes next fire). Changing the expression re-validates it. Email/IM/file delivery routing can only be changed by the user in the calendar panel."
}

func (scheduleUpdate) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "id":{"type":"string","description":"Task id from schedule_list"},
  "name":{"type":"string","description":"New display name (omit to keep current)"},
  "expression":{"type":"string","description":"New cron expression M H DoM Mon DoW (omit to keep current)"},
  "prompt":{"type":"string","description":"New prompt template (omit to keep current)"},
  "enabled":{"type":"boolean","description":"true to enable, false to disable"},
  "output_mode":{"type":"string","description":"im, email, notify, or file"},
  "output_dest":{"type":"string","description":"Target address/path (omit to clear)"}
},
"required":["id"]
}`)
}

func (scheduleUpdate) ReadOnly() bool { return false }

func (t scheduleUpdate) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		ID         string  `json:"id"`
		Name       *string `json:"name"`
		Expression *string `json:"expression"`
		Prompt     *string `json:"prompt"`
		Enabled    *bool   `json:"enabled"`
		OutputMode *string `json:"output_mode"`
		OutputDest *string `json:"output_dest"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	s, err := requireScheduler()
	if err != nil {
		return "", err
	}
	updated, err := s.UpdateForProfile(p.ID, t.profile, func(task *scheduler.ScheduledTask) {
		if p.Name != nil {
			task.Name = *p.Name
		}
		if p.Expression != nil {
			task.Expression = *p.Expression
		}
		if p.Prompt != nil {
			task.Prompt = *p.Prompt
		}
		if p.Enabled != nil {
			task.Enabled = *p.Enabled
		}
		// Delivery downgrade on the agent path (see scheduleCreate): outward
		// routing is user-only; the agent may keep/toggle local notify only.
		if p.OutputMode != nil {
			task.OutputMode = sanitizeAgentOutputMode(*p.OutputMode)
		}
		// p.OutputDest deliberately ignored: with modes clamped to ""/"notify"
		// there is no destination an agent may set.
	})
	if err != nil {
		return "", enrichPastTimeErr(err)
	}
	return formatTask(updated), nil
}

// formatTask renders one task for tool output.
func formatTask(t scheduler.ScheduledTask) string {
	status := "enabled"
	if !t.Enabled {
		status = "paused"
	}
	next := "—"
	if !t.NextRun.IsZero() {
		next = t.NextRun.Format("2006-01-02 15:04")
	}
	last := "never"
	if !t.LastRun.IsZero() {
		last = t.LastRun.Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("- %s [%s] %q\n  expression: %s\n  next: %s · last: %s · runs: %d\n  prompt: %s",
		t.ID, status, t.Name, t.Expression, next, last, t.RunCount, truncatePrompt(t.Prompt))
}

func truncatePrompt(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

// --- schedule_history -------------------------------------------------------

type scheduleHistory struct{ profile string }

func (scheduleHistory) Name() string { return "schedule_history" }

func (scheduleHistory) Description() string {
	return "List recent run records (newest first) for tasks in YOUR profile's partition, optionally filtered to one task by id. Each record shows the run time, status (ok/error/skipped), and a truncated result. Useful to confirm a task actually fired and see what it produced."
}

func (scheduleHistory) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "id":{"type":"string","description":"Optional task id to filter to one task's runs. Omit to list across all tasks."}
},
"required":[]
}`)
}

func (scheduleHistory) ReadOnly() bool { return true }

func (t scheduleHistory) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	s, err := requireScheduler()
	if err != nil {
		return "", err
	}
	recs := s.HistoryForProfile(t.profile, p.ID)
	if len(recs) == 0 {
		return "no run history", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d record(s):\n", len(recs))
	for _, r := range recs {
		mode := r.OutputMode
		if mode == "" {
			mode = "store"
		}
		fmt.Fprintf(&b, "- %s [%s] %s · %s\n  %s\n", r.At.Format("2006-01-02 15:04"), r.Status, mode, r.Name, truncatePrompt(r.Result))
	}
	return b.String(), nil
}

// --- schedule_run_now -------------------------------------------------------

type scheduleRunNow struct{ profile string }

func (scheduleRunNow) Name() string { return "schedule_run_now" }

func (scheduleRunNow) Description() string {
	return "Fire a scheduled task in YOUR profile's partition immediately, outside its schedule. The task's normal delivery runs as usual, and a run-history record is appended; the task's schedule is unaffected. Use to test a task or run it on demand."
}

func (scheduleRunNow) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "id":{"type":"string","description":"Task id from schedule_list"}
},
"required":["id"]
}`)
}

func (scheduleRunNow) ReadOnly() bool { return false }

func (t scheduleRunNow) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	s, err := requireScheduler()
	if err != nil {
		return "", err
	}
	result, err := s.RunNowForProfile(p.ID, t.profile)
	if err != nil {
		return "", err
	}
	return result, nil
}
