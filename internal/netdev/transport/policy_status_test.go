package transport

import (
	"errors"
	"testing"
	"time"
)

func TestKeepalivePolicyDefaults(t *testing.T) {
	p := KeepalivePolicy{}
	if got := p.interval(); got != 30*time.Second {
		t.Fatalf("interval = %v, want 30s", got)
	}
	if got := p.maxMisses(); got != 3 {
		t.Fatalf("maxMisses = %d, want 3", got)
	}
	if got := p.timeout(); got != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", got)
	}
	disabled := KeepalivePolicy{Interval: -1}
	if got := disabled.interval(); got != 0 {
		t.Fatalf("disabled interval = %v, want 0", got)
	}
}

func TestBackoffPolicyDelay(t *testing.T) {
	p := BackoffPolicy{}
	if got := p.delay(0); got != time.Second {
		t.Fatalf("delay(0) = %v, want 1s", got)
	}
	if got := p.delay(1); got != 2*time.Second {
		t.Fatalf("delay(1) = %v, want 2s", got)
	}
	if got := p.delay(2); got != 4*time.Second {
		t.Fatalf("delay(2) = %v, want 4s", got)
	}
	// Capped at Max.
	if got := p.delay(10); got != 60*time.Second {
		t.Fatalf("delay(10) = %v, want 60s cap", got)
	}
}

func TestStatusHubReplaysAndFansOut(t *testing.T) {
	h := newStatusHub()
	first := StatusEvent{Host: "h1", Status: StatusConnected}
	h.publish(first)

	var gotInitial StatusEvent
	cancel1 := h.subscribe(func(ev StatusEvent) { gotInitial = ev })
	if gotInitial.Host != "h1" || gotInitial.Status != StatusConnected {
		t.Fatalf("replay = %+v, want the last event", gotInitial)
	}

	events := make(chan StatusEvent, 8)
	h.subscribe(func(ev StatusEvent) { events <- ev })
	// The new subscriber first receives the replayed last event…
	if ev := <-events; ev.Status != StatusConnected {
		t.Fatalf("replay to second subscriber = %v", ev.Status)
	}
	h.publish(StatusEvent{Host: "h1", Status: StatusReconnecting})
	if ev := <-events; ev.Status != StatusReconnecting {
		t.Fatalf("fanout status = %v", ev.Status)
	}
	cancel1()

	h.publish(StatusEvent{Host: "h1", Status: StatusStopped})
	// The cancelled subscription receives nothing further; only the second
	// subscriber's channel has the event.
	if ev := <-events; ev.Status != StatusStopped {
		t.Fatalf("post-cancel status = %v", ev.Status)
	}
	select {
	case ev := <-events:
		t.Fatalf("cancelled subscriber still receives: %+v", ev)
	default:
	}
}

func TestClassifyDialErrorAuth(t *testing.T) {
	cases := []string{
		"ssh: unable to authenticate",
		"no supported methods remain",
		"permission denied",
	}
	for _, msg := range cases {
		err := classifyDialError(errors.New(msg))
		if !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("classify(%q) = %v, want ErrAuthFailed", msg, err)
		}
	}
	transient := classifyDialError(errors.New("i/o timeout"))
	if errors.Is(transient, ErrAuthFailed) {
		t.Fatalf("i/o timeout classified as auth failure")
	}
	if !errors.Is(errAuth{errors.New("x")}, ErrAuthFailed) {
		t.Fatalf("errAuth does not match ErrAuthFailed")
	}
}
