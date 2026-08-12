package mobilebridge

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"
)

func makeTestHello(t *testing.T, cPriv ed25519.PrivateKey, cDev, sDev string) []byte {
	t.Helper()
	eph, _ := GenerateEphemeral()
	nc, _ := Random(16)
	ch := BuildClientHello(cPriv, eph.PublicKey().Bytes(), nc, cDev, sDev, time.Now().UnixMilli())
	b, _ := json.Marshal(ch)
	return b
}

func newTestConn(t *testing.T, sPub ed25519.PublicKey, store KeyStore) *Conn {
	t.Helper()
	pairing := NewPairing("http://x", sPub, store, NewAudit("error"))
	router := NewCommandRouter("d", &recordingExec{}, PerConnPermissions{}, NewAudit("error"))
	c, err := NewConn(nil, sPub, pairing, router, NewAudit("error"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestConnRejectsUnpairedHello: a C whose key isn't in the store gets silently
// dropped at hello — never told whether S exists (enumeration protection, §5.4).
func TestConnRejectsUnpairedHello(t *testing.T) {
	sPub, _, _ := GenerateLongTerm()
	cPub, cPriv, _ := GenerateLongTerm()
	c := newTestConn(t, sPub, NewMemoryKeyStore())
	c.onDCMessage(makeTestHello(t, cPriv, DevID(cPub), DevID(sPub)))
	if hsState(c.state.Load()) != hsClosed {
		t.Fatal("unpaired hello must close the Conn")
	}
}

// TestConnRejectsRevokedHello: a previously-paired then unpaired C is revoked.
func TestConnRejectsRevokedHello(t *testing.T) {
	sPub, _, _ := GenerateLongTerm()
	store := NewMemoryKeyStore()
	cPub, cPriv, _ := GenerateLongTerm()
	cDev := DevID(cPub)
	_ = store.Set("mobilebridge.peer."+cDev+".pub", cPub)
	pairing := NewPairing("http://x", sPub, store, NewAudit("error"))
	pairing.Unpair(cDev)
	c := newTestConn(t, sPub, store)
	c.pairing = pairing
	c.onDCMessage(makeTestHello(t, cPriv, cDev, DevID(sPub)))
	if hsState(c.state.Load()) != hsClosed {
		t.Fatal("revoked hello must close the Conn")
	}
}

// TestConnRejectsBadSigHello: paired C, but a tampered signature → close.
func TestConnRejectsBadSigHello(t *testing.T) {
	sPub, _, _ := GenerateLongTerm()
	store := NewMemoryKeyStore()
	cPub, cPriv, _ := GenerateLongTerm()
	cDev := DevID(cPub)
	_ = store.Set("mobilebridge.peer."+cDev+".pub", cPub)
	c := newTestConn(t, sPub, store)
	helloBytes := makeTestHello(t, cPriv, cDev, DevID(sPub))
	var m map[string]any
	json.Unmarshal(helloBytes, &m)
	m["sig"] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // well-formed b64 but wrong
	bad, _ := json.Marshal(m)
	c.onDCMessage(bad)
	if hsState(c.state.Load()) != hsClosed {
		t.Fatal("bad-sig hello must close the Conn")
	}
}

// TestConnIgnoresFramesBeforeHandshake: a frame arriving while still in
// waitHello state is ignored (not handed to handleFrame/handleFinished).
func TestConnIgnoresUnexpectedState(t *testing.T) {
	sPub, _, _ := GenerateLongTerm()
	c := newTestConn(t, sPub, NewMemoryKeyStore())
	// random bytes in waitHello → handleHello fails to parse → close
	c.onDCMessage([]byte("not json at all"))
	if hsState(c.state.Load()) != hsClosed {
		t.Fatal("garbage in waitHello must close")
	}
}
