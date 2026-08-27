//go:build windows

package netdev

// dockerpipe_windows.go — the Windows named-pipe leg of the kind=docker
// client (Docker Desktop's local engine). npipe:////./pipe/docker_engine →
// \\.\pipe\docker_engine for go-winio's DialPipe.

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	winio "github.com/Microsoft/go-winio"
)

func newNpipeTransport(addr string) (*http.Transport, error) {
	// "//./pipe/docker_engine" → `\.\pipe\docker_engine` → `\\.\pipe\...`
	norm := strings.ReplaceAll(addr, "/", "\\")
	norm = strings.TrimPrefix(norm, "\\.")
	pipe := `\\.` + norm
	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := winio.DialPipe(pipe, nil)
			if err == nil {
				_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
			}
			return conn, err
		},
	}, nil
}
