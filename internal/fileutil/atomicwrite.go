package fileutil

import (
	"os"
	"path/filepath"
)

// ReplaceFile renames tmp onto dest, falling back to a copy when the rename
// fails — Windows encryption-software filter drivers report a cross-device link
// (EXDEV) for a same-dir rename. The rename error surfaces only if the copy also fails.
func ReplaceFile(tmp, dest string) error {
	if err := os.Rename(tmp, dest); err != nil {
		if copyErr := copyOnto(tmp, dest); copyErr != nil {
			return err
		}
	}
	return nil
}

func copyOnto(tmp, dest string) error {
	info, err := os.Stat(tmp)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dest, data, info.Mode().Perm()); err != nil {
		return err
	}
	// WriteFile keeps an existing dest's mode, so re-apply tmp's mode to match
	// what the rename would have done (a 0600 config tmp must not widen to 0644).
	_ = os.Chmod(dest, info.Mode().Perm())
	_ = os.Remove(tmp)
	return nil
}

// AtomicWriteFile writes data to dest atomically: it writes to a temp file in
// the same directory, applies perm, then renames onto dest via ReplaceFile. The
// temp file is removed on every error path. Use for files that other processes
// or goroutines may read concurrently (config, caches).
func AtomicWriteFile(dest string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := os.Chmod(tmpName, perm); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := ReplaceFile(tmpName, dest); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
