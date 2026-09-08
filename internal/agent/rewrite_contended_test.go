package agent

// rewrite_contended_test.go — AGENT-1 红测试（code review 2026-09-07）：脱离
// run loop 的日志重写（compact/summarize/prune）必须在快照后日志被并发追加
// 或再次重写时放弃落盘，否则并发追加的消息被静默丢弃且持久化。

import (
	"errors"
	"testing"

	"github.com/zzycxz/fairpeer/internal/provider"
)

func snapshotView(s *Session) (version int, msgs []provider.Message) {
	// The capture order the rewrites use: version FIRST, then snapshot.
	version = s.RewriteVersion()
	msgs = s.Snapshot()
	return version, msgs
}

func TestReplaceIfUnchangedHappyPath(t *testing.T) {
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "one"})
	v0, msgs := snapshotView(s)

	rewritten := []provider.Message{msgs[0], {Role: provider.RoleUser, Content: "folded"}}
	if !s.ReplaceIfUnchanged(rewritten, v0, len(msgs)) {
		t.Fatal("uncontended rewrite should apply")
	}
	if s.RewriteVersion() != v0+1 {
		t.Fatalf("rewrite version = %d, want %d", s.RewriteVersion(), v0+1)
	}
	if got := len(s.Snapshot()); got != 2 {
		t.Fatalf("log length = %d, want 2", got)
	}
}

func TestReplaceIfUnchangedRefusesConcurrentAppend(t *testing.T) {
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "one"})
	v0, msgs := snapshotView(s)

	// The run loop appends between the rewrite's snapshot and its Replace.
	s.Add(provider.Message{Role: provider.RoleUser, Content: "concurrent"})

	rewritten := []provider.Message{msgs[0]}
	if s.ReplaceIfUnchanged(rewritten, v0, len(msgs)) {
		t.Fatal("contended rewrite must be refused")
	}
	got := s.Snapshot()
	if len(got) != 3 {
		t.Fatalf("concurrent message was dropped: log = %d messages", len(got))
	}
	if provider.ContentString(got[2].Content) != "concurrent" {
		t.Fatalf("appended message not preserved: %+v", got[2])
	}
}

func TestReplaceIfUnchangedRefusesInterleavedRewrite(t *testing.T) {
	s := NewSession("sys")
	s.Add(provider.Message{Role: provider.RoleUser, Content: "one"})
	v0, msgs := snapshotView(s)

	// Another rewrite lands between the snapshot and this one's Replace.
	s.Replace([]provider.Message{msgs[0], {Role: provider.RoleUser, Content: "other rewrite"}})

	if s.ReplaceIfUnchanged([]provider.Message{msgs[0]}, v0, len(msgs)) {
		t.Fatal("rewrite racing another rewrite must be refused")
	}
	if got := provider.ContentString(s.Snapshot()[1].Content); got != "other rewrite" {
		t.Fatalf("winning rewrite clobbered: %q", got)
	}
}

func TestErrRewriteContendedIsSentinel(t *testing.T) {
	if !errors.Is(ErrRewriteContended, ErrRewriteContended) {
		t.Fatal("sentinel must support errors.Is")
	}
}
