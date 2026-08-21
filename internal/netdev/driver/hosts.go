package driver

import "regexp"

// ── Linux server shell ──────────────────────────────────────────────────────
//
// Servers are network members too (the diagnosis' other half of evidence),
// but a server shell is UNBOUNDED where a network CLI is a closed verb set:
// the read table below is deliberately narrow (network/system diagnostics with
// argument-scoped cat/grep/tail), unknown verbs refuse, and Exec additionally
// rejects shell metacharacters for shell drivers (see metacharDrivers) — a
// pipe or `;` in "ps aux | sh -c 'reboot'" would otherwise smuggle unexamined
// commands past the word-prefix classifier into the PTY.
type linuxShell struct{}

func (linuxShell) Key() string { return "linux-shell" }

func (linuxShell) PagingOff() []string { return nil } // non-interactive; no pager

// Prompt: `user@host:~$ ` / `root@host:~# ` (Debian family) or bare `# `/`$ `.
func (linuxShell) Prompt() *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\n)[A-Za-z0-9._@-]{1,64}@[A-Za-z0-9._-]{1,64}:[^\n]{0,80}[$#] ?$|(?:^|\n)\$ ?$|(?:^|\n)# ?$`)
}

func (linuxShell) Errors() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*(?:bash|sh|ash|dash): .*: (?:command not found|No such file or directory|Permission denied)`),
		regexp.MustCompile(`(?i)^\s*(?:Command|命令) (?:not found|未找到)`),
		regexp.MustCompile(`(?i)^\s*Usage: `),
		regexp.MustCompile(`(?i)^\s*(?:unknown|invalid|bad) option`),
	}
}

var linuxTables = classTables{
	dangerous: []string{
		"reboot", "shutdown", "halt", "poweroff", "init ", "systemctl isolate",
		"killall", "pkill", "mkfs", "mkfs.ext", "mkfs.xfs", "fdisk", "parted",
		"dd ", "userdel", "usermod", "passwd", "visudo", "crontab -r",
		"iptables -F", "iptables -X", "iptables-restore", "nft flush", "rm ", "chmod 777 /",
	},
	write: []string{
		"systemctl start", "systemctl stop", "systemctl restart", "systemctl reload",
		"systemctl enable", "systemctl disable", "systemctl mask", "systemctl unmask",
		"systemctl daemon-reload", "systemctl kill", "systemctl set-default",
		"service ", "ip addr add", "ip addr del", "ip link set", "ip route add", "ip route del",
		"ip neigh del", "ip rule add", "ifup", "ifdown", "ethtool -s", "brctl addif", "brctl delif",
		"brctl addbr", "brctl delbr", "nmcli con", "nmcli device", "useradd", "groupadd",
		"apt install", "apt-get install", "apt remove", "apt-get remove", "yum install", "dnf install",
		"echo ", "printf ", "tee ", "sed -i", "sed --in-place", "cp ", "mv ", "ln -s",
		"mount ", "umount", "swapon", "swapoff", "sysctl -w", "sysctl --system",
		"iptables ", "iptables ", "nft add", "nft insert", "nft delete", "firewall-cmd --add",
		"firewall-cmd --remove", "ufw enable", "ufw disable", "ufw allow", "ufw deny",
		"tc qdisc", "modprobe", "rmmod", "chown ", "chmod ", "setenforce", "hostnamectl set",
		"date -s", "timedatectl set", "systemd-analyze set", "logger ", "wall ",
	},
	read: []string{
		// network diagnostics
		"ip addr", "ip a", "ip link", "ip l", "ip route", "ip r", "ip neigh", "ip -s link",
		"ip rule show", "ss ", "netstat ", "ping ", "ping6 ", "traceroute ", "tracepath ",
		"dig ", "nslookup ", "host ", "ifconfig", "ethtool ", "arp ", "arping ",
		"curl -I ", // HEAD-only, explicit URL on the command line; no data channel
		// system state
		"ps ", "ps", "top -b", "df ", "df", "free ", "free", "du -sh", "du -s",
		"uname ", "uname", "uptime", "date", "hostname", "hostname ", "id", "who", "w",
		"last ", "lastlog", "vmstat ", "iostat ", "dstat ", "lscpu", "lsblk", "lsusb", "lspci",
		"cat /proc", "cat /sys", "cat /etc/os-release", "cat /etc/hostname",
		// services & logs
		"systemctl status", "systemctl list-units", "systemctl list-unit-files",
		"systemctl is-active", "systemctl is-enabled", "systemctl is-failed",
		"systemctl show", "systemctl cat", "journalctl", "dmesg",
		"tail /var/log", "tail -n", "tail --lines", "head /var/log", "head -n",
		"grep /var/log", "grep -c /var/log", "grep -i /var/log", "wc -l /var/log",
		"ls ", "ls", "stat ", "file ", "which ", "whereis ",
		// containers (read surfaces)
		"docker ps", "docker stats --no-stream", "docker inspect", "docker logs --tail",
		"docker images", "crictl ps", "kubectl get", "kubectl describe", "kubectl logs --tail",
		"kubectl top",
	},
}

func (linuxShell) Classify(cmd string) Class { return linuxTables.classify(cmd) }

// ── Windows host (OpenSSH + PowerShell) ─────────────────────────────────────
//
// Windows hosts answer the same diagnostics over OpenSSH's PowerShell endpoint
// (no RDP involved — the GUI stays human-only by design). Get-* cmdlets and
// the classic net tools form the read table; Set-/New-/Stop-* are writes.
type windowsPowerShell struct{}

func (windowsPowerShell) Key() string { return "windows-powershell" }

func (windowsPowerShell) PagingOff() []string { return nil }

// Prompt: `PS C:\Users\admin>` at line end.
func (windowsPowerShell) Prompt() *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\n)PS [A-Za-z]:\\[^\n]{0,100}> ?$`)
}

