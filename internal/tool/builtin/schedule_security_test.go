package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/calendar"
	"github.com/zzycxz/fairpeer/internal/scheduler"
)

// schedule_security_test.go pins the scheduler-globalization hardening
// invariants (see the review that preceded it):
//
//  1. Field whitelist — smuggled JSON keys (profile, confirm_high_frequency,
//     source, output_dest) never reach the created task.
//  2. Identity pinning — a tool bound to profile X creates tasks in X.
//  3. Delivery downgrade — agent-requested email/IM/file routing clamps to
//     the local-only set.
//  4. Partition scoping — list/delete cross-partition report absence.
//  5. schedule_remind — plain, notify-only, approval-free path.

func bindTestScheduler(t *testing.T) *scheduler.Scheduler {
	t.Helper()
	s := scheduler.New(t.TempDir() + "/sched.json")
	SetScheduler(s)
	t.Cleanup(func() { SetScheduler(nil) })
	return s
}

// execTool runs a tool with a raw JSON string payload (the package's shared
// mustExec takes map-shaped args; several smuggling tests here need verbatim
// JSON with hostile extra keys).
func execTool(t *testing.T, tool interface {
	Execute(context.Context, json.RawMessage) (string, error)
}, args string) string {
	t.Helper()
	out, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Execute(%s): %v", args, err)
	}
	return out
}

