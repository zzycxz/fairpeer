//go:build !windows

package netdev

// console_other.go — serial-console stub for non-Windows builds. The v1
// console line targets the Windows desktop (COM ports via USB-serial
// adapters); a termios implementation can slot in behind the same
// openConsoleLine signature later.
import "fmt"

func openConsoleLine(name string, baud int) (consoleLineIO, error) {
	return nil, fmt.Errorf("console line %q: serial console is Windows-only in this build", name)
}

func listConsolePorts() []string { return nil }
