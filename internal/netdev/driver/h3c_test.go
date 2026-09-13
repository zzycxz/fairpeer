package driver

import "testing"

// H3C Comware driver coverage — the domestic-fleet vendor (NETDEV_USAGE §G).
// A wrong Classify entry here means either an unsafe exec on a real switch or
// a false refusal on a read-only diagnostic, so the tables are pinned like the
// huawei/cisco/zte siblings.

func TestH3CComwareClassify(t *testing.T) {
	drv, ok := For("h3c", "comware7")
	if !ok {
		t.Fatal("h3c driver not found")
	}
	runClassCases(t, drv, []classCase{
		{"display version", Read},
		{"display interface brief", Read},
		{"  DISPLAY   interface  description ", Read},
		{"display current-configuration", Read},
		{"screen-length disable", Read},
		{"ping 10.1.1.1", Read},
		{"tracert 10.1.1.1", Read},
		{"quit", Read},
		{"return", Read},
		{"dir flash:/", Read},
		{"system-view", Write},
		{"undo stp enable", Write},
		{"vlan 10", Write},
		{"interface GigabitEthernet1/0/1", Write},
		{"ip route-static 0.0.0.0 0 10.0.0.1", Write},
		{"local-user netops class manage", Write},
		{"snmp-agent community read public", Write},
		{"save force", Write},
		{"clear counters", Write},
		// "ip" is word-seamed: it must NOT swallow "ipv6"-led commands…
		{"ipv6 enable", Write},
		// …and "display ipv6 interface" stays read via the display prefix.
		{"display ipv6 interface brief", Read},
		{"reboot", Dangerous},
		{"delete /unreserved flash:/old.cfg", Dangerous},
		{"reset saved-configuration", Dangerous},
		{"reset factory-configuration", Dangerous},
		{"boot-loader file flash:/app.bin", Dangerous},
		{"format flash:", Dangerous},
		{"displayclock", Unknown},
		{"mystery verb", Unknown},
		{"", Unknown},
	})
}

func TestH3CComwarePromptFixtures(t *testing.T) {
	drv, _ := For("h3c", "comware7")
	// user view, system view, and named views incl. interface/vlan/aaa contexts.
	for _, prompt := range []string{
		"<H3C>",
		"<CORE-SW-01>",
		"[H3C]",
		"[CORE-SW-GigabitEthernet1/0/1]",
		"[CORE-SW-vlan10]",
		"[CORE-SW-aaa]",
	} {
		if !drv.Prompt().MatchString("\n" + prompt + " ") {
			t.Errorf("h3c prompt %q not matched", prompt)
		}
	}
	// Trailing text after the bracket must NOT match (anchored end-of-line).
	if drv.Prompt().MatchString("\n[H3C] aaa session-limit") {
		t.Error("h3c prompt matched mid-command output (should be end-anchored)")
	}
}

func TestH3CComwareErrorFixtures(t *testing.T) {
	drv, _ := For("h3c", "comware7")
	for _, line := range []string{
		// Covers each Errors() alternative: the canonical Comware shapes and
		// the Chinese-language variants firmware localizations emit.
		"% Unrecognized command found at '^' position.",
		"% Wrong parameter found at '^' position.",
		"% Incomplete command found at '^' position.",
		"%命令无法识别",
		"Too many parameters",
	} {
		matched := false
		for _, re := range drv.Errors() {
			if re.MatchString(line) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("h3c error line %q not matched", line)
		}
	}
}

// resolveKey maps the bare vendor name onto the driver, including the empty-OS
// default (settings UI saves vendor without os for snmp-only rows).
func TestH3CResolvesVariants(t *testing.T) {
	for _, osName := range []string{"comware7", "comware5", ""} {
		if _, ok := For("h3c", osName); !ok {
			t.Errorf("h3c/%q did not resolve to a driver", osName)
		}
	}
	if _, ok := For("ruijie", "rgos"); !ok {
		t.Error("ruijie/rgos did not resolve (expected the cisco-ios mapping)")
	}
}