// TestScheduleCreateRejectsSmuggledFields: the args JSON carries hostile
// extra keys — the OLD code unmarshalled straight into ScheduledTask, so
// "profile":"dev" would escalate a sealed netdev agent's task into a
// bash-capable dev run, and "confirm_high_frequency":true would defuse the
// >4/day runaway gate. The whitelist parse must leave all of them inert.
func TestScheduleCreateRejectsSmuggledFields(t *testing.T) {
	s := bindTestScheduler(t)
	tool := scheduleCreate{profile: "netdev"}

	execTool(t, tool, `{
		"name": "smuggle",
		"expression": "daily 09:00",
		"prompt": "check",
		"profile": "dev",
		"confirm_high_frequency": true,
		"source": "manual",
		"id": "evil",
		"output_dest": "attacker@example.com"
	}`)

	tasks := s.List(false)
	if len(tasks) != 1 {
		t.Fatalf("want 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Profile != "netdev" {
		t.Errorf("profile smuggle: task.Profile = %q, want pinned %q", got.Profile, "netdev")
	}
	if got.Source != "agent" {
		t.Errorf("source smuggle: task.Source = %q, want %q", got.Source, "agent")
	}
	if got.ConfirmHighFrequency {
		t.Error("confirm_high_frequency smuggle: gate bypass flag survived the parse")
	}
	if got.ID == "evil" {
		t.Error("id smuggle: caller-supplied id survived (server must assign)")
	}
	if got.OutputDest != "" {
		t.Errorf("output_dest smuggle: %q survived the whitelist", got.OutputDest)
	}
}

// TestScheduleCreateDowngradesOutwardDelivery: an agent asking for email
// routing gets a local notification instead — the delivery bridge fires
// OUTSIDE the tool permission gate, so outward routing is user-only.
func TestScheduleCreateDowngradesOutwardDelivery(t *testing.T) {
	s := bindTestScheduler(t)
	tool := scheduleCreate{profile: "cowork"}

	out := execTool(t, tool, `{
		"name": "digest",
		"expression": "daily 09:00",
		"prompt": "compile",
		"output_mode": "email",
		"output_dest": "attacker@example.com"
	}`)

	got := s.List(false)[0]
	if got.OutputMode != "notify" {
		t.Errorf("OutputMode = %q, want downgraded %q", got.OutputMode, "notify")
	}
	if !strings.Contains(out, "local notification") {
		t.Errorf("tool output should explain the downgrade so the model can relay it:\n%s", out)
	}

	// "" (store-only) and "notify" pass through unchanged.
	execTool(t, tool, `{"name":"a","expression":"daily 08:00","prompt":"x"}`)
	execTool(t, tool, `{"name":"b","expression":"daily 10:00","prompt":"x","output_mode":"notify"}`)
	tasks := s.List(false)
	if tasks[1].OutputMode != "" || tasks[2].OutputMode != "notify" {
		t.Errorf("local modes should pass through: got %q / %q", tasks[1].OutputMode, tasks[2].OutputMode)
	}
}

// TestScheduleRemindCreatesPlainLocalTask: the approval-free reminder path
// produces a Plain, notify-only, partition-pinned task.
func TestScheduleRemindCreatesPlainLocalTask(t *testing.T) {
	s := bindTestScheduler(t)
	tool := scheduleRemind{profile: "dev"}

	execTool(t, tool, `{"name":"打卡","expression":"daily 18:00","text":"下班打卡"}`)

	got := s.List(false)[0]
	if !got.Plain {
		t.Error("remind task must be Plain (no agent runs at fire time)")
	}
	if got.OutputMode != "notify" {
		t.Errorf("remind OutputMode = %q, want notify (local only)", got.OutputMode)
	}
	if got.Profile != "dev" {
		t.Errorf("remind Profile = %q, want pinned %q", got.Profile, "dev")
	}
	if got.Source != "agent" {
		t.Errorf("remind Source = %q, want agent (audit trail)", got.Source)
	}
	if got.Prompt != "下班打卡" {
		t.Errorf("remind Prompt = %q, want the reminder text verbatim", got.Prompt)
	}
}

// TestScheduleToolsPartitionScoped: a netdev-bound tool cannot see, delete,
// or run a cowork task — cross-partition ids report "not found", never a
// permission hint that would confirm the id exists elsewhere.
func TestScheduleToolsPartitionScoped(t *testing.T) {
	s := bindTestScheduler(t)
	coworkTask, err := s.Create(scheduler.ScheduledTask{
		Name: "cow", Expression: "daily 09:00", Prompt: "x",
		Profile: "cowork", Source: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	netdevTask, err := s.Create(scheduler.ScheduledTask{
		Name: "ndv", Expression: "daily 10:00", Prompt: "y",
		Profile: "netdev", Source: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}

	// list: only the netdev partition is visible.
	listOut := execTool(t, scheduleList{profile: "netdev"}, `{}`)
	if strings.Contains(listOut, coworkTask.ID) || !strings.Contains(listOut, netdevTask.ID) {
		t.Errorf("netdev schedule_list leaked partition state:\n%s", listOut)
	}

	// delete: cowork id is "not found" from the netdev binding.
	del := scheduleDelete{profile: "netdev"}
	if _, err := del.Execute(context.Background(), json.RawMessage(`{"id":"`+coworkTask.ID+`"}`)); err == nil {
		t.Fatal("netdev delete of a cowork task must fail")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("cross-partition delete error should say not-found, got: %v", err)
	}
	if len(s.List(false)) != 2 {
		t.Fatal("cross-partition delete must not remove the task")
	}

	// update: same semantics.
	upd := scheduleUpdate{profile: "netdev"}
	if _, err := upd.Execute(context.Background(), json.RawMessage(`{"id":"`+coworkTask.ID+`","name":"hijack"}`)); err == nil {
		t.Fatal("netdev update of a cowork task must fail")
	}
	if got, _ := s.Get(coworkTask.ID); got.Name != "cow" {
		t.Errorf("cross-partition update mutated the task: %q", got.Name)
	}

	// run_now: same semantics.
	rn := scheduleRunNow{profile: "netdev"}
	if _, err := rn.Execute(context.Background(), json.RawMessage(`{"id":"`+coworkTask.ID+`"}`)); err == nil {
		t.Fatal("netdev run_now of a cowork task must fail")
	}
}

// TestScheduleHistoryPartitionScoped: run records filter by the partition the
// record carries, so a dev agent's history view never shows cowork results
// (which can carry email digests and other cross-partition content).
func TestScheduleHistoryPartitionScoped(t *testing.T) {
	s := bindTestScheduler(t)
	cow, err := s.Create(scheduler.ScheduledTask{Name: "cowtask-secret", Expression: "daily 09:00", Prompt: "x", Profile: "cowork"})
	if err != nil {
		t.Fatal(err)
	}
	dev, err := s.Create(scheduler.ScheduledTask{Name: "devtask-own", Expression: "daily 09:30", Prompt: "x", Profile: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunNowForProfile(cow.ID, "cowork"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunNowForProfile(dev.ID, "dev"); err != nil {
		t.Fatal(err)
	}

	hist := scheduleHistory{profile: "dev"}
	out := execTool(t, hist, `{}`)
	if strings.Contains(out, cow.Name) {
		t.Errorf("dev history leaked a cowork run:\n%s", out)
	}
	if !strings.Contains(out, dev.Name) {
		t.Errorf("dev history missing its own run:\n%s", out)
	}
}

// TestCalendarToolStripsRoutingAndPinsProfile mirrors the schedule hardening
// for the calendar tool: agent-created events carry no outward reminder
// routing, land in the bound partition, and cross-partition ids are absent.
func TestCalendarToolStripsRoutingAndPinsProfile(t *testing.T) {
	store := openTestCalendarStore(t)
	tool := calendarTool{profile: "netdev"}

	out := execTool(t, tool, `{
		"action": "create",
		"title": "巡检窗口",
		"start": "2099-06-24T02:00",
		"reminders": [30],
		"output_mode": "im",
		"output_dest": "feishu:oc_attacker"
	}`)
	if strings.Contains(out, "feishu") {
		t.Errorf("create echoed outward routing it must have stripped:\n%s", out)
	}

	events := listAllEvents(t, store)
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	e := events[0]
	if e.OutputMode != "" || e.OutputDest != "" {
		t.Errorf("routing stripped on create: got mode=%q dest=%q", e.OutputMode, e.OutputDest)
	}
	if e.Profile != "netdev" {
		t.Errorf("event.Profile = %q, want pinned %q", e.Profile, "netdev")
	}

	// Cross-partition update → not found (no existence oracle).
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"update","id":"`+e.ID+`","title":"hijack"}`)); err != nil {
		t.Fatal("same-partition update should succeed")
	}
	coworkTool := calendarTool{profile: "cowork"}
	if _, err := coworkTool.Execute(context.Background(), json.RawMessage(`{"action":"delete","id":"`+e.ID+`"}`)); err == nil {
		t.Fatal("cross-partition delete must fail as not-found")
	}

	// export/import are human-UI actions now.
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"export","path":"C:/evil.ics"}`)); err == nil {
		t.Fatal("export action must be rejected for agents")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"import","path":"C:/evil.ics"}`)); err == nil {
		t.Fatal("import action must be rejected for agents")
	}

	// list is partition-scoped.
	listOut := execTool(t, calendarTool{profile: "cowork"}, `{"action":"list","since":"2099-06-01","before":"2099-07-01"}`)
	if strings.Contains(listOut, "巡检窗口") {
		t.Errorf("cowork list leaked a netdev event:\n%s", listOut)
	}
}

// openTestCalendarStore binds a fresh temp calendar store to the tool layer,
// restoring the previous binding on cleanup (the tool reads a package global).
func openTestCalendarStore(t *testing.T) *calendar.Store {
	t.Helper()
	store, err := calendar.Open(t.TempDir() + "/cal.db")
	if err != nil {
		t.Fatalf("calendar open: %v", err)
	}
	SetCalendarStore(store)
	t.Cleanup(func() {
		SetCalendarStore(nil)
		_ = store.Close()
	})
	return store
}

// listAllEvents lists the whole store via a far-future window.
func listAllEvents(t *testing.T, store *calendar.Store) []calendar.Event {
	t.Helper()
	events, err := store.List(
		time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("calendar list: %v", err)
	}
	return events
}
