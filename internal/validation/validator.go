// Package validation provides pre-write syntax validation for the editor
// tools (write_file/edit_file/multi_edit). It catches syntax errors BEFORE the
// file is written, so the agent gets an error and can fix it without corrupting
// the file on disk (SPEC v2 §3.3).
//
// Design constraints (SPEC v2 §2.0):
//   - Zero user learning cost: always on for the languages we support, the user
//     never configures anything. Only fires when there's actually an error.
//   - No prompt bloat: pure host logic (stdlib parsers), the model only sees the
//     returned error string; nothing is added to the system prompt.
//   - No new dependencies: Go uses go/parser (already imported by codeindex),
//     JSON uses encoding/json. YAML/Python are intentionally omitted (would need
//     a new dep / external process) — they fall through to "no check".
//   - Fast: <100ms target. Go parse with AllErrors off stops at the first error;
//     JSON validity is a single-pass scan.
package validation

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// SyntaxError describes a pre-write syntax validation failure. It carries the
// path so the tool can surface "file.go:12: syntax error" to the model.
type SyntaxError struct {
	Path    string
	Line    int    // 1-based; 0 if unavailable (JSON)
	Message string // the parser's error message
}

func (e *SyntaxError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.Path, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// ValidateSyntax checks content for syntax errors based on the file extension.
// It returns nil when the content is valid (or the extension is unrecognized —
// we never block writes to languages we don't check). A non-nil *SyntaxError
// means the write MUST be refused so the file isn't corrupted on disk.
//
// Supported: .go (go/parser), .json (encoding/json). All other extensions pass
// through (no check) — adding a language is a new case here, no caller change.
func ValidateSyntax(path, content string) error {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return validateGo(path, content)
	case ".json":
		return validateJSON(path, content)
	}
	return nil // unrecognized extension: no check, allow the write
}

// validateGo parses the content with go/parser. parser.ParseFile with no
// AllErrors flag stops at the first syntax error (fast). We parse from the
// content string (not the file) so we validate the PROPOSED content, not the
// stale on-disk version — this is what makes it a true pre-write check.
func validateGo(path, content string) error {
	fset := token.NewFileSet()
	// ParseFile with src=content validates the in-memory content. The path is
	// passed only for error positioning; no file read happens.
	_, err := parser.ParseFile(fset, path, content, parser.SkipObjectResolution)
	if err == nil {
		return nil
	}
	// go/parser errors are typically *scanner.Error or *scanner.ErrorList with
	// Pos/Msg. Extract a clean one-line message + line number for the model.
	msg := strings.TrimSpace(err.Error())
	line := extractGoLine(msg)
	// Trim the leading "path:line:col:" prefix the parser adds, since SyntaxError
	// re-adds path:line itself.
	msg = stripGoPosPrefix(msg)
	return &SyntaxError{Path: path, Line: line, Message: msg}
}

// validateJSON does a single-pass validity scan. json.Unmarshal into any is the
// cheapest full validation (json.Valid would also work but gives no position).
func validateJSON(path, content string) error {
	// An empty file is technically invalid JSON, but many tools write empty
	// .json files as initialization — don't block that.
	if strings.TrimSpace(content) == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(content), &v); err != nil {
		return &SyntaxError{Path: path, Line: 0, Message: strings.TrimSpace(err.Error())}
	}
	return nil
}

// extractGoLine pulls the line number from a go/parser error message of the
// form "path:line:col: message". Returns 0 if it can't parse one.
func extractGoLine(msg string) int {
	// Find the first colon after the path, then the digits before the next colon.
	// "foo.go:12:5: expected ..." → 12.
	parts := strings.SplitN(msg, ":", 4)
	if len(parts) >= 3 {
		var line int
		for _, r := range parts[1] {
			if r >= '0' && r <= '9' {
				line = line*10 + int(r-'0')
			} else {
				return 0
			}
		}
		return line
	}
	return 0
}

// stripGoPosPrefix removes the "path:line:col: " prefix that go/parser prepends,
// since SyntaxError re-adds path:line. Leaves the actual error text.
func stripGoPosPrefix(msg string) string {
	parts := strings.SplitN(msg, ":", 4)
	if len(parts) >= 4 {
		return strings.TrimSpace(parts[3])
	}
	// Fallback: if the prefix shape doesn't match, keep the trimmed message but
	// drop a leading filename if present.
	return msg
}
