//go:build !windows

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// get_ui_tree — the non-Windows counterpart of getUITreeEnhanced
// (uiauto_windows.go): same tool name and parameter contract, but WINDOW-level
// only. Control-level child enumeration (UIA/EnumChildWindows) is Windows-only,
// so the description says so and max_children is accepted-but-ignored.
//
// Backends, selected at runtime via runtime.GOOS (same pattern as
// input_other.go / window_other.go):
//   - macOS: one osascript "System Events" round-trip that walks visible
//     application processes and their windows, reading name/position/size
//     (AXPosition/AXSize) per window.
//   - Linux (X11): `wmctrl -lG` (id, desktop, pid, x y w h, host, title).
//
// Output shape matches the Windows tool: "N visible window(s)...:\n" + a JSON
// array of {title, class, rect:{x,y,w,h}} the VLM cross-references with a
// screenshot to pick click targets.

// getUITreeLite is the window-level UI-tree tool for macOS/Linux.
type getUITreeLite struct{}

func (getUITreeLite) Name() string { return "get_ui_tree" }

func (getUITreeLite) Description() string {
	return "Enumerate visible on-screen windows — each with its title, owning app, and exact bounding rect. Returns JSON the VLM cross-references with a screenshot to find precise click coordinates. Use WITHOUT title_prefix to list all windows; WITH title_prefix to filter to windows whose title starts with it (case-insensitive). NOTE: on macOS/Linux this tool is WINDOW-level only — the child-control enumeration (buttons/edit fields with their rects, via max_children) is a Windows-only feature and is not returned here. This is the precision complement to screenshot+image_understand."
}

func (getUITreeLite) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "title_prefix":{"type":"string","description":"Filter to windows whose title starts with this (case-insensitive)."},
  "max_children":{"type":"integer","description":"Windows-only: max child controls to list per window. Accepted for contract parity but ignored on macOS/Linux (no control-level enumeration)."}
},
"required":[]
}`)
}

func (getUITreeLite) ReadOnly() bool { return true }

func (getUITreeLite) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		TitlePrefix string `json:"title_prefix"`
		MaxChildren int    `json:"max_children"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	prefix := strings.ToLower(strings.TrimSpace(p.TitlePrefix))

	wins, err := enumUnixWindows()
	if err != nil {
		return "", err
	}

	type winOut struct {
		Title string         `json:"title"`
		Class string         `json:"class"`
		Rect  map[string]int `json:"rect"`
	}
	out := make([]winOut, 0, len(wins))
	for _, w := range wins {
		if prefix != "" && !strings.HasPrefix(strings.ToLower(w.title), prefix) {
			continue
		}
		out = append(out, winOut{
			Title: w.title,
			Class: w.class,
			Rect:  map[string]int{"x": w.x, "y": w.y, "w": w.w, "h": w.h},
		})
	}

	if len(out) == 0 {
		return fmt.Sprintf("no visible windows%s", prefixFilterNote(prefix)), nil
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d visible window(s)%s:\n%s", len(out), prefixFilterNote(prefix), string(b)), nil
}

// prefixFilterNote mirrors the Windows helper (uitree_windows.go): the suffix
// describing an active title_prefix filter.
func prefixFilterNote(prefix string) string {
	if prefix == "" {
		return ""
	}
	return fmt.Sprintf(" matching %q", prefix)
}

// --- enumeration backends -----------------------------------------------------

// unixWinInfo is one top-level window: title, owning app (the class analog),
// and its rect.
type unixWinInfo struct {
	title      string
	class      string
	x, y, w, h int
}

// enumUnixWindows dispatches to the per-OS window enumeration.
func enumUnixWindows() ([]unixWinInfo, error) {
	if runtime.GOOS == "darwin" {
		return darwinWindowList()
	}
	return linuxWindowList()
}

// darwinWindowList enumerates visible apps' windows via one System Events
// round-trip. Fields are tab-separated so titles containing commas or spaces
// survive; the tab comes from `character id 9` because AppleScript has no \t
// escape in string literals. The owning process name doubles as the Windows
// "class" analog. Per-window failures (some apps withhold AXPosition) skip
// just that window.
func darwinWindowList() ([]unixWinInfo, error) {
	if _, err := mustWindowHelper(); err != nil {
		return nil, err
	}
	script := `tell application "System Events"
	set tabCh to character id 9
	set out to ""
	repeat with p in (application processes whose visible is true)
		set pname to name of p
		try
			repeat with w in windows of p
				try
					set wname to name of w
					set pos to position of w
					set sz to size of w
					if wname is not missing value then
						set out to out & pname & tabCh & wname & tabCh & ((item 1 of pos) as text) & "," & ((item 2 of pos) as text) & "," & ((item 1 of sz) as text) & "," & ((item 2 of sz) as text) & linefeed
					end if
				end try
			end repeat
		end try
	end repeat
	return out
end tell`
	out, err := runWindowCmd(exec.Command("osascript", "-e", script), "enumerate windows via System Events")
	if err != nil {
		return nil, err
	}
	var wins []unixWinInfo
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}
		geo := strings.Split(parts[2], ",")
		if len(geo) != 4 {
			continue
		}
		x, errX := strconv.Atoi(strings.TrimSpace(geo[0]))
		y, errY := strconv.Atoi(strings.TrimSpace(geo[1]))
		w, errW := strconv.Atoi(strings.TrimSpace(geo[2]))
		h, errH := strconv.Atoi(strings.TrimSpace(geo[3]))
		if errX != nil || errY != nil || errW != nil || errH != nil || w <= 0 || h <= 0 {
			continue // zero/negative size — invisible or malformed, skip
		}
		wins = append(wins, unixWinInfo{title: parts[1], class: parts[0], x: x, y: y, w: w, h: h})
	}
	return wins, nil
}

// linuxWindowList parses `wmctrl -lG`: each line is
// "id desktop pid x y w h host title...". Titles may contain spaces, so they
// are re-extracted positionally rather than taken from Fields.
func linuxWindowList() ([]unixWinInfo, error) {
	path, err := mustWindowHelper()
	if err != nil {
		return nil, err
	}
	out, err := runWindowCmd(exec.Command(path, "-lG"), "enumerate windows with geometry")
	if err != nil {
		return nil, err
	}
	var wins []unixWinInfo
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		x, errX := strconv.Atoi(fields[3])
		y, errY := strconv.Atoi(fields[4])
		w, errW := strconv.Atoi(fields[5])
		h, errH := strconv.Atoi(fields[6])
		if errX != nil || errY != nil || errW != nil || errH != nil || w <= 0 || h <= 0 {
			continue // zero/negative size — skip (mirrors the Windows enumVisibleWindows)
		}
		wins = append(wins, unixWinInfo{title: wmctrlGTitle(line), x: x, y: y, w: w, h: h})
	}
	return wins, nil
}

// wmctrlGTitle extracts the title (the 9th column onward) from a
// `wmctrl -lG` line "0x03000007  0 12345 0 0 1920 1040 host title...". Empty
// for malformed lines. Mirrors wmctrlTitle in window_other.go (which skips 3
// columns for plain `wmctrl -l`).
func wmctrlGTitle(line string) string {
	rest := strings.TrimLeft(line, " \t")
	for i := 0; i < 8; i++ { // skip id, desktop, pid, x, y, w, h, hostname
		j := strings.IndexAny(rest, " \t")
		if j < 0 {
			return ""
		}
		rest = strings.TrimLeft(rest[j+1:], " \t")
	}
	return rest
}
