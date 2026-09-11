//go:build !windows

package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/zzycxz/fairpeer/internal/tool"
)

// Window management tools for the CUA — macOS/Linux counterparts of the Win32
// implementations in window_windows.go (same tool names and parameter shapes:
// window_focus / window_maximize / window_restore / window_move / window_close;
// window_move covers resize, exactly like the Windows tool set).
//
// Backends, selected at runtime via runtime.GOOS (same pattern as
// input_other.go) so one file compiles for both darwin and linux:
//
//   - macOS: osascript talking to "System Events" — the process whose window
//     title matches gets frontmost/AXRaise; geometry via the AXPosition/AXSize
//     attributes.
//   - Linux (X11): wmctrl, which alone covers activate, maximize/restore
//     state, move/resize (-e), and a polite close (-c). xdotool is NOT
//     required (input_other.go already uses it for synthetic input).
//
// Every handler returns an actionable error naming the missing helper binary
// (mirroring mustInputTool in input_other.go) instead of failing silently, and
// window lookup errors list a few visible titles so the model can retry with a
// corrected name — same ergonomics as the Windows resolveWindow.

// --- tool set ---------------------------------------------------------------

// WindowTools returns the window-management tool set for macOS/Linux, matching
// the Windows set (window_windows.go) tool-for-tool. Registered in cowork mode
// alongside the screen_* tools.
func WindowTools() []tool.Tool {
	return []tool.Tool{
		windowFocus{},
		windowMaximize{},
		windowRestore{},
		windowMove{},
		windowClose{},
	}
}

// --- shared plumbing ---------------------------------------------------------

// windowHelperMiss names the missing backend binary with an install hint, so a
// missing tool produces an actionable error rather than a bare exec failure.
func windowHelperMiss() error {
	if runtime.GOOS == "darwin" {
		// osascript ships with every macOS install; a miss means a broken system.
		return fmt.Errorf("osascript not found — it is part of every macOS install; check your PATH")
	}
	return fmt.Errorf("wmctrl not installed — install it via your package manager (e.g. apt install wmctrl / dnf install wmctrl / pacman -S wmctrl)")
}

// mustWindowHelper looks up the backend binary; the error explains what's
// missing and how to fix it.
func mustWindowHelper() (string, error) {
	name := "wmctrl"
	if runtime.GOOS == "darwin" {
		name = "osascript"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", windowHelperMiss()
	}
	return path, nil
}

