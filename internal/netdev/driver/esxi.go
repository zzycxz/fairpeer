package driver

import "regexp"

// vmwareESXi drives VMware ESXi over its enabled SSH shell (BusyBox-ish ash +
// esxcli/vim-cmd). ESXi is the virtualization layer of the network: pNICs,
// vSwitches, port groups and VM virtual NICs — the topology's missing piece.
// Write-class commands (vim-cmd, esxcli system/actions, host changes) are
// proposal material like every other driver; the read table below is the
// diagnostic surface (esxcli network/storage/hardware IS, esxcfg-* -l, and
// BusyBox inspection tools).
//
// Quirk: the ESXi shell has NO pager, so PagingOff returns nothing.
type vmwareESXi struct{}

func (vmwareESXi) Key() string { return "vmware-esxi" }

func (vmwareESXi) PagingOff() []string { return nil }

// Prompt: the BusyBox ash prompt — `[~] #`, `[hostname:~] #`, or a bare `# `.
func (vmwareESXi) Prompt() *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\n)\[[^\]\n]{0,64}\]?[^\n]{0,32}# ?$`)
}

func (vmwareESXi) Errors() []*regexp.Regexp {
	return []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*(?:sh|bash|esxcli|vim-cmd|esxcfg-[\w-]+):\s*(?:not found|unknown|invalid|usage)`),
		regexp.MustCompile(`(?i)^\s*-bash: .*: (?:command not found|No such file)`),
		regexp.MustCompile(`(?i)^\s*(?:Error|Failed)[:：]`),
	}
}

var esxiTables = classTables{
	dangerous: []string{
		"reboot", "halt", "poweroff", "shutdown", "vim-cmd hostsvc/maintenance_mode_enter",
		"esxcli system shutdown", "esxcli system maintenancehost", "esxcli vm process kill",
		"esxcli storage filesystem automount", "dd", "mkfs", "fdisk", "partedUtil -w",
	},
	write: []string{
		"esxcli network vswitch standard add", "esxcli network vswitch standard uplink add",
		"esxcli network vswitch standard policy", "esxcli network vswitch standard portgroup add",
		"esxcli network ip interface add", "esxcli network ip interface remove", "esxcli network ip route add", "esxcli network ip route remove", "esxcli network firewall",
		"esxcli hardware tpms", "esxcli iscsi", "esxcli nic", "esxcli san", "esxcli system snmp set",
		"esxcli system settings advanced set", "esxcli system account add", "esxcli system ntp set",
		"esxcli system syslog",
		"esxcfg-vmknic",
		"esxcfg-route", "esxcfg-nics", "vim-cmd solo/registervm", "vim-cmd vmsvc/power", "vim-cmd vmsvc/snapshot",
		"vim-cmd vmsvc/destroy", "vim-cmd vmsvc/unregister", "vim-cmd vmsvc/reload", "vim-cmd vmsvc/upgrade",
		"vim-cmd vmsvc/clone", "vim-cmd vmsvc/migrate", "vim-cmd hostsvc/maintenance", "vim-cmd hostsvc/connect",
		"vim-cmd perms", "vim-cmd vimsvc", "vim-cmd proxysvc", "vim-cmd svc/",
		"cp", "mv", "rm", "chmod", "chown", "touch", "mkdir", "rmdir", "sed -i", "tee",
		"systemctl", "wc -w >", "esxcli vm", "esxcli guest",
	},
	read: []string{
		"esxcli network nic list", "esxcli network nic stats",
		"esxcli network vswitch standard list", "esxcli network vswitch standard policy",
		"esxcli network vswitch standard portgroup list", "esxcli network vswitch dvs vmware list",
		"esxcli network ip interface list", "esxcli network ip interface ipv4 get",
		"esxcli network ip route list", "esxcli network ip connection list",
		"esxcli network ip dns", "esxcli network ip neighbor list", "esxcli network ip arp",
		"esxcli network firewall get", "esxcli network vm list",
		"esxcli hardware cpu global get", "esxcli hardware memory get",
		"esxcli hardware platform get", "esxcli hardware clock get",
		"esxcli storage filesystem list", "esxcli storage vmfs extent list",
		"esxcli storage core device list", "esxcli storage core path list",
		"esxcli storage nfs list", "esxcli system version get", "esxcli system hostname get",
		"esxcli system settings advanced list", "esxcli system uptime get",
		"esxcli system snmp get", "esxcli system ntp get", "esxcli system syslog config get",
		"esxcli system maintenancehost get", "esxcli vm process list", "esxcli guest stats get",
		"esxcfg-vswitch -l", "esxcfg-vmknic -l", "esxcfg-route -l", "esxcfg-nics -l",
		"esxcfg-rescan -h", "esxcfg-mpath -l",
		"vim-cmd vmsvc/getallvms", "vim-cmd vmsvc/power.getstate", "vim-cmd vmsvc/get.summary",
		"vim-cmd hostsvc/net/info", "vim-cmd hostsvc/hostsummary", "vim-cmd hostsvc/storage/info",
		"vim-cmd hostsvc/datastore/listsummary", "vim-cmd hostsvc/hardware/info",
		"localcli --plugin-dir", "vmkping", "ping", "ping6", "traceroute", "nc -z",
		"date", "uname", "uptime", "df", "free", "ps", "ls", "cat /proc",
		"grep", "tail", "head", "wc", "which", "esxcli", "vmware -v", "vmware -l",
	},
}

func (vmwareESXi) Classify(cmd string) Class { return esxiTables.classify(cmd) }
