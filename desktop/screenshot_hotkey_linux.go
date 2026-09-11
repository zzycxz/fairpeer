package main

// screenshot_hotkey_linux.go implements hotkey detection for Linux by polling
// modifier state via xinput. No CGO required.
//
// Requires: xinput (x11-apps/xorg-xinput). There is no global keyboard hook
// without CGO/XRecord, so the hotkey triggers on the configured modifier chord
// alone (main-key detection isn't reliably possible via CLI); the tray menu —
// available when a StatusNotifier host exists — is the other reliable trigger.
//
// Mechanics: `xinput list --name-only` enumerates input devices; for each,
// `xinput query-state <name>` prints the key state as `key[N]=down|up` lines
// (KeyClass block). N indexes the device's key-state vector, which starts at
// the server's min keycode (8), so a reported index can mean keycode N or N+8;
// we accept both when matching against the standard evdev modifier keycodes
// (37/105 Ctrl, 50/62 Shift, 64/108 Alt, 133/134 Super — stable across modern
// distros). Wayland note: xinput only sees Xwayland clients' keys; on pure
// Wayland sessions detection may not fire — the tray menu remains the trigger.

import (
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

// parseHotkey modifier bits (mirrors screenshot_solve.go).
const (
	hotkeyModAlt   = 0x0001
	hotkeyModCtrl  = 0x0002
	hotkeyModShift = 0x0004
	hotkeyModSuper = 0x0008
)

// standard evdev X keycodes for the modifiers (XKB "evdev" rules).
var modifierKeycodes = map[int]uint{
	37:  hotkeyModCtrl,  // Control_L
	105: hotkeyModCtrl,  // Control_R
	50:  hotkeyModShift, // Shift_L
	62:  hotkeyModShift, // Shift_R
	64:  hotkeyModAlt,   // Alt_L
	108: hotkeyModAlt,   // Alt_R
	133: hotkeyModSuper, // Super_L
	134: hotkeyModSuper, // Super_R
}

// Loop tuning. Each tick forks one xinput per device, so 100ms (the old rate)
// would spawn dozens of processes per second on multi-keyboard setups; 250ms
// keeps worst-case latency acceptable at a fraction of the fork load.
const (
	hotkeyPollInterval     = 250 * time.Millisecond
	hotkeyTriggerCooldown  = 500 * time.Millisecond
	deviceListRefreshTicks = 40 // re-run `xinput list` every ~10s (hotplug)
	maxDeviceQueryFailures = 8  // ~2s of total query failure → refresh/stop
)

type hotkeyManager struct {
	app      *App
	stopCh   chan struct{}
	stopOnce sync.Once
	want     uint     // required modifier mask (parseHotkey bits); never 0 in the loop
	devices  []string // cached `xinput list --name-only` output
}

func (a *App) StartScreenshotHotkey() {
	cfg, err := config.Load()
	if err != nil || !cfg.Cowork.ScreenshotEnabled {
		return
	}
	mod, _, err := parseHotkey(cfg.Cowork.ScreenshotHotkey)
	if err != nil {
		slog.Warn("screenshot: invalid hotkey config", "hotkey", cfg.Cowork.ScreenshotHotkey, "err", err)
		return
	}
	// Linux detection is modifier-chord-only: a modifier-less hotkey would
	// either never fire (main key unknowable) or fire on any keypress. Never
	// trigger on an empty modifier set — say so instead of spinning a dead loop.
	if mod == 0 {
		slog.Warn("screenshot: hotkey has no modifiers; Linux detection triggers on modifier chords only — polling disabled (use e.g. Ctrl+Alt+S, or the tray menu)", "hotkey", cfg.Cowork.ScreenshotHotkey)
		return
	}
	hk := &hotkeyManager{
		app:    a,
		stopCh: make(chan struct{}),
		want:   uint(mod),
	}
	a.mu.Lock()
	a.hotkeyMgr = hk
	a.mu.Unlock()
	slog.Info("screenshot: hotkey polling started (Linux xinput)", "hotkey", cfg.Cowork.ScreenshotHotkey)
	go hk.loop()
}

func (a *App) StopScreenshotHotkey() {
	a.mu.Lock()
	hk := a.hotkeyMgr
	a.hotkeyMgr = nil
	a.mu.Unlock()
	if hk != nil {
		hk.Stop()
	}
}

func (h *hotkeyManager) loop() {
	// xinput missing at start: the feature honestly doesn't work on this
	// system — warn once and let the goroutine exit instead of silently
	// spinning forever.
	if !h.refreshDevices() {
		slog.Warn("screenshot: xinput unavailable; Linux hotkey polling disabled — trigger screenshot solving from the tray menu instead", "want_mods", h.want)
		return
	}
	ticker := time.NewTicker(hotkeyPollInterval)
	defer ticker.Stop()
	lastTrigger := time.Time{}
	wasPressed := false
	ticks := 0
	queryFailures := 0
	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			ticks++
			// Hotplug: refresh the cached device list periodically.
			if ticks%deviceListRefreshTicks == 0 {
				h.refreshDevices()
			}
			held, ok := h.queryModifiersDown()
			pressed := ok && held&h.want == h.want
			// Edge-triggered like the Windows loop: fire on the press edge
			// only, with the same 500ms cooldown — never every tick while held.
			if pressed && !wasPressed && time.Since(lastTrigger) > hotkeyTriggerCooldown {
				lastTrigger = time.Now()
				slog.Info("screenshot: hotkey modifiers detected (Linux)")
				h.app.triggerScreenshotSolve()
			}
			wasPressed = pressed
			// Every device query failing usually means a stale device list
			// (keyboard unplugged) or a dying X session; refresh once, and if
			// xinput itself is gone, stop the loop with a warning.
			if !ok {
				queryFailures++
				if queryFailures >= maxDeviceQueryFailures {
					if !h.refreshDevices() {
						slog.Warn("screenshot: xinput no longer available; Linux hotkey polling stopped — trigger screenshot solving from the tray menu instead")
						return
					}
					queryFailures = 0
				}
			} else {
				queryFailures = 0
			}
		}
	}
}

