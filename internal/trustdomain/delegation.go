package trustdomain

import (
	"crypto/sha256"
	"fmt"
)

// Delegation is a member's signed request to exercise a capability token
// for one specific resource+operation (spec §7.3) — the wire form of "agent
// A asks host B to run work". Verification is fully local (offline, no
// server round-trips) and self-contained: identity, token, scope, freshness
// and payload binding are all checked against the verifier's own ledger.
//
// Replay: the delegation is short-lived (ExpiresAt, ≤ token expiry) and
// carries a PayloadHash binding it to exactly one payload; executors that
// need one-shot semantics additionally track Nonce at their discretion.
type Delegation struct {
	TokenID     string `json:"token_id"`
	SubjectID   string `json:"subject_id"` // requester member ID (token subject)
	Resource    string `json:"resource"`
	Operation   string `json:"operation"`
	PayloadHash []byte `json:"payload_hash"` // SHA-256 of the work payload
	Nonce       []byte `json:"nonce,omitempty"`
	IssuedAt    uint64 `json:"issued_at"`
	ExpiresAt   uint64 `json:"expires_at"` // delegation freshness; ≤ token expiry
	Requester   []byte `json:"requester"`  // requester public key
	Sig         []byte `json:"sig"`        // over the material (all fields, Sig nil)
}

// material is the canonical signing preimage (the struct with Sig nil —
// deterministic field order via encoding/json).
func (d *Delegation) material() []byte {
	c := *d
	c.Sig = nil
	return mustJSON(&c)
}

// SignAs signs the delegation with the requester's identity.
func (d *Delegation) SignAs(id *Identity) error {
	if id == nil || len(id.Private) == 0 {
		return errNilKey
	}
	d.Requester = id.Public
	d.Sig = id.Sign(d.material())
	return nil
}

var (
	ErrDelegationExpired       = fmt.Errorf("trustdomain: delegation expired")
	ErrDelegationPayload       = fmt.Errorf("trustdomain: delegation payload hash mismatch")
	ErrDelegationImpersonation = fmt.Errorf("trustdomain: requester key does not match registered member key")
	ErrDelegationScope         = fmt.Errorf("trustdomain: operation or resource outside token scope")
)

// VerifyDelegation performs the full §7.3 gate against this state view:
//
//	① freshness          now ≤ ExpiresAt
//	② identity           SubjectID is an active member whose REGISTERED key
//	                     equals Requester, and Requester's signature verifies
//	③ payload binding    SHA-256(payload) == PayloadHash
//	④ token              CheckToken (exists, subject matches, unrevoked, unexpired)
//	⑤ scope              Operation ∈ token.Operations, Resource matches
//	                     (exact, or token side is the "*" wildcard)
func (s *State) VerifyDelegation(d *Delegation, payload []byte, now uint64) error {
	if d == nil {
		return fmt.Errorf("trustdomain: nil delegation")
	}
	if s.Paused {
		return ErrDomainPaused
	}
	if now > d.ExpiresAt {
		return ErrDelegationExpired
	}
	m := s.Member(d.SubjectID)
	if m == nil {
		return ErrTokenRevoked // not an active member (unknown or revoked)
	}
	if string(m.PublicKey) != string(d.Requester) {
		return ErrDelegationImpersonation
	}
	if !verifySig(d.Requester, d.material(), d.Sig) {
		return errBadSig
	}
	h := sha256.Sum256(payload)
	if string(h[:]) != string(d.PayloadHash) {
		return ErrDelegationPayload
	}
	if err := s.CheckToken(d.TokenID, d.SubjectID, now); err != nil {
		return err
	}
	tok := s.Token(d.TokenID)
	if !scopeAllows(tok.Operations, d.Operation) || !resourceMatches(tok.Resource, d.Resource) {
		return ErrDelegationScope
	}
	return nil
}

// scopeAllows: exact operation match, or a "*" wildcard entry.
func scopeAllows(operations []string, want string) bool {
	for _, op := range operations {
		if op == "*" || op == want {
			return true
		}
	}
	return false
}

// resourceMatches: exact match, or the token was issued for any resource.
func resourceMatches(tokenRes, want string) bool {
	return tokenRes == "*" || tokenRes == want
}

// BuildDelegation constructs and signs a work request from this node for
// one of its own tokens. Fails fast if the local view already rejects it
// (token unknown/revoked/expired) — the remote verifier re-checks anyway.
func (n *Node) BuildDelegation(tokenID, resource, operation string, payload []byte, ttlSec uint64, ts uint64) (*Delegation, error) {
	st := n.chain.State()
	if err := st.CheckToken(tokenID, n.id.ID(), ts); err != nil {
		return nil, err
	}
	tok := st.Token(tokenID)
	if !scopeAllows(tok.Operations, operation) || !resourceMatches(tok.Resource, resource) {
		return nil, ErrDelegationScope
	}
	h := sha256.Sum256(payload)
	d := &Delegation{
		TokenID:     tokenID,
		SubjectID:   n.id.ID(),
		Resource:    resource,
		Operation:   operation,
		PayloadHash: h[:],
		IssuedAt:    ts,
		ExpiresAt:   ts + ttlSec,
	}
	if err := d.SignAs(n.id); err != nil {
		return nil, err
	}
	return d, nil
}

// DelegatingPeer is a transport that can carry work requests to remote
// executors (implemented by nettrans; the in-memory test fabric can too).
type DelegatingPeer interface {
	Peer
	Delegate(d *Delegation, payload []byte) ([]byte, error)
}

// RequestWork sends a signed delegation to a peer and returns its result.
func (n *Node) RequestWork(peer Peer, tokenID, resource, operation string, payload []byte, ttlSec, ts uint64) ([]byte, error) {
	dp, ok := peer.(DelegatingPeer)
	if !ok {
		return nil, fmt.Errorf("trustdomain: peer transport cannot carry delegations")
	}
	d, err := n.BuildDelegation(tokenID, resource, operation, payload, ttlSec, ts)
	if err != nil {
		return nil, err
	}
	return dp.Delegate(d, payload)
}
