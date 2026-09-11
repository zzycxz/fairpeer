//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package main

// estop_hotkey_unix.go — global emergency-stop for unix desktops, the
// counterpart of the Win32 RegisterHotKey implementation. There is no portable
// global-hotkey API (X11 XGrabKey needs a C event loop, Wayland needs the
// compositor portal, macOS needs CGEventTap/cgo), so unix polls the pressed
// key state and fires when the configured combo is detected:
//
//   - Linux/BSD: `xinput query-state` reports every pressed key, so BOTH the
//     modifiers and the main key are matched — the trigger is exact.
//   - macOS: System Events only exposes modifier state via osascript, so the
//     trigger is the modifier CHORD alone. To keep stray chords (e.g. normal
//     copy/paste traffic while Ctrl+Shift is already held) from cancelling a
//     run by accident, the chord must be held for two consecutive polls
//     (~500 ms).
//
// A global kill switch matters because screen_* automation performs
// irreversible physical actions while ANOTHER app has focus — the in-app
// Ctrl/Cmd+Shift+\ stop dies exactly when fairpeer loses focus. CancelTab("")
// is a no-op when nothing is running, so a false trigger is harmless.

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// unixEStop holds the polling loop state. Mirrors the windows estopManager
// contract (Start/Stop via App.estopMgr).
type estopManager struct {
	app      *App
	stopCh   chan struct{}
	ctx      context.Context // cancelled by Stop: every helper probe derives its deadline from this
	stopCtxC context.CancelFunc
	stopOnce sync.Once
}

// StartEStopHotkey polls for the configured emergency-stop combo. Failure is
// non-fatal: coWork keeps working, just without the global kill switch.
func (a *App) StartEStopHotkey() {
	hotkeyStr := a.estopHotkeyString()
	if hotkeyStr == "" {
		return // feature disabled by empty config
	}
	ctx, cancel := context.WithCancel(context.Background())
	em := &estopManager{app: a, stopCh: make(chan struct{}), ctx: ctx, stopCtxC: cancel}
	a.mu.Lock()
	a.estopMgr = em
	a.mu.Unlock()
	go em.run(hotkeyStr)
}

// StopEStopHotkey tears down the polling loop at shutdown.
func (a *App) StopEStopHotkey() {
	a.mu.Lock()
	em := a.estopMgr
	a.estopMgr = nil
	a.mu.Unlock()
	if em != nil {
		em.Stop()
	}
}

func (e *estopManager) Stop() {
	e.stopOnce.Do(func() {
		close(e.stopCh)
		e.stopCtxC()
	})
}

func (e *estopManager) run(hotkeyStr string) {
	platform := runtime.GOOS
	fire, describe, err := unixEStopDetector(e.ctx, hotkeyStr)
	if err != nil {
		slog.Warn("estop: global emergency-stop unavailable on this desktop",
			"hotkey", hotkeyStr, "platform", platform, "err", err)
		return
	}
	slog.Info("estop: global emergency-stop polling active", "hotkey", hotkeyStr, "platform", platform,
		"detection", describe)
	var held int // consecutive polls with the trigger satisfied
	var cooledAt time.Time
	var failStreak int
	var failWarned sync.Once
	// A wedged helper (osascript after sleep, X server gone) must not wedge
	// the loop: every probe carries its own 2s deadline (see the detectors).
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			return
		case <-ticker.C:
		}
		satisfied, err := fire()
		if err != nil {
			// Permission/class errors (Automation prompt on macOS, xinput
			// missing on Linux) warn ONCE; sustained failure exits the loop —
			// the user can re-enable it from settings, which restarts polling.
			failWarned.Do(func() {
				slog.Warn("estop: key-state poll failing (grant the Automation/System Events permission on macOS, or install xinput on Linux); polling stops after sustained failure", "err", err)
			})
			failStreak++
			if failStreak >= 40 { // ~10s of continuous failure
				slog.Warn("estop: key-state probe keeps failing — stopping the polling loop; toggle the estop hotkey setting to restart it")
				return
			}
			held = 0
			continue
		}
		failStreak = 0
		if !satisfied {
			held = 0
			continue
		}
		held++
		// Chord-only detection demands a deliberate ~500 ms hold; the cooldown
		// keeps a held chord from re-firing every tick.
		need := 1
		if describe == estopChordOnly {
			need = 2
		}
		if held < need || time.Since(cooledAt) < 2*time.Second {
			continue
		}
		cooledAt = time.Now()
		held = 0
		e.app.CancelTab("")
		e.app.emitEStopNotice()
	}
}

// estopChordOnly marks detectors that cannot see the main key (macOS System
// Events exposes only modifier flags), so the loop adds the hold-2-ticks
// interlock against accidental chords.
const estopChordOnly = "modifier chord only (main key not detectable on this platform)"

