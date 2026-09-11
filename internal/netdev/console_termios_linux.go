//go:build linux

package netdev

// console_termios_linux.go — baud/ioctl glue for Linux: termios request
// codes and the CBAUD-encoded Cflag baud field.

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	termiosGetReq = unix.TCGETS
	termiosSetReq = unix.TCSETS
)

func applyConsoleBaud(t *unix.Termios, baud int) error {
	c, ok := baudConst(baud)
	if !ok {
		return fmt.Errorf("unsupported baud %d (supported: 1200..921600 standard rates)", baud)
	}
	t.Cflag = (t.Cflag &^ unix.CBAUD) | c
	t.Ispeed, t.Ospeed = c, c
	return nil
}

func baudConst(baud int) (uint32, bool) {
	switch baud {
	case 1200:
		return unix.B1200, true
	case 2400:
		return unix.B2400, true
	case 4800:
		return unix.B4800, true
	case 9600:
		return unix.B9600, true
	case 19200:
		return unix.B19200, true
	case 38400:
		return unix.B38400, true
	case 57600:
		return unix.B57600, true
	case 115200:
		return unix.B115200, true
	case 230400:
		return unix.B230400, true
	case 460800:
		return unix.B460800, true
	case 921600:
		return unix.B921600, true
	}
	return 0, false
}
