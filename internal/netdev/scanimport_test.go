package netdev

import (
	"path/filepath"
	"strings"
	"testing"
)

const nmapFixture = `<?xml version="1.0"?>
<nmaprun>
 <host><address addr="10.0.0.2" addrtype="ipv4"/>
  <hostnames><hostname name="core-sw-1"/></hostnames>
  <ports><port protocol="tcp" portid="22"><state state="open"/><service name="ssh"/></port>
         <port protocol="tcp" portid="161"><state state="filtered"/><service name="snmp"/></port></ports>
 </host>
 <host><address addr="10.0.9.77" addrtype="ipv4"/>
  <ports><port protocol="tcp" portid="445"><state state="open"/><service name="microsoft-ds"/></port></ports>
 </host>
</nmaprun>`

func TestImportNmapXML(t *testing.T) {
	// The import now also records asset leads — keep test writes out of the
	// real state dir.
	oldDir := discoveredDirOverr
	discoveredDirOverr = filepath.Join(t.TempDir(), "discovered")
	t.Cleanup(func() { discoveredDirOverr = oldDir })
	f, err := ImportNmapForConfig(nmapFixture, []string{"10.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Title, "2 台主机") || !strings.Contains(f.Title, "1 台待确认") {
		t.Fatalf("title = %q", f.Title)
	}
	if f.Severity != "warning" {
		t.Fatalf("unknown host must warn, severity=%s", f.Severity)
	}
	joined := strings.Join(func() []string {
		var o []string
		for _, e := range f.Evidence {
			o = append(o, e.Output)
		}
		return o
	}(), "\n")
	if !strings.Contains(joined, "core-sw-1 [已纳管]") || !strings.Contains(joined, "22/tcp(ssh)") {
		t.Fatalf("managed host line malformed:\n%s", joined)
	}
	if !strings.Contains(joined, "10.0.9.77") || !strings.Contains(joined, "待确认") {
		t.Fatalf("unknown host not flagged:\n%s", joined)
	}
	if strings.Contains(joined, "161") {
		t.Fatal("filtered port leaked into evidence — only open ports count")
	}
}
