package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/proc"
)

// psUTF8Prologue forces PowerShell to emit UTF-8 instead of the host's OEM code
// page (e.g. CP936 on a Chinese Windows), so non-ASCII command output and error
// text come back as valid UTF-8 rather than mojibake.
const psUTF8Prologue = "$OutputEncoding=[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;"

// ShellKind is the interpreter a shell command runs under.
type ShellKind int

const (
	ShellBash ShellKind = iota
	ShellPowerShell
)

func (k ShellKind) String() string {
	if k == ShellPowerShell {
		return "powershell"
	}
	return "bash"
}

// Shell is the resolved interpreter the bash tool executes commands with: a kind
// (so callers can adapt prompts) and the executable to invoke. Err is non-nil
// only when resolveShell found no usable interpreter at all — argv then returns
// a refusal script (see argv) instead of a path that can never exec.
type Shell struct {
	Kind ShellKind
	Path string
	// Err names why no interpreter was resolved ("no POSIX shell found …").
	// Carried on the Shell so exec-time failures read as guidance rather than
	// a bare "executable file not found".
	Err error
}

// errNoPOSIXShell is the error carried by a Shell resolved on a system with
// neither bash nor any POSIX sh — practically impossible on a real unix, but
// it must fail with guidance instead of a phantom "bash" path.
var errNoPOSIXShell = errors.New("no POSIX shell found (no bash on PATH, /bin/sh unusable); install bash or a POSIX sh")

// ResolveShell picks the interpreter the shell tool runs commands under. It
// prefers a real bash so the model's POSIX habits work; on Windows, where bash
// is usually absent from PATH, it probes the Git-for-Windows install locations
// and only then falls back to PowerShell so the tool still functions. Off
// Windows the final fallback is a POSIX sh (PATH, then /bin/sh); if neither
// exists the Shell carries errNoPOSIXShell and refuses to run rather than
// exec'ing a path that can never exist. The result is cached for the process
// lifetime since the shell path does not change once the process is running.
func ResolveShell() Shell {
	resolveShellOnce.Do(func() {
		cachedShell = resolveShell(runtime.GOOS, exec.LookPath, fileExists, windowsBashCandidates(), probeBash, isWindowsWSLBash)
	})
	return cachedShell
}

var (
	resolveShellOnce sync.Once
	cachedShell      Shell
)

// resolveShell is ResolveShell with its environment lookups injected — including
// the Git-for-Windows bash candidates, which derive from %ProgramFiles% and so
// are empty off Windows — so the decision table is deterministically testable on
// any host.
func resolveShell(goos string, lookPath func(string) (string, error), exists func(string) bool, winBashCandidates []string, probe func(string) bool, isWSL func(string) bool) Shell {
	if p, err := lookPath("bash"); err == nil && !isWSL(p) && probe(p) {
		return Shell{Kind: ShellBash, Path: p}
	}
	if goos == "windows" {
		for _, p := range winBashCandidates {
			if exists(p) && probe(p) {
				return Shell{Kind: ShellBash, Path: p}
			}
		}
		for _, name := range []string{"pwsh", "powershell"} {
			if p, err := lookPath(name); err == nil {
				return Shell{Kind: ShellPowerShell, Path: p}
			}
		}
		// Windows keeps its historical last resort: some bash.exe may exist
		// where LookPath can't see it (the bash tool resolves Kind when Path
		// is empty).
		return Shell{Kind: ShellBash, Path: "bash"}
	}
	// Unix final fallback: any POSIX sh before giving up — bash is preferred
	// above, but a bare sh still runs the model's commands. Try PATH first,
	// then the conventional /bin/sh location.
	if p, err := lookPath("sh"); err == nil && probe(p) {
		return Shell{Kind: ShellBash, Path: p}
	}
	if probe("/bin/sh") {
		return Shell{Kind: ShellBash, Path: "/bin/sh"}
	}
	return Shell{Kind: ShellBash, Err: errNoPOSIXShell}
}

// isWindowsWSLBash reports whether a resolved bash path is the WSL launcher
// Windows ships under %SystemRoot% (e.g. C:\Windows\System32\bash.exe). With WSL
// installed it runs commands inside the Linux VM — where the Windows workspace is
// a /mnt/<drive> path — so it must never be chosen for a native Windows workspace;
// the only bash.exe Microsoft places under the Windows dir is that launcher.
func isWindowsWSLBash(path string) bool {
	if runtime.GOOS != "windows" || path == "" {
		return false
	}
	win := os.Getenv("SystemRoot")
	if win == "" {
		win = os.Getenv("windir")
	}
	if win == "" {
		return false
	}
	p := strings.ToLower(filepath.Clean(path))
	root := strings.ToLower(filepath.Clean(win)) + string(filepath.Separator)
	return strings.HasPrefix(p, root)
}

// Windows ships a bash.exe launcher stub in %SystemRoot% that opens the WSL
// install prompt instead of running anything, so confirm bash actually works
// before trusting it. The probe applies on unix too: a PATH entry can be a
// stale shim or a non-executable remnant, and `bash -c true` is a cheap,
// definitive check. Timeout-bounded in case the target blocks.
func probeBash(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-c", "true")
	proc.HideWindow(cmd)
	return cmd.Run() == nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// windowsBashCandidates lists the bash.exe paths a Git-for-Windows install
// ships, across the usual program-files roots and a per-user install.
func windowsBashCandidates() []string {
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if v := os.Getenv(env); v != "" {
			roots = append(roots, v)
		}
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		roots = append(roots, filepath.Join(v, "Programs"))
	}
	var out []string
	for _, r := range roots {
		out = append(out,
			filepath.Join(r, "Git", "bin", "bash.exe"),
			filepath.Join(r, "Git", "usr", "bin", "bash.exe"),
		)
	}
	return out
}

// argv builds the exec argv that runs command under this shell.
func (s Shell) argv(command string) []string {
	if s.Err != nil {
		// No interpreter was resolved. Refuse via a script that prints the real
		// problem and exits non-zero (same pattern as sandbox.refuseArgv) rather
		// than exec'ing a path that can never exist — a silent "file not found"
		// would leave the model guessing. If even this interpreter is absent the
		// exec itself errors, so the failure stays loud either way.
		return []string{"/bin/sh", "-c",
			"echo 'fairpeer: no POSIX shell found (no bash on PATH, /bin/sh unusable); install bash or a POSIX sh' >&2; exit 127"}
	}
	path := s.Path
	if path == "" {
		path = s.Kind.String()
	}
	if s.Kind == ShellPowerShell {
		return []string{path, "-NoProfile", "-NonInteractive", "-Command", psUTF8Prologue + command}
	}
	return []string{path, "-c", command}
}

// SupportsChaining reports whether the shell parses '&&' / '||'. bash does;
// Windows PowerShell 5.1 (powershell.exe) does not — only PowerShell 7+ (pwsh).
func (s Shell) SupportsChaining() bool {
	if s.Kind != ShellPowerShell {
		return true
	}
	base := strings.ToLower(s.Path)
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:] // Windows path; split on either separator off-Windows too
	}
	return base == "pwsh" || base == "pwsh.exe"
}
