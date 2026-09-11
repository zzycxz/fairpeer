package main

// estop_hotkey_windows.go implements the global EMERGENCY-STOP hotkey for coWork
// desktop automation. When the user presses the configured combo (default
// Ctrl+Shift+Pause/Break) ANYWHERE on their desktop — even with fairpeer
// minimized and another app focused — we cancel the in-flight turn on the
// active tab. This is the safety baseline for screen_* tools, which perform
// IRREVERSIBLE physical actions (clicks, typing): once a wrong button is
// clicked or a destructive confirm pressed, you can't take it back, so the user
// needs an always-available "kill switch" that doesn't depend on fairpeer
// having focus.
//
// Why a SECOND Win32 hotkey (not a frontend useGlobalShortcut): the value of an
// emergency stop is precisely that it works while the user is watching ANOTHER
// window (the app the agent is driving). Frontend keydown listeners die when
// fairpeer loses focus, so a JS handler would be useless at the exact moment it
// matters. RegisterHotKey is system-global and fires regardless of focus. This
// mirrors the screenshot hotkey's design (screenshot_hotkey_windows.go); both
// are shown in the shortcuts cheatsheet as displayOnly entries.
//
// The hotkey reuses the screenshot hotkey's message-only window + message loop
// (a single hidden window can receive multiple hotkey ids), so we add a new
// hotkey id rather than a second window/pump. If the screenshot hotkey feature
// is off (no window exists yet), we create our own window + loop.

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

const (
	// estopHotkeyID is a unique id distinct from the screenshot hotkey
	// (0x7A21) so RegisterHotKey dispatches to the right handler.
	estopHotkeyID = 0x7A22
	// vkPause is the virtual-key code for the Pause/Break key — a natural
	// "stop" key that almost no other app binds globally.
	vkPause = 0x13
)

// estopManager owns the emergency-stop hotkey registration + message loop. It
// is separate from hotkeyManager (screenshot) so the two features can be
// started/stopped independently, but it reuses the same Win32 procs and
// window-creation helpers.
type estopManager struct {
	app      *App
	hwnd     uintptr
	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
}

// estopMgr is the singleton held on App so Stop can reach it at shutdown.
// (estopManager) is the in-flight manager; nil when the feature is off/stopped.

