//go:build linux

package doctor

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// collectDesktopEnv gathers the domestic-OS (统信UOS/银河麒麟) desktop
// environment facts that decide whether the Wails GUI, Chinese IME, clipboard
// paste, and netdev tooling can work on this host — the checks from
// docs/国产化适配实施方案.md §10.5 R5-4. Linux-only: other platforms render
// an empty section instead.
func collectDesktopEnv() DesktopEnvReport {
	r := DesktopEnvReport{
		Locale:       os.Getenv("LANG"),
		SSHAgentSock: os.Getenv("SSH_AUTH_SOCK") != "",
	}
	// Display server: explicit session type first, then env-var fallbacks.
	// "none" means no graphical session — the Wails GUI cannot start here
	// (headless server install; `fairpeer serve` is the right shape there).
	switch {
	case strings.Contains(os.Getenv("XDG_SESSION_TYPE"), "wayland"):
		r.DisplayServer = "wayland"
	case strings.Contains(os.Getenv("XDG_SESSION_TYPE"), "x11"):
		r.DisplayServer = "x11"
	case os.Getenv("WAYLAND_DISPLAY") != "":
		r.DisplayServer = "wayland"
	case os.Getenv("DISPLAY") != "":
		r.DisplayServer = "x11"
	default:
		r.DisplayServer = "none"
	}
	// IME: fcitx/ibus detection from the GTK/Qt module vars. Empty means the
	// launcher env is not set — Chinese input in the GUI will misbehave.
	for _, v := range []string{"GTK_IM_MODULE", "QT_IM_MODULE", "XMODIFIERS"} {
		s := strings.ToLower(os.Getenv(v))
		switch {
		case strings.Contains(s, "fcitx"):
			r.IME = "fcitx"
		case strings.Contains(s, "ibus"):
			r.IME = "ibus"
		}
		if r.IME != "" {
			break
		}
	}
	// WebKitGTK: the GUI's engine, via pkg-config like the wails build itself.
	for _, mod := range []string{"webkit2gtk-4.0", "webkit2gtk-4.1"} {
		if out, err := exec.Command("pkg-config", "--modversion", mod).Output(); err == nil {
			r.WebKitGTK = mod + " " + strings.TrimSpace(string(out))
			break
		}
	}
	// Helper binaries the Go side shells out to (graceful degradation when
	// absent, but the operator should see it in one place).
	for _, probe := range []struct {
		name string
		bin  string
	}{
		{"notify-send", "notify-send"},
		{"clipboard-image", "wl-paste"},
		{"clipboard-image", "xclip"},
		{"screenshot", "scrot"},
		{"window-input", "xdotool"},
	} {
		if _, err := exec.LookPath(probe.bin); err == nil {
			r.Helpers = appendUnique(r.Helpers, probe.name)
		}
	}
	r.OpenSSH = lookPathOr("ssh", "")
	// CAP_NET_RAW: parse CapEff from /proc/self/status (bit 13) rather than
	// opening a raw socket — a doctor probe must not create network state.
	if capEff, ok := capEffective(); ok {
		r.CAPNetRaw = capEff&(1<<13) != 0
	}
	if rl, ok := rlimitNoFile(); ok {
		r.NoFileSoft = int(rl.Cur)
	}
	r.TmpNoexec = mountNoexec("/tmp")
	// Kernel page size: Kylin V10 server arm64 defaults to 64K pages (vs the
	// 4K desktop norm) — worth surfacing because old WebKitGTK builds abort
	// on 64K kernels and modern ones run with the JS JIT disabled.
	r.PageSize = syscall.Getpagesize()
	return r
}

func lookPathOr(bin, fallback string) string {
	if p, err := exec.LookPath(bin); err == nil {
		return p
	}
	return fallback
}

func appendUnique(list []string, v string) []string {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}

// capEffective parses the CapEff hex line from /proc/self/status.
func capEffective() (uint64, bool) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

func rlimitNoFile() (syscall.Rlimit, bool) {
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err != nil {
		return rl, false
	}
	return rl, true
}

// mountNoexec reports whether the given mountpoint carries the noexec flag
// (a hardened /tmp breaks extract-and-run helpers).
func mountNoexec(mountpoint string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != mountpoint {
			continue
		}
		return strings.Contains(fields[3], "noexec")
	}
	return false
}
