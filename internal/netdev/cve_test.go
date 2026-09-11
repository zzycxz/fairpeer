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
	// cisco hits cve-0001 (iosxe); huawei hits cve-0002 (vrp); linux none
	if len(matches) != 2 {
		t.Fatalf("matches: %+v", matches)
	}
	byDev := map[string]string{}
	for _, m := range matches {
		byDev[m.Device] = m.CVEID
	}
	if byDev["core-sw-1"] != "CVE-2026-0001" || byDev["gw-1"] != "CVE-2026-0002" {
		t.Fatalf("device×CVE distribution drifted: %+v", byDev)
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

// Rolling sweep: repeated MatchCVEsToFindings runs update the ONE cve:sweep
// finding in place (same id, same raise time) instead of piling duplicates —
// pinned after 2026-09-08 found SaveFinding piling a new card per sweep.
func TestCVESweepRolling(t *testing.T) {
	dir := t.TempDir()
	old := netdevStateDirOverr
	oldF := findingsDirOverr
	defer func() { netdevStateDirOverr = old; findingsDirOverr = oldF }()
	netdevStateDirOverr = dir
	findingsDirOverr = dir

	feed := `{"cves":[{"id":"CVE-2026-0003","desc":"vrp flaw","products":["vrp"],"severity":"critical"}]}`
	if _, err := ImportCVEFeed(feed); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{{Name: "gw-1", Vendor: "huawei", OS: "vrp8", Model: "AR6300"}}
	m := NewManager(cfg)

	f1, err := m.MatchCVEsToFindings()
	if err != nil {
		t.Fatal(err)
	}
	f2, err := m.MatchCVEsToFindings()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ListFindings()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("repeated sweeps must keep ONE rolling finding, got %d", len(got))
	}
	if got[0].ID != f1.ID || f2.ID != f1.ID {
		t.Fatalf("rolling id must be stable: f1=%s f2=%s list=%s", f1.ID, f2.ID, got[0].ID)
	}
	if !got[0].CreatedAt.Equal(f1.CreatedAt) {
		t.Fatalf("rolling raise time must stay at first sweep: %v vs %v", got[0].CreatedAt, f1.CreatedAt)
	}
}

// Import merges by CVE-ID (delta exports accumulate; same-ID new wins) and
// ClearCVEFeed is the only removal path (idempotent).
func TestCVEImportMergeAndClear(t *testing.T) {
	dir := t.TempDir()
	old := netdevStateDirOverr
	defer func() { netdevStateDirOverr = old }()
	netdevStateDirOverr = dir

	if _, err := ImportCVEFeed(`{"cves":[
		{"id":"CVE-2026-0100","desc":"old text","products":["vrp"],"severity":"high"},
		{"id":"CVE-2026-0101","desc":"stays","products":["ios"],"severity":"low"}
	]}`); err != nil {
		t.Fatal(err)
	}
	// Delta window: updates 0100, adds 0102 — 0101 must survive.
	if n, err := ImportCVEFeed(`{"cves":[
		{"id":"CVE-2026-0100","desc":"new text","products":["vrp"],"severity":"critical"},
		{"id":"CVE-2026-0102","desc":"added","products":["junos"],"severity":"high"}
	]}`); err != nil || n != 2 {
		t.Fatalf("delta import: %v n=%d", err, n)
	}
	b, err := os.ReadFile(cveFile())
	if err != nil {
		t.Fatal(err)
	}
	var feed cveFeed
	if err := json.Unmarshal(b, &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.CVEs) != 3 {
		t.Fatalf("merge must union to 3 entries, got %d", len(feed.CVEs))
	}
	byID := map[string]CVEEntry{}
	for _, e := range feed.CVEs {
		byID[e.ID] = e
	}
	if byID["CVE-2026-0100"].Desc != "new text" || byID["CVE-2026-0100"].Severity != "critical" {
		t.Fatalf("same-ID entry must be replaced by the new one: %+v", byID["CVE-2026-0100"])
	}
	if byID["CVE-2026-0101"].Desc != "stays" {
		t.Fatal("non-overlapping entry must survive the merge")
	}

	if err := ClearCVEFeed(); err != nil {
		t.Fatal(err)
	}
	if err := ClearCVEFeed(); err != nil {
		t.Fatalf("clear must be idempotent: %v", err)
	}
	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{{Name: "gw-1", Vendor: "huawei", OS: "vrp8", Model: "AR6300"}}
	if _, err := NewManager(cfg).MatchCVEs(); err == nil {
		t.Fatal("match after clear must report no feed")
	}
}

