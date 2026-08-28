package mobilebridge

import (
	"errors"
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
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
	cfg.TURNUser = "u"
	cfg.TURNPass = "p"
	// 本地路径（offer 经嵌入式 K 到达）：纯 host candidate，零云
	if got := toICEServers(cfg, false); got != nil {
		t.Fatalf("expected nil ICE servers for LAN link, got %v", got)
	}
	// 云路径：STUN + TURN（带凭据）
	got := toICEServers(cfg, true)
	if len(got) < 2 {
		t.Fatalf("expected STUN+TURN, got %d", len(got))
	}
	var turn webrtc.ICEServer
	for _, s := range got {
		for _, u := range s.URLs {
			if strings.HasPrefix(u, "turn:") {
				turn = s
			}
		}
	}
	if turn.Username != "u" || turn.Credential != "p" {
		t.Fatalf("expected TURN credentials, got %q/%v", turn.Username, turn.Credential)
	}
}

// TestApplyKnockDefault（UX_ONBOARDING W4）：敲门开启且未填 STUN 时取云 K
// 域名拼 coturn；已填/无云 K/未开启均保持原样。
func TestApplyKnockDefault(t *testing.T) {
	base := Config{UDPKnock: true, CloudSignalURL: "https://signal.example.com"}
	if got := ApplyKnockDefault(base); got.KnockServer != "stun:signal.example.com:3478" {
		t.Fatalf("want derived knock server, got %q", got.KnockServer)
	}
	if got := ApplyKnockDefault(Config{UDPKnock: false, CloudSignalURL: "https://x.com"}); got.KnockServer != "" {
		t.Fatalf("knock off should not derive, got %q", got.KnockServer)
	}
	if got := ApplyKnockDefault(Config{UDPKnock: true, KnockServer: "stun:a:1"}); got.KnockServer != "stun:a:1" {
		t.Fatalf("explicit server must win, got %q", got.KnockServer)
	}
	if got := ApplyKnockDefault(Config{UDPKnock: true}); got.KnockServer != "" {
		t.Fatalf("no cloud URL should stay empty, got %q", got.KnockServer)
	}
}

// TestParseTurnCred（UX_ONBOARDING W3）：从 turn-cred.sh 输出片段提取凭据。
func TestParseTurnCred(t *testing.T) {
	u, p, h, port, ok := ParseTurnCred("turn_user        = \"1893456000\"\nturn_pass        = \"ULFtTEb7+k63cHTlVfN6QlJyZUQ=\"")
	if ok || h != "" {
		t.Fatalf("toml lines without host should not parse, got %v %q", ok, h)
	}
	// 完整凭据串（在任意粘贴文本中）
	u, p, h, port, ok = ParseTurnCred(`粘贴以下内容：
[mobilebridge]
turn_user = "u1"
turn_pass = "p1"
1893456000:p1@turn.example.com:3478 ← 用这个`)
	if !ok || u != "1893456000" || p != "p1" || h != "turn.example.com" || port != 3478 {
		t.Fatalf("want 1893456000/p1/turn.example.com/3478, got %q %q %q %d %v", u, p, h, port, ok)
	}
	// 默认端口
	_, _, h, port, ok = ParseTurnCred("u:p@turn.example.com")
	if !ok || h != "turn.example.com" || port != 3478 {
		t.Fatalf("default port 3478 expected, got %q %d %v", h, port, ok)
	}
	// 坏输入
	for _, bad := range []string{"", "nopassword@", "@host", "a:b@", "u:p@"} {
		if _, _, _, _, ok := ParseTurnCred(bad); ok {
			t.Fatalf("bad input %q should not parse", bad)
		}
	}
}
