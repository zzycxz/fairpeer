package main

import (
	"os/exec"
	"runtime"
)

// openInFileExplorer opens the platform's native file manager at dir. Used by
// the cowork settings panel ("Open PPT template dir" button). Errors are
// returned to the caller so the UI can surface them.
func openInFileExplorer(dir string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", dir).Start()
	case "darwin":
		return exec.Command("open", dir).Start()
	default:
		// Linux & other unix: same freedesktop opener chain (xdg-open → gio →
		// kde-open) the workspace opener uses, with a real error when none of
		// them is installed — instead of a bare xdg-open exec that fails
		// silently on minimal desktops.
		return openWorkspacePath(dir)
	}
}
