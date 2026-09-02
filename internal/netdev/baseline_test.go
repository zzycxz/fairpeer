package netdev

import (
	"path/filepath"
	"strings"
	"testing"
)

// The rule battery is a pure function over (already-redacted) config text:
// fixtures use the POST-redaction forms (secrets replaced by <redacted>) so
// the tests also pin the "rules work on redacted text" contract.
func TestCheckBaselineHuawei(t *testing.T) {
	bad := strings.Join([]string{
		"telnet server enable",
		"snmp-agent community read <redacted>",
		"local-user admin password simple <redacted>",
		"#",
	}, "\n")
	v := CheckBaseline("huawei-vrp", bad)
	ids := map[string]bool{}
	for _, x := range v {
		ids[x.Rule] = true
	}
	for _, want := range []string{"telnet-enabled", "snmp-v1v2c", "plaintext-password"} {
		if !ids[want] {
			t.Errorf("rule %q not hit (got %v)", want, ids)
		}
	}
	if ids["no-ntp"] || ids["no-syslog"] {
		// absence rules: this fixture lacks ntp/loghost so they DO fire;
		// guard the reverse case in the clean fixture below.
	}

	clean := strings.Join([]string{
		"undo telnet server enable",
		"snmp-agent usm-user v3 mon authentication-mode sha <redacted>",
		"local-user admin password irreversible-cipher <redacted>",
		"ntp-service unicast-server 10.0.0.253",
		"info-center loghost 10.0.0.250",
	}, "\n")
	v = CheckBaseline("huawei-vrp", clean)
	if len(v) != 0 {
		t.Fatalf("clean config produced violations: %+v", v)
	}
}

func TestCheckBaselineCisco(t *testing.T) {
	bad := strings.Join([]string{
		"line vty 0 4",
		" transport input telnet ssh",
		"snmp-server community <redacted> RO",
		"username ops password 0 <redacted>",
	}, "\n")
	v := CheckBaseline("cisco-ios", bad)
	ids := map[string]bool{}
	for _, x := range v {
		ids[x.Rule] = true
	}
	for _, want := range []string{"telnet-enabled", "snmp-v1v2c", "plaintext-password"} {
		if !ids[want] {
			t.Errorf("rule %q not hit (got %v)", want, ids)
		}
	}
}

// Unknown driver family → NO rules, not guessed ones (the accuracy bar).
func TestCheckBaselineUnknownFamilyNoRules(t *testing.T) {
	if v := CheckBaseline("zte-zxr10", "telnet server enable\n"); v != nil {
		t.Fatalf("zte must have no rules until syntax is verified, got %+v", v)
	}
}

// Full path against the sim: config read goes through the sealed Exec (audit
// + redaction) and findings land in the store.
func TestRunBaselineSim(t *testing.T) {
	m, _ := testManager(t, startSimDevice(t))
	f, err := m.RunBaseline(t.Context())
	if err != nil {
		t.Fatalf("RunBaseline: %v", err)
	}
	if f == nil || !strings.Contains(f.Title, "安全基线核查完成") {
		t.Fatalf("summary finding missing: %+v", f)
	}
}

