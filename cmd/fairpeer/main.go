// Command fairpeer is a config- and plugin-driven coding agent CLI.
package main

import (
	"os"

	"github.com/zzycxz/fairpeer/internal/cli"

	// Embed the IANA timezone database so ICS calendar TZID resolution works on
	// Windows and minimal Linux (no system zoneinfo / GOROOT available).
	// ~450KB to the binary; the standard fix.
	_ "time/tzdata"

	// Blank imports wire compile-time built-ins into their registries.
	_ "github.com/zzycxz/fairpeer/internal/provider/anthropic"
	_ "github.com/zzycxz/fairpeer/internal/provider/openai"
	_ "github.com/zzycxz/fairpeer/internal/tool/builtin"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version))
}
