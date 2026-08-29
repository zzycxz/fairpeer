package trustdomain

import (
	"encoding/json"
	"fmt"
	"sort"
)

// MemberInfo is a member's registry entry. PublicKey is retained because
// approval verification needs raw keys, not just IDs.
type MemberInfo struct {
	ID          string
	PublicKey   []byte
	DisplayName string // label only; identity is always the ID (spec §5.1)
	Admin       bool
	AdmittedAt  uint64 // block height
}

// TokenInfo is a registered capability token (spec §7.2).
type TokenInfo struct {
	TokenID       string
	SubjectID     string
	Resource      string
	Operations    []string
	ExpiresAt     uint64
	IssuedAt      uint64 // block height
	ParentTokenID string // set on delegated sub-tokens (spec §13.2)
}

// State is the domain state after applying the chain up to some height.
// It is advanced block-by-block by Chain; readers use the accessor methods,
// which encode the ledger's security semantics (revocation-on-sight, token
// checks, terminal).
type State struct {
	RuleVersion uint32

	members  map[string]*MemberInfo // active members (not revoked)
	admins   []string               // ordered admin IDs
	adminSet map[string]bool
	QuorumM  int

	revokedAt map[string]uint64 // member ID -> height of revocation

	tokens       map[string]*TokenInfo
	attestations map[string]*AttestationPayload // latest per member
	auditHeads   map[string]string

	pendingPolicies []*PolicyPayload // activated when height reaches ActivationHeight

	Terminal       bool
	TerminalHeight uint64

	// Dead-man succession (spec §13.2): configured by a policy record.
	SuccessionAfterSec uint32
	SuccessionMembers  []string
	// lastAdminActivityTs is the newest timestamp of any admin-signed
	// record (quorum ops, token issuance, succession) — the dead-man
	// clock's chain-derived, deterministic anchor.
	lastAdminActivityTs uint64

	// Paused is the emergency-brake latch (spec §6.4): quorum-signed,
	// effective at its block, lifted only by a Resume record. While set,
	// VerifyDelegation refuses everything — delegated read-only included.
	Paused bool

	lastCkptHeight uint64
	lastCkptHash   Hash
	haveCkpt       bool
}

func newState() *State {
	return &State{
		members:      map[string]*MemberInfo{},
		admins:       nil,
		adminSet:     map[string]bool{},
		revokedAt:    map[string]uint64{},
		tokens:       map[string]*TokenInfo{},
		attestations: map[string]*AttestationPayload{},
		auditHeads:   map[string]string{},
	}
}

// Clone returns a deep copy so callers can trial-apply candidate forks
// without mutating the canonical state.
func (s *State) Clone() *State {
	c := newState()
	c.RuleVersion = s.RuleVersion
	c.QuorumM = s.QuorumM
	c.Terminal = s.Terminal
	c.TerminalHeight = s.TerminalHeight
	c.Paused = s.Paused
	c.SuccessionAfterSec = s.SuccessionAfterSec
	c.SuccessionMembers = append([]string(nil), s.SuccessionMembers...)
	c.lastAdminActivityTs = s.lastAdminActivityTs
	c.lastCkptHeight, c.lastCkptHash, c.haveCkpt = s.lastCkptHeight, s.lastCkptHash, s.haveCkpt
	c.admins = append([]string(nil), s.admins...)
	for k, v := range s.adminSet {
		c.adminSet[k] = v
	}
	for k, v := range s.members {
		cpy := *v
		cpy.PublicKey = append([]byte(nil), v.PublicKey...)
		c.members[k] = &cpy
	}
	for k, v := range s.revokedAt {
		c.revokedAt[k] = v
	}
	for k, v := range s.tokens {
		cpy := *v
		cpy.Operations = append([]string(nil), v.Operations...)
		c.tokens[k] = &cpy
	}
	for k, v := range s.attestations {
		cpy := *v
		c.attestations[k] = &cpy
	}
	for k, v := range s.auditHeads {
		c.auditHeads[k] = v
	}
	c.pendingPolicies = append([]*PolicyPayload(nil), s.pendingPolicies...)
	return c
}

// IsMember reports whether id is an active (admitted, unrevoked) member.
func (s *State) IsMember(id string) bool {
	_, ok := s.members[id]
	return ok
}

// IsRevoked reports whether id was ever revoked and not re-admitted since.
func (s *State) IsRevoked(id string) bool {
	if _, active := s.members[id]; active {
		return false
	}
	_, revoked := s.revokedAt[id]
	return revoked
}

