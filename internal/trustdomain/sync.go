package trustdomain

import (
	"encoding/json"
	"fmt"
)

// Status is the lightweight announcement peers exchange before transferring
// blocks (spec §5.5): height, head, and the last checkpoint. Everything the
// sync planner can know without shipping blocks.
type Status struct {
	Height     uint64 `json:"height"`
	Head       Hash   `json:"head"`
	HaveCkpt   bool   `json:"have_ckpt"`
	CkptHeight uint64 `json:"ckpt_height"`
	CkptHash   Hash   `json:"ckpt_hash"`
}

// StatusOf builds the announcement for a chain.
func StatusOf(c *Chain) Status {
	h, ck, _ := c.LastCheckpoint()
	return Status{
		Height:     c.Height(),
		Head:       c.HeadHash(),
		HaveCkpt:   h != 0 || ck != (Hash{}),
		CkptHeight: h,
		CkptHash:   ck,
	}
}

// StatusFromJSON decodes a peer announcement.
func StatusFromJSON(data []byte) (Status, error) {
	var st Status
	if err := json.Unmarshal(data, &st); err != nil {
		return st, err
	}
	return st, nil
}

// SyncPlan is what PlanSync decided: which block range to request (if any),
// whether the chains diverged, and the highest block the local side can
// serve back. Ranges are inclusive [from, to].
type SyncPlan struct {
	Need       bool
	NeedFrom   uint64
	NeedTo     uint64
	Diverged   bool   // heads differ at equal height
	FullResync bool   // no common checkpoint identifiable — restart from genesis
	OfferTo    uint64 // local tip height; serve [0, OfferTo]
}

// PlanSync decides the next exchange between local and a peer's announced
// status. Blocks requested are then verified on arrival — the plan trusts
// nothing, it only minimizes traffic (a lying announcement costs the peer
// bandwidth and the validator rejects bad blocks anyway).
func PlanSync(local, remote Status) SyncPlan {
	plan := SyncPlan{OfferTo: local.Height}

	switch {
	case remote.Head == local.Head:
		return plan // in sync

	case remote.Height > local.Height:
		// Peer is ahead; request what we are missing. If the peer is on a
		// different branch we find out at validation time and fall back to a
		// full resync — an honest network makes this rare.
		plan.Need = true
		plan.NeedFrom = local.Height + 1
		plan.NeedTo = remote.Height
		return plan

	case remote.Height < local.Height:
		return plan // we are ahead; the peer will request from us

	default: // equal height, different heads: forked
		plan.Diverged = true
		plan.Need = true
		// Resync from the last checkpoint we provably share; without a
		// shared checkpoint there is no anchor to trust — full resync.
		if local.HaveCkpt && remote.HaveCkpt && local.CkptHash == remote.CkptHash {
			plan.NeedFrom = local.CkptHeight + 1
			plan.NeedTo = remote.Height
		} else {
			plan.FullResync = true
			plan.NeedFrom = 0
			plan.NeedTo = remote.Height
		}
		return plan
	}
}

// TryExtend appends as many consecutive blocks (each building on the current
// tip) as it can; returns how many applied and the error that stopped the
// run (nil if all applied). A stopping error usually means the announcement
// is a fork candidate — hand it to MergeFork.
func TryExtend(c *Chain, blocks []*Block) (int, error) {
	applied := 0
	for _, b := range blocks {
		if err := c.Append(b); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}

// MergeFork independently validates a candidate chain and switches to it if
// (and only if) it belongs to the same domain and wins fork choice. Returns
// the winning chain and whether a switch happened.
func MergeFork(local *Chain, candidateBlocks []*Block) (*Chain, bool, error) {
	cand, err := ValidateChain(candidateBlocks)
	if err != nil {
		return local, false, err
	}
	if DomainID(cand) != DomainID(local) {
		return local, false, fmt.Errorf("trustdomain: candidate belongs to a different domain")
	}
	if ForkChoice(local, cand) == cand {
		return cand, true, nil
	}
	return local, false, nil
}

// SightState folds pending (unconfirmed, possibly forked) blocks into a
// defensive state view: revocations seen anywhere in the pending set take
// effect immediately — 撤销见即生效, 宁误杀可恢复 (a canonical re-admission
// clears the mark), 不漏杀 (spec §6.4). The canonical chain is untouched.
// Token checks against this view are what an agent consults before acting on
// gossip-seen information.
func SightState(c *Chain, pending []*Block) *State {
	st := c.State() // already a clone
	for _, b := range pending {
		for _, rec := range b.Records {
			if rec.Type != RecordRevocation {
				continue
			}
			var p RevocationPayload
			if json.Unmarshal(rec.Payload, &p) == nil && p.TargetID != "" {
				st.forceRevoke(p.TargetID, b.Height)
			}
		}
	}
	return st
}
