package driver

import "regexp"

// zteZXR10 drives ZTE ZXR10 (59/88/89/5900E/5960/9900 series). ZXR10's CLI is
// Cisco-like (show / configure terminal / hostname# prompts) with set-style
// verbs in places, so the tables lean conservative: anything unrecognized
// stays Unknown (write-handled). Public documentation for ZXR10 is the
// scarcest of the three vendors — the tables start narrow and grow with
// fixtures (NETDEV_SPEC §4.1: P2 arrival, quirk-table evolution).
type zteZXR10 struct{}

func (zteZXR10) Key() string { return "zte-zxr10" }

func (zteZXR10) PagingOff() []string {
	return []string{"terminal length 0", "terminal width 512"}
}

// Prompt: "ZXR10>", "ZXR10#", "ZXR10(config)#", "zxr(config-if)#".
func (zteZXR10) Prompt() *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\n)[A-Za-z0-9._-]{1,64}(?:\([A-Za-z0-9._/-]{1,48}\))?[>#] ?$`)
}

func (zteZXR10) Errors() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*%?\s*(?:Invalid|Incomplete|Ambiguous|Unrecognized|Unknown)\s+(?:input\s+)?(?:command|detected|detected at)`),
		regexp.MustCompile(`(?i)^%Invalid input detected at`),
		regexp.MustCompile(`(?i)^%?Error[:：]`),
		regexp.MustCompile(`(?i)命令错误|错误的命令|不完整的命令|无效的输入`),
		regexp.MustCompile(`(?i)^Error: Wrong parameter|Incomplete command`),
	}
}

var zteTables = classTables{
	driverKey: "zte-zxr10",
	dangerous: []string{
		"reboot", "reload", "delete", "rm", "format", "erase",
		"erase startup-config", "write erase", "factory", "reset",
		"startup", "load", "update",
	},
	write: []string{
		"configure terminal", "conf t", "configure", "interface", "no",
		"set", "default", "vlan", "router", "ip", "ipv6", "line",
		"username", "enable secret", "enable password", "aaa",
		"access-list", "snmp-server", "snmp", "logging", "ntp", "banner",
		"hostname", "copy", "write", "clear", "debug", "monitor capture",
		"smartgroup", "lacp", "spanning-tree", "stp", "link-aggregation",
		"dhcp", "nat", "security", "firewall", "qos", "policy-map",
		"class-map", "route-map", "track", "vrrp", "shutdown", "commit",
		"save", "quit-view",
	},
	read: []string{
		"show", "ping", "traceroute", "trace", "terminal length",
		"terminal width", "dir", "more", "exit", "end", "quit",
		"help", "logging monitor",
	},
}

func (zteZXR10) Classify(cmd string) Class { return zteTables.classify(cmd) }

func init() { register(zteZXR10{}) }
