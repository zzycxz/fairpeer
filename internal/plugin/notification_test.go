package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/tool"
)

func TestParseServerNotification(t *testing.T) {
	cases := []struct {
		name   string
		frame  string
		method string
		params string
		wantOK bool
	}{
		{name: "tool list changed", frame: `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`, method: "notifications/tools/list_changed", wantOK: true},
		{name: "with params", frame: `{"jsonrpc":"2.0","method":"notifications/resources/updated","params":{"uri":"file:///a"}}`, method: "notifications/resources/updated", params: `{"uri":"file:///a"}`, wantOK: true},
		{name: "response has id", frame: `{"jsonrpc":"2.0","id":3,"result":{}}`, wantOK: false},
		{name: "server request has id", frame: `{"jsonrpc":"2.0","id":9,"method":"elicitation/create","params":{}}`, wantOK: false},
		{name: "garbage", frame: `not json`, wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, params, ok := parseServerNotification([]byte(tc.frame))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if method != tc.method {
				t.Fatalf("method = %q, want %q", method, tc.method)
			}
			if tc.params != "" && strings.TrimSpace(string(params)) != tc.params {
				t.Fatalf("params = %s, want %s", params, tc.params)
			}
		})
	}
}

func TestSSEDispatchSurfacesNotifications(t *testing.T) {
	tr := &sseTransport{name: "sse-test", pending: map[int]chan json.RawMessage{}}
	notify := make(chan string, 4)
	tr.setNotificationHandler(func(method string, _ json.RawMessage) { notify <- method })

	tr.dispatch(json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`))
	select {
	case m := <-notify:
		if m != "notifications/tools/list_changed" {
			t.Fatalf("method = %q", m)
		}
	case <-time.After(time.Second):
		t.Fatal("notification never surfaced")
	}

	// Responses still route by id.
	ch := make(chan json.RawMessage, 1)
	tr.mu.Lock()
	tr.pending[7] = ch
	tr.mu.Unlock()
	tr.dispatch(json.RawMessage(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`))
	select {
	case r := <-ch:
		if !strings.Contains(string(r), `"ok"`) {
			t.Fatalf("response payload = %s", r)
		}
	default:
		t.Fatal("response not routed to pending caller")
	}
}

func TestHTTPReadSSEResponseSurfacesNotifications(t *testing.T) {
	tr := &httpTransport{name: "http-test"}
	notify := make(chan string, 4)
	tr.setNotificationHandler(func(method string, _ json.RawMessage) { notify <- method })

	body := "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n" +
		"data: {\"jsonrpc\":\"2.0\",\"id\":5,\"result\":{\"tools\":[]}}\n\n"
	res, err := tr.readSSEResponse(strings.NewReader(body), 5)
	if err != nil {
		t.Fatalf("readSSEResponse: %v", err)
	}
	if res == nil {
		t.Fatal("response missing")
	}
	select {
	case m := <-notify:
		if m != "notifications/tools/list_changed" {
			t.Fatalf("method = %q", m)
		}
	case <-time.After(time.Second):
		t.Fatal("notification never surfaced")
	}
}

func TestStdioReadLoopSurfacesNotifications(t *testing.T) {
	pr, pw := io.Pipe()
	tr := &stdioTransport{stdout: bufio.NewReader(pr), pending: map[int]chan rpcResponse{}}
	notify := make(chan string, 4)
	tr.setNotificationHandler(func(method string, _ json.RawMessage) { notify <- method })

	// Progress notifications stay token-routed, never reaching the sink.
	tr.registerProgress("tok-1", func(string) {})
	go tr.readLoop()
	defer pw.Close()

	if _, err := pw.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-notify:
		if m != "notifications/tools/list_changed" {
			t.Fatalf("method = %q", m)
		}
	case <-time.After(time.Second):
		t.Fatal("notification never surfaced")
	}
}

// fakeRefreshTransport answers tools/list with one canned tool.
type fakeRefreshTransport struct{}

func (f *fakeRefreshTransport) call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != "tools/list" {
		return nil, fmt.Errorf("unexpected method %q", method)
	}
	return json.RawMessage(`{"tools":[{"name":"probe","description":"d"}]}`), nil
}
func (f *fakeRefreshTransport) notify(context.Context, string, any) error { return nil }
func (f *fakeRefreshTransport) close()                                    {}

func TestToolsListChangedRefreshesTools(t *testing.T) {
	redirectCache(t) // refreshClientTools persists the handshake cache

	h := &Host{}
	c := &Client{name: "srv", t: &fakeRefreshTransport{}, spec: Spec{Name: "srv", Type: "stdio"}}
	h.mu.Lock()
	h.clients = append(h.clients, c)
	h.mu.Unlock()

	refreshed := make(chan []tool.Tool, 1)
	h.ToolsRefreshed = func(server string, tools []tool.Tool) {
		if server != "srv" {
			t.Errorf("server = %q", server)
		}
		refreshed <- tools
	}

	h.handleServerNotification(c, "notifications/tools/list_changed", nil)

	select {
	case ts := <-refreshed:
		if len(ts) != 1 {
			t.Fatalf("refreshed tools = %d, want 1", len(ts))
		}
		if name := ts[0].Name(); !strings.Contains(name, "probe") {
			t.Fatalf("refreshed tool name = %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ToolsRefreshed never fired")
	}

	// The persisted cache carries the fresh list.
	cs, ok := LoadCachedSchema("srv", SpecFingerprint(c.spec))
	if !ok {
		t.Fatal("refreshed cache entry missing")
	}
	if len(cs.Tools) != 1 || cs.Tools[0].Name != "probe" {
		t.Fatalf("cached tools = %+v", cs.Tools)
	}

	if h.Servers()[0].Tools != 1 {
		t.Fatalf("status toolCount not updated: %+v", h.Servers())
	}
}
