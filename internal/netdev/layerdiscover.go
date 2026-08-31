package netdev

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

// layerdiscover.go — F4: multi-layer discovery (spec §4.5). The recursion is
// "read tables (passive) → gap-fill probe (polite) → human promotes → the
// promoted device becomes the next vantage" — never a lateral login.
// Invariant 8 lives at the vantage door: only inventory devices with
// vault-backed credentials are accepted as vantage names.

// PrecheckResult is what one vantage's three read-only tables yielded.
type PrecheckResult struct {
	Vantage       string        `json:"vantage"`
	Vendor        string        `json:"vendor,omitempty"`
	Interfaces    []string      `json:"interfaces"` // name/ip/mask rows (display)
	DirectSubnets []SubnetClass `json:"direct_subnets"`
	RoutedSubnets []SubnetClass `json:"routed_subnets"`
	ArpKnownIPs   []string      `json:"arp_known_ips"`
	Uncovered     string        `json:"uncovered_note,omitempty"` // aggregates beyond coverage
	Warnings      []string      `json:"warnings"`
	At            string        `json:"at"`
}

// SubnetClass is one classified network with its plan default.
type SubnetClass struct {
	CIDR      string `json:"cidr"`
	Class     string `json:"class"` // direct-small | routed-small | medium | large
	Hosts     int    `json:"hosts"`
	DefaultOn bool   `json:"default_on"`
}

// PlanStep mirrors one planned probe for the UI's plan card.
type PlanStep struct {
	CIDR      string `json:"cidr"`
	Class     string `json:"class"`
	Hosts     int    `json:"hosts"`
	DefaultOn bool   `json:"default_on"`
}

// DiscoverPlan is the confirm-before-probe object (spec §4.5 precheck flow).
type DiscoverPlan struct {
	Vantage  string     `json:"vantage"`
	Steps    []PlanStep `json:"steps"`
	ArpKnown int        `json:"arp_known"`
	Warnings []string   `json:"warnings"`
}

// layerCommands returns (ifbrief, routes, arp) read commands per driver key.
func layerCommands(driverKey string) (string, string, string, bool) {
	switch driverKey {
	case "huawei-vrp":
		return "display ip interface brief", "display ip routing-table", "display arp", true
	case "cisco-ios":
		return "show ip interface brief", "show ip route", "show ip arp", true
	case "linux", "":
		return "ip -4 addr", "ip route", "ip neigh", true // host vantages
	default:
		return "", "", "", false
	}
}

var (
	ipv4MaskRe  = regexp.MustCompile(`(\d{1,3}(?:\.\d{1,3}){3})[/ ](\d{1,2}|(?:\d{1,3}\.){3}\d{1,3})`)
	routeDestRe = regexp.MustCompile(`\b(\d{1,3}(?:\.\d{1,3}){3}/\d{1,2})\b`)
	ipv4OnlyRe  = regexp.MustCompile(`\b(\d{1,3}(?:\.\d{1,3}){3})\b`)
)

// parseIfBrief extracts interface subnets ("ip/mask" or "ip dotted-mask").
func parseIfBrief(out string) []string {
	seen := map[string]bool{}
	var nets []string
	for _, m := range ipv4MaskRe.FindAllStringSubmatch(out, -1) {
		ip, mask := m[1], m[2]
		cidr := toCIDR(ip, mask)
		if cidr == "" || seen[cidr] {
			continue
		}
		seen[cidr] = true
		nets = append(nets, cidr)
	}
	return nets
}

// parseRouteTable extracts routed destination prefixes (skip defaults).
func parseRouteTable(out string) []string {
	seen := map[string]bool{}
	var nets []string
	for _, m := range routeDestRe.FindAllStringSubmatch(out, -1) {
		prefix := m[1]
		if strings.HasPrefix(prefix, "0.0.0.0/") {
			continue
		}
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		nets = append(nets, prefix)
	}
	return nets
}

// parseArpIPs extracts the IPs the vantage already knows alive.
func parseArpIPs(out string) []string {
	seen := map[string]bool{}
	var ips []string
	for _, m := range ipv4OnlyRe.FindAllStringSubmatch(out, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		ips = append(ips, m[1])
	}
	sort.Strings(ips)
	return ips
}

// toCIDR collapses ip + (prefix-len | dotted mask) into a CIDR string.
func toCIDR(ip, mask string) string {
	ipAddr := net.ParseIP(ip)
	if ipAddr == nil {
		return ""
	}
	ipAddr = ipAddr.To4()
	if ipAddr == nil {
		return ""
	}
	bits := 32
	if strings.Contains(mask, ".") {
		m := net.IPMask(net.ParseIP(mask).To4())
		if m == nil {
			return ""
		}
		ones, _ := m.Size()
		bits = ones
	} else {
		fmt.Sscanf(mask, "%d", &bits)
		if bits < 0 || bits > 32 {
			return ""
		}
	}
	return ipAddr.Mask(net.CIDRMask(bits, 32)).String() + "/" + fmt.Sprint(bits)
}

