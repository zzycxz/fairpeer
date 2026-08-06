// Platform-agnostic input primitives for desktop automation.
//
// This file documents the contract of the six input functions that screen_*
// tools (screen_tools.go) call. There are NO declarations here on purpose:
// Go has no header-style prototypes, and duplicate func declarations across
// files in the same package must each carry a body (and would collide if
// both compiled). Instead each platform implements the full set:
//
//   - Windows (screen_windows.go): Win32 SendInput — SetCursorPos for mouse,
//     mouse events for clicks, KEYBDINPUT for keys, KEYEVENTF_UNICODE for
//     text, clipboard+Ctrl+V for long text.
//   - macOS / Linux (input_other.go): external commands — cliclick on macOS,
//     xdotool on Linux. Both are user-installed CLI tools that wrap the
//     native input APIs (Quartz CGEvent on macOS, XTest extension on Linux).
//
// The build tags select exactly one implementation, so the package compiles
// cleanly on every platform.
//
// The contract for pressKeyCombo / pressKey is deliberately string-based
// (platform-agnostic key names), NOT integer VK codes: the same key name
// ("ctrl", "enter", "f5") parses once in screen_tools.go (parseKeyCombo) and
// is translated per-platform inside the implementation. parseKeyCombo
// therefore returns (modName, keyName string) so it can live in
// platform-agnostic code.
//
// Function signatures:
//
//	moveMouse(x, y int) error
//	    Move the cursor to (x, y) in physical screen pixels.
//	    Windows: SetCursorPos + ±1px human-like jitter.
//	    macOS:   cliclick m:x,y
//	    Linux:   xdotool mousemove x y
//
//	clickMouse(button string) error
//	    Press + release a button ("left"/"right"/"middle") at the current
//	    cursor position. The caller (screen_click) does moveMouse first.
//	    Windows: SendInput MOUSEINPUT + jittered hold duration.
//	    macOS:   cliclick c:/. (rc:/. / mc:/. for right/middle)
//	    Linux:   xdotool click 1/2/3
//
//	typeText(text string) error
//	    Type text at the current keyboard focus. Unicode-safe on Windows via
//	    KEYEVENTF_UNICODE; UTF-8 passthrough on macOS/Linux.
//	    Windows: per-rune Unicode for short, clipboard+Ctrl+V for long.
//	    macOS:   cliclick t:text
//	    Linux:   xdotool type --clearmodifiers text
//
//	pressKeyCombo(modName, keyName string) error
//	    Press modifier+key simultaneously then release. modName/keyName are
//	    platform-agnostic key names (NOT VK codes); each impl translates.
//	    Windows: SendInput KEYBDINPUT with VK codes from parseVK.
//	    macOS:   osascript "key code" / "using {command down}" or cliclick kp:
//	    Linux:   xdotool key mod+key  (e.g. "ctrl+s", "alt+Tab")
//
//	pressKey(keyName string) error
//	    Press + release a single key. Used by screen_type's press_enter and
//	    by screen_key for combos without a modifier (e.g. "enter", "f5").
//	    Windows: SendInput KEYBDINPUT with VK code from parseVK.
//	    macOS:   osascript "key code" or cliclick kp:key
//	    Linux:   xdotool key keyName
//
//	scrollWheel(amount int) error
//	    Scroll the wheel by amount notches (positive = down/forward,
//	    negative = up/back). One notch = WHEEL_DELTA (120) on Windows.
//	    Windows: SendInput MOUSEINPUT with mouseData = amount * WHEEL_DELTA.
//	    macOS:   cliclick scroll:dy (N scroll events for N notches)
//	    Linux:   xdotool click 4 (up) / 5 (down), repeated for |amount|
//
// Key name vocabulary (case-insensitive, accepted by parseKeyCombo):
//
//	modifiers: ctrl, shift, alt
//	letters:   a-z
//	digits:    0-9
//	specials:  enter/return, esc/escape, tab, space, delete/del, backspace,
//	           home, end, pageup, pagedown
//	arrows:    arrowup/up, arrowdown/down, arrowleft/left, arrowright/right
//	function:  f1-f12
//
// Per-platform name mapping lives next to each implementation:
//
//   - Windows: parseVK maps a name → VK code.
//   - macOS:   toMacKeyCode / cliclick key tokens.
//   - Linux:   xdotool keysym names (X11).
//
// The functions return a clear error when the required backend is missing
// (e.g. cliclick/xdotool not on PATH), so the caller surfaces actionable
// feedback rather than a silent no-op.

package builtin