// unixEStopDetector builds a per-platform "is the combo pressed right now"
// closure plus a description of what is actually detectable.
func unixEStopDetector(ctx context.Context, hotkeyStr string) (fire func() (bool, error), describe string, err error) {
	switch runtime.GOOS {
	case "darwin":
		mod, _, perr := parseHotkey(hotkeyStr)
		if perr != nil {
			return nil, "", perr
		}
		if mod == 0 {
			return nil, "", fmt.Errorf("hotkey %q has no modifiers — macOS can only detect modifier chords", hotkeyStr)
		}
		return func() (bool, error) { return chordHeldMacOS(ctx, mod) }, estopChordOnly, nil
	default: // linux + BSDs: X11 xinput reads every key
		return estopDetectorLinux(ctx, hotkeyStr)
	}
}

// ── macOS: modifier chord via osascript System Events ──────────────────────

// chordHeldMacOS reports whether every modifier bit in mod (screenshot_solve.go
// hotkey bits) is currently held.
func chordHeldMacOS(ctx context.Context, mod int) (bool, error) {
	var checks []string
	if mod&0x0002 != 0 { // Control
		checks = append(checks, "control down")
	}
	if mod&0x0004 != 0 { // Shift
		checks = append(checks, "shift down")
	}
	if mod&0x0001 != 0 { // Alt/Option
		checks = append(checks, "option down")
	}
	if mod&0x0008 != 0 { // Win/Command
		checks = append(checks, "command down")
	}
	if len(checks) == 0 {
		return false, nil
	}
	script := fmt.Sprintf(`tell application "System Events" to return {%s}`, strings.Join(checks, ", "))
	probeCtx, probeCancel := context.WithTimeout(ctx, 2*time.Second)
	defer probeCancel()
	out, err := exec.CommandContext(probeCtx, "osascript", "-e", script).Output()
	if err != nil {
		// osascript fails exactly when the Automation (System Events)
		// permission was denied or never granted — surface it so this never
		// looks like a silent no-op.
		return false, fmt.Errorf("osascript key-state query failed (Automation permission for System Events?): %w", err)
	}
	result := strings.TrimSpace(string(out))
	return strings.Contains(result, "true") && !strings.Contains(result, "false"), nil
}

// ── Linux/BSD: full combo via xinput query-state ───────────────────────────

// vkToXKeycode maps the virtual-key codes keyToVK produces (screenshot_solve.go)
// onto X11 keycodes (evdev base + 8, what `xinput` reports modulo the server's
// min-keycode offset — the caller matches both n and n+8, mirroring
// parsePressedModifiers).
var vkToXKeycode = map[int]int{
	'Q': 24, 'W': 25, 'E': 26, 'R': 27, 'T': 28, 'Y': 29, 'U': 30, 'I': 31, 'O': 32, 'P': 33,
	'A': 38, 'S': 39, 'D': 40, 'F': 41, 'G': 42, 'H': 43, 'J': 44, 'K': 45, 'L': 46,
	'Z': 52, 'X': 53, 'C': 54, 'V': 55, 'B': 56, 'N': 57, 'M': 58,
	'0': 19, '1': 10, '2': 11, '3': 12, '4': 13, '5': 14, '6': 15, '7': 16, '8': 17, '9': 18,
	0x70: 67, 0x71: 68, 0x72: 69, 0x73: 70, 0x74: 71, 0x75: 72, 0x76: 73, 0x77: 74, 0x78: 75, 0x79: 76, // F1-F10
	0x7A: 95, 0x7B: 96, // F11, F12
	0x20: 65,  // Space
	0x0D: 36,  // Enter
	0x09: 23,  // Tab
	0x08: 22,  // Backspace
	0x1B: 9,   // Escape
	0x13: 127, // Pause — the default estop main key
	0x2D: 118, // Insert
	0x2E: 119, // Delete
	0x21: 112, // Page Up
	0x22: 117, // Page Down
	0x24: 110, // Home
	0x23: 114, // End
}

// estopLinux carries the xinput device cache for one estop loop.
type estopLinux struct {
	ctx        context.Context // cancelled by Stop: probes derive deadlines from this
	mods       uint            // required modifier bits (screenshot_solve.go hotkey bits)
	mainX      int             // X keycode of the main key; 0 = chord-only
	devices    []string
	refreshed  time.Time
	deadStreak int // consecutive all-device query failures → surfaced as an error
}

