package netdev

// netprobeorch.go — 编排自家的 netprobe 二进制（cmd/netprobe，NETDEV_SPEC
// §5.1）。隧道探测是 TCP-only 且单 CIDR ≤4096；netprobe 跑在网络位置上
// （通常拷到跳板机执行），覆盖 ICMP 存活与 /16 级网段。产品只编排与解析
// （同 nmap 语法），闸门沿用：engagement 信封 + scopes 永不可关白名单；
// 主机预算走 max_hosts_per_job（默认 65536 = 一个 /16），不占 tunnel 的
// 4096 上限。登录面红线不变——netprobe 不做任何登录。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// NetprobeHost mirrors cmd/netprobe's hostResult JSON.
type NetprobeHost struct {
	IP    string `json:"ip"`
	ICMP  bool   `json:"icmp"`
	Ports []struct {
		Port   int    `json:"port"`
		Open   bool   `json:"open"`
		Banner string `json:"banner,omitempty"`
	} `json:"ports"`
}

// NetprobeSweepResult summarizes one orchestrated sweep.
type NetprobeSweepResult struct {
	CIDR      string `json:"cidr"`
	ICMP      bool   `json:"icmp"`
	Alive     int    `json:"alive"`
	WithPorts int    `json:"with_ports"`
	Results   []NetprobeHost `json:"results"`
	Command   string `json:"command"`
	Duration  string `json:"duration"`
}

// NetprobeSweep orchestrates cmd/netprobe over one in-scope CIDR. Leads land
// in the 待确认区 (source netprobe) — ICMP-only hosts become port-less alive
// rows, port hits merge like any other discovery source.
func (m *Manager) NetprobeSweep(ctx context.Context, cidr string, doICMP bool) (*NetprobeSweepResult, error) {
	if !m.cfg.NetDev.Enabled {
		return nil, fmt.Errorf("[netdev] is disabled")
	}
	if err := AssessmentActive(m.cfg.NetDev); err != nil {
		return nil, fmt.Errorf("netprobe sweep gate (same authorization class as weak-cred): %w", err)
	}
	cidr = strings.TrimSpace(cidr)
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	if !m.scopeAllows(ipnet) {
		return nil, fmt.Errorf("CIDR %s is outside the configured discovery scopes — probing is refused (scopes are a never-off guardrail)", cidr)
	}
	// The per-JOB budget (default 65536 = one /16) governs netprobe-class
	// sweeps; the tunnel mode's 4096 cap does not apply here by design.
	if _, err := discoveryHostsWithinCap([]string{cidr}, m.cfg.NetDev.Discovery.MaxHostsPerJob); err != nil {
		return nil, err
	}
	bin := strings.TrimSpace(m.cfg.NetDev.Discovery.NetprobePath)
	if bin == "" {
		if bin, err = exec.LookPath("netprobe"); err != nil {
			return nil, fmt.Errorf("netprobe binary not found — build cmd/netprobe and set [netdev.discovery] netprobe_path (copy it onto the network position you want to probe from)")
		}
	}
	args := []string{"-cidr", cidr, "-ports", "22,23,161,443,830", "-concurrency", "100"}
	if doICMP {
		args = append(args, "-icmp")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	start := time.Now()
	out, runErr := cmd.Output()
	res := &NetprobeSweepResult{CIDR: cidr, ICMP: doICMP, Command: bin + " " + strings.Join(args, " ")}
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return nil, fmt.Errorf("run netprobe: %w", runErr)
		}
		res.Duration = fmt.Sprintf("netprobe exited with error (%v); partial results below", runErr)
	}
	if err := json.Unmarshal(out, &res.Results); err != nil {
		return nil, fmt.Errorf("parse netprobe JSON: %w", err)
	}
	leads := make([]DiscoverHostResult, 0, len(res.Results))
	for _, h := range res.Results {
		res.Alive++
		if len(h.Ports) > 0 {
			res.WithPorts++
		}
		ports := make([]DiscoverPortProbe, 0, len(h.Ports))
		for _, p := range h.Ports {
			ports = append(ports, DiscoverPortProbe{Port: p.Port, Open: p.Open, Banner: p.Banner})
		}
		leads = append(leads, DiscoverHostResult{IP: h.IP, Ports: ports})
	}
	if res.Duration == "" {
		res.Duration = time.Since(start).Truncate(time.Second).String()
	}
	if len(leads) > 0 {
		if err := RecordDiscovered(SourceNetprobe, leads); err != nil {
			return res, fmt.Errorf("record leads: %w", err)
		}
	}
	_ = SaveRollingFinding(&Finding{
		Title:    fmt.Sprintf("netprobe 存活探测：%s — %d 台存活 / %d 台有开放端口%s", cidr, res.Alive, res.WithPorts, map[bool]string{true: "（含 ICMP）", false: ""}[doICMP]),
		Severity: "info",
		Devices:  []string{"(netprobe)"},
		Detail:   fmt.Sprintf("编排命令：%s（%s）。存活主机已回填待确认区（仅 ICMP 存活的主机为无端口行）。", res.Command, res.Duration),
		Evidence: []Evidence{{Device: "(netprobe)", Command: res.Command, Output: fmt.Sprintf("alive=%d with_ports=%d", res.Alive, res.WithPorts)}},
		Source:   "netprobe:sweep:" + cidr,
	})
	_ = AppendAudit(Audit{Time: time.Now(), Device: "(netprobe)", Command: "sweep " + cidr, Class: "assess", Status: AuditOK})
	return res, nil
}

