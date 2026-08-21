// argsdelta.go — upgrade spec 3-3: live tool-argument previews. While the
// model streams a tool call's JSON arguments, the provider forwards each raw
// fragment (ChunkToolArgsDelta). For apply_patch the arguments are a single
// JSON string (patchText) whose value IS a patch — extract the partial value
// as it arrives (no need to wait for the closing quote) and let the frontend
// render the incomplete diff. Other tools' args don't preview cheaply (their
// JSON shape isn't line-oriented), so they stay opaque until dispatch.
package agent

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// partialJSONString returns the value of key as unescaped so far from a
// (possibly incomplete) JSON object text. ok is false when the key's opening
// quote hasn't arrived yet. Escapes are decoded incrementally; a trailing
// incomplete \uXXXX yields nothing for that escape.
func partialJSONString(raw, key string) (string, bool) {
	// Locate "key" followed by ':' and the opening quote of its value.
	k := `"` + key + `"`
	i := strings.Index(raw, k)
	if i < 0 {
		return "", false
	}
	rest := raw[i+len(k):]
	j := 0
	for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t' || rest[j] == '\n' || rest[j] == '\r') {
		j++
	}
	if j >= len(rest) || rest[j] != ':' {
		return "", false
	}
	j++
	for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t' || rest[j] == '\n' || rest[j] == '\r') {
		j++
	}
	if j >= len(rest) || rest[j] != '"' {
		return "", false
	}
	j++ // past the opening quote

	var b strings.Builder
	for j < len(rest) {
		c := rest[j]
		if c == '"' {
			return b.String(), true // complete value
		}
		if c != '\\' {
			b.WriteByte(c)
			j++
			continue
		}
		j++
		if j >= len(rest) {
			return b.String(), true // dangling backslash: stop here
		}
		switch rest[j] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '/', '\\', '"':
			b.WriteByte(rest[j])
		case 'u':
			if j+4 >= len(rest) {
				return b.String(), true // incomplete \uXXXX
			}
			if r, ok := hex4(rest[j+1 : j+5]); ok {
				if utf16.IsSurrogate(r) {
					// Surrogate pair halves render as nothing until complete;
					// dropping is lossy but safe for a transient preview.
					j += 4
					continue
				}
				b.WriteRune(r)
				j += 4
				continue
			}
			b.WriteByte(rest[j]) // not hex: keep literally
		default:
			b.WriteByte(rest[j])
		}
		j++
	}
	return b.String(), true // value not closed yet — partial
}

func hex4(s string) (rune, bool) {
	var v rune
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v*16 + rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v*16 + rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

// argsPreviewAccum tracks one call's raw args and the last emit time, so the
// agent can throttle preview events (the model emits many small fragments).
type argsPreviewAccum struct {
	raw       strings.Builder
	lastEmit  int64 // unix ms of the last emitted preview
}

// minPreviewIntervalMs throttles preview emissions per call; the final
// fragment before ChunkToolCall bypasses it (flush at stream end).
const minPreviewIntervalMs = 500

// patchFromArgs extracts the (partial) patchText from raw apply_patch args.
func patchFromArgs(raw string) (string, bool) {
	return partialJSONString(raw, "patchText")
}

// ensure utf8 import is used (decode helper may evolve); referenced via
// utf8.RuneLen in future work.
var _ = utf8.RuneLen
