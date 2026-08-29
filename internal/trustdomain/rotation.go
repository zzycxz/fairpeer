package trustdomain

// ProposerFor returns the member ID whose turn it is to propose `height`,
// given the member set as of the parent block: round-robin over the active
// members sorted by ID (deterministic; height-1 because genesis is height 0,
// built by the founding process).
//
// Validation deliberately does NOT enforce rotation. Whether the designated
// proposer was offline is a timing fact the validator cannot observe from
// the chain alone (spec §5.5: offline proposers are skipped, the next member
// takes the height). An eager out-of-turn block is therefore still *valid* —
// it simply forks, and forks lose to the checkpointed chain (ForkChoice).
// Rotation is a scheduling courtesy that keeps one honest proposer per
// height in the common case, not a validity rule.
func ProposerFor(st *State, height uint64) string {
	ids := st.MemberIDs()
	if len(ids) == 0 || height == 0 {
		return ""
	}
	return ids[(height-1)%uint64(len(ids))]
}

// IsProposerTurn reports whether memberID holds the proposal slot for height.
func IsProposerTurn(st *State, height uint64, memberID string) bool {
	return ProposerFor(st, height) == memberID
}
