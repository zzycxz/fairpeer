package runtime

// runtime.go provides unified runtime environment resolution for FairPeer.
// It replaces the 6 scattered findPython()/pythonExe() helpers with a single
// source of truth that knows about uv (bundled or PATH), Python (direct or via
// uv), and Node.js.
//
// Resolution priority (borrowed from internal/codegraph's Resolve pattern):
//
//   uv:    config override → PATH lookup → exe-adjacent bundle → (download)
//   python: uv (`uv run python`) → python3/python/py on PATH
//   node:  node/npx on PATH (no bundle; Node is too large to bundle)
//
// The package is safe for concurrent use after init. All functions are
// idempotent and cache their first result.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/proc"
)

// BundleDirName is the subdirectory next to the fairpeer executable where
// bundled runtimes live (e.g. fairpeer.exe/runtimes/uv.exe).
const BundleDirName = "runtimes"

// uvNames returns the candidate filenames for uv on each platform.
func uvNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"uv.exe", "uv.cmd", "uv.bat"}
	}
	return []string{"uv"}
}

// pythonNames returns the candidate command names for Python.
func pythonNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"python", "py", "python3"}
	}
	return []string{"python3", "python"}
}

// nodeNames returns the candidate command names for Node.js.
func nodeNames() []string {
	return []string{"node"}
}

func npxNames() []string {
	if runtime.GOOS == "windows" {
		return []string{"npx", "npx.cmd"}
	}
	return []string{"npx"}
}

// --- cached results ---------------------------------------------------------

var (
	// resolveMu serializes the resolution caches below against
	// resetRuntimeResolution: a sync.Once can't be reset, so Install swaps the
	// Once values after a successful download, and the mutex keeps a
	// concurrent ResolveUV/ResolvePython from racing that swap. The Do
	// closures run under it too, which is why they call resolveUVOnce /
	// resolvePythonOnce directly instead of the locking ResolveUV /
	// ResolvePython (the same mutex is not reentrant).
	resolveMu sync.Mutex

	uvOnce  sync.Once
	uvPath  string
	uvFound bool

	pyOnce   sync.Once
	pyCmd    string
	pyPrefix []string
	pyErr    error

	nodeOnce  sync.Once
	nodePath  string
	npxPath   string
	nodeFound bool
)

// ResolveUV finds uv on the system. Checks PATH first, then the exe-adjacent
// bundle directory, then (Windows) the well-known per-user install dirs GUI
// processes don't see on PATH. Returns the absolute path and true if found.
func ResolveUV() (string, bool) {
	resolveMu.Lock()
	defer resolveMu.Unlock()
	uvOnce.Do(resolveUVOnce)
	return uvPath, uvFound
}

// resolveUVOnce is ResolveUV's body, run at most once per resolution
// generation. Callers must hold resolveMu.
func resolveUVOnce() {
	// 1. PATH lookup
	if p, err := exec.LookPath(uvNames()[0]); err == nil {
		uvPath, uvFound = p, true
		return
	}
	// 2. Cache (auto-downloaded by Install)
	if p, ok := cachedUV(); ok {
		uvPath, uvFound = p, true
		return
	}
	// 3. Bundle (exe-adjacent)
	if p, ok := bundledUV(); ok {
		uvPath, uvFound = p, true
		return
	}
	// 4. Windows: common install dirs. The standalone installer and
	// `winget install astral-sh.uv` drop uv.exe into per-user locations that
	// reach the user PATH only in freshly-spawned shells — a GUI process
	// inheriting the pre-install environment finds nothing via LookPath.
	if runtime.GOOS == "windows" {
		for _, dir := range windowsUVDirs() {
			if p, ok := lookPathInDir(dir, "uv"); ok {
				uvPath, uvFound = p, true
				return
			}
		}
	}
	uvPath, uvFound = "", false
}

