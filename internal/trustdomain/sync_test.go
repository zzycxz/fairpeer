package trustdomain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- store: round-trip, tamper rejection, missing ledger ----------------------

func TestStoreRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)
	h.attest(h.member, "v1")

	dir := filepath.Join(t.TempDir(), "domain")
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(h.chain); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Height() != h.chain.Height() || loaded.HeadHash() != h.chain.HeadHash() {
		t.Fatalf("round-trip mismatch: %d/%s vs %d/%s",
			loaded.Height(), loaded.HeadHash().Hex(), h.chain.Height(), h.chain.HeadHash().Hex())
	}
	if DomainID(loaded) != DomainID(h.chain) {
		t.Fatal("domain ID changed across round-trip")
	}
	st := loaded.State()
	if !st.IsMember(h.member.ID()) {
		t.Fatal("state lost the member across round-trip")
	}
}

func TestStoreTamperedFileRejected(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(h.chain); err != nil {
		t.Fatal(err)
	}

	// Flip a byte inside the archive — Load must revalidate and reject.
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(data), `"height"`)
	if i < 0 {
		t.Fatal("height field not found in archive")
	}
	data[i+len(`"height"`)] = 'X'
	if err := os.WriteFile(store.Path(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("tampered ledger accepted")
	}

	// Missing file is a distinct, non-error-of-corruption signal.
	os.Remove(store.Path())
	if _, err := store.Load(); err != ErrNoLedger {
		t.Fatalf("missing ledger: got %v, want ErrNoLedger", err)
	}
}

// --- rotation: deterministic round-robin over the member set ------------------

func TestProposerRotation(t *testing.T) {
	h := newHarness(t)
	st := h.chain.State()

	// Genesis admins only: rotation cycles over 3 admins (sorted IDs).
	ids := st.MemberIDs()
	if len(ids) != 3 {
		t.Fatalf("members = %d, want 3", len(ids))
	}
	for height := uint64(1); height <= 7; height++ {
		want := ids[(height-1)%uint64(len(ids))]
		if got := ProposerFor(st, height); got != want {
			t.Fatalf("height %d: proposer %s, want %s", height, got, want)
		}
	}

	// After admitting a 4th member the rotation set grows; heights before
	// the change would have scheduled differently, which is fine — the
	// schedule is a policy, not a validity rule (see rotation.go).
	h.admit(h.member, "host-b", false)
	st = h.chain.State()
	if got := len(st.MemberIDs()); got != 4 {
		t.Fatalf("members after admission = %d, want 4", got)
	}
	if !IsProposerTurn(st, 4, ProposerFor(st, 4)) {
		t.Fatal("IsProposerTurn disagrees with ProposerFor")
	}
}

// --- sync: plan negotiation -----------------------------------------------------

func TestPlanSync(t *testing.T) {
	local := Status{Height: 5, Head: Hash{1}, HaveCkpt: true, CkptHeight: 3, CkptHash: Hash{9}}

	// In sync.
	p := PlanSync(local, Status{Height: 5, Head: Hash{1}})
	if p.Need || p.Diverged || p.FullResync {
		t.Fatalf("in-sync case planned work: %+v", p)
	}

	// Remote ahead: request the missing range.
	p = PlanSync(local, Status{Height: 9, Head: Hash{2}})
	if !p.Need || p.NeedFrom != 6 || p.NeedTo != 9 || p.Diverged {
		t.Fatalf("behind case: %+v", p)
	}

	// Local ahead: nothing to request, offer our tip.
	p = PlanSync(local, Status{Height: 2, Head: Hash{3}})
	if p.Need || p.OfferTo != 5 {
		t.Fatalf("ahead case: %+v", p)
	}

	// Forked at equal height with a shared checkpoint: resync after it.
	p = PlanSync(local, Status{Height: 5, Head: Hash{4}, HaveCkpt: true, CkptHeight: 3, CkptHash: Hash{9}})
	if !p.Need || !p.Diverged || p.FullResync || p.NeedFrom != 4 || p.NeedTo != 5 {
		t.Fatalf("fork-with-common-ckpt case: %+v", p)
	}

	// Forked without a shared checkpoint: full resync from genesis.
	p = PlanSync(local, Status{Height: 5, Head: Hash{4}, HaveCkpt: true, CkptHeight: 4, CkptHash: Hash{8}})
	if !p.FullResync || p.NeedFrom != 0 {
		t.Fatalf("fork-without-common-ckpt case: %+v", p)
	}
}

// --- sync: catch-up, fork merge, and revocation-on-sight over gossip -------------

