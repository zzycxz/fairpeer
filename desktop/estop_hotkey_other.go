//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package main

// estopManager is a no-op stub on unix platforms outside the supported desktop
// set (linux/darwin/BSD get the polling kill switch in estop_hotkey_unix.go).
type estopManager struct{}

// StartEStopHotkey is a no-op here — no key-state probe exists on these
// platforms. The stub keeps app.go compilable cross-platform.
func (a *App) StartEStopHotkey() {}

// StopEStopHotkey mirrors the no-op for symmetry with StartEStopHotkey so
// shutdown code is unconditional.
func (a *App) StopEStopHotkey() {}