// windowsUVDirs lists the per-user uv install locations on Windows:
// %LOCALAPPDATA%\Programs\uv (standalone installer / winget) and
// %USERPROFILE%\.local\uv's documented default bin dir. LOCALAPPDATA missing
// (stripped environment) falls back to its usual home-relative location.
func windowsUVDirs() []string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		if home, err := os.UserHomeDir(); err == nil {
			local = filepath.Join(home, "AppData", "Local")
		}
	}
	var dirs []string
	if local != "" {
		dirs = append(dirs, filepath.Join(local, "Programs", "uv"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return dirs
}

// pyProbeScript is the runnability + version probe every Python candidate must
// pass before ResolvePython adopts it: exit 0 means the interpreter actually
// executes AND reports >= 3.10.
const pyProbeScript = `import sys; raise SystemExit(0 if sys.version_info >= (3, 10) else 1)`

// pyProbeTimeout bounds each candidate probe; a hung interpreter (or a stub
// waiting on input) must not stall startup.
const pyProbeTimeout = 3 * time.Second

// ResolvePython finds Python. Priority: uv (`uv run python`) → python3/python/py
// on PATH. When uv is used, returns ("uv", ["run", "python"]) so the caller
// can prepend the prefix args. When a direct Python is found, returns
// ("/usr/bin/python3", nil).
//
// Every candidate — uv-managed and direct alike — is probed with
// pyProbeScript before adoption, because LookPath alone lies twice over: the
// stock macOS /usr/bin/python3 is an Xcode CLT stub that resolves on PATH but
// fails at exec with xcode-select noise, and a pre-3.10 interpreter passes
// LookPath despite the 3.10 floor the error text claims. The returned error
// names each candidate and why it was rejected.
func ResolvePython() (cmd string, prefixArgs []string, err error) {
	resolveMu.Lock()
	defer resolveMu.Unlock()
	pyOnce.Do(resolvePythonOnce)
	return pyCmd, pyPrefix, pyErr
}

// resolvePythonOnce is ResolvePython's body, run at most once per resolution
// generation. Callers must hold resolveMu.
func resolvePythonOnce() {
	var failures []string

	// 1. If uv is available, prefer it (handles deps + venv isolation) — but
	// only if its Python actually runs: a uv without a usable interpreter
	// (not installed yet, offline) must fall through to direct Pythons.
	uvOnce.Do(resolveUVOnce)
	if uvFound {
		if err := probePython(uvPath, []string{"run", "python"}); err == nil {
			pyCmd, pyPrefix, pyErr = uvPath, []string{"run", "python"}, nil
			return
		} else {
			failures = append(failures, fmt.Sprintf("uv (%s run python): %v", uvPath, err))
		}
	}

	// 2. Direct Python on PATH.
	for _, name := range pythonNames() {
		p, err := exec.LookPath(name)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: not on PATH", name))
			continue
		}
		if err := probePython(p, nil); err == nil {
			pyCmd, pyPrefix, pyErr = p, nil, nil
			return
		}
		failures = append(failures, fmt.Sprintf("%s (%s): %v", name, p, err))
	}

	if len(failures) == 0 {
		// Nothing to probe (no uv, no candidate names) — the plain not-found error.
		pyCmd, pyPrefix, pyErr = "", nil, errPythonNotFound
		return
	}
	pyCmd, pyPrefix, pyErr = "", nil, &pythonNotFoundError{detail: strings.Join(failures, "\n  - ")}
}

// probePython runs `<cmd> <prefix…> -c pyProbeScript` and returns nil only
// when the interpreter executes and reports >= 3.10. The failure is annotated
// with the interpreter's own output so the caller sees actionable noise (the
// darwin Xcode CLT stub prints xcode-select text; an old Python exits 1
// silently, which reads as "reports Python < 3.10").
func probePython(cmd string, prefix []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pyProbeTimeout)
	defer cancel()
	args := append(append([]string{}, prefix...), "-c", pyProbeScript)
	c := exec.CommandContext(ctx, cmd, args...)
	proc.HideWindow(c) // no console flash when the caller is a GUI process on Windows
	out, err := c.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("probe timed out after %s", pyProbeTimeout)
	}
	text := strings.TrimSpace(string(out))
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i]) // one line per candidate in the aggregate error
	}
	if runtime.GOOS == "darwin" && strings.Contains(text, "xcode-select") {
		// /usr/bin/python3 is the Xcode CLT stub: LookPath finds it, exec
		// refuses until the tools are installed. Name the fix.
		return fmt.Errorf("macOS stub, no real interpreter — run `xcode-select --install` (%v)", err)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 && text == "" {
		// pyProbeScript's silent SystemExit(1): runs, but too old.
		return errors.New("reports Python < 3.10")
	}
	if text == "" {
		return err
	}
	return fmt.Errorf("%s (%v)", text, err)
}

