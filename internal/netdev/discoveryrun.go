package netdev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// discoveryrun.go — F4's closing deviations (spec §9.2): checkpoint/resume
// for layered scans, and the cross-layer budget ledger.
//
// Checkpoint granularity is one CIDR per step — a completed net never
// re-probes (and the cache-TTL filter makes even a re-listed net cheap:
// fresh leads are skipped). Resume continues the REMAINING nets of a paused
// run; the 待确认区 store carries all evidence either way.

// DiscoveryRunState is the persisted progress of one layered scan.
type DiscoveryRunState struct {
	ID        string   `json:"id"`
	Vantage   string   `json:"vantage"`
	Ports     []int    `json:"ports,omitempty"`
	Cidrs     []string `json:"cidrs"`
	DoneCidrs []string `json:"done_cidrs"`
	// Status: running | paused | done.
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	UpdatedAt  string `json:"updated_at"`
	FoundSoFar int    `json:"found_so_far"`
}

// Remaining returns the not-yet-probed nets in plan order.
func (r *DiscoveryRunState) Remaining() []string {
	done := map[string]bool{}
	for _, c := range r.DoneCidrs {
		done[c] = true
	}
	var out []string
	for _, c := range r.Cidrs {
		if !done[c] {
			out = append(out, c)
		}
	}
	return out
}

var (
	discoveryRunMu   sync.Mutex
	discoveryRunOver string
)

func discoveryRunFile() string {
	if discoveryRunOver != "" {
		return discoveryRunOver
	}
	return filepath.Join(netdevStateDir(), "discovery-run.json")
}

// SaveDiscoveryRun persists the run state (idempotent overwrite).
func SaveDiscoveryRun(r *DiscoveryRunState) error {
	discoveryRunMu.Lock()
	defer discoveryRunMu.Unlock()
	return saveDiscoveryRunLocked(r)
}

func saveDiscoveryRunLocked(r *DiscoveryRunState) error {
	if r.UpdatedAt == "" {
		r.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(discoveryRunFile()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(discoveryRunFile(), b, 0o600)
}

// LoadDiscoveryRun returns the current run (nil when none/corrupt).
func LoadDiscoveryRun() (*DiscoveryRunState, error) {
	discoveryRunMu.Lock()
	defer discoveryRunMu.Unlock()
	b, err := os.ReadFile(discoveryRunFile())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var r DiscoveryRunState
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil // corrupt state restarts clean, never blocks a scan
	}
	return &r, nil
}

// ── cross-layer ledger ──────────────────────────────────────────────────────

// The layer ledger records at which recursion depth each managed device
// became a vantage: inventory devices sit at 0; a device promoted from a
// layer-N lead inherits N; leads it discovers are layer N+1. max_hops caps
// the recursion (§4.7): the human gate (promotion) stays, the budget is now
// enforced instead of remembered.

var (
	deviceLayersMu   sync.Mutex
	deviceLayersOver string
)

func deviceLayersFile() string {
	if deviceLayersOver != "" {
		return deviceLayersOver
	}
	return filepath.Join(netdevStateDir(), "discovery-layers.json")
}

// DeviceLayersPath exposes the layer ledger's file location for the
// state-history snapshot (promotions rewrite it).
func DeviceLayersPath() string { return deviceLayersFile() }

// LoadDeviceLayers returns device → recursion depth (nil map when none).
func LoadDeviceLayers() map[string]int {
	deviceLayersMu.Lock()
	defer deviceLayersMu.Unlock()
	return loadDeviceLayersLocked()
}

func loadDeviceLayersLocked() map[string]int {
	b, err := os.ReadFile(deviceLayersFile())
	if err != nil {
		return map[string]int{}
	}
	var m map[string]int
	if json.Unmarshal(b, &m) != nil {
		return map[string]int{}
	}
	return m
}

// RecordDeviceLayer stamps one device's layer (idempotent).
func RecordDeviceLayer(device string, layer int) error {
	deviceLayersMu.Lock()
	defer deviceLayersMu.Unlock()
	m := loadDeviceLayersLocked()
	m[device] = layer
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(deviceLayersFile()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(deviceLayersFile(), b, 0o600)
}

// maxHopsEffective: 0 → spec default 2, clamped to 1..4.
func maxHopsEffective(cfg int) int {
	if cfg <= 0 {
		return 2
	}
	if cfg > 4 {
		return 4
	}
	return cfg
}

// layerGuard refuses a scan whose leads would exceed max_hops. The message
// names the knob — a refusal with guidance, never a silent truncation.
func layerGuard(maxHops, vantageLayer int, vantage string) error {
	if vantageLayer+1 > maxHops {
		return fmt.Errorf("vantage %s sits at layer %d — another layer would exceed max_hops=%d (raise [netdev.discovery] max_hops within 1..4, or choose a shallower vantage)", vantage, vantageLayer, maxHops)
	}
	return nil
}

// sortedPorts renders ports stably for run-state identity.
func sortedPorts(ports []int) string {
	p := append([]int{}, ports...)
	sort.Ints(p)
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ",")
}