// P2-3：基线发现的 Source 生命周期——重复核查更新同一告警（不堆积），
// 规则不再命中且全部受检设备已复核时自动 resolve；定向复核（只查部分
// 设备）不得 resolve 未复核设备的告警。
func TestReconcileBaselineFindings(t *testing.T) {
	dir := t.TempDir()
	orig := findingsDirOverr
	findingsDirOverr = dir
	t.Cleanup(func() { findingsDirOverr = orig })

	// An active telnet alert from a previous run.
	old := &Finding{
		Title: "基线：Telnet 开启", Severity: "warning",
		Devices: []string{"sw1", "sw2"}, Source: "baseline:telnet-enabled", Status: "active",
		Evidence: []Evidence{{Device: "sw1", Command: "display current-configuration", Output: "telnet server enable"}},
	}
	if err := SaveFinding(old); err != nil {
		t.Fatal(err)
	}

	// Re-run still hits telnet on sw1: same alert ID, updated, not duplicated.
	fresh := &Finding{Title: "基线：Telnet 开启", Severity: "warning", Devices: []string{"sw1"}}
	if err := reconcileBaselineFindings(map[string]*Finding{"telnet-enabled": fresh}, []string{"sw1", "sw2"}); err != nil {
		t.Fatal(err)
	}
	if fresh.ID != old.ID {
		t.Fatalf("re-hit must reuse the alert ID: %s vs %s", fresh.ID, old.ID)
	}
	fs, _ := ListFindings()
	if len(fs) != 1 {
		t.Fatalf("duplicate alerts piled up: %d", len(fs))
	}

	// Now telnet is fixed everywhere and both devices were re-checked → resolve.
	fixed := map[string]*Finding{}
	if err := reconcileBaselineFindings(fixed, []string{"sw1", "sw2"}); err != nil {
		t.Fatal(err)
	}
	fs, _ = ListFindings()
	if len(fs) != 1 || fs[0].Status != "resolved" {
		t.Fatalf("fixed rule not auto-resolved: %+v", fs)
	}

	// Scoped re-check that did NOT cover sw2 must not resolve a sw1+sw2 alert.
	old2 := &Finding{
		Title: "基线：SNMP v1/v2c", Severity: "warning",
		Devices: []string{"sw1", "sw2"}, Source: "baseline:snmp-v1v2c", Status: "active",
		Evidence: []Evidence{{Device: "sw1", Command: "display current-configuration", Output: "snmp-agent community read <redacted>"}},
	}
	if err := SaveFinding(old2); err != nil {
		t.Fatal(err)
	}
	if err := reconcileBaselineFindings(map[string]*Finding{}, []string{"sw1"}); err != nil {
		t.Fatal(err)
	}
	fs, _ = ListFindings()
	for _, f := range fs {
		if f.Source == "baseline:snmp-v1v2c" && f.Status == "resolved" {
			t.Fatal("scoped re-check resolved an alert whose device was not re-verified")
		}
	}
}

// 滚动汇总：巡检/基线的 info 级汇总 Source 化后每次运行更新同一张卡，
// 且不受 baseline reconcile 的"不再命中即 resolve"影响。
func TestRollingSummaryFindings(t *testing.T) {
	findingsDirOverr = filepath.Join(t.TempDir(), "findings")
	t.Cleanup(func() { findingsDirOverr = "" })

	f1 := &Finding{Title: "安全基线核查完成：2 台受检 / 2 台，命中 1 项", Severity: "info",
		Devices:  []string{"sw1", "sw2"},
		Evidence: []Evidence{{Device: "sw1", Command: "display current-configuration", Output: "ok"}},
		Source:   "baseline:summary"}
	if err := SaveRollingFinding(f1); err != nil {
		t.Fatal(err)
	}
	f2 := &Finding{Title: "安全基线核查完成：2 台受检 / 2 台，命中 0 项", Severity: "info",
		Devices:  []string{"sw1", "sw2"},
		Evidence: []Evidence{{Device: "sw1", Command: "display current-configuration", Output: "ok"}},
		Source:   "baseline:summary"}
	if err := SaveRollingFinding(f2); err != nil {
		t.Fatal(err)
	}
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].ID != f1.ID || fs[0].Title != f2.Title {
		t.Fatalf("rolling summary not updated in place: %+v", fs)
	}
	// reconcile (empty hits, all devices checked) must NOT resolve the summary card.
	if err := reconcileBaselineFindings(map[string]*Finding{}, []string{"sw1", "sw2"}); err != nil {
		t.Fatal(err)
	}
	fs, _ = ListFindings()
	if fs[0].Status == "resolved" {
		t.Fatal("summary card got auto-resolved by the rule reconciler")
	}
}
