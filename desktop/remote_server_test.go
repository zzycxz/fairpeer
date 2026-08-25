package main

// TestServerTransportE2E attaches to a live `fairpeer host --listen` over TCP
// through the real serverTransport (token handshake + protocol). Opt-in via
// FP_SERVER_ADDR / FP_SERVER_TOKEN:
//
//	../scratch/fairpeer-host-e2e.exe host --listen 127.0.0.1:18787 --token tk123 &
//	FP_SERVER_ADDR=127.0.0.1:18787 FP_SERVER_TOKEN=tk123 \
//	  go test -run TestServerTransportE2E -count=1
//
// A wrong-token variant is asserted inline when the env points at the same host.
import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/remotehost"
)

func TestServerTransportE2E(t *testing.T) {
	addr := strings.TrimSpace(os.Getenv("FP_SERVER_ADDR"))
	token := strings.TrimSpace(os.Getenv("FP_SERVER_TOKEN"))
	if addr == "" || token == "" {
		t.Skip("FP_SERVER_ADDR / FP_SERVER_TOKEN not set")
	}

	ref := RemoteRef{Kind: "server", Target: addr}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Wrong token must fail cleanly.
	bad := &serverTransport{token: "wrong-token"}
	if _, _, _, err := bad.Dial(ctx, ref); err == nil {
		t.Fatal("dial with wrong token should fail")
	}

	tr := &serverTransport{token: token}
	stdin, stdout, proc, err := tr.Dial(ctx, ref)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	link := newRemoteHostLink(ctx, stdin, stdout, proc)
	defer link.close()

	var hello remotehost.HelloResult
	if err := link.call(ctx, "host/hello", map[string]any{}, &hello); err != nil {
		t.Fatalf("hello: %v", err)
	}
	if hello.Version == "" {
		t.Fatal("hello missing version")
	}

	// Session survives a reconnect (shared registry across connections).
	var newRes remotehost.SessionNewResult
	if err := link.call(ctx, "session/new", remotehost.SessionNewParams{SessionID: "persist", Cwd: hello.Home}, &newRes); err != nil {
		t.Fatalf("session/new: %v", err)
	}
	link.close()

	stdin2, stdout2, proc2, err := tr.Dial(ctx, ref)
	if err != nil {
		t.Fatalf("re-Dial: %v", err)
	}
	link2 := newRemoteHostLink(ctx, stdin2, stdout2, proc2)
	defer link2.close()
	var state remotehost.SessionStateResult
	if err := link2.call(ctx, "session/state", remotehost.SessionRef{SessionID: "persist"}, &state); err != nil {
		t.Fatalf("session/state after reconnect: %v (sessions should be shared)", err)
	}
	if state.SessionPath != newRes.SessionPath {
		t.Fatalf("sessionPath after reconnect = %s, want %s", state.SessionPath, newRes.SessionPath)
	}
}
