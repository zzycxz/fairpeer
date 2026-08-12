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
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 10.0.0.1")
	if got := realIP(r); got != "9.9.9.9" {
		t.Fatalf("want 9.9.9.9 got %s", got)
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
