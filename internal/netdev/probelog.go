package netdev

// probelog.go — the log-source probe (服务/软件/日志文件探测). The problem it
// answers: the operator does not know which logs exist inside the box they
// just SSH'd into. The probe runs three PLAIN read-table commands over the
// device's existing sealed session (systemctl list-units / docker ps /
// ls -lh /var/log) and turns the outputs into log-source candidates for the
// 日志页 to offer as one-click picks. This is NOT network discovery: no scope
// whitelist is involved, no traffic beyond the device's own session, and the
// classifier/budget/redaction/audit of Exec apply unchanged.

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/zzycxz/fairpeer/internal/config"
)

// ProbeFile is one candidate log file. Allowed marks whether it sits inside
// the device's log whitelist (/var/log + log_paths): a false file needs its
// directory registered (one click in the UI) before file: reads work.
type ProbeFile struct {
	Name    string `json:"name"`    // messages
	Path    string `json:"path"`    // /var/log/messages
	Size    string `json:"size"`    // 2.1M (from ls -lh; "" = unknown)
	Allowed bool   `json:"allowed"` // readable via file: as-is
}

// LogSourceProbe is the probe result: running services (journal: candidates),
// containers (docker:), and /var/log files (file:). Errors carries per-leg
// notes (e.g. "docker: not available") — a missing docker is the common case,
// surfaced as a hint rather than a failure.
type LogSourceProbe struct {
	Device     string      `json:"device"`
	Services   []string    `json:"services"`
	Containers []string    `json:"containers"`
	Files      []ProbeFile `json:"files"`
	Errors     []string    `json:"errors"`
}

// ProbeLogSources probes one configured device. Every command is a read-table
// line composed here (never model- or user-supplied), so a refusal can only
// mean the device's driver refused it — reported per leg, never fatal. The
// battery branches on the device's driver: a systemd host, a Windows host,
// and everything else (network CLIs, ESXi, the API kinds) get what actually
// exists there — never three guaranteed-failing legs.
func (m *Manager) ProbeLogSources(ctx context.Context, deviceName string) LogSourceProbe {
	out := LogSourceProbe{Device: deviceName, Services: []string{}, Containers: []string{}, Files: []ProbeFile{}, Errors: []string{}}
	device, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		out.Errors = append(out.Errors, "device not in inventory")
		return out
	}
	switch drvKey(device) {
	case "linux-shell":
		m.probeLinuxLogSources(ctx, device, &out)
	case "windows-powershell":
		m.probeWindowsLogSources(ctx, device, &out)
	default:
		// Network CLIs (huawei/cisco/zte), ESXi, and the kind targets
		// (docker/k8s/firewall/redfish/snmp) have no journal/文件系统 battery —
		// one clean note instead of three failing legs.
		out.Errors = append(out.Errors, "log probe not supported for this vendor")
	}
	return out
}

// probeLinuxLogSources is the systemd-host battery: running units, /var/log,
// docker ps, and the common app-log roots.
func (m *Manager) probeLinuxLogSources(ctx context.Context, device config.NetDevDevice, out *LogSourceProbe) {
	if r := m.Exec(ctx, device.Name, "systemctl list-units --type=service --state=running --no-pager"); r.Refused {
		out.Errors = append(out.Errors, "services: "+r.Refusal)
	} else if r.IsError {
		out.Errors = append(out.Errors, "services: "+firstOutputLine(r.Output))
	} else {
		out.Services = parseRunningServices(r.Output)
	}
	if r := m.Exec(ctx, device.Name, "ls -lh /var/log"); r.Refused {
		out.Errors = append(out.Errors, "files: "+r.Refusal)
	} else if r.IsError {
		out.Errors = append(out.Errors, "files: "+firstOutputLine(r.Output))
	} else {
		out.Files = append(out.Files, parseLsFiles(r.Output, "/var/log", true)...)
	}
	if r := m.Exec(ctx, device.Name, "docker ps"); r.Refused || r.IsError {
		out.Errors = append(out.Errors, "docker: not available")
	} else {
		out.Containers = parseDockerPs(r.Output)
	}
	out.Files = append(out.Files, m.probeAppLogDirs(ctx, device, out)...)
}

// windowsServicesCmd lists the services (read prefix "get-", no pipes — the
// shell-metachar guard refuses PowerShell pipelines).
const windowsServicesCmd = "get-service"

// windowsEventChannelsCmd enumerates the Windows event log channels. The
// driver's read table carries this exact form (wevtutil qe was already read;
// enum-logs is the same pure enumeration).
const windowsEventChannelsCmd = "wevtutil enum-logs"

// probeWindowsLogSources is the Windows-host battery: running services
// (journal: has no analogue — services feed the picker) and the event-log
// channels surfaced as winevt: candidates in Files.
func (m *Manager) probeWindowsLogSources(ctx context.Context, device config.NetDevDevice, out *LogSourceProbe) {
	if r := m.Exec(ctx, device.Name, windowsServicesCmd); r.Refused {
		out.Errors = append(out.Errors, "services: "+r.Refusal)
	} else if r.IsError {
		out.Errors = append(out.Errors, "services: "+firstOutputLine(r.Output))
	} else {
		out.Services = parseWindowsServices(r.Output)
	}
	if r := m.Exec(ctx, device.Name, windowsEventChannelsCmd); r.Refused {
		out.Errors = append(out.Errors, "winevt: "+r.Refusal)
	} else if r.IsError {
		out.Errors = append(out.Errors, "winevt: "+firstOutputLine(r.Output))
	} else {
		out.Files = append(out.Files, parseEventChannels(r.Output)...)
	}
}

