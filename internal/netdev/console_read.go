package netdev

// console_read.go — the platform-neutral half of the serial-console Read
// classification, shared by console_unix.go (termios VTIME) and
// console_windows.go (ReadIntervalTimeout). Kept pure and untagged so one
// test covers every GOOS.

import "io"

// consoleIdleRead applies the idle rule to one raw read result: n==0 with
// either a nil error (the driver's bare idle timeout) or io.EOF (Go's
// poll.FD.eofError rewrites EVERY zero nil-error read into io.EOF, which
// would otherwise kill the io.Copy feeding the session buffer after the
// first quiet gap) is an idle tick, never line end — a serial console has no
// EOF; real link loss arrives as a device errno, which the callers propagate.
// idle=true means the caller returns (0, nil).
func consoleIdleRead(n int, err error) (int, bool) {
	if n == 0 && (err == nil || err == io.EOF) {
		return 0, true
	}
	return n, false
}
