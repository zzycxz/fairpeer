package trustdomain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Test harness: a small domain of 3 admins (quorum 2) plus one regular
// member, mirroring the spec's §14.2 acceptance scenarios.

type harness struct {
	t      *testing.T
	admins []*Identity // 3 founding admins, quorum 2
	member *Identity   // a plain member (host)
	chain  *Chain
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t}
	for i := 0; i < 3; i++ {
		id, err := GenerateIdentity()
		if err != nil {
			t.Fatalf("admin identity: %v", err)
		}
		h.admins = append(h.admins, id)
	}
	member, err := GenerateIdentity()
	if err != nil {
		t.Fatalf("member identity: %v", err)
	}
	h.member = member

	gen, err := BuildGenesis(h.admins, 2, "test-domain", 1000)
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	h.chain, err = ValidateChain([]*Block{gen})
	if err != nil {
		t.Fatalf("validate genesis: %v", err)
	}
	return h
}

// admit quorum-signs a member admission and appends it.
func (h *harness) admit(id *Identity, name string, admin bool) {
	h.t.Helper()
	rec := NewMemberRecord(id.Public, name, admin, 2000)
	if err := rec.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		h.t.Fatal(err)
	}
	if err := rec.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		h.t.Fatal(err)
	}
	h.append(rec)
}

// revoke quorum-signs a revocation of target and appends it.
func (h *harness) revoke(target *Identity) {
	h.t.Helper()
	rec := NewRevocationRecord(target.ID(), "test", 3000)
	if err := rec.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		h.t.Fatal(err)
	}
	if err := rec.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		h.t.Fatal(err)
	}
	h.append(rec)
}

// attest publishes the member's self-attestation.
func (h *harness) attest(id *Identity, version string) {
	h.t.Helper()
	rec := NewAttestationRecord(id.ID(), AttestationPayload{Version: version, PolicyHash: "ph", AuditHead: "ah"}, 4000)
	if err := rec.SignAs(id, h.chain.HeadHash()); err != nil {
		h.t.Fatal(err)
	}
	h.append(rec)
}

// append builds a block (proposed by admin[2]) and extends the chain.
func (h *harness) append(records ...*Record) {
	h.t.Helper()
	b, err := NewBlock(h.chain.Height()+1, h.chain.HeadHash(), h.admins[2], records)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.chain.Append(b); err != nil {
		h.t.Fatalf("append block %d: %v", b.Height, err)
	}
}

// appendExpectErr is append that must fail.
func (h *harness) appendExpectErr(want string, records ...*Record) {
	h.t.Helper()
	b, err := NewBlock(h.chain.Height()+1, h.chain.HeadHash(), h.admins[2], records)
	if err != nil {
		h.t.Fatal(err)
	}
	err = h.chain.Append(b)
	if err == nil {
		h.t.Fatalf("append block %d: expected error containing %q, got success", b.Height, want)
	}
	if !strings.Contains(err.Error(), want) {
		h.t.Fatalf("append block %d: error %q does not contain %q", b.Height, err, want)
	}
}

// --- §14.2 line 1: admission → attestation → query → revocation → rejection ---

func TestAdmissionAttestationRevocationPipeline(t *testing.T) {
	h := newHarness(t)
	st := h.chain.State()
	if got := len(st.Admins()); got != 3 {
		t.Fatalf("genesis admins = %d, want 3", got)
	}
	if st.QuorumM != 2 {
		t.Fatalf("quorum = %d, want 2", st.QuorumM)
	}

	h.admit(h.member, "host-b", false)
	st = h.chain.State()
	if !st.IsMember(h.member.ID()) || st.IsAdmin(h.member.ID()) {
		t.Fatal("member admitted but state disagrees")
	}
	if m := st.Member(h.member.ID()); m == nil || m.DisplayName != "host-b" {
		t.Fatal("member info missing or wrong")
	}

	h.attest(h.member, "v0.1.0")
	st = h.chain.State()
	if a := st.LatestAttestation(h.member.ID()); a == nil || a.Version != "v0.1.0" {
		t.Fatal("attestation not on the board")
	}

	// Token issued to the member while in good standing.
	tok := NewTokenRecord(TokenPayload{TokenID: "tok-1", SubjectID: h.member.ID(), Resource: "res-db", Operations: []string{"read"}, ExpiresAt: 9000}, 5000)
	if err := tok.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(tok)
	st = h.chain.State()
	if err := st.CheckToken("tok-1", h.member.ID(), 6000); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}

	h.revoke(h.member)
	st = h.chain.State()
	if st.IsMember(h.member.ID()) {
		t.Fatal("revoked member still active")
	}
	if !st.IsRevoked(h.member.ID()) {
		t.Fatal("member not marked revoked")
	}
	// Token dies with its subject even before expiry.
	if err := st.CheckToken("tok-1", h.member.ID(), 6000); err != ErrTokenRevoked {
		t.Fatalf("revoked subject token: got %v, want ErrTokenRevoked", err)
	}

	// Post-revocation: the revoked member can no longer self-attest.
	rec := NewAttestationRecord(h.member.ID(), AttestationPayload{Version: "v0.2.0"}, 7000)
	if err := rec.SignAs(h.member, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("not an active member", rec)

	// Re-admission clears the revocation (准入→撤销→再准入, spec §5.5 ordering).
	h.admit(h.member, "host-b", false)
	st = h.chain.State()
	if !st.IsMember(h.member.ID()) || st.IsRevoked(h.member.ID()) {
		t.Fatal("re-admission did not clear revocation")
	}
}