// RevokedIDs lists all members currently in revoked state, sorted — for
// status UIs and prefix lookups.
func (s *State) RevokedIDs() []string {
	out := make([]string, 0, len(s.revokedAt))
	for id := range s.revokedAt {
		if s.IsRevoked(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// IsAdmin reports whether id is a currently-active admin.
func (s *State) IsAdmin(id string) bool {
	return s.adminSet[id] && s.IsMember(id)
}

// Admins returns the active admin IDs, deterministically ordered.
func (s *State) Admins() []string {
	out := append([]string(nil), s.admins...)
	sort.Strings(out)
	return out
}

// MemberIDs returns all active member IDs, sorted — the deterministic
// ordering that round-robin proposer rotation (rotation.go) iterates.
func (s *State) MemberIDs() []string {
	ids := make([]string, 0, len(s.members))
	for id := range s.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// forceRevoke is the defensive half of revocation-on-sight (spec §6.4):
// SightState uses it to mark members revoked from unconfirmed gossip —
// 宁误杀可恢复 (a real re-admission clears it), 不漏杀.
func (s *State) forceRevoke(id string, height uint64) {
	delete(s.members, id)
	s.removeAdmin(id)
	if _, seen := s.revokedAt[id]; !seen {
		s.revokedAt[id] = height
	}
}

// Member returns a member's info, or nil.
func (s *State) Member(id string) *MemberInfo { return s.members[id] }

// LatestAttestation returns the member's most recent self-attestation.
func (s *State) LatestAttestation(id string) *AttestationPayload {
	return s.attestations[id]
}

// AuditHead returns the member's most recent anchored audit chain head.
func (s *State) AuditHead(id string) string { return s.auditHeads[id] }

// Token returns a registered token by ID, or nil.
func (s *State) Token(id string) *TokenInfo { return s.tokens[id] }

// MemberTokens returns all registered tokens whose subject is memberID —
// the candidate set for capability discovery (netdev_remote).
func (s *State) MemberTokens(memberID string) []*TokenInfo {
	var out []*TokenInfo
	for _, tok := range s.tokens {
		if tok.SubjectID == memberID {
			out = append(out, tok)
		}
	}
	return out
}

var (
	ErrTokenUnknown = fmt.Errorf("trustdomain: unknown token")
	ErrTokenSubject = fmt.Errorf("trustdomain: token subject mismatch")
	ErrTokenRevoked = fmt.Errorf("trustdomain: token subject revoked")
	ErrTokenExpired = fmt.Errorf("trustdomain: token expired")
	ErrDomainClosed = fmt.Errorf("trustdomain: domain terminated")
	// ErrDomainPaused: the quorum emergency brake is engaged (spec §6.4) —
	// all delegated work refused until a Resume record lands.
	ErrDomainPaused = fmt.Errorf("trustdomain: domain paused (emergency brake)")
)

// CheckToken validates a delegation request's token (spec §7.3): exists,
// subject matches and is unrevoked, not expired. `now` is the caller's local
// clock — token expiry intentionally uses local time plus caller-side safety
// margin (spec §16-3, leaning local-clock).
func (s *State) CheckToken(tokenID, subject string, now uint64) error {
	tok := s.tokens[tokenID]
	if tok == nil {
		return ErrTokenUnknown
	}
	if tok.SubjectID != subject {
		return ErrTokenSubject
	}
	// Walk the delegation ancestry (depth-capped at issuance; the loop
	// bound is defensive): every ancestor must be an unrevoked member
	// with an unexpired token — revoking the grantor kills the grants.
	t := tok
	for i := 0; i < 4 && t != nil; i++ {
		if !s.IsMember(t.SubjectID) {
			return ErrTokenRevoked
		}
		if t.ExpiresAt != 0 && now > t.ExpiresAt {
			return ErrTokenExpired
		}
		if t.ParentTokenID == "" {
			break
		}
		t = s.tokens[t.ParentTokenID]
		if t == nil {
			return ErrTokenUnknown
		}
	}
	return nil
}

// SuccessionDue reports the dead-man clock: whether the configured
// successors may promote at wall-time `now`, plus the configured values
// (for UI/CLI display).
func (s *State) SuccessionDue(now uint64) (due bool, members []string, afterSec uint32, lastActivity uint64) {
	if s.SuccessionAfterSec == 0 || len(s.SuccessionMembers) == 0 {
		return false, nil, 0, s.lastAdminActivityTs
	}
	due = now >= s.lastAdminActivityTs+uint64(s.SuccessionAfterSec)
	return due, s.SuccessionMembers, s.SuccessionAfterSec, s.lastAdminActivityTs
}

// --- record application ---------------------------------------------------
//
// applyRecord performs the *effects* of a record on state; permission and
// signature checks happen in chain.go before this is called. Records apply in
// order within a block, so later records see earlier effects.

func (s *State) applyRecord(rec *Record, height uint64) error {
	switch rec.Type {
	case RecordGenesis:
		var p GenesisPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: genesis payload: %w", err)
		}
		s.RuleVersion = p.RuleVersion
		for _, k := range p.AdminKeys {
			m := &MemberInfo{ID: ID(k), PublicKey: k, Admin: true, AdmittedAt: height}
			s.members[m.ID] = m
			s.addAdmin(m.ID)
		}
		s.QuorumM = p.QuorumM

	case RecordMember:
		var p MemberPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: member payload: %w", err)
		}
		if ID(p.PublicKey) != p.MemberID {
			return fmt.Errorf("trustdomain: member ID does not match key")
		}
		delete(s.revokedAt, p.MemberID) // re-admission clears prior revocation
		m := &MemberInfo{ID: p.MemberID, PublicKey: p.PublicKey, DisplayName: p.DisplayName, Admin: p.Admin, AdmittedAt: height}
		s.members[p.MemberID] = m
		if p.Admin {
			s.addAdmin(p.MemberID)
		}

	case RecordRevocation:
		var p RevocationPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: revocation payload: %w", err)
		}
		delete(s.members, p.TargetID)
		s.removeAdmin(p.TargetID)
		if _, seen := s.revokedAt[p.TargetID]; !seen {
			s.revokedAt[p.TargetID] = height
		}

	case RecordToken:
		var p TokenPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: token payload: %w", err)
		}
		s.tokens[p.TokenID] = &TokenInfo{
			TokenID: p.TokenID, SubjectID: p.SubjectID, Resource: p.Resource,
			Operations: p.Operations, ExpiresAt: p.ExpiresAt, IssuedAt: height,
			ParentTokenID: p.ParentTokenID,
		}

	case RecordAuditAnchor:
		var p AuditAnchorPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: audit anchor payload: %w", err)
		}
		s.auditHeads[rec.Subject] = p.AuditHead

	case RecordAttestation:
		var p AttestationPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: attestation payload: %w", err)
		}
		s.attestations[rec.Subject] = &p

	case RecordPolicy:
		var p PolicyPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: policy payload: %w", err)
		}
		if p.ActivationHeight > height {
			s.pendingPolicies = append(s.pendingPolicies, &p)
		} else {
			s.applyPolicy(&p)
		}

	case RecordTerminal:
		var p TerminalPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: terminal payload: %w", err)
		}
		s.Terminal = true
		s.TerminalHeight = height

	case RecordPause:
		var p PausePayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: pause payload: %w", err)
		}
		s.Paused = !p.Resume

	case RecordSuccession:
		var p SuccessionPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return fmt.Errorf("trustdomain: succession payload: %w", err)
		}
		if m := s.members[p.MemberID]; m != nil {
			m.Admin = true
		}
		s.addAdmin(p.MemberID)

	default:
		return fmt.Errorf("trustdomain: unknown record type %v", rec.Type)
	}

	// Dead-man clock: admin-signed activity refreshes it (quorum ops,
	// token issuance, succession promotions). Derived purely from chain
	// data — every node computing the same chain agrees.
	if rec.Type.requiresQuorum() || rec.Type == RecordToken || rec.Type == RecordSuccession {
		if rec.Timestamp > s.lastAdminActivityTs {
			s.lastAdminActivityTs = rec.Timestamp
		}
	}
	return nil
}

