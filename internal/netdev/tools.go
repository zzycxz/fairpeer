package netdev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev/driver"
	"github.com/zzycxz/fairpeer/internal/netdev/transport"
	"github.com/zzycxz/fairpeer/internal/tool"
)

// HostKeyPrompt is the interactive TOFU hook. nil (default) = strict mode:
// unknown host keys are rejected. The desktop settings panel (P1 UI) injects
// a dialog-backed prompt here at startup.
var HostKeyPrompt transport.HostKeyPrompt

// Manager owns per-device connections and enforces the structural read-only
// seal: every netdev_exec command passes the driver classifier, and anything
// that is not ClassRead is refused and audited — never executed. There is no
// code path from the agent to a write; writes arrive with the proposal
// pipeline (P4), which a human approves.
type Manager struct {
	cfg *config.Config

	mu    sync.Mutex
	conns map[string]*managedConn
}

type managedConn struct {
	client  *transport.Client
	session *Session
	drv     driver.Driver
	lastUse time.Time
}

// idleAfter: an unused device session is closed (freeing the device's scarce
// VTY line, NETDEV_SPEC §6.5/B-6) even though the supervisor would happily
// keep the TCP/SSH link alive.
const idleAfter = 5 * time.Minute

func NewManager(cfg *config.Config) *Manager {
	m := &Manager{cfg: cfg, conns: map[string]*managedConn{}}
	go m.reaper(context.Background())
	return m
}

// Close tears down every cached connection.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, c := range m.conns {
		c.session.Close()
		c.client.Close()
		delete(m.conns, name)
	}
}

func (m *Manager) reaper(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			for name, c := range m.conns {
				if time.Since(c.lastUse) > idleAfter {
					c.session.Close()
					c.client.Close()
					delete(m.conns, name)
				}
			}
			m.mu.Unlock()
		}
	}
}

// ExecResult is the tool-facing outcome of one command.
type ExecResult struct {
	Device  string `json:"device"`
	Command string `json:"command"`
	Class   string `json:"class"`
	Output  string `json:"output"`
	IsError bool   `json:"is_error"`
	Refused bool   `json:"refused,omitempty"`
	Refusal string `json:"refusal,omitempty"`
}

// Exec runs one command on one configured device under the read-only seal.
func (m *Manager) Exec(ctx context.Context, deviceName, command string) ExecResult {
	device, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return ExecResult{Device: deviceName, Command: command, Refused: true,
			Refusal: fmt.Sprintf("device %q is not in the user-global netdev inventory (add it in the 运维 settings; the agent cannot add devices itself)", deviceName)}
	}

	drv, ok := driver.For(device.Vendor, device.OS)
	if !ok {
		return ExecResult{Device: deviceName, Command: command, Refused: true,
			Refusal: fmt.Sprintf("no driver for %s/%s (available: %s)", device.Vendor, device.OS, strings.Join(driver.Keys(), ", "))}
	}

	class := drv.Classify(command)
	base := ExecResult{Device: deviceName, Command: command, Class: class.String()}
	if class != driver.Read {
		base.Refused = true
		switch class {
		case driver.Write:
			base.Refusal = "write command — not executed. netdev's diagnostic hand is structurally read-only; configuration changes go through a human-approved change proposal (变更提案). Tell the user what change is needed and why."
		case driver.Dangerous:
			base.Refusal = "dangerous command — not executed. This class of command (reboot/delete/erase…) is proposal-only and requires secondary confirmation."
		default:
			base.Refusal = "unknown command — conservatively treated as write and not executed. Ask the user to confirm whether it is a read-only diagnostic; if so they can add it to the driver's read table (知识库归类)."
		}
		m.audit(device, command, class, AuditRefused, 0, nil)
		return base
	}

	res, err := m.runRead(ctx, device, drv, command)
	if err != nil {
		m.audit(device, command, class, AuditFailure, 0, err)
		base.Refused = true
		base.Refusal = "connection/session failure: " + err.Error()
		return base
	}
	status := AuditOK
	if res.IsError {
		status = AuditDeviceError
	}
	// Redact BEFORE the text crosses into the model context: credential lines
	// in device output never reach the LLM (or the audit's byte count).
	redacted := Redact(res.Output)
	m.audit(device, command, class, status, len(redacted), nil)
	base.Output = redacted
	base.IsError = res.IsError
	return base
}

