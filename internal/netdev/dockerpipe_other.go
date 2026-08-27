//go:build !windows

package netdev

// dockerpipe_other.go — npipe is a Windows-only transport; everywhere else
// docker endpoints ride unix:// or tcp:// sockets.

import (
	"fmt"
	"net/http"
)

func newNpipeTransport(addr string) (*http.Transport, error) {
	return nil, fmt.Errorf("npipe:// sockets are Windows-only (use unix:// or tcp://): %q", addr)
}
