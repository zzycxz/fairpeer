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

// discovered.go — F1's 待确认区 store (spec §4.2.2). One JSON per IP beside
// the findings dir, same storage grammar. DiscoveredHost is an ASSET LEAD,
// never a Finding (problem evidence) and never an inventory device: promotion
// to managed is a human act through the config-apply path.

// DiscoveredPort is one port observation on one host.
type DiscoveredPort struct {
	Port   int        `json:"port"`
	Banner string     `json:"banner,omitempty"`
	Parsed BannerInfo `json:"parsed,omitempty"`
	// HTTP carries F3's opt-in application fingerprint (title/Server/cert)
	// when http_probe is enabled and the port answered one standard GET.
	HTTP *HTTPFingerprint `json:"http,omitempty"`
	At   time.Time        `json:"at"`
	// FirstSeen stamps the port's first observation (R2 journal basis:
	// newly-opened/newly-closed events are the diff between sweeps).
	FirstSeen time.Time `json:"first_seen,omitempty"`
}

// DiscoveredHost is one unmanaged asset lead.
type DiscoveredHost struct {
	IP         string           `json:"ip"`
	Hostname   string           `json:"hostname,omitempty"`
	VendorHint string           `json:"vendor_hint,omitempty"`
	RoleHint   string           `json:"role_hint,omitempty"`
	FirstSeen  time.Time        `json:"first_seen"`
	LastSeen   time.Time        `json:"last_seen"`
	Sources    []string         `json:"sources"`
	Ports      []DiscoveredPort `json:"ports"`
	// Vantage records which managed device saw this lead (F4 provenance —
	// the human-driven recursion's audit trail); Layer is the recursion depth
	// it was found at (inventory vantage → 1).
	Vantage string `json:"vantage,omitempty"`
	Layer   int    `json:"layer,omitempty"`
}

// Discovery sources (spec §4.2.2): where a lead came from. layer-discover
// arrives with F4.
const (
	SourceDiscover  = "discover"
	SourceNmap      = "nmap-import"
	SourceNetprobe  = "netprobe"
	SourceTopo      = "topo-neighbor"
	SourceLocateARP = "locate-arp"
)

var (
	discoveredMu       sync.Mutex
	discoveredDirOverr string
)

// DiscoveredDir stores leads as one JSON per IP.
func DiscoveredDir() string {
	if discoveredDirOverr != "" {
		return discoveredDirOverr
	}
	return filepath.Join(netdevStateDir(), "discovered")
}

func discoveredFile(ip string) string {
	// IPv6 colons are hostile to filenames; dots are fine.
	safe := strings.ReplaceAll(strings.TrimSpace(ip), ":", "-")
	return filepath.Join(DiscoveredDir(), safe+".json")
}

// RecordDiscovered merges one scan batch into the store (upsert per IP:
// sources dedupe, ports merge by number, hints only strengthen). Errors are
// returned but callers treat recording as best-effort — a store hiccup must
// never fail the discovery run itself. New ports fire R2 newly-opened events.
func RecordDiscovered(source string, hosts []DiscoverHostResult) error {
	return recordDiscoveredSwept(source, hosts, nil)
}

// RecordDiscoveredSwept is RecordDiscovered for a FULL port sweep: the caller
// probed exactly the swept list, so a stored port in that list that no longer
// answers is newly-closed (R2 event; the row drops). Hosts whose ports all
// closed never appear in results — their rows age out via LastSeen, a
// documented limitation (the partial-close signal is what matters).
func RecordDiscoveredSwept(source string, hosts []DiscoverHostResult, swept []int) error {
	if len(swept) == 0 {
		return recordDiscoveredSwept(source, hosts, nil)
	}
	set := make(map[int]bool, len(swept))
	for _, p := range swept {
		set[p] = true
	}
	return recordDiscoveredSwept(source, hosts, set)
}

