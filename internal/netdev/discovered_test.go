package netdev

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseBanner(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		kind    string
		product string
		version string
		vendor  string
		role    string
	}{
		{"huawei ssh", "SSH-2.0-Huawei-1.0\r\n", "ssh", "Huawei-1.0", "1.0", "huawei", ""},
		// Vendor stays empty on generic SSH stacks — no wrong hints.
		{"openssh", "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13", "ssh", "OpenSSH_9.6p1", "9.6p1", "", ""},
		{"cisco ssh", "SSH-2.0-Cisco-1.25", "ssh", "Cisco-1.25", "1.25", "cisco", ""},
		{"vmware ssh implies server", "SSH-2.0-VMware-8.0.0", "ssh", "VMware-8.0.0", "8.0.0", "vmware", "server"},
		{"ssh bare software", "SSH-2.0-libssh_0.9.5", "ssh", "libssh_0.9.5", "0.9.5", "", ""},
		{"http with server header", "HTTP/1.1 200 OK\r\nServer: nginx/1.24.0\r\n\r\n", "http", "nginx", "1.24.0", "", ""},
		{"http status only", "HTTP/1.1 302 Found\r\n\r\n", "http", "", "", "", ""},
		{"ftp vsftpd", "220 (vsFTPd 3.0.3)", "ftp", "(vsFTPd", "", "", ""},
		{"smtp esmtp", "220 mail.example.com ESMTP Postfix ready", "smtp", "", "", "", ""},
		{"empty", "", "", "", "", "", ""},
		{"binary junk stays other", "\x01\x02\x03binary", "", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := ParseBanner(c.raw)
			if b.Kind != c.kind || b.Product != c.product || b.Version != c.version ||
				b.VendorHint != c.vendor || b.RoleHint != c.role {
				t.Errorf("ParseBanner(%q) = %+v, want kind=%q product=%q version=%q vendor=%q role=%q",
					c.raw, b, c.kind, c.product, c.version, c.vendor, c.role)
			}
		})
	}
}

func TestDiscoveredStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	old := discoveredDirOverr
	discoveredDirOverr = filepath.Join(dir, "discovered")
	t.Cleanup(func() { discoveredDirOverr = old })

	now := time.Now()
	batch := []DiscoverHostResult{
		{IP: "10.0.0.5", Ports: []DiscoverPortProbe{
			{Port: 22, Open: true, Banner: "SSH-2.0-Huawei-1.0"},
			{Port: 23, Open: true},
		}},
	}
	if err := RecordDiscovered(SourceDiscover, batch); err != nil {
		t.Fatalf("record: %v", err)
	}
	// Second run from nmap re-records the same IP: sources merge, FirstSeen
	// survives, the 22/23 ports refresh, a new port appends.
	batch2 := []DiscoverHostResult{
		{IP: "10.0.0.5", Ports: []DiscoverPortProbe{
			{Port: 22, Open: true, Banner: "SSH-2.0-Huawei-2.0"},
			{Port: 161, Open: true},
		}},
	}
	if err := RecordDiscovered(SourceNmap, batch2); err != nil {
		t.Fatalf("record2: %v", err)
	}
	hosts, err := ListDiscoveredHosts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(hosts) != 1 {
		t.Fatalf("want 1 host, got %d", len(hosts))
	}
	h := hosts[0]
	if h.IP != "10.0.0.5" {
		t.Fatalf("ip = %q", h.IP)
	}
	if len(h.Sources) != 2 || h.Sources[0] != SourceDiscover || h.Sources[1] != SourceNmap {
		t.Errorf("sources = %v", h.Sources)
	}
	if !h.FirstSeen.Before(time.Now()) || h.LastSeen.Sub(now) > 5*time.Second {
		t.Errorf("first/last seen wrong: %v / %v", h.FirstSeen, h.LastSeen)
	}
	if len(h.Ports) != 3 {
		t.Fatalf("ports = %+v", h.Ports)
	}
	if h.Ports[0].Port != 22 || h.Ports[0].Parsed.VendorHint != "huawei" {
		t.Errorf("port22 = %+v", h.Ports[0])
	}
	if h.VendorHint != "huawei" {
		t.Errorf("vendor hint = %q", h.VendorHint)
	}
	if err := DeleteDiscoveredHost("10.0.0.5"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if hosts, _ = ListDiscoveredHosts(); len(hosts) != 0 {
		t.Errorf("after delete: %d hosts", len(hosts))
	}
	if _, err := os.Stat(filepath.Join(DiscoveredDir(), "10.0.0.5.json")); !os.IsNotExist(err) {
		t.Errorf("file should be gone")
	}
}
