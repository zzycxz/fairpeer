package trustdomain

import (
	"errors"
	"testing"
)

// TestTokenDelegationChain (spec §13.2 #1): the holder of a token derives
// a narrowed grant; the ledger enforces subset scope, shorter life, depth
// one; revoking the grantor kills the grants.
func TestTokenDelegationChain(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)

	// A second member receives the delegated grant.
	grantee, err := GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	h.admit(grantee, "host-c", false)

	// Admin issues the parent token to host-b: read+write on res-db.
	parent := NewTokenRecord(TokenPayload{
		TokenID: "tok-parent", SubjectID: h.member.ID(), Resource: "res-db",
		Operations: []string{"read", "write"}, ExpiresAt: 99_000,
	}, 5_000)
	if err := parent.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(parent)

	// host-b delegates read-only to host-c until 98_000.
	nodeB := NewNode(h.member, h.chain, func() []Peer { return nil }, NodeOptions{})
	if err := nodeB.DelegateToken("tok-parent", grantee.ID(), "res-db", []string{"read"}, 98_000, 6_000); err != nil {
		t.Fatalf("delegate: %v", err)
	}

	st := h.chain.State()
	if err := st.CheckToken("sub-not-the-id", grantee.ID(), 7_000); err != ErrTokenUnknown {
		// The sub-token ID is derived; find it via the registry instead.
		var subID string
		for _, tok := range st.MemberTokens(grantee.ID()) {
			if tok.ParentTokenID == "tok-parent" {
				subID = tok.TokenID
			}
		}
		if subID == "" {
			t.Fatal("delegated sub-token not registered")
		}
		if err := st.CheckToken(subID, grantee.ID(), 7_000); err != nil {
			t.Fatalf("sub-token invalid while grantor active: %v", err)
		}
	}

	// Find the actual sub-token ID for later assertions.
	var subID string
	for _, tok := range st.MemberTokens(grantee.ID()) {
		if tok.ParentTokenID == "tok-parent" {
			subID = tok.TokenID
		}
	}

	// Scope violations are refused at issuance (never make it to a block).
	badScope := NewTokenRecord(TokenPayload{
		TokenID: "sub-bad", SubjectID: grantee.ID(), Resource: "res-db",
		Operations: []string{"reboot"}, ExpiresAt: 98_000, ParentTokenID: "tok-parent",
	}, 7_000)
	if err := badScope.SignAs(h.member, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("outside parent scope", badScope)

	// Outliving the parent is refused.
	badLife := NewTokenRecord(TokenPayload{
		TokenID: "sub-long", SubjectID: grantee.ID(), Resource: "res-db",
		Operations: []string{"read"}, ExpiresAt: 99_999, ParentTokenID: "tok-parent",
	}, 7_000)
	if err := badLife.SignAs(h.member, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("expire before its parent", badLife)

	// Only the holder may delegate.
	nonHolder := NewTokenRecord(TokenPayload{
		TokenID: "sub-thief", SubjectID: h.admins[0].ID(), Resource: "res-db",
		Operations: []string{"read"}, ExpiresAt: 98_000, ParentTokenID: "tok-parent",
	}, 7_000)
	if err := nonHolder.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("only the holder", nonHolder)

	// Depth cap: the grantee cannot delegate the sub-token further.
	nodeC := NewNode(grantee, h.chain, func() []Peer { return nil }, NodeOptions{})
	if err := nodeC.DelegateToken(subID, h.member.ID(), "res-db", []string{"read"}, 97_000, 7_000); err == nil {
		t.Fatal("depth-2 delegation accepted")
	}

	// Revoking the grantor kills the grant (ancestry walk in CheckToken).
	h.revoke(h.member)
	st = h.chain.State()
	if err := st.CheckToken(subID, grantee.ID(), 8_000); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("want ErrTokenRevoked after grantor revocation, got %v", err)
	}
}

// TestSuccessionDeadMan (spec §13.2 #2): policy configures the clock;
// before it elapses promotion is refused, after it any configured
// successor promotes without admin quorum, and the promotion itself
// refreshes the admin-activity clock.
func TestSuccessionDeadMan(t *testing.T) {
	h := newHarness(t) // 3 admins, quorum 2
	h.admit(h.member, "host-b", false)

	// Configure: 2 hours of admin silence → host-b may promote.
	pol := NewPolicyRecord(PolicyPayload{
		RuleVersion:        2,
		ActivationHeight:   h.chain.Height() + 1,
		SuccessionAfterSec: 2 * 3600,
		SuccessionMembers:  []string{h.member.ID()},
	}, 3_000)
	if err := pol.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	if err := pol.ApproveWith(h.admins[1], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(pol) // last admin activity ts = 3_000

	st := h.chain.State()
	if due, _, after, last := st.SuccessionDue(5_000); due || after != 7200 || last != 3_000 {
		t.Fatalf("dead-man state: due=%v after=%d last=%d", due, after, last)
	}

	// Before the timeout: promotion record invalid (admins still active).
	early := NewSuccessionRecord(h.member.ID(), 8_000)
	if err := early.SignAs(h.member, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("dead-man clock not elapsed", early)

	// Non-configured member can never be promoted this way.
	other, _ := GenerateIdentity()
	h.admit(other, "host-d", false)
	rogue := NewSuccessionRecord(other.ID(), 12_000)
	if err := rogue.SignAs(other, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.appendExpectErr("not a configured successor", rogue)

	// After the timeout (≥ 3_000+7_200 = 10_200): self-promotion lands
	// WITHOUT any admin signature.
	promo := NewSuccessionRecord(h.member.ID(), 11_000)
	if err := promo.SignAs(h.member, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(promo)

	st = h.chain.State()
	if !st.IsAdmin(h.member.ID()) {
		t.Fatal("successor not promoted to admin")
	}
	// The promotion refreshed the admin clock (ts 11_000).
	if due, _, _, last := st.SuccessionDue(15_000); due || last != 11_000 {
		t.Fatalf("clock not refreshed by promotion: due=%v last=%d", due, last)
	}

	// The promoted admin's quorum signature now counts like any admin's.
	rec := NewPauseRecord(true, "successor test", 16_000)
	if err := rec.SignAs(h.member, h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	if err := rec.ApproveWith(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(rec) // must validate: member's admin status is real
}
