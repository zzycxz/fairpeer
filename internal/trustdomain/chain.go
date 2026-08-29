package trustdomain

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Protocol limits (spec §5.5 反滥用): block size cap and per-member record
// cap within one block. Cross-block rate limiting is a transport policy.
const (
	MaxBlockBytes               = 256 << 10
	MaxRecordsPerMemberPerBlock = 8
)

// ChainBreakError reports a continuity failure: either a height gap or a
// PrevHash mismatch. This is the deletion-detection signal (spec §5.3-2) —
// a replica with a block removed (or reordered) produces exactly this error,
// with the break located.
type ChainBreakError struct {
	Height   uint64 // height of the block that failed to attach
	Kind     string // "gap" or "mismatch"
	Expected Hash
	Got      Hash
}

func (e *ChainBreakError) Error() string {
	if e.Kind == "gap" {
		return fmt.Sprintf("trustdomain: chain gap at height %d", e.Height)
	}
	return fmt.Sprintf("trustdomain: prev-hash mismatch at height %d: expected %s got %s",
		e.Height, e.Expected.Hex(), e.Got.Hex())
}

var (
	ErrNotGenesis      = errors.New("trustdomain: first block must be a valid genesis block")
	ErrChainTerminated = errors.New("trustdomain: terminal record present; chain may not extend")
	ErrGenesisMixed    = errors.New("trustdomain: genesis block must contain exactly one genesis record")
)

// Chain is a validated, contiguous ledger from genesis. It is immutable
// except through Append, which validates before extending.
type Chain struct {
	blocks []*Block
	hashes []Hash
	state  *State
}

// Blocks returns the blocks (do not mutate).
func (c *Chain) Blocks() []*Block { return c.blocks }

// Head returns the tip block.
func (c *Chain) Head() *Block { return c.blocks[len(c.blocks)-1] }

// HeadHash returns the tip block's hash.
func (c *Chain) HeadHash() Hash { return c.hashes[len(c.hashes)-1] }

// Height returns the tip height.
func (c *Chain) Height() uint64 { return c.Head().Height }

// State returns a clone of the current domain state.
func (c *Chain) State() *State { return c.state.Clone() }

// HashAt returns the hash of the block at height, if present.
func (c *Chain) HashAt(height uint64) (Hash, bool) {
	if height < uint64(len(c.hashes)) && c.blocks[height].Height == height {
		return c.hashes[height], true
	}
	return Hash{}, false
}

// LastCheckpoint reports the most recent checkpoint target in this chain.
func (c *Chain) LastCheckpoint() (height uint64, hash Hash, ok bool) {
	return c.state.lastCkptHeight, c.state.lastCkptHash, c.state.haveCkpt
}

// ValidateChain fully validates a block sequence and returns the resulting
// Chain, or the first validation failure (continuity, signatures, write
// permissions, checkpoints, terminal rule).
func ValidateChain(blocks []*Block) (*Chain, error) {
	c := &Chain{state: newState()}
	for i, b := range blocks {
		parent := (*Block)(nil)
		var parentHash Hash
		if i > 0 {
			parent = blocks[i-1]
			parentHash = c.hashes[i-1]
		}
		if err := c.validateBlock(b, parent, parentHash); err != nil {
			return nil, fmt.Errorf("trustdomain: block %d: %w", b.Height, err)
		}
		c.commit(b, b.Hash())
	}
	return c, nil
}

// Append validates one block against the current tip and extends the chain.
func (c *Chain) Append(b *Block) error {
	tip := c.Head()
	if err := c.validateBlock(b, tip, c.HeadHash()); err != nil {
		return fmt.Errorf("trustdomain: block %d: %w", b.Height, err)
	}
	c.commit(b, b.Hash())
	return nil
}

// commit stores a validated block and advances state (already applied during
// validation into c.state).
func (c *Chain) commit(b *Block, h Hash) {
	c.blocks = append(c.blocks, b)
	c.hashes = append(c.hashes, h)
}

