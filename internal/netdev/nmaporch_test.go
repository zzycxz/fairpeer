package netdev

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

func TestParseNmapXML(t *testing.T) {
	raw := `<?xml version="1.0"?>
<nmaprun>
 <host>
  <address addr="10.1.0.5" addrtype="ipv4"/>
  <hostnames><hostname name="web01.lab"/></hostnames>
  <ports>
   <port protocol="tcp" portid="22"><state state="open"/><service name="ssh" product="OpenSSH" version="9.6"/></port>
   <port protocol="tcp" portid="80"><state state="open"/><service name="http" product="nginx" version="1.24.0"/></port>
   <port protocol="tcp" portid="8080"><state state="closed"/></port>
  </ports>
 </host>
 <host>
  <address addr="10.1.0.6" addrtype="mac"/>
  <ports><port protocol="tcp" portid="23"><state state="open"/><service name="telnet"/></port></ports>
 </host>
</nmaprun>`
	hosts, err := parseNmapXML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	// The MAC-only host has no IP → dropped; closed ports → dropped.
	if len(hosts) != 1 {
		t.Fatalf("hosts = %+v", hosts)
	}
	h := hosts[0]
	if h.IP != "10.1.0.5" || h.Hostname != "web01.lab" || len(h.Services) != 2 {
		t.Fatalf("host = %+v", h)
	}
	if h.Services[0].Product != "OpenSSH" || h.Services[0].Version != "9.6" || h.Services[1].Service != "http" {
		t.Fatalf("services = %+v", h.Services)
	}
}

func TestNmapSweepGates(t *testing.T) {
	sim := startSimDevice(t)
	m, _ := testManager(t, sim) // no assessment envelope

	// Engagement gate first: refused even before scope/binary checks.
	if _, err := m.NmapSweep(context.Background(), "10.0.0.0/24"); err == nil || !strings.Contains(err.Error(), "engagement") {
		t.Fatalf("no-envelope sweep must refuse on the engagement gate, got %v", err)
	}
	// With an envelope, an out-of-scope CIDR still refuses (scopes never off).
	m.cfg.NetDev.Assessment = config.NetDevAssessment{
		EngagementID: "NMAP-TEST-1",
		Expires:      time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
		Approver:     "tester",
	}
	if _, err := m.NmapSweep(context.Background(), "203.0.113.0/24"); err == nil || !strings.Contains(err.Error(), "scopes") {
		t.Fatalf("out-of-scope sweep must refuse on scopes, got %v", err)
	}
}

func TestNetprobeSweepGates(t *testing.T) {
	sim := startSimDevice(t)
	m, _ := testManager(t, sim) // no assessment envelope

	if _, err := m.NetprobeSweep(context.Background(), "10.0.0.0/24", false); err == nil || !strings.Contains(err.Error(), "engagement") {
		t.Fatalf("no-envelope sweep must refuse on the engagement gate, got %v", err)
	}
	m.cfg.NetDev.Assessment = config.NetDevAssessment{
		EngagementID: "NETPROBE-TEST-1",
		Expires:      time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
		Approver:     "tester",
	}
	if _, err := m.NetprobeSweep(context.Background(), "203.0.113.0/24", false); err == nil || !strings.Contains(err.Error(), "scopes") {
		t.Fatalf("out-of-scope sweep must refuse on scopes, got %v", err)
	}
	// A /16 is legal for netprobe (per-job budget), unlike tunnel mode's 4096
	// cap — verified by reaching the binary-lookup stage (scopes gate must
	// pass for an in-scope /16 first).
	m.cfg.NetDev.Discovery.Scopes = []string{"198.18.0.0/15"}
	_, err := m.NetprobeSweep(context.Background(), "198.18.0.0/16", false)
	if err == nil || !strings.Contains(err.Error(), "netprobe binary not found") {
		t.Fatalf("in-scope /16 must pass gates and reach the binary lookup, got %v", err)
	}
}
