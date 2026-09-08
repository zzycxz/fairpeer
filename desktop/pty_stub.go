//go:build !windows

package main

import "errors"

// PTY bindings degrade on non-Windows builds: ConPTY is the only PTY backend
// wired so far (upgrade spec 3-4). The methods exist so the Wails binding
// surface stays identical across platforms; the frontend TerminalPanel falls
// back to one-shot RunShell mode when creation fails.
var errPTYUnsupported = errors.New("PTY is only available on Windows (ConPTY); use one-shot shell mode instead")

func (a *App) PTYCreate(cols, rows int) (int, error) { return 0, errPTYUnsupported }

func (a *App) PTYCreateForTab(tabID string, cols, rows int) (int, error) {
	return 0, errPTYUnsupported
}

func (a *App) PTYWrite(id int, input string) error { return errPTYUnsupported }

func (a *App) PTYRead(id int) (string, bool, error) { return "", false, errPTYUnsupported }

func (a *App) PTYResize(id, cols, rows int) error { return errPTYUnsupported }

func (a *App) PTYKill(id int) {}

func (a *App) PTYAlive(id int) bool { return false }
