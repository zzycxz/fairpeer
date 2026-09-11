package builtin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// echoCommand returns a shell command that echoes stdin lines back — portable
// across the Windows cmd / unix shells the session tool spawns.
func echoCommand() string {
	// ResolveShell finds Git Bash on Windows too — cat works in both.
	return "cat"
}

func es(t *testing.T, action, sessionID, command, input string, timeoutMs int) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"action": action, "session_id": sessionID,
		"command": command, "input": input, "timeout_ms": timeoutMs,
	})
	out, err := (execSessionTool{}).Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return out
}

func TestExecSessionSpawnWriteReadKill(t *testing.T) {
	out := es(t, "spawn", "", echoCommand(), "", 0)
	if !strings.Contains(out, "session es") {
		t.Fatalf("spawn output = %q", out)
	}
	id := strings.Fields(out)[1]

	es(t, "write", id, "", "hello session", 0)
	deadline := time.Now().Add(5 * time.Second)
	var got string
	for {
		got += es(t, "read", id, "", "", 300)
		if strings.Contains(got, "hello session") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("output never contained the written line; got %q", got)
		}
	}

	if out := es(t, "kill", id, "", "", 0); !strings.Contains(out, "killed") {
		t.Fatalf("kill output = %q", out)
	}
	// Post-kill operations on the removed session fail cleanly.
	if _, err := (execSessionTool{}).Execute(context.Background(), mustJSONSession(map[string]string{"action": "read", "session_id": id})); err == nil {
		t.Fatal("read on killed session must fail")
	}
}

func TestExecSessionUnknownSessionFails(t *testing.T) {
	for _, action := range []string{"write", "read", "kill"} {
		if _, err := (execSessionTool{}).Execute(context.Background(), mustJSONSession(map[string]string{"action": action, "session_id": "nope"})); err == nil {
			t.Fatalf("%s on unknown session must fail", action)
		}
	}
	if _, err := (execSessionTool{}).Execute(context.Background(), mustJSONSession(map[string]string{"action": "spawn"})); err == nil {
		t.Fatal("spawn without command must fail")
	}
}

func mustJSONSession(m map[string]string) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

var _ = context.Background
