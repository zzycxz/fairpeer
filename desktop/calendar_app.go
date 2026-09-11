package main

// calendar_app.go exposes calendar CRUD to the frontend via Wails bindings.
// Pattern mirrors scheduler_app.go: JSON-friendly View structs, EventsEmit
// for live refresh, and direct Store calls for mutations.

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/zzycxz/fairpeer/internal/apihelper"
	"github.com/zzycxz/fairpeer/internal/calendar"
	schedulerpkg "github.com/zzycxz/fairpeer/internal/scheduler"
	"github.com/zzycxz/fairpeer/internal/tool/builtin"
)

// CalendarEventView is the JSON-friendly projection for the UI.
type CalendarEventView struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Location      string   `json:"location"`
	Start         string   `json:"start"` // "2006-01-02T15:04"
	End           string   `json:"end"`   // "2006-01-02T15:04"
	AllDay        bool     `json:"allDay"`
	Timezone      string   `json:"timezone"`
	Color         string   `json:"color"`
	Status        string   `json:"status"`
	Source        string   `json:"source"`
	Recurrence    string   `json:"recurrence"`
	RecurrenceEnd string   `json:"recurrenceEnd"`
	Reminders     []int    `json:"reminders"`
	TaskID        string   `json:"taskId"`
	Tags          []string `json:"tags"`
	// Profile is the owning partition (dev/cowork/netdev; "" = legacy cowork).
	// The UI may assign any partition; agent tools are partition-bound.
	Profile string `json:"profile"`
	// Output routing for reminders (mirrors Event). Empty outputMode = toast only.
	OutputMode    string `json:"outputMode"`
	OutputDest    string `json:"outputDest"`
	OutputAccount string `json:"outputAccount"`
	CreatedAt     string `json:"createdAt"`
}

// CalendarEventInput is the create/update payload from the UI.
type CalendarEventInput struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Location      string   `json:"location"`
	Start         string   `json:"start"`
	End           string   `json:"end"`
	AllDay        bool     `json:"allDay"`
	Timezone      string   `json:"timezone"`
	Color         string   `json:"color"`
	Recurrence    string   `json:"recurrence"`
	RecurrenceEnd string   `json:"recurrenceEnd"`
	Reminders     []int    `json:"reminders"`
	Tags          []string `json:"tags"`
	Profile       string   `json:"profile"` // dev/cowork/netdev; "" = cowork default
	// ActionPrompt (P4 事件动作编译): when non-empty, the event is compiled
	// with a LINKED one-shot scheduled task that runs this prompt at the
	// event's start time (task.ID stored in Event.TaskID). Human-UI-only by
	// construction — the agent calendar tool parses a field whitelist with no
	// action/task_id entry, and ICS imports carry no TaskID, so imported or
	// agent-sourced events can never attach an execution. Empty on update =
	// leave the linked task unchanged.
	ActionPrompt  string `json:"actionPrompt"`
	OutputMode    string `json:"outputMode"`
	OutputDest    string `json:"outputDest"`
	OutputAccount string `json:"outputAccount"`
}

const calTimeFmt = "2006-01-02T15:04"

// initCalendar opens the calendar database, starts the reminder engine,
// and injects the store into the tool layer.
func (a *App) initCalendar() {
	dbPath := filepath.Join(desktopConfigDir(), "calendar.db")
	store, err := calendar.Open(dbPath)
	if err != nil {
		slog.Warn("calendar: open failed", "err", err)
		return
	}
	a.calendarStore = store
	builtin.SetCalendarStore(store)

	// Start the reminder engine (checks every 60s). The desktop toast is the
	// default channel; IM/email bridges let events opt into pushing beyond the
	// desktop (OutputMode on the Event). Both are read at fire time so a bot
	// started after calendar init still works.
	re := calendar.NewReminderEngine(store, &calendarNotifier{app: a})
	re.SetIMPusher(calendarIMPusher{app: a})
	re.SetEmailSender(calendarEmailSender{})
	re.SetLogger(func(format string, args ...any) {
		slog.Debug("calendar: "+format, args...)
	})
	re.Start()
	a.calendarRemind = re
}

// calendarNotifier delivers reminders as desktop toasts via Wails events.
// The in-app event is heard by the App-root listener (every page); the OS
// toast makes the reminder visible even when the window is minimized — same
// two-channel discipline as schedulerNotifier. Previously only the in-app
// event fired, so with the calendar panel unmounted a reminder was completely
// invisible (globalization review, defect C).
type calendarNotifier struct {
	app *App
}

