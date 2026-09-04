package netdev

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

// Feed round-trip: import → match → findings (hit and clean paths).
func TestCVEFeedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	old := netdevStateDirOverr
	oldF := findingsDirOverr
	defer func() { netdevStateDirOverr = old; findingsDirOverr = oldF }()
	netdevStateDirOverr = dir
	findingsDirOverr = dir

	feed := `{"cves":[
		{"id":"CVE-2026-0001","desc":"IOS XE Web UI RCE","products":["cisco iosxe","cisco ios"],"severity":"critical"},
		{"id":"CVE-2026-0002","desc":"VRP buffer overflow","products":["huawei vrp"],"severity":"high"}
	]}`
	n, err := ImportCVEFeed(feed)
	if err != nil || n != 2 {
		t.Fatalf("import: %v n=%d", err, n)
	}
	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "core-sw-1", Vendor: "cisco", OS: "iosxe", Model: "9300"},
		{Name: "gw-1", Vendor: "huawei", OS: "vrp8", Model: "AR6300"},
		{Name: "vm-1", Vendor: "linux"},
	}
	m := NewManager(cfg)
	matches, err := m.MatchCVEs()
	if err != nil {
		t.Fatal(err)
	}
	// cisco hits both cve-0001 (iosxe) ; huawei hits cve-0002 ; linux none
	if len(matches) != 2 {
		t.Fatalf("matches: %+v", matches)
	}
	f, err := m.MatchCVEsToFindings()
	if err != nil || !strings.Contains(f.Title, "2 台命中") {
		t.Fatalf("finding: %v %+v", err, f)
	}
	if !strings.Contains(f.Detail, "CVE-2026-0001") || !strings.Contains(f.Detail, "CVE-2026-0002") {
		t.Fatalf("detail: %s", f.Detail)
	}
	// bad feed refuses
	if _, err := ImportCVEFeed(`{"cves":[]}`); err == nil {
		t.Fatal("empty feed must refuse")
	}
}

// NVD API 2.0 export ({"vulnerabilities":[...]}) converts and matches.
func TestNVD20Import(t *testing.T) {
	dir := t.TempDir()
	old := netdevStateDirOverr
	oldF := findingsDirOverr
	defer func() { netdevStateDirOverr = old; findingsDirOverr = oldF }()
	netdevStateDirOverr = dir
	findingsDirOverr = dir

	feed := `{"vulnerabilities":[
		{"cve":{
			"id":"CVE-2026-1001",
			"descriptions":[{"lang":"en","value":"Web UI auth bypass."}],
			"metrics":{"cvssMetricV31":[{"cvssData":{"baseSeverity":"CRITICAL"}}]},
			"configurations":[{"nodes":[{"cpe_match":[
				{"criteria":"cpe:2.3:o:cisco:ios_xe:16.9.1","vulnerable":true},
				{"criteria":"cpe:2.3:h:cisco:9300","vulnerable":true}
			]}]}]}},
		{"cve":{
			"id":"CVE-2026-1002",
			"descriptions":[{"lang":"en","value":"Buffer overflow."},{"lang":"zh","value":"x"}],
			"metrics":{"cvssMetricV2":[{"cvssData":{"baseSeverity":"MEDIUM"}}]},
			"configurations":[{"nodes":[{"cpe_match":[
				{"criteria":"cpe:2.3:o:huawei:vrp:*:*:*:*:*:*:*:*"}
			]}]}]}},
		{"cve":{
			"id":"CVE-2026-1003",
			"descriptions":[{"lang":"en","value":"No CPE here."}],
			"metrics":{"cvssMetricV31":[{"cvssData":{"baseSeverity":"HIGH"}}]}
		}}
	]}`
	n, err := ImportCVEFeed(feed)
	if err != nil || n != 2 {
		t.Fatalf("import: %v n=%d (CPE-less entry must be dropped)", err, n)
	}
	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "sw-1", Vendor: "cisco", OS: "ios xe", Model: "9300"},
		{Name: "gw-1", Vendor: "huawei", OS: "vrp8"},
	}
	matches, err := NewManager(cfg).MatchCVEs()
	if err != nil {
		t.Fatal(err)
	}
	// sw-1 hits 1001 (cisco/ios xe/9300), gw-1 hits 1002 (huawei/vrp)
	if len(matches) != 2 {
		t.Fatalf("matches: %+v", matches)
	}
	for _, h := range matches {
		if h.Severity != "critical" && h.Severity != "medium" {
			t.Fatalf("severity not normalized: %+v", h)
		}
	}
}

