package linkpeersignal

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func mustKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func fixedClockStore(t *testing.T) *PairStore {
	t.Helper()
	s := NewPairStore(DefaultConfig().Pair)
	t0 := time.Unix(1700000000, 0)
	s.SetNow(func() time.Time { return t0 })
	return s
}

func TestRegisterSuccess(t *testing.T) {
	s := fixedClockStore(t)
	pub, _ := mustKey(t)
	id, err := s.Register("ABC123", "devS1", pub, fingerprint(pub))
	if err != nil || id == "" {
		t.Fatalf("register err=%v id=%q", err, id)
	}
}

func TestRegisterFpMismatch(t *testing.T) {
	s := fixedClockStore(t)
	pub, _ := mustKey(t)
	_, err := s.Register("ABC123", "devS1", pub, "WRONGFP")
	if !errors.Is(err, ErrFpMismatch) {
		t.Fatalf("want ErrFpMismatch, got %v", err)
	}
}

func TestRegisterCodeConflict(t *testing.T) {
	s := fixedClockStore(t)
	pub, _ := mustKey(t)
	pubS, fpS := pub, fingerprint(pub)
	if _, err := s.Register("DUPCODE", "devS1", pubS, fpS); err != nil {
		t.Fatal(err)
	}
	_, err := s.Register("DUPCODE", "devS2", pubS, fpS)
	if !errors.Is(err, ErrCodeConflict) {
		t.Fatalf("want ErrCodeConflict, got %v", err)
	}
}

func TestRegisterPerDevConcurrent(t *testing.T) {
	cfg := DefaultConfig().Pair
	cfg.MaxConcurrentPerDev = 2
	s := NewPairStore(cfg)
	pub, _ := mustKey(t)
	pubS, fpS := pub, fingerprint(pub)
	for i := 0; i < 2; i++ {
		if _, err := s.Register("C"+string(rune('A'+i)), "devS1", pubS, fpS); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Register("CC", "devS1", pubS, fpS); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("3rd same dev want CapacityFull, got %v", err)
	}
	// different dev still allowed
	if _, err := s.Register("CD", "devS2", pubS, fpS); err != nil {
		t.Fatalf("other dev should register: %v", err)
	}
}

func TestRegisterGlobalCap(t *testing.T) {
	cfg := DefaultConfig().Pair
	cfg.MaxGlobal = 1
	s := NewPairStore(cfg)
	pub, _ := mustKey(t)
	pubS, fpS := pub, fingerprint(pub)
	if _, err := s.Register("C1", "d1", pubS, fpS); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register("C2", "d2", pubS, fpS); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("want CapacityFull, got %v", err)
	}
}

func TestExchangeSuccess(t *testing.T) {
	s := fixedClockStore(t)
	pubS, _ := mustKey(t)
	pubC, _ := mustKey(t)
	pid, _ := s.Register("CODE1", "devS1", pubS, fingerprint(pubS))
	gotPubS, gotFpS, err := s.Exchange(pid, "CODE1", "devC1", pubC, fingerprint(pubC))
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !bytes.Equal(gotPubS, pubS) || gotFpS != fingerprint(pubS) {
		t.Fatal("returned wrong S pub/fp")
	}
}

func TestExchangePairNotFound(t *testing.T) {
	s := fixedClockStore(t)
	pubC, _ := mustKey(t)
	_, _, err := s.Exchange("nope", "X", "d", pubC, fingerprint(pubC))
	if !errors.Is(err, ErrPairNotFound) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestExchangeBruteForceLock(t *testing.T) {
	s := fixedClockStore(t)
	pubS, _ := mustKey(t)
	pubC, _ := mustKey(t)
	pid, _ := s.Register("RIGHT", "devS1", pubS, fingerprint(pubS))
	for i := 0; i < 4; i++ {
		_, _, err := s.Exchange(pid, "WRONG", "devC", pubC, fingerprint(pubC))
		if !errors.Is(err, ErrCodeMismatch) {
			t.Fatalf("attempt %d: want CodeMismatch, got %v", i, err)
		}
	}
	// 5th wrong → locked + pair deleted
	_, _, err := s.Exchange(pid, "WRONG", "devC", pubC, fingerprint(pubC))
	if !errors.Is(err, ErrPairLocked) {
		t.Fatalf("5th want Locked, got %v", err)
	}
	_, _, err = s.Exchange(pid, "RIGHT", "devC", pubC, fingerprint(pubC))
	if !errors.Is(err, ErrPairNotFound) {
		t.Fatalf("after lock want NotFound, got %v", err)
	}
}

func TestExchangeFpMismatchC(t *testing.T) {
	s := fixedClockStore(t)
	pubS, _ := mustKey(t)
	pubC, _ := mustKey(t)
	pid, _ := s.Register("CODE", "devS1", pubS, fingerprint(pubS))
	_, _, err := s.Exchange(pid, "CODE", "devC", pubC, "WRONGFP")
	if !errors.Is(err, ErrFpMismatch) {
		t.Fatalf("want FpMismatch, got %v", err)
	}
}

func TestExchangeExpired(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := NewPairStore(DefaultConfig().Pair)
	s.SetNow(func() time.Time { return now })
	pubS, _ := mustKey(t)
	pubC, _ := mustKey(t)
	pid, _ := s.Register("CODE", "devS1", pubS, fingerprint(pubS))
	s.SetNow(func() time.Time { return now.Add(2 * time.Minute) }) // TTL is 60s
	_, _, err := s.Exchange(pid, "CODE", "devC", pubC, fingerprint(pubC))
	if !errors.Is(err, ErrPairExpired) {
		t.Fatalf("want Expired, got %v", err)
	}
}

func TestConfirm(t *testing.T) {
	s := fixedClockStore(t)
	pubS, _ := mustKey(t)
	pubC, _ := mustKey(t)
	pid, _ := s.Register("CODE", "devS1", pubS, fingerprint(pubS))
	_, _, _ = s.Exchange(pid, "CODE", "devC", pubC, fingerprint(pubC))
	if err := s.Confirm(pid); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	p, ok := s.Get(pid)
	if !ok || !p.Confirmed {
		t.Fatal("pair should be confirmed")
	}
	if err := s.Confirm("nope"); !errors.Is(err, ErrPairNotFound) {
		t.Fatalf("missing want NotFound, got %v", err)
	}
}

func TestSweepExpired(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := NewPairStore(DefaultConfig().Pair)
	s.SetNow(func() time.Time { return now })
	pub, _ := mustKey(t)
	pid, _ := s.Register("CODE", "devS1", pub, fingerprint(pub))
	s.SetNow(func() time.Time { return now.Add(time.Hour) })
	s.Sweep()
	if _, ok := s.Get(pid); ok {
		t.Fatal("sweep should remove expired pair")
	}
}

func TestDelete(t *testing.T) {
	s := fixedClockStore(t)
	pub, _ := mustKey(t)
	pid, _ := s.Register("CODE", "devS1", pub, fingerprint(pub))
	s.Delete(pid)
	if _, ok := s.Get(pid); ok {
		t.Fatal("Delete should remove pair")
	}
}

func TestGenCode(t *testing.T) {
	s := fixedClockStore(t)
	c, err := s.GenCode()
	if err != nil || len(c) != 6 {
		t.Fatalf("GenCode err=%v len=%d", err, len(c))
	}
}
