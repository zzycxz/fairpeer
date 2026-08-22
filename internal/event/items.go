// items.go — upgrade spec 4-1 Step 1: the item-based event model that will
// eventually replace the flat Kind enum. In Step 1 the agent emits BOTH the
// old Kind events (for every existing sink) and ItemEvents (for new
// consumers: mobile bridge, future remote). The dual-track is zero-breaking:
// old sinks simply ignore ItemEvents on the wire.
//
// An "item" is one thing on the timeline the user sees — one user message,
// one tool call card, one reasoning block, one notice. Items have a stable
// ID and a three-phase lifecycle:
//
//   item_started    → the item exists (card appears)
//   item_delta      → incremental content (streaming text, partial patch)
//   item_completed  → the item is final (card settles)
//
// This maps 1:1 to how the desktop reducer already works internally; the
// ItemEvent form makes that structure explicit on the wire so any frontend
// (desktop, mobile, remote) can render without translating from 18 flat
// event kinds.
package event

import "encoding/json"

// ItemKind identifies what a timeline item IS (not what happened to it).
type ItemKind string

const (
	ItemUserMessage  ItemKind = "user_message"
	ItemAgentMessage ItemKind = "agent_message"
	ItemReasoning    ItemKind = "reasoning"
	ItemToolCall     ItemKind = "tool_call"
	ItemApproval     ItemKind = "approval"
	ItemCompaction   ItemKind = "compaction"
	ItemNotice       ItemKind = "notice"
	ItemTurnSummary  ItemKind = "turn_summary"
	ItemPhase        ItemKind = "phase"
)

// ItemPhaseTransition identifies which lifecycle phase an ItemEvent carries.
type ItemPhaseTransition string

const (
	ItemStarted   ItemPhaseTransition = "item_started"
	ItemDelta     ItemPhaseTransition = "item_delta"
	ItemCompleted ItemPhaseTransition = "item_completed"
)

// ItemEvent is the wire form of a timeline item transition. It rides on
// Event as a JSON payload, so existing sinks that don't know about it can
// skip it without any code change.
type ItemEvent struct {
	Phase    ItemPhaseTransition `json:"phase"`
	ItemID   string              `json:"item_id"`
	ItemKind ItemKind            `json:"item_kind"`
	// Delta carries the incremental text for ItemDelta (streaming).
	Delta string `json:"delta,omitempty"`
	// Item carries the full item payload on ItemStarted/ItemCompleted.
	// Structure depends on ItemKind; consumers switch on ItemKind.
	Item json.RawMessage `json:"item,omitempty"`
}

// AgentMessageItem is the payload for ItemAgentMessage.
type AgentMessageItem struct {
	Text      string `json:"text,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// ToolCallItem is the payload for ItemToolCall.
type ToolCallItem struct {
	Name      string     `json:"name"`
	Args      string     `json:"args,omitempty"`
	Output    string     `json:"output,omitempty"`
	Err       string     `json:"err,omitempty"`
	FileDiff  *FileDiff  `json:"file_diff,omitempty"`
	ReadOnly  bool       `json:"read_only"`
	DurationMs int64     `json:"duration_ms,omitempty"`
	Status    string     `json:"status"` // running | done | error | stopped
}

// NoticeItem is the payload for ItemNotice.
type NoticeItem struct {
	Level string `json:"level"` // info | warn
	Text  string `json:"text"`
}

// ApprovalItem is the payload for ItemApproval.
type ApprovalItem struct {
	Tool    string          `json:"tool"`
	Subject string          `json:"subject"`
	Changes []FileChange    `json:"changes,omitempty"`
}

// CompactionItem is the payload for ItemCompaction.
type CompactionItem struct {
	Trigger  string `json:"trigger"`
	Messages int    `json:"messages,omitempty"`
	Summary  string `json:"summary,omitempty"`
}
