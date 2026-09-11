//go:build linux || freebsd || netbsd || openbsd || dragonfly

package main

// autostart_linux.go — launch-at-login via the XDG autostart convention
// (~/.config/autostart/*.desktop), honored by GNOME, KDE, and most other
// desktop environments. No admin rights, per-user.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const autostartDesktopEntryName = "fairpeer-desktop-autostart.desktop"

func autostartEntryPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "autostart", autostartDesktopEntryName), nil
}

func osExecutableClean() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

func enableAutostart() error {
	exe, err := autostartExePath()
	if err != nil {
		return err
	}
	path, err := autostartEntryPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Quote Exec per the desktop-entry spec so spaces in home dirs survive.
	entry := `[Desktop Entry]
Type=Application
Name=fairpeer
Comment=fairpeer desktop (launch at login)
Exec=` + fmt.Sprintf("%q", exe) + `
Terminal=false
X-GNOME-Autostart-enabled=true
`
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		return err
	}
	// Register the entry with the session's MIME/database handlers when the
	// tooling exists; failure is non-fatal — autostart reads the file directly.
	_, _ = exec.Command("update-desktop-database", filepath.Dir(path)).Output()
	return nil
}

func disableAutostart() error {
	path, err := autostartEntryPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func autostartRegistered() (bool, error) {
	path, err := autostartEntryPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
