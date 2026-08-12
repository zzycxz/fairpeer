package linkpeersignal

import "testing"

func TestDevIDDeterministic(t *testing.T) {
	pub := make([]byte, 32)
	a, b := devID(pub), devID(pub)
	if a != b {
		t.Fatal("devID not deterministic")
	}
	if len(a) != 16 { // 10 bytes = 80 bits → 16 base32 chars
		t.Fatalf("devID len = %d, want 16", len(a))
	}
}

func TestFingerprintDeterministic(t *testing.T) {
	pub := make([]byte, 32)
	if fingerprint(pub) != fingerprint(pub) {
		t.Fatal("fingerprint not deterministic")
	}
}

func TestDevIDDistinct(t *testing.T) {
	a := make([]byte, 32)
	b := make([]byte, 32)
	b[0] = 1
	if devID(a) == devID(b) {
		t.Fatal("distinct pubs must give distinct devIds")
	}
}