// StartEStopHotkey registers the global emergency-stop hotkey and begins
// pumping its message loop. Called from app startup. If the combo is already
// taken by another app, RegisterHotKey fails and we log a warning (the user
// must pick a different combination). Failure is non-fatal: coWork still works,
// just without the kill switch.
func (a *App) StartEStopHotkey() {
	em := &estopManager{app: a, stopCh: make(chan struct{})}
	hotkeyStr := a.estopHotkeyString()
	if hotkeyStr == "" {
		return // feature disabled by empty config
	}
	// Register the manager BEFORE starting the goroutine (same rationale as
	// StartScreenshotHotkey).
	a.mu.Lock()
	a.estopMgr = em
	a.mu.Unlock()
	go func() {
		// Pin to one OS thread: RegisterHotKey + PeekMessage must share a
		// thread or WM_HOTKEY won't be delivered (see screenshot_hotkey for
		// the full explanation).
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// Settings toggles Stop→Start back to back: the OLD loop tears its
		// hotkey down on its next ≤100ms tick, so an immediate RegisterHotKey
		// here can lose the race and silently kill the feature. Retry briefly.
		var regErr error
		for attempt := 0; attempt < 5; attempt++ {
			regErr = em.register(hotkeyStr)
			if regErr == nil {
				break
			}
			select {
			case <-em.stopCh:
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		if regErr != nil {
			slog.Warn("estop: hotkey registration failed (combination may be in use by another app); emergency stop unavailable",
				"hotkey", hotkeyStr, "err", regErr)
			return
		}
		slog.Info("estop: global emergency-stop hotkey registered", "hotkey", hotkeyStr)
		em.loop()
	}()
}

// StopEStopHotkey tears down the hotkey + message loop at shutdown.
func (a *App) StopEStopHotkey() {
	a.mu.Lock()
	em := a.estopMgr
	a.estopMgr = nil
	a.mu.Unlock()
	if em != nil {
		em.Stop()
	}
}

// estopHotkeyString / defaultEStopHotkey / emitEStopNotice live in
// estop_shared.go (platform-neutral).

// register creates a hidden message-only window (its own, so the feature works
// even if the screenshot hotkey is off and created no window) and registers the
// combo against it.
func (e *estopManager) register(hotkeyStr string) error {
	hwnd := createMessageWindow()
	if hwnd == 0 {
		return fmt.Errorf("create message window failed")
	}
	destroy := func() { procDestroyWindow.Call(hwnd) }
	mod, vk, err := parseEStopHotkey(hotkeyStr)
	if err != nil {
		destroy() // never leak the message-only window on a bad combo
		return err
	}
	r1, _, _ := procRegisterHotKey.Call(
		uintptr(hwnd),
		uintptr(estopHotkeyID),
		uintptr(mod),
		uintptr(vk),
	)
	if r1 == 0 {
		destroy()
		return fmt.Errorf("RegisterHotKey failed (combination may be in use)")
	}
	e.hwnd = hwnd
	e.app.estopHwnd = hwnd
	return nil
}

// loop pumps the Windows message loop, dispatching WM_HOTKEY to onHotkey. Uses
// non-blocking PeekMessage (same rationale as the screenshot hotkey: so Stop()
// can interrupt an idle pump rather than blocking forever in GetMessage).
func (e *estopManager) loop() {
	msg := make([]byte, 48) // MSG struct
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		// Drain all currently-queued messages (non-blocking).
		for {
			ret, _, _ := procPeekMessage.Call(
				uintptr(unsafe.Pointer(&msg[0])),
				0, 0, 0,
				uintptr(pmRemove),
			)
			if ret == 0 {
				break
			}
			// MSG.message is at offset 8 on amd64 (hwnd uintptr = 8 bytes),
			// not 4 (32-bit assumption). See screenshot_hotkey_windows.go for
			// the full explanation — same bug, same fix.
			msgID := *(*uint32)(unsafe.Pointer(&msg[8]))
			if msgID == wmHotkey {
				e.onHotkey()
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
			_, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
		}
		select {
		case <-e.stopCh:
			// Teardown here, on the SAME OS thread that registered the hotkey
			// and owns the message window: DestroyWindow from another thread
			// fails silently (and UnregisterHotKey raced Stop's shutdown
			// goroutine). The goroutine holds its locked thread for life.
			e.teardown()
			return
		case <-ticker.C:
		}
	}
}

// onHotkey fires when the global emergency-stop hotkey is pressed: cancel the
// in-flight turn on the active tab and surface a red toast so the user gets
// immediate confirmation the stop landed. Cancel is a no-op if no turn is
// running, so an accidental press during idle is harmless.
func (e *estopManager) onHotkey() {
	// Cancel the active tab's in-flight turn. CancelTab is a no-op when nothing
	// is running, so stray presses don't error.
	e.app.CancelTab("")
	// Surface immediate confirmation via a frontend event → red toast. Emit
	// regardless of whether a turn was running: the user pressed STOP and
	// deserves a visible ack that the input registered.
	e.app.emitEStopNotice()
}

// Stop unregisters the hotkey and stops the message loop. Idempotent. The
// actual Unregister/Destroy runs on the loop goroutine's locked thread (see
// loop's stopCh branch) — Stop only signals.
func (e *estopManager) Stop() {
	e.stopOnce.Do(func() {
		close(e.stopCh)
	})
}

// teardown unregisters the hotkey and destroys the message-only window, so
// repeated stop/start cycles of the estop feature never leak HWNDs.
func (e *estopManager) teardown() {
	if e.hwnd == 0 {
		return
	}
	procUnregisterHotKey.Call(uintptr(e.hwnd), uintptr(estopHotkeyID))
	procDestroyWindow.Call(uintptr(e.hwnd))
	e.hwnd = 0
}
