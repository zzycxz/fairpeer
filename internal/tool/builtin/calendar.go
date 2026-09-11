package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zzycxz/fairpeer/internal/calendar"
	"github.com/zzycxz/fairpeer/internal/tool"
)

// Calendar tools. The store is injected at boot via SetCalendarStore; when nil
// the tools return a clear error. Each instance is BOUND to the registering
// profile: events it creates land in that profile's partition, and reads/
// updates/deletes are scoped to it (one profile's agent never sees another
// cabinet). See SchedulerTools for the shared security invariants.

var calendarStore *calendar.Store

func SetCalendarStore(s *calendar.Store) { calendarStore = s }

func requireCalendarStore() (*calendar.Store, error) {
	if calendarStore == nil {
		return nil, errors.New("calendar is offline — restart the app to initialize")
	}
	return calendarStore, nil
}

// CalendarTools returns the calendar tool bound to the calling profile.
func CalendarTools(profile string) []tool.Tool {
	return []tool.Tool{calendarTool{profile: profile}}
}

// calendarParams is the shared request struct for all calendar actions.
// Deliberately WITHOUT output routing (output_mode/output_dest/output_account)
// and path fields: the agent never configures outward reminder routing or
// touches .ics files — both are user-only UI actions (see the create/update
// handlers). Extra JSON keys the model smuggles in are simply never read.
type calendarParams struct {
	Action        string   `json:"action"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Location      string   `json:"location"`
	Start         string   `json:"start"`
	End           string   `json:"end"`
	AllDay        bool     `json:"all_day"`
	Timezone      string   `json:"timezone"`
	Color         string   `json:"color"`
	Recurrence    string   `json:"recurrence"`
	RecurrenceEnd string   `json:"recurrence_end"`
	Reminders     []int    `json:"reminders"`
	Tags          []string `json:"tags"`
	Since         string   `json:"since"`
	Before        string   `json:"before"`
	Q             string   `json:"q"`
	Limit         int      `json:"limit"`
}

// calendarTool is the unified calendar entry point.
type calendarTool struct{ profile string }

func (calendarTool) Name() string { return "calendar" }

func (calendarTool) Description() string {
	return "Manage calendar events in YOUR profile's partition (other profiles' events are invisible). Actions: create (title/start/end required), list (since/before for time range), update (id + fields to change), delete (id required), search (q keyword), freebusy (since/before), holidays. Reminders fire as local desktop notifications — email/IM routing and .ics import/export are user-only actions in the calendar panel."
}

func (calendarTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "action":{"type":"string","enum":["create","list","update","delete","search","freebusy","holidays"],"description":"Action to perform"},
  "id":{"type":"string","description":"Event ID (for update/delete)"},
  "title":{"type":"string","description":"Event title (for create/update)"},
  "description":{"type":"string","description":"Event description"},
  "location":{"type":"string","description":"Event location"},
  "start":{"type":"string","description":"Start time: '2026-07-07T10:00' or '2026-07-07' for all-day"},
  "end":{"type":"string","description":"End time (optional, defaults to start + 1h)"},
  "all_day":{"type":"boolean","description":"All-day event (default false)"},
  "timezone":{"type":"string","description":"Timezone (default Asia/Shanghai)"},
  "color":{"type":"string","description":"Hex color e.g. '#FF4444'"},
  "recurrence":{"type":"string","description":"RFC 5545 RRULE, e.g. 'FREQ=WEEKLY;BYDAY=MO'"},
  "recurrence_end":{"type":"string","description":"Recurrence end date '2026-12-31' (default +1 year)"},
  "reminders":{"type":"array","items":{"type":"integer"},"description":"Reminder minutes before event, e.g. [15, 5] — fires as a local desktop notification"},
  "tags":{"type":"array","items":{"type":"string"},"description":"Tags for categorization"},
  "since":{"type":"string","description":"List/search start boundary: '2026-07-07' or 'today' or 'this_week'"},
  "before":{"type":"string","description":"List/search end boundary"},
  "q":{"type":"string","description":"Search keyword (for search action)"},
  "limit":{"type":"integer","description":"Max results for search (default 50)"}
},
"required":["action"]
}`)
}

func (calendarTool) ReadOnly() bool { return false }

