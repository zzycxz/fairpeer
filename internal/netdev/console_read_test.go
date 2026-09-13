package netdev

import (
	"errors"
	"io"
	"syscall"
	"testing"
)

func TestConsoleIdleRead(t *testing.T) {
	cases := []struct {
		name string
		n    int
		err  error
		want int
		idle bool
	}{
		{"bare idle timeout (unix VTIME raw)", 0, nil, 0, true},
		{"eofError-rewritten idle (0, io.EOF)", 0, io.EOF, 0, true},
		{"data", 5, nil, 5, false},
		{"data with deferred error", 3, io.EOF, 3, false},
		{"device error propagates", 0, syscall.EBADF, 0, false},
		{"wrapped device error propagates", 0, errors.Join(syscall.ENODEV), 0, false},
	}
	for _, tc := range cases {
		n, idle := consoleIdleRead(tc.n, tc.err)
		if idle != tc.idle || (idle && n != tc.want) {
			t.Errorf("%s: consoleIdleRead(%d, %v) = (%d, %v), want idle=%v", tc.name, tc.n, tc.err, n, idle, tc.idle)
		}
	}
}
