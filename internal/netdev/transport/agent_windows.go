//go:build windows

package transport

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	winio "github.com/Microsoft/go-winio"
)

// windowsOpenSSHAgentPipe is the named pipe the Windows OpenSSH agent (the
// "OpenSSH Authentication Agent" service) listens on.
const windowsOpenSSHAgentPipe = `\\.\pipe\openssh-ssh-agent`

// dialAgent connects to the ssh-agent on Windows. The OpenSSH agent listens
// on the named pipe \\.\pipe\openssh-ssh-agent; SSH_AUTH_SOCK may name that
// pipe (in any of the \\.\pipe\, //./pipe/ or npipe:// spellings). An empty
// sock falls back to the stock agent pipe.
func dialAgent(sock string) (net.Conn, error) {
	pipe := normalizeAgentPipe(sock)
	conn, err := winio.DialPipe(pipe, nil)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("netdev: ssh-agent pipe not found — is the OpenSSH Authentication Agent service running? (dial %s)", pipe)
		}
		return nil, fmt.Errorf("netdev: ssh-agent dial %s: %w", pipe, err)
	}
	return conn, nil
}

// normalizeAgentPipe maps the SSH_AUTH_SOCK spellings onto the \\.\pipe\ form
// go-winio's DialPipe expects, defaulting to the OpenSSH agent pipe when the
// caller passes nothing.
func normalizeAgentPipe(sock string) string {
	sock = strings.TrimSpace(sock)
	sock = strings.TrimPrefix(sock, "npipe://")
	sock = strings.ReplaceAll(sock, "/", `\`)
	if sock == "" {
		return windowsOpenSSHAgentPipe
	}
	return sock
}