func (windowsPowerShell) Errors() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*(?:Get|Set|New|Remove|Stop|Start|Restart)-\w+\s*:\s*(?:Term|Object|Parameter|Command)`),
		regexp.MustCompile(`(?i)^\s*(?:CategoryInfo|FullyQualifiedErrorId)`),
		regexp.MustCompile(`(?i)is not recognized as an (?:internal|external) command`),
	}
}

var windowsTables = classTables{
	dangerous: []string{
		"shutdown", "restart-computer", "stop-computer", "format-volume", "format c",
		"clear-disk", "initialize-disk", "remove-item -recurse", "bcdedit /set",
		"reg delete", "wevtutil cl",
	},
	write: []string{
		"set-", "new-", "remove-", "stop-", "restart-", "start-", "disable-", "enable-",
		"invoke-expression", "invoke-command", "netsh ", "sc config", "sc start", "sc stop",
		"reg add", "schtasks /create", "schtasks /delete", "net user", "net localgroup",
		"net stop", "net start", "route add", "route delete", "netsh interface",
		"set-service", "set-netipinterface", "move-item", "copy-item", "rename-item",
		"clear-", "reset-", "write-", "out-file", "add-", "export-", "import-",
	},
	read: []string{
		"get-", "get", "systeminfo", "ipconfig", "ping ", "ping", "tracert", "nslookup",
		"netstat", "arp -a", "arp", "route print", "tasklist", "quser", "qwinsta",
		"whoami", "hostname", "ver", "wmic cpu", "wmic memorychip", "wmic diskdrive",
		"wmic netuse", "netsh interface show", "netsh advfirewall show", "netsh lan show",
		"wevtutil qe", "typeperf", "driverquery", "vol ", "dir ", "tree /f",
		"test-netconnection", "resolve-dnsname", "get-nettcpconnection", "get-help",
	},
}

func (windowsPowerShell) Classify(cmd string) Class { return windowsTables.classify(cmd) }

// metacharDrivers are the interactive SHELL drivers where a pipe, semicolon,
// backtick or redirect would smuggle unclassified commands past the word-prefix
// classifier into the PTY. Network CLIs are not shells — their `| include` is a
// device-side filter, not a command chain — so only these drivers need the
// metacharacter refusal in Exec.
var metacharDrivers = map[string]bool{
	"linux-shell":       true,
	"vmware-esxi":       true,
	"windows-powershell": true,
}

// ShellMetachars is the refusal set: pipe/command substitution/sequencing/
// redirection/subshell syntax. Exec explains the rule when it fires.
const ShellMetachars = ";|&`$()<>\\"

// IsShellMetacharDriver reports whether a driver key runs a general shell and
// therefore rejects metacharacters in commands.
func IsShellMetacharDriver(key string) bool { return metacharDrivers[key] }
