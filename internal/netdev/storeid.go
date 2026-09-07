package netdev

import (
	"path/filepath"
	"strings"
)

// validStoreID guards file-backed store lookups (proposals, templates, srvconf
// snapshots, backups) against path traversal: these ids arrive from UI strings
// and agent-authored proposal fields and are joined into paths verbatim, so
// "../secrets" must never reach filepath.Join.
func validStoreID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return false
	}
	// Base mismatch catches separators in either direction plus trailing
	// slashes on both platforms.
	return filepath.Base(id) == id
}