// ── agent tool surface ───────────────────────────────────────────────────────

// netprobeTool exposes the liveness-sweep orchestrator. Like netdev_nmap it
// is an active scan gated on the engagement envelope.
type netprobeTool struct{ m *Manager }

func (t *netprobeTool) Name() string { return "netdev_netprobe" }

func (t *netprobeTool) Description() string {
	return "Orchestrated netprobe liveness sweep (NETDEV_SPEC §5.1): runs fairpeer's own netprobe binary (cmd/netprobe — the user builds it and sets [netdev.discovery] netprobe_path, typically on a jump host) over one in-scope CIDR, covering what SSH-tunnel probing cannot: ICMP echo liveness and /16-class subnets (per-job budget, not the tunnel 4096 cap). " +
		"Gated like netdev_assess: requires the [netdev.assessment] engagement envelope; the CIDR must sit inside the configured discovery scopes. Alive hosts land in the 待确认区 (ICMP-only hosts as port-less rows). The binary never logs in to anything."
}

func (t *netprobeTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"cidr": {"type": "string", "description": "one CIDR inside the configured discovery scopes, e.g. 10.1.0.0/16"},
			"icmp": {"type": "boolean", "description": "also ICMP-echo each host (netprobe must run with raw-socket privileges for this)"}
		},
		"required": ["cidr"]
	}`)
}

func (t *netprobeTool) ReadOnly() bool { return false }

func (t *netprobeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		CIDR string `json:"cidr"`
		ICMP bool   `json:"icmp"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.CIDR) == "" {
		return "", errors.New("netdev_netprobe: cidr is required")
	}
	label := "netprobe sweep " + strings.TrimSpace(a.CIDR)
	start := t.m.liveCmdStart("(netprobe)", label, "assess")
	status := AuditOK
	defer func() { t.m.liveCmdEnd("(netprobe)", label, "assess", status, start, 0, "") }()
	if err := AssessmentActive(t.m.cfg.NetDev); err != nil {
		t.m.liveCmdRefused("(netprobe)", label, "assess", err.Error())
		return "", fmt.Errorf("netdev_netprobe: %w", err)
	}
	res, err := t.m.NetprobeSweep(ctx, a.CIDR, a.ICMP)
	if err != nil {
		status = AuditRefused
		return "", fmt.Errorf("netdev_netprobe: %w", err)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: %d alive / %d with open ports (%s, icmp=%v)\n", res.CIDR, res.Alive, res.WithPorts, res.Duration, res.ICMP)
	for i, h := range res.Results {
		if i >= 20 {
			fmt.Fprintf(&sb, "… and %d more hosts\n", len(res.Results)-20)
			break
		}
		marker := map[bool]string{true: "icmp+", false: ""}[h.ICMP]
		ports := make([]string, 0, len(h.Ports))
		for _, p := range h.Ports {
			ports = append(ports, fmt.Sprintf("%d", p.Port))
		}
		fmt.Fprintf(&sb, "  %s: %s%s\n", h.IP, marker, strings.Join(ports, ","))
	}
	sb.WriteString("存活主机已回填待确认区；有端口的线索纳管后进 CVE 匹配。")
	return sb.String(), nil
}