func (m *Manager) audit(d config.NetDevDevice, cmd string, class driver.Class, status string, outBytes int, err error) {
	e := Audit{
		Device:      d.Name,
		Via:         d.Via,
		Command:     cmd,
		Class:       class.String(),
		Status:      status,
		OutputBytes: outBytes,
	}
	if err != nil {
		e.Error = err.Error()
	}
	if aerr := AppendAudit(e); aerr != nil {
		// Surfaced in the result stream is the manager's business; audit file
		// errors go to stderr via the tool error path only when fatal.
		_ = aerr
	}
}

// runRead obtains (or establishes) the device's cached session and runs the
// already-classified-Read command.
func (m *Manager) runRead(ctx context.Context, d config.NetDevDevice, drv driver.Driver, command string) (Result, error) {
	m.mu.Lock()
	existing, ok := m.conns[d.Name]
	if ok {
		existing.lastUse = time.Now()
	}
	m.mu.Unlock()
	if ok {
		return existing.session.Run(ctx, command)
	}

	client, session, err := m.connect(ctx, d, drv)
	if err != nil {
		return Result{}, err
	}
	m.mu.Lock()
	// Lost a race creating the same device's connection: close the loser.
	if other, ok := m.conns[d.Name]; ok {
		m.mu.Unlock()
		session.Close()
		client.Close()
		other.lastUse = time.Now()
		return other.session.Run(ctx, command)
	}
	m.conns[d.Name] = &managedConn{client: client, session: session, drv: drv, lastUse: time.Now()}
	m.mu.Unlock()
	return session.Run(ctx, command)
}

// connect resolves the route (device + via hop chain), credentials, and host
// key policy, then establishes a supervised transport client and CLI session.
func (m *Manager) connect(ctx context.Context, d config.NetDevDevice, drv driver.Driver) (*transport.Client, *Session, error) {
	if !m.cfg.NetDev.Enabled {
		return nil, nil, errors.New("[netdev] is disabled in the user config")
	}
	lookup := m.lookupEntry()
	resolved, err := transport.ResolveHost(lookup, d.Name, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve device: %w", err)
	}
	jumps, err := transport.ResolveJumpHosts(lookup, d.Via, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve route: %w", err)
	}

	auth := transport.AuthOptions{
		Password:   secretReader(SecretKindPassword, d.PasswordEnv),
		Passphrase: secretReader(SecretKindPassphrase, d.PassphraseEnv),
	}
	if d.PasswordEnv == "" && d.IdentityFile == "" {
		return nil, nil, fmt.Errorf("device %q has no credentials configured (set password_env or identity_file)", d.Name)
	}
	hops := make([]transport.JumpHostOptions, 0, len(jumps))
	for i, j := range jumps {
		hopCfg := m.hopByRaw(d.Via[i])
		ja := transport.AuthOptions{
			Password:   secretReader(SecretKindPassword, hopCfg.PasswordEnv),
			Passphrase: secretReader(SecretKindPassphrase, hopCfg.PassphraseEnv),
		}
		hops = append(hops, transport.JumpHostOptions{Host: j, Auth: ja})
	}

	client, err := transport.New(transport.Options{
		Host:      resolved,
		Auth:      auth,
		JumpHosts: hops,
		HostKeys:  &transport.HostKeyPolicy{Prompt: HostKeyPrompt},
	})
	if err != nil {
		return nil, nil, err
	}
	if err := client.Start(ctx); err != nil {
		client.Close()
		return nil, nil, err
	}
	session, err := OpenSession(ctx, client, drv, d.Encoding)
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	return client, session, nil
}

// lookupEntry adapts the pinned config into transport's name→entry lookup.
func (m *Manager) lookupEntry() transport.LookupEntry {
	return func(name string) (transport.HostEntry, bool) {
		if d, ok := m.cfg.NetDevDeviceByName(name); ok {
			return transport.HostEntry{
				Name: d.Name, Host: d.Address, Port: d.Port, User: d.Username,
				IdentityFile: d.IdentityFile, PassphraseEnv: d.PassphraseEnv,
				PasswordEnv: d.PasswordEnv, UseSSHConfig: d.UseSSHConfig,
			}, true
		}
		if h, ok := m.cfg.NetDevHopByName(name); ok {
			return transport.HostEntry{
				Name: h.Name, Host: h.Host, Port: h.Port, User: h.User,
				IdentityFile: h.IdentityFile, PassphraseEnv: h.PassphraseEnv,
				PasswordEnv: h.PasswordEnv, ProxyJump: h.ProxyJump, UseSSHConfig: h.UseSSHConfig,
			}, true
		}
		return transport.HostEntry{}, false
	}
}

func (m *Manager) hopByRaw(name string) config.NetDevHop {
	h, _ := m.cfg.NetDevHopByName(name)
	return h
}

