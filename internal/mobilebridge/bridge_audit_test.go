package mobilebridge

import (
	"errors"
	"testing"
)

func TestAuditAllMethods(t *testing.T) {
	a := NewAudit("info")
	a.PairStart("devXXXXXXXXXX")
	a.PairConfirmed("devXXXXXXXXXX", "devSXXXXXXXXXXX")
	a.Unpaired("devXXXXXXXXXX")
	a.ConnOpen("devXXXXXXXXXX", "host")
	a.ConnClose("devXXXXXXXXXX")
	a.Cmd("devXXXXXXXXXX", "submit", "tab", true)
	a.Denied("devXXXXXXXXXX", "office_run", "high_risk")
	a.Error("evt", "devXXXXXXXXXX", errors.New("boom"))
}

func TestAuditTruncDev(t *testing.T) {
	if got := truncDev("1234567890ABCDEF"); got != "1234...DEF" {
		t.Fatalf("truncDev: %q", got)
	}
	if got := truncDev("short"); got != "short" {
		t.Fatalf("short devId should pass through: %q", got)
	}
}

func TestBridgeUnpairRemovesAndRevokes(t *testing.T) {
	sPub, sPriv, _ := GenerateLongTerm()
	store := NewMemoryKeyStore()
	cPub, _, _ := GenerateLongTerm()
	cDev := DevID(cPub)
	_ = store.Set("mobilebridge.peer."+cDev+".pub", cPub)
	bridge := NewBridge(DefaultConfig(), sPriv, sPub, store, &recordingExec{}, NewAudit("error"))
	bridge.Unpair(cDev)
	if _, err := store.Get("mobilebridge.peer." + cDev + ".pub"); err == nil {
		t.Fatal("peer pub should be deleted after unpair")
	}
	if !bridge.pairing.IsRevoked(cDev) {
		t.Fatal("device should be revoked after unpair")
	}
}

func TestBridgeForwardEventNoOpWithoutSubs(t *testing.T) {
	sPub, sPriv, _ := GenerateLongTerm()
	bridge := NewBridge(DefaultConfig(), sPriv, sPub, NewMemoryKeyStore(), &recordingExec{}, NewAudit("error"))
	// no subscriptions → ForwardEvent must not panic
	bridge.ForwardEvent("tabNone", []byte(`{"kind":"text","text":"hi"}`))
	// subscription registered but conn unknown/not encrypted → also safe
	bridge.setSubscription("ghostConn", "tab1")
	bridge.ForwardEvent("tab1", []byte(`{"kind":"text"}`))
	bridge.ForwardEvent("otherTab", []byte(`{"kind":"text"}`))
}

func TestBridgePairingLifecycle(t *testing.T) {
	sPub, sPriv, _ := GenerateLongTerm()
	bridge := NewBridge(DefaultConfig(), sPriv, sPub, NewMemoryKeyStore(), &recordingExec{}, NewAudit("error"))
	// Pending/Reject on empty state don't blow up
	if pp := bridge.PendingPairings(); len(pp) != 0 {
		t.Fatal("expected no pending")
	}
	bridge.RejectPairing("nonexistent")
	// inject a fake pending via pairing.OnExchange then Reject
	cPub, _, _ := GenerateLongTerm()
	bridge.pairing.OnExchange(SignalMsg{
		Type: "pair_exchange", PairID: "p1",
		DevC: DevID(cPub), PubC: b64(cPub), FpC: Fingerprint(cPub),
	})
	if len(bridge.PendingPairings()) != 1 {
		t.Fatal("expected 1 pending")
	}
	bridge.RejectPairing("p1")
	if len(bridge.PendingPairings()) != 0 {
		t.Fatal("expected 0 pending after reject")
	}
}

func TestBridgeToICEServers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TURNEnabled = true
	cfg.TURNServers = []string{"turn:example.com:5349"}
	got := toICEServers(cfg)
	if len(got) < 2 {
		t.Fatalf("expected STUN+TURN, got %d", len(got))
	}
}
