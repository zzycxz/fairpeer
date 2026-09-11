//go:build !darwin && !windows

package main

// open_workspace_unix.go — open a path with the desktop environment's default
// handler on Linux and other non-macOS Unix.

import (
	"fmt"
	"os/exec"
	"strings"
)

// xdgOpener is one candidate freedesktop.org opener. args is prepended before
// the path (gio needs the "open" subcommand; the kde/xdg helpers don't).
type xdgOpener struct {
	name string
	args []string
}

// xdgOpeners lists the openers to try, in order. xdg-open is the standard but
// isn't universal: minimal desktops sometimes ship only gio (GNOME/GLib) or
// kde-open (KDE), and broken xdg-utils installs are common enough to be worth
// falling through on rather than failing outright.
var xdgOpeners = []xdgOpener{
	{name: "xdg-open"},
	{name: "gio", args: []string{"open"}},
	{name: "kde-open5"},
	{name: "kde-open"},
}

// openWithXDGTools opens path with the first opener found on PATH. When none
// exists it returns an error listing everything that was tried, so the user (or
// the log) knows why nothing happened and what to install.
func openWithXDGTools(path string) error {
	var missing []string
	for _, opener := range xdgOpeners {
		if _, err := exec.LookPath(opener.name); err != nil {
			missing = append(missing, opener.name)
			continue
		}
		return exec.Command(opener.name, append(append([]string{}, opener.args...), path)...).Start()
	}
	return fmt.Errorf("no file opener found on PATH (tried %s); install xdg-utils (provides xdg-open) and retry", strings.Join(missing, ", "))
}

func openWorkspacePath(path string) error {
	return openWithXDGTools(path)
}
