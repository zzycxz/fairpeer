package netdev

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Topology: parse CDP/LLDP neighbor output into device edges. Neighbors that
// reference devices outside the inventory are returned as-is — "看到 ≠ 可连"
// (NETDEV_SPEC §5.3): the UI renders unmanaged nodes grey and unclickable;
// connecting to them still requires human inventory registration.

// TopologyEdge is one adjacency.
type TopologyEdge struct {
	LocalDevice  string `json:"local_device"`
	LocalPort    string `json:"local_port"`
	RemoteDevice string `json:"remote_device"`
	RemotePort   string `json:"remote_port,omitempty"`
	RemoteIP     string `json:"remote_ip,omitempty"`
	Source       string `json:"source"` // lldp | cdp
}

// NeighborCommand returns the read command that yields neighbor information
// for the driver key (huawei-vrp → LLDP brief, cisco-ios → CDP detail).
func NeighborCommand(driverKey string) (string, bool) {
	switch driverKey {
	case "huawei-vrp":
		return "display lldp neighbor", true
	case "cisco-ios":
		return "show cdp neighbors detail", true
	default:
		return "", false
	}
}

// ── Huawei LLDP ─────────────────────────────────────────────────────────────

var (
	lldpPortHeader = regexp.MustCompile(`(?m)^(\S+)\s+has\s+\d+\s+neighbor`)
	lldpField      = map[string]*regexp.Regexp{
		"system name": regexp.MustCompile(`(?im)^\s*System name\s*:\s*(\S+)`),
		"port id":     regexp.MustCompile(`(?im)^\s*Port ID\s*:\s*(\S+)`),
		"mgmt":        regexp.MustCompile(`(?im)^\s*Management address\s*:\s*(\d+\.\d+\.\d+\.\d+)`),
	}
)

// parseHuaweiLLDP splits `display lldp neighbor` output into per-port blocks
// and extracts (local port, remote system name, remote port id, mgmt IP).
func parseHuaweiLLDP(out string) []TopologyEdge {
	var edges []TopologyEdge
	type seg struct {
		localPort string
		body      strings.Builder
	}
	var segs []seg
	var cur *seg
	for _, line := range strings.Split(out, "\n") {
		if m := lldpPortHeader.FindStringSubmatch(line); m != nil {
			segs = append(segs, seg{localPort: normalizeIfName(m[1])})
			cur = &segs[len(segs)-1]
			continue
		}
		if cur != nil {
			cur.body.WriteString(line)
			cur.body.WriteByte('\n')
		}
	}
	for _, s := range segs {
		body := s.body.String()
		name := firstGroup(lldpField["system name"], body)
		if name == "" {
			continue // block without a system name is not a usable neighbor
		}
		edges = append(edges, TopologyEdge{
			LocalPort:    s.localPort,
			RemoteDevice: name,
			RemotePort:   normalizeIfName(firstGroup(lldpField["port id"], body)),
			RemoteIP:     firstGroup(lldpField["mgmt"], body),
			Source:       "lldp",
		})
	}
	return edges
}

// ── Cisco CDP ───────────────────────────────────────────────────────────────

var (
	cdpDeviceID = regexp.MustCompile(`(?im)^\s*Device ID:\s*(.+?)\s*$`)
	cdpPlatform = regexp.MustCompile(`(?im)^\s*Platform:\s*([^,]+)`)
	cdpIface    = regexp.MustCompile(`(?im)^\s*Interface:\s*([^,]+),\s*Port ID \(outgoing port\):\s*(.+?)\s*$`)
	cdpIP       = regexp.MustCompile(`(?im)^\s*IP(?:v4)? address:\s*(\d+\.\d+\.\d+\.\d+)`)
)

// parseCiscoCDP splits `show cdp neighbors detail` into device blocks.
func parseCiscoCDP(out string) []TopologyEdge {
	var edges []TopologyEdge
	blocks := cdpDeviceID.Split(out, -1) // block i describes device ID i-1
	ids := cdpDeviceID.FindAllStringSubmatch(out, -1)
	for i, block := range blocks[1:] {
		id := strings.TrimSpace(ids[i][1])
		// Device ID may carry a serial in parens: "SW2(9ABCDEF1234)".
		if j := strings.Index(id, "("); j > 0 {
			id = id[:j]
		}
		m := cdpIface.FindStringSubmatch(block)
		if m == nil {
			continue
		}
		edges = append(edges, TopologyEdge{
			RemoteDevice: id,
			LocalPort:    normalizeIfName(strings.TrimSpace(m[1])),
			RemotePort:   normalizeIfName(strings.TrimSpace(m[2])),
			RemoteIP:     firstGroup(cdpIP, block),
			Source:       "cdp",
		})
	}
	return edges
}

