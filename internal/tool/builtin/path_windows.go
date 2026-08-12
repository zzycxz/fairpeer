package builtin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// extendLengthPath adds the \\?\ prefix on Windows so paths longer than MAX_PATH
// (260 chars) work for file operations. The prefix disables the OS's path
// munging, so the path MUST be backslash-only and fully qualified first. On
// non-Windows it's a pass-through.
func extendLengthPath(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	if strings.HasPrefix(path, `\\?\`) {
		return path // already prefixed
	}
	// Backslash-only + cleaned: the \\?\ prefix turns off slash translation, so
	// any forward slash would be passed literally to the API and rejected.
	path = filepath.FromSlash(path)
	path = filepath.Clean(path)
	// UNC paths use the \\?\UNC\ prefix form.
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + path[2:]
	}
	if filepath.IsAbs(path) {
		return `\\?\` + path
	}
	return path // relative — prefix only helps absolute paths
}

// writeFileWithRetry wraps os.WriteFile with a bounded retry loop for Windows
// ERROR_SHARING_VIOLATION (errno 32), which fires transiently when an editor,
// antivirus, or cloud-sync tool holds a write lock on the target. It retries
// up to 3 times with backoff (100/200/300ms); non-sharing errors are returned
// immediately. On non-Windows it's a plain os.WriteFile.
func writeFileWithRetry(path string, data []byte, perm os.FileMode) error {
	err := os.WriteFile(path, data, perm)
	if err == nil || !isSharingViolation(err) {
		return err
	}
	for attempt := 1; attempt <= 3; attempt++ {
		time.Sleep(time.Duration(100*attempt) * time.Millisecond)
		err = os.WriteFile(path, data, perm)
		if err == nil || !isSharingViolation(err) {
			return err
		}
	}
	return fmt.Errorf("file %s locked after retries (sharing violation): %w", path, err)
}

// isSharingViolation reports whether err is a Windows ERROR_SHARING_VIOLATION
// (32), matched by substring since Go's syscall error wrapping varies by Go
// version and the OS error string is stable. On non-Windows it's always false.
func isSharingViolation(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sharing violation") ||
		strings.Contains(msg, "being used by another process") ||
		strings.Contains(msg, "the process cannot access the file")
}
