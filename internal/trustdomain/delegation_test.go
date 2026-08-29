package trustdomain

import (
	"crypto/sha256"
	"errors"
	"testing"
)

func TestDelegationVerification(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)
	tok := NewTokenRecord(TokenPayload{
		TokenID: "tok-d1", SubjectID: h.member.ID(), Resource: "res-db",
		Operations: []string{"read", "propose"}, ExpiresAt: 99_000,
	}, 5_000)
	if err := tok.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(tok)

	payload := []byte(`{"query":"uptime"}`)
	hashOf := func() []byte {
		s := sha256.Sum256(payload)
		return s[:]
	}
	build := func(op, res string, ttl uint64) *Delegation {
		t.Helper()
		d := &Delegation{
			TokenID: "tok-d1", SubjectID: h.member.ID(),
			Resource: res, Operation: op,
			PayloadHash: hashOf(),
			IssuedAt:    6_000, ExpiresAt: 6_000 + ttl,
		}
		if err := d.SignAs(h.member); err != nil {
			t.Fatal(err)
		}
		return d
	}
	verify := func(d *Delegation, now uint64) error {
		return h.chain.State().VerifyDelegation(d, payload, now)
	}

	// Happy path.
	if err := verify(build("read", "res-db", 600), 6_100); err != nil {
		t.Fatalf("valid delegation rejected: %v", err)
	}

	// Expired delegation.
	if err := verify(build("read", "res-db", 60), 6_100); !errors.Is(err, ErrDelegationExpired) {
		t.Fatalf("want ErrDelegationExpired, got %v", err)
	}

	// Payload swapped after signing.
	d := build("read", "res-db", 600)
	if err := h.chain.State().VerifyDelegation(d, []byte("tampered"), 6_100); !errors.Is(err, ErrDelegationPayload) {
		t.Fatalf("want ErrDelegationPayload, got %v", err)
	}

	// Operation outside token scope.
	if err := verify(build("write", "res-db", 600), 6_100); !errors.Is(err, ErrDelegationScope) {
		t.Fatalf("want ErrDelegationScope (op), got %v", err)
	}

	// Resource outside token scope.
	if err := verify(build("read", "res-web", 600), 6_100); !errors.Is(err, ErrDelegationScope) {
		t.Fatalf("want ErrDelegationScope (res), got %v", err)
	}

	// Impersonation: attacker signs but claims the member as subject.
	attacker, _ := GenerateIdentity()
	forged := build("read", "res-db", 600)
	forged.Requester = attacker.Public
	forged.Sig = attacker.Sign(forged.material())
	if err := verify(forged, 6_100); !errors.Is(err, ErrDelegationImpersonation) {
		t.Fatalf("want ErrDelegationImpersonation, got %v", err)
	}

	// Token expiry beats delegation TTL: the delegation itself is still
	// fresh (issued at 99_000, valid to 101_000) while the token died.
	tokExpiredDel := &Delegation{
		TokenID: "tok-d1", SubjectID: h.member.ID(),
		Resource: "res-db", Operation: "read",
		PayloadHash: hashOf(), IssuedAt: 99_000, ExpiresAt: 101_000,
	}
	if err := tokExpiredDel.SignAs(h.member); err != nil {
		t.Fatal(err)
	}
	if err := verify(tokExpiredDel, 99_500); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}

	// Revocation kills the delegation mid-flight.
	h.revoke(h.member)
	if err := verify(build("read", "res-db", 600), 6_100); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("want ErrTokenRevoked after revocation, got %v", err)
	}
}

func TestDelegationWildcardScope(t *testing.T) {
	h := newHarness(t)
	h.admit(h.member, "host-b", false)
	tok := NewTokenRecord(TokenPayload{
		TokenID: "tok-wild", SubjectID: h.member.ID(), Resource: "*",
		Operations: []string{"*"}, ExpiresAt: 99_000,
	}, 5_000)
	if err := tok.SignAs(h.admins[0], h.chain.HeadHash()); err != nil {
		t.Fatal(err)
	}
	h.append(tok)

	payload := []byte("x")
	s := sha256.Sum256(payload)
	d := &Delegation{
		TokenID: "tok-wild", SubjectID: h.member.ID(),
		Resource: "anything", Operation: "whatever",
		PayloadHash: s[:], IssuedAt: 6_000, ExpiresAt: 7_000,
	}
	if err := d.SignAs(h.member); err != nil {
		t.Fatal(err)
	}
	if err := h.chain.State().VerifyDelegation(d, payload, 6_500); err != nil {
		t.Fatalf("wildcard token rejected: %v", err)
	}
}