// classifySubnet maps one CIDR to its spec §4.5 class + plan default.
func classifySubnet(cidr string, direct bool) (SubnetClass, bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return SubnetClass{}, false
	}
	ones, _ := ipnet.Mask.Size()
	hosts := 1 << uint(32-ones)
	if ones <= 30 {
		hosts -= 2
	}
	class := ""
	switch {
	case ones >= 24 && direct:
		class = "direct-small"
	case ones >= 24:
		class = "routed-small"
	case ones >= 21:
		class = "medium"
	default:
		class = "large"
	}
	return SubnetClass{CIDR: cidr, Class: class, Hosts: hosts, DefaultOn: class == "direct-small" || class == "routed-small"}, true
}

// buildPlan folds a precheck into the confirm-first plan. mediumOn reflects
// [netdev.discovery] medium_no_confirm (zero value = medium stays unchecked).
func buildPlan(p *PrecheckResult, mediumOn bool) *DiscoverPlan {
	plan := &DiscoverPlan{Vantage: p.Vantage, ArpKnown: len(p.ArpKnownIPs), Warnings: p.Warnings}
	seen := map[string]bool{}
	add := func(cidr string, direct bool) {
		if seen[cidr] {
			return
		}
		if sc, ok := classifySubnet(cidr, direct); ok {
			seen[cidr] = true
			if sc.Class == "medium" && mediumOn {
				sc.DefaultOn = true
			}
			plan.Steps = append(plan.Steps, PlanStep{CIDR: sc.CIDR, Class: sc.Class, Hosts: sc.Hosts, DefaultOn: sc.DefaultOn})
		}
	}
	for _, s := range p.DirectSubnets {
		add(s.CIDR, true)
	}
	for _, s := range p.RoutedSubnets {
		add(s.CIDR, false)
	}
	sort.Slice(plan.Steps, func(i, j int) bool {
		if plan.Steps[i].Class != plan.Steps[j].Class {
			return plan.Steps[i].Class < plan.Steps[j].Class
		}
		return plan.Steps[i].Hosts < plan.Steps[j].Hosts
	})
	return plan
}

// DiscoverPrecheck runs the three read-only table commands on a managed
// vantage through the sealed Exec path (audited by Exec itself) and folds
// them into a plan. Zero probe traffic — the polite-first layer of F4.
func (m *Manager) DiscoverPrecheck(ctx context.Context, vantage string) (*DiscoverPlan, error) {
	if !m.cfg.NetDev.Enabled {
		return nil, fmt.Errorf("[netdev] is disabled")
	}
	d, ok := m.cfg.NetDevDeviceByName(strings.TrimSpace(vantage))
	if !ok {
		return nil, fmt.Errorf("vantage %q is not in the inventory — layers only start from managed devices (invariant 8)", vantage)
	}
	ifBriefCmd, routeCmd, arpCmd, ok := layerCommands(drvKey(d))
	if !ok {
		return nil, fmt.Errorf("no layer table commands for vendor %q", d.Vendor)
	}
	res := &PrecheckResult{Vantage: d.Name, Vendor: d.Vendor, At: time.Now().Format("15:04:05")}
	for _, cmd := range []string{ifBriefCmd, routeCmd, arpCmd} {
		r := m.Exec(ctx, d.Name, cmd)
		if r.Refused {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s 被拒绝（只读表命令）", cmd))
			continue
		}
		if r.IsError {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s 执行失败", cmd))
			continue
		}
		switch cmd {
		case ifBriefCmd:
			res.Interfaces = parseIfBrief(r.Output)
		case routeCmd:
			res.RoutedSubnets = nil
			for _, cidr := range parseRouteTable(r.Output) {
				if sc, ok := classifySubnet(cidr, false); ok {
					res.RoutedSubnets = append(res.RoutedSubnets, sc)
				}
			}
		case arpCmd:
			res.ArpKnownIPs = parseArpIPs(r.Output)
		}
	}
	for _, cidr := range res.Interfaces {
		if sc, ok := classifySubnet(cidr, true); ok {
			res.DirectSubnets = append(res.DirectSubnets, sc)
		}
	}
	_ = AppendAudit(Audit{Time: time.Now(), Device: d.Name, Command: "(discover-precheck) tables", Class: "read", Status: AuditOK})
	return buildPlan(res, m.cfg.NetDev.Discovery.NoMediumConfirm), nil
}

