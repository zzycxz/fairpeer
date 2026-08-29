package trustdomain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

func hexOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// Peer is one reachable domain member as seen from this node: a narrow,
// synchronous request/response surface. The production implementation rides
// the mobilebridge E2E channels (spec §四); tests use an in-memory fabric.
// The transport layer owns threading, retransmission and ordering — the Node
// itself is single-goroutine by design.
type Peer interface {
	PeerID() string
	Status() Status
	// Blocks serves the inclusive [from, to] range from its chain; may
	// return fewer than requested (caller re-plans).
	Blocks(from, to uint64) []*Block
	// Approve co-signs a quorum record bound to parent. Returns nil when
	// the peer is not an admin or refuses. A production peer SHOULD verify
	// the record against its own view before signing (here: type check +
	// admin status; full policy review arrives with the RPC transport).
	Approve(rec *Record, parent Hash) *Approval
	// ApproveCkpt co-signs a checkpoint. A production peer verifies the
	// target exists in its own chain first (the in-memory fabric does).
	ApproveCkpt(ck *Checkpoint) *Approval
}

// NodeOptions tunes the node service.
type NodeOptions struct {
	// CheckpointEvery: propose a checkpoint after this many new blocks
	// (0 = checkpoints only via explicit proposal). Spec §5.5 建议 10min——
	// block-count is the chain-native clock here.
	CheckpointEvery uint64
	// Store persists the chain after every change (nil = in-memory only).
	Store *Store
	// SightWindow caps how many seen-but-unchosen candidate blocks the
	// node remembers for defensive revocation-on-sight (default 64).
	SightWindow int
}

const defaultSightWindow = 64

// Node is the domain service on one host: gossip sync, block proposal,
// approval collection, checkpoint scheduling, persistence and the defensive
// sight view. It owns no goroutines — embed it in the host's runtime loop.
type Node struct {
	id    *Identity
	chain *Chain
	peers func() []Peer
	opts  NodeOptions

	// candidates are valid blocks seen during sync that lost fork choice —
	// revocations inside them stay effective defensively (宁误杀可恢复).
	candidates []*Block
	dirty      bool

	// stamp tracks the ledger file so a long-running daemon notices blocks
	// appended by OTHER processes (one-shot CLI ops sharing the data dir)
	// and reloads instead of serving a stale chain — the single-writer
	// hazard (spec §16-9), mitigated without a control port.
	stamp ledgerStamp
}

// ledgerStamp is the (mtime, size) fingerprint of the ledger file.
type ledgerStamp struct{ mtime, size int64 }

func stampOf(path string) ledgerStamp {
	if fi, err := os.Stat(path); err == nil {
		return ledgerStamp{fi.ModTime().UnixNano(), fi.Size()}
	}
	return ledgerStamp{}
}

// NewNode wires a node over an already-valid chain (typically genesis, or
// one loaded from Store).
func NewNode(id *Identity, chain *Chain, peers func() []Peer, opts NodeOptions) *Node {
	if opts.SightWindow <= 0 {
		opts.SightWindow = defaultSightWindow
	}
	n := &Node{id: id, chain: chain, peers: peers, opts: opts}
	if opts.Store != nil {
		n.stamp = stampOf(opts.Store.Path())
	}
	return n
}

// SetPeers replaces the peer source. Wiring-time hook: listeners and peer
// addresses often exist only after the node is constructed.
func (n *Node) SetPeers(peers func() []Peer) { n.peers = peers }

// Identity returns this node's member ID.
func (n *Node) Identity() string { return n.id.ID() }

// Self returns this node's identity (needed by transports that sign as us).
func (n *Node) Self() *Identity { return n.id }

// Chain returns the node's chain (tip advances via Tick/Propose).
func (n *Node) Chain() *Chain { return n.chain }

// State returns a snapshot of the canonical domain state.
func (n *Node) State() *State { return n.chain.State() }

// Sight returns the defensive view: canonical state plus revocations seen in
// unchosen candidate blocks — what an agent should consult before acting on
// gossip-fresh information (spec §6.4).
func (n *Node) Sight() *State { return SightState(n.chain, n.candidates) }

