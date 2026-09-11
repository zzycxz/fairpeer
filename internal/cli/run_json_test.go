package cli

// run_json_test.go — `fairpeer run --json`（CODEX_GAP_AUDIT G2）红测试：
// jsonlSink 每事件一行合法 JSON（eventwire 契约，Item/ExpertCollab 全覆盖），
// result 汇总行反映 ok/text/error，错误事件不丢。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/event"
)

func emitAll(t *testing.T, s *jsonlSink, events []event.Event) bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	s.w = &buf
	for _, e := range events {
		s.Emit(e)
	}
	return buf
}

func decodeLines(t *testing.T, buf bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line is not valid JSON: %q (%v)", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestJSONLSinkEmitsWireContract(t *testing.T) {
	s := &jsonlSink{}
	buf := emitAll(t, s, []event.Event{
		{Kind: event.TurnStarted},
		{Kind: event.Text, Text: "Hel"},
		{Kind: event.ToolDispatch, Tool: event.Tool{ID: "t1", Name: "bash", Args: "{}"}},
		{Kind: event.Message, Text: "done"},
		{Kind: event.TurnDone},
	})
	lines := decodeLines(t, buf)
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5", len(lines))
	}
	wantKinds := []string{"turn_started", "text", "tool_dispatch", "message", "turn_done"}
	for i, m := range lines {
		if m["kind"] != wantKinds[i] {
			t.Errorf("line %d kind = %v, want %s", i, m["kind"], wantKinds[i])
		}
	}
	// Tool payload rides the wire (id/name/readOnly fields of the contract).
	tool := lines[2]["tool"].(map[string]any)
	if tool["id"] != "t1" || tool["name"] != "bash" {
		t.Fatalf("tool payload = %v", tool)
	}
}

func TestJSONLSinkCarriesItemAndCollabKinds(t *testing.T) {
	s := &jsonlSink{}
	buf := emitAll(t, s, []event.Event{
		{Kind: event.Item, Item: &event.ItemEvent{
			Phase: event.ItemDelta, ItemID: "t9", ItemKind: event.ItemToolCall,
			Delta: "chunk", DeltaKind: event.ItemDeltaOutput,
		}},
		{Kind: event.ExpertCollab, Collab: event.Collab{RunID: "r1", TeamName: "netops", Synthesis: "syn"}},
		{Kind: event.Resumed},
	})
	lines := decodeLines(t, buf)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3 — empty-kind frames are forbidden (G2)", len(lines))
	}
	if lines[0]["kind"] != "item" {
		t.Fatalf("kind = %v, want item", lines[0]["kind"])
	}
	item := lines[0]["item"].(map[string]any)
	if item["itemId"] != "t9" || item["deltaKind"] != "output" {
		t.Fatalf("item payload = %v", item)
	}
	if lines[1]["kind"] != "expert_collab" || lines[1]["collab"] == nil {
		t.Fatalf("expert_collab line = %v", lines[1])
	}
	if lines[2]["kind"] != "resumed" {
		t.Fatalf("resumed line = %v", lines[2])
	}
}

func TestJSONLSinkResultLineAndLastText(t *testing.T) {
	s := &jsonlSink{}
	emitAll(t, s, []event.Event{
		{Kind: event.Message, Text: "final answer"},
		{Kind: event.TurnDone},
	})
	var buf bytes.Buffer
	s.w = &buf
	s.writeResult(true, "sess/file.jsonl")

	lines := decodeLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("result lines = %d, want 1", len(lines))
	}
	r := lines[0]
	if r["kind"] != "result" || r["ok"] != true || r["text"] != "final answer" {
		t.Fatalf("result = %v", r)
	}

	// Failed turn: ok=false and the error rides the line.
	s2 := &jsonlSink{}
	emitAll(t, s2, []event.Event{{Kind: event.TurnDone, Err: errors.New("boom")}})
	buf2 := emitAll(t, s2, nil)
	s2.w = &buf2
	s2.writeResult(false, "sess/x.jsonl")
	r2 := decodeLines(t, buf2)[0]
	if r2["ok"] != false || r2["error"] != "boom" {
		t.Fatalf("failed result = %v", r2)
	}
}
