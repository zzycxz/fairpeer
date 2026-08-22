// crashrecovery.go — upgrade spec 5-2: crash-turn recovery markers. A marker
// file is created at turn start and deleted at turn end; if the app crashes
// mid-turn, the marker survives and the next startup can offer to resume.
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// CrashMarker records an interrupted turn for recovery (spec 5-2).
type CrashMarker struct {
	SessionPath string    `json:"session_path"`
	TurnInput   string    `json:"turn_input"`
	StartedAt   time.Time `json:"started_at"`
}

// CrashMarkerPath returns the marker file path for a session directory.
func CrashMarkerPath(sessionDir string) string {
	return filepath.Join(sessionDir, "crash_recovery.json")
}

// WriteCrashMarker creates the marker at turn start.
func WriteCrashMarker(sessionDir, sessionPath, turnInput string) {
	if sessionDir == "" {
		return
	}
	m := CrashMarker{
		SessionPath: sessionPath,
		TurnInput:   turnInput,
		StartedAt:   time.Now().UTC(),
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(CrashMarkerPath(sessionDir), b, 0o600)
}

// ClearCrashMarker removes the marker at turn end (normal or error).
func ClearCrashMarker(sessionDir string) {
	if sessionDir == "" {
		return
	}
	_ = os.Remove(CrashMarkerPath(sessionDir))
}

// FindCrashMarker returns the marker if one survives (crash recovery).
func FindCrashMarker(sessionDir string) (*CrashMarker, bool) {
	b, err := os.ReadFile(CrashMarkerPath(sessionDir))
	if err != nil {
		return nil, false
	}
	var m CrashMarker
	if json.Unmarshal(b, &m) != nil || m.TurnInput == "" {
		return nil, false
	}
	return &m, true
}
