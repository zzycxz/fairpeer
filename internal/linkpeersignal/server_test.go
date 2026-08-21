package linkpeersignal

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Test helpers for WS authentication.
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) } // for JSON bodies
// b64u is URL-safe base64 for URL query params (WS auth).
func b64u(b []byte) string { return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(b) }
func signStr(priv ed25519.PrivateKey, s string) []byte { return ed25519.Sign(priv, []byte(s)) }

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	srv := NewServer(DefaultConfig(), NewAudit("error"))
	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestHTTPHealthz(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var h struct {
		OK     bool `json:"ok"`
		Online int  `json:"online"`
	}
	json.NewDecoder(resp.Body).Decode(&h)
	resp.Body.Close()
	if !h.OK {
		t.Fatal("healthz not ok")
	}
}

func TestHTTPMetrics(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTPRegisterBadFp(t *testing.T) {
	_, ts := newTestServer(t)
	pubS, _ := mustKey(t)
	body, _ := json.Marshal(map[string]string{"code": "C", "devS": "d", "pubS": b64(pubS), "fpS": "WRONG"})
	resp, err := http.Post(ts.URL+"/pair/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("want 400 fp_mismatch, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHTTPRegisterExchangeConfirmFlow(t *testing.T) {
	_, ts := newTestServer(t)
	pubS, _ := mustKey(t)
	pubC, _ := mustKey(t)
	pubSStr, fpS := b64(pubS), fingerprint(pubS)
	pubCStr, fpC := b64(pubC), fingerprint(pubC)

	// register
	regBody, _ := json.Marshal(map[string]string{"code": "CODE1", "devS": "dS", "pubS": pubSStr, "fpS": fpS})
	resp, err := http.Post(ts.URL+"/pair/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		PairID    string `json:"pairId"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	if reg.PairID == "" {
		t.Fatal("no pairId")
	}

	// exchange
	exBody, _ := json.Marshal(map[string]string{"pairId": reg.PairID, "code": "CODE1", "devC": "dC", "pubC": pubCStr, "fpC": fpC})
	resp, err = http.Post(ts.URL+"/pair/exchange", "application/json", bytes.NewReader(exBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("exchange status %d", resp.StatusCode)
	}
	var ex struct{ PubS, FpS string }
	json.NewDecoder(resp.Body).Decode(&ex)
	resp.Body.Close()
	if ex.PubS != pubSStr {
		t.Fatal("wrong pubS returned")
	}

	// confirm returns C's keys to S
	cfBody, _ := json.Marshal(map[string]string{"pairId": reg.PairID, "devS": "dS"})
	resp, err = http.Post(ts.URL+"/pair/confirm", "application/json", bytes.NewReader(cfBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("confirm status %d", resp.StatusCode)
	}
	var cf struct{ PubC, DevC string }
	json.NewDecoder(resp.Body).Decode(&cf)
	resp.Body.Close()
	if cf.PubC != pubCStr || cf.DevC != "dC" {
		t.Fatal("confirm returned wrong C keys")
	}
}

func TestHTTPExchangeWrongCode(t *testing.T) {
	srv, ts := newTestServer(t)
	pubS, _ := mustKey(t)
	pid, _ := srv.pairs.Register("RIGHT", "dS", pubS, fingerprint(pubS))
	pubC, _ := mustKey(t)
	body, _ := json.Marshal(map[string]string{"pairId": pid, "code": "WRONG", "devC": "dC", "pubC": b64(pubC), "fpC": fingerprint(pubC)})
	resp, err := http.Post(ts.URL+"/pair/exchange", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestWSAuthRejected(t *testing.T) {
	_, ts := newTestServer(t)
	// no/invalid auth query → 401 before WS upgrade
	resp, err := http.Get(ts.URL + "/session/ws")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestWSRoutingE2E(t *testing.T) {
	_, ts := newTestServer(t)
	wsURL := "ws" + ts.URL[len("http"):]

	pubA, privA := mustKey(t)
	devA := devID([]byte(pubA))
	pubB, privB := mustKey(t)
	devB := devID([]byte(pubB))

	dial := func(pub ed25519.PublicKey, priv ed25519.PrivateKey, dev string) (*websocket.Conn, error) {
		tsStr := strconv.FormatInt(time.Now().Unix(), 10)
		sig := signStr(priv, dev+tsStr)
		u := wsURL + "/session/ws?dev=" + dev + "&ts=" + tsStr +
			"&pub=" + b64u(pub) + "&sig=" + b64u(sig)
		c, _, err := websocket.DefaultDialer.Dial(u, nil)
		return c, err
	}
	connA, err := dial(pubA, privA, devA)
	if err != nil {
		t.Fatal(err)
	}
	defer connA.Close()
	connB, err := dial(pubB, privB, devB)
	if err != nil {
		t.Fatal(err)
	}
	defer connB.Close()

	// A → B
	msg, _ := json.Marshal(map[string]string{"type": "offer", "to": devB, "from": devA})
	if err := connA.WriteMessage(websocket.TextMessage, msg); err != nil {
		t.Fatal(err)
	}
	connB.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := connB.ReadMessage()
	if err != nil {
		t.Fatalf("B did not receive: %v", err)
	}
	var got struct{ Type string }
	json.Unmarshal(raw, &got)
	if got.Type != "offer" {
		t.Fatalf("B got wrong msg type: %s", raw)
	}
}

func TestWSRoutingOfflinePeer(t *testing.T) {
	_, ts := newTestServer(t)
	wsURL := "ws" + ts.URL[len("http"):]

	pubA, privA := mustKey(t)
	devA := devID([]byte(pubA))
	tsStr := strconv.FormatInt(time.Now().Unix(), 10)
	sig := signStr(privA, devA+tsStr)
	u := wsURL + "/session/ws?dev=" + devA + "&ts=" + tsStr + "&pub=" + b64u(pubA) + "&sig=" + b64u(sig)
	connA, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connA.Close()

	// A → offline dev → should get "unavailable" back
	msg, _ := json.Marshal(map[string]string{"type": "offer", "to": "GHOST", "from": devA})
	connA.WriteMessage(websocket.TextMessage, msg)
	connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := connA.ReadMessage()
	if err != nil {
		t.Fatalf("expected unavailable reply: %v", err)
	}
	var got struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}
	json.Unmarshal(raw, &got)
	if got.Type != "unavailable" {
		t.Fatalf("want unavailable, got %s", raw)
	}
}

// TestHTTPRegisterWithExplicitPairID：双 K 同码注册——S 自带 pairId 注册
// （本地 K + 云 K 同 pid/code），K 必须按 S 给的 pid 建档，且同一 pid 在
// 两台 K 上各自独立 exchange/confirm。坏 pid（非法字符/过短）拒绝。
func TestHTTPRegisterWithExplicitPairID(t *testing.T) {
	pubS, _ := mustKey(t)
	pubC, _ := mustKey(t)
	pubSStr, fpS := b64(pubS), fingerprint(pubS)
	pubCStr, fpC := b64(pubC), fingerprint(pubC)
	const pid = "0123456789ABCDEFG" // Crockford b32 合法（含 0/1/8/9）

	tryRegister := func(ts *httptest.Server, pairID string) int {
		body, _ := json.Marshal(map[string]string{
			"pairId": pairID, "code": "CODE9", "devS": "dS", "pubS": pubSStr, "fpS": fpS,
		})
		resp, err := http.Post(ts.URL+"/pair/register", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var reg struct {
			PairID string `json:"pairId"`
		}
		json.NewDecoder(resp.Body).Decode(&reg)
		if resp.StatusCode == 200 && pairID != "" && reg.PairID != pairID {
			t.Fatalf("K 没有用 S 提供的 pairId：want %s got %s", pairID, reg.PairID)
		}
		return resp.StatusCode
	}

	// 两台独立 K，同一 pid+code 各自注册成功（模拟本地 K + 云 K）
	srvA, tsA := newTestServer(t)
	_ = srvA
	_, tsB := newTestServer(t)
	if got := tryRegister(tsA, pid); got != 200 {
		t.Fatalf("K-A register status %d", got)
	}
	if got := tryRegister(tsB, pid); got != 200 {
		t.Fatalf("K-B register status %d", got)
	}
	// 同一台 K 重复注册同 pid → pair_id_conflict
	if got := tryRegister(tsA, pid); got != 409 {
		t.Fatalf("want 409 pair_id_conflict, got %d", got)
	}
	// 非法 pid：小写/特殊字符/过短 → 400 bad_pair_id
	for _, bad := range []string{"abcdefg", "ABC!DEF", "SHORT"} {
		if got := tryRegister(tsA, bad); got != 400 {
			t.Fatalf("bad pid %q: want 400, got %d", bad, got)
		}
	}

	// K-B 上 exchange + confirm 用同一 pid 正常走通（手机经云 K 配对的路径）
	exBody, _ := json.Marshal(map[string]string{"pairId": pid, "code": "CODE9", "devC": "dC", "pubC": pubCStr, "fpC": fpC})
	resp, err := http.Post(tsB.URL+"/pair/exchange", "application/json", bytes.NewReader(exBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("exchange status %d", resp.StatusCode)
	}
	resp.Body.Close()
	cfBody, _ := json.Marshal(map[string]string{"pairId": pid, "devS": "dS"})
	resp, err = http.Post(tsB.URL+"/pair/confirm", "application/json", bytes.NewReader(cfBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("confirm status %d", resp.StatusCode)
	}
	resp.Body.Close()
}
