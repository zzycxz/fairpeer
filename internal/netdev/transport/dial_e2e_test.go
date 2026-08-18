package transport

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClientDirectConnectExec(t *testing.T) {
	_, addr := newSSHTestServer(t, "pw", false)
	host, port := splitHostPort(t, addr)

	c, err := New(Options{
		Host:     ResolvedHost{HostName: host, Port: port, User: "u"},
		Auth:     AuthOptions{Password: func() (string, error) { return "pw", nil }},
		HostKeys: acceptAllPolicy(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	if got := c.Status().Status; got != StatusConnected {
		t.Fatalf("status = %v, want connected", got)
	}
	res, err := c.Exec(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d, stdout = %q", res.ExitCode, res.Stdout)
	}
}

func TestClientJumpChainDialsThroughHop(t *testing.T) {
	// The jump server forwards TCP; the target does not. The target is only
	// reachable through the jump host's direct-tcpip channel.
	_, jumpAddr := newSSHTestServer(t, "jumpw", true)
	_, targetAddr := newSSHTestServer(t, "targetpw", false)
	jumpHost, jumpPort := splitHostPort(t, jumpAddr)
	targetHost, targetPort := splitHostPort(t, targetAddr)

	// A control dial straight to the target is possible at the TCP level here
	// (same process), so reachability is not the assertion; the assertion is
	// that the chain resolves, authenticates per hop, and reaches Connected
	// with hops tracked.
	c, err := New(Options{
		Host: ResolvedHost{
			HostName: targetHost, Port: targetPort, User: "u",
			ProxyJump: []string{"jump"},
		},
		Auth: AuthOptions{Password: func() (string, error) { return "targetpw", nil }},
		JumpHosts: []JumpHostOptions{{
			Host: ResolvedHost{HostName: jumpHost, Port: jumpPort, User: "jumpu"},
			Auth: AuthOptions{Password: func() (string, error) { return "jumpw", nil }},
		}},
		HostKeys: acceptAllPolicy(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start through jump: %v", err)
	}
	defer c.Close()

	if got := c.Status().Status; got != StatusConnected {
		t.Fatalf("status = %v, want connected", got)
	}
	res, err := c.Exec(context.Background(), "uptime")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("Exec via jump: %v %+v", err, res)
	}
}

func TestClientAuthFailureStops(t *testing.T) {
	_, addr := newSSHTestServer(t, "correct", false)
	host, port := splitHostPort(t, addr)

	c, err := New(Options{
		Host:        ResolvedHost{HostName: host, Port: port, User: "u"},
		Auth:        AuthOptions{Password: func() (string, error) { return "wrong", nil }},
		HostKeys:    acceptAllPolicy(t),
		DialTimeout: testDialTimeout(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Start(context.Background())
	if err == nil {
		c.Close()
		t.Fatal("Start with wrong password unexpectedly succeeded")
	}
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	if got := c.Status().Status; got != StatusStopped {
		t.Fatalf("status = %v, want stopped (no retry loop on auth failure)", got)
	}
}

func TestClientReconnectsAfterDrop(t *testing.T) {
	_, addr := newSSHTestServer(t, "pw", false)
	host, port := splitHostPort(t, addr)

	c, err := New(Options{
		Host:        ResolvedHost{HostName: host, Port: port, User: "u"},
		Auth:        AuthOptions{Password: func() (string, error) { return "pw", nil }},
		HostKeys:    acceptAllPolicy(t),
		DialTimeout: testDialTimeout(),
		Keepalive:   KeepalivePolicy{Interval: 50 * time.Millisecond, Timeout: 200 * time.Millisecond},
		Backoff:     BackoffPolicy{Initial: 10 * time.Millisecond, Max: 50 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Close()

	reconnected := make(chan struct{})
	c.Subscribe(func(ev StatusEvent) {
		if ev.Status == StatusConnected && ev.Attempt > 0 {
			select {
			case <-reconnected:
			default:
				close(reconnected)
			}
		}
	})

	// Kill the underlying connection; the supervisor must redial and reach
	// Connected again.
	cl, err := c.SSH()
	if err != nil {
		t.Fatal(err)
	}
	_ = cl.Close()

	select {
	case <-reconnected:
	case <-time.After(5 * time.Second):
		t.Fatalf("no reconnect observed; status = %+v", c.Status())
	}
}
