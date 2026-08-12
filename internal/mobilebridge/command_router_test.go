package mobilebridge

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// recordingExec records every command method called on it.
type recordingExec struct {
	mu                  sync.Mutex
	called              []string
	lastTab, lastInput  string
}

func (e *recordingExec) mark(name string) {
	e.mu.Lock()
	e.called = append(e.called, name)
	e.mu.Unlock()
}
func (e *recordingExec) reset() { e.mu.Lock(); e.called = nil; e.mu.Unlock() }
func (e *recordingExec) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.called...)
}

func (e *recordingExec) Submit(tab, input, _ string) error { e.mark("submit"); e.lastTab = tab; e.lastInput = input; return nil }
func (e *recordingExec) Cancel(string) error               { e.mark("cancel"); return nil }
func (e *recordingExec) Steer(string, string) error         { e.mark("steer"); return nil }
func (e *recordingExec) Pause(string) error                 { e.mark("pause"); return nil }
func (e *recordingExec) Resume(string) error                { e.mark("resume"); return nil }
func (e *recordingExec) Approve(string, string, bool, bool, bool) error { e.mark("approve"); return nil }
func (e *recordingExec) Answer(string, string, []string) error          { e.mark("answer"); return nil }
func (e *recordingExec) SetPlan(string, bool) error          { e.mark("setplan"); return nil }
func (e *recordingExec) SetModel(tab, m string) error        { e.mark("setmodel"); return nil }
func (e *recordingExec) ListSessions() ([]SessionInfo, error) { e.mark("list"); return nil, nil }

func TestRouteAllCommands(t *testing.T) {
	exec := &recordingExec{}
	r := NewCommandRouter("devC1", exec, PerConnPermissions{AllowHighRisk: true}, NewAudit("error"))
	cases := []struct {
		name, json string
	}{
		{"submit", `{"t":"submit","tab":"t1","input":"hi"}`},
		{"cancel", `{"t":"cancel","tab":"t1"}`},
		{"steer", `{"t":"steer","tab":"t1","text":"more"}`},
		{"pause", `{"t":"pause","tab":"t1"}`},
		{"resume", `{"t":"resume","tab":"t1"}`},
		{"approve", `{"t":"approve","tab":"t1","approvalId":"a1","allow":true}`},
		{"answer", `{"t":"answer","tab":"t1","askId":"q1","answers":["x"]}`},
		{"set_plan", `{"t":"set_plan","tab":"t1"}`},
		{"set_model", `{"t":"set_model","tab":"t1","model":"m1"}`},
		{"subscribe_tab", `{"t":"subscribe_tab","tab":"t1"}`},
		{"list_sessions", `{"t":"list_sessions"}`},
		{"switch_tab", `{"t":"switch_tab","tab":"t2"}`},
		{"ping", `{"t":"ping"}`},
	}
	for _, c := range cases {
		exec.reset()
		if err := r.Route([]byte(c.json)); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestRouteReadOnlyBlocksWrites(t *testing.T) {
	exec := &recordingExec{}
	r := NewCommandRouter("d", exec, PerConnPermissions{ReadOnly: true}, NewAudit("error"))
	writeCmds := []string{
		`{"t":"submit","tab":"t","input":"x"}`,
		`{"t":"cancel","tab":"t"}`,
		`{"t":"approve","tab":"t","approvalId":"a","allow":true}`,
		`{"t":"set_model","tab":"t","model":"m"}`,
	}
	for _, j := range writeCmds {
		if err := r.Route([]byte(j)); err != ErrForbidden {
			t.Errorf("write cmd should be forbidden under ReadOnly: %s", j)
		}
	}
	// read cmds still allowed
	for _, j := range []string{`{"t":"list_sessions"}`, `{"t":"subscribe_tab","tab":"t"}`, `{"t":"ping"}`} {
		if err := r.Route([]byte(j)); err != nil {
			t.Errorf("read cmd should pass: %s: %v", j, err)
		}
	}
}

func TestRouteHighRiskGating(t *testing.T) {
	exec := &recordingExec{}
	r := NewCommandRouter("d", exec, PerConnPermissions{AllowHighRisk: false}, NewAudit("error"))
	if err := r.Route([]byte(`{"t":"office_run","tab":"t"}`)); err != ErrForbidden {
		t.Fatalf("office_run forbidden without AllowHighRisk, got %v", err)
	}
	r2 := NewCommandRouter("d", exec, PerConnPermissions{AllowHighRisk: true}, NewAudit("error"))
	// office_run with AllowHighRisk passes the gate (router doesn't execute it; returns nil)
	if err := r2.Route([]byte(`{"t":"office_run","tab":"t"}`)); err != nil {
		t.Fatalf("office_run with AllowHighRisk: %v", err)
	}
}

func TestRouteBadJSON(t *testing.T) {
	r := NewCommandRouter("d", &recordingExec{}, PerConnPermissions{}, NewAudit("error"))
	if err := r.Route([]byte("not json")); err == nil {
		t.Fatal("bad json should error")
	}
}

func TestRouteUnknownCommand(t *testing.T) {
	r := NewCommandRouter("d", &recordingExec{}, PerConnPermissions{}, NewAudit("error"))
	// unknown command falls through to default → nil (forward-compat)
	if err := r.Route([]byte(`{"t":"some_future_cmd","tab":"t"}`)); err != nil {
		t.Fatalf("unknown cmd: %v", err)
	}
}

func TestRouteSubscribeHook(t *testing.T) {
	exec := &recordingExec{}
	r := NewCommandRouter("d", exec, PerConnPermissions{}, NewAudit("error"))
	var gotTab string
	r.SetSubscribeHook(func(tab string) { gotTab = tab })
	if err := r.Route([]byte(`{"t":"subscribe_tab","tab":"tabXYZ"}`)); err != nil {
		t.Fatal(err)
	}
	if gotTab != "tabXYZ" {
		t.Fatalf("subscribe hook got %q", gotTab)
	}
}

// silence unused import for json in case future asserts use it
var _ = json.Marshal
var _ = errors.New
