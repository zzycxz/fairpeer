// Package trustdomain implements the private-network trust domain ledger
// defined by docs/TRUSTDOMAIN_SPEC.md: a permissioned, quorum-signed
// replicated log carrying member admissions, revocations, capability tokens,
// audit anchors and self-attestations.
//
// Design invariants (spec §1.4) enforced by this package:
//   - control-plane metadata only; payloads are type-checked structs
//   - no token, no mining: sybil resistance comes from admin-signed admission
//   - revocation-on-sight: a revocation takes effect at its block height,
//     never deferred to checkpoint confirmation
//   - every member verifies everything: ValidateChain is self-contained and
//     offline-capable (no external clock or service)
//
// The package is transport-agnostic: gossip and peer channels (spec §四) plug
// in later; this core only produces and validates blocks.
package trustdomain
