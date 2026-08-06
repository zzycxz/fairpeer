//go:build !windows

package builtin

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Non-Windows input backend for the cross-platform screen_* tools. Instead of
// Win32 SendInput, this shells out to a user-installed CLI that wraps the
// native input APIs:
//
//   - macOS: cliclick (https://github.com/BlueM/cliclick) — Quartz CGEvent.
//   - Linux: xdotool — X11 XTest extension.
//
// Both are standard, widely-available tools; the functions below return a
// clear, actionable error (naming the missing binary and how to install it)
// when the tool isn't on PATH, rather than failing opaquely.
//
// The platform is selected at runtime via runtime.GOOS so this file compiles
// for both darwin and linux; only the matching branch runs on each. Key names
// arrive in the platform-agnostic vocabulary produced by parseKeyCombo
// (screen_tools.go) and are translated to each tool's tokens here.

// backendMissMsg returns the install hint shown when the required CLI is
// missing, so the user gets actionable feedback instead of a bare exec error.
func backendMissMsg() string {
	switch runtime.GOOS {
	case "darwin":
		return "install cliclick: brew install cliclick"
	default:
		return "install xdotool: e.g. apt install xdotool / dnf install xdotool / pacman -S xdotool"
	}
}

// inputCmd returns the configured command name + a lookup helper. It does NOT
// shell out; callers build an exec.Cmd and pass it to runInput.
func inputCmdName() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return "cliclick", nil
	default:
		return "xdotool", nil
	}
}

// mustInputTool looks up the backend binary; on success it returns the path,
// on failure it returns an error that explains what's missing and how to fix
// it. Centralizing this keeps every input function's "missing tool" error
// consistent.
func mustInputTool() (string, error) {
	name, _ := inputCmdName()
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("desktop input backend %q not found on PATH (%s)", name, backendMissMsg())
	}
	return path, nil
}

// runCmd executes the given command and wraps any failure with context. The
// caller decides args; this just runs + reports exit/stderr.
func runCmd(cmd *exec.Cmd, what string) error {
	if out, err := cmd.CombinedOutput(); err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return fmt.Errorf("%s failed: %w: %s", what, err, detail)
		}
		return fmt.Errorf("%s failed: %w", what, err)
	}
	return nil
}

// moveMouse moves the cursor to (x, y) in physical screen pixels.
//
//	macOS: cliclick m:x,y
//	Linux: xdotool mousemove x y
func moveMouse(x, y int) error {
	path, err := mustInputTool()
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command(path, "m:", fmt.Sprintf("%d,%d", x, y))
	} else {
		cmd = exec.Command(path, "mousemove", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	}
	return runCmd(cmd, fmt.Sprintf("moveMouse(%d,%d)", x, y))
}

// clickMouse presses + releases a button at the current cursor position.
//
//	macOS: cliclick c:  (left) / rc: (right) / mc: (middle)
//	Linux: xdotool click 1 (left) / 2 (middle) / 3 (right)
func clickMouse(button string) error {
	path, err := mustInputTool()
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		sub := "c:" // left
		switch button {
		case "", "left":
			sub = "c:"
		case "right":
			sub = "rc:"
		case "middle":
			sub = "mc:"
		default:
			return fmt.Errorf("unknown button %q", button)
		}
		cmd = exec.Command(path, sub)
	} else {
		btn := "1" // left
		switch button {
		case "", "left":
			btn = "1"
		case "middle":
			btn = "2"
		case "right":
			btn = "3"
		default:
			return fmt.Errorf("unknown button %q", button)
		}
		cmd = exec.Command(path, "click", btn)
	}
	return runCmd(cmd, fmt.Sprintf("clickMouse(%s)", button))
}

