package agent

import (
	"encoding/json"
	"testing"

	"github.com/zzycxz/fairpeer/internal/provider"
	"github.com/zzycxz/fairpeer/internal/tool"
)

// P1-D1: task calls opted in with concurrent=true join the parallel fan-out
// (up to maxConcurrentTasks wide); plain task calls stay serial.
func TestPartitionConcurrentTasks(t *testing.T) {
	r := tool.NewRegistry()
	r.Add(fakeTool{name: "task", readOnly: false})
	r.Add(fakeTool{name: "read_file", readOnly: true})

	taskArgs := func(concurrent bool) string {
		b, _ := json.Marshal(map[string]any{"prompt": "x", "concurrent": concurrent})
		return string(b)
	}
	calls := []provider.ToolCall{
		{Name: "task", Arguments: taskArgs(true)},
		{Name: "task", Arguments: taskArgs(true)},
		{Name: "task", Arguments: taskArgs(true)},
	}
	// NEW-03 final verdict: concurrent task fan-out is SUSPENDED — worktree
	// isolation was proven never wired (runtime test: writes landed on the
	// shared main workspace with a lost update), so concurrent tasks run in
	// the serial writer batch until real isolation lands.
	batches := partitionToolCalls(r, calls, nil)
	if len(batches) != 3 {
		t.Fatalf("concurrent tasks must run as 3 serial single-call batches, got %+v", batches)
	}
	for _, b := range batches {
		if b.parallel {
			t.Fatalf("task batches must not be parallel while isolation is unwired: %+v", batches)
		}
	}

	serial := []provider.ToolCall{
		{Name: "task", Arguments: taskArgs(false)},
		{Name: "task", Arguments: taskArgs(false)},
	}
	b2 := partitionToolCalls(r, serial, nil)
	if len(b2) != 2 || b2[0].parallel {
		t.Fatalf("plain tasks must stay serial, got %+v", b2)
	}

	// Mixed: read-only runs parallel, the suspended concurrent task runs as
	// its own serial batch right after.
	mixed := []provider.ToolCall{
		{Name: "read_file", Arguments: `{}`},
		{Name: "task", Arguments: taskArgs(true)},
	}
	b3 := partitionToolCalls(r, mixed, nil)
	if len(b3) != 2 || !b3[0].parallel || b3[1].parallel {
		t.Fatalf("read-only + suspended concurrent task = parallel reads then serial task, got %+v", b3)
	}
}