func (n *calendarNotifier) NotifyReminder(title, body string) {
	if n.app.ctx != nil {
		runtime.EventsEmit(n.app.ctx, "calendar:reminder", map[string]string{
			"title": title,
			"body":  body,
		})
		// Best-effort long-duration OS toast (Windows persists to Action
		// Center); see schedulerNotifier for the platform notes.
		go notifyLongDurationToast(n.app.ctx, title, apihelper.Truncate(body, 200))
	}
}

// calendarIMPusher implements calendar.ReminderIMPusher by routing through the
// bot gateway (same Push the scheduler uses). The gateway is bound lazily, so
// we read it at push time; nil gateway = silent skip (the toast already fired).
type calendarIMPusher struct{ app *App }

func (p calendarIMPusher) Push(ctx context.Context, dest, text string) error {
	gw := p.app.botGW.Load()
	if gw == nil {
		return nil // bot not running — toast already covered it
	}
	return gw.Push(ctx, dest, text)
}

// calendarEmailSender implements calendar.ReminderEmailSender via the shared
// builtin.SendPlainTextAs (same multi-account SMTP as the scheduler/tools).
type calendarEmailSender struct{}

func (calendarEmailSender) Send(ctx context.Context, account, to, subject, body string) error {
	return builtin.SendPlainTextAs(ctx, account, to, subject, body)
}

// calendarChanged emits a Wails event so the frontend refreshes.
func (a *App) calendarChanged() {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "calendar:changed")
	}
}

// ListCalendarEvents returns events in a time range for the UI.
func (a *App) ListCalendarEvents(since, before string) []CalendarEventView {
	if a.calendarStore == nil {
		return []CalendarEventView{}
	}
	var s, b time.Time
	var err error
	if since == "" {
		now := time.Now()
		s = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	} else {
		s, err = time.ParseInLocation(calTimeFmt, since, time.Local)
		if err != nil {
			s, _ = time.ParseInLocation("2006-01-02", since, time.Local)
		}
	}
	if before == "" {
		b = s.AddDate(0, 1, 0)
	} else {
		b, err = time.ParseInLocation(calTimeFmt, before, time.Local)
		if err != nil {
			b, _ = time.ParseInLocation("2006-01-02", before, time.Local)
		}
	}
	events, err := a.calendarStore.List(s, b)
	if err != nil {
		return []CalendarEventView{}
	}
	out := make([]CalendarEventView, 0, len(events))
	for _, e := range events {
		out = append(out, eventToView(e))
	}
	return out
}

// compileEventAction creates or resyncs the LINKED one-shot task behind an
// event's "到点动作" (P4: events compile to scheduler tasks — the calendar
// never gains its own executor). The task fires at the event's start, runs
// under the event's profile partition, delivers a local notification, and is
// reachable only from the human UI: the agent calendar tool's field whitelist
// has no action/task_id entry, and ICS imports carry no TaskID.
func (a *App) compileEventAction(e *calendar.Event, actionPrompt string) error {
	if a.scheduler == nil {
		return fmt.Errorf("scheduler is offline — cannot attach an action to the event")
	}
	if e.Recurrence != "" {
		return fmt.Errorf("循环事件暂不支持到点动作，请改用任务列表新建定时任务")
	}
	if actionPrompt == "" {
		return nil
	}
	expr := "at " + e.StartTime.Format("2006-01-02 15:04")
	profile := e.Profile
	if profile == "" {
		profile = "cowork"
	}
	if e.TaskID != "" {
		// Resync an existing link (time/title/prompt may all have moved).
		if _, err := a.scheduler.Update(e.TaskID, func(t *schedulerpkg.ScheduledTask) {
			t.Name = e.Title
			t.Expression = expr
			t.Prompt = actionPrompt
			t.Profile = profile
			t.Source = "manual"
		}); err != nil {
			return err
		}
		a.emitSchedulerChanged()
		return nil
	}
	task, err := a.scheduler.Create(schedulerpkg.ScheduledTask{
		Name:       e.Title,
		Expression: expr,
		Prompt:     actionPrompt,
		Profile:    profile,
		Source:     "manual",
		OutputMode: "notify",
	})
	if err != nil {
		return err
	}
	e.TaskID = task.ID
	a.emitSchedulerChanged()
	return nil
}

