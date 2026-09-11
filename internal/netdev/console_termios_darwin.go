//go:build darwin

package netdev

// console_termios_darwin.go — baud/ioctl glue for macOS: TIOCGETA/TIOCSETA
// request codes; the speed lives in the Ispeed/Ospeed fields and macOS uses
// the numeric rate as the value (B115200 == 115200, …).

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	termiosGetReq = unix.TIOCGETA
	termiosSetReq = unix.TIOCSETA
)

func applyConsoleBaud(t *unix.Termios, baud int) error {
	switch baud {
	case 1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200, 230400:
		t.Ispeed, t.Ospeed = uint64(baud), uint64(baud)
		return nil
	}
	return fmt.Errorf("unsupported baud %d (supported: 1200..230400 standard rates)", baud)
}
