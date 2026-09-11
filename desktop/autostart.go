package main

// autostart.go — launch-at-login support, a desktop-shell capability that was
// previously absent on EVERY platform (see the packaging audit). The config bit
// lives in [desktop] autostart; the OS-specific registration is per-file:
// registry Run key (windows), launchd agent (darwin), XDG autostart entry
// (linux). The config is written only after the registration itself succeeded,
// so the setting never lies about a registration that didn't take.

import (
	"fmt"
	"runtime"

	"github.com/zzycxz/fairpeer/internal/config"
)

// GetAutostart reports whether launch-at-login is currently registered with
// the OS (not just configured).
func (a *App) GetAutostart() (bool, error) {
	return autostartRegistered()
}

// SetAutostart registers or unregisters launch-at-login, then records the
// outcome in [desktop] autostart.
func (a *App) SetAutostart(enabled bool) error {
	if enabled {
		if err := enableAutostart(); err != nil {
			return err
		}
	} else if err := disableAutostart(); err != nil {
		return err
	}
	return a.applyConfigOnly(func(c *config.Config) error { return c.SetDesktopAutostart(enabled) })
}

// autostartExePath is the binary to register; errors are wrapped with the
// platform name because os.Executable failure modes differ per OS.
func autostartExePath() (string, error) {
	exe, err := osExecutableClean()
	if err != nil {
		return "", fmt.Errorf("autostart: resolve executable on %s: %w", runtime.GOOS, err)
	}
	return exe, nil
}
