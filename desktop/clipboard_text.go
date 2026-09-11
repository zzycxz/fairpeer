package main

// clipboard_text.go — read the system clipboard as text for frontend flows
// that cannot use navigator.clipboard.readText: WKWebView (macOS) does not
// implement it and WebKitGTK (Linux) gates it behind a permission prompt
// Wails never grants. Write-side (CopyButton) keeps the web API — that one
// works with a user gesture on every platform.

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/zzycxz/fairpeer/internal/proc"
)

// ReadClipboardText returns the system clipboard's text content.
func (a *App) ReadClipboardText() (string, error) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbpaste")
	case "windows":
		// Get-Clipboard appends a line break to its output; trimmed below.
		// ResolvePowerShell prefers the stock powershell and falls back to pwsh.
		cmd = exec.Command(proc.ResolvePowerShell(), "-NoProfile", "-Command", "Get-Clipboard")
	case "linux":
		// Wayland first, then the X11 tools.
		if _, err := exec.LookPath("wl-paste"); err == nil {
			cmd = exec.Command("wl-paste", "--no-newline")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--output")
		} else {
			return "", fmt.Errorf("no clipboard tool found (install wl-clipboard or xclip)")
		}
	default:
		return "", fmt.Errorf("clipboard read unsupported on %s", runtime.GOOS)
	}
	proc.HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read clipboard: %w", err)
	}
	if runtime.GOOS == "windows" {
		out = bytes.TrimSuffix(out, []byte("\r\n"))
	}
	return string(out), nil
}