// Tick runs one gossip round: sync from every peer, then checkpoint if due,
// then persist. Errors from individual peers do not abort the round.
func (n *Node) Tick() {
	n.reloadIfChanged()
	for _, p := range n.peers() {
		if p.PeerID() == n.Identity() {
			continue
		}
		_ = n.syncFrom(p) // a bad peer must not stall the fleet
	}
	_ = n.maybeCheckpoint()
	_ = n.persist()
}

func (n *Node) syncFrom(p Peer) error {
	plan := PlanSync(StatusOf(n.chain), p.Status())
	if !plan.Need {
		return nil
	}
	blocks := p.Blocks(plan.NeedFrom, plan.NeedTo)
	if len(blocks) == 0 {
		return nil
	}
	applied, err := TryExtend(n.chain, blocks)
	if applied > 0 {
		n.dirty = true
	}
	if err == nil {
		return nil
	}
	if applied > 0 {
		return err // partial progress; the rest re-planned next tick
	}

	// The announcement does not connect: candidate branch. Pull the peer's
	// full chain and arbitrate. Either way the blocks stay visible for
	// revocation-on-sight if they are valid.
	full := p.Blocks(0, p.Status().Height)
	winner, switched, merr := MergeFork(n.chain, full)
	if merr != nil {
		return merr
	}
	if switched {
		n.chain = winner
		n.dirty = true
	}
	n.rememberCandidates(blocks)
	return nil
}

func (n *Node) rememberCandidates(blocks []*Block) {
	n.candidates = append(n.candidates, blocks...)
	if over := len(n.candidates) - n.opts.SightWindow; over > 0 {
		n.candidates = n.candidates[over:]
	}
}

// maybeCheckpoint proposes an empty block carrying a checkpoint of the
// current head once CheckpointEvery new blocks have accumulated. Only
// admins propose; peers co-sign via ApproveCkpt until quorum.
func (n *Node) maybeCheckpoint() error {
	if n.opts.CheckpointEvery == 0 {
		return nil
	}
	st := n.chain.State()
	if !st.IsAdmin(n.id.ID()) || st.Terminal {
		return nil
	}
	base, _, ok := n.chain.LastCheckpoint()
	if ok && base >= n.chain.Height() {
		return nil // this head is already checkpointed
	}
	if !ok {
		base = 0
	}
	if n.chain.Height()-base < n.opts.CheckpointEvery {
		return nil
	}

	target := n.chain.HeadHash()
	ck, err := NewCheckpoint(n.chain.Height(), target, n.id)
	if err != nil {
		return err
	}
	for _, p := range n.peers() {
		if len(ck.Sigs) >= st.QuorumM {
			break
		}
		if ap := p.ApproveCkpt(ck); ap != nil {
			ck.Sigs = append(ck.Sigs, *ap)
		}
	}
	if len(ck.Sigs) < st.QuorumM {
		return fmt.Errorf("trustdomain: checkpoint quorum not reached (%d/%d)", len(ck.Sigs), st.QuorumM)
	}
	b, err := NewBlock(n.chain.Height()+1, target, n.id, nil)
	if err != nil {
		return err
	}
	b.Checkpoint = ck
	if err := n.chain.Append(b); err != nil {
		return err
	}
	return n.commitPersist()
}

func (n *Node) persist() error {
	if n.opts.Store == nil || !n.dirty {
		return nil
	}
	if err := n.opts.Store.Save(n.chain); err != nil {
		// Persistence failure must not corrupt the in-memory chain; the
		// next successful round retries. Propose* surfaces the error;
		// Tick keeps going (a flaky disk must not stall gossip).
		return err
	}
	n.dirty = false
	n.stamp = stampOf(n.opts.Store.Path())
	return nil
}