// resetRuntimeResolution clears the cached uv/python resolution so the current
// process re-resolves after a successful uv Install instead of waiting for the
// next launch (ResolvePython's cached "uv not found" would otherwise pin the
// process to direct Pythons forever). Both Onces swap to fresh zero values
// under resolveMu, so the next resolution re-runs from scratch. Callers must
// NOT hold resolveMu (Install doesn't).
func resetRuntimeResolution() {
	resolveMu.Lock()
	defer resolveMu.Unlock()
	uvOnce = sync.Once{}
	uvPath, uvFound = "", false
	pyOnce = sync.Once{}
	pyCmd, pyPrefix, pyErr = "", nil, nil
}

// ResolveNode finds Node.js and npx on PATH. Returns absolute paths.
// Node is NOT bundled (too large); the caller should prompt the user to install
// if not found.
func ResolveNode() (nodePath, npxPath string, ok bool) {
	nodeOnce.Do(func() {
		for _, name := range nodeNames() {
			if p, err := exec.LookPath(name); err == nil {
				nodePath = p
				break
			}
		}
		for _, name := range npxNames() {
			if p, err := exec.LookPath(name); err == nil {
				npxPath = p
				break
			}
		}
		nodeFound = nodePath != "" || npxPath != ""
	})
	return nodePath, npxPath, nodeFound
}

// --- status for doctor/UI ---------------------------------------------------

// RuntimeStatus is a snapshot of all runtime environments, for the doctor
// command and the settings UI.
type RuntimeStatus struct {
	UV     RuntimeEntry `json:"uv"`
	Python RuntimeEntry `json:"python"`
	Node   RuntimeEntry `json:"node"`
	NPX    RuntimeEntry `json:"npx"`
}

// RuntimeEntry describes one runtime's availability.
type RuntimeEntry struct {
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Source    string `json:"source,omitempty"` // "path" | "bundle" | "uv" | ""
}

// DetectAll checks all runtimes and returns a status snapshot. Safe to call
// repeatedly (each sub-resolver caches its first result).
func DetectAll() RuntimeStatus {
	st := RuntimeStatus{}

	if p, ok := ResolveUV(); ok {
		st.UV = RuntimeEntry{Available: true, Path: p, Source: uvSource(p)}
	}

	if cmd, prefix, err := ResolvePython(); err == nil {
		pyPath := cmd
		if len(prefix) > 0 {
			pyPath = cmd + " " + filepath.Join(prefix...) // "uv run python"
		}
		source := "path"
		if len(prefix) > 0 {
			source = "uv"
		}
		st.Python = RuntimeEntry{Available: true, Path: pyPath, Source: source}
	}

	if np, nx, ok := ResolveNode(); ok {
		if np != "" {
			st.Node = RuntimeEntry{Available: true, Path: np, Source: "path"}
		}
		if nx != "" {
			st.NPX = RuntimeEntry{Available: true, Path: nx, Source: "path"}
		}
	}

	return st
}

// uvSource determines where uv was found ("path" or "bundle").
func uvSource(p string) string {
	if base, ok := bundledBaseDir(); ok && filepath.Dir(p) == base {
		return "bundle"
	}
	return "path"
}

// --- errors -----------------------------------------------------------------

var errPythonNotFound = &pythonNotFoundError{}

// pythonNotFoundError reports that no runnable Python 3.10+ candidate was
// found. When candidates existed but failed their probes, detail lists each
// one with its failure (e.g. the macOS xcode-select stub hint) so the user
// gets a single actionable message instead of exec noise at first tool call.
type pythonNotFoundError struct {
	detail string
}

func (e *pythonNotFoundError) Error() string {
	base := "no runnable Python 3.10+ found (uv preferred when available)"
	if e == nil || e.detail == "" {
		return base + "; install Python 3.10+ or uv (https://docs.astral.sh/uv/)"
	}
	return base + ":\n  - " + e.detail + "\n  install Python 3.10+ or uv (https://docs.astral.sh/uv/)"
}