// DiscoverLayer probes the CONFIRMED subnets through the vantage device's
// own SSH tunnel (direct-tcpip from its network position). Scope whitelist
// applies per CIDR at the dial boundary; results record as layer-discover.
func (m *Manager) DiscoverLayer(ctx context.Context, vantage string, cidrs []string, ports []int) ([]DiscoverHostResult, error) {
	if !m.cfg.NetDev.Enabled {
		return nil, fmt.Errorf("[netdev] is disabled")
	}
	d, ok := m.cfg.NetDevDeviceByName(strings.TrimSpace(vantage))
	if !ok {
		return nil, fmt.Errorf("vantage %q is not in the inventory — layers only start from managed devices (invariant 8)", vantage)
	}
	if len(ports) == 0 {
		ports = []int{22, 23, 161, 443, 830}
	}
	if len(cidrs) == 0 {
		return nil, fmt.Errorf("no subnets selected")
	}
	if _, err := discoveryHostsWithinCap(cidrs, m.cfg.NetDev.Discovery.MaxHostsPerJob); err != nil {
		return nil, err
	}
	// Cross-layer budget (§4.7 max_hops): the human promotion gate stays, the
	// depth cap is enforced here with guidance, not remembered by hand.
	layers := LoadDeviceLayers()
	vLayer := layers[d.Name]
	if err := layerGuard(maxHopsEffective(m.cfg.NetDev.Discovery.MaxHops), vLayer, d.Name); err != nil {
		return nil, err
	}
	runID := time.Now().Format("20060102-150405")
	ctx, stopRun := runCtx(ctx, runID)
	defer stopRun()
	run := &DiscoveryRunState{ID: runID, Vantage: d.Name, Ports: ports, Cidrs: cidrs, Status: "running", StartedAt: time.Now().Format(time.RFC3339)}
	defer func() {
		if run.Status == "running" && ctx.Err() != nil {
			run.Status = "paused"
		}
		_ = SaveDiscoveryRun(run)
	}()
	// The vantage's own supervised client (vault credentials — invariant 8:
	// only inventory credentials ever become a tunnel).
	client, err := m.dialDeviceClient(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("vantage %s: %w", d.Name, err)
	}
	defer client.Close()
	sshClient, err := client.SSH()
	if err != nil {
		return nil, fmt.Errorf("vantage %s ssh: %w", d.Name, err)
	}
	dialer := sshDialer{client: sshClient}

	var out []DiscoverHostResult
	for _, cidr := range cidrs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		target, targetNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
		hosts, err := expandCIDR(target, targetNet)
		if err != nil {
			return nil, err
		}
		if !m.scopeAllows(targetNet) {
			// scope 外：记录不探测（看到≠可连）——跳过并在审计留痕。
			_ = AppendAudit(Audit{Time: time.Now(), Device: d.Name, Command: "(discover-layer) skip out-of-scope " + cidr, Class: "guardrail", Status: AuditRefused})
			continue
		}
		hosts = cacheTTLFilter(hosts, m.cfg.NetDev.Discovery.CacheTTLHours, time.Now())
		res, err := m.probeHosts(ctx, dialer, hosts, ports)
		if err != nil {
			return out, err
		}
		_ = AppendAudit(Audit{Time: time.Now(), Device: d.Name, Command: "(discover-layer) tcp-probe " + cidr, Class: "read", Status: AuditOK})
		if len(res) > 0 {
			_ = RecordDiscoveredSwept(SourceLayer, res, ports)
			ips := make([]string, 0, len(res))
			for _, h := range res {
				ips = append(ips, h.IP)
			}
			// Provenance + depth: this vantage, one level below it.
			_ = StampDiscoveredVantage(d.Name, ips)
			_ = StampDiscoveredLayer(vLayer+1, ips)
			run.FoundSoFar += len(res)
			out = append(out, res...)
		}
		// Checkpoint: a completed net never re-probes on resume.
		run.DoneCidrs = append(run.DoneCidrs, cidr)
		run.UpdatedAt = time.Now().Format(time.RFC3339)
		_ = SaveDiscoveryRun(run)
		if ctx.Err() != nil {
			run.Status = "paused"
			return out, nil // paused, not failed — resume continues the rest
		}
	}
	run.Status = "done"
	return out, nil
}

// DiscoverResume continues a paused layered scan from its last checkpoint.
// Results accumulate in the 待确认区 either way; the return value carries
// only this continuation's finds.
func (m *Manager) DiscoverResume(ctx context.Context) ([]DiscoverHostResult, error) {
	run, err := LoadDiscoveryRun()
	if err != nil || run == nil {
		return nil, fmt.Errorf("no discovery run to resume")
	}
	if run.Status != "paused" {
		return nil, fmt.Errorf("last run is %s — nothing to resume (start a new plan instead)", run.Status)
	}
	remaining := run.Remaining()
	if len(remaining) == 0 {
		run.Status = "done"
		_ = SaveDiscoveryRun(run)
		return nil, nil
	}
	return m.DiscoverLayer(ctx, run.Vantage, remaining, run.Ports)
}

// SourceLayer is F4's store source constant.
const SourceLayer = "layer-discover"
