//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package netdev

// console_unix.go — the serial console line for unix desktops (macOS/Linux/
// BSD): termios raw line (8N1, no flow control) over /dev/cu.* and /dev/tty*
// USB-serial adapters, mirroring the Windows DCB implementation byte-for-byte
// at the consoleLineIO contract level. Reads return (0, nil) when the line
// idles past VTIME so the reader goroutine can poll the wire without
// blocking forever. Baud/ioctl glue lives in console_termios_*.go.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// openConsoleLine opens one serial port in the console configuration.
func openConsoleLine(name string, baud int) (consoleLineIO, error) {
	if baud <= 0 {
		baud = 9600
	}
	path := consoleDevicePath(name)
	// Non-blocking open first: a USB-serial adapter without asserted carrier
	// lines would block a plain open. We clear the flag once we own the fd so
	// VMIN/VTIME govern reads instead.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open console port %s (is the USB-serial adapter plugged in and free?): %w", path, err)
	}
	fail := func(err error) (consoleLineIO, error) {
		_ = unix.Close(fd)
		return nil, err
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		return fail(fmt.Errorf("console port %s: %w", path, err))
	}
	tio, err := unix.IoctlGetTermios(fd, termiosGetReq)
	if err != nil {
		return fail(fmt.Errorf("get termios %s: %w", path, err))
	}
	// Raw 8N1, every handshake flavor off — a console line is raw, same
	// configuration the Windows side programs into the DCB.
	tio.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	tio.Oflag &^= unix.OPOST
	tio.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	tio.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB | unix.CRTSCTS
	tio.Cflag |= unix.CS8 | unix.CLOCAL | unix.CREAD
	if err := applyConsoleBaud(tio, baud); err != nil {
		return fail(fmt.Errorf("console port %s: %w", path, err))
	}
	// VMIN=0 + VTIME=2: a read returns whatever arrived, or (0, nil) once the
	// line idles 200ms — the termios-granularity equivalent of the Windows
	// 20ms ReadIntervalTimeout.
	tio.Cc[unix.VMIN] = 0
	tio.Cc[unix.VTIME] = 2
	if err := unix.IoctlSetTermios(fd, termiosSetReq, tio); err != nil {
		return fail(fmt.Errorf("set termios %s (%d baud): %w", path, baud, err))
	}
	return &consoleLine{f: os.NewFile(uintptr(fd), path)}, nil
}

// consoleLine adapts the tty fd to io.ReadWriteCloser. A timed-out read
// surfaces as (0, nil) — the read loop's idle tick, not an error.
type consoleLine struct {
	f *os.File
}

func (c *consoleLine) Read(p []byte) (int, error) {
	for {
		n, err := c.f.Read(p)
		// See console_read.go: the kernel's bare idle tick AND Go's
		// eofError-rewritten (0, io.EOF) both mean "line idle" — the real
		// link-loss signature is a device errno, which propagates below.
		if out, idle := consoleIdleRead(n, err); idle {
			return out, nil
		}
		if n > 0 {
			return n, nil
		}
		if errno, ok := err.(unix.Errno); ok && (errno == unix.EINTR || errno == unix.EAGAIN || errno == unix.EWOULDBLOCK) {
			continue
		}
		return 0, err
	}
}

func (c *consoleLine) Write(p []byte) (int, error) { return c.f.Write(p) }

func (c *consoleLine) Close() error { return c.f.Close() }

// consoleDevicePath maps a configured console_port onto a device node. Both
// spellings the config validator accepts work: a full "/dev/ttyUSB0" or a
// bare node name ("ttyUSB0", "cu.usbserial-XXXX"); the bare Windows-style
// `\\.\COMn` form is normalized too so a config written on Windows at least
// yields a clean "device not found" instead of a mangled path.
func consoleDevicePath(name string) string {
	port := strings.TrimSpace(name)
	port = strings.TrimPrefix(port, `\\.\`)
	if strings.HasPrefix(port, "/dev/") {
		return port
	}
	return filepath.Join("/dev", port)
}

// listConsolePorts enumerates pluggable serial adapters: USB-serial and
// USB-modem nodes on both naming families (/dev/ttyUSB*|ttyACM* on Linux,
// /dev/tty.usb*|cu.usb* on macOS). Built-in /dev/ttyS* ports are excluded
// from the picker — on Linux those device nodes exist with or without
// hardware, so listing them is noise; configuring console_port = "/dev/ttyS0"
// directly still works.
func listConsolePorts() []string {
	patterns := []string{
		"/dev/ttyUSB*", "/dev/ttyACM*",
		"/dev/tty.usb*", "/dev/cu.usb*",
	}
	seen := map[string]bool{}
	var out []string
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			if strings.HasSuffix(m, ".debug") { // macOS debug mirrors of cu.*
				continue
			}
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	return out
}
