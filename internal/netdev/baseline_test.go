package netdev

import (
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
