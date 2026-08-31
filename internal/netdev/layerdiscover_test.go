package netdev

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const huaweiIfBriefFixture = `
Interface                         IP Address/Mask      Physical   Protocol
GigabitEthernet0/0/0              unassigned           down       down
GigabitEthernet0/0/1              192.168.1.1/24       up         up
Vlanif10                          10.20.0.1/24         up         up
Vlanif99                          10.21.0.1/22         up         up
`

const huaweiRouteFixture = `
Destination/Mask    Proto   NextHop
0.0.0.0/0           Static  10.30.0.1
10.20.0.0/24        Direct  10.20.0.1
10.30.5.0/24        OSPF    10.20.0.2
10.128.0.0/9        Static  10.20.0.2
`

const ciscoIfBriefFixture = `
Interface              IP-Address      OK? Method Status
GigabitEthernet0/1     10.30.0.1       YES manual up
GigabitEthernet0/2     unassigned      YES unset  down
`

const arpFixture = `
IP ADDRESS      MAC ADDRESS     EXPIRE(M) TYPE
10.20.0.5       5cb9-01ed-2f42  12        I
10.20.0.6       5cb9-01ee-1111  8         I
`

func TestParseIfBrief(t *testing.T) {
	got := parseIfBrief(huaweiIfBriefFixture)
	want := []string{"192.168.1.0/24", "10.20.0.0/24", "10.21.0.0/22"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("net[%d] = %s, want %s", i, got[i], want[i])
		}
	}
	if cs := parseIfBrief(ciscoIfBriefFixture); len(cs) != 0 {
		// Cisco brief carries bare IPs without masks — nothing to infer here.
		t.Errorf("cisco brief without masks should yield no subnets: %v", cs)
	}
}

func TestParseRouteTable(t *testing.T) {
	got := parseRouteTable(huaweiRouteFixture)
	// Default route skipped; three prefixes remain.
	if len(got) != 3 || got[0] != "10.20.0.0/24" || got[2] != "10.128.0.0/9" {
		t.Fatalf("routes = %v", got)
	}
}

func TestParseArpIPs(t *testing.T) {
	ips := parseArpIPs(arpFixture)
	if len(ips) != 2 || ips[0] != "10.20.0.5" {
		t.Fatalf("arp ips = %v", ips)
	}
}

func TestClassifySubnet(t *testing.T) {
	cases := []struct {
		cidr   string
		direct bool
		class  string
		on     bool
	}{
		{"192.168.1.0/24", true, "direct-small", true},
		{"10.20.0.0/24", false, "routed-small", true},
		{"10.21.0.0/22", true, "medium", false},
		{"10.21.0.0/22", false, "medium", false},
		{"10.128.0.0/9", false, "large", false},
		{"10.0.0.0/8", true, "large", false},
	}
	for _, c := range cases {
		sc, ok := classifySubnet(c.cidr, c.direct)
		if !ok || sc.Class != c.class || sc.DefaultOn != c.on {
			t.Errorf("classifySubnet(%s,%v) = %+v ok=%v, want class=%s on=%v", c.cidr, c.direct, sc, ok, c.class, c.on)
		}
	}
	if sc, _ := classifySubnet("10.20.0.0/24", true); sc.Hosts != 254 {
		t.Errorf("/24 hosts = %d, want 254", sc.Hosts)
	}
}

func TestBuildPlan(t *testing.T) {
	p := &PrecheckResult{
		Vantage:       "CORE-1",
		DirectSubnets: []SubnetClass{{CIDR: "192.168.1.0/24"}, {CIDR: "10.21.0.0/22"}},
		RoutedSubnets: []SubnetClass{{CIDR: "10.20.0.0/24"}, {CIDR: "10.128.0.0/9"}},
		ArpKnownIPs:   []string{"10.20.0.5", "10.20.0.6"},
	}
	plan := buildPlan(p, false)
	if plan.Vantage != "CORE-1" || plan.ArpKnown != 2 {
		t.Fatalf("plan meta = %+v", plan)
	}
	// Four classes, direct/routed small default-on, medium/large off; the
	// overlap (10.20/24 direct? no — direct list here has no overlap) plus
	// sort order: class asc, hosts asc.
	byCIDR := map[string]PlanStep{}
	for _, s := range plan.Steps {
		byCIDR[s.CIDR] = s
	}
	if len(plan.Steps) != 4 {
		t.Fatalf("steps = %+v", plan.Steps)
	}
	for cidr, on := range map[string]bool{
		"192.168.1.0/24": true, "10.20.0.0/24": true, "10.21.0.0/22": false, "10.128.0.0/9": false,
	} {
		if byCIDR[cidr].DefaultOn != on {
			t.Errorf("%s default_on = %v, want %v", cidr, byCIDR[cidr].DefaultOn, on)
		}
	}
}

