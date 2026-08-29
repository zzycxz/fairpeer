package trustdomain

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// RecordType enumerates the ledger's record vocabulary (spec §5.1: "就这五类"
// plus the two governance records). Adding a type requires a rule-version
// bump (spec §5.5 规则版本化) — old nodes must be able to reject what they
// cannot interpret.
type RecordType uint8

const (
	RecordGenesis     RecordType = iota + 1 // block 0 only: admins, quorum, rule version
	RecordMember                            // admission (or re-admission) of a member
	RecordRevocation                        // revocation-on-sight of a member
	RecordToken                             // capability token registration
	RecordAuditAnchor                       // local audit chain head, cross-anchored
	RecordAttestation                       // self-attestation summary
	RecordPolicy                            // rule/policy change with activation height
	RecordTerminal                          // domain dissolution; chain may not extend
	RecordPause                             // emergency brake: all delegated work stops
	RecordSuccession                        // dead-man promotion (spec §13.2), no quorum
)

var recordNames = map[RecordType]string{
	RecordGenesis:     "genesis",
	RecordMember:      "member",
	RecordRevocation:  "revocation",
	RecordToken:       "token",
	RecordAuditAnchor: "audit_anchor",
	RecordAttestation: "attestation",
	RecordPolicy:      "policy",
	RecordTerminal:    "terminal",
	RecordPause:       "pause",
	RecordSuccession:  "succession",
}

func (t RecordType) String() string {
	if n, ok := recordNames[t]; ok {
		return n
	}
	return fmt.Sprintf("record(%d)", uint8(t))
}

// quorumRecords lists record types that change the power structure and
// therefore require m-of-n admin approval (spec §6.1 write matrix).
func (t RecordType) requiresQuorum() bool {
	switch t {
	case RecordMember, RecordRevocation, RecordPolicy, RecordTerminal, RecordPause:
		return true
	}
	return false
}

// Approval is one admin's signature over a record. Power-structure records
// collect Approvals until the quorum threshold is met; the count is checked
// against the admin set as of the record's position in the chain.
type Approval struct {
	Admin []byte `json:"admin"` // admin public key
	Sig   []byte `json:"sig"`   // signature over recordSigningMaterial
}

// Record is one ledger entry. Payload is canonical JSON of the type-specific
// struct below; Sig binds issuer, type, subject, payload, timestamp AND the
// parent block hash — a record is position-bound and cannot be replayed into
// a different block (spec §5.2).
type Record struct {
	Type      RecordType `json:"type"`
	Subject   string     `json:"subject,omitempty"` // member/token ID the record concerns
	Payload   []byte     `json:"payload"`           // canonical JSON, type-specific
	Timestamp uint64     `json:"timestamp"`
	Issuer    []byte     `json:"issuer"` // primary signer public key
	Sig       []byte     `json:"sig"`    // Issuer's signature
	Approvals []Approval `json:"approvals,omitempty"`
}

// recordSigningMaterial is the canonical byte string every signer (issuer and
// approvers) signs: type | len(subject) subject | len(payload) payload |
// parent-hash | timestamp. Length prefixes prevent concatenation ambiguity.
func recordSigningMaterial(rec *Record, parent Hash) []byte {
	var buf bytes.Buffer
	buf.WriteByte(byte(rec.Type))
	var l2 [2]byte
	binary.BigEndian.PutUint16(l2[:], uint16(len(rec.Subject)))
	buf.Write(l2[:])
	buf.WriteString(rec.Subject)
	var l4 [4]byte
	binary.BigEndian.PutUint32(l4[:], uint32(len(rec.Payload)))
	buf.Write(l4[:])
	buf.Write(rec.Payload)
	buf.Write(parent[:])
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], rec.Timestamp)
	buf.Write(ts[:])
	return buf.Bytes()
}

// Payload structs. Only control-plane metadata — the "chain carries
// credentials, never content" invariant (spec §1.4 #1) is enforced by these
// types having nowhere to put bulk data.

type GenesisPayload struct {
	RuleVersion uint32   `json:"rule_version"`
	AdminKeys   [][]byte `json:"admin_keys"` // founding admins' public keys
	QuorumM     int      `json:"quorum_m"`
	DomainName  string   `json:"domain_name,omitempty"`
}

type MemberPayload struct {
	MemberID    string `json:"member_id"` // must equal ID(PublicKey)
	PublicKey   []byte `json:"public_key"`
	DisplayName string `json:"display_name,omitempty"` // label only — identity is the ID (spec §5.1)
	Admin       bool   `json:"admin"`                  // admitted as admin
}

type RevocationPayload struct {
	TargetID string `json:"target_id"` // must match Subject
	Reason   string `json:"reason,omitempty"`
}

type TokenPayload struct {
	TokenID    string   `json:"token_id"`
	SubjectID  string   `json:"subject_id"` // who is authorized
	Resource   string   `json:"resource"`   // resource ID/class, never contents
	Operations []string `json:"operations"` // capability class whitelist
	ExpiresAt  uint64   `json:"expires_at"` // local-clock deadline (spec §16-3)
	// ParentTokenID makes this a DELEGATED sub-token (spec §13.2 #1):
	// the holder of the parent derives a narrowed capability (subset
	// scope, never outliving the parent). Depth is capped at one level.
	ParentTokenID string `json:"parent_token_id,omitempty"`
}

type AuditAnchorPayload struct {
	AuditHead string `json:"audit_head"` // hex of local audit hash-chain head
}

type AttestationPayload struct {
	Version    string `json:"version"`
	PolicyHash string `json:"policy_hash"`
	AuditHead  string `json:"audit_head"`
	Health     string `json:"health,omitempty"`
}

type PolicyPayload struct {
	RuleVersion      uint32   `json:"rule_version"`
	ActivationHeight uint64   `json:"activation_height"`
	QuorumM          int      `json:"quorum_m,omitempty"`      // 0 = unchanged
	AddAdmins        [][]byte `json:"add_admins,omitempty"`    // public keys
	RemoveAdmins     []string `json:"remove_admins,omitempty"` // IDs
	// Dead-man succession (spec §13.2 #2): when no admin signs anything
	// for SuccessionAfterSec (chain time = last admin-activity record
	// timestamp), the configured members may promote themselves via a
	// RecordSuccession — no admin quorum needed, which is the point.
	SuccessionAfterSec uint32   `json:"succession_after_sec,omitempty"`
	SuccessionMembers  []string `json:"succession_members,omitempty"`
}

// SuccessionPayload promotes one configured successor to admin after the
// dead-man timeout. Valid without quorum — the whole reason it exists.
type SuccessionPayload struct {
	MemberID string `json:"member_id"`
}

type TerminalPayload struct {
	Reason string `json:"reason,omitempty"`
}

// PausePayload is the emergency brake (spec §6.4): Resume=false halts ALL
// delegated work fleet-wide (read-only included — the brake errs strict:
// 宁可多停，不可少停), local diagnostics are unaffected. Resume=true lifts it.
type PausePayload struct {
	Resume bool   `json:"resume,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// encodePayload marshals a payload struct deterministically (encoding/json
// emits struct fields in declaration order).
func encodePayload(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

var errBadSig = errors.New("trustdomain: invalid signature")

// verifySig checks an ed25519 signature over msg.
func verifySig(pub, msg, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize || len(sig) == 0 {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), msg, sig)
}
