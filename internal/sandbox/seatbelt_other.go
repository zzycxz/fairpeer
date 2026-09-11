//go:build !darwin

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Command runs the command unwrapped: no OS sandbox is implemented for this
// platform beyond bubblewrap on Linux (the permission layer still gates the
// call).
//
// When spec.Mode is "enforce" and bubblewrap (bwrap) is usable on this host,
// the command is wrapped in a bubblewrap sandbox with a profile analogous to
// macOS Seatbelt: writes confined to WriteRoots plus temp/toolchain caches,
// network denied unless spec.Network is true. When bwrap is unusable, enforce
// + RequireAvailable fails CLOSED (mirroring the darwin path): the returned
// argv refuses instead of running unconfined. Without RequireAvailable the
// command runs unconfined (boot and acp warn about this once at startup).
func Command(spec Spec, sh Shell, command string) ([]string, bool) {
	if !spec.enforce() {
		return sh.argv(command), false
	}
	if bwrap, ok := bwrapPath(); ok {
		argv := append([]string{bwrap}, bwrapArgs(spec, sh, command)...)
		return argv, true
	}
	// enforce requested but bwrap unavailable. RequireAvailable fails closed
	// like the darwin path — but instead of a nil argv (darwin's refusal
	// signal, checked by callers that honor the bool), return an argv that
	// runs the SHELL with a refusal script. The bash tool ignores the second
	// return and indexes argv[0], so nil would panic it; a refusal argv keeps
	// it error-safe: nothing executes and the model sees a clear reason.
	if spec.RequireAvailable {
		return refuseArgv(sh), false
	}
	// No RequireAvailable: boot/acp already warned at startup; fall back to
	// unconfined (the false result signals "not sandboxed").
	return sh.argv(command), false
}

// refuseArgv builds a shell argv that reports the sandbox refusal on stderr
// and exits non-zero WITHOUT running the command. Works under both bash and
// PowerShell (echo >&2 and exit are valid in both).
func refuseArgv(sh Shell) []string {
	return sh.argv(`echo 'bash sandbox: enforce requested with require_available=true but no OS sandbox backend is available on this platform; refusing to run unconfined' >&2; exit 126`)
}

// Available reports whether an OS sandbox backend is usable on this platform.
// On Linux this is bubblewrap (bwrap), verified FUNCTIONALLY rather than by
// LookPath alone: hosts with unprivileged user namespaces disabled (a common
// hardening setting on enterprise distros) ship a bwrap binary that exists but
// cannot build a sandbox, so every wrapped command would fail at exec. The
// probe runs a minimal `bwrap … -- true` once and caches the result, keeping
// Available cheap for the boot warning (internal/boot) and the doctor flag
// (internal/doctor).
func Available() bool {
	_, ok := bwrapPath()
	return ok
}

var (
	bwrapProbeOnce sync.Once
	bwrapProbePath string
	bwrapProbeOK   bool
)

// bwrapPath resolves bwrap once: LookPath, then a functional probe that
// actually builds a sandbox and runs true inside it. Only a passing probe
// marks the backend available (and returns its path for Command to use).
func bwrapPath() (string, bool) {
	bwrapProbeOnce.Do(func() {
		p, err := exec.LookPath("bwrap")
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// The same mount skeleton bwrapArgs builds, minus the write grants: if
		// this cannot run (userns disabled, setuid bubblewrap missing), no
		// profile we generate can either.
		cmd := exec.CommandContext(ctx, p, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", "true")
		if err := cmd.Run(); err != nil {
			return
		}
		bwrapProbePath, bwrapProbeOK = p, true
	})
	return bwrapProbePath, bwrapProbeOK
}

// bwrapArgs builds the bubblewrap command-line arguments that confine the
// shell command to the write roots, deny network unless allowed, and allow
// read access to the whole filesystem (matching the macOS Seatbelt profile's
// read-open policy).
//
// Like the darwin profile's writeAllowDirs, temp and the common toolchain
// caches are granted on top of the caller's roots: without them pip/npm/go
// install/cargo cannot download or unpack packages under enforce, and users
// respond by turning the sandbox off. bwrap --bind requires the source path
// to exist, so cache dirs are created as needed (darwin's Seatbelt matches
// subpaths of not-yet-existing dirs, bwrap cannot bind them).
func bwrapArgs(spec Spec, sh Shell, command string) []string {
	args := []string{
		"--unshare-net", // deny network by default
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
	}
	if spec.Network {
		// Re-allow network by removing the network namespace.
		args = args[1:] // drop --unshare-net
	}
	for _, root := range spec.WriteRoots {
		args = append(args, "--bind", root, root)
	}
	for _, dir := range bwrapWriteDirs(spec.WriteRoots, spec.StrictWrites) {
		// A missing source makes the whole bwrap invocation fail, so create
		// the grant dirs and drop any that cannot be created (a read-only
		// home, say) rather than failing every sandboxed command.
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		args = append(args, "--bind", dir, dir)
	}
	return append(args, sh.argv(command)...)
}

// bwrapWriteDirs is the deduplicated set of directories the sandbox grants
// writes to: the caller's roots plus temp and the common toolchain caches
// under $HOME, mirroring writeAllowDirs in seatbelt_darwin.go (Linux has no
// /private, so no extra temp aliases; /tmp itself is covered by --tmpfs
// above, so os.TempDir() only earns a --bind when TMPDIR points elsewhere).
//
// strict narrows the toolchain grants to true cache subdirs only, the same
// mode the darwin path honors: ~/.npm and ~/go/pkg/mod drop out (npm installs
// and go module extraction break — that's the point), while the cargo
// registry's cache/src subdirs keep dependency fetches alive.
func bwrapWriteDirs(roots []string, strict bool) []string {
	dirs := append([]string{}, roots...)
	if tmp := os.TempDir(); tmp != "/tmp" {
		dirs = append(dirs, tmp)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if strict {
			// Cache-only subdirs: downloaded artifacts the toolchain regenerates
			// on demand. Bin/pkg/lifecycle dirs are deliberately excluded — a
			// prompt-injected command cannot drop an executable that a later
			// `cargo run` or npm script would execute.
			for _, sub := range []string{
				".cache",                // pip / generic XDG cache
				".cargo/registry/cache", // cargo downloaded crate tarballs
				".cargo/registry/src",   // cargo extracted crate sources
				"go/pkg/mod/cache",      // go module cache (downloaded modules)
			} {
				dirs = append(dirs, filepath.Join(home, sub))
			}
		} else {
			// SECURITY NOTE (audit A8, mirrors the darwin decision): these
			// whole-directory grants cover the toolchain's persistent
			// locations (~/.cargo holds bin/, ~/.npm holds lifecycle
			// scripts, ~/go/pkg/mod holds extracted modules a later build
			// executes). A prompt-injected command could drop a binary there
			// for the next build to run — a persistence backdoor. The broad
			// grants stay because `go install`/`cargo build`/`npm install`
			// legitimately write outside the cache; strict mode above scopes
			// them down for deployments that don't expect build-tool
			// execution.
			for _, sub := range []string{
				".cache",              // pip / generic XDG cache
				".npm",                // npm package cache + lifecycle scripts
				".cargo/registry",     // cargo registry (crate cache + sources)
				".cargo/registry/src", // cargo extracted crate sources
				"go/pkg/mod",          // go module cache
			} {
				dirs = append(dirs, filepath.Join(home, sub))
			}
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	return out
}
