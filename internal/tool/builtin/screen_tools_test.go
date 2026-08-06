package builtin

import "testing"

// TestParseKeyCombo covers the screen_key parser across the shapes the agent
// actually sends: ctrl+s, ctrl+shift+t, lone enter, single letters with ctrl.
// A wrong parse here = the wrong key gets pressed (e.g. ctrl+c instead of
// ctrl+s → copy instead of save), so it's worth locking down.
//
// This test is platform-agnostic: parseKeyCombo now returns platform-agnostic
// key NAMES ("ctrl", "s", "enter", ...) rather than Windows VK codes, so the
// same expectation applies on Windows / macOS / Linux. The per-platform
// backends (parseVK / macKeyName / linuxKeyName) translate the names; this
// test only locks the parsing contract.
func TestParseKeyCombo(t *testing.T) {
	cases := []struct {
		in     string
		hasMod bool
		mod    string // expected modName ("" if hasMod is false)
		key    string // expected keyName ("" for error seeds)
		wantErr bool  // true → expect a non-nil error
	}{
		{"ctrl+s", true, "ctrl", "s", false},
		{"Control+S", true, "ctrl", "s", false},
		{"ctrl+shift+t", true, "ctrl+shift", "t", false},
		{"ctrl-shift-tab", true, "ctrl+shift", "tab", false}, // dash separator; ctrl+shift+tab
		{"enter", false, "", "enter", false},
		{"escape", false, "", "escape", false},
		{"esc", false, "", "esc", false},
		{"ctrl+a", true, "ctrl", "a", false},
		{"f5", false, "", "f5", false},
		{"alt+tab", true, "alt", "tab", false},
		{"backspace", false, "", "backspace", false},
		{"shift+ctrl+s", true, "shift+ctrl", "s", false}, // order preserved
		{"ctrl+ctrl+s", true, "ctrl", "s", false},        // dedupe: same mod twice
		{"arrowleft", false, "", "arrowleft", false},
		{"pageup", false, "", "pageup", false},
		{"delete", false, "", "delete", false},
		{"", false, "", "", true},    // empty input
		{"xyz", false, "", "", true}, // unknown key
		{"ctrl+", false, "", "", true}, // empty key after +
		{"foo+s", false, "", "", true}, // unknown modifier
	}
	for _, c := range cases {
		mod, key, err := parseKeyCombo(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseKeyCombo(%q) want error, got mod=%q key=%q", c.in, mod, key)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseKeyCombo(%q) unexpected error: %v", c.in, err)
			continue
		}
		if c.hasMod {
			if mod != c.mod {
				t.Errorf("parseKeyCombo(%q) mod = %q, want %q", c.in, mod, c.mod)
			}
		} else if mod != "" {
			t.Errorf("parseKeyCombo(%q) unexpected modifier %q", c.in, mod)
		}
		if key != c.key {
			t.Errorf("parseKeyCombo(%q) key = %q, want %q", c.in, key, c.key)
		}
	}
}

// TestValidKeyName locks the platform-agnostic key vocabulary so a typo (e.g.
// "arowup" vs "arrowup") is caught here rather than as a runtime "unknown key"
// during a real desktop session.
func TestValidKeyName(t *testing.T) {
	good := []string{
		"a", "z", "0", "9",
		"enter", "return", "esc", "escape", "tab", "space",
		"delete", "del", "backspace", "home", "end",
		"pageup", "pagedown",
		"arrowup", "up", "arrowdown", "down",
		"arrowleft", "left", "arrowright", "right",
		"f1", "f6", "f12",
	}
	for _, k := range good {
		if !validKeyName(k) {
			t.Errorf("validKeyName(%q) = false, want true", k)
		}
	}
	bad := []string{"", "ab", "A", "Z", "5x", "arrow", "f13", "f0", "ctrl", "shift", "return!", "space "}
	for _, k := range bad {
		if validKeyName(k) {
			t.Errorf("validKeyName(%q) = true, want false", k)
		}
	}
}

// TestBaseScreenToolsRoster locks the four cross-platform action tools. Every
// platform's ScreenTools() builds on this set, so a dropped/renamed tool here
// breaks desktop automation everywhere. We assert by name (the LLM-facing
// identifier) rather than type so a refactor of the struct doesn't silently
// change what the model sees.
func TestBaseScreenToolsRoster(t *testing.T) {
	tools := baseScreenTools()
	want := map[string]bool{
		"screen_click":  true,
		"screen_type":   true,
		"screen_scroll": true,
		"screen_key":    true,
	}
	if len(tools) != len(want) {
		t.Fatalf("baseScreenTools() returned %d tools, want %d", len(tools), len(want))
	}
	seen := make(map[string]bool, len(tools))
	for _, tl := range tools {
		name := tl.Name()
		seen[name] = true
		if !want[name] {
			t.Errorf("unexpected base screen tool %q", name)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing base screen tool %q", name)
		}
	}
}

// TestBaseScreenToolsReadOnlyClassification locks the read-only flag for the
// four action tools: all four MUTATE the desktop (none is read-only), so the
// scheduler must never batch them in parallel with each other or with
// perception. A flip here would silently change batching behavior.
func TestBaseScreenToolsReadOnlyClassification(t *testing.T) {
	for _, tl := range baseScreenTools() {
		if tl.ReadOnly() {
			t.Errorf("%s ReadOnly() = true, want false (action tools mutate the desktop)", tl.Name())
		}
	}
}

// TestAbsInt covers the scroll-direction helper used by screen_scroll's message
// (moved here from screen_windows_test.go when the helper became
// platform-agnostic). Positive → as-is, negative → negated.
func TestAbsInt(t *testing.T) {
	cases := map[int]int{0: 0, 3: 3, -3: 3, 100: 100, -100: 100}
	for in, want := range cases {
		if got := absInt(in); got != want {
			t.Errorf("absInt(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestSplitModifiers covers the helper that pressKeyCombo backends use to break
// a "ctrl+shift" string into parts. Order + dedup-untouched + empty handling
// all matter for the assembled key combo.
func TestSplitModifiers(t *testing.T) {
	cases := map[string][]string{
		"":            nil,
		"ctrl":        {"ctrl"},
		"ctrl+shift":  {"ctrl", "shift"},
		"shift+ctrl":  {"shift", "ctrl"},
		"ctrl+shift ": {"ctrl", "shift"}, // trailing space trimmed
		" ctrl + alt ": {"ctrl", "alt"},  // surrounding spaces trimmed
	}
	for in, want := range cases {
		got := splitModifiers(in)
		if len(got) != len(want) {
			t.Errorf("splitModifiers(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("splitModifiers(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}