// reloadIfChanged picks up ledger writes from other processes on this data
// dir: stat differs → full reload+revalidate; adopt only when it is the
// same domain and at least as tall (never clobber our chain with less).
func (n *Node) reloadIfChanged() {
	if n.opts.Store == nil {
		return
	}
	st := stampOf(n.opts.Store.Path())
	if st == n.stamp {
		return
	}
	c, err := n.opts.Store.Load()
	if err != nil {
		return // unreadable/corrupt mid-write: retry next tick
	}
	if DomainID(c) != DomainID(n.chain) || c.Height() < n.chain.Height() {
		return
	}
	n.chain = c
	n.stamp = st
	n.dirty = false
}

// commitPersist marks the chain changed and persists immediately.
// Proposal paths are synchronous one-shot operations (often from CLI
// processes that never Tick) — deferring the save would silently lose
// records when the process exits.
func (n *Node) commitPersist() error {
	n.dirty = true
	return n.persist()
}

// Propose packages records into the next block immediately. build receives
// the parent hash the block will extend, so signatures are always
// position-fresh — records are parent-bound and cannot be re-used on a
// different tip (spec §5.2).
func (n *Node) Propose(build func(parent Hash) ([]*Record, error)) error {
	parent := n.chain.HeadHash()
	recs, err := build(parent)
	if err != nil {
		return err
	}
	b, err := NewBlock(n.chain.Height()+1, parent, n.id, recs)
	if err != nil {
		return err
	}
	if err := n.chain.Append(b); err != nil {
		return err
	}
	return n.commitPersist()
}

// Attest publishes this node's self-attestation (spec §5.1 公告板).
func (n *Node) Attest(p AttestationPayload, ts uint64) error {
	return n.Propose(func(parent Hash) ([]*Record, error) {
		rec := NewAttestationRecord(n.id.ID(), p, ts)
		if err := rec.SignAs(n.id, parent); err != nil {
			return nil, err
		}
		return []*Record{rec}, nil
	})
}

// AnchorAudit cross-anchors this node's local audit chain head (spec §八).
func (n *Node) AnchorAudit(auditHeadHex string, ts uint64) error {
	return n.Propose(func(parent Hash) ([]*Record, error) {
		rec := NewAuditAnchorRecord(n.id.ID(), auditHeadHex, ts)
		if err := rec.SignAs(n.id, parent); err != nil {
			return nil, err
		}
		return []*Record{rec}, nil
	})
}

// IssueToken registers a capability token. Admin-only (spec §6.1); the
// chain re-checks anyway — this just fails fast with a clearer error.
func (n *Node) IssueToken(p TokenPayload, ts uint64) error {
	if !n.chain.State().IsAdmin(n.id.ID()) {
		return fmt.Errorf("trustdomain: %s is not an admin", n.id.ID())
	}
	return n.Propose(func(parent Hash) ([]*Record, error) {
		rec := NewTokenRecord(p, ts)
		if err := rec.SignAs(n.id, parent); err != nil {
			return nil, err
		}
		return []*Record{rec}, nil
	})
}

// DelegateToken lets this node derive a narrowed sub-token (depth 1) from
// a token it holds, granting it to another member (spec §13.2 #1). The
// ledger enforces the derivation rules (subset scope, shorter life); this
// pre-check fails fast with the same semantics.
func (n *Node) DelegateToken(parentID, toMemberID, resource string, operations []string, expiresAt, ts uint64) error {
	st := n.chain.State()
	parent := st.Token(parentID)
	if parent == nil {
		return ErrTokenUnknown
	}
	if parent.SubjectID != n.id.ID() {
		return fmt.Errorf("trustdomain: only the holder may delegate a token")
	}
	if parent.ParentTokenID != "" {
		return fmt.Errorf("trustdomain: delegation depth exceeds one level")
	}
	if parent.Resource != "*" && parent.Resource != resource {
		return fmt.Errorf("trustdomain: delegated resource outside parent scope")
	}
	if !opsWithin(operations, parent.Operations) {
		return fmt.Errorf("trustdomain: delegated operations outside parent scope")
	}
	if parent.ExpiresAt != 0 && (expiresAt == 0 || expiresAt > parent.ExpiresAt) {
		return fmt.Errorf("trustdomain: delegated token must expire before its parent")
	}
	tokenID := "sub-" + hexOf(parentID+"|"+toMemberID+"|"+fmt.Sprint(ts))
	return n.Propose(func(parent Hash) ([]*Record, error) {
		rec := NewTokenRecord(TokenPayload{
			TokenID: tokenID, SubjectID: toMemberID, Resource: resource,
			Operations: operations, ExpiresAt: expiresAt, ParentTokenID: parentID,
		}, ts)
		if err := rec.SignAs(n.id, parent); err != nil {
			return nil, err
		}
		return []*Record{rec}, nil
	})
}

