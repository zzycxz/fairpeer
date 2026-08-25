//go:build !windows

package main

// remote_wsl_other.go — WSL only exists on Windows; other platforms have no P1
// transport (Docker/SSH/Server follow the same remoteTransport interface).

import (
	"context"
	"fmt"
	"io"
)

type wslTransport struct{}

func newWSLTransport() remoteTransport { return wslTransport{} }

// ListWSLDistros is a stub off Windows.
func (a *App) ListWSLDistros() []wslDistro { return nil }

func (t wslTransport) Dial(ctx context.Context, ref RemoteRef) (io.Reader, io.Writer, remoteProcess, error) {
	return nil, nil, nil, fmt.Errorf("wsl transport is Windows-only")
}

// wslHomeForProbe is a stub off Windows.
func wslHomeForProbe(distro, user string) (string, error) {
	return "", fmt.Errorf("wsl transport is Windows-only")
}