// typeText types text at the current keyboard focus.
//
//	macOS: cliclick t:text
//	Linux: xdotool type --clearmodifiers text
//
// Text is passed as a direct argv element (no shell), so no escaping is needed
// and shell-injection / quoting bugs are impossible.
func typeText(text string) error {
	path, err := mustInputTool()
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command(path, "t:", text)
	} else {
		// --clearmodifiers releases any held modifier (e.g. the agent just pressed
		// ctrl) so the typed text isn't interpreted as a shortcut.
		cmd = exec.Command(path, "type", "--clearmodifiers", text)
	}
	return runCmd(cmd, fmt.Sprintf("typeText(%d chars)", len(text)))
}

// pressKeyCombo presses modifier+key simultaneously then releases. modName is a
// '+'-joined list of platform-agnostic modifier names ("ctrl", "ctrl+shift");
// keyName is a single platform-agnostic key name. Both are translated to the
// backend's token form here.
//
//	macOS: cliclick kp:<combo>   e.g. "kp:cmd+s", "kp:cmd+shift+tab"
//	Linux: xdotool key <combo>   e.g. "ctrl+s", "ctrl+shift+Tab"
func pressKeyCombo(modName, keyName string) error {
	path, err := mustInputTool()
	if err != nil {
		return err
	}
	var combo string
	if runtime.GOOS == "darwin" {
		combo, err = macKeyCombo(modName, keyName)
	} else {
		combo, err = linuxKeyCombo(modName, keyName)
	}
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command(path, "kp:"+combo)
	} else {
		cmd = exec.Command(path, "key", combo)
	}
	return runCmd(cmd, fmt.Sprintf("pressKeyCombo(%s+%s)", modName, keyName))
}

// pressKey presses + releases a single key (no modifier).
//
//	macOS: cliclick kp:<key>
//	Linux: xdotool key <key>
func pressKey(keyName string) error {
	path, err := mustInputTool()
	if err != nil {
		return err
	}
	var token string
	if runtime.GOOS == "darwin" {
		token, err = macKeyName(keyName)
	} else {
		token, err = linuxKeyName(keyName)
	}
	if err != nil {
		return err
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command(path, "kp:"+token)
	} else {
		cmd = exec.Command(path, "key", token)
	}
	return runCmd(cmd, fmt.Sprintf("pressKey(%s)", keyName))
}

// scrollWheel scrolls the wheel by amount notches (positive = down/forward,
// negative = up/back).
//
//	macOS: cliclick scroll:dy  (positive dy scrolls down)
//	Linux: xdotool click 4 (up) / 5 (down), repeated |amount| times
func scrollWheel(amount int) error {
	path, err := mustInputTool()
	if err != nil {
		return err
	}
	if amount == 0 {
		return nil
	}
	if runtime.GOOS == "darwin" {
		// cliclick's scroll takes a delta in "lines"; map one notch → one line.
		cmd := exec.Command(path, "scroll:", fmt.Sprintf("%d", amount))
		return runCmd(cmd, fmt.Sprintf("scrollWheel(%d)", amount))
	}
	// Linux: button 4 = wheel up, 5 = wheel down. Repeat per notch.
	btn := "5"
	if amount < 0 {
		btn = "4"
	}
	notches := amount
	if notches < 0 {
		notches = -notches
	}
	args := make([]string, 0, 1+notches*2)
	args = append(args, "click", "--repeat", fmt.Sprintf("%d", notches), btn)
	return runCmd(exec.Command(path, args...), fmt.Sprintf("scrollWheel(%d)", amount))
}

// --- macOS key-name translation ---------------------------------------------

// macKeyName maps a platform-agnostic key name to a cliclick key token.
// Letters stay single-char lowercase; named keys use cliclick's spelling
// (return/escape/tab/up/down/...). F-keys are f1..f12.
func macKeyName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) == 1 {
		c := name[0]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return name, nil
		}
		return "", fmt.Errorf("unknown key %q", name)
	}
	switch name {
	case "enter", "return":
		return "return", nil
	case "esc", "escape":
		return "escape", nil
	case "tab":
		return "tab", nil
	case "space":
		return "space", nil
	case "delete", "del":
		// macOS "delete" is backspace; forward-delete is "fn+delete".
		return "delete", nil
	case "backspace":
		return "delete", nil
	case "home":
		return "home", nil
	case "end":
		return "end", nil
	case "pageup":
		return "pageup", nil
	case "pagedown":
		return "pagedown", nil
	case "arrowup", "up":
		return "up", nil
	case "arrowdown", "down":
		return "down", nil
	case "arrowleft", "left":
		return "left", nil
	case "arrowright", "right":
		return "right", nil
	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		return name, nil
	}
	return "", fmt.Errorf("unknown key %q", name)
}

