package driver

import "regexp"

// huaweiVRP drives Huawei VRP (VRP5/VRP8: S/CE/AR/USG families). P1 coverage is
// the S-series switch CLI; CE/USG quirks ride in the tables as fixtures
// accumulate (NETDEV_SPEC §4.1).
type huaweiVRP struct{}

func (huaweiVRP) Key() string { return "huawei-vrp" }

func (huaweiVRP) PagingOff() []string {
	return []string{"screen-length 0 temporary"}
}

// Prompt: `<hostname>` in user view, `[~hostname]`/`[hostname]` in system and
// config views (VRP8 core/eth-trunk/etc. views nest brackets inside).
func (huaweiVRP) Prompt() *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\n)(?:<[A-Za-z0-9._@/-]{1,64}>|\[~?[A-Za-z0-9._@/-]{1,64}(?:-[A-Za-z0-9._/-]+)*\]) ?$`)
}

func (huaweiVRP) Errors() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*(?:Error|错误)[:：]`),
		regexp.MustCompile(`(?i)^Error at`),
		regexp.MustCompile(`(?i)^\s*% (?:Unrecognized|Unknown|Incomplete|Ambiguous|Invalid|Wrong) `),
		regexp.MustCompile(`(?i)%(?:Unrecognized|Unknown|Incomplete|Ambiguous|Invalid|Wrong)\s+command`),
		regexp.MustCompile(`(?i)命令错误|未知的命令|错误命令`),
		regexp.MustCompile(`(?i)^Too many parameters|Incomplete command`),
	}
}

var huaweiTables = classTables{
	dangerous: []string{
		"reboot", "reset saved-configuration", "reset factory-configuration",
		"format", "delete", "delete /unreserved", "load", "update", "patch delete",
		"startup saved-configuration", "undo startup saved-configuration",
	},
	write: []string{
		"system-view",
		"undo", "set", "vlan", "interface", "port", "port-group", "link-group",
		"stp", "loopback-detect", "aaa", "local-user", "domain", "snmp-agent",
		"telnet server", "ssh server", "ssh user", "user-interface", "acl",
		"traffic", "route-policy", "ip route-static", "ospf", "bgp", "isis",
		"dhcp", "nat", "security-policy", "firewall", "arp", "mac-address",
		"ntp-service", "radius", "tacacs", "netconf", "commit", "save",
		"clear", "debugging", "monitor", "capture", "info-center", "sysname",
		"header", "super password", "password", "rsa local-key-pair", "dsa",
		"irf", "stack", "mesh", "cfm", "lacp", "eth-trunk", "service-manager",
	},
	read: []string{
		"display", "show", "ping", "tracert", "traceroute", "ping -a", "ping -c",
		"screen-length", "language-mode", "quit", "return", "help",
		"terminal monitor", "dns resolve", "nslookup", "verify",
	},
}

func (huaweiVRP) Classify(cmd string) Class { return huaweiTables.classify(cmd) }

// ciscoIOS drives Cisco IOS / IOS-XE. NX-OS/ASA are NOT covered by this
// driver's tables (different command families); they stay Unknown until they
// get their own entries.
type ciscoIOS struct{}

func (ciscoIOS) Key() string { return "cisco-ios" }

func (ciscoIOS) PagingOff() []string {
	return []string{"terminal length 0", "terminal width 511"}
}

// Prompt: "host>", "host#", "host(config)#", "host(config-if)#".
func (ciscoIOS) Prompt() *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\n)[A-Za-z0-9._-]{1,64}(?:\([A-Za-z0-9._-]{1,48}\))?[>#] ?$`)
}

func (ciscoIOS) Errors() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*% (?:Invalid input|Incomplete command|Ambiguous command|Unknown command)`),
		regexp.MustCompile(`(?i)^% Bad IP address|^% Unrecognized host`),
		regexp.MustCompile(`(?i)% Invalid input detected`),
		regexp.MustCompile(`(?i)^Command authorization failed`),
	}
}

var ciscoTables = classTables{
	dangerous: []string{
		"reload", "write erase", "erase startup-config", "erase flash",
		"delete", "format flash", "factory-reset",
		"license boot", "boot system flash", "no boot system",
	},
	write: []string{
		"configure terminal", "conf t", "configure", "interface", "no",
		"default", "vlan", "router", "ip", "ipv6", "switchport", "spanning-tree",
		"cdp run", "no cdp", "lldp", "line", "username", "enable secret",
		"enable password", "service password", "aaa", "access-list", "ip access-list",
		"route-map", "policy-map", "class-map", "crypto", "ssh", "ip ssh",
		"telnet", "snmp-server", "logging", "ntp", "banner", "hostname",
		"copy", "write memory", "write", "clear", "debug", "monitor capture",
		"archive", "scheduler", "kron", "track", "standby", "vrrp", "hsrp",
		"bfd", "port-channel", "etherchannel", "power", "shutdown",
	},
	read: []string{
		"show", "ping", "traceroute", "trace", "terminal length",
		"terminal width", "dir", "more", "exit", "end", "quit",
		"where", "help",
	},
}

func (ciscoIOS) Classify(cmd string) Class { return ciscoTables.classify(cmd) }

func init() {
	register(huaweiVRP{})
	register(ciscoIOS{})
	register(vmwareESXi{})
}
