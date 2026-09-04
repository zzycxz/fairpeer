// Package mobilebridge bridges fairpeer's desktop Controller to linkpeer
// mobile clients over an end-to-end-encrypted WebRTC P2P link.
//
// Status (2026-09): the desktop-side bridge is implemented and wired into
// the main app — Ed25519 identity + pairing, signaling client (primary
// long-poll and cloud-relay second path), AES-GCM framing with rekey and
// tab-ring resync, per-command permission routing, UPnP/TURN hole punching,
// and an audit log; cmd/linkpeer-signal is the standalone signaling server.
// The Flutter mobile app lives in a separate repository and is still in
// development. See docs/LINKPEER_FAIRPEER_SPEC.md and LINKPEER.md.
package mobilebridge
