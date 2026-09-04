//go:build windows

package netdev

// console_windows.go — the serial console line, natively over the Windows API
// (CreateFile on \\.\COMn + DCB + CommTimeouts; golang.org/x/sys only, no
// third-party dependency). This is the USB-to-serial adapter path for switch
// console ports: 8N1, no flow control, no host keys, no auth — physical
// presence IS the authorization.

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// DCB flag bits we care about (winbase.h): fBinary must be set on input
// (Windows doesn't support non-binary mode); all handshake bits off.
const (
	dcbBinary         = 0x00000001
	dcbOutxCtsFlow    = 0x00000004
	dcbOutxDsrFlow    = 0x00000008
	dcbDtrControlM    = 0x00000030 // mask
	dcbDsrSensitivity = 0x00000040
	dcbOutX           = 0x00000100
	dcbInX            = 0x00000200
)

// openConsoleLine opens one serial port in the console configuration. Reads
// complete after a 20ms line-idle gap (or when the buffer holds bytes), so a
// reader goroutine can poll the wire without blocking forever.
func openConsoleLine(name string, baud int) (consoleLineIO, error) {
	if baud <= 0 {
		baud = 9600
	}
	port := strings.TrimSpace(name)
	if !strings.HasPrefix(port, `\\.\`) {
		port = `\\.\` + port
	}
	path, err := windows.UTF16PtrFromString(port)
	if err != nil {
		return nil, fmt.Errorf("console port %q: %w", name, err)
	}
	h, err := windows.CreateFile(path,
		windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("open console port %q (is the USB-serial adapter plugged in and free?): %w", name, err)
	}
	fail := func(err error) (consoleLineIO, error) {
		windows.CloseHandle(h)
		return nil, err
	}
	var dcb windows.DCB
	dcb.DCBlength = uint32(unsafe.Sizeof(dcb))
	if err := windows.GetCommState(h, &dcb); err != nil {
		return fail(fmt.Errorf("get comm state %q: %w", name, err))
	}
	dcb.BaudRate = uint32(baud)
	dcb.ByteSize = 8
	dcb.Parity = 0 // NOPARITY
	dcb.StopBits = 0
	// Binary mode, every handshake flavor off: a console line is raw.
	dcb.Flags = dcbBinary
	dcb.Flags &^= dcbOutxCtsFlow | dcbOutxDsrFlow | dcbDsrSensitivity | dcbOutX | dcbInX
	dcb.Flags &^= dcbDtrControlM
	if err := windows.SetCommState(h, &dcb); err != nil {
		return fail(fmt.Errorf("set comm state %q (%d baud): %w", name, baud, err))
	}
	// Interval-only timeouts: return what arrived once the line idles 20ms.
	if err := windows.SetCommTimeouts(h, &windows.CommTimeouts{
		ReadIntervalTimeout:         20,
		ReadTotalTimeoutMultiplier:  0,
		ReadTotalTimeoutConstant:    0,
		WriteTotalTimeoutMultiplier: 10,
		WriteTotalTimeoutConstant:   500,
	}); err != nil {
		return fail(fmt.Errorf("set comm timeouts %q: %w", name, err))
	}
	_ = windows.PurgeComm(h, 0x0000000f) // purge rx/tx both, best effort
	return &consoleLine{f: os.NewFile(uintptr(h), port), h: h}, nil
}

// consoleLine adapts the comm handle to io.ReadWriteCloser: interval-timeout
// reads surface as (0, nil) — the read loop's idle tick, not an error.
type consoleLine struct {
	f *os.File
	h windows.Handle
}

func (c *consoleLine) Read(p []byte) (int, error) {
	for {
		n, err := c.f.Read(p)
		if n > 0 {
			return n, nil
		}
		if err == nil {
			return 0, nil
		}
		var eno windows.Errno
		if errors.As(err, &eno) && (eno == windows.ERROR_TIMEOUT || eno == windows.ERROR_SEM_TIMEOUT || eno == windows.ERROR_OPERATION_ABORTED) {
			return 0, nil // line idle — retry happens on the next Read call
		}
		return 0, err
	}
}

func (c *consoleLine) Write(p []byte) (int, error) { return c.f.Write(p) }

func (c *consoleLine) Close() error {
	err := c.f.Close()
	windows.CloseHandle(c.h) // no-op double close is fine; f.Close released it
	return err
}

// listConsolePorts enumerates present COM ports from the device map registry
// key — populated for USB-serial adapters the moment they plug in.
func listConsolePorts() []string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if v, _, err := k.GetStringValue(n); err == nil && v != "" {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
