package main

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/tool/builtin"
)

func TestNormalizeSkillContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean passthrough", "---\nname: x\n---\nbody", "---\nname: x\n---\nbody"},
		{"markdown fence", "```markdown\n---\nname: x\n---\nbody\n```", "---\nname: x\n---\nbody"},
		{"leading prose", "好的，以下是技能：\n---\nname: x\n---\nbody", "---\nname: x\n---\nbody"},
	}
	for _, c := range cases {
		if got := normalizeSkillContent(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSkillContentParsable(t *testing.T) {
	if !skillContentParsable("---\nname: x\ndescription: d\n---\nbody") {
		t.Error("valid frontmatter should parse")
	}
	if skillContentParsable("no frontmatter") {
		t.Error("missing frontmatter should not parse")
	}
	if skillContentParsable("```\n---\nname: x\n---\nbody\n```") {
		t.Error("fenced content should not parse (normalization happens first)")
	}
	if skillContentParsable("---\ndescription: d\n---\nbody") {
		t.Error("frontmatter without name should not parse")
	}
}

func TestNaiveSkillDraftNeverEmptyName(t *testing.T) {
	events := []builtin.ConsoleRecordEvent{{Type: "navigate", URL: "https://x/"}}
	// Chinese-only hint sanitizes to "" — the draft must still carry a valid
	// name in BOTH the returned struct and the frontmatter (E2E 2026-08-29).
	draft := naiveSkillDraft("百度搜索", events, "")
	if draft.Name != "browser-skill" {
		t.Errorf("struct name: got %q, want browser-skill", draft.Name)
	}
	if !strings.Contains(draft.Content, "name: browser-skill") {
		t.Error("frontmatter must contain the fallback name")
	}
	if skillContentParsable(draft.Content) != true {
		t.Error("naive draft must be editor-parsable")
	}
}
