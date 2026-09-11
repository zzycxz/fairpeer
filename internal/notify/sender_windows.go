//go:build windows

package notify

import (
	"encoding/base64"
	"os/exec"
	"strings"
	"unicode/utf16"

	"github.com/zzycxz/fairpeer/internal/proc"
)

// PlatformSender delivers notifications through the host OS.
type PlatformSender struct{}

// NewPlatformSender returns the best-effort sender for the current platform.
func NewPlatformSender() PlatformSender { return PlatformSender{} }

func (PlatformSender) Send(m Message) error {
	// NEW-39: `-Command <script> Title Body` never bound $args (PowerShell
	// concatenates trailing args into the SCRIPT text — toast showed no body
	// and dynamic text executed as code). Interpolate single-quoted PS string
	// literals ('' escapes a quote) and pass via -EncodedCommand, which takes
	// exactly one script and no trailing arguments.
	script := "$t = " + psQuote(m.Title) + "; $b = " + psQuote(m.Body) + "; " + `
if (Get-Command New-BurntToastNotification -ErrorAction SilentlyContinue) {
  New-BurntToastNotification -Text $t, $b
} elseif (Get-Command msg -ErrorAction SilentlyContinue) {
  msg $env:USERNAME ($t + ': ' + $b)
}`
	encoded := base64.StdEncoding.EncodeToString(utf16LE([]byte(script)))
	cmd := exec.Command(proc.ResolvePowerShell(), "-NoProfile", "-EncodedCommand", encoded)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// psQuote renders s as a single-quoted PowerShell string literal (” escapes').
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// utf16LE encodes s as little-endian UTF-16 bytes — the byte order
// -EncodedCommand expects (unicode/utf16 handles surrogate pairs).
func utf16LE(s []byte) []byte {
	runes := []rune(string(s))
	units := utf16.Encode(runes)
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}
