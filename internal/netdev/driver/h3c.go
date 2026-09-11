package driver

import "regexp"

// h3cComware drives H3C Comware (S/MSR/ER families, Comware 7). The CLI is
// Huawei-VRP-adjacent (`display` instead of `show`, `undo` instead of `no`)
// with Cisco-flavored config verbs in places. Tables start with the S-series
// switch surface and grow with fixtures (NETDEV_SPEC §4.1: quirk-table
// evolution). Comware 5's `screen-length 0 temporary` spelling is NOT in
// PagingOff on purpose: paging-off failures abort the session open, so the
// table rides the Comware 7 syntax and the runtime `---- More ----` pager
// fallback covers the rest.
type h3cComware struct{}

func (h3cComware) Key() string { return "h3c-comware" }

func (h3cComware) PagingOff() []string {
	return []string{"screen-length disable"}
}

// Prompt: `<hostname>` in user view, `[hostname]`/`[hostname-IfGE1/0/1]` in
// system and named views (Comware nests context suffixes inside the brackets).
func (h3cComware) Prompt() *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\n)(?:<[A-Za-z0-9._@/-]{1,64}>|\[[A-Za-z0-9._@/-]{1,64}(?:-[A-Za-z0-9._/-]+)*\]) ?$`)
}

func (h3cComware) Errors() []*regexp.Regexp {
	return []*regexp.Regexp{
		// `% Unrecognized command found at '^' position.` and siblings are the
		// canonical Comware error shapes.
		regexp.MustCompile(`(?i)^\s*%\s*(?:Unrecognized|Incomplete|Ambiguous|Wrong|Invalid)\s+(?:command|parameter)`),
		regexp.MustCompile(`(?i)found at '\^' position`),
		regexp.MustCompile(`(?i)命令无法识别|命令不完整|参数错误|错误的命令|无效的命令|存在歧义`),
		regexp.MustCompile(`(?i)^Too many parameters`),
	}
}

var h3cTables = classTables{
	driverKey: "h3c-comware",
	dangerous: []string{
		"reboot", "reset saved-configuration", "reset factory-configuration",
		"format", "delete", "delete /unreserved", "boot-loader",
		"startup", "undo startup", "password", "restore factory",
	},
	write: []string{
		"system-view",
		"undo", "vlan", "interface", "port", "port-group", "link-aggregation",
		"bridge-aggregation", "stp", "local-user", "user-interface", "domain",
		"ssh server", "ssh user", "telnet server", "snmp-agent", "ntp-service",
		"acl", "qos", "traffic", "policy", "route-policy", "ip route-static",
		"ip", "ipv6", "ospf", "bgp", "isis", "vrrp", "dhcp", "nat", "arp",
		"mac-address", "info-center", "sysname", "header", "super password",
		"radius", "tacacs", "netconf", "irf", "lldp", "mirroring", "mirror",
		"save", "copy", "clear", "debugging", "monitor", "capture",
	},
	read: []string{
		"display", "ping", "tracert", "screen-length", "screen-width",
		"more", "dir", "quit", "return", "exit", "end", "help",
		"language-mode", "nslookup", "verify",
	},
}

func (h3cComware) Classify(cmd string) Class { return h3cTables.classify(cmd) }

func init() { register(h3cComware{}) }