// runWindowCmd executes a prepared command, wrapping failures with context and
// the tool's own stderr (e.g. AppleScript "not allowed assistive access", or
// wmctrl's "Cannot open display").
func runWindowCmd(cmd *exec.Cmd, what string) (string, error) {
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return "", fmt.Errorf("%s failed: %w: %s", what, err, detail)
		}
		return "", fmt.Errorf("%s failed: %w", what, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// appleScriptQuote escapes s for use inside an AppleScript string literal.
// AppleScript does not understand \uXXXX escapes, so non-ASCII (CJK window
// titles like "记事本") must pass through verbatim — only \ and " need citing.
func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// parseWindowArgs unmarshals the shared {"title": ...} argument.
func parseWindowArgs(args json.RawMessage) (string, error) {
	var p struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	return strings.TrimSpace(p.Title), nil
}

// windowTitleRequired mirrors the Windows resolveWindow contract: an empty
// title is an error — these tools always need a target.
func windowTitleRequired(title string) error {
	return fmt.Errorf("title is required (substring of a visible window's title, e.g. \"记事本\" or \"terminal\")")
}

// listVisibleWindowTitles returns up to `max` visible window titles for the
// "not found" error message, so a wrong/missing name is fixable instead of a
// bare failure (same as the Windows resolveWindow).
func listVisibleWindowTitles(max int) string {
	var titles []string
	if runtime.GOOS == "darwin" {
		out, err := runWindowCmd(exec.Command("osascript", "-e", `tell application "System Events"
	set out to ""
	repeat with p in (application processes whose visible is true)
		try
			repeat with w in windows of p
				set t to name of w
				if t is not missing value then set out to out & t & linefeed
			end repeat
		end try
	end repeat
	return out
end tell`), "enumerate windows")
		if err == nil && out != "" {
			titles = strings.Split(out, "\n")
		}
	} else {
		out, err := runWindowCmd(exec.Command("wmctrl", "-l"), "enumerate windows")
		if err == nil && out != "" {
			for _, line := range strings.Split(out, "\n") {
				if t := wmctrlTitle(line); t != "" {
					titles = append(titles, t)
				}
			}
		}
	}
	if len(titles) > max {
		titles = titles[:max]
	}
	return strings.Join(titles, ", ")
}

// notFoundError builds the shared "nothing matched" error.
func notFoundError(title string) error {
	return fmt.Errorf("no visible window title contains %q; visible titles: %s", title, listVisibleWindowTitles(8))
}

// --- macOS backend -----------------------------------------------------------

// darwinWindowAction runs an AppleScript that finds the first visible window
// whose title contains titlePart and performs actionScript on it. The script
// returns WINDOW_NOT_FOUND (mapped to the helpful notFoundError) or
// "OK: <title>". Each action is one osascript round-trip: find + act inline,
// stateless between calls.
func darwinWindowAction(titlePart, actionScript string) (string, error) {
	if _, err := mustWindowHelper(); err != nil {
		return "", err
	}
	if titlePart == "" {
		return "", windowTitleRequired(titlePart)
	}
	script := fmt.Sprintf(`tell application "System Events"
	repeat with p in (application processes whose visible is true)
		try
			repeat with w in windows of p
				if name of w contains %s then
					%s
					return "OK: " & (name of w)
				end if
			end repeat
		end try
	end repeat
	return "WINDOW_NOT_FOUND"
end tell`, appleScriptQuote(titlePart), actionScript)
	out, err := runWindowCmd(exec.Command("osascript", "-e", script), "drive window via System Events")
	if err != nil {
		return "", err
	}
	if strings.Contains(out, "WINDOW_NOT_FOUND") {
		return "", notFoundError(titlePart)
	}
	return strings.TrimPrefix(out, "OK: "), nil
}

// --- Linux backend -----------------------------------------------------------

// resolveLinuxWindow finds the first window whose title contains title
// (case-insensitive) in `wmctrl -l` output and returns its hex id + description.
func resolveLinuxWindow(title string) (id, desc string, err error) {
	if _, err := mustWindowHelper(); err != nil {
		return "", "", err
	}
	if title == "" {
		return "", "", windowTitleRequired(title)
	}
	out, err := runWindowCmd(exec.Command("wmctrl", "-l"), "enumerate windows")
	if err != nil {
		return "", "", err
	}
	lower := strings.ToLower(title)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		t := wmctrlTitle(line)
		if strings.Contains(strings.ToLower(t), lower) {
			return fields[0], fmt.Sprintf("%q (%s)", t, fields[0]), nil
		}
	}
	return "", "", notFoundError(title)
}

// wmctrlTitle extracts the title (the 4th column onward) from a `wmctrl -l`
// line "0x03000007  0 hostname title...". Empty for malformed lines.
func wmctrlTitle(line string) string {
	line = strings.TrimLeft(line, " \t")
	rest := line
	for i := 0; i < 3; i++ { // skip id, desktop number, hostname
		j := strings.IndexAny(rest, " \t")
		if j < 0 {
			return ""
		}
		rest = strings.TrimLeft(rest[j+1:], " \t")
	}
	return rest
}

// linuxWindowCmd runs a wmctrl action against a resolved window id.
func linuxWindowCmd(id string, args ...string) error {
	path, err := mustWindowHelper()
	if err != nil {
		return err
	}
	full := append([]string{"-i", "-r", id}, args...)
	_, err = runWindowCmd(exec.Command(path, full...), "wmctrl "+strings.Join(full, " "))
	return err
}

// --- tools -------------------------------------------------------------------

// windowFocus brings a window to the foreground and focuses it so subsequent
// screen_type / screen_key land in it. Mirror of the Windows window_focus.
type windowFocus struct{}