// macModifier maps a platform-agnostic modifier to its cliclick token. Note the
// deliberate ctrl → cmd mapping: on macOS the "primary" modifier (the one the
// LLM means by "ctrl+s") is Command, not Control — matching how every native
// mac app binds save/copy/paste. If a caller truly needs macOS Control they'd
// need a separate "control" token; that's not exposed by the LLM schema.
func macModifier(name string) (string, error) {
	switch name {
	case "ctrl":
		return "cmd", nil
	case "shift":
		return "shift", nil
	case "alt":
		// macOS "alt" key is labelled Option; cliclick accepts "alt" and "option".
		return "alt", nil
	}
	return "", fmt.Errorf("unknown modifier %q", name)
}

// macKeyCombo builds a cliclick key combo string "mod1+mod2+key" from the
// platform-agnostic modName ("ctrl+shift", "") and keyName.
func macKeyCombo(modName, keyName string) (string, error) {
	key, err := macKeyName(keyName)
	if err != nil {
		return "", err
	}
	parts := []string{key}
	for _, m := range splitModifiers(modName) {
		tok, err := macModifier(m)
		if err != nil {
			return "", err
		}
		parts = append([]string{tok}, parts...) // modifiers first, key last
	}
	return strings.Join(parts, "+"), nil
}

// --- Linux (X11) key-name translation ---------------------------------------

// linuxKeyName maps a platform-agnostic key name to an xdotool/X11 keysym.
// xdotool accepts both "s" and "s" (lowercase letter) and named keysyms
// (Return, Escape, Tab, Up/Down/Left/Right, F1..F12, ...).
func linuxKeyName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) == 1 {
		c := name[0]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return name, nil
		}
		return "", fmt.Errorf("unknown key %q", name)
	}
	switch name {
	case "enter", "return":
		return "Return", nil
	case "esc", "escape":
		return "Escape", nil
	case "tab":
		return "Tab", nil
	case "space":
		return "space", nil
	case "delete", "del":
		return "Delete", nil
	case "backspace":
		return "BackSpace", nil
	case "home":
		return "Home", nil
	case "end":
		return "End", nil
	case "pageup":
		return "Page_Up", nil
	case "pagedown":
		return "Page_Down", nil
	case "arrowup", "up":
		return "Up", nil
	case "arrowdown", "down":
		return "Down", nil
	case "arrowleft", "left":
		return "Left", nil
	case "arrowright", "right":
		return "Right", nil
	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		// X11 keysyms are uppercase F1..F12.
		return strings.ToUpper(name), nil
	}
	return "", fmt.Errorf("unknown key %q", name)
}

// linuxModifier maps a platform-agnostic modifier to its xdotool/X11 name. On
// Linux ctrl stays ctrl (the native primary modifier), unlike macOS.
func linuxModifier(name string) (string, error) {
	switch name {
	case "ctrl":
		return "ctrl", nil
	case "shift":
		return "shift", nil
	case "alt":
		return "alt", nil
	}
	return "", fmt.Errorf("unknown modifier %q", name)
}

// linuxKeyCombo builds an xdotool key combo string "mod1+mod2+key" from the
// platform-agnostic modName ("ctrl+shift", "") and keyName.
func linuxKeyCombo(modName, keyName string) (string, error) {
	key, err := linuxKeyName(keyName)
	if err != nil {
		return "", err
	}
	parts := []string{key}
	for _, m := range splitModifiers(modName) {
		tok, err := linuxModifier(m)
		if err != nil {
			return "", err
		}
		parts = append([]string{tok}, parts...) // modifiers first, key last
	}
	return strings.Join(parts, "+"), nil
}
