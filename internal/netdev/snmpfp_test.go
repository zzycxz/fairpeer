package netdev

import (
	"path/filepath"
	"testing"
)

// F2's word-seam: sysDescr/platform strings → vendor/role hints.
func TestHintsFromSysDescr(t *testing.T) {
	cases := []struct {
		desc   string
		vendor string
		role   string
	}{
		{"S5735-L48P-EI Huawei Versatile Routing Platform", "huawei", "switch"}, // S\d{4} word
		{"USG6320 Huawei Versatile Routing Platform", "huawei", "firewall"},     // usg prefix
		{"cisco WS-C3750G-24TS,  Capabilities: Switch IGMP", "cisco", "switch"}, // switch word
		{"cisco ISR4321/K9,  Capabilities: Router", "cisco", "router"},          // isr prefix
		{"Cisco IOS Software, C3750 Software", "cisco", ""},                     // vendor only — honest
		{"Linux web01 5.15.0-91-generic x86_64", "linux", "server"},             // a Linux box is server-class
		{"VMware ESXi 8.0.0 build-20513097", "vmware", "server"},
		{"", "", ""},
		{"Hillstone StoneOS 5.5R8", "hillstone", ""}, // vendor only
	}
	for _, c := range cases {
		vendor, role := hintsFromSysDescr(c.desc)
		if vendor != c.vendor || role != c.role {
			t.Errorf("hintsFromSysDescr(%q) = (%q,%q), want (%q,%q)", c.desc, vendor, role, c.vendor, c.role)
		}
	}
}

func TestRecordDiscoveredHints(t *testing.T) {
	dir := t.TempDir()
	old := discoveredDirOverr
	discoveredDirOverr = filepath.Join(dir, "discovered")
	t.Cleanup(func() { discoveredDirOverr = oldDirBack(old) })

	// Hint-first: a lead that only ever came from the neighbor tables.
	if err := RecordDiscoveredHints(SourceTopo, "10.30.2.1", "huawei", "firewall"); err != nil {
		t.Fatal(err)
	}
	// A later, weaker hint must merge the source but never downgrade.
	if err := RecordDiscoveredHints(SourceDiscover, "10.30.2.1", "", "switch"); err != nil {
		t.Fatal(err)
	}
	hosts, err := ListDiscoveredHosts()
	if err != nil || len(hosts) != 1 {
		t.Fatalf("hosts = %v err = %v", hosts, err)
	}
	h := hosts[0]
	if h.VendorHint != "huawei" || h.RoleHint != "firewall" {
		t.Errorf("hints downgraded: %+v", h)
	}
	if len(h.Sources) != 2 {
		t.Errorf("sources = %v", h.Sources)
	}
}

// oldDirBack exists so the cleanup closure reads clearly above.
func oldDirBack(s string) string { return s }