func TestDiscoveryPacing(t *testing.T) {
	// rate: default 50, config override honored, fast_mode x4 capped at 256.
	if r := discoveryEffectiveRate(0, false); r != 50 {
		t.Errorf("default rate = %d", r)
	}
	if r := discoveryEffectiveRate(100, true); r != 256 {
		t.Errorf("fast x4 cap = %d", r)
	}
	if r := discoveryEffectiveRate(40, true); r != 160 {
		t.Errorf("fast 40x4 = %d", r)
	}
	// host cap: default one /16; override honored.
	if c := discoveryHostCap(0); c != 65536 {
		t.Errorf("default cap = %d", c)
	}
	if c := discoveryHostCap(1024); c != 1024 {
		t.Errorf("override cap = %d", c)
	}
	// per-host delay: 0 → spec default 800, -1 → off, value passes.
	for in, want := range map[int]int{0: 800, -1: 0, 250: 250} {
		if got := discoveryPerHostDelayMs(in); got != want {
			t.Errorf("delay(%d) = %d, want %d", in, got, want)
		}
	}
	// cap enforcement: one /22 (1024) under default cap passes; a /8 refuses
	// with guidance (never silently truncates).
	if n, err := discoveryHostsWithinCap([]string{"10.21.0.0/22"}, 0); err != nil || n != 1024 {
		t.Errorf("within cap: n=%d err=%v", n, err)
	}
	if _, err := discoveryHostsWithinCap([]string{"10.0.0.0/8"}, 0); err == nil ||
		!strings.Contains(err.Error(), "max_hosts_per_job") {
		t.Errorf("over cap must refuse with guidance: %v", err)
	}
}

func TestCacheTTLFilter(t *testing.T) {
	dir := t.TempDir()
	oldDir := discoveredDirOverr
	discoveredDirOverr = filepath.Join(dir, "d")
	t.Cleanup(func() { discoveredDirOverr = oldDir })
	now := time.Now()
	_ = RecordDiscoveredPorts(SourceDiscover, "10.0.0.1", "", nil) // fresh
	_ = RecordDiscoveredPorts(SourceDiscover, "10.0.0.2", "", nil)
	// age 10.0.0.2 beyond the TTL by rewriting LastSeen
	p := filepath.Join(discoveredDirOverr, "10.0.0.2.json")
	b, _ := os.ReadFile(p)
	var h DiscoveredHost
	_ = json.Unmarshal(b, &h)
	h.LastSeen = now.Add(-48 * time.Hour)
	bb, _ := json.Marshal(h)
	_ = os.WriteFile(p, bb, 0o600)

	hosts := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	got := cacheTTLFilter(hosts, 24, now)
	if len(got) != 2 || got[0] != "10.0.0.2" || got[1] != "10.0.0.3" {
		t.Fatalf("filtered = %v (fresh 10.0.0.1 must be skipped)", got)
	}
	if all := cacheTTLFilter(hosts, -1, now); len(all) != 3 {
		t.Errorf("ttl -1 disables filtering: %v", all)
	}
}

func TestStampDiscoveredVantage(t *testing.T) {
	dir := t.TempDir()
	oldDir := discoveredDirOverr
	discoveredDirOverr = filepath.Join(dir, "d")
	t.Cleanup(func() { discoveredDirOverr = oldDir })
	_ = RecordDiscoveredPorts(SourceDiscover, "10.9.9.9", "", nil)
	if err := StampDiscoveredVantage("CORE-1", []string{"10.9.9.9"}); err != nil {
		t.Fatal(err)
	}
	hosts, _ := ListDiscoveredHosts()
	if len(hosts) != 1 || hosts[0].Vantage != "CORE-1" {
		t.Fatalf("vantage stamp = %+v", hosts)
	}
}

func TestCancelDiscoverRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runCtx, done := runCtx(ctx, "test")
	defer done()
	select {
	case <-runCtx.Done():
		t.Fatal("run cancelled prematurely")
	default:
	}
	if n := CancelDiscoverRuns(); n < 1 {
		t.Fatalf("cancelled %d runs, want >=1", n)
	}
	select {
	case <-runCtx.Done():
	default:
		t.Fatal("emergency stop must cancel the registered run")
	}
}

func TestBuildPlanMediumOptIn(t *testing.T) {
	p := &PrecheckResult{
		DirectSubnets: []SubnetClass{{CIDR: "10.21.0.0/22"}, {CIDR: "192.168.1.0/24"}},
	}
	// default: medium stays unchecked
	plan := buildPlan(p, false)
	for _, s := range plan.Steps {
		if s.Class == "medium" && s.DefaultOn {
			t.Error("medium must stay unchecked by default")
		}
	}
	// medium_no_confirm = true: pre-checked (explicit trust)
	plan = buildPlan(p, true)
	mediumOn := false
	for _, s := range plan.Steps {
		if s.Class == "medium" && s.DefaultOn {
			mediumOn = true
		}
	}
	if !mediumOn {
		t.Error("medium_no_confirm must pre-check medium nets")
	}
}
