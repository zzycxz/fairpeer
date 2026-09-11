package main

// panic_log.go — last-resort panic evidence for packaged GUI launches:
// stdout/stderr are detached there, so without this a panic vanishes without
// a trace (there is no OS console to catch it on macOS/Linux .app/.desktop
// launches). Appends (does not truncate) so consecutive crashes accumulate.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

func logPanic(r any) {
	cfgDir := desktopConfigDir()
	if cfgDir == "" {
		return
	}
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(cfgDir, "app.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n=== PANIC %s ===\n%v\n%s\n", time.Now().Format(time.RFC3339), r, debug.Stack())
}
