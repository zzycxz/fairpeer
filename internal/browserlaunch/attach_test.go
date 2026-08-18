package browserlaunch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNormalizeCDPEndpoint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"http://127.0.0.1:9222", "http://127.0.0.1:9222"},
		{"ws://127.0.0.1:9222/devtools/browser/abc", "http://127.0.0.1:9222"},
		{"localhost:9222", "http://localhost:9222"},
		{"9222", "http://127.0.0.1:9222"},
		{"localhost", "http://localhost:9222"},
		{"  http://127.0.0.1:9222/  ", "http://127.0.0.1:9222"},
		{"", ""},
		{"   ", ""},
		{"ftp://x", ""},
	}
	for _, tc := range cases {
		if got := NormalizeCDPEndpoint(tc.in); got != tc.want {
			t.Errorf("NormalizeCDPEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProbeAttachResolvesWSURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"Browser": "Chrome/137.0.7151.68",
			"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/abc123"
		}`))
	}))
	defer srv.Close()

	info, err := ProbeAttach(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("ProbeAttach: %v", err)
	}
	if info.CDPURL != srv.URL {
		t.Errorf("CDPURL = %q, want %q", info.CDPURL, srv.URL)
	}
	if info.WSURL != "ws://127.0.0.1:9222/devtools/browser/abc123" {
		t.Errorf("WSURL = %q, want the /json/version webSocketDebuggerUrl", info.WSURL)
	}
	if info.BrowserName != "Chrome" {
		t.Errorf("BrowserName = %q, want Chrome", info.BrowserName)
	}
}

func TestProbeAttachUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Port 1 on loopback is never bound (reserve port; bind requires root).
	if _, err := ProbeAttach(ctx, "http://127.0.0.1:1"); err == nil {
		t.Error("expected error for unreachable endpoint")
	}
}

func TestBrowserProduct(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Chrome/137.0.7151.68", "Chrome"},
		{"Edg/137.0.100.0", "Edge"},
		{"HeadlessChrome/120.0", "HeadlessChrome"},
		{"Brave/1.0", "Brave"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := browserProduct(tc.in); got != tc.want {
			t.Errorf("browserProduct(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
