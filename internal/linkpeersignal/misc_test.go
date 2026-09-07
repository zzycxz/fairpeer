package linkpeersignal

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pair.CodeTTL != 60 || cfg.Pair.MaxGlobal != 50000 {
		t.Fatalf("defaults wrong: %+v", cfg.Pair)
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "signal.toml")
	content := []byte(`
[server]
listen = "0.0.0.0:9090"
[pair]
code_ttl = 120
[log]
level = "debug"
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "0.0.0.0:9090" || cfg.Pair.CodeTTL != 120 || cfg.Log.Level != "debug" {
		t.Fatalf("config not loaded: %+v", cfg)
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(path, []byte("[pair]\ncode_ttl = 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("invalid config should fail validate")
	}
}

func TestAuditAllMethods(t *testing.T) {
	a := NewAudit("info")
	a.Info("msg")
	a.Warn("msg")
	a.PairRegister("devXXXXXXXXXX", "1.2.3.4")
	a.PairExchange("devXXXXXXXXXX", "1.2.3.4", true)
	a.PairExchange("devXXXXXXXXXX", "1.2.3.4", false)
	a.WSConnect("devXXXXXXXXXX", "1.2.3.4")
	a.WSDisconnect("devXXXXXXXXXX")
	a.RateLimit("ip", "1.2.3.4")
	a.Error("evt", "devXXXXXXXXXX", errors.New("boom"))
}

func TestServerSweepNoPanic(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Sweep()
}

func TestRealIPFromForwarded(t *testing.T) {
	// Trusted proxy (loopback): take the RIGHTMOST entry — the one our own
	// proxy appended. A client-supplied spoofed leftmost entry must not be
	// able to pick its rate-limit bucket.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 10.0.0.1")
	if got := realIP(r); got != "10.0.0.1" {
		t.Fatalf("want rightmost 10.0.0.1, got %s", got)
	}

	// Untrusted direct peer (public address): XFF is ignored entirely.
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "203.0.113.7:5555"
	r2.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := realIP(r2); got != "203.0.113.7" {
		t.Fatalf("want remote addr 203.0.113.7 for untrusted peer, got %s", got)
	}

	// Public 172.x is NOT an RFC1918 address — must not be treated as a
	// trusted proxy (the old string-prefix check trusted all of 172/8).
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.RemoteAddr = "172.32.0.5:5555"
	r3.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := realIP(r3); got != "172.32.0.5" {
		t.Fatalf("want remote addr 172.32.0.5 for public-172 peer, got %s", got)
	}

	// Private 172.16-31.x IS trusted (docker network).
	r4 := httptest.NewRequest("GET", "/", nil)
	r4.RemoteAddr = "172.17.0.2:5555"
	r4.Header.Set("X-Forwarded-For", "8.8.8.8")
	if got := realIP(r4); got != "8.8.8.8" {
		t.Fatalf("want appended 8.8.8.8 via trusted docker proxy, got %s", got)
	}
}

func TestRealIPRemoteAddr(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "7.7.7.7:1234"
	if got := realIP(r); got != "7.7.7.7" {
		t.Fatalf("want 7.7.7.7 got %s", got)
	}
}

func TestHTTPRegisterCodeConflict(t *testing.T) {
	_, ts := newTestServer(t)
	pub, _ := mustKey(t)
	body := jsonMarshal(map[string]string{"code": "DUP", "devS": "d1", "pubS": b64(pub), "fpS": fingerprint(pub)})
	resp, err := http.Post(ts.URL+"/pair/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("first register should succeed, got %d", resp.StatusCode)
	}
	resp2, err := http.Post(ts.URL+"/pair/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 409 {
		t.Fatalf("want 409 conflict, got %d", resp2.StatusCode)
	}
}

// jsonMarshal is a tiny helper to keep call sites short.
func jsonMarshal(m map[string]string) []byte {
	b, _ := json.Marshal(m)
	return b
}
