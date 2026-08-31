package netdev

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPFingerprintPlain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("polite probe must GET / only, got %q", r.URL.Path)
		}
		w.Header().Set("Server", "nginx/1.24.0")
		w.Write([]byte("<html><head><title>OA 登录</title></head><body></body></html>"))
	}))
	defer srv.Close()
	ip, port := hostPort(t, srv.URL)
	fp := httpFingerprint(context.Background(), directDialer{timeout: 3e9}, ip, port, false)
	if fp == nil {
		t.Fatal("fp = nil")
	}
	if fp.Title != "OA 登录" || fp.Server != "nginx/1.24.0" || fp.CertCN != "" {
		t.Errorf("fp = %+v", fp)
	}
}

func TestHTTPFingerprintTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "Apache")
		w.Write([]byte("<TITLE>VPN Portal</TITLE>"))
	}))
	defer srv.Close()
	ip, port := hostPort(t, srv.URL)
	fp := httpFingerprint(context.Background(), directDialer{timeout: 3e9}, ip, port, true)
	if fp == nil {
		t.Fatal("fp = nil")
	}
	// Case-insensitive title; the test cert carries SANs without a CN
	// (modern default) — assert the SAN capture instead.
	if fp.Title != "VPN Portal" || fp.Server != "Apache" {
		t.Errorf("fp = %+v", fp)
	}
	if fp.CertSAN == "" || !strings.Contains(fp.CertSAN, "example.com") {
		t.Errorf("cert SAN not captured: %+v", fp)
	}
}

func TestHTTPFingerprintUnreachable(t *testing.T) {
	// Closed port on loopback: nil, not an error path.
	if fp := httpFingerprint(context.Background(), directDialer{timeout: 1}, "127.0.0.1", 1, false); fp != nil {
		t.Errorf("fp = %+v, want nil", fp)
	}
}

func TestRecordDiscoveredHTTP(t *testing.T) {
	old := discoveredDirOverr
	discoveredDirOverr = filepath.Join(t.TempDir(), "discovered")
	t.Cleanup(func() { discoveredDirOverr = old })

	if err := RecordDiscoveredHTTP("10.0.0.9", 443, &HTTPFingerprint{Title: "OA", Server: "nginx", CertCN: "oa.corp"}); err != nil {
		t.Fatal(err)
	}
	hosts, _ := ListDiscoveredHosts()
	if len(hosts) != 1 || len(hosts[0].Ports) != 1 {
		t.Fatalf("hosts = %+v", hosts)
	}
	p := hosts[0].Ports[0]
	if p.Port != 443 || p.HTTP == nil || p.HTTP.Title != "OA" || p.HTTP.CertCN != "oa.corp" {
		t.Errorf("port = %+v", p)
	}
}

// hostPort splits an httptest URL. The fingerprint dialer takes ip/port.
func hostPort(t *testing.T, url string) (string, int) {
	t.Helper()
	u := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	i := strings.LastIndexByte(u, ':')
	if i < 0 {
		t.Fatalf("bad url %q", url)
	}
	port := 0
	for _, c := range u[i+1:] {
		if c < '0' || c > '9' {
			break
		}
		port = port*10 + int(c-'0')
	}
	return u[:i], port
}

// compile-time: keep tls imported for the server helper's expectations.
var _ = tls.VersionTLS12