// validateBlock performs full validation of b against parent (nil for
// genesis), advancing a trial state; on success the trial becomes c.state.
func (c *Chain) validateBlock(b *Block, parent *Block, parentHash Hash) error {
	if len(mustJSON(b)) > MaxBlockBytes {
		return fmt.Errorf("block exceeds %d bytes", MaxBlockBytes)
	}

	if parent == nil {
		if b.Height != 0 || b.PrevHash != ZeroHash {
			return ErrNotGenesis
		}
		return c.validateGenesis(b)
	}

	// Continuity: the deletion-detection core. Any gap or PrevHash mismatch
	// aborts with a located ChainBreakError.
	if b.Height != parent.Height+1 {
		return &ChainBreakError{Height: b.Height, Kind: "gap"}
	}
	if b.PrevHash != parentHash {
		return &ChainBreakError{Height: b.Height, Kind: "mismatch", Expected: parentHash, Got: b.PrevHash}
	}

	// Terminal rule (spec §6.6): nothing may follow the terminal block.
	if c.state.Terminal {
		return ErrChainTerminated
	}

	// Proposer must be an active member at this point in history.
	if !c.state.IsMember(ID(b.Proposer)) {
		return fmt.Errorf("proposer %s is not an active member", ID(b.Proposer))
	}
	if !verifySig(b.Proposer, b.proposerMaterial(), b.ProposerSig) {
		return errBadSig
	}

	if err := c.checkRecordCaps(b); err != nil {
		return err
	}

	// Records apply in order; each is checked against the state as of its
	// position (quorum counts use the admin set at that moment).
	working := c.state.Clone()
	for _, rec := range b.Records {
		if err := verifyRecord(rec, b.PrevHash, working); err != nil {
			return fmt.Errorf("record %s: %w", rec.Type, err)
		}
		if err := working.applyRecord(rec, b.Height); err != nil {
			return err
		}
	}

	if b.Checkpoint != nil {
		if err := c.validateCheckpoint(b.Checkpoint, working); err != nil {
			return fmt.Errorf("checkpoint: %w", err)
		}
		working.lastCkptHeight = b.Checkpoint.TargetHeight
		working.lastCkptHash = b.Checkpoint.TargetHash
		working.haveCkpt = true
	}

	working.activatePolicies(b.Height)
	c.state = working
	return nil
}

func (c *Chain) validateGenesis(b *Block) error {
	if len(b.Records) != 1 || b.Records[0].Type != RecordGenesis {
		return ErrGenesisMixed
	}
	rec := b.Records[0]
	var p GenesisPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("genesis payload: %w", err)
	}
	if p.QuorumM < 1 || p.QuorumM > len(p.AdminKeys) {
		return fmt.Errorf("genesis quorum %d invalid for %d admins", p.QuorumM, len(p.AdminKeys))
	}
	if p.RuleVersion == 0 {
		return fmt.Errorf("genesis rule version must be >= 1")
	}

	mat := recordSigningMaterial(rec, ZeroHash)
	valid := map[string]bool{}
	if verifySig(rec.Issuer, mat, rec.Sig) {
		valid[ID(rec.Issuer)] = true
	}
	keySet := map[string][]byte{}
	for _, k := range p.AdminKeys {
		keySet[ID(k)] = k
	}
	for _, ap := range rec.Approvals {
		if k, ok := keySet[ID(ap.Admin)]; ok && verifySig(k, mat, ap.Sig) {
			valid[ID(ap.Admin)] = true
		}
	}
	if len(valid) < p.QuorumM {
		return fmt.Errorf("genesis approvals %d < quorum %d", len(valid), p.QuorumM)
	}
	if _, ok := keySet[ID(b.Proposer)]; !ok || !verifySig(b.Proposer, b.proposerMaterial(), b.ProposerSig) {
		return fmt.Errorf("genesis proposer must be a founding admin")
	}

	working := c.state.Clone()
	if err := working.applyRecord(rec, 0); err != nil {
		return err
	}
	c.state = working
	return nil
}

// checkRecordCaps enforces the per-member record cap within one block.
func (c *Chain) checkRecordCaps(b *Block) error {
	counts := map[string]int{}
	for _, rec := range b.Records {
		counts[ID(rec.Issuer)]++
	}
	for id, n := range counts {
		if n > MaxRecordsPerMemberPerBlock {
			return fmt.Errorf("member %s issued %d records in one block (cap %d)",
				id, n, MaxRecordsPerMemberPerBlock)
		}
	}
	return nil
}

