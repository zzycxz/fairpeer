package builtin

import (
	"testing"
	"time"
)

func recEvent(typ, selector string, ts int64) ConsoleRecordEvent {
	return ConsoleRecordEvent{Type: typ, Selector: selector, Time: ts}
}

func TestFilterRecordEventsMergesEffects(t *testing.T) {
	base := time.Now().UnixMilli()
	events := []ConsoleRecordEvent{
		{Type: "click", Selector: "#go", Time: base},
		{Type: "effect", Selector: "#go", Value: "true", Time: base + 600},
		{Type: "click", Selector: "#dead", Time: base + 1000},
		{Type: "effect", Selector: "#dead", Value: "false", Time: base + 1600},
	}
	kept, dropped := FilterRecordEvents(events)
	if len(kept) != 1 {
		t.Fatalf("effective click kept, ineffective wandering click dropped: kept=%+v", kept)
	}
	if len(dropped) != 1 || dropped[0].Selector != "#dead" {
		t.Fatalf("only the wandering click should drop, dropped=%+v", dropped)
	}
	if kept[0].Effective == nil || !*kept[0].Effective {
		t.Errorf("click #go should be effective, got %+v", kept[0].Effective)
	}
}

func TestFilterRecordEventsCollapsesRapidClicks(t *testing.T) {
	base := time.Now().UnixMilli()
	events := []ConsoleRecordEvent{
		recEvent("click", "#btn", base),
		recEvent("click", "#btn", base+200),
		recEvent("click", "#btn", base+400),
	}
	kept, dropped := FilterRecordEvents(events)
	if len(kept) != 1 || kept[0].Selector != "#btn" {
		t.Fatalf("rapid same-target clicks should collapse to one, kept=%+v", kept)
	}
	if len(dropped) != 2 {
		t.Fatalf("expected 2 dropped, got %d", len(dropped))
	}
}

func TestFilterRecordEventsDropsIneffectiveWanderingClick(t *testing.T) {
	base := time.Now().UnixMilli()
	events := []ConsoleRecordEvent{
		{Type: "click", Selector: "div.wall", Time: base, Effective: boolPtr(false)},
		{Type: "click", Selector: "a.next", Time: base + 2000, Effective: boolPtr(false)},
		{Type: "navigate", URL: "https://x/next", Time: base + 2100},
	}
	kept, dropped := FilterRecordEvents(events)
	if len(kept) != 2 {
		t.Fatalf("wandering click dropped, navigating click kept: kept=%+v dropped=%+v", kept, dropped)
	}
	if len(dropped) != 1 || dropped[0].Selector != "div.wall" {
		t.Fatalf("only the wandering click should drop, dropped=%+v", dropped)
	}
}

func TestFilterRecordEventsKeepsLastInputPerField(t *testing.T) {
	base := time.Now().UnixMilli()
	events := []ConsoleRecordEvent{
		{Type: "input", Selector: "#q", Value: "op", Time: base},
		{Type: "input", Selector: "#q", Value: "ops portal", Time: base + 3000},
		{Type: "input", Selector: "#user", Value: "admin", Time: base + 4000},
	}
	kept, dropped := FilterRecordEvents(events)
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept inputs, got %+v", kept)
	}
	if kept[0].Value != "ops portal" || kept[1].Value != "admin" {
		t.Fatalf("later input should win per field: %+v", kept)
	}
	if len(dropped) != 1 || dropped[0].Value != "op" {
		t.Fatalf("superseded input should drop: %+v", dropped)
	}
}

func TestFilterRecordEventsCollapsesRoundTripNavigation(t *testing.T) {
	base := time.Now().UnixMilli()
	events := []ConsoleRecordEvent{
		{Type: "navigate", URL: "https://portal/", Time: base},
		{Type: "navigate", URL: "https://wrong.example/", Time: base + 500},
		{Type: "navigate", URL: "https://portal/", Time: base + 900},
		{Type: "click", Selector: "#login", Time: base + 1500},
	}
	kept, dropped := FilterRecordEvents(events)
	if len(kept) != 2 {
		t.Fatalf("round trip should collapse to start+click, kept=%+v", kept)
	}
	if len(dropped) != 2 || dropped[0].URL != "https://wrong.example/" || dropped[1].URL != "https://portal/" {
		t.Fatalf("both B legs should drop: %+v", dropped)
	}
}

func TestFilterRecordEventsDropsDuplicateNavigation(t *testing.T) {
	base := time.Now().UnixMilli()
	events := []ConsoleRecordEvent{
		{Type: "navigate", URL: "https://a/", Time: base},
		{Type: "navigate", URL: "https://a/", Time: base + 100},
	}
	kept, dropped := FilterRecordEvents(events)
	if len(kept) != 1 || len(dropped) != 1 {
		t.Fatalf("duplicate navigation should collapse: kept=%+v dropped=%+v", kept, dropped)
	}
}

func TestFilterRecordEventsDropsMalformed(t *testing.T) {
	events := []ConsoleRecordEvent{{Type: "click", Time: 1}}
	kept, dropped := FilterRecordEvents(events)
	if len(kept) != 0 || len(dropped) != 1 {
		t.Fatalf("selector-less click should drop: kept=%+v dropped=%+v", kept, dropped)
	}
}

func boolPtr(b bool) *bool { return &b }
