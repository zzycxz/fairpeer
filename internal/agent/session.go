// Package agent wires a Provider, a tool Registry, and a Session into the
// harness loop that drives a coding task to completion.
package agent

import (
	"errors"
	"sync"

	"github.com/zzycxz/fairpeer/internal/provider"
)

// ErrRewriteContended reports a detached log rewrite (compact/summarize/prune)
// that was abandoned because the run loop appended messages or another rewrite
// landed after the rewrite snapshotted the log — applying the stale result
// would silently drop them (code review 2026-09-07, AGENT-1). Retry-safe.
var ErrRewriteContended = errors.New("message log changed during rewrite; retry")

// Session holds the conversation history for one task. The run loop (one turn at
// a time) is the only writer, but a frontend can read History/Save from another
// goroutine while a turn appends, so mu guards Messages. Direct Messages reads on
// the run-loop goroutine stay lock-free (serial with its own writes); cross-
// goroutine access goes through Snapshot.
type Session struct {
	mu             sync.RWMutex
	Messages       []provider.Message
	rewriteVersion int // bumped each time the log is rewritten (compact/fold)
}

// NewSession initializes a session with an optional system prompt.
func NewSession(system string) *Session {
	s := &Session{}
	if system != "" {
		s.Messages = append(s.Messages, provider.Message{Role: provider.RoleSystem, Content: system})
	}
	return s
}

// Add appends a message.
func (s *Session) Add(m provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = append(s.Messages, m)
}

// Replace swaps the whole message log unconditionally and counts as a
// rewrite (bumps the version, so concurrent ReplaceIfUnchanged callers
// correctly refuse). Only safe on the run loop itself (serial with its own
// appends); detached rewrites (compact / summarize / prune) must use
// ReplaceIfUnchanged — an unconditional Replace from a stale snapshot
// silently drops concurrently appended messages, and the loss persists to
// disk.
func (s *Session) Replace(msgs []provider.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Messages = msgs
	s.rewriteVersion++
}

// ReplaceIfUnchanged swaps the whole message log only when nothing touched it
// since the caller's snapshot: seenVersion/seenLen are the RewriteVersion and
// message count captured BEFORE taking the snapshot (read the version first —
// a rewrite landing between the two then fails the CAS instead of succeeding
// against a stale version). Returns false — log untouched — when the run loop
// appended messages or another rewrite landed in between. Success bumps the
// rewrite version, so callers must not call IncrementRewrite separately.
func (s *Session) ReplaceIfUnchanged(msgs []provider.Message, seenVersion, seenLen int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rewriteVersion != seenVersion || len(s.Messages) != seenLen {
		return false
	}
	s.Messages = msgs
	s.rewriteVersion++
	return true
}

// Snapshot returns a copy of the messages, safe to read from another goroutine
// while a turn appends. Frontends (History, Save) use it instead of touching the
// live slice.
func (s *Session) Snapshot() []provider.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]provider.Message(nil), s.Messages...)
}

// RewriteVersion returns the current rewrite version.
func (s *Session) RewriteVersion() int { return s.rewriteVersion }

// IncrementRewrite bumps the rewrite version by 1.
func (s *Session) IncrementRewrite() { s.rewriteVersion++ }

// HasContent returns true when the session carries at least one user,
// assistant, or tool message — i.e. more than just a system prompt. An
// "empty" conversation that has never been used should not be persisted.
func (s *Session) HasContent() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.Messages {
		if m.Role != provider.RoleSystem {
			return true
		}
	}
	return false
}
