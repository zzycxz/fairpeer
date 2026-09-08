package event

import (
	"encoding/json"
	"testing"
)

// collectingSink records every event in emission order.
type collectingSink struct{ events []Event }

func (c *collectingSink) Emit(e Event) { c.events = append(c.events, e) }

func TestItemAdapterToolDeltaKinds(t *testing.T) {
	sink := &collectingSink{}
	a := NewItemAdapter(sink)

	a.Emit(Event{Kind: ToolDispatch, Tool: Tool{ID: "t1", Name: "bash", Args: "{}"}})
	a.Emit(Event{Kind: ToolArgsDelta, Tool: Tool{ID: "t1", Name: "bash"}, Text: "*** patch"})
	a.Emit(Event{Kind: ToolProgress, Tool: Tool{ID: "t1", Name: "bash", Output: "chunk1\n"}})
	a.Emit(Event{Kind: ToolResult, Tool: Tool{ID: "t1", Name: "bash", Output: "chunk1\n"}})

	var kinds []string
	for _, e := range sink.events {
		if e.Kind != Item || e.Item.ItemID != "t1" {
			continue
		}
		switch e.Item.Phase {
		case ItemDelta:
			kind := string(e.Item.DeltaKind)
			if kind == "" {
				t.Fatalf("tool delta without a category: %+v", e.Item)
			}
			kinds = append(kinds, kind)
		case ItemCompleted:
			var payload ToolCallItem
			if err := json.Unmarshal(e.Item.Item, &payload); err != nil {
				t.Fatalf("completed payload: %v", err)
			}
			if payload.Status != "done" || payload.Output != "chunk1\n" {
				t.Fatalf("completed payload = %+v", payload)
			}
		}
	}
	if len(kinds) != 2 || kinds[0] != "args" || kinds[1] != "output" {
		t.Fatalf("delta kinds = %v, want [args output]", kinds)
	}
}

// The dual-track ordering guarantee the desktop reducer's suppression relies
// on: for every derived item, the legacy twin was emitted FIRST.
func TestItemAdapterEmitsLegacyBeforeItemTwin(t *testing.T) {
	sink := &collectingSink{}
	a := NewItemAdapter(sink)

	a.Emit(Event{Kind: Text, Text: "one"})
	a.Emit(Event{Kind: Message, Text: "one", Reasoning: "r"})

	seenLegacy := false
	for _, e := range sink.events {
		if e.Kind == Text {
			seenLegacy = true
			continue
		}
		if e.Kind == Item {
			if !seenLegacy {
				t.Fatal("item twin emitted before its legacy Text event")
			}
		}
	}
}