func (t calendarTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p calendarParams
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}

	switch p.Action {
	case "create":
		return t.calendarCreate(p)
	case "list":
		return t.calendarList(p)
	case "update":
		return t.calendarUpdate(p)
	case "delete":
		return t.calendarDelete(p)
	case "search":
		return t.calendarSearch(p)
	case "freebusy":
		return t.calendarFreebusy(p)
	case "holidays":
		return calendarHolidays(p)
	case "export", "import":
		// .ics export/import moved to the human UI (Export/ImportCalendarDialog):
		// the old tool actions took an arbitrary filesystem path — an
		// any-path write primitive (ExportICS does os.WriteFile with no
		// confinement). The dialog is the only supported path now.
		return "", errors.New("ics export/import is a user action — ask the user to use the calendar panel's import/export buttons")
	default:
		return "", fmt.Errorf("unknown action %q", p.Action)
	}
}

func (t calendarTool) calendarCreate(p calendarParams) (string, error) {
	if p.Title == "" {
		return "", errors.New("title is required")
	}
	if p.Start == "" {
		return "", errors.New("start time is required")
	}

	start, err := parseTime(p.Start)
	if err != nil {
		return "", fmt.Errorf("invalid start time: %w", err)
	}

	var end time.Time
	if p.End != "" {
		end, err = parseTime(p.End)
		if err != nil {
			return "", fmt.Errorf("invalid end time: %w", err)
		}
	} else if p.AllDay {
		end = start.AddDate(0, 0, 1)
	} else {
		end = start.Add(time.Hour)
	}

	tz := p.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}

	e := &calendar.Event{
		Title:       p.Title,
		Description: p.Description,
		Location:    p.Location,
		StartTime:   start,
		EndTime:     end,
		AllDay:      p.AllDay,
		Timezone:    tz,
		Color:       p.Color,
		Source:      "agent",
		Profile:     t.profile, // partition pinned to the calling agent
		Recurrence:  p.Recurrence,
		Reminders:   p.Reminders,
		Tags:        p.Tags,
		// Output routing deliberately NOT copied: an event carrying email/IM
		// routing fires those channels from the reminder engine — OUTSIDE the
		// tool permission gate — on a schedule the agent controls. Local
		// reminders only; the user adds routing in the calendar panel.
	}

	if p.RecurrenceEnd != "" {
		reEnd, err := parseTime(p.RecurrenceEnd)
		if err == nil {
			e.RecurrenceEnd = reEnd
		}
	} else if p.Recurrence != "" {
		e.RecurrenceEnd = start.AddDate(1, 0, 0) // default +1 year
	}

	store, err := requireCalendarStore()
	if err != nil {
		return "", err
	}
	if err := store.Create(e); err != nil {
		return "", err
	}

	msg := fmt.Sprintf("created event %q at %s", e.Title, e.StartTime.Format("2006-01-02 15:04"))
	return msg, nil
}

func (t calendarTool) calendarList(p calendarParams) (string, error) {
	store, err := requireCalendarStore()
	if err != nil {
		return "", err
	}

	since, before, err := parseRange(p.Since, p.Before)
	if err != nil {
		return "", err
	}

	events, err := store.ListForProfile(t.profile, since, before)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "no events in this range", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d event(s):\n", len(events))
	for _, e := range events {
		tags := ""
		if len(e.Tags) > 0 {
			tags = " [" + strings.Join(e.Tags, ", ") + "]"
		}
		task := ""
		if e.TaskID != "" {
			task = " ⚡"
		}
		rec := ""
		if e.Recurrence != "" {
			rec = " 🔁"
		}
		if e.AllDay {
			fmt.Fprintf(&b, "- %s [全天] %s%s%s%s\n", e.StartTime.Format("01-02"), e.Title, tags, rec, task)
		} else {
			fmt.Fprintf(&b, "- %s %s~%s %s%s%s%s\n",
				e.StartTime.Format("01-02 15:04"),
				e.StartTime.Format("15:04"),
				e.EndTime.Format("15:04"),
				e.Title, tags, rec, task)
		}
	}
	return b.String(), nil
}

