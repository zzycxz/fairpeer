//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package netdev

import "testing"

// consoleDevicePath maps configured console_port values onto unix device
// nodes; a Windows-style config must degrade to a clean "not found" instead
// of a mangled path. Unix-tagged (the mapping only exists there).
func TestConsoleDevicePath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/dev/ttyUSB0", "/dev/ttyUSB0"},
		{"ttyUSB0", "/dev/ttyUSB0"},
		{"cu.usbserial-XXXX", "/dev/cu.usbserial-XXXX"},
		{`\\.\COM3`, "/dev/COM3"},
		{"COM3", "/dev/COM3"},
	}
	for _, c := range cases {
		if got := consoleDevicePath(c.in); got != c.want {
			t.Fatalf("consoleDevicePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
