// Package spill keeps oversized tool output retrievable. The jobs/bash capped
// buffers retain head+tail and DISCARD the middle — for a 2 MiB build log the
// dropped middle is exactly where the error usually is, and the only recovery
// was re-running the tool (P1-B1). A Sink captures everything written after
// the cap engages; the buffer's truncation marker then carries the spill path
// so the model can read the full overflow back with read_file.
package spill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	mu   sync.Mutex
	base string
)

// SetBase overrides the spill directory (tests). Empty restores the default.
func SetBase(dir string) {
	mu.Lock()
	base = dir
	mu.Unlock()
}

// Dir returns the spill directory, creating it on first use.
func Dir() (string, error) {
	mu.Lock()
	b := base
	mu.Unlock()
	if b == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		b = filepath.Join(home, ".fairpeer", "spills")
	}
	if err := os.MkdirAll(b, 0o700); err != nil {
		return "", err
	}
	return b, nil
}

// Sink is an append-only spill file. Safe for concurrent use is NOT required —
// each sink belongs to one output pipe.
type Sink struct {
	f    *os.File
	path string
}

// Open creates a new spill file labeled by the producing job (e.g.
// "bash-3"). label is sanitized into the filename.
func Open(label string) (*Sink, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, label)
	if safe == "" {
		safe = "output"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.log", safe, time.Now().UnixMilli()))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	go Prune(100) // best-effort retention; never blocks the producer
	return &Sink{f: f, path: path}, nil
}

// Write appends to the spill file. Errors are swallowed: spilling is strictly
// best-effort recovery, never a new failure mode for the tool itself.
func (s *Sink) Write(p []byte) {
	if s == nil || s.f == nil {
		return
	}
	_, _ = s.f.Write(p)
}

// Path returns the spill file's path ("" for a nil sink).
func (s *Sink) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Close flushes and closes the sink. Nil-safe.
func (s *Sink) Close() {
	if s == nil || s.f == nil {
		return
	}
	_ = s.f.Close()
	s.f = nil
}

// Prune keeps only the newest keep spill files (0/负数 = 不清理). Old spills are
// diagnostic output, not data — unbounded growth on a heavy session would
// quietly eat the home dir.
func Prune(keep int) {
	if keep <= 0 {
		return
	}
	dir, err := Dir()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= keep {
		return
	}
	type fileInfo struct {
		name string
		mod  time.Time
	}
	infos := make([]fileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		if info, err := e.Info(); err == nil {
			infos = append(infos, fileInfo{e.Name(), info.ModTime()})
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].mod.After(infos[j].mod) })
	for _, old := range infos[keep:] {
		_ = os.Remove(filepath.Join(dir, old.name))
	}
}