func (h *hotkeyManager) Stop() {
	h.stopOnce.Do(func() { close(h.stopCh) })
}

// refreshDevices re-runs `xinput list --name-only` and caches the device names.
// Returns false when xinput is unusable (missing binary, or the list can't be
// read — no X connection).
func (h *hotkeyManager) refreshDevices() bool {
	if _, err := exec.LookPath("xinput"); err != nil {
		return false
	}
	out, err := exec.Command("xinput", "list", "--name-only").Output()
	if err != nil {
		return false
	}
	var devices []string
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			devices = append(devices, name)
		}
	}
	if len(devices) == 0 {
		return false
	}
	h.devices = devices
	return true
}

// queryModifiersDown returns the OR of modifier bits currently held across all
// known devices. ok is false when xinput failed for every device (the caller
// refreshes its device list / stops the loop on repeated total failure).
// Per-device errors are normal — `xinput list` includes virtual-core names that
// query-state rejects — so they never mark the whole poll failed.
func (h *hotkeyManager) queryModifiersDown() (held uint, ok bool) {
	for _, dev := range h.devices {
		out, err := exec.Command("xinput", "query-state", dev).Output()
		if err != nil {
			continue
		}
		ok = true
		held |= parsePressedModifiers(string(out))
	}
	return held, ok
}

// keyDownLine matches a KeyClass state line: `\tkey[37]=down`.
var keyDownLine = regexp.MustCompile(`^key\[(\d+)\]=down$`)

// parsePressedModifiers extracts the held modifier bits from one
// `xinput query-state` output. Non-keyboard devices simply have no key lines
// and yield 0.
func parsePressedModifiers(out string) uint {
	var held uint
	for _, line := range strings.Split(out, "\n") {
		m := keyDownLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		// The key[] index is 0-based from the server's min keycode (8), so
		// match both interpretations against the evdev modifier keycodes.
		if mod, found := modifierKeycodes[n]; found {
			held |= mod
		}
		if mod, found := modifierKeycodes[n+8]; found {
			held |= mod
		}
	}
	return held
}