// Zero-hit sweep and feed clear both close the rolling card's lifecycle: the
// old hit finding must not stay active forever (alert auto-resolve semantics).
func TestCVESweepResolveOnZero(t *testing.T) {
	dir := t.TempDir()
	old := netdevStateDirOverr
	oldF := findingsDirOverr
	defer func() { netdevStateDirOverr = old; findingsDirOverr = oldF }()
	netdevStateDirOverr = dir
	findingsDirOverr = dir

	if _, err := ImportCVEFeed(`{"cves":[{"id":"CVE-2026-0200","desc":"vrp flaw","products":["vrp"],"severity":"critical"}]}`); err != nil {
		t.Fatal(err)
	}
	hit := &config.Config{}
	hit.NetDev.Devices = []config.NetDevDevice{{Name: "gw-1", Vendor: "huawei", OS: "vrp8", Model: "AR6300"}}
	if _, err := NewManager(hit).MatchCVEsToFindings(); err != nil {
		t.Fatal(err)
	}
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Status != "active" {
		t.Fatalf("hit sweep must file one active card: %+v", fs)
	}

	// All devices fixed/replaced → zero-hit re-sweep resolves the card.
	fixed := &config.Config{}
	fixed.NetDev.Devices = []config.NetDevDevice{{Name: "gw-1", Vendor: "cisco", OS: "iosxe", Model: "9300"}}
	f, err := NewManager(fixed).MatchCVEsToFindings()
	if err != nil || !strings.Contains(f.Title, "无命中") {
		t.Fatalf("zero-hit sweep: %v %+v", err, f)
	}
	fs, _ = ListFindings()
	if len(fs) != 1 || fs[0].Status != "resolved" || fs[0].ResolvedAt == nil {
		t.Fatalf("zero-hit sweep must resolve the rolling card: %+v", fs)
	}

	// Manual resolve path (feed clear) is idempotent on an already-resolved card.
	NewManager(fixed).ResolveCVESweep()
	fs, _ = ListFindings()
	if len(fs) != 1 {
		t.Fatalf("resolve must not add cards: %d", len(fs))
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
		{"cpe:2.3:o:*:*", nil},                           // wildcards filtered
		{"cpe:2.3:o:foo", nil},                           // too few fields
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

// 批 B golden：CPE 版本区间的边界判定（含闭开端与尾部零填充）。
func TestVersionInRange(t *testing.T) {
	cases := []struct {
		v    string
		r    CPEVersionRange
		want bool
	}{
		{"15.2", CPEVersionRange{StartIncl: "15.1", EndIncl: "15.6"}, true},
		{"15.1", CPEVersionRange{StartIncl: "15.1", EndIncl: "15.6"}, true},  // 闭端含起
		{"15.6", CPEVersionRange{StartIncl: "15.1", EndIncl: "15.6"}, true},  // 闭端含止
		{"15.1", CPEVersionRange{StartExcl: "15.1", EndExcl: "15.6"}, false}, // 开端排起
		{"15.6", CPEVersionRange{StartExcl: "15.1", EndExcl: "15.6"}, false}, // 开端排止
		{"15.2", CPEVersionRange{StartIncl: "15.2.0"}, true},                 // 尾部零填充
		{"15.2(4)M", CPEVersionRange{StartIncl: "15.2", EndExcl: "16.0"}, true},
		{"7.0.4", CPEVersionRange{EndExcl: "7.0.4"}, false},
		{"15.2", CPEVersionRange{}, true}, // 无边界 = 全区间
		{"abc", CPEVersionRange{StartIncl: "1.0"}, false},
		{"15.2", CPEVersionRange{StartIncl: "-"}, true}, // 无数字边界 = 该侧无界（CPE "-" 通写法）
		{"15.2", CPEVersionRange{EndExcl: "-"}, true},   // 无数字边界 = 该侧无界（全无界区间）
	}
	for _, c := range cases {
		if got := versionInRange(c.v, c.r); got != c.want {
			t.Errorf("versionInRange(%q, %+v) = %v, want %v", c.v, c.r, got, c.want)
		}
	}
}

// 批 B：NVD 导入携带版本边界，匹配按 三态 分类——in_range / out_of_range
// （保留供核对）/ unverified（无版本或无边界，行为与批 B 前一致）。
func TestCVEMatchVersionStatus(t *testing.T) {
	dir := t.TempDir()
	old := netdevStateDirOverr
	defer func() { netdevStateDirOverr = old }()
	netdevStateDirOverr = dir

	feed := `{"cves":[
		{"id":"CVE-2026-2001","desc":"iosxe flaw","products":["iosxe"],"severity":"critical",
		 "versions":[{"start_incl":"15.1","end_excl":"16.0"}]},
		{"id":"CVE-2026-2002","desc":"no bounds","products":["iosxe"],"severity":"high"}
	]}`
	if _, err := ImportCVEFeed(feed); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "sw-in", Vendor: "cisco", OS: "iosxe", Model: "C9300 IOS 15.2(4)M"},
		{Name: "sw-out", Vendor: "cisco", OS: "iosxe", Model: "C9300 running 17.3.2"},
		{Name: "sw-plain", Vendor: "cisco", OS: "iosxe", Model: "C9300"},
	}
	matches, err := NewManager(cfg).MatchCVEs()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]string{} // device → cve → status
	for _, m := range matches {
		if got[m.Device] == nil {
			got[m.Device] = map[string]string{}
		}
		got[m.Device][m.CVEID] = m.VersionStatus
	}
	if got["sw-in"]["CVE-2026-2001"] != VersionInRange {
		t.Fatalf("15.2 in [15.1,16.0) must be in_range: %+v", got)
	}
	// 版本提取钉死：带维护号（15.2(4)）；model 混杂时取最左点分数。
	for _, m := range matches {
		if m.Device == "sw-in" && m.DeviceVersion != "15.2(4)" {
			t.Fatalf("device version must carry the maintenance suffix, got %q", m.DeviceVersion)
		}
		if m.Device == "sw-out" && m.DeviceVersion != "17.3.2" {
			t.Fatalf("17.3.2 must extract the full dotted run, got %q", m.DeviceVersion)
		}
	}
	if got["sw-out"]["CVE-2026-2001"] != VersionOutOfRange {
		t.Fatalf("17.3 in [15.1,16.0) must be out_of_range (kept, not dropped): %+v", got)
	}
	if got["sw-plain"]["CVE-2026-2001"] != VersionUnverified {
		t.Fatalf("model without dotted version must be unverified: %+v", got)
	}
	if got["sw-in"]["CVE-2026-2002"] != VersionUnverified || got["sw-out"]["CVE-2026-2002"] != VersionUnverified {
		t.Fatalf("bounds-less feed entries stay unverified for everyone: %+v", got)
	}

	// NVD 原生导出也提取边界。
	nvd := `{"vulnerabilities":[{"cve":{
		"id":"CVE-2026-2003",
		"descriptions":[{"lang":"en","value":"ranged flaw."}],
		"metrics":{"cvssMetricV31":[{"cvssData":{"baseSeverity":"HIGH"}}]},
		"configurations":[{"nodes":[{"cpe_match":[
			{"criteria":"cpe:2.3:o:cisco:ios_xe:*","vulnerable":true,
			 "versionStartIncluding":"15.1","versionEndExcluding":"16.0"}
		]}]}]}}]}`
	if _, err := ImportCVEFeed(nvd); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cveFile())
	var f cveFeed
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	for _, e := range f.CVEs {
		if e.ID == "CVE-2026-2003" {
			if len(e.Versions) != 1 || e.Versions[0].StartIncl != "15.1" || e.Versions[0].EndExcl != "16.0" {
				t.Fatalf("NVD bounds must round-trip: %+v", e.Versions)
			}
			return
		}
	}
	t.Fatal("CVE-2026-2003 missing from cache")
}