func (windowFocus) Name() string   { return "window_focus" }
func (windowFocus) ReadOnly() bool { return false }
func (windowFocus) Description() string {
	return "Bring a window to the foreground and give it keyboard focus, by a substring of its title. This is the FIRST thing to do after launching an app and before typing/clicking into it: without focus, screen_type/screen_key go to whatever window happens to be active (the #1 cause of 'text didn't appear where I expected'). Also raises the window if it is behind others. Example: window_focus {\"title\":\"terminal\"}. Verify with screen_perceive after."
}
func (windowFocus) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"substring of the target window's title, case-insensitive (e.g. \"记事本\", \"notepad\", \"保存\")"}},"required":["title"]}`)
}
func (windowFocus) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	title, err := parseWindowArgs(args)
	if err != nil {
		return "", err
	}
	if title == "" {
		return "", windowTitleRequired(title)
	}
	if runtime.GOOS == "darwin" {
		name, err := darwinWindowAction(title, `set frontmost of p to true
					perform action "AXRaise" of w`)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("focused window %q", name), nil
	}
	id, desc, err := resolveLinuxWindow(title)
	if err != nil {
		return "", err
	}
	path, _ := mustWindowHelper()
	if _, err := runWindowCmd(exec.Command(path, "-i", "-a", id), "wmctrl -a "+id); err != nil {
		return "", err
	}
	return fmt.Sprintf("focused window %s", desc), nil
}

// windowMaximize maximizes a window so its full content is visible and not
// occluded — a reliable workspace state before perceiving/clicking.
type windowMaximize struct{}

func (windowMaximize) Name() string   { return "window_maximize" }
func (windowMaximize) ReadOnly() bool { return false }
func (windowMaximize) Description() string {
	return "Maximize a window (by title substring) so its full content is visible and it's not occluded by other windows. Good to call before screen_perceive so the whole UI is on screen. Example: window_maximize {\"title\":\"terminal\"}."
}
func (windowMaximize) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"substring of the target window's title, case-insensitive"}},"required":["title"]}`)
}
func (windowMaximize) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	title, err := parseWindowArgs(args)
	if err != nil {
		return "", err
	}
	if title == "" {
		return "", windowTitleRequired(title)
	}
	if runtime.GOOS == "darwin" {
		// AXZoomWindow is macOS "zoom" (the green button's maximize toggle);
		// fall back to clicking the zoom button when the action is missing.
		name, err := darwinWindowAction(title, `try
						perform action "AXZoomWindow" of w
					on error
						click (first button of w whose subrole is "AXZoomButton")
					end try`)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("maximized %q", name), nil
	}
	id, desc, err := resolveLinuxWindow(title)
	if err != nil {
		return "", err
	}
	if err := linuxWindowCmd(id, "-b", "add,maximized_vert,maximized_horz"); err != nil {
		return "", fmt.Errorf("maximize %s failed: %w", desc, err)
	}
	return fmt.Sprintf("maximized %s", desc), nil
}

// windowRestore un-maximizes / un-minimizes a window to its normal size.
type windowRestore struct{}