// CreateCalendarEvent creates a new event from UI input.
func (a *App) CreateCalendarEvent(in CalendarEventInput) (CalendarEventView, error) {
	if a.calendarStore == nil {
		return CalendarEventView{}, fmt.Errorf("calendar store not initialized")
	}
	start, err := time.ParseInLocation(calTimeFmt, in.Start, time.Local)
	if err != nil {
		start, err = time.ParseInLocation("2006-01-02", in.Start, time.Local)
		if err != nil {
			return CalendarEventView{}, fmt.Errorf("invalid start time: %w", err)
		}
	}
	var end time.Time
	if in.End != "" {
		end, err = time.ParseInLocation(calTimeFmt, in.End, time.Local)
		if err != nil {
			end, _ = time.ParseInLocation("2006-01-02", in.End, time.Local)
		}
	} else if in.AllDay {
		end = start.AddDate(0, 0, 1)
	} else {
		end = start.Add(time.Hour)
	}

	tz := in.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}

	e := &calendar.Event{
		Title:         in.Title,
		Description:   in.Description,
		Location:      in.Location,
		StartTime:     start,
		EndTime:       end,
		AllDay:        in.AllDay,
		Timezone:      tz,
		Color:         in.Color,
		Source:        "manual",
		Profile:       in.Profile, // "" → store defaults to cowork
		Recurrence:    in.Recurrence,
		Reminders:     in.Reminders,
		Tags:          in.Tags,
		OutputMode:    in.OutputMode,
		OutputDest:    in.OutputDest,
		OutputAccount: in.OutputAccount,
	}
	if in.RecurrenceEnd != "" {
		reEnd, err := time.ParseInLocation("2006-01-02", in.RecurrenceEnd, time.Local)
		if err == nil {
			e.RecurrenceEnd = reEnd
		}
	} else if in.Recurrence != "" {
		e.RecurrenceEnd = start.AddDate(1, 0, 0)
	}

	// P4: a non-empty action compiles into a linked one-shot task BEFORE the
	// event is stored, so TaskID lands in the INSERT.
	if in.ActionPrompt != "" {
		if err := a.compileEventAction(e, in.ActionPrompt); err != nil {
			return CalendarEventView{}, err
		}
	}

	if err := a.calendarStore.Create(e); err != nil {
		return CalendarEventView{}, err
	}
	a.calendarChanged()
	return eventToView(*e), nil
}

// UpdateCalendarEvent updates an existing event.
func (a *App) UpdateCalendarEvent(in CalendarEventInput) (CalendarEventView, error) {
	if a.calendarStore == nil {
		return CalendarEventView{}, fmt.Errorf("calendar store not initialized")
	}
	if in.ID == "" {
		return CalendarEventView{}, fmt.Errorf("id is required")
	}
	e, err := a.calendarStore.Get(in.ID)
	if err != nil {
		return CalendarEventView{}, fmt.Errorf("event not found: %w", err)
	}
	if in.Title != "" {
		e.Title = in.Title
	}
	if in.Description != "" {
		e.Description = in.Description
	}
	if in.Location != "" {
		e.Location = in.Location
	}
	if in.Start != "" {
		t, err := time.ParseInLocation(calTimeFmt, in.Start, time.Local)
		if err == nil {
			e.StartTime = t
		}
	}
	if in.End != "" {
		t, err := time.ParseInLocation(calTimeFmt, in.End, time.Local)
		if err == nil {
			e.EndTime = t
		}
	}
	if in.Color != "" {
		e.Color = in.Color
	}
	if in.Recurrence != "" {
		e.Recurrence = in.Recurrence
	}
	if len(in.Reminders) > 0 {
		e.Reminders = in.Reminders
	}
	if len(in.Tags) > 0 {
		e.Tags = in.Tags
	}
	if in.Profile != "" {
		e.Profile = in.Profile // human may re-assign the partition
	}
	// Output routing (human UI only): "none" clears push routing back to
	// toast-only; otherwise non-empty overwrites. (Empty = leave unchanged.)
	if strings.EqualFold(strings.TrimSpace(in.OutputMode), "none") {
		e.OutputMode = ""
		e.OutputDest = ""
		e.OutputAccount = ""
	} else if in.OutputMode != "" {
		e.OutputMode = in.OutputMode
	}
	if in.OutputDest != "" {
		e.OutputDest = in.OutputDest
	}
	if in.OutputAccount != "" {
		e.OutputAccount = in.OutputAccount
	}
	// P4: resync the linked action task — the event's (possibly moved) start
	// time is the task's fire time. Empty ActionPrompt leaves the link alone.
	if in.ActionPrompt != "" {
		if err := a.compileEventAction(e, in.ActionPrompt); err != nil {
			return CalendarEventView{}, err
		}
	}
	if err := a.calendarStore.Update(e); err != nil {
		return CalendarEventView{}, err
	}
	a.calendarChanged()
	return eventToView(*e), nil
}

