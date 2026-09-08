package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/control"
)

// TestServeHostGuardBlocksDNSRebinding guards against a page at attacker.com
// (resolving to 127.0.0.1) becoming same-origin to the browser: the Host
// header names attacker.com, not the loopback the server bound, so the
// request is refused regardless of Content-Type.
func TestServeHostGuardBlocksDNSRebinding(t *testing.T) {
	s := &Server{bindHost: "127.0.0.1"}

	for _, tc := range []struct {
		host string
		want bool
	}{
		{"127.0.0.1:8787", true},
		{"localhost:8787", true},
		{"[::1]:8787", true},
		{"LOCALHOST:8787", true},
		{"attacker.com:8787", false},
		{"127.0.0.2:8787", true}, // loopback literal: only a local user sends this
		{"", false},
	} {
		if got := s.hostAllowed(tc.host); got != tc.want {
			t.Errorf("hostAllowed(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}

	// Wildcard bind accepts private hosts but not public ones.
	w := &Server{bindHost: "0.0.0.0"}
	if !w.hostAllowed("192.168.1.10:8787") {
		t.Error("wildcard bind should allow private LAN host")
	}
	if w.hostAllowed("evil.example:8787") {
		t.Error("wildcard bind must refuse public host names (rebinding)")
	}

	// Unset bindHost (tests / direct Handler use) allows everything.
	u := &Server{bindHost: ""}
	if !u.hostAllowed("anything:1") {
		t.Error("unset bindHost must not restrict")
	}
}

// TestServeHostGuardEndToEnd drives the middleware over a real socket: a
// JSON POST with a spoofed Host is rejected before reaching the runner.
func TestServeHostGuardEndToEnd(t *testing.T) {
	got := make(chan string, 1)
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Runner: fakeRunner{got: got}, Sink: bc})
	s := New(ctrl, bc)
	s.bindHost = "127.0.0.1"
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/submit", strings.NewReader(`{"input":"pwn"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "attacker.example:8787"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("spoofed Host status = %d, want 403", resp.StatusCode)
	}
	select {
	case in := <-got:
		t.Fatalf("rebinding POST reached the runner with %q", in)
	default:
	}
}

// TestServeBodyLimit caps unauthenticated request bodies.
func TestServeBodyLimit(t *testing.T) {
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Runner: fakeRunner{}, Sink: bc})
	s := New(ctrl, bc)
	s.bindHost = "127.0.0.1"
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	big := strings.Repeat("x", serveMaxBodyBytes+1)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/submit", strings.NewReader(`{"input":"`+big+`"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// The handler's decode-error path answers 400; what matters is that the
	// body was never fully read and the runner never saw the payload.
	if resp.StatusCode != http.StatusRequestEntityTooLarge && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized body status = %d, want 413 or 400", resp.StatusCode)
	}
}

// TestSetBindHostEmptyHostMeansWildcardNotDisabled guards SERVE-1: ":8787"
// (empty host = bind-all, a common Go idiom) must normalize to the wildcard
// branch — landing in bindHost == "" would silently allow-everything and
// disable the hostGuard entirely.
func TestSetBindHostEmptyHostMeansWildcardNotDisabled(t *testing.T) {
	s := &Server{}
	s.setBindHost(":8787")
	if s.bindHost != "*" {
		t.Fatalf("bindHost = %q, want * (wildcard semantics)", s.bindHost)
	}
	if !s.hostAllowed("192.168.1.10:8787") {
		t.Error("wildcard normalization should allow private LAN hosts")
	}
	if s.hostAllowed("evil.example:8787") {
		t.Error("wildcard normalization must still refuse public host names")
	}
}