func (windowRestore) Name() string   { return "window_restore" }
func (windowRestore) ReadOnly() bool { return false }
func (windowRestore) Description() string {
	return "Restore a window (by title substring) from maximized or minimized state to its normal size. Use when you need a smaller, movable window or to undo a previous window_maximize. Example: window_restore {\"title\":\"terminal\"}."
}
func (windowRestore) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"substring of the target window's title, case-insensitive"}},"required":["title"]}`)
}
func (windowRestore) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	title, err := parseWindowArgs(args)
	if err != nil {
		return "", err
	}
	if title == "" {
		return "", windowTitleRequired(title)
	}
	if runtime.GOOS == "darwin" {
		name, err := darwinWindowAction(title, `set value of attribute "AXMinimized" of w to false
					perform action "AXRaise" of w`)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("restored %q", name), nil
	}
	id, desc, err := resolveLinuxWindow(title)
	if err != nil {
		return "", err
	}
	if err := linuxWindowCmd(id, "-b", "remove,maximized_vert,maximized_horz"); err != nil {
		return "", fmt.Errorf("restore %s failed: %w", desc, err)
	}
	return fmt.Sprintf("restored %s", desc), nil
}

// windowMove moves/resizes a window to a specific (x, y, w, h). Mirror of the
// Windows window_move (which also handles both position and size).
type windowMove struct{}

func (windowMove) Name() string   { return "window_move" }
func (windowMove) ReadOnly() bool { return false }
func (windowMove) Description() string {
	return "Move and/or resize a window to exact coordinates by title substring. Use it to place a window where you KNOW the layout will be (so screen_click coordinates are unambiguous), or to bring an off-screen window fully into view. Pass x,y for the new top-left and w,h for the new size (all in physical screen pixels). Example: window_move {\"title\":\"terminal\",\"x\":0,\"y\":0,\"w\":800,\"h\":600}."
}
func (windowMove) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"substring of the target window's title, case-insensitive"},"x":{"type":"integer","description":"new left edge (screen pixels)"},"y":{"type":"integer","description":"new top edge (screen pixels)"},"w":{"type":"integer","description":"new width (screen pixels)"},"h":{"type":"integer","description":"new height (screen pixels)"}},"required":["title","x","y","w","h"]}`)
}
func (windowMove) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Title string `json:"title"`
		X     int    `json:"x"`
		Y     int    `json:"y"`
		W     int    `json:"w"`
		H     int    `json:"h"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return "", windowTitleRequired(title)
	}
	if p.W <= 0 || p.H <= 0 {
		return "", fmt.Errorf("invalid size %dx%d: w and h must be positive", p.W, p.H)
	}
	if runtime.GOOS == "darwin" {
		name, err := darwinWindowAction(title, fmt.Sprintf(`set position of w to {%d, %d}
					set size of w to {%d, %d}`, p.X, p.Y, p.W, p.H))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("moved %q to (%d,%d) %dx%d", name, p.X, p.Y, p.W, p.H), nil
	}
	id, desc, err := resolveLinuxWindow(title)
	if err != nil {
		return "", err
	}
	// gravity 0 = static; the rect is x,y,w,h.
	if err := linuxWindowCmd(id, "-e", fmt.Sprintf("0,%d,%d,%d,%d", p.X, p.Y, p.W, p.H)); err != nil {
		return "", fmt.Errorf("move/resize %s failed: %w", desc, err)
	}
	return fmt.Sprintf("moved %s to (%d,%d) %dx%d", desc, p.X, p.Y, p.W, p.H), nil
}

// windowClose closes a window cleanly (the window manager's close request,
// same as clicking the X).
type windowClose struct{}

func (windowClose) Name() string   { return "window_close" }
func (windowClose) ReadOnly() bool { return false }
func (windowClose) Description() string {
	return "Close a window cleanly by title substring (sends the window manager's close request, the same as clicking the close button). Cleaner than killing a process. Example: window_close {\"title\":\"terminal\"}."
}
func (windowClose) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","description":"substring of the target window's title, case-insensitive"}},"required":["title"]}`)
}
func (windowClose) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	title, err := parseWindowArgs(args)
	if err != nil {
		return "", err
	}
	if title == "" {
		return "", windowTitleRequired(title)
	}
	if runtime.GOOS == "darwin" {
		// The close button is the one whose subrole is AXCloseButton (button
		// numbering is not stable across macOS versions).
		name, err := darwinWindowAction(title, `click (first button of w whose subrole is "AXCloseButton")`)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("sent close to %q", name), nil
	}
	id, desc, err := resolveLinuxWindow(title)
	if err != nil {
		return "", err
	}
	path, _ := mustWindowHelper()
	// -c sends a polite close request (the app may prompt to save — like the X).
	if _, err := runWindowCmd(exec.Command(path, "-i", "-c", id), "wmctrl -c "+id); err != nil {
		return "", err
	}
	return fmt.Sprintf("sent close to %s", desc), nil
}
