package mobilebridge

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

func TestDevIDFingerprintDeterministic(t *testing.T) {
	pub := bytes.Repeat([]byte{0xAB}, 32)
	if DevID(pub) != DevID(pub) {
		t.Fatal("DevID not deterministic")
	}
	if Fingerprint(pub) != Fingerprint(pub) {
		t.Fatal("Fingerprint not deterministic")
	}
	// Crockford base32: 10 bytes → 16 chars; 8 bytes → 13 chars
	if len(DevID(pub)) != 16 {
		t.Fatalf("DevID len = %d, want 16", len(DevID(pub)))
	}
}

func TestGenerateLongTermAndSign(t *testing.T) {
	pub, priv, err := GenerateLongTerm()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("hello linkpeer")
	sig := ed25519.Sign(priv, msg)
	if !ed25519.Verify(pub, msg, sig) {
		t.Fatal("ed25519 round-trip failed")
	}
	// DevID of the pub should be a stable identifier
	id := DevID(pub)
	if len(id) != 16 {
		t.Fatalf("devId len %d", len(id))
	}
}

func TestECDHAndKeyDerivationSymmetric(t *testing.T) {
	// Both sides derive the SAME c2s/s2c from the shared secret.
	nc, _ := Random(16)
	ns, _ := Random(16)
	aliceEph, _ := GenerateEphemeral()
	bobEph, _ := GenerateEphemeral()
	aliceShared, err := ECDHShared(aliceEph, bobEph.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	bobShared, err := ECDHShared(bobEph, aliceEph.PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !ConstantTimeEqual(aliceShared, bobShared) {
		t.Fatal("ECDH shared secrets differ")
	}
	aC2S, aS2C := DeriveSessionKeys(aliceShared, nc, ns)
	bC2S, bS2C := DeriveSessionKeys(bobShared, nc, ns)
	if !ConstantTimeEqual(aC2S, bC2S) || !ConstantTimeEqual(aS2C, bS2C) {
		t.Fatal("derived keys differ across sides")
	}
	// direction split: c2s != s2c
	if ConstantTimeEqual(aC2S, aS2C) {
		t.Fatal("c2s must differ from s2c (reflection protection)")
	}
}

func TestAEADRoundTrip(t *testing.T) {
	key, _ := Random(32)
	aead, err := NewAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce, _ := Random(12)
	pt := []byte(`{"t":"submit","input":"hi"}`)
	sealed := aead.Seal(nil, nonce, pt, nil)
	got, err := aead.Open(nil, nonce, sealed, nil)
	if err != nil || !ConstantTimeEqual(got, pt) {
		t.Fatalf("AEAD round-trip failed: %v", err)
	}
	// tamper with ciphertext → must fail
	sealed[0] ^= 1
	if _, err := aead.Open(nil, nonce, sealed, nil); err == nil {
		t.Fatal("tampered ciphertext must fail auth")
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !ConstantTimeEqual([]byte("abc"), []byte("abc")) {
		t.Fatal("equal bytes not equal")
	}
	if ConstantTimeEqual([]byte("abc"), []byte("abd")) {
		t.Fatal("unequal bytes reported equal")
	}
}
