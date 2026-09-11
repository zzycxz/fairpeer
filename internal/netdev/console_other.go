//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package netdev

// console_other.go — serial-console stub for unix platforms outside the
// supported desktop set (linux/darwin/BSD get the real termios line in
// console_unix.go; exotic ports keep this honest refusal).
import "fmt"

func openConsoleLine(name string, baud int) (consoleLineIO, error) {
	return nil, fmt.Errorf("console line %q: serial console is not supported on this platform", name)
}

func listConsolePorts() []string { return nil }
