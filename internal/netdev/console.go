package netdev

// console.go — the serial-console session (COM 口/串口控制台): drives the
// SAME vendor CLI engine as SSH over a physical console line. There is no
// host-key or auth layer — console access is physical presence — but every
// command still goes through the read classifier, redaction, audit and the
// live tap, exactly like an SSH session: the console is a transport, not a
// policy bypass.

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

// consoleLineIO is one configured serial line (platform implementations in
// console_windows.go / console_other.go).
type consoleLineIO interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

// OpenConsoleSession opens the line, runs the driver's paging-off sequence,
// and waits for the first driver prompt — then hands back a Session whose
// stdin IS the line and whose output buffer is fed by a reader goroutine.
// Session.Run (echo strip, prompt match, pager advance, clean) works
// unchanged; Close is nil-ssh-safe.
func OpenConsoleSession(ctx context.Context, portName string, baud int, drv driver.Driver, encoding string) (*Session, error) {
	line, err := openConsoleLine(portName, baud)
	if err != nil {
		return nil, err
	}
	out := &syncBuffer{}
	go func() { _, _ = io.Copy(out, line) }() // ends when the line closes
	s := &Session{drv: drv, stdin: line, out: out, encode: decoderFor(encoding)}
	// Wake the console and wait for its prompt (same tolerance as OpenSession:
	// a silent line times out into a usable session; the first Run tolerates
	// the leftovers).
	_, _ = line.Write([]byte("\r\n"))
	waitForConsolePrompt(s, out)
	for _, cmd := range drv.PagingOff() {
		if _, err := s.Run(ctx, cmd); err != nil {
			s.Close()
			return nil, fmt.Errorf("console paging-off %q: %w", cmd, err)
		}
	}
	out.reset()
	return s, nil
}

// waitForConsolePrompt blocks until the driver's prompt shows up on the line
// or the open timeout passes (then returns — the first Run tolerates the
// leftover bytes, same contract as OpenSession).
func waitForConsolePrompt(s *Session, out *syncBuffer) {
	deadline := time.Now().Add(sessionOpenTimeout)
	for {
		if s.drv.Prompt().MatchString(ansi.Strip(s.encode(out.snapshot()))) {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ListConsolePorts enumerates the machine's present serial ports (platform
// implementations in console_windows.go / console_other.go).
func ListConsolePorts() []string { return listConsolePorts() }
