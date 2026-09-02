package skill

import (
	"fmt"
	"strings"
)

// IndexMaxChars caps the pinned skills-index block so it can't bloat the
// cache-stable system-prompt prefix; bodies never enter the prefix.
// (Some providers currently do not report cache tokens; the prefix stability still helps.)
const IndexMaxChars = 4000

const missingDescPlaceholder = `(no description — frontmatter is missing a "description:" line; tell the user to add one)`

// indexHeader introduces the skills block in the system prompt: the invocation
// policy (mandatory for inline, judgment-based for subagent) and how to call one.
// Kept compact: every token here goes into the system prompt every turn, so
// only the rules the model can't infer from the index entries themselves are
// stated. The index lines (name + description + tag) carry the specifics.
const indexHeader = "# Skills\n\n" +
	"Call `run_skill({ name: \"<name>\", arguments: \"<task>\" })` — name is the identifier only (e.g. `\"explore\"`), not the tag. Users can also invoke via `/<name>`.\n" +
	"- Untagged (inline): body loads as a tool result you act on directly. Invoke on plausible relevance before pre-judging — loading one is cheap.\n" +
	"- `[🧬 subagent]`: spawns an isolated agent; its reasoning/tool calls stay out of your context, only the final answer comes back. Use for context-heavy work, not weak relevance.\n" +
	"- `[⚙ 确定性]`: executes its step table verbatim in the kernel (no LLM per step). Pass missing runtime values as arguments like `参数=值`.\n" +
	"- `[关闭]`: disabled by user — not callable. If a task fits a disabled skill, tell the user to enable it in Settings → Skills.\n" +
	"Prefer the dedicated top-level tool when one exists for a built-in subagent skill."

// ApplyIndex appends the skills index to basePrompt, or returns it unchanged
// when there are no skills. Only names + descriptions (+ a subagent tag) are
// listed; bodies load on demand via run_skill.
//
// Overflow policy (many skills, one capped block): active skills come first
// and cold (long-unused) ones last, so a tight budget eats hibernating
// entries before live ones; degradation is staged — full lines → cold lines
// compressed to name-only → whole-line cut with an explicit count of omitted
// skills (the model loses names, never gets a mid-line garble, and the user
// can still invoke any skill via /<name>).
func ApplyIndex(basePrompt string, skills []Skill) string {
	if len(skills) == 0 {
		return basePrompt
	}
	// Stable partition: active (and disabled — user intent outranks cold)
	// first, cold last; store order (name-sorted) preserved within groups.
	ordered := make([]Skill, 0, len(skills))
	var cold []Skill
	for _, sk := range skills {
		if sk.Cold && !sk.Disabled {
			cold = append(cold, sk)
		} else {
			ordered = append(ordered, sk)
		}
	}
	ordered = append(ordered, cold...)

	joined := strings.Join(renderIndexLines(ordered, false), "\n")
	if r := []rune(joined); len(r) > IndexMaxChars {
		// Stage 2: drop cold descriptions (names stay callable).
		joined = strings.Join(renderIndexLines(ordered, true), "\n")
	}
	if r := []rune(joined); len(r) > IndexMaxChars {
		// Stage 3: whole-line cut + actionable omission count.
		kept := []string{}
		runes := 0
		omitted := 0
		for _, line := range strings.Split(joined, "\n") {
			n := len([]rune(line)) + 1
			if runes+n > IndexMaxChars-120 { // reserve room for the notice
				omitted++
				continue
			}
			kept = append(kept, line)
			runes += n
		}
		joined = strings.Join(kept, "\n") +
			fmt.Sprintf("\n… (truncated: %d skills omitted to fit — the user can still invoke any skill by /<name>; retire long-unused ones in Settings → Skills)", omitted)
	}
	return basePrompt + "\n\n" + indexHeader + "\n\n```\n" + joined + "\n```"
}

// renderIndexLines renders the index body. When compressCold is set, cold
// skills degrade to name+tag only — the budget buys more callable names.
func renderIndexLines(skills []Skill, compressCold bool) []string {
	lines := make([]string, 0, len(skills))
	for _, sk := range skills {
		if compressCold && sk.Cold && !sk.Disabled {
			tag := " [休眠]"
			if sk.RunAs == RunSubagent {
				tag = " [🧬 subagent] [休眠]"
			}
			lines = append(lines, "- "+sk.Name+tag)
			continue
		}
		lines = append(lines, indexLine(sk))
	}
	return lines
}

// indexLine renders one skill as "- name [tag] — description", clipped to a
// stable width. The subagent tag goes after the name so a model copying the line
// into run_skill's `name` arg still yields a clean identifier.
func indexLine(sk Skill) string {
	desc := strings.TrimSpace(strings.ReplaceAll(sk.Description, "\n", " "))
	if desc == "" {
		desc = missingDescPlaceholder
	}
	tag := ""
	if sk.RunAs == RunSubagent {
		tag = " [🧬 subagent]"
	}
	if sk.Executor == ExecutorBrowserFlow {
		tag = " [⚙ 确定性]"
	}
	if sk.Disabled {
		tag += " [关闭]"
	}
	if sk.Cold {
		tag += " [休眠]"
	}
	max := 130 - len([]rune(sk.Name)) - len([]rune(tag))
	clipped := clipRunes(desc, max)
	if clipped == "" {
		return "- " + sk.Name + tag
	}
	return "- " + sk.Name + tag + " — " + clipped
}

// clipRunes truncates s to at most max runes (ellipsis included), never
// splitting a multi-byte rune.
func clipRunes(s string, max int) string {
	if max < 1 {
		max = 1
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max-1 < 1 {
		return string(r[:1])
	}
	return string(r[:max-1]) + "…"
}