// NVD legacy 1.1 feed export ({"CVE_Items":[...]}) converts (V2 severity
// fallback, cpe23Uri field, non-en description skip).
func TestNVD11Import(t *testing.T) {
	dir := t.TempDir()
	old := netdevStateDirOverr
	defer func() { netdevStateDirOverr = old }()
	netdevStateDirOverr = dir

	feed := `{"CVE_Items":[
		{"cve":{"CVE_data_meta":{"ID":"CVE-2026-2001"},
			"description":{"description_data":[
				{"lang":"zh","value":"zh desc"},{"lang":"en","value":"English desc."}]}},
		 "impact":{"baseMetricV2":{"severity":"HIGH"}},
		 "configurations":{"nodes":[{"cpe_match":[
			{"cpe23Uri":"cpe:2.3:a:microsoft:windows_server_2019:-","vulnerable":true},
			{"cpe23Uri":"cpe:2.3:o:linux:linux_kernel:5.4","vulnerable":false}]}]}}
	]}`
	n, err := ImportCVEFeed(feed)
	if err != nil || n != 1 {
		t.Fatalf("import: %v n=%d", err, n)
	}
	raw, _ := os.ReadFile(cveFile())
	var f cveFeed
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	e := f.CVEs[0]
	if e.Desc != "English desc." || e.Severity != "high" {
		t.Fatalf("entry: %+v", e)
	}
	// vulnerable:false CPE excluded; windows product underscores → spaces
	joined := strings.Join(e.Products, ",")
	if !strings.Contains(joined, "windows server 2019") || strings.Contains(joined, "linux") {
		t.Fatalf("products: %v", e.Products)
	}
}

// cpeProducts edge cases: wildcards, NA, escapes, short fields.
func TestCPEProducts(t *testing.T) {
	for _, c := range []struct {
		cpe  string
		want []string
	}{
		{"cpe:2.3:o:cisco:ios_xe:16.9", []string{"cisco", "ios xe"}},
		{"cpe:2.3:o:-:something", []string{"something"}}, // NA vendor filtered, product survives
		{"cpe:2.3:o:*:*", nil},                            // wildcards filtered
		{"cpe:2.3:o:foo", nil},                            // too few fields
		{"not-a-cpe", nil},
	} {
		got := cpeProducts(c.cpe)
		if len(got) != len(c.want) {
			t.Fatalf("cpeProducts(%q) = %v, want %v", c.cpe, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("cpeProducts(%q) = %v, want %v", c.cpe, got, c.want)
			}
		}
	}
}

// The agent-facing tool: guidance when no feed, match lines when fed, and the
// empty-intersection hint.
func TestCVEMatchTool(t *testing.T) {
	dir := t.TempDir()
	old, oldF := netdevStateDirOverr, findingsDirOverr
	defer func() { netdevStateDirOverr = old; findingsDirOverr = oldF }()
	netdevStateDirOverr = dir
	findingsDirOverr = dir

	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "core-sw-1", Vendor: "cisco", OS: "iosxe", Model: "9300"},
	}
	tool := &cveMatchTool{m: NewManager(cfg)}

	// No feed → guidance, not an error.
	out, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("no-feed execute: %v", err)
	}
	if !strings.Contains(out, "安全工作台") {
		t.Fatalf("no-feed guidance missing import hint: %q", out)
	}

	// Fed → match line with device, id, product.
	if _, err := ImportCVEFeed(`{"cves":[{"id":"CVE-2026-0009","desc":"Web UI RCE","products":["cisco iosxe"],"severity":"critical"}]}`); err != nil {
		t.Fatal(err)
	}
	out, err = tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "core-sw-1") || !strings.Contains(out, "CVE-2026-0009") || !strings.Contains(out, "cisco iosxe") {
		t.Fatalf("match output incomplete: %q", out)
	}
	if !strings.Contains(out, "只读验证") {
		t.Fatalf("output must demand verification before filing: %q", out)
	}

	// Zero intersection → hint about fingerprint fields.
	cfg.NetDev.Devices = []config.NetDevDevice{{Name: "vm-1", Vendor: "linux"}}
	tool = &cveMatchTool{m: NewManager(cfg)}
	out, err = tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0 命中") {
		t.Fatalf("empty-intersection hint missing: %q", out)
	}
}
