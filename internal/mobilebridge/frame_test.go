package mobilebridge

import (
	"bytes"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	key, _ := Random(32)
	aead, _ := NewAEAD(key)
	nonce, _ := Random(12)
	pt := []byte(`{"t":"ping","ts":12345}`)
	frame := SealFrame(aead, 42, nonce, pt)
	seq, got, err := OpenFrame(aead, frame)
	if err != nil || seq != 42 || !bytes.Equal(got, pt) {
		t.Fatalf("round-trip: seq=%d err=%v", seq, err)
	}
}

func TestFrameTamperRejected(t *testing.T) {
	key, _ := Random(32)
	aead, _ := NewAEAD(key)
	nonce, _ := Random(12)
	frame := SealFrame(aead, 1, nonce, []byte("payload"))
	frame[len(frame)-1] ^= 1 // flip a tag bit
	if _, _, err := OpenFrame(aead, frame); err == nil {
		t.Fatal("tampered tag must fail")
	}
}

func TestFrameHeaderTamperRejected(t *testing.T) {
	key, _ := Random(32)
	aead, _ := NewAEAD(key)
	nonce, _ := Random(12)
	frame := SealFrame(aead, 7, nonce, []byte("x"))
	frame[5] ^= 1 // flip a seq byte (part of AAD)
	seq, _, err := OpenFrame(aead, frame)
	if err == nil {
		t.Fatalf("tampered header must fail; got seq=%d", seq)
	}
}

func TestFrameShort(t *testing.T) {
	key, _ := Random(32)
	aead, _ := NewAEAD(key)
	if _, _, err := OpenFrame(aead, []byte{1, 2, 3}); err != ErrShortFrame {
		t.Fatalf("want ErrShortFrame, got %v", err)
	}
}

func TestFrameBadVersion(t *testing.T) {
	key, _ := Random(32)
	aead, _ := NewAEAD(key)
	nonce, _ := Random(12)
	frame := SealFrame(aead, 1, nonce, []byte("x"))
	frame[0] = 99 // bad version
	if _, _, err := OpenFrame(aead, frame); err != ErrBadVersion {
		t.Fatalf("want ErrBadVersion, got %v", err)
	}
}

func TestFrameSeqIsLittleEndian(t *testing.T) {
	key, _ := Random(32)
	aead, _ := NewAEAD(key)
	nonce, _ := Random(12)
	frame := SealFrame(aead, 0x0102030405060708, nonce, []byte("x"))
	seq, _, _ := OpenFrame(aead, frame)
	if seq != 0x0102030405060708 {
		t.Fatalf("seq round-trip wrong: %d", seq)
	}
}