// TopologySnapshot runs the neighbor query on every inventory device (the
// sealed read path, redacted) and merges the edges. Nodes not in the
// inventory carry Unmanaged=true — visible, never connectable.
type TopologyNode struct {
	Name     string `json:"name"`
	Managed  bool   `json:"managed"`
	DeviceIP string `json:"device_ip,omitempty"`
	// Subnet annotates the local IP-plan view (the /24 the address lives in).
	Subnet string `json:"subnet,omitempty"`
	// Tier is the LOCAL inference's band (0 core / 1 agg / 2 access / 3
	// unmanaged) from group words, name conventions, or subnet ordering —
	// present only in the IP-plan view; the LLDP snapshot leaves it at -1
	// and the map falls back to its degree heuristic.
	Tier int `json:"tier"`
}

// TopologyGraph is the merged snapshot for the layout's mini-map.
type TopologyGraph struct {
	Nodes []TopologyNode `json:"nodes"`
	Edges []TopologyEdge `json:"edges"`
	At    string         `json:"at"`
}

func (m *Manager) TopologySnapshot(ctx context.Context) (*TopologyGraph, error) {
	g := &TopologyGraph{At: time.Now().Format("15:04:05")}
	seenNode := map[string]bool{}
	managed := map[string]bool{}
	for _, d := range m.cfg.NetDev.Devices {
		managed[d.Name] = true
	}
	for _, d := range m.cfg.NetDev.Devices {
		drv, ok := m.driverFor(d)
		if !ok {
			continue
		}
		cmd, ok := NeighborCommand(drv.Key())
		if !ok {
			continue
		}
		res := m.Exec(ctx, d.Name, cmd)
		if res.Refused {
			continue
		}
		edges, err := parseNeighbors(drvKey(d), res.Output)
		if err != nil {
			continue
		}
		for i := range edges {
			edges[i].LocalDevice = d.Name
			g.Edges = append(g.Edges, edges[i])
		}
	}
	// Nodes: every managed device that appears, plus unmanaged neighbors.
	// Tier stays -1 here: the measured view has no local inference; the map's
	// degree heuristic assigns bands.
	for _, d := range m.cfg.NetDev.Devices {
		if !seenNode[d.Name] {
			seenNode[d.Name] = true
			g.Nodes = append(g.Nodes, TopologyNode{Name: d.Name, Managed: true, DeviceIP: d.Address, Tier: -1})
		}
	}
	for _, e := range g.Edges {
		if !seenNode[e.RemoteDevice] {
			seenNode[e.RemoteDevice] = true
			tier := -1
			if !managed[e.RemoteDevice] {
				tier = 3
			}
			g.Nodes = append(g.Nodes, TopologyNode{Name: e.RemoteDevice, Managed: managed[e.RemoteDevice], DeviceIP: e.RemoteIP, Tier: tier})
		}
	}
	return g, nil
}

// parseNeighbors dispatches on the driver key.
func parseNeighbors(driverKey, out string) ([]TopologyEdge, error) {
	switch driverKey {
	case "huawei-vrp":
		return parseHuaweiLLDP(out), nil
	case "cisco-ios":
		return parseCiscoCDP(out), nil
	default:
		return nil, fmt.Errorf("no neighbor parser for driver %q", driverKey)
	}
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// normalizeIfName expands the abbreviations devices use in neighbor tables
// ("Gig 0/1", "Gi0/1", "GE0/0/1") toward a canonical long form so edges from
// both ends of a link can be correlated.
func normalizeIfName(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	long := map[string]string{
		"gi": "GigabitEthernet", "gig": "GigabitEthernet", "ge": "GigabitEthernet",
		"fa": "FastEthernet", "fastr": "FastEthernet",
		"te": "TenGigabitEthernet", "xg": "TenGigabitEthernet", "10ge": "TenGigabitEthernet",
		"xgig": "TenGigabitEthernet", "twe": "TwentyFiveGigE", "fo": "FortyGigE",
		"eth": "Ethernet", "e": "Ethernet",
		"po": "Eth-Trunk", "port-channel": "Eth-Trunk", "eth-trunk": "Eth-Trunk",
		"vl": "Vlanif", "vlanif": "Vlanif", "vla": "Vlanif",
		"se": "Serial", "br": "Bridge-Aggregation", "hu": "HundredGigE",
	}
	// Split "prefix" from the first digit or slash.
	i := 0
	for i < len(lower) && !(lower[i] >= '0' && lower[i] <= '9') && lower[i] != '/' {
		i++
	}
	prefix, rest := s[:i], s[i:]
	if expand, ok := long[strings.ToLower(prefix)]; ok {
		return expand + rest
	}
	return s
}
