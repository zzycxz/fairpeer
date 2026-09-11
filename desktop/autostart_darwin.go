//go:build darwin

package main

// autostart_darwin.go — launch-at-login via a user launchd agent
// (~/Library/LaunchAgents), the standard no-admin mechanism on macOS. The
// agent runs `open -a <bundle>` so a .app install keeps its bundle identity;
// raw binaries outside a bundle are launched directly.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const autostartPlistLabel = "com.fairpeer.desktop"

func autostartPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", autostartPlistLabel+".plist"), nil
}

func osExecutableClean() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Inside a .app bundle, point launchd at the bundle so activation and the
	// menu bar behave like a normal mac app launch.
	if idx := strings.Index(exe, ".app/Contents/MacOS/"); idx >= 0 {
		return exe[:idx+len(".app")], nil
	}
	return exe, nil
}

// plistEscape escapes the XML metacharacters for text content.
func plistEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func enableAutostart() error {
	target, err := osExecutableClean()
	if err != nil {
		return err
	}
	path, err := autostartPlistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	inBundle := strings.HasSuffix(target, ".app")
	var prog string
	if inBundle {
		prog = fmt.Sprintf("<string>open</string>\n\t\t<string>-a</string>\n\t\t<string>%s</string>", plistEscape(strings.TrimSuffix(target, ".app")))
	} else {
		prog = fmt.Sprintf("<string>%s</string>", plistEscape(target))
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + autostartPlistLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		` + prog + `
	</array>
	<key>RunAtLoad</key><true/>
</dict>
</plist>
`
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return err
	}
	// Make launchd notice without requiring a re-login; a failure here is
	// non-fatal — the agent loads at next login regardless.
	_, _ = exec.Command("launchctl", "unload", path).Output()
	_, _ = exec.Command("launchctl", "load", path).Output()
	return nil
}

func disableAutostart() error {
	path, err := autostartPlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return nil // already gone
	}
	_, _ = exec.Command("launchctl", "unload", path).Output()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func autostartRegistered() (bool, error) {
	path, err := autostartPlistPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
