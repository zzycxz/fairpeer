package mobilebridge

import "testing"

func TestSignalClientSendWithoutConn(t *testing.T) {
	pub, priv, _ := GenerateLongTerm()
	sc := NewSignalClient("http://x", pub, priv, nil, NewAudit("error"))
	if err := sc.Send(SignalMsg{Type: "x"}); err == nil {
		t.Fatal("Send without a connection should error")
	}
}

func TestSignalClientCloseIdle(t *testing.T) {
	pub, priv, _ := GenerateLongTerm()
	sc := NewSignalClient("http://x", pub, priv, nil, NewAudit("error"))
	sc.Close() // no active conn — must not panic
	sc.Close() // double-close safe
}

func TestCryptoErrorPaths(t *testing.T) {
	eph, _ := GenerateEphemeral()
	if _, err := ECDHShared(eph, []byte("short")); err == nil {
		t.Fatal("ECDHShared with bad-length pub should fail")
	}
	if _, err := NewAEAD([]byte("only-16-bytes!!!!")); err == nil {
		t.Fatal("NewAEAD with non-32B key should fail")
	}
}

func TestPairingConfirmNoPending(t *testing.T) {
	pub, _, _ := GenerateLongTerm()
	p := NewPairing("http://x", pub, NewMemoryKeyStore(), NewAudit("error"))
	if err := p.Confirm("nonexistent"); err != ErrNoPending {
		t.Fatalf("want ErrNoPending, got %v", err)
	}
}

func TestPairingRevokedEmpty(t *testing.T) {
	pub, _, _ := GenerateLongTerm()
	p := NewPairing("http://x", pub, NewMemoryKeyStore(), NewAudit("error"))
	if p.IsRevoked("anyone") {
		t.Fatal("empty store should report not-revoked")
	}
	if _, ok := p.PeerPub("nobody"); ok {
		t.Fatal("unpaired dev should not have a pub")
	}
}

func TestConnInitialState(t *testing.T) {
	sPub, _, _ := GenerateLongTerm()
	pairing := NewPairing("http://x", sPub, NewMemoryKeyStore(), NewAudit("error"))
	router := NewCommandRouter("d", &recordingExec{}, PerConnPermissions{}, NewAudit("error"))
	c, err := NewConn(nil, sPub, pairing, router, NewAudit("error"))
	if err != nil {
		t.Fatal(err)
	}
	if c.IsEncrypted() {
		t.Fatal("fresh Conn must not be encrypted")
	}
	if c.DevC() != "" {
		t.Fatal("DevC must be empty before hello")
	}
	c.close() // no pc/dc — must be safe
	c.close() // double-close idempotent
}

func TestConfigDefaults(t *testing.T) {
	c := DefaultConfig()
	if !c.UPnP || !c.AllowFileDrop || c.MaxConnections != 4 {
		t.Fatalf("defaults wrong: %+v", c)
	}
	p := c.DefaultPermissions()
	if p.ReadOnly || p.AllowHighRisk {
		t.Fatal("permission defaults should be safe")
	}
}

func TestKeystoreRoundTrip(t *testing.T) {
	s := NewMemoryKeyStore()
	if _, err := s.Get("missing"); err == nil {
		t.Fatal("missing key should error")
	}
	_ = s.Set("k", []byte("v"))
	got, err := s.Get("k")
	if err != nil || string(got) != "v" {
		t.Fatalf("round-trip: %v %q", err, got)
	}
	s.Delete("k")
	if _, err := s.Get("k"); err == nil {
		t.Fatal("deleted key should error")
	}
}
