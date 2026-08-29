package trustdomain

import (
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"
)

func hashOfBytes(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

// TestPauseEmergencyBrake: one quorum record stops every delegated work
// path (spec §6.4), one Resume record restores it.
func TestPauseEmergencyBrake(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)
	tok := NewTokenRecord(TokenPayload{
		TokenID: "tok-p1", SubjectID: h.member.ID(), Resource: "res-db",
		Operations: []string{"read"}, ExpiresAt: 99_000,
	}, 5_000)
	if err := tok.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(tok)

	payload := []byte("p")
	d := &Delegation{
		TokenID: "tok-p1", SubjectID: h.member.ID(),
		Resource: "res-db", Operation: "read",
		PayloadHash: hashOfBytes(payload), IssuedAt: 6_000, ExpiresAt: 7_000,
	}
	if err := d.SignAs(h.member); err != nil {
		t.Fatal(err)
	}
	if err := h.chain.State().VerifyDelegation(d, payload, 6_100); err != nil {
		t.Fatalf("pre-pause: %v", err)
	}

	// Engage the brake (quorum-signed).
	pause := NewPauseRecord(false, "incident", 6_200)
	if err := pause.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	if err := pause.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(pause)

	st := h.chain.State()
	if !st.Paused {
		t.Fatal("brake not latched in state")
	}
	if err := st.VerifyDelegation(d, payload, 6_300); !errors.Is(err, ErrDomainPaused) {
		t.Fatalf("want ErrDomainPaused, got %v", err)
	}

	// CheckToken itself still passes — the brake gates delegation, not
	// the token registry (local reads stay useful during an incident).
	if err := st.CheckToken("tok-p1", h.member.ID(), 6_300); err != nil {
		t.Fatalf("token check broken by pause: %v", err)
	}

	// Lift it (quorum-signed resume).
	resume := NewPauseRecord(true, "all clear", 6_400)
	if err := resume.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	if err := resume.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(resume)

	st = h.chain.State()
	if st.Paused {
		t.Fatal("resume did not lift the brake")
	}
	if err := st.VerifyDelegation(d, payload, 6_500); err != nil {
		t.Fatalf("post-resume: %v", err)
	}

	// A pause without quorum is just an invalid record.
	rogue := NewPauseRecord(false, "solo", 6_600)
	if err := rogue.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("quorum not met", rogue)
}

// TestLedgerHotReload: a node picks up blocks another process wrote to
// the shared ledger file — the single-writer mitigation (spec §16-9).
func TestLedgerHotReload(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	dir := filepath.Join(t.TempDir(), "node")
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(h.chain); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	node := NewNode(h.admins[0], loaded, func() []Peer { return nil },
		NodeOptions{Store: store})
	before := node.Chain().Height()

	// "Another process": append a block and save behind the node's back.
	att := NewAttestationRecord(h.admins[0].ID(), AttestationPayload{Version: "v-ext"}, 7_000)
	if err := att.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	b, err := NewBlock(h.chain.Height()+1, h.chain.HeadHash(), h.admins[2], []*Record{att})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.chain.Append(b); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(h.chain); err != nil {
		t.Fatal(err)
	}

	// Same height as the old file → no visible mtime change on coarse
	// filesystems; force by touching is flaky, so assert via the stamp
	// differing: the save rewrites the file (size changes with content).
	node.Tick()
	if node.Chain().Height() != before+1 {
		t.Fatalf("hot reload missed external write: height %d want %d", node.Chain().Height(), before+1)
	}
	if node.State().LatestAttestation(h.admins[0].ID()) == nil {
		t.Fatal("reloaded chain lost the external attestation")
	}

	// A stale/foreign file never clobbers a taller in-memory chain.
	if err := store.Save(loaded); err != nil { // shorter chain on disk
		t.Fatal(err)
	}
	node.Tick()
	if node.Chain().Height() != before+1 {
		t.Fatal("shorter on-disk chain clobbered the node")
	}
}

// TestQuorumRatchet: the founding flow's second half (spec §6.2) — raise
// the quorum via a policy record; afterwards fewer signatures no longer
// clear the gate.
func TestQuorumRatchet(t *testing.T) {
	h := newHarness(t) // 3 admins, quorum 2

	st := h.chain.State()
	pol := NewPolicyRecord(PolicyPayload{
		RuleVersion:      st.RuleVersion + 1,
		ActivationHeight: h.chain.Height() + 1, // applies at its own block
		QuorumM:          3,
	}, 7_000)
	if err := pol.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	if err := pol.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(pol)

	if got := h.chain.State().QuorumM; got != 3 {
		t.Fatalf("quorum = %d, want 3", got)
	}

	// A revocation that would have passed under M=2 now fails with one
	// fewer signature.
	rec := NewRevocationRecord(h.member.ID(), "ratchet-test", 7_100)
	if err := rec.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	if err := rec.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("quorum not met", rec)
}
