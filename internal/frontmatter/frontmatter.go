// Package frontmatter provides a minimal, dependency-free parser for the
// ---fenced "key: value" blocks that prefix skill, command, and memory files.
// It mirrors the YAML-like frontmatter convention without pulling in a YAML
// library, keeping fairpeer's single-(TOML)-dependency promise.
package frontmatter

import "strings"

// Split separates an optional leading ---fenced block of "key: value" lines from
// the body. It returns the parsed keys (lowercased) and the remaining body. With
// no opening/closing fence the whole input is the body. An opened but never
// closed fence treats the entire input as body (no partial parse).
//
// Values are trimmed of surrounding whitespace and outer quotes (" or ').
// A key with an empty value heads either a section ("metadata:") whose indented
// "key: value" lines flatten (metadata.type → fm["type"]), or a YAML list whose
// "- item" lines are joined comma-separated (allowed-tools → "read_file, grep"),
// so list-valued keys from skills authored for other agent tools survive.
// A key valued ">" or "|" (unquoted) opens a YAML block scalar: the following
// blank or deeper-indented lines form its body — folded to one line for ">" and
// kept newline-separated for "|" — so multi-line descriptions authored as
// folded scalars keep their body instead of collapsing to a literal ">".
// The last write wins for duplicate keys.
func Split(s string) (map[string]string, string) {
	fm := map[string]string{}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, s
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		content := lines[1:i]
		for j := 0; j < len(content); j++ {
			k, v, ok := strings.Cut(content[j], ":")
			if !ok {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(k))
			raw := strings.TrimSpace(v)
			if raw == ">" || raw == "|" {
				// Block scalar: consume the indented body that follows.
				text, last := blockScalar(content, j, raw == ">")
				j = last
				if text != "" {
					fm[key] = text
				}
				continue
			}
			val := strings.Trim(raw, `"'`)
			if val == "" {
				// Empty value: either a section header (metadata:) whose nested
				// "key: value" lines flatten below, or a YAML list whose "- item"
				// lines we join comma-separated so list-valued keys (allowed-tools,
				// from skills authored for other agent tools) survive instead of
				// being dropped.
				var items []string
				for j+1 < len(content) {
					item, ok := strings.CutPrefix(strings.TrimSpace(content[j+1]), "-")
					if !ok {
						break // not a list item — leave it for the outer loop
					}
					items = append(items, strings.Trim(strings.TrimSpace(item), `"'`))
					j++
				}
				if len(items) > 0 {
					fm[key] = strings.Join(items, ", ")
				}
				continue
			}
			fm[key] = val
		}
		return fm, strings.Join(lines[i+1:], "\n")
	}
	return fm, s // opened but never closed: treat all as body
}

// blockScalar reads the body of a ">" (folded) or "|" (literal) block scalar
// whose key line is content[start]. Body lines are the following lines that are
// blank or indented deeper than the key line; anything at or shallower than the
// key's indent ends the block and stays for the caller. It returns the value
// (folded to a single line for ">", newline-joined for "|", after stripping the
// common indent) and the index of the last consumed line. An empty body yields
// "" and the key is left unset, matching the empty-section behaviour above.
func blockScalar(content []string, start int, folded bool) (string, int) {
	keyIndent := indentWidth(content[start])
	last := start
	var body []string
	for k := start + 1; k < len(content); k++ {
		line := content[k]
		if strings.TrimSpace(line) != "" && indentWidth(line) <= keyIndent {
			break
		}
		body = append(body, line)
		last = k
	}
	// Leading/trailing blank lines are framing, not content.
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	if len(body) == 0 {
		return "", last
	}
	// Strip the common indent so relative structure inside the block survives.
	common := -1
	for _, ln := range body {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if w := indentWidth(ln); common < 0 || w < common {
			common = w
		}
	}
	for i, ln := range body {
		switch {
		case len(ln) >= common:
			body[i] = ln[common:]
		default:
			body[i] = ""
		}
	}
	if folded {
		// YAML folding: a line break becomes a space; a blank line stays a
		// paragraph break.
		var sb strings.Builder
		for _, ln := range body {
			if ln == "" {
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				continue
			}
			if s := sb.String(); sb.Len() > 0 && !strings.HasSuffix(s, "\n") {
				sb.WriteByte(' ')
			}
			sb.WriteString(strings.TrimRight(ln, " \t"))
		}
		return sb.String(), last
	}
	for i, ln := range body {
		body[i] = strings.TrimRight(ln, " \t")
	}
	return strings.Join(body, "\n"), last
}

// indentWidth counts the leading whitespace of a frontmatter line.
func indentWidth(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}
