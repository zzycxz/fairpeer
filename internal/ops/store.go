// store.go — the persisted Request ledger: one JSON file per request under
// <user config>/fairpeer/ops/requests/, atomic writes (the same discipline as
// the netdev OpStep ledger). File-based on purpose: a request outlives any
// one session and must survive restarts for ops_status and the later
// Job/Finding/Proposal linkage phases.
package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/fileutil"
)

var (
	stateDirOverr string
	mu            sync.Mutex
)

// SetStateDir overrides the store location (tests).
func SetStateDir(p string) {
	stateDirOverr = p
}

func requestsDir() string {
	if stateDirOverr != "" {
		return filepath.Join(stateDirOverr, "requests")
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		home, _ := os.UserHomeDir()
		dir = home
	}
	return filepath.Join(dir, "fairpeer", "ops", "requests")
}

// NewRequest mints a request in `received` with a per-day sequential id
// (REQ-YYYYMMDD-NNNN). Prefer MintAndSaveRequest on creation paths: mint and
// persist must be one atomic step or two concurrent creators can mint the
// same id (both scan the dir before either saves).
func NewRequest(source, actor, text string) *Request {
	now := time.Now()
	mu.Lock()
	id := mintRequestIDLocked(now)
	mu.Unlock()
	r := &Request{
		ID:        id,
		Source:    source,
		Actor:     actor,
		Text:      text,
		State:     StateReceived,
		CreatedAt: now.UTC().Format(time.RFC3339),
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}
	return r
}

// MintAndSaveRequest creates the request and persists it under one lock —
// the id-sequencing scan and the file write can't interleave with another
// creator's, so concurrent ops_classify calls get distinct ids.
func MintAndSaveRequest(source, actor, text string) (*Request, error) {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	r := &Request{
		ID:        mintRequestIDLocked(now),
		Source:    source,
		Actor:     actor,
		Text:      text,
		State:     StateReceived,
		CreatedAt: now.UTC().Format(time.RFC3339),
		UpdatedAt: now.UTC().Format(time.RFC3339),
	}
	dir := requestsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := fileutil.AtomicWriteFile(filepath.Join(dir, r.ID+".json"), b, 0o600); err != nil {
		return nil, err
	}
	return r, nil
}

// mintRequestIDLocked is mintRequestID with mu already held.
func mintRequestIDLocked(now time.Time) string {
	prefix := fmt.Sprintf("REQ-%s-", now.Format("20060102"))
	max := 0
	if entries, err := os.ReadDir(requestsDir()); err == nil {
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".json")
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			var n int
			if _, err := fmt.Sscanf(strings.TrimPrefix(name, prefix), "%d", &n); err == nil && n > max {
				max = n
			}
		}
	}
	return fmt.Sprintf("%s%04d", prefix, max+1)
}

// SaveRequest persists the request atomically.
func SaveRequest(r *Request) error {
	mu.Lock()
	defer mu.Unlock()
	return saveRequestLocked(r)
}

// saveRequestLocked persists r with the caller holding mu.
func saveRequestLocked(r *Request) error {
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	dir := requestsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(filepath.Join(dir, r.ID+".json"), b, 0o600)
}

// UpdateRequest loads the request and runs fn under the store lock across the
// whole load→mutate→save span (P0-7): the GetRequest→mutate→SaveRequest idiom
// left that span unlocked, so two concurrent tool calls on the same request
// could drop each other's state transitions. An error from fn aborts without
// saving.
func UpdateRequest(id string, fn func(*Request) error) error {
	mu.Lock()
	defer mu.Unlock()
	r, err := GetRequest(id) // no internal locking — safe under the caller's mu
	if err != nil {
		return err
	}
	if err := fn(r); err != nil {
		return err
	}
	return saveRequestLocked(r)
}

// GetRequest loads one request by id.
func GetRequest(id string) (*Request, error) {
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return nil, fmt.Errorf("invalid request id %q", id)
	}
	b, err := os.ReadFile(filepath.Join(requestsDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var r Request
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRequests returns requests newest-first (by id's date+sequence, falling
// back to nothing when the ledger is empty). limit <= 0 means 20.
func ListRequests(limit int) []Request {
	if limit <= 0 {
		limit = 20
	}
	entries, err := os.ReadDir(requestsDir())
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	// Ids sort lexicographically in creation order (date prefix + zero pad).
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	out := make([]Request, 0, limit)
	for _, name := range names {
		if len(out) >= limit {
			break
		}
		b, err := os.ReadFile(filepath.Join(requestsDir(), name+".json"))
		if err != nil {
			continue
		}
		var r Request
		if json.Unmarshal(b, &r) == nil && r.ID != "" {
			out = append(out, r)
		}
	}
	return out
}