// verifyRecord checks signatures and the write-permission matrix (spec §6.1)
// against the state at the record's position. Effects are applied separately.
func verifyRecord(rec *Record, parent Hash, st *State) error {
	mat := recordSigningMaterial(rec, parent)

	switch rec.Type {
	case RecordMember:
		var p MemberPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return err
		}
		if p.MemberID != rec.Subject {
			return fmt.Errorf("subject/payload member ID mismatch")
		}
		if ID(p.PublicKey) != p.MemberID {
			return fmt.Errorf("member ID does not match public key")
		}
		return checkQuorum(rec, mat, st)

	case RecordRevocation:
		var p RevocationPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return err
		}
		if p.TargetID != rec.Subject {
			return fmt.Errorf("subject/payload target ID mismatch")
		}
		return checkQuorum(rec, mat, st)

	case RecordPolicy, RecordTerminal, RecordPause:
		return checkQuorum(rec, mat, st)

	case RecordToken:
		var p TokenPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return err
		}
		if p.TokenID != rec.Subject {
			return fmt.Errorf("subject/payload token ID mismatch")
		}
		if p.ParentTokenID != "" {
			// Delegated sub-token (spec §13.2): the HOLDER of the parent
			// derives a narrowed grant. No admin needed — but the ledger
			// checks the derivation rules structurally.
			issuer := ID(rec.Issuer)
			if !st.IsMember(issuer) {
				return fmt.Errorf("delegating issuer %s is not an active member", issuer)
			}
			parent := st.Token(p.ParentTokenID)
			if parent == nil {
				return fmt.Errorf("parent token %s unknown", p.ParentTokenID)
			}
			if parent.SubjectID != issuer {
				return fmt.Errorf("only the holder may delegate a token")
			}
			if parent.ParentTokenID != "" {
				return fmt.Errorf("delegation depth exceeds one level")
			}
			if parent.Resource != "*" && parent.Resource != p.Resource {
				return fmt.Errorf("delegated resource outside parent scope")
			}
			if !opsWithin(p.Operations, parent.Operations) {
				return fmt.Errorf("delegated operations outside parent scope")
			}
			if parent.ExpiresAt != 0 {
				if p.ExpiresAt == 0 || p.ExpiresAt > parent.ExpiresAt {
					return fmt.Errorf("delegated token must expire before its parent")
				}
				if rec.Timestamp > parent.ExpiresAt {
					return fmt.Errorf("parent token already expired at issuance")
				}
			}
		} else if !st.IsAdmin(ID(rec.Issuer)) {
			return fmt.Errorf("token issuer %s is not an admin", ID(rec.Issuer))
		}
		if !verifySig(rec.Issuer, mat, rec.Sig) {
			return errBadSig
		}
		return nil

	case RecordSuccession:
		// Dead-man promotion (spec §13.2): valid WITHOUT admin quorum —
		// that is the feature. The determinism trick: the timeout compares
		// the record's own signed timestamp against the chain-derived
		// last-admin-activity, so every validator of the same chain agrees.
		var p SuccessionPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			return err
		}
		if st.SuccessionAfterSec == 0 {
			return fmt.Errorf("no succession policy configured")
		}
		if !containsID(st.SuccessionMembers, p.MemberID) {
			return fmt.Errorf("member %s is not a configured successor", p.MemberID)
		}
		if rec.Timestamp < st.lastAdminActivityTs+uint64(st.SuccessionAfterSec) {
			return fmt.Errorf("admins still active (dead-man clock not elapsed)")
		}
		if !st.IsMember(p.MemberID) {
			return fmt.Errorf("successor %s is not an active member", p.MemberID)
		}
		if !st.IsMember(ID(rec.Issuer)) {
			return fmt.Errorf("succession issuer is not an active member")
		}
		if !verifySig(rec.Issuer, mat, rec.Sig) {
			return errBadSig
		}
		return nil

	case RecordAttestation, RecordAuditAnchor:
		issuer := ID(rec.Issuer)
		if rec.Subject != issuer {
			return fmt.Errorf("%s must be self-signed (subject %s != issuer %s)", rec.Type, rec.Subject, issuer)
		}
		if !st.IsMember(issuer) {
			return fmt.Errorf("%s issuer %s is not an active member", rec.Type, issuer)
		}
		if !verifySig(rec.Issuer, mat, rec.Sig) {
			return errBadSig
		}
		return nil

	default:
		return fmt.Errorf("unknown or misplaced record type %s", rec.Type)
	}
}

