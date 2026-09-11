//go:build windows

package main

// autostart_windows.go — launch-at-login via the per-user Run key (HKCU), so
// no admin rights are involved and the registration follows the logged-in
// user, matching the per-user NSIS install.

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

const (
	autostartRunKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	autostartValueName = "fairpeer-desktop"
)

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
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	// Quote the path: install dirs contain spaces (C:\Users\...\AppData\...).
	return k.SetStringValue(autostartValueName, `"`+exe+`"`)
}

func disableAutostart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(autostartValueName); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}

func autostartRegistered() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autostartValueName)
	if err == registry.ErrNotExist {
		return false, nil
	}
	return err == nil, err
}