func (t calendarTool) calendarUpdate(p calendarParams) (string, error) {
	if p.ID == "" {
		return "", errors.New("id is required for update")
	}
	store, err := requireCalendarStore()
	if err != nil {
		return "", err
	}
	// Partition-scoped read: another profile's event reports not-found (the
	// agent must not learn it exists).
	e, err := store.GetForProfile(p.ID, t.profile)
	if err != nil {
		return "", fmt.Errorf("event %q not found: %w", p.ID, err)
	}
	if p.Title != "" {
		e.Title = p.Title
	}
	if p.Description != "" {
		e.Description = p.Description
	}
	if p.Location != "" {
		e.Location = p.Location
	}
	if p.Start != "" {
		t, err := parseTime(p.Start)
		if err != nil {
			return "", fmt.Errorf("invalid start: %w", err)
		}
		e.StartTime = t
	}
	if p.End != "" {
		t, err := parseTime(p.End)
		if err != nil {
			return "", fmt.Errorf("invalid end: %w", err)
		}
		e.EndTime = t
	}
	if p.Color != "" {
		e.Color = p.Color
	}
	if p.Recurrence != "" {
		e.Recurrence = p.Recurrence
	}
	if len(p.Reminders) > 0 {
		e.Reminders = p.Reminders
	}
	if len(p.Tags) > 0 {
		e.Tags = p.Tags
	}
	// Output routing deliberately NOT applied: the agent may neither add nor
	// change email/IM push routing (same invariant as create). A user who
	// wants routing changed does it in the calendar panel.
	if err := store.Update(e); err != nil {
		return "", err
	}
	return fmt.Sprintf("updated event %q", e.Title), nil
}

func (t calendarTool) calendarDelete(p calendarParams) (string, error) {
	if p.ID == "" {
		return "", errors.New("id is required for delete")
	}
	store, err := requireCalendarStore()
	if err != nil {
		return "", err
	}
	e, err := store.GetForProfile(p.ID, t.profile)
	if err != nil {
		return "", fmt.Errorf("event %q not found: %w", p.ID, err)
	}
	if err := store.Delete(p.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("deleted event %q", e.Title), nil
}

func (t calendarTool) calendarSearch(p calendarParams) (string, error) {
	if p.Q == "" {
		return "", errors.New("q (keyword) is required for search")
	}
	store, err := requireCalendarStore()
	if err != nil {
		return "", err
	}
	events, err := store.SearchForProfile(t.profile, p.Q, p.Limit)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "no matching events", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d result(s):\n", len(events))
	for _, e := range events {
		// Full id — update/delete match on the exact string, so a truncated
		// display form hands the model an unusable reference (TOOL-6).
		fmt.Fprintf(&b, "- [%s] %s @ %s\n", e.ID, e.Title, e.StartTime.Format("01-02 15:04"))
	}
	return b.String(), nil
}

func (t calendarTool) calendarFreebusy(p calendarParams) (string, error) {
	store, err := requireCalendarStore()
	if err != nil {
		return "", err
	}
	since, before, err := parseRange(p.Since, p.Before)
	if err != nil {
		return "", err
	}
	events, err := store.ListForProfile(t.profile, since, before)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		return "free — no events in this range", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "busy — %d event(s):\n", len(events))
	for _, e := range events {
		fmt.Fprintf(&b, "- %s~%s %s\n",
			e.StartTime.Format("15:04"),
			e.EndTime.Format("15:04"),
			e.Title)
	}
	return b.String(), nil
}

func calendarHolidays(p calendarParams) (string, error) {
	year := time.Now().Year()
	if p.Since != "" {
		if t, err := parseTime(p.Since); err == nil {
			year = t.Year()
		}
	}
	holidays := calendar.ChineseHolidays(year)
	var b strings.Builder
	fmt.Fprintf(&b, "%d holidays in %d:\n", len(holidays), year)
	for _, h := range holidays {
		fmt.Fprintf(&b, "- %s~%s %s\n",
			h.StartTime.Format("01-02"),
			h.EndTime.Format("01-02"),
			h.Title)
	}
	return b.String(), nil
}

// --- Time parsing helpers ---

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02T15:04",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q (expected YYYY-MM-DDThh:mm or YYYY-MM-DD)", s)
}

func parseRange(since, before string) (time.Time, time.Time, error) {
	now := time.Now()
	var s, b time.Time
	var err error

	if since == "" {
		s = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	} else {
		s, err = parseRelativeOrAbsolute(since, now)
		if err != nil {
			return s, b, fmt.Errorf("invalid since: %w", err)
		}
	}

	if before == "" {
		b = s.AddDate(0, 1, 0)
	} else {
		b, err = parseRelativeOrAbsolute(before, now)
		if err != nil {
			return s, b, fmt.Errorf("invalid before: %w", err)
		}
	}
	return s, b, nil
}

func parseRelativeOrAbsolute(s string, now time.Time) (time.Time, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local), nil
	case "tomorrow":
		return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.Local), nil
	case "this_week":
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return time.Date(now.Year(), now.Month(), now.Day()-weekday+1, 0, 0, 0, 0, time.Local), nil
	case "next_week":
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		monday := time.Date(now.Year(), now.Month(), now.Day()-weekday+1, 0, 0, 0, 0, time.Local)
		return monday.AddDate(0, 0, 7), nil
	case "this_month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local), nil
	}
	return parseTime(s)
}
