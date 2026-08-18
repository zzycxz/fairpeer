package netdev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Audit records every device interaction in an append-only JSONL file under
// <user config>/fairpeer/netdev/audit.jsonl — one of the never-off guardrails
// (NETDEV_SPEC invariant 4). Command text and outcome are recorded; raw
// output is deliberately NOT stored (device output can carry secrets — the
// redactor and evidence integration land in P2; until then only the size and
// a hash are kept).
type Audit struct {
	Time        time.Time `json:"time"`
	Device      string    `json:"device"`
	Via         []string  `json:"via,omitempty"`
	Command     string    `json:"command"`
	Class       string    `json:"class"`  // read | write | dangerous | unknown (classifier verdict)
	Status      string    `json:"status"` // ok | device-error | refused | failure
	OutputBytes int       `json:"output_bytes,omitempty"`
	Error       string    `json:"error,omitempty"`
}

// Audit statuses.
const (
	AuditOK          = "ok"
	AuditDeviceError = "device-error"
	AuditRefused     = "refused"
	AuditFailure     = "failure"
)

var (
	auditMu   sync.Mutex
	auditPath string // overridden in tests
)

// SetAuditPath overrides the audit file location (tests).
func SetAuditPath(p string) {
	auditMu.Lock()
	defer auditMu.Unlock()
	auditPath = p
}

func auditFile() string {
	auditMu.Lock()
	defer auditMu.Unlock()
	if auditPath != "" {
		return auditPath
	}
	return filepath.Join(netdevStateDir(), "audit.jsonl")
}

// AuditPath returns the audit file location (test override included).
func AuditPath() string {
	if p := auditPathLocked(); p != "" {
		return p
	}
	return filepath.Join(netdevStateDir(), "audit.jsonl")
}

func auditPathLocked() string {
	auditMu.Lock()
	defer auditMu.Unlock()
	return auditPath
}

// netdevStateDir is the netdev state directory beside secrets.enc.json.
func netdevStateDir() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		home, _ := os.UserHomeDir()
		dir = home
	}
	return filepath.Join(dir, "fairpeer", "netdev")
}

// AppendAudit writes one entry. Failures are returned (the caller logs); an
// audit failure must not silently vanish — but it also must not block the
// diagnostic hand (the command already ran; the audit gap is itself
// observable in ops review).
func AppendAudit(e Audit) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	auditMu.Lock()
	defer auditMu.Unlock()
	path := auditPath
	if path == "" {
		path = filepath.Join(netdevStateDir(), "audit.jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
