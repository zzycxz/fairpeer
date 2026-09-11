package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/control"
)

// TestServeTokenGuard guards the --token remote-access posture: with a token
// set every route requires it (Bearer header or ?token= query — the query
// form is what EventSource must use), and a wrong token gets 401 before any
// handler runs.
func TestServeTokenGuard(t *testing.T) {
	got := make(chan string, 1)
	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Runner: fakeRunner{got: got}, Sink: bc})
	s := New(ctrl, bc)
	s.SetAuthToken("secret-1")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// No token: 401, and nothing reaches the runner.
	resp, err := http.Post(srv.URL+"/submit", "application/json", strings.NewReader(`{"input":"pwn"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", resp.StatusCode)
	}
	select {
	case in := <-got:
		t.Fatalf("an unauthenticated POST reached the runner with %q", in)
	default:
	}

	// Wrong token via header: 401.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/submit", strings.NewReader(`{"input":"pwn"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", resp.StatusCode)
	}

	// Right token via header: passes the guard (reaches the controller; the
	// fake runner's response is irrelevant here).
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/submit", strings.NewReader(`{"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-1")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("correct Bearer token: got 401")
	}

	// Right token via query param: passes (the EventSource form).
	resp, err = http.Get(srv.URL + "/status?token=secret-1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("correct ?token= query: got 401")
	}

	// The index page is guarded too (no unauthenticated shell).
	resp, err = http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("index without token: status = %d, want 401", resp.StatusCode)
	}
}
