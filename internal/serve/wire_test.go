package serve

import (
	"errors"
	"testing"

	"github.com/zzycxz/fairpeer/internal/event"
	"github.com/zzycxz/fairpeer/internal/provider"
)

func TestToWire(t *testing.T) {
	t.Run("tool dispatch", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{Name: "bash", Args: `{"cmd":"ls"}`, ReadOnly: false}})
		if w.Kind != "tool_dispatch" || w.Tool == nil || w.Tool.Name != "bash" || w.Tool.Args != `{"cmd":"ls"}` {
			t.Errorf("dispatch = %+v / %+v", w, w.Tool)
		}
	})

	t.Run("tool dispatch profile", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{
			Name: "task", Args: `{"prompt":"x"}`,
			Profile: &event.Profile{Model: "test-provider", Effort: "max"},
		}})
		if w.Tool == nil || w.Tool.Profile == nil || w.Tool.Profile.Model != "test-provider" || w.Tool.Profile.Effort != "max" {
			t.Errorf("profile = %+v", w.Tool)
		}
	})

	t.Run("tool result duration", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.ToolResult, Tool: event.Tool{Name: "web_fetch", Output: "ok", DurationMs: 522}})
		if w.Tool == nil || w.Tool.Output != "ok" || w.Tool.DurationMs != 522 {
			t.Errorf("tool result duration = %+v", w.Tool)
		}
	})

	t.Run("usage with cost", func(t *testing.T) {
		w := toWire(event.Event{
			Kind:    event.Usage,
			Usage:   &provider.Usage{PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200, CacheHitTokens: 900, CacheMissTokens: 100},
			Pricing: &provider.Pricing{CacheHit: 0.02, Input: 1, Output: 2},
			CacheDiagnostics: &event.CacheDiagnostics{
				PrefixChanged:       true,
				PrefixChangeReasons: []string{"log_rewrite"},
				LogRewriteVersion:   1,
			},
		})
		if w.Usage == nil || w.Usage.TotalTokens != 1200 || w.Usage.Cost <= 0 || w.Usage.CostUSD <= 0 || w.Usage.Currency != "¥" {
			t.Errorf("usage = %+v", w.Usage)
		}
		if w.Usage.CacheDiagnostics == nil || w.Usage.CacheDiagnostics.PrefixChangeReasons[0] != "log_rewrite" {
			t.Errorf("cache diagnostics = %+v", w.Usage.CacheDiagnostics)
		}
	})

	t.Run("notice warn", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.Notice, Level: event.LevelWarn, Text: "truncated"})
		if w.Kind != "notice" || w.Level != "warn" || w.Text != "truncated" {
			t.Errorf("notice = %+v", w)
		}
	})

	t.Run("approval", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.ApprovalRequest, Approval: event.Approval{ID: "3", Tool: "bash", Subject: "rm"}})
		if w.Approval == nil || w.Approval.ID != "3" || w.Approval.Tool != "bash" {
			t.Errorf("approval = %+v", w.Approval)
		}
	})

	t.Run("turn done error", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.TurnDone, Err: errors.New("boom")})
		if w.Kind != "turn_done" || w.Err != "boom" {
			t.Errorf("turn_done = %+v", w)
		}
	})

	t.Run("steer", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.Steer, Text: "mid-turn guidance"})
		if w.Kind != "steer" || w.Text != "mid-turn guidance" {
			t.Errorf("steer = %+v", w)
		}
	})
}

func TestToWireItemCollabResumedKinds(t *testing.T) {
	t.Run("resumed kind-only", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.Resumed})
		if w.Kind != "resumed" {
			t.Errorf("resumed kind = %q", w.Kind)
		}
	})
	t.Run("expert collab payload", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.ExpertCollab, Collab: event.Collab{
			RunID: "r1", TeamID: "t1", TeamName: "netops", Task: "diagnose", Mode: "parallel",
			Rounds:    [][]event.CollabAnswer{{{ExpertName: "A", Text: "ans A"}, {ExpertName: "B", Text: "ans B"}}},
			Synthesis: "syn", CreatedAt: 1700000000,
		}})
		if w.Kind != "expert_collab" || w.Collab == nil {
			t.Fatalf("collab wire = %+v / %+v", w, w.Collab)
		}
		if w.Collab.RunID != "r1" || w.Collab.TeamName != "netops" || len(w.Collab.Rounds) != 1 ||
			len(w.Collab.Rounds[0]) != 2 || w.Collab.Rounds[0][1].ExpertName != "B" ||
			w.Collab.Synthesis != "syn" || w.Collab.CreatedAt != 1700000000 {
			t.Errorf("collab payload = %+v", w.Collab)
		}
	})
	t.Run("item transition", func(t *testing.T) {
		w := toWire(event.Event{Kind: event.Item, Item: &event.ItemEvent{
			Phase: event.ItemDelta, ItemID: "i1", ItemKind: event.ItemAgentMessage, Delta: "hello",
		}})
		if w.Kind != "item" || w.Item == nil {
			t.Fatalf("item wire = %+v / %+v", w, w.Item)
		}
		if w.Item.Phase != "item_delta" || w.Item.ItemID != "i1" || w.Item.ItemKind != "agent_message" || w.Item.Delta != "hello" {
			t.Errorf("item payload = %+v", w.Item)
		}
	})
}

func TestToWireItemToolDeltaKind(t *testing.T) {
	w := toWire(event.Event{Kind: event.Item, Item: &event.ItemEvent{
		Phase: event.ItemDelta, ItemID: "t1", ItemKind: event.ItemToolCall,
		Delta: "chunk", DeltaKind: event.ItemDeltaOutput,
	}})
	if w.Item == nil || w.Item.DeltaKind != "output" {
		t.Fatalf("tool delta kind not carried: %+v", w.Item)
	}
}