// --- §14.2: forged signature rejected ---------------------------------------

func TestForgedSignatureRejected(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	// Forged attestation: attacker signs with their own key but claims the
	// member's ID as subject.
	attacker, _ := GenerateIdentity()
	rec := NewAttestationRecord(h.member.ID(), AttestationPayload{Version: "evil"}, 8000)
	if err := rec.SignAs(attacker, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("self-signed", rec)

	// Tampered payload after signing.
	ok := NewAttestationRecord(h.member.ID(), AttestationPayload{Version: "v1"}, 8100)
	if err := ok.SignAs(h.member, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	ok.Payload[0] ^= 0xFF
	h.appendExpectErr("invalid signature", ok)
}

// --- §14.2: membership change without quorum rejected -----------------------

func TestQuorumNotMetRejected(t *testing.T) {
	h := newHarness(t)
	stranger, _ := GenerateIdentity()

	// Only one admin signature — below quorum 2.
	rec := NewMemberRecord(stranger.Public, "sneaky", false, 9000)
	if err := rec.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("quorum not met", rec)

	// A non-admin "approving" doesn't help.
	if err := rec.ApproveWith(h.member, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("quorum not met", rec)
}

// --- §14.2: deleted block detected ------------------------------------------

func TestDeletedBlockDetected(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)
	h.attest(h.member, "v1")
	h.attest(h.member, "v2")
	h.revoke(h.member)

	blocks := h.chain.Blocks()
	if len(blocks) != 5 { // genesis + 4
		t.Fatalf("blocks = %d, want 5", len(blocks))
	}

	// Delete a middle block (the admission, height 1) — the classic
	// "remove the revocation's subject history" attack shape. The next
	// block's height no longer lines up: a located gap.
	gapped := append([]*Block{}, blocks[0], blocks[2], blocks[3], blocks[4])
	_, err := ValidateChain(gapped)
	if err == nil {
		t.Fatal("gap not detected")
	}
	var br *ChainBreakError
	if !errors.As(err, &br) || br.Height != 2 || br.Kind != "gap" {
		t.Fatalf("want gap at height 2, got %v", err)
	}

	// Substitution: correct height but a foreign parent hash — mismatch.
	tampered := append([]*Block{}, blocks...)
	tb := *blocks[1]
	tb.PrevHash = ZeroHash
	tampered[1] = &tb
	_, err = ValidateChain(tampered)
	if err == nil {
		t.Fatal("parent substitution not detected")
	}
	if !errors.As(err, &br) || br.Height != 1 || br.Kind != "mismatch" {
		t.Fatalf("want mismatch at height 1, got %v", err)
	}

	// Reordering also breaks: swap two blocks.
	swapped := append([]*Block{}, blocks...)
	swapped[1], swapped[2] = swapped[2], swapped[1]
	if _, err := ValidateChain(swapped); err == nil {
		t.Fatal("reorder not detected")
	}
}

// --- §14.2: terminal record rejects any extension ----------------------------

func TestTerminalRejectsExtension(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	term := NewTerminalRecord("decommission", 9500)
	if err := term.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	if err := term.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(term)

	st := h.chain.State()
	if !st.Terminal {
		t.Fatal("terminal not latched")
	}

	// Anything after the terminal block is invalid — even a valid checkpoint.
	ck, err := NewCheckpoint(h.chain.Height(), h.chain.HeadHash(), h.admins[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := ck.ApproveCheckpoint(h.admins[1]); err != nil {
		t.Fatal(err)
	}
	b, err := NewBlock(h.chain.Height()+1, h.chain.HeadHash(), h.admins[2], nil)
	if err != nil {
		t.Fatal(err)
	}
	b.Checkpoint = ck
	if err := h.chain.Append(b); err == nil || !strings.Contains(err.Error(), "may not extend") {
		t.Fatalf("post-terminal append: got %v", err)
	}
}

// --- §14.2: fork converges by latest checkpoint -------------------------------

func TestForkCheckpointResolution(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	// Fork point: after genesis+admission (height 1).
	forkBase := h.chain.Blocks()
	head1 := h.chain.HeadHash()

	// Branch A: two more blocks, second carries a checkpoint of its parent.
	a1, err := NewBlock(2, head1, h.admins[2], nil)
	if err != nil {
		t.Fatal(err)
	}
	chainA, err := ValidateChain(append(append([]*Block{}, forkBase...), a1))
	if err != nil {
		t.Fatal(err)
	}
	if err := chainA.Append(mustBlock(t, chainA, h.admins[2], nil, func(b *Block) {
		ck, err := NewCheckpoint(2, chainA.HeadHash(), h.admins[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := ck.ApproveCheckpoint(h.admins[1]); err != nil {
			t.Fatal(err)
		}
		b.Checkpoint = ck
	})); err != nil {
		t.Fatal(err)
	}

	// Branch B: THREE blocks (longer), no checkpoint.
	chainB, err := ValidateChain(append([]*Block{}, forkBase...))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := chainB.Append(mustBlock(t, chainB, h.admins[2], nil, nil)); err != nil {
			t.Fatal(err)
		}
	}

	// Checkpoint beats length (spec §5.5).
	if got := ForkChoice(chainA, chainB); got != chainA {
		t.Fatal("longer checkpoint-less fork beat checkpointed fork")
	}

	// Once B checkpoints later than A's, B wins.
	lastB := chainB.HeadHash()
	b4, err := NewBlock(chainB.Height()+1, lastB, h.admins[2], nil)
	if err != nil {
		t.Fatal(err)
	}
	ck, err := NewCheckpoint(chainB.Height(), lastB, h.admins[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := ck.ApproveCheckpoint(h.admins[2]); err != nil {
		t.Fatal(err)
	}
	b4.Checkpoint = ck
	if err := chainB.Append(b4); err != nil {
		t.Fatal(err)
	}
	if got := ForkChoice(chainA, chainB); got != chainB {
		t.Fatal("later checkpoint did not win")
	}
}

// mustBlock builds a block extending chain's tip with optional mutation
// applied before signing is finalized — mutation cannot forge signatures
// because signing happens on the final material.
func mustBlock(t *testing.T, c *Chain, proposer *Identity, records []*Record, mutate func(*Block)) *Block {
	t.Helper()
	if mutate == nil {
		b, err := NewBlock(c.Height()+1, c.HeadHash(), proposer, records)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// Build, mutate, then re-sign: sign AFTER mutation so the mutation is
	// covered by the proposer signature (the honest-proposer path).
	b := &Block{Height: c.Height() + 1, PrevHash: c.HeadHash(), Records: records, Proposer: proposer.Public}
	mutate(b)
	b.Proposer = proposer.Public
	b.ProposerSig = proposer.Sign(b.proposerMaterial())
	return b
}

// --- §14.2: revocation effective immediately (no checkpoint needed) ----------

func TestRevocationEffectiveImmediately(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	// The revocation block is NOT checkpointed — see-on-sight semantics.
	h.revoke(h.member)

	st := h.chain.State()
	if _, _, ok := h.chain.LastCheckpoint(); ok {
		t.Fatal("no checkpoint expected in this scenario")
	}
	if !st.IsRevoked(h.member.ID()) {
		t.Fatal("revocation should be effective without checkpoint")
	}
	if !BlockRevokes(h.chain.Head(), h.member.ID()) {
		t.Fatal("BlockRevokes helper failed to see the revocation")
	}

	// And a token check fails for the revoked subject mid-chain.
	tok := NewTokenRecord(TokenPayload{TokenID: "t2", SubjectID: h.member.ID(), Resource: "r", Operations: []string{"read"}, ExpiresAt: 99_999}, 9600)
	if err := tok.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(tok) // issuance itself is fine (admins signed it)
	st = h.chain.State()
	if err := st.CheckToken("t2", h.member.ID(), 9700); err != ErrTokenRevoked {
		t.Fatalf("token for revoked subject: got %v, want ErrTokenRevoked", err)
	}
}

// --- token expiry --------------------------------------------------------------

func TestTokenExpiryAndSubjectMismatch(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	tok := NewTokenRecord(TokenPayload{TokenID: "t3", SubjectID: h.member.ID(), Resource: "r", Operations: []string{"read"}, ExpiresAt: 10_000}, 9800)
	if err := tok.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(tok)

	st := h.chain.State()
	if err := st.CheckToken("t3", h.member.ID(), 9999); err != nil {
		t.Fatalf("pre-expiry: %v", err)
	}
	if err := st.CheckToken("t3", h.member.ID(), 10_001); err != ErrTokenExpired {
		t.Fatalf("post-expiry: got %v, want ErrTokenExpired", err)
	}
	if err := st.CheckToken("t3", "someone-else", 9999); err != ErrTokenSubject {
		t.Fatalf("subject mismatch: got %v, want ErrTokenSubject", err)
	}
	if err := st.CheckToken("nope", h.member.ID(), 9999); err != ErrTokenUnknown {
		t.Fatalf("unknown token: got %v, want ErrTokenUnknown", err)
	}
}

// --- abuse caps (spec §5.5 反滥用) ---------------------------------------------

func TestRecordCapsEnforced(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	records := make([]*Record, 0, MaxRecordsPerMemberPerBlock+1)
	for i := 0; i <= MaxRecordsPerMemberPerBlock; i++ {
		rec := NewAttestationRecord(h.member.ID(), AttestationPayload{Version: "spam"}, 9900)
		if err := rec.SignAs(h.member, h.chain.HeadHash()); err != nil {
			t.Fatal(err)
		}
		records = append(records, rec)
	}
	h.appendExpectErr("cap", records...)
}

// --- genesis sanity --------------------------------------------------------------

func TestGenesisValidation(t *testing.T) {
	// Quorum M cannot exceed the number of founding admins.
	admins := make([]*Identity, 2)
	for i := range admins {
		admins[i], _ = GenerateIdentity()
	}
	gen, err := BuildGenesis(admins, 3, "bad", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateChain([]*Block{gen}); err == nil || !strings.Contains(err.Error(), "quorum") {
		t.Fatalf("oversized quorum accepted: %v", err)
	}

	// Genesis approvals below quorum.
	one, _ := GenerateIdentity()
	bad := &Record{
		Type:      RecordGenesis,
		Subject:   "genesis",
		Payload:   encodePayload(GenesisPayload{RuleVersion: 1, AdminKeys: [][]byte{one.Public}, QuorumM: 1}),
		Timestamp: 1,
	}
	if err := bad.SignAs(admins[0], ZeroHash); err != nil {
		t.Fatal(err)
	}
	blk, err := NewBlock(0, ZeroHash, admins[0], []*Record{bad})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateChain([]*Block{blk}); err == nil {
		t.Fatal("non-founding-admin genesis accepted")
	}
}

// --- non-genesis proposer must be a member ----------------------------------------

func TestForeignProposerRejected(t *testing.T) {
	h := newHarness(t)
	stranger, _ := GenerateIdentity()
	b, err := NewBlock(h.chain.Height()+1, h.chain.HeadHash(), stranger, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.chain.Append(b); err == nil || !strings.Contains(err.Error(), "not an active member") {
		t.Fatalf("foreign proposer accepted: %v", err)
	}
}

// --- JSON round-trip: blocks survive serialization (gossip needs this) ------------

func TestBlockJSONRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)
	h.attest(h.member, "v1")

	var restored []*Block
	for _, b := range h.chain.Blocks() {
		data := mustJSON(b)
		rb := &Block{}
		if err := json.Unmarshal(data, rb); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		restored = append(restored, rb)
		if rb.Hash() != b.Hash() {
			t.Fatalf("hash drift after JSON round-trip at height %d", b.Height)
		}
	}
	if _, err := ValidateChain(restored); err != nil {
		t.Fatalf("restored chain invalid: %v", err)
	}
}
