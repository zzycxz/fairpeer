package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzycxz/fairpeer/internal/tool"
)

// session_search gives the MODEL a way to find and re-read past sessions
// (P1-B2). "按我们上次定的方案来" used to leave the model empty-handed — the
// UI had a search box (agent.SearchSessions) but the capability was never
// exposed as a tool, so the user had to play retriever themselves.
//
// Two modes:
//   - search: keyword across the session dir (case-insensitive, excerpts),
//     excluding the CURRENT session (it contains the query itself — a self
//     match is pure noise).
//   - read: render one session as a readable transcript (role + text), paged
//     by message index, so the model can quote the actual prior decision.
//
// Scope is injected by boot via SetSessionSearchScope (the package default is
// empty → the tool reports offline, like calendar without a store). Sessions
// are read-only here; the netdev profile never registers this tool (its
// registry is hard-sealed, and letting it read other profiles' transcripts
// would be a cross-profile leak).

var (
	sessionSearchDir  string
	sessionSearchSelf string
)

// SetSessionSearchScope installs the searchable session directory and the
// current session's path (excluded from search hits).
func SetSessionSearchScope(dir, currentSessionPath string) {
	sessionSearchDir, sessionSearchSelf = dir, currentSessionPath
}

// SetSessionSearchSelf updates just the current-session exclusion path — the
// controller calls it whenever the active session file changes (new session,
// resume, branch switch), keeping the self-exclusion accurate without boot
// having to know the final path up front.
func SetSessionSearchSelf(currentSessionPath string) {
	sessionSearchSelf = currentSessionPath
}

type sessionSearch struct{}

func init() { tool.RegisterBuiltin(sessionSearch{}) }

// SessionSearchTool returns the session_search instance boot adds explicitly
// (the init-registered zero value is the same tool; the explicit add exists so
// registration can stay profile-gated in boot).
func SessionSearchTool() tool.Tool { return sessionSearch{} }

func (sessionSearch) Name() string { return "session_search" }

func (sessionSearch) Description() string {
	return `Search PAST chat sessions of this workspace/user and re-read them. Use when the user refers to earlier work ("按上次的方案", "we discussed this before") — find the prior decision instead of guessing.
- mode=search (default): keyword search across session titles + full transcripts. Returns matching sessions with title, activity time, path and short excerpts.
- mode=read: render one session (path from a search hit) as a readable transcript (user/assistant turns), paged by message offset/limit.
Sessions are read-only. The current session is excluded from search results.`
}

func (sessionSearch) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "mode":{"type":"string","enum":["search","read"],"description":"search (default): find sessions by keyword; read: render one session's transcript."},
  "query":{"type":"string","description":"search mode: keyword (case-insensitive)."},
  "path":{"type":"string","description":"read mode: session file path from a prior search hit."},
  "offset":{"type":"integer","description":"read mode: 0-based message index to start at (default 0)."},
  "limit":{"type":"integer","description":"read mode: max messages to render (default 40)."}
},
"required":[]
}`)
}

func (sessionSearch) ReadOnly() bool { return true }

func (sessionSearch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Mode   string `json:"mode"`
		Query  string `json:"query"`
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(sessionSearchDir) == "" {
		return "", fmt.Errorf("session search is offline (no session directory configured)")
	}
	switch strings.ToLower(strings.TrimSpace(p.Mode)) {
	case "", "search":
		return searchSessionsForModel(p.Query)
	case "read":
		return readSessionForModel(p.Path, p.Offset, p.Limit)
	default:
		return "", fmt.Errorf("unknown mode %q (use search or read)", p.Mode)
	}
}

func searchSessionsForModel(query string) (string, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 {
		return "", fmt.Errorf("query too short (min 2 chars)")
	}
	hits := SearchSessions(sessionSearchDir, query)
	// Drop the current session: its transcript contains the query itself, so a
	// self-match is pure noise (and its content is already in context).
	if self := strings.TrimSpace(sessionSearchSelf); self != "" {
		kept := hits[:0]
		for _, h := range hits {
			if h.Path != self {
				kept = append(kept, h)
			}
		}
		hits = kept
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No past session matches %q. Ask the user for details instead of guessing a prior plan.", query), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Sessions matching %q (most recent first, current session excluded):\n", query)
	for i, h := range hits {
		title := h.Title
		if strings.TrimSpace(title) == "" {
			title = filepath.Base(h.Path)
		}
		fmt.Fprintf(&b, "\n%d. %s", i+1, title)
		if h.Scope != "" || h.Profile != "" {
			fmt.Fprintf(&b, "  [%s %s]", strings.TrimSpace(h.Scope+" "+h.Profile), "")
		}
		fmt.Fprintf(&b, "\n   path: %s", h.Path)
		for _, e := range h.Excerpts {
			fmt.Fprintf(&b, "\n   …%s", strings.ReplaceAll(e, "\n", " "))
		}
	}
	b.WriteString("\n\nOpen one with session_search mode=read path=<path> to quote the actual conversation.")
	return b.String(), nil
}

// readSessionForModel renders one transcript paged by message index. The path
// must live under the configured session dir and end in .jsonl — the tool
// reads other conversations, so it must not become an arbitrary-file reader.
func readSessionForModel(path string, offset, limit int) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("read mode requires path (take it from a search hit)")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(sessionSearchDir)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs+string(filepath.Separator), root+string(filepath.Separator)) || filepath.Ext(abs) != ".jsonl" {
		return "", fmt.Errorf("path must be a .jsonl session under %s", root)
	}
	if abs == sessionSearchSelf {
		return "", fmt.Errorf("that is the CURRENT session — its content is already in your context")
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("open session: %w", err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	var b strings.Builder
	idx, shown := 0, 0
	for {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := dec.Decode(&m); err != nil {
			break // tolerate a torn tail like LoadSession
		}
		if idx < offset {
			idx++
			continue
		}
		if shown >= limit {
			fmt.Fprintf(&b, "\n…(more messages follow — page with offset=%d)", offset+shown)
			break
		}
		text := contentText(m.Content)
		if len(text) > 1500 {
			text = text[:1500] + " …(cut)"
		}
		if strings.TrimSpace(text) != "" {
			fmt.Fprintf(&b, "[%s] %s\n\n", strings.ToUpper(m.Role[:min(1, len(m.Role))])+m.Role[min(1, len(m.Role)):], text)
		}
		shown++
		idx++
	}
	if shown == 0 {
		return "", fmt.Errorf("no messages at offset %d (session may be shorter)", offset)
	}
	return fmt.Sprintf("Transcript %s (messages %d..%d):\n\n%s", filepath.Base(abs), offset, offset+shown-1, b.String()), nil
}

// contentText pulls displayable text out of a message content field (string
// or OpenAI-style parts array).
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var out []string
		for _, p := range parts {
			if p.Text != "" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
