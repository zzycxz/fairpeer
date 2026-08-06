//go:build !windows

package builtin

import "github.com/zzycxz/fairpeer/internal/tool"

// ScreenTools returns the desktop-automation tools available on macOS/Linux.
//
// The four cross-platform action tools — screen_click, screen_type,
// screen_scroll, screen_key — work everywhere via external CLIs (cliclick on
// macOS, xdotool on Linux; see input_other.go). They are the same tools, with
// the same schemas and semantics, that Windows exposes.
//
// screen_perceive (screenshot + element labeling + VLM selection) is added
// separately: its macOS/Linux implementation is a VLM-only stub for now (see
// screen_perceive_other.go) and the cross-platform perceive work is a follow-up
// task. screen_perceive is intentionally NOT included here yet so the tool
// roster doesn't advertise a tool that returns "not implemented" — the
// non-Windows perceive will be wired in once it actually works.
func ScreenTools() []tool.Tool {
	return baseScreenTools()
}