// DeleteCalendarEvent deletes an event by id, cascading to its linked action
// task — the compiled task has no meaning without its event.
func (a *App) DeleteCalendarEvent(id string) error {
	if a.calendarStore == nil {
		return fmt.Errorf("calendar store not initialized")
	}
	taskID := ""
	if e, err := a.calendarStore.Get(id); err == nil {
		taskID = e.TaskID
	}
	if err := a.calendarStore.Delete(id); err != nil {
		return err
	}
	if taskID != "" && a.scheduler != nil {
		a.scheduler.Delete(taskID)
		a.emitSchedulerChanged()
	}
	a.calendarChanged()
	return nil
}

// SearchCalendarEvents searches events by keyword.
func (a *App) SearchCalendarEvents(q string, limit int) []CalendarEventView {
	if a.calendarStore == nil {
		return []CalendarEventView{}
	}
	if limit <= 0 {
		limit = 50
	}
	events, err := a.calendarStore.Search(q, limit)
	if err != nil {
		return []CalendarEventView{}
	}
	out := make([]CalendarEventView, 0, len(events))
	for _, e := range events {
		out = append(out, eventToView(e))
	}
	return out
}

func eventToView(e calendar.Event) CalendarEventView {
	v := CalendarEventView{
		ID:            e.ID,
		Title:         e.Title,
		Description:   e.Description,
		Location:      e.Location,
		Start:         e.StartTime.In(time.Local).Format(calTimeFmt),
		End:           e.EndTime.In(time.Local).Format(calTimeFmt),
		AllDay:        e.AllDay,
		Timezone:      e.Timezone,
		Color:         e.Color,
		Status:        e.Status,
		Source:        e.Source,
		Recurrence:    e.Recurrence,
		Reminders:     e.Reminders,
		TaskID:        e.TaskID,
		Tags:          e.Tags,
		Profile:       e.Profile,
		OutputMode:    e.OutputMode,
		OutputDest:    e.OutputDest,
		OutputAccount: e.OutputAccount,
		CreatedAt:     e.CreatedAt.Format("2006-01-02 15:04"),
	}
	if !e.RecurrenceEnd.IsZero() {
		v.RecurrenceEnd = e.RecurrenceEnd.Format("2006-01-02")
	}
	return v
}

// ExportCalendarEvents exports all events to an .ics file.
func (a *App) ExportCalendarEvents(path string) (string, error) {
	if a.calendarStore == nil {
		return "", fmt.Errorf("calendar store not initialized")
	}
	events, err := a.calendarStore.ListAll()
	if err != nil {
		return "", err
	}
	if err := calendar.ExportICS(path, events); err != nil {
		return "", err
	}
	a.calendarChanged()
	return fmt.Sprintf("exported %d events to %s", len(events), path), nil
}

// ImportCalendarEvents imports events from an .ics file.
func (a *App) ImportCalendarEvents(path string) (string, error) {
	if a.calendarStore == nil {
		return "", fmt.Errorf("calendar store not initialized")
	}
	res, err := calendar.ImportICS(path)
	if err != nil {
		return "", err
	}
	imported := 0
	for _, e := range res.Events {
		if err := a.calendarStore.Create(&e); err != nil {
			continue
		}
		imported++
	}
	a.calendarChanged()
	msg := fmt.Sprintf("imported %d events from %s", imported, path)
	// Surface lossy parsing (usually unresolvable TZIDs) so the user knows
	// events were dropped rather than silently missing.
	if res.SkippedNoStart > 0 {
		msg += fmt.Sprintf(" (%d events skipped: no parsable start time, e.g. unresolvable TZID)", res.SkippedNoStart)
	}
	return msg, nil
}

// ExportCalendarDialog opens a save-file dialog, then exports all events to the
// chosen .ics path. Returns the result message (or "" if the user cancelled).
func (a *App) ExportCalendarDialog() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("app not ready")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出日历",
		DefaultFilename: "calendar.ics",
		Filters:         []runtime.FileFilter{{DisplayName: "iCalendar (*.ics)", Pattern: "*.ics"}},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil // user cancelled
	}
	return a.ExportCalendarEvents(path)
}

// ImportCalendarDialog opens an open-file dialog, then imports events from the
// chosen .ics file. Returns the result message (or "" if cancelled).
func (a *App) ImportCalendarDialog() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("app not ready")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "导入日历",
		Filters: []runtime.FileFilter{{DisplayName: "iCalendar (*.ics)", Pattern: "*.ics"}},
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", nil // user cancelled
	}
	return a.ImportCalendarEvents(path)
}

// GetChineseHolidays returns Chinese public holidays for the given year.
func (a *App) GetChineseHolidays(year int) []CalendarEventView {
	holidays := calendar.ChineseHolidays(year)
	out := make([]CalendarEventView, 0, len(holidays))
	for _, h := range holidays {
		out = append(out, eventToView(h))
	}
	return out
}
