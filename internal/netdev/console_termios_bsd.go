//go:build freebsd || netbsd || openbsd || dragonfly

package netdev

// console_termios_bsd.go — baud/ioctl glue for the BSDs: TIOCGETA/TIOCSETA
// request codes and the B-constant encoding in Ispeed/Ospeed. Assignments
// use untyped constants directly: the struct field type differs across BSDs
// (uint32 on freebsd/dragonfly, int32 on netbsd/openbsd). Rates above 230400
// are not offered here — the constant set varies per BSD.

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
	case 1200:
		t.Ispeed, t.Ospeed = unix.B1200, unix.B1200
	case 2400:
		t.Ispeed, t.Ospeed = unix.B2400, unix.B2400
	case 4800:
		t.Ispeed, t.Ospeed = unix.B4800, unix.B4800
	case 9600:
		t.Ispeed, t.Ospeed = unix.B9600, unix.B9600
	case 19200:
		t.Ispeed, t.Ospeed = unix.B19200, unix.B19200
	case 38400:
		t.Ispeed, t.Ospeed = unix.B38400, unix.B38400
	case 57600:
		t.Ispeed, t.Ospeed = unix.B57600, unix.B57600
	case 115200:
		t.Ispeed, t.Ospeed = unix.B115200, unix.B115200
	case 230400:
		t.Ispeed, t.Ospeed = unix.B230400, unix.B230400
	default:
		return fmt.Errorf("unsupported baud %d (supported: 1200..230400 standard rates)", baud)
	}
	return nil
}