// PromoteSuccession proposes the dead-man promotion of a configured
// successor (spec §13.2 #2) — this node by default. Works without admin
// quorum once the chain-derived timeout has elapsed.
func (n *Node) PromoteSuccession(memberID string, ts uint64) error {
	if memberID == "" {
		memberID = n.id.ID()
	}
	st := n.chain.State()
	if due, members, _, _ := st.SuccessionDue(ts); !due {
		return fmt.Errorf("trustdomain: dead-man clock not elapsed (admins active)")
	} else if !containsID(members, memberID) {
		return fmt.Errorf("trustdomain: %s is not a configured successor", memberID)
	}
	return n.Propose(func(parent Hash) ([]*Record, error) {
		rec := NewSuccessionRecord(memberID, ts)
		if err := rec.SignAs(n.id, parent); err != nil {
			return nil, err
		}
		return []*Record{rec}, nil
	})
}

// ApproveRecord co-signs a quorum record as this node's admin identity.
// Returns nil (refusal) unless this node is an active admin AND the record
// type genuinely requires quorum. The signature binds the record to its
// parent block — a co-signature is only meaningful for that position.
func (n *Node) ApproveRecord(rec *Record, parent Hash) *Approval {
	if rec == nil || !rec.Type.requiresQuorum() {
		return nil
	}
	if !n.chain.State().IsAdmin(n.id.ID()) {
		return nil
	}
	return &Approval{
		Admin: n.id.Public,
		Sig:   n.id.Sign(recordSigningMaterial(rec, parent)),
	}
}

// ApproveCheckpoint co-signs a checkpoint after verifying its target
// exists in THIS node's chain (an admin never signs what it cannot see).
// Returns nil on refusal.
func (n *Node) ApproveCheckpoint(ck *Checkpoint) *Approval {
	if ck == nil {
		return nil
	}
	if !n.chain.State().IsAdmin(n.id.ID()) {
		return nil
	}
	if h, ok := n.chain.HashAt(ck.TargetHeight); !ok || h != ck.TargetHash {
		return nil
	}
	return &Approval{
		Admin: n.id.Public,
		Sig:   n.id.Sign(checkpointMaterial(ck.TargetHeight, ck.TargetHash)),
	}
}

// ProposeQuorum builds a power-structure record (member/revocation/policy/
// terminal) and collects co-signatures from peer admins until quorum, then
// proposes. The caller signs as issuer inside build; approvals arrive via
// the Peer surface. Fails without proposing if quorum is unreachable.
func (n *Node) ProposeQuorum(build func(parent Hash) (*Record, error)) error {
	parent := n.chain.HeadHash()
	rec, err := build(parent)
	if err != nil {
		return err
	}
	if !rec.Type.requiresQuorum() {
		return fmt.Errorf("trustdomain: %s does not require quorum; use Propose", rec.Type)
	}
	st := n.chain.State()
	have := 0
	if st.IsAdmin(ID(rec.Issuer)) {
		have = 1 // the issuer's own signature counts
	}
	for _, p := range n.peers() {
		if have >= st.QuorumM {
			break
		}
		if ap := p.Approve(rec, parent); ap != nil {
			rec.Approvals = append(rec.Approvals, *ap)
			have++
		}
	}
	if have < st.QuorumM {
		return fmt.Errorf("trustdomain: quorum not reached (%d/%d)", have, st.QuorumM)
	}
	b, err := NewBlock(n.chain.Height()+1, parent, n.id, []*Record{rec})
	if err != nil {
		return err
	}
	if err := n.chain.Append(b); err != nil {
		return err
	}
	return n.commitPersist()
}
