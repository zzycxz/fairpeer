package config

import (
	"fmt"
	"math"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// NetDevConfig is the [netdev] section: the network-device operations
// inventory (devices, hops/jump hosts, groups) and its policies. Like
// Reasonix's [remote] it is a USER-GLOBAL security control: LoadForRoot pins
// it back to the user config after the project merge, so a cloned repo's
// fairpeer.toml can never inject devices, hop chains, or scan scopes that the
// agent would then connect to with the user's global credentials
// (NETDEV_SPEC §7.3). Secrets never live here: entries name credential env
// vars (*_env) whose values sit in the secret store under netdev/*.
// NetDevAlertsConfig（[netdev.alerts]）：夜班值守窗口。night_window 形如
// "22:00-07:00"（可跨午夜；空=无夜班窗口）；night_min 为窗口内允许打扰的
// 最低分级 critical|warning（默认 critical——夜班只叫人看高价值告警）。
type NetDevAlertsConfig struct {
	NightWindow string `toml:"night_window"`
	NightMin    string `toml:"night_min"`
}

// NightMinOrDefault returns the configured night-shift minimum band with the
// critical default applied.
func (c NetDevAlertsConfig) NightMinOrDefault() string {
	if strings.TrimSpace(c.NightMin) == "" {
		return "critical"
	}
	return strings.TrimSpace(c.NightMin)
}

type NetDevConfig struct {
	Enabled bool             `toml:"enabled"`
	Trap    NetDevTrapConfig `toml:"trap"`
	// NotifyWebhook is the Finding notification outlet (NETDEV_SPEC_V2 §5.2):
	// generic JSON POST; empty = off. min severity: info|warning|critical.
	NotifyWebhook     string `toml:"notify_webhook"`
	NotifyMinSeverity string `toml:"notify_min_severity"`
	NotifyFormat      string `toml:"notify_format"` // generic | feishu | dingtalk | wecom
	// SMTP 通知出口（§5.2 追加）：与 webhook 并行；密码入 secret store。
	NotifySMTPHost    string   `toml:"notify_smtp_host"`
	NotifySMTPPort    int      `toml:"notify_smtp_port"`
	NotifySMTPUser    string   `toml:"notify_smtp_user"`
	NotifySMTPPassEnv string   `toml:"notify_smtp_pass_env"`
	NotifySMTPFrom    string   `toml:"notify_smtp_from"`
	NotifySMTPTo      []string `toml:"notify_smtp_to"`
	// NotifyBotDest pushes through the embedded IM gateway (feishu:oc_xxx /
	// weixin:wxid_xxx / qq:group / telegram:chat) — empty = off.
	NotifyBotDest string `toml:"notify_bot_dest"`
	// BriefingPushTime schedules the daily briefing push ("08:00" local;
	// "" = off) through the notify outlets.
	BriefingPushTime string `toml:"briefing_push_time"`
	// Alerts 是告警联动的值守策略（SCENARIO_SPEC S4-2）：夜班窗口内低于
	// night_min 分级的巡检通知静默（轮次照常记录，晨报补账）。
	Alerts NetDevAlertsConfig `toml:"alerts"`
	// NetworkName is the managed network's display name (e.g. "总部生产网") —
	// 运维页面的身份标识, like a coding workspace's project name.
	NetworkName          string          `toml:"network_name"`
	DefaultMode          string          `toml:"default_mode"` // diagnose | assess
	AuditRetention       string          `toml:"audit_retention"`
	ProxyDeviceTraffic   bool            `toml:"proxy_device_traffic"` // false: devices dialed directly, never via the shared HTTP proxy
	MaxSessionsPerDevice int             `toml:"max_sessions_per_device"`
	Devices              []NetDevDevice  `toml:"devices"`
	Hops                 []NetDevHop     `toml:"hops"`
	Groups               []NetDevGroup   `toml:"groups"`
	Discovery            NetDevDiscovery `toml:"discovery"`
	// ExtraRead extends the drivers' read-command tables at runtime
	// ([netdev.extra_read] with vendor tables — see NETDEV_SPEC B-1: the
	// knowledge-growth path; unknown commands stay refused until classified).
	ExtraRead map[string][]string `toml:"extra_read"`
	// WeakCredDict is a local password-dictionary file for the strong tier of
	// netdev_weak_cred / NetDevWeakCredCheck (completion-spec §5.2). Empty =
	// basic tier only. The file's CONTENT never enters config or exports.
	WeakCredDict string `toml:"weak_cred_dict"`
	// InspectionInterval schedules the read-battery sweep ("1h", "30m"; "" = off).
	InspectionInterval string `toml:"inspection_interval"`
	// ScheduledBaseline rides the scheduled inspection sweep: also run the
	// config-security baseline battery each scheduled pass (default off).
	ScheduledBaseline bool `toml:"scheduled_baseline"`
	// BackupInterval schedules the config-backup sweep ("1h", "24h"; "" = off):
	// every tick snapshots every managed device's running-config into the
	// versioned vault — the drift/history backbone.
	BackupInterval string `toml:"backup_interval"`
	// BackupGitMirror turns the vault's git mirror on (WRITE_AUTHZ_SPEC §7.2):
	// every saveBackup also commits into a git repo inside the backups dir —
	// dedup'd, blame-able, human-readable history. Missing git binary degrades
	// gracefully (vault stays the runtime authority).
	BackupGitMirror bool             `toml:"backup_git_mirror"`
	Assessment      NetDevAssessment `toml:"assessment"`
	// Guardrails are the per-ask / per-tool-call controls (NETDEV_SPEC §6):
	// they reach DOWN into every LLM turn, not just the mode level.
	Guardrails NetDevGuardrails `toml:"guardrails"`
	// Write is the device write-authorization block (NETDEV_WRITE_AUTHZ_SPEC):
	// the global tier floor and the per-turn direct-write budget. Enforcement
	// lives in the netdev Manager (session-independent), NOT in permission.
	Write NetDevWriteConfig `toml:"write"`
	// Projects are site-level scopes (collections of device groups) for the
	// title-bar switcher — see NetDevProject.
	Projects []NetDevProject `toml:"projects"`
	// Presets are named diagnostic command batteries ("OSPF 邻居全套") the
	// device card can run in one click — each command still goes through the
	// sealed Exec path one by one.
	Presets []NetDevPreset `toml:"presets"`
	// LogFollow bounds the streaming tail -F follows (hard caps: lines, bytes,
	// duration — an unbounded follow streaming into the UI is an incident).
	LogFollow NetDevLogFollow `toml:"log_follow"`
	// DBSources are read-only database diagnostic endpoints (netdev_db_query):
	// the allowlist is exact-statement-prefix, the account itself must be
	// least-privilege (the real structural seal lives in the DB grants).
	DBSources []NetDevDBSource `toml:"db_sources"`
	// PollIntervalSeconds schedules the SNMP health sweep (0 = off): every
	// device carrying an [netdev.devices.*.snmp] block is polled for
	// reachability/uptime/interface status into the health snapshot.
	PollIntervalSeconds int `toml:"poll_interval_seconds"`
	// AlertRules turn health/syslog signals into auto-Findings (active →
	// resolved lifecycle). Evaluated on every health poll.
	AlertRules []NetDevAlertRule `toml:"alert_rules"`
	// Syslog is the passive receiver (UDP): devices point their syslog here;
	// lines aggregate per device and known-bad patterns auto-escalate to
	// Findings. Port 0 = off.
	Syslog NetDevSyslogConfig `toml:"syslog"`
}

// NetDevAlertRule is one threshold rule over the health snapshot.
type NetDevAlertRule struct {
	Name   string `toml:"name"`
	Metric string `toml:"metric"` // reachable | if_down_count | uptime_reset | flap_count | if_down_above_p90 | gpu.xid | gpu.temp | gpu.mem_pct | gpu.count
	Op     string `toml:"op"`     // >= | <= | ==
	// Value is float since the GPU metrics are decimal watermarks; TOML
	// integer literals decode into the float field unchanged (BurntSushi).
	Value    float64 `toml:"value"`    // reachable: 1=up 0=down; uptime_reset: 1=reboot detected
	Severity string  `toml:"severity"` // info | warning | critical
	Enabled  bool    `toml:"enabled"`
	// ForRounds is the debounce: fire only after the condition held for N
	// consecutive polls (0/1 = immediately). 语义同割接门的 SustainSec——
	// 单轮毛刺不立案。
	ForRounds int `toml:"for_rounds"`
	// PresetKey 是向导预设的稳定标识（如 "unreachable"）——按 key 去重，
	// 语言/文案改版不再产生重复规则（批次 B5）。手工规则留空。
	PresetKey string `toml:"preset_key,omitempty"`
}

// NetDevTrapConfig bounds the passive SNMP trap receiver (v2c).
type NetDevTrapConfig struct {
	Port int `toml:"port"` // UDP listen port; 0 = off
}

// NetDevSyslogConfig bounds the passive syslog receiver.
type NetDevSyslogConfig struct {
	Port       int `toml:"port"`         // UDP listen port; 0 = off
	RatePerMin int `toml:"rate_per_min"` // per-device ingest cap (0 => 600)
}

// NetDevLogFollow caps one streaming log follow.
type NetDevLogFollow struct {
	MaxLines   int `toml:"max_lines"`   // 0 => 500
	MaxBytes   int `toml:"max_bytes"`   // 0 => 256 KiB
	MaxSeconds int `toml:"max_seconds"` // 0 => 600
}

// Capped returns the follow caps with defaults filled in.
func (f NetDevLogFollow) Capped() NetDevLogFollow {
	if f.MaxLines <= 0 {
		f.MaxLines = 500
	}
	if f.MaxBytes <= 0 {
		f.MaxBytes = 256 * 1024
	}
	if f.MaxSeconds <= 0 {
		f.MaxSeconds = 600
	}
	return f
}

// NetDevDBSource is one read-only database diagnostic endpoint. PasswordEnv
// names the secret-store entry; the allowlist is exact-statement prefixes
// ("SHOW PROCESSLIST" style) — no wildcards, no table-name patterns.
type NetDevDBSource struct {
	Name        string   `toml:"name"`
	Type        string   `toml:"type"` // mysql | postgres | redis | mongodb | mssql | clickhouse | elasticsearch
	Host        string   `toml:"host"`
	Port        int      `toml:"port"`
	Username    string   `toml:"username"`
	PasswordEnv string   `toml:"password_env"`
	Database    string   `toml:"database"`
	Allowlist   []string `toml:"allowlist"`
	// Via tunnels the connection through the first named hop's SSH chain
	// (local forward; production DBs behind bastions, NETDEV_SPEC_V2 追加).
	Via []string `toml:"via"`
}

// NetDevPreset is one saved diagnostic battery.
type NetDevPreset struct {
	Name     string   `toml:"name"`
	Commands []string `toml:"commands"`
	Vendors  []string `toml:"vendors"` // empty = all vendors
}

// NetDevGuardrails — fine-grained, per-interaction controls:
//
//   - ConfirmEachCommand: every netdev_exec / netdev_netconf call pops an
//     approval card BEFORE it runs (boot installs permission Ask rules; Ask
//     outranks both readOnly-allow and YOLO mode, so even full-access mode
//     keeps asking). The one knob that makes every tool call a control point.
//   - TurnCommandBudget: max read commands per user turn (0 = unlimited).
//     The frontend resets the counter on each submit; beyond the budget the
//     tool refuses with a reminder instead of executing — runaway-loop
//     protection at the turn level.
//   - AllowedGroups: when non-empty, the agent may only see and touch devices
//     in these groups (netdev_devices output is filtered too, so the model's
//     world is scoped before the first token is spent).
type NetDevGuardrails struct {
	ConfirmEachCommand bool     `toml:"confirm_each_command"`
	TurnCommandBudget  int      `toml:"turn_command_budget"`
	AllowedGroups      []string `toml:"allowed_groups"`
}

// NetDevWriteTier is a device's write-authorization tier (WRITE_AUTHZ_SPEC §3):
// sealed (default) = direct writes refused, changes go through proposals;
// confirm = write commands run directly but each pops an approval card;
// auto = write commands run directly under the sandwich pipeline (lab).
type NetDevWriteTier string

const (
	NetDevWriteSealed  NetDevWriteTier = "sealed"
	NetDevWriteConfirm NetDevWriteTier = "confirm"
	NetDevWriteAuto    NetDevWriteTier = "auto"
)

// ParseNetDevWriteTier validates a configured tier string; empty resolves to
// sealed — Go's zero value keeps the SAFE default (same convention as
// NetDevDiscovery.NoMediumConfirm's inverted key).
func ParseNetDevWriteTier(s string) (NetDevWriteTier, error) {
	switch NetDevWriteTier(strings.ToLower(strings.TrimSpace(s))) {
	case "", NetDevWriteSealed:
		return NetDevWriteSealed, nil
	case NetDevWriteConfirm:
		return NetDevWriteConfirm, nil
	case NetDevWriteAuto:
		return NetDevWriteAuto, nil
	default:
		return "", fmt.Errorf("unknown write tier %q (want sealed|confirm|auto)", s)
	}
}

// writeTierRank orders sealed < confirm < auto; "wider" = higher rank.
func writeTierRank(t NetDevWriteTier) int {
	switch t {
	case NetDevWriteConfirm:
		return 1
	case NetDevWriteAuto:
		return 2
	default:
		return 0
	}
}

// NetDevWriteConfig is the [netdev.write] block: the global tier floor and
// the per-turn direct-write budget (independent of the read budget).
type NetDevWriteConfig struct {
	DefaultTier     string `toml:"default_tier"`      // "" = sealed
	TurnWriteBudget int    `toml:"turn_write_budget"` // 0 => 10; must be >= 0
}

// TurnWriteBudgetOrDefault fills the safe default (10 per user turn).
func (w NetDevWriteConfig) TurnWriteBudgetOrDefault() int {
	if w.TurnWriteBudget > 0 {
		return w.TurnWriteBudget
	}
	return 10
}

// NetDevDevice is one managed network device (router/switch/firewall).
type NetDevDevice struct {
	Name    string   `toml:"name"`
	Vendor  string   `toml:"vendor"` // huawei | cisco | zte
	OS      string   `toml:"os"`     // vrp8 | vrp5 | ios | iosxe | zxr10 …
	Model   string   `toml:"model"`
	Address string   `toml:"address"`
	Port    int      `toml:"port"` // 0 => 22
	Via     []string `toml:"via"`  // ordered hop names (route to the device)
	Group   string   `toml:"group"`
	// WriteOverride tightens (never widens) the device's write tier below its
	// group's tier (WRITE_AUTHZ_SPEC §4.1: auto 组里的设备可单独降回 sealed，
	// 反向不行 — validation rejects wider overrides).
	WriteOverride string `toml:"write_override"`
	// Role is the user's EXPLICIT device-class override for the topology icon
	// set (router/switch/firewall/ips/vpn/bastion/server/ap/cloud; Chinese
	// aliases accepted). Empty = infer (group words → model/name → vendor
	// default). This is the minimal non-GUI "manual override" of the parked
	// topology-overlay lot.
	Role      string   `toml:"role"`
	Protocols []string `toml:"protocols"` // priority order: ssh, netconf（telnet 已裁决删除，§6.4）
	// GPU（SCENARIO_SPEC S1-2）：智算主机标记——分诊电池追加 GPU 档
	// （nvidia-smi/npu-smi 只读三表 + XID 异常立案），设备卡显示 GPU 徽标。
	GPU           bool   `toml:"gpu"`
	Username      string `toml:"username"`
	PasswordEnv   string `toml:"password_env"`
	IdentityFile  string `toml:"identity_file"`
	PassphraseEnv string `toml:"passphrase_env"`
	UseSSHConfig  bool   `toml:"use_ssh_config"`
	Encoding      string `toml:"encoding"` // auto | utf-8 | gbk
	// LegacyAlgo opts this device into the legacy SSH interop profile: the
	// client additionally offers aes128-cbc / 3des-cbc / aes256-cbc ciphers and
	// diffie-hellman-group1-sha1 KEX (implemented but deliberately not offered
	// by x/crypto defaults). For ancient Cisco IOS 12.x / early VRP5 / Comware
	// boxes that fail with "no common algorithm". Opt-in per device — legacy
	// algorithms are cryptographically weak.
	LegacyAlgo bool `toml:"legacy_algo"`
	// OOBURL is the 带外启动器 deep link (NETDEV_SPEC_V2 §6.3): ESXi/堡垒/BMC
	// Web UI entry. FairPeer only launches the local browser/RDP client — no
	// RDP/VNC protocol in-product; the click is audited.
	OOBURL string      `toml:"oob_url"`
	SNMP   *NetDevSNMP `toml:"snmp"`
	// ConsolePort, when set, replaces the SSH dial with a serial console
	// line (COM3 on Windows — the USB-serial adapter plugged into the
	// switch's console port). No host keys, no auth: physical presence IS
	// the authorization; the read-only classifier still seals every command.
	// ConsoleBaud 0 => 9600, line format fixed at 8N1.
	ConsolePort string `toml:"console_port"`
	ConsoleBaud int    `toml:"console_baud"`
	// LogPaths whitelists additional log-directory roots for this device
	// (e.g. "/opt/app/logs", "/usr/local/tomcat/logs"). tail/head/grep/wc on
	// paths under /var/log or one of these roots classify as read — the
	// log-source path whitelist that feeds netdev_log_read and the classifier
	// bypass. Human-registered only, like every inventory field.
	LogPaths []string `toml:"log_paths"`
	// ConfigPaths whitelists server config-file roots for §7.3 配置文件管理
	// (e.g. "/etc/nginx"): snapshot/diff/drift reads and restore-verify
	// proposal steps are confined to these roots — same authorization model as
	// LogPaths, human-registered only. Edits NEVER happen in-product: change
	// products are submitted as file-upload proposal steps.
	ConfigPaths []string `toml:"config_paths"`
	// Kind is the DATA-PLANE discriminator (NETDEV_SPEC_V2 §2.1): "" derives
	// from vendor (backward compatible — existing devices unchanged);
	// "docker" / "k8s" enable their API clients below. vendor stays the CLI
	// driver story for network gear.
	Kind   string                `toml:"kind"` // "" | docker | k8s | firewall
	Docker *NetDevDockerConfig   `toml:"docker,omitempty"`
	K8s    *NetDevK8sConfig      `toml:"k8s,omitempty"`
	Fw     *NetDevFirewallConfig `toml:"firewall,omitempty"`
}

// NetDevDockerConfig is the kind=docker data plane: one Docker Engine
// endpoint, GET-only API paths (NETDEV_SPEC_V2 §2.2 — no client-side code
// path for POST/DELETE exists at all).
type NetDevDockerConfig struct {
	// Socket: npipe:////./pipe/docker_engine (Windows local) |
	// unix:///var/run/docker.sock | tcp://host:2375 (inventory hosts only).
	Socket string `toml:"socket"`
}

// NetDevK8sConfig is the kind=k8s data plane: one kubeconfig (secret store)
// + a pinned context (NETDEV_SPEC_V2 §2.3 / appendix B-7: the tool layer
// accepts the TARGET NAME only — no kubeconfig content, context or server
// overrides, so no SSRF/context-escape surface).
type NetDevK8sConfig struct {
	KubeconfigEnv string   `toml:"kubeconfig_env"` // secret-store key holding the kubeconfig YAML
	Context       string   `toml:"context"`        // pinned; "" = kubeconfig's current-context
	Namespaces    []string `toml:"namespaces"`     // allowed namespaces; empty = all
}

// NetDevFirewallConfig is the kind=firewall data plane (NETDEV_SPEC_V2 §2.6):
// vendor REST monitor endpoints, GET-only. v1 vendor: fortinet (FortiOS
// /api/v2/monitor/* + read-only /api/v2/cmdb/* GETs).
type NetDevFirewallConfig struct {
	ApiTokenEnv string `toml:"api_token_env"` // secret-store key holding the REST API token
}

// NetDevHop is a bastion/jump host on the route to devices. Hops are
// human-registered only — discovery results never auto-promote (NETDEV_SPEC
// invariant 5).
type NetDevHop struct {
	Name          string `toml:"name"`
	Host          string `toml:"host"`
	Port          int    `toml:"port"`
	User          string `toml:"user"`
	IdentityFile  string `toml:"identity_file"`
	PassphraseEnv string `toml:"passphrase_env"`
	PasswordEnv   string `toml:"password_env"`
	ProxyJump     string `toml:"proxy_jump"` // comma-separated chain of other hop names
	UseSSHConfig  bool   `toml:"use_ssh_config"`
}

// NetDevGroup carries the shared policy for a set of devices.
type NetDevGroup struct {
	Name         string `toml:"name"`
	Policy       string `toml:"policy"`        // read-only | proposal | proposal+confirm2
	ChangeWindow string `toml:"change_window"` // e.g. "tue,thu 22:00-24:00"; "" = any time
	// Write is the group's default write tier (sealed|confirm|auto; "" = inherit
	// [netdev.write].default_tier). Members inherit it; device overrides may
	// only tighten. WIDENING this past the confirmed-lock state still needs the
	// alert confirmation (Manager-side clamp).
	Write string `toml:"write"`
	// AutoExpires time-boxes the auto tier (WRITE_AUTHZ_SPEC §3: "72h"): once
	// the human-confirmed window elapses, the tier degrades to confirm — a
	// temporary lab grant is never left forgotten open. "" = no box.
	AutoExpires string `toml:"auto_expires"`
}

// NetDevWriteTierFor resolves a device's configured write tier: global
// default → group → device override. Validation enforces tighten-only at load;
// this resolver also clamps a wider override as a second line of defense
// (config could be hand-edited past validation by a partially-updated file).
func (nd NetDevConfig) NetDevWriteTierFor(d NetDevDevice) NetDevWriteTier {
	tier, _ := ParseNetDevWriteTier(nd.Write.DefaultTier)
	if g, ok := ndGroupByName(nd, d.Group); ok {
		if gt, err := ParseNetDevWriteTier(g.Write); err == nil && strings.TrimSpace(g.Write) != "" {
			tier = gt
		}
	}
	if ot, err := ParseNetDevWriteTier(d.WriteOverride); err == nil && strings.TrimSpace(d.WriteOverride) != "" {
		if writeTierRank(ot) <= writeTierRank(tier) {
			tier = ot
		}
	}
	return tier
}

// NetDevProject is a SITE-level scope (the Mist "site" / industry
// site-first pattern): a named collection of device groups — one 机房 / 园区 /
// 客户网络. 运维标题栏带有项目切换器; rail, findings and
// proposals filter to the active project so the operator thinks "which site"
// first, exactly like every mainstream NMS console.
type NetDevProject struct {
	Name   string   `toml:"name"`
	Groups []string `toml:"groups"`
	Note   string   `toml:"note"`
}

// NetDevDiscovery bounds network probing (the scope whitelist is one of the
// never-off guardrails, NETDEV_SPEC invariant 3).
type NetDevDiscovery struct {
	Scopes        []string `toml:"scopes"`         // CIDR whitelist; probing outside is refused
	Rate          int      `toml:"rate"`           // parallel probe cap
	Mode          string   `toml:"mode"`           // tunnel | probe | auto
	ProbeFallback string   `toml:"probe_fallback"` // tunnel when netprobe can't deploy
	// NmapPath is the user-supplied nmap binary for the service-sweep
	// orchestrator (empty = LookPath("nmap"); absent = the feature refuses
	// with install guidance — the product orchestrates, never bundles).
	NmapPath string `toml:"nmap_path"`
	// NetprobePath is fairpeer's own netprobe binary (cmd/netprobe) for
	// in-network liveness sweeps — the user builds/copies it (often onto a
	// jump host, where SSH-tunnel probing can't reach); the product
	// orchestrates and parses, same grammar as nmap_path.
	NetprobePath string `toml:"netprobe_path"`
	// SnmpCommunity enables F2's sysDescr fingerprint on discovery: hosts
	// with an open 161 get ONE v2c GET (no retry). Empty (default) = off —
	// SNMP stays a per-device metrics channel only.
	SnmpCommunity string `toml:"snmp_community"`
	// HTTPProbe enables F3's application fingerprint (default off): one
	// standard GET / per open 80/443/8080/8443 — title/Server header and the
	// TLS certificate. Opt-in: it is the only discovery traffic that is more
	// than a TCP handshake + banner wait.
	HTTPProbe bool `toml:"http_probe"`
	// F4 pacing keys (spec §4.7). Zero values take the spec defaults; -1
	// disables where disabling is a legal posture.
	FastMode       bool `toml:"fast_mode"`          // rate x4 for authorized windows
	MaxHostsPerJob int  `toml:"max_hosts_per_job"`  // 0 => 65536 (one /16)
	WallSec        int  `toml:"discovery_wall_sec"` // 0 => 14400 (4h)
	PerHostDelayMS int  `toml:"per_host_delay_ms"`  // 0 => 800ms jitter; -1 = off
	CacheTTLHours  int  `toml:"cache_ttl_hours"`    // 0 => 24; -1 = always re-probe
	MaxHops        int  `toml:"max_hops"`           // 0 => 2 (clamped 1..4): recursion depth cap
	// NoMediumConfirm pre-checks /23-/21 nets on the plan card (the key is
	// inverted so Go's zero value keeps the SAFE default: medium nets stay
	// unchecked until the operator opts into trusting them).
	NoMediumConfirm bool `toml:"medium_no_confirm"`
}

// NetDevSNMP carries SNMP collector credentials for a device (env names only).
type NetDevSNMP struct {
	Version      string `toml:"version"` // v2c | v3
	CommunityEnv string `toml:"community_env"`
	Username     string `toml:"username"`
	AuthEnv      string `toml:"auth_env"`
	PrivEnv      string `toml:"priv_env"`
}

// NetDevAssessment is the engagement envelope that must be present and valid
// to switch to assess mode (P5; declared now so configs are forward-stable).
type NetDevAssessment struct {
	EngagementID string   `toml:"engagement_id"`
	Scopes       []string `toml:"scopes"`
	Expires      string   `toml:"expires"`
	Approver     string   `toml:"approver"`
}

// NetDevGroupPolicy values.
const (
	NetDevPolicyReadOnly     = "read-only"
	NetDevPolicyProposal     = "proposal"
	NetDevPolicyProposalConf = "proposal+confirm2"
)

// NetDevDeviceByName looks up a configured device.
func (c *Config) NetDevDeviceByName(name string) (NetDevDevice, bool) {
	for _, d := range c.NetDev.Devices {
		if d.Name == name {
			return d, true
		}
	}
	return NetDevDevice{}, false
}

// NetDevHopByName looks up a configured hop.
func (c *Config) NetDevHopByName(name string) (NetDevHop, bool) {
	for _, h := range c.NetDev.Hops {
		if h.Name == name {
			return h, true
		}
	}
	return NetDevHop{}, false
}

// NetDevGroupByName looks up a configured group.
func (c *Config) NetDevGroupByName(name string) (NetDevGroup, bool) {
	for _, g := range c.NetDev.Groups {
		if g.Name == name {
			return g, true
		}
	}
	return NetDevGroup{}, false
}

// ValidateNetDev checks the whole [netdev] section. Errors carry the entry
// name so a bad device in a long inventory is findable.
func ValidateNetDev(nd NetDevConfig) error {
	if !nd.Enabled {
		return nil // everything else only matters once enabled
	}
	// Assessment envelope: partial envelopes are refused (both id and expiry,
	// or neither); an expired date is a legal saved state — the runtime gate
	// (AssessmentActive) refuses use, saving history stays possible.
	if nd.Assessment.EngagementID != "" || nd.Assessment.Expires != "" || nd.Assessment.Approver != "" {
		if nd.Assessment.EngagementID == "" || nd.Assessment.Expires == "" {
			return fmt.Errorf("netdev assessment: engagement_id and expires are both required (or clear all fields to close the envelope)")
		}
		if _, err := time.ParseInLocation("2006-01-02", nd.Assessment.Expires, time.Local); err != nil {
			return fmt.Errorf("netdev assessment: expires must be YYYY-MM-DD")
		}
	}
	switch strings.ToLower(strings.TrimSpace(nd.DefaultMode)) {
	case "", "diagnose", "assess":
	default:
		return fmt.Errorf("netdev: default_mode must be diagnose or assess")
	}
	if nd.Guardrails.TurnCommandBudget < 0 {
		return fmt.Errorf("netdev guardrails: turn_command_budget must be >= 0 (0 = unlimited)")
	}
	// Write tiers (WRITE_AUTHZ_SPEC §4): parse every configured tier loudly, and
	// enforce tighten-only overrides — a device may drop below its group's tier
	// but never widen past it (widening belongs to the group + alert flow).
	if _, err := ParseNetDevWriteTier(nd.Write.DefaultTier); err != nil {
		return fmt.Errorf("netdev write: default_tier: %v", err)
	}
	if nd.Write.TurnWriteBudget < 0 {
		return fmt.Errorf("netdev write: turn_write_budget must be >= 0 (0 = default 10)")
	}
	for _, g := range nd.Groups {
		if _, err := ParseNetDevWriteTier(g.Write); err != nil {
			return fmt.Errorf("netdev group %q: write: %v", g.Name, err)
		}
		if s := strings.TrimSpace(g.AutoExpires); s != "" {
			if d, err := time.ParseDuration(s); err != nil || d <= 0 {
				return fmt.Errorf("netdev group %q: auto_expires must be a positive duration (e.g. 72h), got %q", g.Name, s)
			}
		}
	}
	for _, d := range nd.Devices {
		ot, err := ParseNetDevWriteTier(d.WriteOverride)
		if err != nil {
			return fmt.Errorf("netdev device %q: write_override: %v", d.Name, err)
		}
		base := nd.Write.DefaultTier
		if g, ok := ndGroupByName(nd, d.Group); ok {
			base = g.Write
		}
		bt, _ := ParseNetDevWriteTier(base)
		if writeTierRank(ot) > writeTierRank(bt) {
			return fmt.Errorf("netdev device %q: write_override %q is wider than its group tier %q — overrides may only tighten (raise the group tier instead)", d.Name, ot, bt)
		}
	}
	seenProjects := map[string]bool{}
	for _, p := range nd.Projects {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("netdev project: name is required")
		}
		if seenProjects[p.Name] {
			return fmt.Errorf("netdev project %q: duplicate name", p.Name)
		}
		seenProjects[p.Name] = true
		for _, g := range p.Groups {
			if _, ok := ndGroupByName(nd, g); !ok {
				return fmt.Errorf("netdev project %q: references unknown group %q", p.Name, g)
			}
		}
	}
	seenPresets := map[string]bool{}
	for _, p := range nd.Presets {
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("netdev preset: name is required")
		}
		if seenPresets[p.Name] {
			return fmt.Errorf("netdev preset %q: duplicate name", p.Name)
		}
		seenPresets[p.Name] = true
		if len(p.Commands) == 0 {
			return fmt.Errorf("netdev preset %q: needs at least one command", p.Name)
		}
		for _, c := range p.Commands {
			if strings.ContainsAny(c, "\n\r") {
				return fmt.Errorf("netdev preset %q: commands must be single-line", p.Name)
			}
		}
	}
	if len(nd.Guardrails.AllowedGroups) > 0 {
		for _, g := range nd.Guardrails.AllowedGroups {
			if strings.TrimSpace(g) == "" {
				return fmt.Errorf("netdev guardrails: allowed_groups contains an empty name")
			}
			if _, ok := ndGroupByName(nd, g); !ok {
				return fmt.Errorf("netdev guardrails: allowed_groups references unknown group %q", g)
			}
		}
	}
	seenDevices := map[string]bool{}
	for _, d := range nd.Devices {
		if strings.TrimSpace(d.Name) == "" {
			return fmt.Errorf("netdev device: name is required")
		}
		if !ndNameValid(d.Name) {
			return fmt.Errorf("netdev device %q: name must be one plain token (letters/digits/_ . @ -, ≤64 chars) — it becomes file names in the backup/golden vaults", d.Name)
		}
		if seenDevices[d.Name] {
			return fmt.Errorf("netdev device %q: duplicate name", d.Name)
		}
		seenDevices[d.Name] = true
		if strings.TrimSpace(d.Address) == "" && strings.TrimSpace(d.ConsolePort) == "" {
			// Console devices may not know their IP yet — the serial line is
			// the only reach; everyone else still needs an address.
			return fmt.Errorf("netdev device %q: address is required", d.Name)
		}
		if cp := strings.TrimSpace(d.ConsolePort); cp != "" {
			if !ndNameValid(strings.TrimPrefix(strings.TrimPrefix(cp, `\\.\`), "/dev/")) {
				return fmt.Errorf("netdev device %q: console_port must be a plain port name (COM3 / /dev/ttyUSB0)", d.Name)
			}
			if d.ConsoleBaud < 0 || d.ConsoleBaud > 921600 {
				return fmt.Errorf("netdev device %q: console_baud must be 0 (=>9600) .. 921600", d.Name)
			}
			switch d.Vendor {
			case "redfish", "snmp":
				return fmt.Errorf("netdev device %q: console needs a CLI vendor (huawei|cisco|zte|linux|windows|vmware), not %q", d.Name, d.Vendor)
			}
			switch d.Kind {
			case "docker", "k8s", "firewall":
				return fmt.Errorf("netdev device %q: kind=%s has no CLI console", d.Name, d.Kind)
			}
		}
		if d.Port < 0 || d.Port > 65535 {
			return fmt.Errorf("netdev device %q: port %d out of range", d.Name, d.Port)
		}
		// Telnet is adjudicated out (§6.4) and no transport exists, but
		// discovery still probes port 23 — a telnet-only device would
		// otherwise die later inside the SSH version exchange with an
		// unexplained x/crypto error. Reject it where the cause is visible.
		if d.Port == 23 && strings.TrimSpace(d.ConsolePort) == "" {
			return fmt.Errorf("netdev device %q: port 23 is telnet — telnet transport is not implemented (SSH only); point port at the SSH listener or use console_port for the serial line", d.Name)
		}
		switch d.Vendor {
		case "huawei", "cisco", "zte", "h3c", "ruijie", "vmware", "redfish", "linux", "windows", "snmp", "":
		default:
			return fmt.Errorf("netdev device %q: unknown vendor %q (huawei|cisco|zte|h3c|ruijie|vmware|redfish|linux|windows|snmp)", d.Name, d.Vendor)
		}
		switch d.Encoding {
		case "", "auto", "utf-8", "gbk":
		default:
			return fmt.Errorf("netdev device %q: encoding must be auto|utf-8|gbk", d.Name)
		}
		for _, via := range d.Via {
			if _, ok := ndHopByName(nd, via); !ok {
				return fmt.Errorf("netdev device %q: via references unknown hop %q", d.Name, via)
			}
		}
		if d.Group != "" {
			if _, ok := ndGroupByName(nd, d.Group); !ok {
				return fmt.Errorf("netdev device %q: unknown group %q", d.Name, d.Group)
			}
		}
		for _, lp := range d.LogPaths {
			if err := validateLogPath(d.Name, lp); err != nil {
				return err
			}
		}
		// ConfigPaths 与 LogPaths 同一授权模型（字段注释自述）——srvconf 的
		// 路径会进远端 cat 命令，不做同样的注入校验就是漏斗（逐行精读 R3 P2-1）。
		for _, cp := range d.ConfigPaths {
			if err := validateLogPath(d.Name, cp); err != nil {
				return fmt.Errorf("config_paths: %w", err)
			}
		}
		switch d.Kind {
		case "", "docker", "k8s", "firewall":
		default:
			return fmt.Errorf("netdev device %q: kind must be docker|k8s|firewall (empty = derive from vendor)", d.Name)
		}
		if d.Kind == "k8s" {
			if d.K8s == nil || strings.TrimSpace(d.K8s.KubeconfigEnv) == "" {
				return fmt.Errorf("netdev device %q: kind=k8s needs k8s.kubeconfig_env (secret-store key holding the kubeconfig)", d.Name)
			}
		}
		if d.Kind == "firewall" && d.Fw == nil {
			return fmt.Errorf("netdev device %q: kind=firewall needs a [firewall] block (api_token_env)", d.Name)
		}
	}
	seenHops := map[string]bool{}
	for _, h := range nd.Hops {
		if strings.TrimSpace(h.Name) == "" {
			return fmt.Errorf("netdev hop: name is required")
		}
		if !ndNameValid(h.Name) {
			return fmt.Errorf("netdev hop %q: name must be one plain token (letters/digits/_ . @ -, ≤64 chars)", h.Name)
		}
		if seenHops[h.Name] {
			return fmt.Errorf("netdev hop %q: duplicate name", h.Name)
		}
		seenHops[h.Name] = true
		if strings.TrimSpace(h.Host) == "" {
			return fmt.Errorf("netdev hop %q: host is required", h.Name)
		}
		for _, j := range splitNDJumpChain(h.ProxyJump) {
			if j == h.Name {
				return fmt.Errorf("netdev hop %q: proxy_jump references itself", h.Name)
			}
			if _, ok := ndHopByName(nd, j); !ok {
				return fmt.Errorf("netdev hop %q: proxy_jump references unknown hop %q", h.Name, j)
			}
		}
	}
	for _, g := range nd.Groups {
		switch g.Policy {
		case "", NetDevPolicyReadOnly, NetDevPolicyProposal, NetDevPolicyProposalConf:
		default:
			return fmt.Errorf("netdev group %q: policy must be read-only|proposal|proposal+confirm2", g.Name)
		}
	}
	for _, scope := range nd.Discovery.Scopes {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(scope)); err != nil {
			return fmt.Errorf("netdev discovery scope %q: invalid CIDR", scope)
		}
	}
	if nd.PollIntervalSeconds < 0 || nd.PollIntervalSeconds > 86400 {
		return fmt.Errorf("netdev: poll_interval_seconds must be 0..86400 (0 = off)")
	}
	if nd.Syslog.Port < 0 || nd.Syslog.Port > 65535 {
		return fmt.Errorf("netdev syslog: port out of range")
	}
	if nd.BriefingPushTime != "" {
		var h, m int
		if n, _ := fmt.Sscanf(nd.BriefingPushTime, "%d:%d", &h, &m); n != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
			return fmt.Errorf("netdev: briefing_push_time must be HH:MM (e.g. 08:00), got %q", nd.BriefingPushTime)
		}
	}
	switch nd.NotifyFormat {
	case "", "generic", "feishu", "dingtalk", "wecom":
	default:
		return fmt.Errorf("netdev notify: format must be generic|feishu|dingtalk|wecom")
	}
	switch nd.NotifyMinSeverity {
	case "", "info", "warning", "critical":
	default:
		return fmt.Errorf("netdev notify: min_severity must be info|warning|critical")
	}
	if nd.Syslog.Port > 0 && nd.Syslog.Port < 1024 {
		return fmt.Errorf("netdev syslog: privileged ports (<1024) are not supported — use a high port and forward from the device (e.g. 5140)")
	}
	if nd.Trap.Port > 0 && nd.Trap.Port < 1024 {
		// Same rationale as syslog: a non-root desktop on macOS/Linux cannot
		// bind 162, and the trap receiver would silently stay off.
		return fmt.Errorf("netdev trap: privileged ports (<1024) are not supported — use a high port and point the device at it (e.g. 1162)")
	}
	seenRules := map[string]bool{}
	for _, r := range nd.AlertRules {
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("netdev alert_rule: name is required")
		}
		if seenRules[r.Name] {
			return fmt.Errorf("netdev alert_rule %q: duplicate name", r.Name)
		}
		seenRules[r.Name] = true
		switch r.Metric {
		case "reachable", "if_down_count", "uptime_reset", "flap_count", "if_down_above_p90",
			"gpu.xid", "gpu.temp", "gpu.mem_pct", "gpu.count":
		default:
			return fmt.Errorf("netdev alert_rule %q: metric must be reachable|if_down_count|uptime_reset|flap_count|if_down_above_p90|gpu.xid|gpu.temp|gpu.mem_pct|gpu.count", r.Name)
		}
		if r.ForRounds < 0 || r.ForRounds > 120 {
			return fmt.Errorf("netdev alert_rule %q: for_rounds must be 0-120 (consecutive polls before firing)", r.Name)
		}
		if math.IsNaN(r.Value) || math.IsInf(r.Value, 0) {
			return fmt.Errorf("netdev alert_rule %q: value must be finite (nan/inf never compares true)", r.Name)
		}
		switch r.Op {
		case "", ">=", "<=", "==":
		default:
			return fmt.Errorf("netdev alert_rule %q: op must be >=|<=|==", r.Name)
		}
		// == 配小数阈值是静默死规则：全部指标返回整数值 float，92.5 这类
		// 阈值永远不触发且无任何提示（逐行精读 P3-6）。只对显式 == 生效——
		// 空 op 在运行时规范化为 >=（ruleCmp），小数阈值合法，不能拒绝
		// （否则合法配置整份加载失败，逐行精读 R3 P1-1）。
		if r.Op == "==" && r.Value != math.Trunc(r.Value) {
			return fmt.Errorf("netdev alert_rule %q: op \"==\" requires an integer value (got %v) — use \">=\" for fractional thresholds", r.Name, r.Value)
		}
		// preset_key 字符集白名单（附录 B-10 精神：内部标识不自由文本）。
		if r.PresetKey != "" && !alertPresetKeyRe.MatchString(r.PresetKey) {
			return fmt.Errorf("netdev alert_rule %q: preset_key must match [a-z0-9_-]{1,64}", r.Name)
		}
		switch r.Severity {
		case "", "info", "warning", "critical":
		default:
			return fmt.Errorf("netdev alert_rule %q: severity must be info|warning|critical", r.Name)
		}
	}
	seenDB := map[string]bool{}
	for _, s := range nd.DBSources {
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("netdev db_source: name is required")
		}
		if seenDB[s.Name] {
			return fmt.Errorf("netdev db_source %q: duplicate name", s.Name)
		}
		seenDB[s.Name] = true
		if strings.TrimSpace(s.Host) == "" {
			return fmt.Errorf("netdev db_source %q: host is required", s.Name)
		}
		switch s.Type {
		case "mysql", "postgres", "redis", "mongodb", "mssql", "clickhouse", "elasticsearch":
		default:
			return fmt.Errorf("netdev db_source %q: type must be mysql|postgres|redis|mongodb|mssql|clickhouse|elasticsearch", s.Name)
		}
		if s.Port < 0 || s.Port > 65535 {
			return fmt.Errorf("netdev db_source %q: port %d out of range", s.Name, s.Port)
		}
		if len(s.Allowlist) == 0 {
			return fmt.Errorf("netdev db_source %q: allowlist must have at least one statement (exact prefixes — this is the seal)", s.Name)
		}
		for _, q := range s.Allowlist {
			if strings.ContainsAny(q, "\n\r;") || strings.Contains(q, "--") || strings.Contains(q, "/*") {
				return fmt.Errorf("netdev db_source %q: allowlist entries must be single plain statements (no ;, no comments)", s.Name)
			}
		}
	}
	return nil
}

// ndNameRe bounds inventory entry names: they are spliced into FILE PATHS
// (backups/<name>@<nanos>.json, golden/<name>.conf) — slashes, "..", and
// separators must never reach the filesystem layer.
var alertPresetKeyRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

var ndNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{0,63}$`)

func ndNameValid(name string) bool { return ndNameRe.MatchString(strings.TrimSpace(name)) }

func ndHopByName(nd NetDevConfig, name string) (NetDevHop, bool) {
	for _, h := range nd.Hops {
		if h.Name == name {
			return h, true
		}
	}
	return NetDevHop{}, false
}

func ndGroupByName(nd NetDevConfig, name string) (NetDevGroup, bool) {
	for _, g := range nd.Groups {
		if g.Name == name {
			return g, true
		}
	}
	return NetDevGroup{}, false
}

// validateLogPath enforces the shape a log whitelist root must have: absolute,
// lexically clean, and free of whitespace/shell metacharacters — the root is
// spliced into commands verbatim, so a space or `;` here would be an injection
// vector, not a path.
func validateLogPath(device, lp string) error {
	lp = strings.TrimSpace(lp)
	if lp == "" {
		return nil
	}
	if !strings.HasPrefix(lp, "/") {
		return fmt.Errorf("netdev device %q: log_paths entries must be absolute (%q)", device, lp)
	}
	if strings.Contains(lp, "..") || strings.ContainsAny(lp, " \t\n\r;|&`$()<>\"'") {
		return fmt.Errorf("netdev device %q: log_paths entry %q must be clean (no .., whitespace, or shell metacharacters)", device, lp)
	}
	return nil
}

func splitNDJumpChain(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" && !strings.EqualFold(p, "none") {
			out = append(out, p)
		}
	}
	return out
}

// pinNetDev restores the [netdev] section from the USER config after the
// project merge (LoadForRoot). This is the global pin: project-level TOML can
// never inject devices, hops, or scopes. A fresh decode of just the user file
// is used so merge mutations elsewhere in cfg are untouched.
func pinNetDev(cfg *Config) {
	uc := userConfigPath()
	if uc == "" {
		cfg.NetDev = NetDevConfig{}
		return
	}
	var userCfg struct {
		NetDev NetDevConfig `toml:"netdev"`
	}
	if _, err := toml.DecodeFile(uc, &userCfg); err != nil {
		// Unreadable/absent user config: the merged-in project section is not
		// trustworthy either — fall back to empty (disabled), never to the
		// project's values.
		cfg.NetDev = NetDevConfig{}
		return
	}
	cfg.NetDev = userCfg.NetDev
}

// NDPortOrDefault returns the configured port or 22.
func (d NetDevDevice) NDPortOrDefault() int {
	if d.Port > 0 {
		return d.Port
	}
	return 22
}

// NetDevWarnings returns soft-degrade findings for misconfigurations that are
// safe to keep loading（D1 软降级：坏通道进运行期 LastError/NoProbe，不绑架整份
// 配置启动）。Hard errors stay in ValidateNetDev. 由 config.Load 记 slog。
func NetDevWarnings(nd NetDevConfig) []string {
	var w []string
	for _, d := range nd.Devices {
		if d.SNMP != nil {
			switch d.SNMP.Version {
			case "v2c":
				if d.SNMP.CommunityEnv == "" {
					w = append(w, fmt.Sprintf("device %q: snmp v2c 缺 community_env——该设备不会进健康轮询", d.Name))
				}
			case "v3":
				if d.SNMP.Username == "" || d.SNMP.AuthEnv == "" {
					w = append(w, fmt.Sprintf("device %q: snmp v3 缺 username/auth_env——该设备不会进健康轮询", d.Name))
				}
			case "":
			default:
				w = append(w, fmt.Sprintf("device %q: snmp version %q 未实现（仅 v2c/v3）", d.Name, d.SNMP.Version))
			}
		}
		for _, pr := range d.Protocols {
			if pr != "ssh" && pr != "netconf" {
				w = append(w, fmt.Sprintf("device %q: protocol %q 无传输（telnet 已裁决删除）", d.Name, pr))
			}
		}
	}
	// proxy_jump 链成环检测（D1）：ProxyJump 是逗号分隔的 hop 名链，沿链
	// DFS，重复访问即环——加载期点名，不硬失败。
	hopByName := map[string]NetDevHop{}
	for _, h := range nd.Hops {
		hopByName[h.Name] = h
	}
	var hopWalk func(name string, chain []string)
	hopWalk = func(name string, chain []string) {
		h, ok := hopByName[name]
		if !ok {
			return
		}
		for _, jump := range strings.Split(h.ProxyJump, ",") {
			jump = strings.TrimSpace(jump)
			if jump == "" {
				continue
			}
			if containsString(append(chain, name), jump) {
				w = append(w, fmt.Sprintf("hop %q: proxy_jump 链成环（%v → %s）", name, chain, jump))
				continue
			}
			hopWalk(jump, append(chain, jump))
		}
	}
	for _, h := range nd.Hops {
		hopWalk(h.Name, nil)
	}
	for _, r := range nd.AlertRules {
		switch r.Metric {
		case "reachable":
			if r.Value != 0 && r.Value != 1 {
				w = append(w, fmt.Sprintf("alert_rule %q: reachable 的 value 应为 0/1（当前 %v）——规则永不触发或恒触发", r.Name, r.Value))
			}
		case "if_down_count", "flap_count", "if_down_above_p90", "gpu.xid", "gpu.count":
			if r.Value < 0 {
				w = append(w, fmt.Sprintf("alert_rule %q: %s 的 value 为负——恒真，会持续立案", r.Name, r.Metric))
			}
		case "gpu.temp":
			if r.Value < 0 || r.Value > 150 {
				w = append(w, fmt.Sprintf("alert_rule %q: gpu.temp 阈值 %v 超出物理范围 0..150", r.Name, r.Value))
			}
		case "gpu.mem_pct":
			if r.Value < 0 || r.Value > 100 {
				w = append(w, fmt.Sprintf("alert_rule %q: gpu.mem_pct 阈值 %v 超出 0..100", r.Name, r.Value))
			}
		}
	}
	return w
}
