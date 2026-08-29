package trustdomain

import (
	"fmt"
)

// Builders construct and sign records/blocks. They exist so tests and the
// future gossip/transport layer share exactly the signing rules the
// validator enforces — no hand-rolled serialization anywhere.

// --- unsigned record constructors ------------------------------------------

func NewMemberRecord(memberPub []byte, displayName string, admin bool, ts uint64) *Record {
	return &Record{
		Type:      RecordMember,
		Subject:   ID(memberPub),
		Payload:   encodePayload(MemberPayload{MemberID: ID(memberPub), PublicKey: memberPub, DisplayName: displayName, Admin: admin}),
		Timestamp: ts,
	}
}

func NewRevocationRecord(targetID, reason string, ts uint64) *Record {
	return &Record{
		Type:      RecordRevocation,
		Subject:   targetID,
		Payload:   encodePayload(RevocationPayload{TargetID: targetID, Reason: reason}),
		Timestamp: ts,
	}
}

func NewTokenRecord(p TokenPayload, ts uint64) *Record {
	return &Record{
		Type:      RecordToken,
		Subject:   p.TokenID,
		Payload:   encodePayload(p),
		Timestamp: ts,
	}
}

func NewAuditAnchorRecord(memberID, auditHeadHex string, ts uint64) *Record {
	return &Record{
		Type:      RecordAuditAnchor,
		Subject:   memberID,
		Payload:   encodePayload(AuditAnchorPayload{AuditHead: auditHeadHex}),
		Timestamp: ts,
	}
}

func NewAttestationRecord(memberID string, p AttestationPayload, ts uint64) *Record {
	return &Record{
		Type:      RecordAttestation,
		Subject:   memberID,
		Payload:   encodePayload(p),
		Timestamp: ts,
	}
}

func NewPolicyRecord(p PolicyPayload, ts uint64) *Record {
	return &Record{
		Type:      RecordPolicy,
		Subject:   fmt.Sprintf("policy-v%d", p.RuleVersion),
		Payload:   encodePayload(p),
		Timestamp: ts,
	}
}

// NewSuccessionRecord builds a dead-man promotion (spec §13.2) — valid
// without quorum once the configured timeout has elapsed.
func NewSuccessionRecord(memberID string, ts uint64) *Record {
	return &Record{
		Type:      RecordSuccession,
		Subject:   memberID,
		Payload:   encodePayload(SuccessionPayload{MemberID: memberID}),
		Timestamp: ts,
	}
}

// NewPauseRecord builds the emergency brake (resume=false) or its release
// (resume=true). Quorum-signed like every power-structure record.
func NewPauseRecord(resume bool, reason string, ts uint64) *Record {
	return &Record{
		Type:      RecordPause,
		Subject:   "pause",
		Payload:   encodePayload(PausePayload{Resume: resume, Reason: reason}),
		Timestamp: ts,
	}
}

func NewTerminalRecord(reason string, ts uint64) *Record {
	return &Record{
		Type:      RecordTerminal,
		Subject:   "terminal",
		Payload:   encodePayload(TerminalPayload{Reason: reason}),
		Timestamp: ts,
	}
}

// --- signing -----------------------------------------------------------------

// SignAs sets the issuer signature. For quorum records the issuer counts as
// one approval; collect more with ApproveWith until m-of-n is met.
func (rec *Record) SignAs(id *Identity, parent Hash) error {
	if id == nil || len(id.Private) == 0 {
		return errNilKey
	}
	rec.Issuer = id.Public
	rec.Sig = id.Sign(recordSigningMaterial(rec, parent))
	return nil
}

// ApproveWith appends an admin approval signature.
func (rec *Record) ApproveWith(id *Identity, parent Hash) error {
	if id == nil || len(id.Private) == 0 {
		return errNilKey
	}
	rec.Approvals = append(rec.Approvals, Approval{
		Admin: id.Public,
		Sig:   id.Sign(recordSigningMaterial(rec, parent)),
	})
	return nil
}

// NewBlock builds a block over the given parent and signs it as proposer.
func NewBlock(height uint64, parent Hash, proposer *Identity, records []*Record) (*Block, error) {
	if proposer == nil || len(proposer.Private) == 0 {
		return nil, errNilKey
	}
	b := &Block{Height: height, PrevHash: parent, Records: records, Proposer: proposer.Public}
	b.ProposerSig = proposer.Sign(b.proposerMaterial())
	return b, nil
}

// BuildGenesis creates and signs the genesis block from the founding admin
// identities. Every founding admin approves, so the first checkpoint quorum
// is available immediately.
func BuildGenesis(admins []*Identity, quorumM int, domainName string, ts uint64) (*Block, error) {
	if len(admins) == 0 {
		return nil, fmt.Errorf("trustdomain: genesis needs at least one admin")
	}
	keys := make([][]byte, len(admins))
	for i, a := range admins {
		keys[i] = a.Public
	}
	rec := &Record{
		Type:      RecordGenesis,
		Subject:   "genesis",
		Payload:   encodePayload(GenesisPayload{RuleVersion: 1, AdminKeys: keys, QuorumM: quorumM, DomainName: domainName}),
		Timestamp: ts,
	}
	if err := rec.SignAs(admins[0], ZeroHash); err != nil {
		return nil, err
	}
	for _, a := range admins[1:] {
		if err := rec.ApproveWith(a, ZeroHash); err != nil {
			return nil, err
		}
	}
	return NewBlock(0, ZeroHash, admins[0], []*Record{rec})
}

// NewCheckpoint signs a checkpoint over the given head with one admin;
// add more SignCheckpoint-style approvals via ck.Sigs until quorum is met.
func NewCheckpoint(height uint64, head Hash, admin *Identity) (*Checkpoint, error) {
	if admin == nil || len(admin.Private) == 0 {
		return nil, errNilKey
	}
	mat := checkpointMaterial(height, head)
	ck := &Checkpoint{TargetHeight: height, TargetHash: head}
	ck.Sigs = append(ck.Sigs, Approval{Admin: admin.Public, Sig: admin.Sign(mat)})
	return ck, nil
}

// ApproveCheckpoint appends one more admin signature to a checkpoint.
func (ck *Checkpoint) ApproveCheckpoint(admin *Identity) error {
	if admin == nil || len(admin.Private) == 0 {
		return errNilKey
	}
	mat := checkpointMaterial(ck.TargetHeight, ck.TargetHash)
	ck.Sigs = append(ck.Sigs, Approval{Admin: admin.Public, Sig: admin.Sign(mat)})
	return nil
}