func (s *State) applyPolicy(p *PolicyPayload) {
	s.RuleVersion = p.RuleVersion
	if p.QuorumM > 0 {
		s.QuorumM = p.QuorumM
	}
	if p.SuccessionAfterSec > 0 {
		s.SuccessionAfterSec = p.SuccessionAfterSec
		s.SuccessionMembers = append([]string(nil), p.SuccessionMembers...)
	}
	for _, k := range p.AddAdmins {
		id := ID(k)
		if m := s.members[id]; m != nil {
			m.Admin = true
		} else {
			s.members[id] = &MemberInfo{ID: id, PublicKey: k, Admin: true}
		}
		s.addAdmin(id)
	}
	for _, id := range p.RemoveAdmins {
		s.removeAdmin(id)
	}
}

// activatePolicies applies due pending policies at a new height
// (spec §5.5: policies take effect at their activation height).
func (s *State) activatePolicies(height uint64) {
	var keep []*PolicyPayload
	for _, p := range s.pendingPolicies {
		if p.ActivationHeight <= height {
			s.applyPolicy(p)
		} else {
			keep = append(keep, p)
		}
	}
	s.pendingPolicies = keep
}

func (s *State) addAdmin(id string) {
	if !s.adminSet[id] {
		s.adminSet[id] = true
		s.admins = append(s.admins, id)
	}
}

func (s *State) removeAdmin(id string) {
	if s.adminSet[id] {
		delete(s.adminSet, id)
		for i, a := range s.admins {
			if a == id {
				s.admins = append(s.admins[:i], s.admins[i+1:]...)
				break
			}
		}
	}
}
