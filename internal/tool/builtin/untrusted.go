package builtin

import (
	"fmt"
	"regexp"
	"strings"
)

// untrustedTagRe matches the fence tags case-insensitively on the ORIGINAL
// string. Matching must never go through a transformed copy: strings.ToLower
// can change a rune's UTF-8 length (U+0130 İ 2→1, U+212A K 3→1, U+017F ſ 2→1),
// so byte offsets taken from the lowered copy drift from the original and
// slice the wrong span — corrupting output and, with enough shrinkage before
// a tag, leaving the closing fence partially intact.
var untrustedTagRe = regexp.MustCompile(`(?i)</?untrusted_content`)

// WrapUntrusted wraps externally-sourced content (web pages, browser DOM, RAG
// snippets) in an <untrusted_content> tag. The cowork system prompt instructs
// the model to treat anything inside this tag as DATA, never as instructions —
// the core defense against prompt injection from malicious web pages or
// documents that try to hijack the agent ("ignore previous instructions…").
//
// source identifies where the content came from ("browser", "web", "rag") so
// the model can weigh trust and the user can audit the provenance in tool
// output. An empty content still gets wrapped so the boundary is always
// explicit — never let untrusted text bleed into the model's context without
// a clear fence around it.
//
// SECURITY: content is sanitized so a literal </untrusted_content> inside it
// cannot close the fence early. The closing tag is split into HTML-entity-
// encoded pieces the model still reads as data but that no longer match the
// fence boundary. This is the ONLY defense against prompt injection from
// fetched content, so it must be airtight.
//
// Exported so non-builtin packages (e.g. desktop expert-team injection,
// boot-time RAG auto-search) can share the same airtight fence instead of
// hand-rolling <untrusted_content> tags that forget to sanitize.
func WrapUntrusted(source, content string) string {
	return fmt.Sprintf("<untrusted_content source=%q>\n%s\n</untrusted_content>", source, sanitizeUntrusted(content))
}

// sanitizeUntrusted neutralizes any literal occurrence of the fence tags within
// content so a malicious payload can neither:
//   - close the fence early (</untrusted_content>) and inject instructions after
//     it, nor
//   - forge a nested <untrusted_content source="system"> block inside the fence
//     to mislead the model about the trust level of subsequent content.
//
// We replace the leading "<" of both tags with "&lt;" so the sequences are no
// longer recognized as tag boundaries, while remaining readable to the model as
// data. Matching is case-insensitive so attackers can't slip through with
// </UNTRUSTED_CONTENT> or <UNTRUSTED_CONTENT ...>. See security review finding:
// the close-tag-only sanitize left open-tag forgery possible.
func sanitizeUntrusted(content string) string {
	// Fast path detector on a lowered copy is sound (ToLower can only map a
	// cased rune to a form whose lowercase contains the needle if the original
	// did, case-insensitively) — only OFFSETS derived from the copy are unsafe,
	// and the replacement below never uses them.
	if !strings.Contains(strings.ToLower(content), "untrusted_content") {
		return content
	}
	// Replace on the original string, preserving the matched text's case and
	// everything around it byte-for-byte.
	return untrustedTagRe.ReplaceAllStringFunc(content, func(m string) string {
		return "&lt;" + m[1:]
	})
}
