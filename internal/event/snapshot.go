// snapshot.go — upgrade spec 4-2: periodic item snapshots that let a frontend
// restore any-length sessions instantly (replacing the 100-turn present
// sidecar cap). A snapshot is a full item list at a known revision; recovery
// replays deltas after it.
package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionSnapshot is the persisted item state at a point in time.
type SessionSnapshot struct {
	SessionID string     `json:"session_id"`
	Revision  int        `json:"revision"`
	Items     []ItemEvent `json:"items"`
	CreatedAt time.Time  `json:"created_at"`
}

// SnapshotWriter accumulates ItemEvents and periodically flushes a snapshot.
type SnapshotWriter struct {
	mu        sync.Mutex
	dir       string
	sessionID string
	revision  int
	items     []ItemEvent
	lastFlush time.Time
	// dirty counts items since the last flush.
	dirty int
}

// NewSnapshotWriter creates a writer for one session directory.
func NewSnapshotWriter(sessionDir, sessionID string) *SnapshotWriter {
	return &SnapshotWriter{
		dir:       sessionDir,
		sessionID: sessionID,
	}
}

// Record appends one ItemEvent and triggers a flush when thresholds hit.
func (w *SnapshotWriter) Record(e ItemEvent) {
	w.mu.Lock()
	w.items = append(w.items, e)
	w.dirty++
	shouldFlush := w.dirty >= 50 || time.Since(w.lastFlush) > 30*time.Second
	w.mu.Unlock()
	if shouldFlush {
		w.Flush()
	}
}

// Flush writes the current snapshot to disk atomically.
func (w *SnapshotWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	snap := SessionSnapshot{
		SessionID: w.sessionID,
		Revision:  w.revision,
		Items:     append([]ItemEvent(nil), w.items...),
		CreatedAt: time.Now().UTC(),
	}
	b, _ := json.MarshalIndent(snap, "", "  ")
	path := filepath.Join(w.dir, "snapshot.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
	w.dirty = 0
	w.lastFlush = time.Now()
}

// LoadSnapshot reads a snapshot from disk (nil when absent/corrupt).
func LoadSnapshot(sessionDir string) *SessionSnapshot {
	b, err := os.ReadFile(filepath.Join(sessionDir, "snapshot.json"))
	if err != nil {
		return nil
	}
	var snap SessionSnapshot
	if json.Unmarshal(b, &snap) != nil {
		return nil
	}
	return &snap
}
