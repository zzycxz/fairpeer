package agent

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zzycxz/fairpeer/internal/event"
	"github.com/zzycxz/fairpeer/internal/provider"
	"github.com/zzycxz/fairpeer/internal/tool"
)

func TestRepeatGuardBlocksRepeatedSuccessfulBashFileWrite(t *testing.T) {
	var calls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false, calls: &calls})
	args := `{"command":"python -c \"with open('prompt.txt', 'w') as f: f.write('hello')\""}`
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "bash", args), {Type: provider.ChunkDone}},
		{toolCallChunk("c2", "bash", args), {Type: provider.ChunkDone}},
		{toolCallChunk("c3", "bash", args), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(context.Background(), "update the prompt file"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("bash executed %d times, want 2 before the repeat guard blocks", got)
	}
	results := toolResults(a.session, "bash")
	if len(results) != 3 {
		t.Fatalf("tool results = %d, want 3", len(results))
	}
	last := results[len(results)-1]
	if !strings.Contains(last, "[loop guard]") || !strings.Contains(last, "edit_file") {
		t.Fatalf("third repeated write should nudge the model to change tools, got %q", last)
	}
}

func TestRepeatGuardAllowsRepeatedNonWritingBashCommand(t *testing.T) {
	var calls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false, calls: &calls})
	args := `{"command":"go test ./internal/agent"}`
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "bash", args), {Type: provider.ChunkDone}},
		{toolCallChunk("c2", "bash", args), {Type: provider.ChunkDone}},
		{toolCallChunk("c3", "bash", args), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(context.Background(), "verify repeatedly"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("bash executed %d times, want 3 for non-writing commands", got)
	}
	if last := lastToolResult(a.session, "bash"); strings.Contains(last, "[loop guard]") {
		t.Fatalf("non-writing bash should not trip the repeat guard, got %q", last)
	}
}

func TestRepeatGuardAllowsTwoRepeatedWriterSuccesses(t *testing.T) {
	var calls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false, calls: &calls})
	args := `{"path":"prompt.txt","content":"hello"}`
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "write_file", args), {Type: provider.ChunkDone}},
		{toolCallChunk("c2", "write_file", args), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	if err := a.Run(context.Background(), "write twice"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("writer executed %d times, want 2 before the guard threshold", got)
	}
	if last := lastToolResult(a.session, "write_file"); strings.Contains(last, "[loop guard]") {
		t.Fatalf("second repeated writer call should still be allowed, got %q", last)
	}
}

// TestRepeatGuardConcurrentWriterBatch is the regression test for the
// concurrent-map-write crash: writer batches with disjoint preview paths run
// via runParallel, so recordRepeatSuccess and repeatedSuccessBlock fire from
// parallel goroutines whenever a model emits two writer calls in one turn.
// Run with -race; before repeatMu existed this threw
// "fatal error: concurrent map read and map write".
func TestRepeatGuardConcurrentWriterBatch(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false, calls: new(int32)})
	reg.Add(fakeTool{name: "edit_file", readOnly: false, calls: new(int32)})
	a := New(&scriptedProvider{name: "p"}, reg, NewSession(""), Options{}, event.Discard)

	const goroutines = 16
	const iterations = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := "write_file"
			if g%2 == 1 {
				name = "edit_file"
			}
			call := provider.ToolCall{ID: "c", Name: name, Arguments: `{"path":"f` + strconv.Itoa(g) + `.txt","content":"x"}`}
			tool, _ := reg.Get(name)
			for i := 0; i < iterations; i++ {
				a.recordRepeatSuccess(call, tool)
				a.repeatedSuccessBlock(call, tool)
			}
		}(g)
	}
	wg.Wait()
}