func recordDiscoveredSwept(source string, hosts []DiscoverHostResult, swept map[int]bool) error {
	discoveredMu.Lock()
	defer discoveredMu.Unlock()
	now := time.Now()
	if err := os.MkdirAll(DiscoveredDir(), 0o700); err != nil {
		return err
	}
	for _, h := range hosts {
		if strings.TrimSpace(h.IP) == "" {
			continue
		}
		path := discoveredFile(h.IP)
		rec := &DiscoveredHost{IP: h.IP, FirstSeen: now, LastSeen: now}
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, rec) // corrupt file → start a fresh record
		}
		rec.LastSeen = now
		if rec.FirstSeen.IsZero() {
			rec.FirstSeen = now
		}
		if !containsStr(rec.Sources, source) {
			rec.Sources = append(rec.Sources, source)
		}
		for i := range rec.Ports {
			if rec.Ports[i].FirstSeen.IsZero() {
				rec.Ports[i].FirstSeen = rec.Ports[i].At // backfill pre-R2 rows
			}
		}
		openNow := map[int]bool{}
		for _, p := range h.Ports {
			if !p.Open {
				continue
			}
			openNow[p.Port] = true
			parsed := ParseBanner(p.Banner)
			merged := false
			for i := range rec.Ports {
				if rec.Ports[i].Port == p.Port {
					rec.Ports[i].Banner = p.Banner
					rec.Ports[i].Parsed = parsed
					rec.Ports[i].At = now
					merged = true
					break
				}
			}
			if !merged {
				rec.Ports = append(rec.Ports, DiscoveredPort{Port: p.Port, Banner: p.Banner, Parsed: parsed, At: now, FirstSeen: now})
				AppendPortEvent(h.IP, p.Port, "newly-opened") // R2（锁序：discoveredMu→journalMu 单向）
			}
			if parsed.VendorHint != "" {
				rec.VendorHint = parsed.VendorHint
			}
			if parsed.RoleHint != "" {
				rec.RoleHint = parsed.RoleHint
			}
		}
		if swept != nil {
			kept := rec.Ports[:0]
			for _, p := range rec.Ports {
				if swept[p.Port] && !openNow[p.Port] {
					AppendPortEvent(h.IP, p.Port, "newly-closed")
					continue
				}
				kept = append(kept, p)
			}
			rec.Ports = kept
		}
		sort.Slice(rec.Ports, func(i, j int) bool { return rec.Ports[i].Port < rec.Ports[j].Port })
		b, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("discovered %s: %w", h.IP, err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return fmt.Errorf("discovered %s: %w", h.IP, err)
		}
	}
	return nil
}