// secretGetter is the credential read seam; production uses the secret store,
// tests stub it (the real store is a user-global singleton).
var secretGetter = GetSecret

// secretReader builds a credential closure from the secret store; nil when
// no env name is configured (transport then falls back to the next method).
func secretReader(kind, envName string) func() (string, error) {
	if strings.TrimSpace(envName) == "" {
		return nil
	}
	return func() (string, error) {
		v, ok, err := secretGetter(kind, envName)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("secret %s/%s not set — add it in the 运维 settings (credential values live in the secret store, never in TOML)", kind, envName)
		}
		return v, nil
	}
}

// ── Tools ────────────────────────────────────────────────────────────────────

// RegisterTools adds the netdev tool family to the registry. boot calls this
// ONLY inside the netdev profile branch, so dev/cowork sessions never see
// these tools (the reverse half of the hard seal; NETDEV_SPEC §7.1).
func RegisterTools(reg *tool.Registry, cfg *config.Config) {
	m := NewManager(cfg)
	reg.Add(&execTool{m: m})
	reg.Add(&devicesTool{cfg: cfg})
	reg.Add(&discoverTool{m: m})
}

type discoverTool struct{ m *Manager }

func (t *discoverTool) Name() string { return "netdev_discover" }

func (t *discoverTool) Description() string {
	return "TCP-probe a CIDR for open device ports (default 22/23) and grab banners, optionally THROUGH a configured hop " +
		"(via = hop name; empty = direct). The CIDR must be inside the configured discovery scopes — out-of-scope probes are refused. " +
		"Tunnel mode only (no UDP/ICMP); /20+ networks are refused (use the probe for those)."
}

func (t *discoverTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"cidr": {"type": "string", "description": "target network, e.g. 10.30.2.0/24"},
			"ports": {"type": "array", "items": {"type": "integer"}, "description": "ports to probe; default [22, 23]"},
			"via": {"type": "string", "description": "hop name to probe through; empty = direct"}
		},
		"required": ["cidr"]
	}`)
}

func (t *discoverTool) ReadOnly() bool { return true }

func (t *discoverTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		CIDR  string `json:"cidr"`
		Ports []int  `json:"ports"`
		Via   string `json:"via"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.CIDR) == "" {
		return "", errors.New("netdev_discover: cidr is required")
	}
	res, err := t.m.DiscoverTCP(ctx, a.Via, a.CIDR, a.Ports)
	if err != nil {
		return "", err
	}
	if err := AppendAudit(Audit{
		Device: "(discover)", Command: "tcp-probe " + a.CIDR + " via=" + strings.TrimSpace(a.Via),
		Class:  "read", Status: AuditOK,
	}); err != nil {
		_ = err
	}
	if len(res) == 0 {
		return "no responsive hosts in " + a.CIDR, nil
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

type execTool struct{ m *Manager }

func (t *execTool) Name() string { return "netdev_exec" }

func (t *execTool) Description() string {
	return "Run ONE read-only diagnostic command on a configured network device (display/show/ping/tracert…). " +
		"Write and dangerous commands are structurally refused — propose changes to the user instead. " +
		"Output is cleaned (paging/echo/prompt stripped) and device errors are flagged."
}

func (t *execTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"device": {"type": "string", "description": "device name from netdev_devices"},
			"command": {"type": "string", "description": "single CLI command, no newlines"}
		},
		"required": ["device", "command"]
	}`)
}

func (t *execTool) ReadOnly() bool { return true } // enforced by the classifier seal

func (t *execTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Device  string `json:"device"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if a.Device == "" || strings.TrimSpace(a.Command) == "" {
		return "", errors.New("netdev_exec: device and command are required")
	}
	b, err := json.Marshal(t.m.Exec(ctx, a.Device, a.Command))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

type devicesTool struct{ cfg *config.Config }

func (t *devicesTool) Name() string { return "netdev_devices" }

func (t *devicesTool) Description() string {
	return "List the configured network devices (name, vendor/OS, address, route via, group). Use the names with netdev_exec."
}

func (t *devicesTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *devicesTool) ReadOnly() bool { return true }

func (t *devicesTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	type row struct {
		Name, Vendor, OS, Model, Address, Group string
		Via                                     []string
	}
	rows := make([]row, 0, len(t.cfg.NetDev.Devices))
	for _, d := range t.cfg.NetDev.Devices {
		rows = append(rows, row{d.Name, d.Vendor, d.OS, d.Model, d.Address, d.Group, d.Via})
	}
	if len(rows) == 0 {
		return "no devices configured — add them (and their credentials) in the 运维 settings; [netdev] lives in the USER config only", nil
	}
	b, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
