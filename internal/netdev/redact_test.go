package netdev

import (
	"strings"
	"testing"
)

// Fixtures model real configuration lines from `display current-configuration`
// (Huawei VRP) and `show running-config` (Cisco IOS). Only the secret token
// must be masked; structure stays for the model to reason about.
func TestRedactHuaweiConfig(t *testing.T) {
	in := strings.Join([]string{
		"#",
		"aaa",
		" authentication-scheme default",
		" local-user admin password cipher %^%#8KjX2mQzV+7pLtO3nRq9wA==%^%#",
		" local-user admin privilege level 15",
		" local-user netops password irreversible-cipher $1a$YhT9xKq2mVv7LpZ3nQ==",
		"snmp-agent",
		" snmp-agent community read %#Public_R0!",
		" snmp-agent community write Private_W1#",
		" snmp-agent usm-user v3 snmpv3user authentication-mode sha %^%#AuthKeySecret%^%#",
		"radius-server template rad1",
		" radius-server shared-key cipher VerySecretRad123",
		" ssh user admin authentication-type password",
		"interface Vlanif1",
		" ip address 10.0.0.1 255.255.255.0",
	}, "\n")

	out := Redact(in)
	for _, leak := range []string{
		"%^%#8KjX2mQzV+7pLtO3nRq9wA==%^%#",
		"$1a$YhT9xKq2mVv7LpZ3nQ==",
		"%#Public_R0!",
		"Private_W1#",
		"%^%#AuthKeySecret%^%#",
		"VerySecretRad123",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("secret leaked: %q\noutput:\n%s", leak, out)
		}
	}
	// Structure preserved: the model still sees what the line IS.
	for _, keep := range []string{
		"local-user admin password cipher",
		"local-user netops password irreversible-cipher",
		"snmp-agent community read",
		"radius-server shared-key cipher",
		"ip address 10.0.0.1",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("structure lost: %q\noutput:\n%s", keep, out)
		}
	}
	if strings.Count(out, redactedToken) < 6 {
		t.Errorf("expected at least 6 redactions, got %d\n%s", strings.Count(out, redactedToken), out)
	}
}

func TestRedactCiscoConfig(t *testing.T) {
	in := strings.Join([]string{
		"!",
		"enable secret 5 $1$mERr$UHfdDFerQhK4ZbAqkO0oQ.",
		"enable password 7 0822455D0A16",
		"username admin privilege 15 password 7 094F471A1A0A",
		"username opsecret secret 5 $1$KjLm$xyzhashvaluehere",
		"snmp-server community RO-publicStr RO",
		"snmp-server community RW-privateStr RW",
		"key config-key password-encrypt KeYsTrInG123",
		"key chain KC1",
		" key 1",
		"  key-string 7 12090404011C03162E",
		"radius-server host 10.1.1.5 auth-port 1812 key 7 SharedRadSecret",
		"interface GigabitEthernet0/1",
		" ip address 10.0.0.2 255.255.255.0",
		"crypto isakmp policy 10",
		" pre-shared-key address 10.2.0.1 key PskValue!23",
	}, "\n")

	out := Redact(in)
	for _, leak := range []string{
		"$1$mERr$UHfdDFerQhK4ZbAqkO0oQ.",
		"0822455D0A16",
		"094F471A1A0A",
		"$1$KjLm$xyzhashvaluehere",
		"RO-publicStr",
		"RW-privateStr",
		"KeYsTrInG123",
		"12090404011C03162E",
		"SharedRadSecret",
		"PskValue!23",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("secret leaked: %q\noutput:\n%s", leak, out)
		}
	}
	for _, keep := range []string{
		"enable secret 5",
		"username admin privilege 15 password 7",
		"snmp-server community",
		"key-string 7",
		"ip address 10.0.0.2",
		"pre-shared-key address 10.2.0.1",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("structure lost: %q\noutput:\n%s", keep, out)
		}
	}
}

// Non-secret lines must pass through byte-identical: a redactor that mangles
// ordinary output would break diagnosis more than it protects.
func TestRedactLeavesPlainOutputIntact(t *testing.T) {
	cases := []string{
		"display version",
		"Huawei Versatile Routing Platform Software",
		" Line protocol state : UP",
		" OSPF process 100 with Router ID 10.0.0.1",
		" CPU Usage: 12.3%   Memory Usage: 45%",
		" interface GigabitEthernet0/0/1 current state : up",
		" Gateway of last resort is 10.0.0.254 to network 0.0.0.0",
	}
	for _, c := range cases {
		if got := Redact(c); got != c {
			t.Errorf("plain line mangled:\n in: %q\ngot: %q", c, got)
		}
	}
}
