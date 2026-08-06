//go:build windows

package builtin

import "testing"

// TestScreenToolsRoster guards the WINDOWS desktop-automation tool set: the
// four cross-platform action tools PLUS the Windows-native perception tools
// (screenshot, get_ui_tree, screen_perceive). This file is //go:build windows,
// so it only runs on Windows. The cross-platform base set (screen_click /
// screen_type / screen_scroll / screen_key) is covered separately and runs on
// every platform — see TestBaseScreenToolsRoster in screen_tools_test.go.
func TestScreenToolsRoster(t *testing.T) {
	tools := ScreenTools()
	// Windows exposes the full 7-tool set (base actions + Windows perception).
	want := map[string]bool{
		"screenshot": true, "screen_click": true, "screen_type": true,
		"screen_scroll": true, "get_ui_tree": true, "screen_perceive": true,
		"screen_key": true,
	}
	seen := make(map[string]bool, len(tools))
	for _, tl := range tools {
		name := tl.Name()
		seen[name] = true
		if !want[name] {
			t.Errorf("unexpected screen tool %q", name)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing screen tool %q", name)
		}
	}
}

// TestScreenToolsReadOnlyClassification locks the read-only flags so a flip
// doesn't silently change batching behavior. screenshot + get_ui_tree are
// read-only (safe to parallelize); click/type/scroll mutate the desktop.
func TestScreenToolsReadOnlyClassification(t *testing.T) {
	readOnly := map[string]bool{"screenshot": true, "get_ui_tree": true, "screen_perceive": true}
	for _, tl := range ScreenTools() {
		got := tl.ReadOnly()
		if want := readOnly[tl.Name()]; got != want {
			t.Errorf("%s ReadOnly() = %v, want %v", tl.Name(), got, want)
		}
	}
}

// TestAbsInt was moved to screen_tools_test.go (the helper is now
// platform-agnostic, so the test runs on every platform there).
