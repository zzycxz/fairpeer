package main

// netdev_app.go bridges the 运维 (netdev) settings surface to the frontend:
// device/hop inventory editing (persisted to the USER config — the pinned
// global section), credential capture (secret store, netdev/* namespace),
// audit tail for the settings page, and ~/.ssh/config import candidates.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev"
	"github.com/zzycxz/fairpeer/internal/netdev/transport"
)

// NetDevDeviceView is one device row in the settings UI. Password never
// crosses the bridge; PasswordSet tells the form whether a secret exists.
type NetDevDeviceView struct {
	Name         string   `json:"name"`
	Vendor       string   `json:"vendor"`
	OS           string   `json:"os"`
	Model        string   `json:"model"`
	Address      string   `json:"address"`
	Port         int      `json:"port"`
	Via          []string `json:"via"`
	Group        string   `json:"group"`
	Username     string   `json:"username"`
	PasswordEnv  string   `json:"passwordEnv"`
	PasswordSet  bool     `json:"passwordSet"`
	IdentityFile string   `json:"identityFile"`
	Encoding     string   `json:"encoding"`
	AllowTelnet  bool     `json:"allowTelnet"`
	// Password is write-only from the form: blank = leave the stored secret
	// untouched; non-blank = store it under the netdev namespace.
	Password string `json:"password,omitempty"`
}

// NetDevHopView is one hop (bastion) row.
type NetDevHopView struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	PasswordEnv string `json:"passwordEnv"`
	PasswordSet bool   `json:"passwordSet"`
	ProxyJump   string `json:"proxyJump"`
	Password    string `json:"password,omitempty"`
}

// NetDevSettingsView is the whole settings payload.
type NetDevSettingsView struct {
	Enabled        bool               `json:"enabled"`
	NetworkName    string             `json:"networkName"`
	Devices        []NetDevDeviceView `json:"devices"`
	Hops           []NetDevHopView    `json:"hops"`
	Groups         []string           `json:"groups"` // group names (policy editing arrives with the proposal pipeline)
	AuditRetention string             `json:"auditRetention"`
	Scopes         []string           `json:"scopes"`
	// Guardrails reach into every ask / every tool call (NETDEV_SPEC §6):
	// per-command approval, per-turn command budget, per-conversation device scope.
	GuardConfirmEach  bool                `json:"guardConfirmEach"`
	GuardTurnBudget   int                 `json:"guardTurnBudget"`
	GuardAllowedGroup []string            `json:"guardAllowedGroups"`
	// ExtraRead is the read-table extension map (vendor → commands) so the
	// settings page can show and edit the knowledge-growth path.
	ExtraRead map[string][]string `json:"extraRead"`
}