// opsWithin: every wanted op is covered by the parent's set (exact or "*").
func opsWithin(want, parent []string) bool {
	for _, w := range want {
		covered := false
		for _, p := range parent {
			if p == "*" || p == w {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// checkQuorum counts DISTINCT valid admin signatures (issuer included when
// the issuer is an admin) over the record material and requires QuorumM.
// Non-admin or revoked signers contribute nothing — the glass-house rule:
// every power-structure change needs live majority approval (spec §6.1/6.3).
func checkQuorum(rec *Record, mat []byte, st *State) error {
	valid := map[string]bool{}
	if st.IsAdmin(ID(rec.Issuer)) && verifySig(rec.Issuer, mat, rec.Sig) {
		valid[ID(rec.Issuer)] = true
	}
	for _, ap := range rec.Approvals {
		id := ID(ap.Admin)
		if !st.IsAdmin(id) {
			continue
		}
		if m := st.Member(id); m == nil || !verifySig(m.PublicKey, mat, ap.Sig) {
			continue
		}
		valid[id] = true
	}
	if len(valid) < st.QuorumM {
		return fmt.Errorf("quorum not met: %d/%d valid admin signatures", len(valid), st.QuorumM)
	}
	return nil
}

func (c *Chain) validateCheckpoint(ck *Checkpoint, st *State) error {
	// Admins sign the current head, so the checkpoint rides on the NEXT
	// block: target may be the parent or earlier, never this block or later.
	if ck.TargetHeight > c.blocks[len(c.blocks)-1].Height {
		return fmt.Errorf("checkpoint target %d not yet in chain", ck.TargetHeight)
	}
	h, ok := c.HashAt(ck.TargetHeight)
	if !ok || h != ck.TargetHash {
		return fmt.Errorf("checkpoint target %s@%d not in chain", ck.TargetHash.Hex(), ck.TargetHeight)
	}
	mat := checkpointMaterial(ck.TargetHeight, ck.TargetHash)
	valid := map[string]bool{}
	for _, ap := range ck.Sigs {
		id := ID(ap.Admin)
		if !st.IsAdmin(id) {
			continue
		}
		if m := st.Member(id); m == nil || !verifySig(m.PublicKey, mat, ap.Sig) {
			continue
		}
		valid[id] = true
	}
	if len(valid) < st.QuorumM {
		return fmt.Errorf("checkpoint quorum not met: %d/%d", len(valid), st.QuorumM)
	}
	return nil
}

// BlockRevokes reports whether block b contains a revocation of member id.
// The transport layer uses this to implement revocation-on-sight for blocks
// seen in gossip before they are checkpointed (spec §6.4: 撤销见即生效).
func BlockRevokes(b *Block, id string) bool {
	for _, rec := range b.Records {
		if rec.Type != RecordRevocation {
			continue
		}
		var p RevocationPayload
		if json.Unmarshal(rec.Payload, &p) == nil && p.TargetID == id {
			return true
		}
	}
	return false
}

// ForkChoice returns the preferred chain (spec §5.5): most recent valid
// checkpoint wins; then length; then higher head hash — fully deterministic.
func ForkChoice(a, b *Chain) *Chain {
	ah, _, aok := a.LastCheckpoint()
	bh, _, bok := b.LastCheckpoint()
	if aok != bok {
		if aok {
			return a
		}
		return b
	}
	if aok && ah != bh {
		if ah > bh {
			return a
		}
		return b
	}
	if a.Height() != b.Height() {
		if a.Height() > b.Height() {
			return a
		}
		return b
	}
	if a.HeadHash().Hex() >= b.HeadHash().Hex() {
		return a
	}
	return b
}
