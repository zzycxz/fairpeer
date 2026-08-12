package mobilebridge

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestHandshakeFullFlow runs the complete C↔S handshake end-to-end and proves
// BOTH sides derive identical session keys (the property M1's Conn depends on).
// This is the protocol-correctness gate from LINKPEER_VERIFICATION_PLAN §3.
func TestHandshakeFullFlow(t *testing.T) {
	// long-term identity keys
	cPub, cPriv, _ := GenerateLongTerm()
	sPub, sPriv, _ := GenerateLongTerm()
	cid, sid := DevID(cPub), DevID(sPub)

	// ephemeral keys (one side each, kept private)
	cEph, _ := GenerateEphemeral()
	sEph, _ := GenerateEphemeral()
	nc, _ := Random(16)
	ns, _ := Random(16)
	ts := time.Now().UnixMilli()

	// C builds + S verifies ClientHello
	ch := BuildClientHello(cPriv, cEph.PublicKey().Bytes(), nc, cid, sid, ts)
	if err := VerifyClientHello(sPub, ch); err != nil {
		// S uses C's public key to verify, not its own
	}
	if err := VerifyClientHello(cPub, ch); err != nil {
		t.Fatalf("verify ClientHello: %v", err)
	}

	// S builds + C verifies ServerHello
	sh := BuildServerHello(sPriv, sEph.PublicKey().Bytes(), ns, cid, sid, ts)
	if err := VerifyServerHello(sPub, sh); err != nil {
		t.Fatalf("verify ServerHello: %v", err)
	}

	// both sides complete: each uses its own ephPriv + the peer's ephPub
	chJSON, _ := json.Marshal(ch)
	shJSON, _ := json.Marshal(sh)
	cEphPub, _ := ClientEphPub(ch)
	sEphPub, _ := ServerEphPub(sh)

	cSide, err := CompleteHandshake(cEph, sEphPub, nc, ns, chJSON, shJSON)
	if err != nil {
		t.Fatalf("C CompleteHandshake: %v", err)
	}
	sSide, err := CompleteHandshake(sEph, cEphPub, nc, ns, chJSON, shJSON)
	if err != nil {
		t.Fatalf("S CompleteHandshake: %v", err)
	}

	// THE critical property: identical keys on both sides
	if !bytes.Equal(cSide.C2S, sSide.C2S) || !bytes.Equal(cSide.S2C, sSide.S2C) {
		t.Fatal("session keys differ between C and S")
	}
	if !bytes.Equal(cSide.Transcript, sSide.Transcript) {
		t.Fatal("transcript hash differs between C and S")
	}

	// Finished round-trip: C seals fin with c2s, S opens + verifies transcript
	cAEAD, _ := NewAEAD(cSide.C2S)
	finMsg := FinishedMessage("c", cSide.Transcript)
	finJSON, _ := json.Marshal(finMsg)
	nonce, _ := Random(12)
	cFinFrame := SealFrame(cAEAD, 0, nonce, finJSON)
	sAEAD, _ := NewAEAD(cSide.C2S) // C→S uses c2s
	_, finPlain, err := OpenFrame(sAEAD, cFinFrame)
	if err != nil {
		t.Fatalf("open Finished: %v", err)
	}
	var fin struct {
		T    string `json:"t"`
		Role string `json:"role"`
		Th   string `json:"th"`
	}
	json.Unmarshal(finPlain, &fin)
	if err := VerifyFinished(fin, "c", cSide.Transcript); err != nil {
		t.Fatalf("verify Finished: %v", err)
	}
}

func TestHandshakeBadSigRejected(t *testing.T) {
	cPub, cPriv, _ := GenerateLongTerm()
	_, _, _ = GenerateLongTerm()
	nc, _ := Random(16)
	cEph, _ := GenerateEphemeral()
	ch := BuildClientHello(cPriv, cEph.PublicKey().Bytes(), nc, "cid", "sid", time.Now().UnixMilli())
	ch.Sig = b64(bytes.Repeat([]byte{0}, 64)) // wrong sig
	if err := VerifyClientHello(cPub, ch); err != ErrBadSig {
		t.Fatalf("want ErrBadSig, got %v", err)
	}
}

func TestHandshakeBadEphemeral(t *testing.T) {
	cPub, cPriv, _ := GenerateLongTerm()
	nc, _ := Random(16)
	cEph, _ := GenerateEphemeral()
	ch := BuildClientHello(cPriv, cEph.PublicKey().Bytes(), nc, "cid", "sid", time.Now().UnixMilli())
	ch.Eph = b64([]byte("short")) // wrong length
	if err := VerifyClientHello(cPub, ch); err != ErrBadEphemeral {
		t.Fatalf("want ErrBadEphemeral, got %v", err)
	}
}

func TestFinishedTranscriptBinding(t *testing.T) {
	// Finished must fail if transcript differs (downgrade detection)
	ts := []byte("0123456789abcdef")
	other := []byte("differenttranscript!")
	fin := FinishedMessage("c", ts)
	if err := VerifyFinished(fin, "c", ts); err != nil {
		t.Fatalf("matching transcript should pass: %v", err)
	}
	if err := VerifyFinished(fin, "c", other); err == nil {
		t.Fatal("mismatched transcript should fail")
	}
	// wrong role
	if err := VerifyFinished(fin, "s", ts); err == nil {
		t.Fatal("wrong role should fail")
	}
}
