package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSSEServer is a minimal legacy HTTP+SSE MCP server: the GET /sse stream
// announces the POST endpoint (optionally after a delay, to prove the client
// blocks on it instead of failing its first call) and later carries responses;
// POSTs to /messages are answered with a JSON-RPC result pushed onto that
// stream. Mirrors the mcpHTTPServer pattern in transport_http_test.go.
func fakeSSEServer(t *testing.T, endpointDelay time.Duration) *httptest.Server {
	t.Helper()
	frames := make(chan string, 16)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			if endpointDelay > 0 {
				time.Sleep(endpointDelay)
			}
			fmt.Fprint(w, "event: endpoint\ndata: /messages\n\n")
			if fl != nil {
				fl.Flush()
			}
			for {
				select {
				case frame := <-frames:
					fmt.Fprint(w, frame)
					if fl != nil {
						fl.Flush()
					}
				case <-r.Context().Done():
					return
				}
			}
		case "/messages":
			var req struct {
				ID     *int   `json:"id"`
				Method string `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == nil {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			var result any
			switch req.Method {
			case "initialize":
				result = map[string]any{"protocolVersion": protocolVersion, "serverInfo": map[string]any{"name": "s", "version": "0"}}
			case "tools/list":
				result = map[string]any{"tools": []map[string]any{{
					"name":        "greet",
					"description": "Greet someone.",
					"inputSchema": map[string]any{"type": "object"},
				}}}
			case "tools/call":
				result = map[string]any{"content": []map[string]any{{"type": "text", "text": "hi"}}}
			}
			resp := map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}
			b, _ := json.Marshal(resp)
			frames <- "event: message\ndata: " + string(b) + "\n\n"
		default:
			http.NotFound(w, r)
		}
	}))
}

// TestSSETransportDelayedEndpoint pins the endpoint handshake: the endpoint
// event arrives 300ms after the SSE stream opens — well after StartAll has
// already fired its initialize — so call() must block on the event instead of
// failing the first request with "no endpoint received".
func TestSSETransportDelayedEndpoint(t *testing.T) {
	srv := fakeSSEServer(t, 300*time.Millisecond)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	host, tools, err := StartAll(ctx, []Spec{{Name: "s", Type: "sse", URL: srv.URL + "/sse"}})
	if err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	defer host.Close()

	if len(tools) != 1 || tools[0].Name() != "mcp__s__greet" {
		t.Fatalf("tools = %v, want [mcp__s__greet]", names(tools))
	}
	got, err := tools[0].Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got != "hi" {
		t.Errorf("Execute = %q, want %q", got, "hi")
	}
}

// TestSSETransportNoEndpoint verifies a server that never sends the endpoint
// event fails the call with the documented message once the caller's context
// gives up.
func TestSSETransportNoEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Open the stream, never announce an endpoint, hold it open.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	c, err := start(ctx, ctx, Spec{Name: "stall", Type: "sse", URL: srv.URL + "/sse"})
	if err == nil {
		c.close()
		t.Fatal("start should fail when the endpoint event never arrives")
	}
	if !strings.Contains(err.Error(), "no endpoint received") {
		t.Errorf("err = %v, want it to mention the missing endpoint", err)
	}
}