// appLogDirsCmd lists the common non-/var/log application log roots with
// `ls -ld` so every match renders as a `drwx… /path` line the parser below
// expects (a bare `ls -d` prints only paths, which the perm-column parser
// would drop). The glob is expanded by the device's own shell (a * is not a
// ShellMetachar — it cannot chain commands, it only feeds ls paths).
const appLogDirsCmd = "ls -ld /opt/*/logs /opt/*/log /usr/local/*/logs /srv/*/logs /data/logs /data/*/logs /home/*/logs"

// probeAppLogDirs lists one ls -lh per discovered app-log directory (≤8) and
// returns its files, each marked against the device's log whitelist. A miss
// (no app dirs) is the common case and reports nothing.
func (m *Manager) probeAppLogDirs(ctx context.Context, device config.NetDevDevice, out *LogSourceProbe) []ProbeFile {
	r := m.Exec(ctx, device.Name, appLogDirsCmd)
	if r.Refused || r.IsError {
		return nil
	}
	dirs := []string{}
	for _, line := range strings.Split(r.Output, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasPrefix(f[0], "d") {
			continue // "ls: cannot access …" notes fall out here
		}
		if p := f[len(f)-1]; strings.HasPrefix(p, "/") {
			dirs = append(dirs, strings.TrimRight(p, "/"))
		}
	}
	sort.Strings(dirs)
	if len(dirs) > 8 {
		dirs = dirs[:8]
	}
	roots := LogAllowedRoots(device)
	files := []ProbeFile{}
	for _, dir := range dirs {
		rl := m.Exec(ctx, device.Name, "ls -lh "+dir)
		if rl.Refused || rl.IsError {
			out.Errors = append(out.Errors, dir+": "+firstOutputLine(rl.Output))
			continue
		}
		files = append(files, parseLsFiles(rl.Output, dir, false)...)
	}
	// Allowed is per FILE against the whitelist (the device may already have
	// the dir registered in log_paths — then its files are directly readable).
	for i := range files {
		files[i].Allowed = logPathAllowed(files[i].Path, roots)
	}
	return files
}

func firstOutputLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// parseRunningServices takes `systemctl list-units` output (columns
// UNIT LOAD ACTIVE SUB DESCRIPTION) and returns running service unit names
// without the .service suffix. The header ("UNIT LOAD …") and the legend
// ("N loaded units listed.") fall out via the .service filter.
func parseRunningServices(out string) []string {
	seen := map[string]bool{}
	svcs := []string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		name := strings.TrimSuffix(f[0], ".service")
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		svcs = append(svcs, name)
	}
	sort.Strings(svcs)
	return svcs
}

// parseLsFiles takes `ls -lh <dir>` output and returns the regular files
// (perm column starts with '-'), skipping rotated archives so the picker
// offers live logs, not their history. allowed pre-marks whitelist state for
// the whole dir (/var/log and registered log_paths are always allowed).
var rotatedSuffix = regexp.MustCompile(`\.(gz|xz|bz2|zip|old|deleted|\d+)$|-\d{6,}$`)

func parseLsFiles(out, dir string, allowed bool) []ProbeFile {
	files := []ProbeFile{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 9 { // perm links owner group size month day time|year name
			continue
		}
		if !strings.HasPrefix(f[0], "-") {
			continue // dirs, symlinks, devices
		}
		name := f[len(f)-1]
		if name == "" || strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") || rotatedSuffix.MatchString(name) {
			continue
		}
		files = append(files, ProbeFile{Name: name, Path: strings.TrimRight(dir, "/") + "/" + name, Size: f[4], Allowed: allowed})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files
}

// parseDockerPs takes `docker ps` table output; NAMES is the final column.
func parseDockerPs(out string) []string {
	names := []string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "CONTAINER" {
			continue
		}
		if n := f[len(f)-1]; n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// parseWindowsServices takes `get-service` Format-Table output (columns
// Status Name [DisplayName…]; the header may be localized) and returns the
// service names. Format-Table truncates (never wraps) columns, so every data
// row is "Running audiosrv Windows Audio" with the name as the SECOND token;
// the "------ ------" separator and the header fall out via the filters.
func parseWindowsServices(out string) []string {
	seen := map[string]bool{}
	svcs := []string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "-") || strings.HasPrefix(f[1], "-") {
			continue // blank lines and the dashes separator
		}
		if f[0] == "Status" || f[0] == "状态" || f[1] == "Name" || f[1] == "名称" {
			continue // the (possibly localized) header
		}
		if name := f[1]; !seen[name] {
			seen[name] = true
			svcs = append(svcs, name)
		}
	}
	sort.Strings(svcs)
	return svcs
}

// parseEventChannels takes `wevtutil enum-logs` output (one channel name per
// line) and returns each channel as a log-source candidate (Path carries the
// winevt: source the reader consumes; Allowed is true — winevt reads are
// whitelisted by grammar, not by log_paths). Only channels the winevt: reader
// accepts (logUnitRe: one plain token) surface — the deep
// "Provider/Operational" channels would fail composeLogCommand validation.
func parseEventChannels(out string) []ProbeFile {
	files := []ProbeFile{}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		ch := strings.TrimSpace(line)
		if ch == "" || seen[ch] || !logUnitRe.MatchString(ch) {
			continue
		}
		seen[ch] = true
		files = append(files, ProbeFile{Name: ch, Path: "winevt:" + ch, Allowed: true})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files
}