// NetDevAuditEntryView is one audit row for the settings page.
type NetDevAuditEntryView struct {
	Time    string `json:"time"`
	Device  string `json:"device"`
	Command string `json:"command"`
	Class   string `json:"class"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

// NetDevSSHImportCandidate is one concrete Host alias from ~/.ssh/config.
type NetDevSSHImportCandidate struct {
	Alias string `json:"alias"`
	Host  string `json:"host"`
	User  string `json:"user"`
	Port  int    `json:"port"`
}

// NetDevSettings loads the pinned [netdev] section for the settings page.
func (a *App) NetDevSettings() (NetDevSettingsView, error) {
	startInspectionScheduler(a)
	cfg, err := config.Load()
	if err != nil {
		return NetDevSettingsView{}, err
	}
	v := NetDevSettingsView{
		Enabled:           cfg.NetDev.Enabled,
		NetworkName:       cfg.NetDev.NetworkName,
		AuditRetention:    cfg.NetDev.AuditRetention,
		Scopes:            cfg.NetDev.Discovery.Scopes,
		GuardConfirmEach:  cfg.NetDev.Guardrails.ConfirmEachCommand,
		GuardTurnBudget:   cfg.NetDev.Guardrails.TurnCommandBudget,
		GuardAllowedGroup: cfg.NetDev.Guardrails.AllowedGroups,
		ExtraRead:         cfg.NetDev.ExtraRead,
	}
	for _, d := range cfg.NetDev.Devices {
		v.Devices = append(v.Devices, NetDevDeviceView{
			Name: d.Name, Vendor: d.Vendor, OS: d.OS, Model: d.Model,
			Address: d.Address, Port: d.Port, Via: d.Via, Group: d.Group,
			Username: d.Username, PasswordEnv: d.PasswordEnv,
			PasswordSet:  netdevSecretSet(netdev.SecretKindPassword, d.PasswordEnv),
			IdentityFile: d.IdentityFile, Encoding: d.Encoding, AllowTelnet: d.AllowTelnet,
		})
	}
	for _, h := range cfg.NetDev.Hops {
		v.Hops = append(v.Hops, NetDevHopView{
			Name: h.Name, Host: h.Host, Port: h.Port, User: h.User,
			PasswordEnv: h.PasswordEnv,
			PasswordSet: netdevSecretSet(netdev.SecretKindPassword, h.PasswordEnv),
			ProxyJump:   h.ProxyJump,
		})
	}
	for _, g := range cfg.NetDev.Groups {
		v.Groups = append(v.Groups, g.Name)
	}
	return v, nil
}

func netdevSecretSet(kind, envName string) bool {
	if strings.TrimSpace(envName) == "" {
		return false
	}
	_, ok, err := netdev.GetSecret(kind, envName)
	return err == nil && ok
}

// SetNetDevSettings persists the inventory: non-secret fields via the config
// edit pipeline (which writes the USER config — [netdev] is pinned there),
// secrets to the encrypted store under netdev/<kind>/<env-name>. Blank
// password fields leave stored secrets untouched (standard form UX).
func (a *App) SetNetDevSettings(v NetDevSettingsView) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("SetNetDevSettings panic", "panic", r)
			err = fmt.Errorf("保存运维设置时发生内部错误：%v", r)
		}
	}()

	// Secrets first (so a config save failure doesn't orphan a typed password).
	for i, d := range v.Devices {
		pwd := strings.TrimSpace(d.Password)
		if pwd == "" {
			continue
		}
		env := autoEnvName(d.PasswordEnv, "NETDEV_PWD_"+envSafe(d.Name))
		v.Devices[i].PasswordEnv = env
		if err := netdev.SetSecret(netdev.SecretKindPassword, env, pwd); err != nil {
			return fmt.Errorf("save secret for device %q: %w", d.Name, err)
		}
	}
	for i, h := range v.Hops {
		pwd := strings.TrimSpace(h.Password)
		if pwd == "" {
			continue
		}
		env := autoEnvName(h.PasswordEnv, "NETDEV_PWD_"+envSafe(h.Name))
		v.Hops[i].PasswordEnv = env
		if err := netdev.SetSecret(netdev.SecretKindPassword, env, pwd); err != nil {
			return fmt.Errorf("save secret for hop %q: %w", h.Name, err)
		}
	}

	cfgErr := a.applyConfigOnly(func(c *config.Config) error {
		nd := config.NetDevConfig{
			Enabled:        v.Enabled,
			NetworkName:    strings.TrimSpace(v.NetworkName),
			AuditRetention: strings.TrimSpace(v.AuditRetention),
			Devices:        make([]config.NetDevDevice, 0, len(v.Devices)),
			Hops:           make([]config.NetDevHop, 0, len(v.Hops)),
		}
		if len(v.Scopes) > 0 {
			nd.Discovery.Scopes = v.Scopes
		}
		// Preserve fields this form does not edit: a rebuild here must never
		// wipe the user's read-table extensions, inspection scheduler, or
		// assessment envelope (they're TOML-managed; the form used to drop
		// them silently on every save). ExtraRead is form-owned when the
		// frontend sends it; older payloads fall back to preserving.
		if v.ExtraRead != nil {
			nd.ExtraRead = v.ExtraRead
		} else {
			nd.ExtraRead = c.NetDev.ExtraRead
		}
		nd.InspectionInterval = c.NetDev.InspectionInterval
		nd.Assessment = c.NetDev.Assessment
		nd.DefaultMode = c.NetDev.DefaultMode
		nd.ProxyDeviceTraffic = c.NetDev.ProxyDeviceTraffic
		nd.MaxSessionsPerDevice = c.NetDev.MaxSessionsPerDevice
		nd.Guardrails = config.NetDevGuardrails{
			ConfirmEachCommand: v.GuardConfirmEach,
			TurnCommandBudget:  v.GuardTurnBudget,
			AllowedGroups:      v.GuardAllowedGroup,
		}
		// Preserve group definitions not editable from this form yet.
		for _, g := range c.NetDev.Groups {
			nd.Groups = append(nd.Groups, g)
		}
		if nd.AuditRetention == "" {
			nd.AuditRetention = c.NetDev.AuditRetention
		}
		for _, d := range v.Devices {
			nd.Devices = append(nd.Devices, config.NetDevDevice{
				Name: strings.TrimSpace(d.Name), Vendor: strings.TrimSpace(d.Vendor),
				OS: strings.TrimSpace(d.OS), Model: strings.TrimSpace(d.Model),
				Address: strings.TrimSpace(d.Address), Port: d.Port,
				Via: d.Via, Group: strings.TrimSpace(d.Group),
				Username: strings.TrimSpace(d.Username), PasswordEnv: strings.TrimSpace(d.PasswordEnv),
				IdentityFile: strings.TrimSpace(d.IdentityFile), Encoding: strings.TrimSpace(d.Encoding),
				AllowTelnet: d.AllowTelnet,
			})
		}
		for _, h := range v.Hops {
			nd.Hops = append(nd.Hops, config.NetDevHop{
				Name: strings.TrimSpace(h.Name), Host: strings.TrimSpace(h.Host),
				Port: h.Port, User: strings.TrimSpace(h.User),
				PasswordEnv: strings.TrimSpace(h.PasswordEnv),
				ProxyJump:   strings.TrimSpace(h.ProxyJump),
			})
		}
		if err := config.ValidateNetDev(nd); err != nil {
			return err
		}
		c.NetDev = nd
		return nil
	})
	if cfgErr != nil {
		return cfgErr
	}
	// The shared Manager keeps serving with the fresh guardrails immediately;
	// extra_read tables go live without a restart.
	if fresh, err := config.Load(); err == nil && fresh != nil {
		netdev.ApplyExtraRead(fresh)
		_ = netdev.SharedManager(fresh)
	}
	slog.Info("SetNetDevSettings saved", "devices", len(v.Devices), "hops", len(v.Hops))
	return nil
}

// NetDevAddExtraRead is the one-click knowledge-growth path: when a command
// was refused as unknown (classifier table miss), the user can teach the read
// table right from the refusal chip — no TOML editing. Single line only, and
// the driver must exist for the vendor.
func (a *App) NetDevAddExtraRead(vendor, command string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("NetDevAddExtraRead panic", "panic", r)
			err = fmt.Errorf("添加读表条目时发生内部错误：%v", r)
		}
	}()
	command = strings.TrimSpace(command)
	vendor = strings.ToLower(strings.TrimSpace(vendor))
	if command == "" || strings.ContainsAny(command, "\n\r") {
		return fmt.Errorf("命令必须是非空单行")
	}
	cfgErr := a.applyConfigOnly(func(c *config.Config) error {
		for _, x := range c.NetDev.ExtraRead[vendor] {
			if x == command {
				return nil // idempotent: already taught
			}
		}
		if c.NetDev.ExtraRead == nil {
			c.NetDev.ExtraRead = map[string][]string{}
		}
		c.NetDev.ExtraRead[vendor] = append(c.NetDev.ExtraRead[vendor], command)
		return config.ValidateNetDev(c.NetDev)
	})
	if cfgErr != nil {
		return cfgErr
	}
	if fresh, err := config.Load(); err == nil && fresh != nil {
		netdev.ApplyExtraRead(fresh)
		_ = netdev.SharedManager(fresh)
	}
	slog.Info("netdev extra_read extended", "vendor", vendor, "command", command)
	return nil
}

// NetDevTurnBegin resets the per-turn command budget. The frontend calls it
// on every user submit while in the 运维 profile, so [netdev.guardrails]
// turn_command_budget is a true per-ask control.
func (a *App) NetDevTurnBegin() {
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}
	netdev.SharedManager(cfg).TurnBegin()
}

// ── proposal pipeline (human-only entry points; the agent can only draft
// via the netdev_propose tool) ─────────────────────────────────────────────

// inspectionSchedOnce starts the periodic inspection loop the first time the
// settings/findings surface is touched: every [netdev] inspection_interval,
// run the read battery and file a Finding. "" or unparsable = off.
var inspectionSchedOnce sync.Once

func startInspectionScheduler(a *App) {
	inspectionSchedOnce.Do(func() {
		go func() {
			for {
				cfg, err := config.Load()
				if err != nil || cfg == nil {
					return
				}
				d, err := time.ParseDuration(strings.TrimSpace(cfg.NetDev.InspectionInterval))
				if err != nil || d <= 0 {
					return // scheduling disabled; a later settings save restarts the app-level loop
				}
				time.Sleep(d)
				ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
				if f, err := netdev.SharedManager(cfg).RunInspection(ctx); err == nil && f != nil {
					slog.Info("scheduled netdev inspection filed", "title", f.Title)
				}
				cancel()
			}
		}()
	})
}

// NetDevQuickExec runs ONE read-only command from the UI (device detail
// quick-diagnose buttons) through the SAME sealed path as the agent's
// netdev_exec: classifier, redaction, audit — write/dangerous refused.
func (a *App) NetDevQuickExec(device, command string) (netdev.ExecResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return netdev.ExecResult{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	res := netdev.SharedManager(cfg).Exec(ctx, device, command)
	return res, nil
}

// NetDevTopologySnapshot merges every device's CDP/LLDP table into one graph
// for the layout mini-map (managed vs unmanaged nodes).
func (a *App) NetDevTopologySnapshot() (*netdev.TopologyGraph, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	return netdev.SharedManager(cfg).TopologySnapshot(ctx)
}

// NetDevTopologyPlan is the LOCAL topology view: pure computation over the
// inventory (groups, name conventions, the intranet IP plan) — zero device
// sessions, zero model calls. The 拓扑 tab shows it on open; the measured
// LLDP/CDP sweep (NetDevTopologySnapshot) only runs when the user clicks for
// it. Never mixed silently: the frontend badges which view it is showing.
func (a *App) NetDevTopologyPlan() (*netdev.TopologyGraph, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	g := netdev.InferTopology(cfg)
	return &g, nil
}

// NetDevEmergencyStop closes every device connection/session at once (the
// red button; audited). Returns how many connections were dropped.
func (a *App) NetDevEmergencyStop() (int, error) {
	cfg, err := config.Load()
	if err != nil {
		return 0, err
	}
	n := netdev.SharedManager(cfg).KillAllConnections()
	slog.Warn("netdev emergency stop", "connections", n)
	return n, nil
}

// NetDevRunInspection sweeps all devices with the read battery and files one
// Finding with the evidence (the manual 定时巡检; scheduler wiring later).
func (a *App) NetDevRunInspection() (*netdev.Finding, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
	defer cancel()
	return netdev.SharedManager(cfg).RunInspection(ctx)
}

// NetDevFindings lists diagnosis findings newest-first (Finding cards).
func (a *App) NetDevFindings() ([]*netdev.Finding, error) {
	return netdev.ListFindings()
}

// NetDevProposals lists proposals newest-first.
func (a *App) NetDevProposals() ([]*netdev.Proposal, error) {
	return netdev.ListProposals()
}

// NetDevApproveProposal is the human gate: enforces group policies
// (proposal+confirm2) and every group's change window.
func (a *App) NetDevApproveProposal(id string, confirm2 bool) (*netdev.Proposal, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return netdev.SharedManager(cfg).ApproveProposal(id, confirm2)
}

// NetDevExecuteProposal rolls the approved change device-by-device (backup →
// apply → verify); the first failure freezes the rest as partial.
func (a *App) NetDevExecuteProposal(id string) (*netdev.Proposal, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	return netdev.SharedManager(cfg).ExecuteProposal(ctx, id)
}

// NetDevRollbackProposal runs the authored rollback plan over the applied
// steps (a human decision after a partial freeze or a completed change).
func (a *App) NetDevRollbackProposal(id string) (*netdev.Proposal, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	return netdev.SharedManager(cfg).RollbackProposal(ctx, id)
}

// NetDevTestConnection runs the first-device flow for one device: connect →
// TOFU capture (no interactive prompt) → CLI session. Returns ok, or the
// first-seen host-key question for the UI to confirm, or the failure class.
func (a *App) NetDevTestConnection(device string) (netdev.TestResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return netdev.TestResult{}, err
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return netdev.SharedManager(cfg).TestConnection(ctx, device), nil
}

// NetDevTrustHostKey durably trusts a first-seen host key after the user
// confirmed its fingerprint in the UI (two-step TOFU; one-shot per capture).
func (a *App) NetDevTrustHostKey(fingerprint string) error {
	return netdev.TrustHostKey(fingerprint)
}

// NetDevDeleteSecret removes one stored credential (settings "清除密码").
func (a *App) NetDevDeleteSecret(kind, envName string) error {
	return netdev.DeleteSecret(kind, envName)
}

// NetDevAuditTail returns the last n audit entries (newest last).
func (a *App) NetDevAuditTail(n int) ([]NetDevAuditEntryView, error) {
	if n <= 0 || n > 500 {
		n = 100
	}
	raw, err := os.ReadFile(netdev.AuditPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	var out []NetDevAuditEntryView
	for _, line := range lines[start:] {
		if line == "" {
			continue
		}
		var e struct {
			Time    time.Time `json:"time"`
			Device  string    `json:"device"`
			Command string    `json:"command"`
			Class   string    `json:"class"`
			Status  string    `json:"status"`
			Error   string    `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, NetDevAuditEntryView{
			Time: e.Time.Format("01-02 15:04:05"), Device: e.Device,
			Command: e.Command, Class: e.Class, Status: e.Status, Error: e.Error,
		})
	}
	return out, nil
}

// NetDevSSHImportCandidates lists concrete ~/.ssh/config Host aliases so the
// settings form can prefill a device from an existing alias (address/user/
// port resolved through the embedded parser; no ssh -G subprocess here).
func (a *App) NetDevSSHImportCandidates() ([]NetDevSSHImportCandidate, error) {
	src, err := transport.LoadUserSSHConfig()
	if err != nil || src == nil {
		return nil, err
	}
	var out []NetDevSSHImportCandidate
	for _, h := range src.Aliases() {
		eff := src.Effective(h.Alias)
		out = append(out, NetDevSSHImportCandidate{
			Alias: h.Alias, Host: eff.HostName, User: eff.User, Port: eff.Port,
		})
	}
	return out, nil
}

func autoEnvName(existing, fallback string) string {
	if s := strings.TrimSpace(existing); s != "" {
		return s
	}
	return fallback
}

func envSafe(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else if r == '-' || r == ' ' {
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "DEVICE"
	}
	return b.String()
}
