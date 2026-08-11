package present

import (
	"path/filepath"
	"testing"

	"github.com/zzycxz/fairpeer/internal/event"
)

func TestFromEventProjectsFields(t *testing.T) {
	cases := []struct {
		name string
		e    event.Event
		want Kind
	}{
		{"turn_started", event.Event{Kind: event.TurnStarted}, KindTurnStarted},
		{"reasoning", event.Event{Kind: event.Reasoning, Text: "thinking..."}, KindReasoning},
		{"notice warn", event.Event{Kind: event.Notice, Text: "careful", Level: event.LevelWarn}, KindNotice},
		{"tool dispatch", event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "c1", Name: "bash", ReadOnly: false}}, KindToolDispatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, ok := FromEvent(c.e)
			if !ok {
				t.Fatalf("FromEvent returned ok=false for %s", c.name)
			}
			if rec.Kind != c.want {
				t.Errorf("kind = %q, want %q", rec.Kind, c.want)
			}
		})
	}
}

func TestFromEventSkipsNonPresentable(t *testing.T) {
	// ApprovalRequest / AskRequest / TurnDone have no replay value.
	for _, k := range []event.Kind{event.ApprovalRequest, event.AskRequest, event.TurnDone} {
		if _, ok := FromEvent(event.Event{Kind: k}); ok {
			t.Errorf("kind %d should be skipped (not presentable)", k)
		}
	}
}

func TestFromEventToolCarriesRichFields(t *testing.T) {
	// The whole point of the sidecar is that these fields — lost in
	// provider.Message — survive in the record.
	e := event.Event{Kind: event.ToolResult, Tool: event.Tool{
		ID: "c1", Name: "bash", Output: "done", ReadOnly: false,
		DurationMs: 1234, Truncated: true, ParentID: "parent1",
		Attachments: []event.Attachment{{Path: "x.png", Kind: "image"}},
		Profile:     &event.Profile{Model: "glm-5", Effort: "high"},
	}}
	rec, ok := FromEvent(e)
	if !ok {
		t.Fatal("FromEvent returned ok=false")
	}
	if rec.Tool == nil {
		t.Fatal("Tool is nil")
	}
	if rec.Tool.DurationMs != 1234 {
		t.Errorf("DurationMs = %d, want 1234", rec.Tool.DurationMs)
	}
	if !rec.Tool.Truncated {
		t.Error("Truncated lost")
	}
	if rec.Tool.ParentID != "parent1" {
		t.Errorf("ParentID = %q, want parent1", rec.Tool.ParentID)
	}
	if len(rec.Tool.Attachments) != 1 || rec.Tool.Attachments[0].Path != "x.png" {
		t.Errorf("Attachments lost: %+v", rec.Tool.Attachments)
	}
	if rec.Tool.Profile == nil || rec.Tool.Profile.Model != "glm-5" {
		t.Errorf("Profile lost: %+v", rec.Tool.Profile)
	}
}

func TestRecorderAppendAndTurnBoundaries(t *testing.T) {
	r := NewRecorder()
	// Turn 1: started + one tool
	r.Append(event.Event{Kind: event.TurnStarted})
	r.Append(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "a"}})
	// Turn 2: started + one tool
	r.Append(event.Event{Kind: event.TurnStarted})
	r.Append(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "b"}})

	recs := r.Records()
	// 2 turn markers + 2 tool dispatches = 4 records
	if len(recs) != 4 {
		t.Fatalf("len(records) = %d, want 4", len(recs))
	}
	if len(r.turnStartIndex) != 2 {
		t.Errorf("turnStartIndex len = %d, want 2", len(r.turnStartIndex))
	}
}

func TestRecorderTruncateToTurn(t *testing.T) {
	r := NewRecorder()
	// Three turns, each with a tool.
	for turn := 0; turn < 3; turn++ {
		r.Append(event.Event{Kind: event.TurnStarted})
		r.Append(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: string(rune('a' + turn))}})
	}
	// Keep only the last turn.
	r.TruncateToTurn(1)
	recs := r.Records()
	// Should keep: turn_started + tool dispatch from the last turn only.
	if len(recs) != 2 {
		t.Fatalf("after truncate len = %d, want 2", len(recs))
	}
	if recs[0].Kind != KindTurnStarted {
		t.Errorf("first kept record = %q, want turn_started", recs[0].Kind)
	}
	if recs[1].Tool == nil || recs[1].Tool.ID != "c" {
		t.Errorf("kept tool = %+v, want id=c", recs[1].Tool)
	}
}

func TestRecorderTruncateKeepsAllWhenKeepTurnsExceedsCount(t *testing.T) {
	r := NewRecorder()
	r.Append(event.Event{Kind: event.TurnStarted})
	r.Append(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "a"}})
	r.TruncateToTurn(5) // more turns than exist
	if len(r.Records()) != 2 {
		t.Errorf("truncate over-truncated: len = %d, want 2", len(r.Records()))
	}
}

func TestRecorderNilIsNoOp(t *testing.T) {
	// A nil recorder must never panic — callers invoke Append/Save without nil checks.
	var r *Recorder
	r.Append(event.Event{Kind: event.TurnStarted})
	r.SetRewriteVersion(3)
	r.TruncateToTurn(1)
	if err := r.Save(""); err != nil {
		t.Errorf("Save(\"\") = %v, want nil", err)
	}
	if recs := r.Records(); recs != nil {
		t.Errorf("Records() = %v, want nil", recs)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.present.jsonl")
	r := NewRecorder()
	r.Append(event.Event{Kind: event.TurnStarted})
	r.Append(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "c1", Name: "bash"}})
	r.Append(event.Event{Kind: event.Notice, Text: "hello", Level: event.LevelInfo})
	r.SetRewriteVersion(42)

	if err := r.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, ver, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ver != 42 {
		t.Errorf("rewriteVersion = %d, want 42", ver)
	}
	// turn_started + tool_dispatch + notice = 3 (header excluded from count).
	if len(loaded) != 3 {
		t.Fatalf("loaded len = %d, want 3", len(loaded))
	}
	if loaded[0].Kind != KindTurnStarted {
		t.Errorf("loaded[0].Kind = %q, want turn_started", loaded[0].Kind)
	}
	if loaded[2].Kind != KindNotice || loaded[2].Text != "hello" {
		t.Errorf("loaded[2] = %+v, want notice/hello", loaded[2])
	}
}

func TestLoadMissingFileReturnsNil(t *testing.T) {
	loaded, ver, err := Load(filepath.Join(t.TempDir(), "nope.present.jsonl"))
	if err != nil {
		t.Fatalf("Load missing file should not error: %v", err)
	}
	if loaded != nil {
		t.Errorf("loaded = %v, want nil", loaded)
	}
	if ver != 0 {
		t.Errorf("ver = %d, want 0", ver)
	}
}

func TestPresentPath(t *testing.T) {
	got := PresentPath("/x/y/session.jsonl")
	want := "/x/y/session.jsonl.present.jsonl"
	if got != want {
		t.Errorf("PresentPath = %q, want %q", got, want)
	}
	if PresentPath("") != "" {
		t.Error("PresentPath(\"\") should be empty")
	}
}