// RecordDiscoveredPorts records a lead whose ports came from a non-tunnel
// source (nmap import: service name instead of banner).
func RecordDiscoveredPorts(source, ip, hostname string, ports []DiscoveredPort) error {
	discoveredMu.Lock()
	defer discoveredMu.Unlock()
	now := time.Now()
	if err := os.MkdirAll(DiscoveredDir(), 0o700); err != nil {
		return err
	}
	path := discoveredFile(ip)
	rec := &DiscoveredHost{IP: ip, FirstSeen: now, LastSeen: now}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, rec)
	}
	rec.LastSeen = now
	if rec.FirstSeen.IsZero() {
		rec.FirstSeen = now
	}
	if hostname != "" && rec.Hostname == "" {
		rec.Hostname = hostname
	}
	if !containsStr(rec.Sources, source) {
		rec.Sources = append(rec.Sources, source)
	}
	for _, p := range ports {
		merged := false
		for i := range rec.Ports {
			if rec.Ports[i].Port == p.Port {
				merged = true
				break
			}
		}
		if !merged {
			rec.Ports = append(rec.Ports, DiscoveredPort{Port: p.Port, Banner: p.Banner, Parsed: p.Parsed, At: now})
		}
	}
	sort.Slice(rec.Ports, func(i, j int) bool { return rec.Ports[i].Port < rec.Ports[j].Port })
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// RecordDiscoveredHTTP files F3's application fingerprint for one ip:port
// (upsert; the port row is created if the lead only ever had table sources).
func RecordDiscoveredHTTP(ip string, port int, fp *HTTPFingerprint) error {
	if fp == nil || strings.TrimSpace(ip) == "" {
		return nil
	}
	discoveredMu.Lock()
	defer discoveredMu.Unlock()
	if err := os.MkdirAll(DiscoveredDir(), 0o700); err != nil {
		return err
	}
	path := discoveredFile(ip)
	now := time.Now()
	rec := &DiscoveredHost{IP: ip, FirstSeen: now, LastSeen: now}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, rec)
	}
	if rec.FirstSeen.IsZero() {
		rec.FirstSeen = now
	}
	merged := false
	for i := range rec.Ports {
		if rec.Ports[i].Port == port {
			rec.Ports[i].HTTP = fp
			rec.Ports[i].At = now
			merged = true
			break
		}
	}
	if !merged {
		rec.Ports = append(rec.Ports, DiscoveredPort{Port: port, HTTP: fp, At: now})
		sort.Slice(rec.Ports, func(i, j int) bool { return rec.Ports[i].Port < rec.Ports[j].Port })
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// cacheTTLFilter drops IPs whose leads were last seen inside the TTL —
// fresh knowledge is not re-probed (spec §4.7 cache_ttl_hours). Returns the
// filtered list; unreadable store = no filtering (probe rather than skip).
func cacheTTLFilter(hosts []string, ttlHours int, now time.Time) []string {
	if ttlHours == 0 {
		ttlHours = 24
	}
	if ttlHours < 0 {
		return hosts
	}
	known, err := ListDiscoveredHosts()
	if err != nil {
		return hosts
	}
	lastSeen := make(map[string]time.Time, len(known))
	for _, h := range known {
		lastSeen[h.IP] = h.LastSeen
	}
	var out []string
	for _, ip := range hosts {
		if t, ok := lastSeen[ip]; ok && now.Sub(t) < time.Duration(ttlHours)*time.Hour {
			continue
		}
		out = append(out, ip)
	}
	return out
}

// StampDiscoveredVantage marks which vantage produced a batch of leads
// (best-effort provenance stamp after RecordDiscovered).
func StampDiscoveredVantage(vantage string, ips []string) error {
	discoveredMu.Lock()
	defer discoveredMu.Unlock()
	for _, ip := range ips {
		if strings.TrimSpace(ip) == "" {
			continue
		}
		path := discoveredFile(ip)
		rec := &DiscoveredHost{IP: ip}
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, rec)
		}
		rec.Vantage = vantage
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// StampDiscoveredLayer stamps the recursion depth on a batch of leads.
func StampDiscoveredLayer(layer int, ips []string) error {
	discoveredMu.Lock()
	defer discoveredMu.Unlock()
	for _, ip := range ips {
		if strings.TrimSpace(ip) == "" {
			continue
		}
		path := discoveredFile(ip)
		rec := &DiscoveredHost{IP: ip}
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, rec)
		}
		rec.Layer = layer
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// RecordDiscoveredHints strengthens one lead's vendor/role hints (F2: SNMP
// sysDescr or neighbor platform). Existing hints win — hints never downgrade.
func RecordDiscoveredHints(source, ip, vendor, role string) error {
	if strings.TrimSpace(ip) == "" || (vendor == "" && role == "") {
		return nil
	}
	discoveredMu.Lock()
	defer discoveredMu.Unlock()
	if err := os.MkdirAll(DiscoveredDir(), 0o700); err != nil {
		return err
	}
	path := discoveredFile(ip)
	rec := &DiscoveredHost{IP: ip, FirstSeen: time.Now(), LastSeen: time.Now()}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, rec)
	}
	if rec.FirstSeen.IsZero() {
		rec.FirstSeen = time.Now()
	}
	if rec.VendorHint == "" {
		rec.VendorHint = vendor
	}
	if rec.RoleHint == "" {
		rec.RoleHint = role
	}
	if !containsStr(rec.Sources, source) {
		rec.Sources = append(rec.Sources, source)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// ListDiscoveredHosts returns leads newest-first (LastSeen).
func ListDiscoveredHosts() ([]*DiscoveredHost, error) {
	discoveredMu.Lock()
	defer discoveredMu.Unlock()
	entries, err := os.ReadDir(DiscoveredDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []*DiscoveredHost{}, nil
		}
		return nil, err
	}
	var out []*DiscoveredHost
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(DiscoveredDir(), e.Name()))
		if err != nil {
			continue
		}
		var h DiscoveredHost
		if err := json.Unmarshal(b, &h); err != nil || h.IP == "" {
			continue // corrupt records are skipped, never fatal
		}
		out = append(out, &h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out, nil
}

// DeleteDiscoveredHost removes one lead (dismissed by the user, or consumed
// by promotion).
func DeleteDiscoveredHost(ip string) error {
	discoveredMu.Lock()
	defer discoveredMu.Unlock()
	err := os.Remove(discoveredFile(ip))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
