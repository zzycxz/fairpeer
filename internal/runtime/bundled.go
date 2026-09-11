package runtime

// bundled.go finds runtimes bundled alongside the fairpeer executable.
// Mirrors internal/codegraph's bundledBaseDir() pattern: look in the directory
// containing the executable for a <BundleDirName>/ subdirectory.

import (
	"os"
	"path/filepath"
	"runtime"
)

// bundledBaseDir returns the absolute path to the bundle directory next to the
// fairpeer executable (e.g. /opt/fairpeer/runtimes or C:\...\fairpeer\runtimes).
// Returns ("", false) if the executable path can't be determined.
func bundledBaseDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Join(filepath.Dir(exe), BundleDirName), true
}

// isExec reports whether path exists and is usable as an executable: on Windows
// any existing file qualifies (callers filter on .exe/.cmd/.bat extensions); off
// Windows the file must carry at least one execute bit (owner/group/other). The
// exec-bit check matters on unix, where a download or archive extraction can
// leave a 0644 binary that os/exec would refuse to run with a confusing EACCES.
func isExec(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

// bundledUV looks for uv in the exe-adjacent bundle directory.
func bundledUV() (string, bool) {
	base, ok := bundledBaseDir()
	if !ok {
		return "", false
	}
	for _, name := range uvNames() {
		// Check both flat (runtimes/uv.exe) and nested (runtimes/bin/uv.exe).
		for _, p := range []string{
			filepath.Join(base, name),
			filepath.Join(base, "bin", name),
		} {
			if isExec(p) {
				return p, true
			}
		}
	}
	return "", false
}

// lookPathInDir checks if a command exists in a specific directory (used for
// Windows common install locations that aren't on PATH for GUI processes).
func lookPathInDir(dir, name string) (string, bool) {
	if runtime.GOOS == "windows" {
		for _, ext := range []string{".exe", ".cmd", ".bat"} {
			p := filepath.Join(dir, name+ext)
			if isExec(p) {
				return p, true
			}
		}
	} else {
		p := filepath.Join(dir, name)
		if isExec(p) {
			return p, true
		}
	}
	return "", false
}
