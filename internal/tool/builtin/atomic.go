package builtin

// atomic.go provides crash-atomic file writes for document/builtin tools.
//
// All file-producing tools (doc_write, xlsx_write, csv_write, write_file,
// edit_file, multi_edit, apply_patch, delete_range, mindmap_create) ultimately
// write a target path. A naive truncate-and-write (os.Create / os.WriteFile /
// excelize.SaveAs) leaves a torn, unopenable file behind if the process dies
// mid-write — the original bytes are already gone, so there is no fallback.
//
// atomicWrite instead serializes the full payload to a sibling temp file,
// fsyncs it, then swaps it over the target in a single os.Rename. A crash at
// any point leaves either the old file or the new file intact, never a torn
// one. This mirrors OfficeCLI's AtomicPackageWriter (AtomicPackageWriter.cs).
//
// CRITICAL: the caller MUST close any handle it holds on `path` before calling
// (e.g. the zip reader in writeDOCXAppend). On Windows, os.Rename onto a path
// that's still open fails — see docxwrite.go:197-198.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// atomicWrite writes `write`'s output to a temp file, fsyncs, then atomically
// renames it over `path`. On any error from `write` or the fsync/rename, the
// temp file is removed and the original at `path` is left untouched.
func atomicWrite(path string, write func(*os.File) error) (err error) {
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0o755); e != nil {
		return fmt.Errorf("atomic write: mkdir %s: %w", dir, e)
	}
	tmp, e := os.CreateTemp(dir, ".fp-tmp-*")
	if e != nil {
		return fmt.Errorf("atomic write: create temp: %w", e)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup on any failure path. On success tmpName == path
	// after the rename, so the Remove is a no-op (path now points at the new
	// file; the temp name is gone).
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if err = write(tmp); err != nil {
		tmp.Close()
		return err
	}
	// Flush the kernel page cache to disk so a power loss after a successful
	// rename still leaves the bytes committed. (Crash-safety against process
	// death comes from the rename alone; fsync adds power-loss durability.)
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("atomic write: fsync: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("atomic write: close temp: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomic write: rename over %s: %w", path, err)
	}
	return nil
}

// atomicWriteBytes is a convenience wrapper for the common "write a byte slice"
// case (text files, encoded payloads).
func atomicWriteBytes(path string, data []byte) error {
	return atomicWrite(path, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	})
}

// cleanupStaleTemps removes leftover .fp-tmp-* files from prior crashed runs.
// Only files older than 1 hour are touched, so an in-flight write from another
// (live) process is never disturbed. The scan walks `dir`; pass the workspace
// root. Safe to call at boot — missing dir is a no-op.
func cleanupStaleTemps(dir string) {
	const ageThreshold = time.Hour
	cutoff := time.Now().Add(-ageThreshold)
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort; a walk error shouldn't stop cleanup
		}
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(p)
		// Only our own temp prefix — never touch .docx-append-* (legacy) or
		// unrelated dotfiles. Hidden prefix avoids matching real user files.
		if !strings.HasPrefix(base, ".fp-tmp-") {
			return nil
		}
		if info.ModTime().After(cutoff) {
			return nil // fresh enough to be live
		}
		_ = os.Remove(p)
		return nil
	})
}