// buildDivergedPair produces two same-domain chains: local (longer,
// checkpoint-less) and remote (shorter but checkpointed so it wins choice).
func buildDivergedPair(t *testing.T) (local, remote *Chain, admins []*Identity, member *Identity) {
	t.Helper()
	h := newHarness(t)
	admins, member = h.admins, h.member
	h.admit(h.member, "host-b", false) // height 1, common prefix

	// Local: three more blocks, no checkpoint.
	local = h.chain
	for i := 0; i < 3; i++ {
		b, err := NewBlock(local.Height()+1, local.HeadHash(), admins[2], nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := local.Append(b); err != nil {
			t.Fatal(err)
		}
	}

	// Remote: fork at height 1, one extra block plus a checkpointed block.
	remote, err := ValidateChain(append([]*Block{}, h.chain.Blocks()[:2]...))
	if err != nil {
		t.Fatal(err)
	}
	fb, err := NewBlock(2, remote.HeadHash(), admins[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Append(fb); err != nil {
		t.Fatal(err)
	}
	ck, err := NewCheckpoint(remote.Height(), remote.HeadHash(), admins[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := ck.ApproveCheckpoint(admins[1]); err != nil {
		t.Fatal(err)
	}
	cb, err := NewBlock(remote.Height()+1, remote.HeadHash(), admins[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	cb.Checkpoint = ck
	if err := remote.Append(cb); err != nil {
		t.Fatal(err)
	}
	// Match local's height so both chains end tip-to-tip on different heads
	// — the precondition of the fork-negotiation branch in PlanSync.
	tb, err := NewBlock(remote.Height()+1, remote.HeadHash(), admins[1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Append(tb); err != nil {
		t.Fatal(err)
	}
	return local, remote, admins, member
}

func TestSyncCatchUpAndForkMerge(t *testing.T) {
	local, remote, _, _ := buildDivergedPair(t)

	// Announce: statuses differ at different heights → plan requests range.
	plan := PlanSync(StatusOf(local), StatusOf(remote))
	if !plan.Need {
		t.Fatalf("diverged pair planned no work: %+v", plan)
	}

	// The ranges don't connect directly (different branches) — TryExtend
	// stops, and MergeFork arbitrates: remote's checkpoint beats local's length.
	if _, err := TryExtend(local, remote.Blocks()); err == nil {
		t.Fatal("cross-branch extend should fail")
	}
	winner, switched, err := MergeFork(local, remote.Blocks())
	if err != nil {
		t.Fatal(err)
	}
	if !switched || winner.Height() != remote.Height() || winner.HeadHash() != remote.HeadHash() {
		t.Fatalf("checkpointed fork should win: switched=%v winner=%d/%s want=%d/%s",
			switched, winner.Height(), winner.HeadHash().Hex(), remote.Height(), remote.HeadHash().Hex())
	}

	// A candidate from a different domain is refused outright.
	h2 := newHarness(t)
	other := h2.chain
	if _, _, err := MergeFork(local, other.Blocks()); err == nil || !strings.Contains(err.Error(), "different domain") {
		t.Fatalf("foreign domain candidate: %v", err)
	}
}

func TestSyncHonestCatchUp(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)
	h.attest(h.member, "v1")

	// A fresh node knows only genesis; it receives the announced blocks and
	// extends cleanly.
	fresh, err := ValidateChain([]*Block{h.chain.Blocks()[0]})
	if err != nil {
		t.Fatal(err)
	}
	plan := PlanSync(StatusOf(fresh), StatusOf(h.chain))
	if !plan.Need || plan.NeedFrom != 1 || plan.NeedTo != h.chain.Height() {
		t.Fatalf("catch-up plan: %+v", plan)
	}
	n, err := TryExtend(fresh, h.chain.Blocks()[1:])
	if err != nil || n != int(h.chain.Height()) {
		t.Fatalf("catch-up extend: applied=%d err=%v", n, err)
	}
	if fresh.HeadHash() != h.chain.HeadHash() {
		t.Fatal("caught-up head mismatch")
	}
}

func TestSightStateRevocationOnSight(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)
	tok := NewTokenRecord(TokenPayload{TokenID: "t9", SubjectID: h.member.ID(), Resource: "r", Operations: []string{"read"}, ExpiresAt: 99_000}, 10_000)
	if err := tok.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(tok)

	// A pending (un-confirmed) fork block carries the member's revocation.
	pending, err := NewBlock(h.chain.Height()+1, h.chain.HeadHash(), h.admins[2],
		[]*Record{quorumRevocation(t, h)})
	if err != nil {
		t.Fatal(err)
	}

	// Canonical view: still fine.
	if st := h.chain.State(); st.CheckToken("t9", h.member.ID(), 10_100) != nil {
		t.Fatal("canonical state changed unexpectedly")
	}

	// Sight view: revoked on sight, token defensively dead.
	sight := SightState(h.chain, []*Block{pending})
	if !sight.IsRevoked(h.member.ID()) {
		t.Fatal("pending revocation not effective on sight")
	}
	if err := sight.CheckToken("t9", h.member.ID(), 10_100); err != ErrTokenRevoked {
		t.Fatalf("sight token check: %v, want ErrTokenRevoked", err)
	}

	// The canonical chain itself is untouched.
	if h.chain.State().IsRevoked(h.member.ID()) {
		t.Fatal("canonical chain mutated by sight folding")
	}
}

// quorumRevocation builds a fully-signed revocation against the harness head.
func quorumRevocation(t *testing.T, h *harness) *Record {
	t.Helper()
	rec := NewRevocationRecord(h.member.ID(), "compromised", 10_050)
	if err := rec.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	if err := rec.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	return rec
}