func estopDetectorLinux(ctx context.Context, hotkeyStr string) (func() (bool, error), string, error) {
	mod, vk, err := parseHotkey(hotkeyStr)
	if err != nil {
		return nil, "", err
	}
	if mod == 0 {
		return nil, "", fmt.Errorf("hotkey %q has no modifiers — refusing a trigger that fires on every keypress", hotkeyStr)
	}
	st := &estopLinux{ctx: ctx, mods: uint(mod)}
	describe := "full combo (modifiers + main key)"
	if code, ok := vkToXKeycode[vk]; ok {
		st.mainX = code
	} else {
		// Unmapped main key (rare): degrade to the chord-only interlock rather
		// than never firing at all.
		st.mainX = 0
		describe = estopChordOnly
	}
	return st.poll, describe, nil
}

func (s *estopLinux) poll() (bool, error) {
	if _, err := exec.LookPath("xinput"); err != nil {
		return false, fmt.Errorf("xinput not found (install x11-xserver-utils / xorg-xinput)")
	}
	if len(s.devices) == 0 || time.Since(s.refreshed) > 10*time.Second {
		s.refresh()
	}
	modsDown := uint(0)
	mainDown := false
	anyOK := false
	for _, dev := range s.devices {
		qCtx, qCancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(qCtx, "xinput", "query-state", dev).Output()
		qCancel()
		if err != nil {
			continue // virtual-core names reject query-state; normal
		}
		anyOK = true
		modsDown |= parseEstopPressedModifiers(string(out))
		if s.mainX != 0 && keyDownIn(string(out), s.mainX) {
			mainDown = true
		}
	}
	if !anyOK {
		// Every query failed — likely a hotplug or the X session went away.
		// Count it so the loop's failStreak can escalate (exit after ~10s)
		// instead of forking xinput forever with zero detection.
		s.deadStreak++
		if s.deadStreak >= 8 {
			s.deadStreak = 0
			return false, fmt.Errorf("xinput query-state failed on every device %d times in a row", 8)
		}
		s.refresh()
		return false, nil
	}
	s.deadStreak = 0
	if modsDown&s.mods != s.mods {
		return false, nil
	}
	if s.mainX != 0 && !mainDown {
		return false, nil
	}
	return true, nil
}

func (s *estopLinux) refresh() {
	lCtx, lCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer lCancel()
	out, err := exec.CommandContext(lCtx, "xinput", "list", "--name-only").Output()
	if err != nil {
		return
	}
	var devices []string
	for _, line := range strings.Split(string(out), "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		// Pointing devices never carry the modifier keys we need; skipping
		// them bounds the per-tick fork count on peripheral-heavy desks.
		lower := strings.ToLower(name)
		if strings.Contains(lower, "mouse") || strings.Contains(lower, "touchpad") ||
			strings.Contains(lower, "pointer") || strings.Contains(lower, "consumer") {
			continue
		}
		devices = append(devices, name)
		if len(devices) >= 8 { // fork-cost cap for peripheral farms
			break
		}
	}
	if len(devices) > 0 {
		s.devices = devices
		s.refreshed = time.Now()
	}
}

// estopKeyDownLine matches a KeyClass state line: `key[N]=down` (same wire
// shape the screenshot hotkey reads).
var estopKeyDownLine = regexp.MustCompile(`^key\[(\d+)\]=down$`)

// estopModifierKeycodes maps evdev modifier keycodes onto the shared hotkey
// bits (screenshot_solve.go): Ctrl 29/97, Shift 42/54, Alt 56/100,
// Super 125/126 (and Mac-style 133/134 for keyboards that report them).
var estopModifierKeycodes = map[int]uint{
	29: 0x0002, 97: 0x0002, 42: 0x0004, 54: 0x0004,
	56: 0x0001, 100: 0x0001, 125: 0x0008, 126: 0x0008,
	133: 0x0008, 134: 0x0008,
}

// parseEstopPressedModifiers extracts the held modifier bits from one
// `xinput query-state` output. Non-keyboard devices have no key lines and
// yield 0. (Self-contained: the screenshot hotkey's parser lives in a
// _linux-suffixed file and is invisible to darwin builds.)
func parseEstopPressedModifiers(out string) uint {
	var held uint
	for _, line := range strings.Split(out, "\n") {
		m := estopKeyDownLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if mod, found := estopModifierKeycodes[n]; found {
			held |= mod
		}
		if mod, found := estopModifierKeycodes[n+8]; found {
			held |= mod
		}
	}
	return held
}

// keyDownIn reports whether the given X keycode is currently held in one
// `xinput query-state` output, matching both the raw and the min-keycode-8
// offset interpretation.
func keyDownIn(out string, x int) bool {
	for _, line := range strings.Split(out, "\n") {
		m := estopKeyDownLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n == x || n+8 == x {
			return true
		}
	}
	return false
}
