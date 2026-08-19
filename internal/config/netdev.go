package config

import (
	"fmt"
	"net"
	"strings"

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
type NetDevConfig struct {
	Enabled bool `toml:"enabled"`
	// NetworkName is the managed network's display name (e.g. "总部生产网") —
	// the 运维 page's identity anchor, like a coding workspace's project name.
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
	// InspectionInterval schedules the read-battery sweep ("1h", "30m"; "" = off).
	InspectionInterval string           `toml:"inspection_interval"`
	Assessment         NetDevAssessment `toml:"assessment"`
	// Guardrails are the per-ask / per-tool-call controls (NETDEV_SPEC §6):
	// they reach DOWN into every LLM turn, not just the mode level.
	Guardrails NetDevGuardrails `toml:"guardrails"`
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

// NetDevDevice is one managed network device (router/switch/firewall).
type NetDevDevice struct {
	Name          string      `toml:"name"`
	Vendor        string      `toml:"vendor"` // huawei | cisco | zte
	OS            string      `toml:"os"`     // vrp8 | vrp5 | ios | iosxe | zxr10 …
	Model         string      `toml:"model"`
	Address       string      `toml:"address"`
	Port          int         `toml:"port"` // 0 => 22
	Via           []string    `toml:"via"`  // ordered hop names (route to the device)
	Group         string      `toml:"group"`
	Protocols     []string    `toml:"protocols"` // priority order: ssh, telnet, netconf
	Username      string      `toml:"username"`
	PasswordEnv   string      `toml:"password_env"`
	IdentityFile  string      `toml:"identity_file"`
	PassphraseEnv string      `toml:"passphrase_env"`
	UseSSHConfig  bool        `toml:"use_ssh_config"`
	Encoding      string      `toml:"encoding"` // auto | utf-8 | gbk
	AllowTelnet   bool        `toml:"allow_telnet"`
	SNMP          *NetDevSNMP `toml:"snmp"`
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
}

// NetDevDiscovery bounds network probing (the scope whitelist is one of the
// never-off guardrails, NETDEV_SPEC invariant 3).
type NetDevDiscovery struct {
	Scopes        []string `toml:"scopes"`         // CIDR whitelist; probing outside is refused
	Rate          int      `toml:"rate"`           // parallel probe cap
	Mode          string   `toml:"mode"`           // tunnel | probe | auto
	ProbeFallback string   `toml:"probe_fallback"` // tunnel when netprobe can't deploy
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
	switch strings.ToLower(strings.TrimSpace(nd.DefaultMode)) {
	case "", "diagnose", "assess":
	default:
		return fmt.Errorf("netdev: default_mode must be diagnose or assess")
	}
	if nd.Guardrails.TurnCommandBudget < 0 {
		return fmt.Errorf("netdev guardrails: turn_command_budget must be >= 0 (0 = unlimited)")
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
		if seenDevices[d.Name] {
			return fmt.Errorf("netdev device %q: duplicate name", d.Name)
		}
		seenDevices[d.Name] = true
		if strings.TrimSpace(d.Address) == "" {
			return fmt.Errorf("netdev device %q: address is required", d.Name)
		}
		if d.Port < 0 || d.Port > 65535 {
			return fmt.Errorf("netdev device %q: port %d out of range", d.Name, d.Port)
		}
		switch d.Vendor {
		case "huawei", "cisco", "zte", "":
		default:
			return fmt.Errorf("netdev device %q: unknown vendor %q (huawei|cisco|zte)", d.Name, d.Vendor)
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
	}
	seenHops := map[string]bool{}
	for _, h := range nd.Hops {
		if strings.TrimSpace(h.Name) == "" {
			return fmt.Errorf("netdev hop: name is required")
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
	return nil
}

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
