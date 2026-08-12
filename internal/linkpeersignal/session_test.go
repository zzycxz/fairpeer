package linkpeersignal

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func freshAuth(t *testing.T) (pub []byte, priv ed25519.PrivateKey, dev, ts, pubB64, sigB64 string) {
	t.Helper()
	pub, priv, _ = ed25519.GenerateKey(rand.Reader)
	dev = devID([]byte(pub))
	ts = strconv.FormatInt(time.Now().Unix(), 10)
	sig := ed25519.Sign(priv, []byte(dev+ts))
	// WS auth uses URL-safe base64 (PROTOCOL §4.1): query strings corrupt on '+'.
	uEnc := base64.URLEncoding.WithPadding(base64.NoPadding)
	pubB64 = uEnc.EncodeToString(pub)
	sigB64 = uEnc.EncodeToString(sig)
	return
}

func TestVerifyAuthSuccess(t *testing.T) {
	_, _, dev, ts, pubB64, sigB64 := freshAuth(t)
	if err := verifyAuth(dev, ts, pubB64, sigB64, 60*time.Second); err != nil {
		t.Fatalf("should pass: %v", err)
	}
}

func TestVerifyAuthBadDevID(t *testing.T) {
	_, priv, _, ts, pubB64, _ := freshAuth(t)
	// sign over a wrong devId, claim a different one
	wrong := "WRONGDEVID"
	sig := ed25519.Sign(priv, []byte(wrong+ts))
	uEnc := base64.URLEncoding.WithPadding(base64.NoPadding)
	err := verifyAuth(wrong, ts, pubB64, uEnc.EncodeToString(sig), 60*time.Second)
	if err == nil {
		t.Fatal("bad devId (not matching pub) should fail")
	}
}

func TestVerifyAuthBadSig(t *testing.T) {
	pub, _, dev, ts, pubB64, _ := freshAuth(t)
	_ = pub
	uEnc := base64.URLEncoding.WithPadding(base64.NoPadding)
	badSig := make([]byte, 64)
	err := verifyAuth(dev, ts, pubB64, uEnc.EncodeToString(badSig), 60*time.Second)
	if err == nil {
		t.Fatal("bad sig should fail")
	}
}

func TestVerifyAuthStaleTS(t *testing.T) {
	_, priv, dev, _, pubB64, _ := freshAuth(t)
	oldTS := "1000000000" // 2001 — far outside the skew window
	sig := ed25519.Sign(priv, []byte(dev+oldTS))
	uEnc := base64.URLEncoding.WithPadding(base64.NoPadding)
	err := verifyAuth(dev, oldTS, pubB64, uEnc.EncodeToString(sig), 60*time.Second)
	if err == nil {
		t.Fatal("stale ts should fail")
	}
}

func TestVerifyAuthBadPubLen(t *testing.T) {
	uEnc := base64.URLEncoding.WithPadding(base64.NoPadding)
	err := verifyAuth("x", "1", uEnc.EncodeToString([]byte("short")), "sig", 60*time.Second)
	if err == nil {
		t.Fatal("bad pub length should fail")
	}
}

func TestVerifyAuthCorruptBase64(t *testing.T) {
	err := verifyAuth("x", "1", "!!!notb64!!!", "sig", 60*time.Second)
	if err == nil {
		t.Fatal("corrupt pub b64 should fail")
	}
}

func TestRouteKeepaliveLocal(t *testing.T) {
	s := NewSessionStore(DefaultConfig().Session)
	from := &PeerConn{}
	raw, _ := json.Marshal(map[string]string{"type": "kp"})
	mt, delivered, err := s.Route(from, raw)
	if err != nil || !delivered || mt != "kp" {
		t.Fatalf("kp route: mt=%s delivered=%v err=%v", mt, delivered, err)
	}
}

func TestRouteMissingTo(t *testing.T) {
	s := NewSessionStore(DefaultConfig().Session)
	from := &PeerConn{}
	raw, _ := json.Marshal(map[string]string{"type": "offer"}) // no "to"
	_, _, err := s.Route(from, raw)
	if err == nil {
		t.Fatal("missing to should error")
	}
}

func TestSweepIdle(t *testing.T) {
	s := NewSessionStore(DefaultConfig().Session)
	pc := &PeerConn{DevID: "ghost"}
	pc.lastSeen.Store(time.Now().Add(-time.Hour).UnixMilli())
	s.peers["ghost"] = pc
	s.SweepIdle(time.Minute)
	if _, ok := s.peers["ghost"]; ok {
		t.Fatal("idle peer should be reaped")
	}
}

func TestOnlineCount(t *testing.T) {
	s := NewSessionStore(DefaultConfig().Session)
	if s.OnlineCount() != 0 {
		t.Fatal("expected 0 online")
	}
	s.Register(&PeerConn{DevID: "a"})
	if s.OnlineCount() != 1 {
		t.Fatal("expected 1 online")
	}
	s.Unregister("a")
	if s.OnlineCount() != 0 {
		t.Fatal("expected 0 after unregister")
	}
}
