//go:build !darwin

package sandbox

import "os/exec"

// Command runs the command unwrapped: no OS sandbox is implemented for this
// platform beyond bubblewrap on Linux (the permission layer still gates the
// call).
//
// When spec.Mode is "enforce" and bubblewrap (bwrap) is available on PATH,
// the command is wrapped in a bubblewrap sandbox with a profile analogous to
// macOS Seatbelt: writes confined to WriteRoots, network denied unless
// spec.Network is true. When bwrap is unavailable, enforce + RequireAvailable
// fails CLOSED (mirroring the darwin path): the returned argv refuses instead
// of running unconfined. Without RequireAvailable the command runs unconfined
// (boot and acp warn about this once at startup).
func Command(spec Spec, sh Shell, command string) ([]string, bool) {
	if !spec.enforce() {
		return sh.argv(command), false
	}
	if bwrap, err := exec.LookPath("bwrap"); err == nil {
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

// Available reports whether an OS sandbox is available on this platform.
// On Linux, this checks for bubblewrap (bwrap) on PATH.
func Available() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

// bwrapArgs builds the bubblewrap command-line arguments that confine the
// shell command to the write roots, deny network unless allowed, and allow
// read access to the whole filesystem (matching the macOS Seatbelt profile's
// read-open policy).
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
	return append(args, sh.argv(command)...)
}
