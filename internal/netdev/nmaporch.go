package netdev

// nmaporch.go — 服务探测编排 v1（PENLAB_CAPABILITY_GAPS P1-1，SPEC v2 附录 C
// "编排外部工具优先于自研"）：产品不内置扫描引擎，只编排用户自备的 nmap
// 二进制、解析其 XML、把结果证据化回填待确认区。授权与边界沿用既有闸门：
// engagement 信封（与弱口令核查同级）+ scopes 永不可关白名单 + 单 CIDR 上限。
// 登录面红线不变——本文件只调本机 nmap 进程，不碰任何设备登录。

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

// NmapHostService is one open service on one host.
type NmapHostService struct {
	Port    int    `json:"port"`
	Proto   string `json:"proto,omitempty"`
	Service string `json:"service,omitempty"` // name, e.g. ssh/http/redis
	Product string `json:"product,omitempty"` // e.g. OpenSSH / nginx
	Version string `json:"version,omitempty"`
}

// NmapHost is one host with its open services.
type NmapHost struct {
	IP       string            `json:"ip"`
	Hostname string            `json:"hostname,omitempty"`
	Services []NmapHostService `json:"services"`
}

// NmapSweepResult summarizes one orchestrated sweep.
type NmapSweepResult struct {
	CIDR      string     `json:"cidr"`
	Hosts     int        `json:"hosts"`
	OpenPorts int        `json:"open_ports"`
	Results   []NmapHost `json:"results"`
	Command   string     `json:"command"`
	Duration  string     `json:"duration"`
}

// nmap XML output (only the fields we consume).
type nmapXML struct {
	Hosts []struct {
		Hostnames struct {
			Hostname []struct {
				Name string `xml:"name,attr"`
			} `xml:"hostname"`
		} `xml:"hostnames"`
		Addresses []struct {
			Addr     string `xml:"addr,attr"`
			AddrType string `xml:"addrtype,attr"`
		} `xml:"address"`
		Ports struct {
			Port []struct {
				PortID  int                               `xml:"portid,attr"`
				Proto   string                            `xml:"protocol,attr"`
				State   struct{ State string `xml:"state,attr"` } `xml:"state"`
				Service struct {
					Name    string `xml:"name,attr"`
					Product string `xml:"product,attr"`
					Version string `xml:"version,attr"`
				} `xml:"service"`
			} `xml:"port"`
		} `xml:"ports"`
	} `xml:"host"`
}

// NmapSweep orchestrates one user-supplied nmap service sweep over a CIDR.
// Gates (in order): engagement envelope → scope whitelist → host cap →
// binary presence. Results land in the 待确认区 store (source nmap-import)
// so the promote → 指纹回填 → CVE 匹配 loop consumes them like any lead.
func (m *Manager) NmapSweep(ctx context.Context, cidr string) (*NmapSweepResult, error) {
	if !m.cfg.NetDev.Enabled {
		return nil, fmt.Errorf("[netdev] is disabled")
	}
	if err := AssessmentActive(m.cfg.NetDev); err != nil {
		return nil, fmt.Errorf("nmap sweep gate (same authorization class as weak-cred): %w", err)
	}
	cidr = strings.TrimSpace(cidr)
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	if !m.scopeAllows(ipnet) {
		return nil, fmt.Errorf("CIDR %s is outside the configured discovery scopes — probing is refused (scopes are a never-off guardrail)", cidr)
	}
	if ones, _ := ipnet.Mask.Size(); 1<<uint(32-ones) > discoverMaxHosts {
		return nil, fmt.Errorf("CIDR %s exceeds the %d-host sweep cap — split it (same cap as tunnel discovery)", cidr, discoverMaxHosts)
	}
	bin := strings.TrimSpace(m.cfg.NetDev.Discovery.NmapPath)
	if bin == "" {
		if bin, err = exec.LookPath("nmap"); err != nil {
			return nil, fmt.Errorf("nmap not found — install it yourself and set [netdev.discovery] nmap_path (the product orchestrates, never bundles a scanner)")
		}
	}
	args := []string{"-Pn", "-sT", "-sV", "--version-light", "--open", "-oX", "-", cidr}
	cmd := exec.CommandContext(ctx, bin, args...)
	start := time.Now()
	out, runErr := cmd.Output()
	res := &NmapSweepResult{CIDR: cidr, Command: bin + " " + strings.Join(args, " ")}
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			return nil, fmt.Errorf("run nmap: %w", runErr)
		}
		// nmap exits non-zero on partial failures but still emits XML for the
		// hosts it reached — parse what we have, surface the exit in Duration.
		res.Duration = fmt.Sprintf("nmap exited with error (%v); partial results below", runErr)
	}
	parsed, parseErr := parseNmapXML(out)
	if parseErr != nil {
		return nil, fmt.Errorf("parse nmap XML: %w", parseErr)
	}
	res.Results = parsed
	for _, h := range parsed {
		res.Hosts++
		res.OpenPorts += len(h.Services)
	}
	if res.Duration == "" {
		res.Duration = time.Since(start).Truncate(time.Second).String()
	}
	// Evidence back into the 待确认区：product/version 直接进 Parsed（纳管的
	// 指纹回填消费 Parsed），banner 位留人读的 "product version" 串。
	for _, h := range parsed {
		ports := make([]DiscoveredPort, 0, len(h.Services))
		for _, s := range h.Services {
			banner := s.Service
			if s.Product != "" {
				banner = s.Product
				if s.Version != "" {
					banner += " " + s.Version
				}
			}
			ports = append(ports, DiscoveredPort{Port: s.Port, Banner: banner,
				Parsed: BannerInfo{Kind: kindForService(s.Service), Product: s.Product, Version: s.Version}})
		}
		if err := RecordDiscoveredPorts(SourceNmap, h.IP, h.Hostname, ports); err != nil {
			return res, fmt.Errorf("record leads: %w", err)
		}
	}
	_ = SaveRollingFinding(&Finding{
		Title:    fmt.Sprintf("nmap 服务探测：%s — %d 台存活 / %d 个开放服务", cidr, res.Hosts, res.OpenPorts),
		Severity: "info",
		Devices:  []string{"(nmap)"},
		Detail:   fmt.Sprintf("编排命令：%s（%s）。结果已回填待确认区，纳管后指纹进入 CVE 匹配。", res.Command, res.Duration),
		Evidence: []Evidence{{Device: "(nmap)", Command: res.Command, Output: fmt.Sprintf("hosts=%d open_ports=%d", res.Hosts, res.OpenPorts)}},
		Source:   "nmap:sweep:" + cidr,
	})
	_ = AppendAudit(Audit{Time: time.Now(), Device: "(nmap)", Command: "sweep " + cidr, Class: "assess", Status: AuditOK})
	return res, nil
}

func parseNmapXML(raw []byte) ([]NmapHost, error) {
	var doc nmapXML
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var out []NmapHost
	for _, h := range doc.Hosts {
		ip := ""
		for _, a := range h.Addresses {
			if a.AddrType == "ipv4" || a.AddrType == "ipv6" {
				ip = a.Addr
				break
			}
		}
		if ip == "" {
			continue
		}
		host := NmapHost{IP: ip}
		if len(h.Hostnames.Hostname) > 0 {
			host.Hostname = h.Hostnames.Hostname[0].Name
		}
		for _, p := range h.Ports.Port {
			if p.State.State != "open" {
				continue
			}
			host.Services = append(host.Services, NmapHostService{
				Port: p.PortID, Proto: p.Proto,
				Service: p.Service.Name, Product: p.Service.Product, Version: p.Service.Version,
			})
		}
		if len(host.Services) > 0 {
			out = append(out, host)
		}
	}
	return out, nil
}

func kindForService(name string) string {
	switch name {
	case "ssh":
		return "ssh"
	case "http", "https", "http-alt", "https-alt":
		return "http"
	case "ftp":
		return "ftp"
	case "smtp":
		return "smtp"
	}
	return ""
}

// ── agent tool surface ───────────────────────────────────────────────────────

// nmapTool exposes the service-sweep orchestrator to the agent. Like
// netdev_assess it is NOT read-only (an active scan) and refuses without the
// engagement envelope; the scopes guardrail applies per CIDR inside.
type nmapTool struct{ m *Manager }

func (t *nmapTool) Name() string { return "netdev_nmap" }

func (t *nmapTool) Description() string {
	return "Orchestrated nmap service sweep (PENLAB P1-1): runs the USER-SUPPLIED nmap binary (set [netdev.discovery] nmap_path) over one in-scope CIDR, parses the XML, and files the open services into the 待确认区 (asset leads) — promote them to inventory and the fingerprint backfill feeds CVE matching. " +
		"Gated like netdev_assess: requires the [netdev.assessment] engagement envelope; the CIDR must sit inside the configured discovery scopes; single-CIDR cap = 4096 hosts. The product orchestrates the external tool — it never bundles a scanner and never touches device logins."
}

func (t *nmapTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"cidr": {"type": "string", "description": "one CIDR inside the configured discovery scopes, e.g. 10.1.0.0/24"}
		},
		"required": ["cidr"]
	}`)
}

func (t *nmapTool) ReadOnly() bool { return false }

func (t *nmapTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		CIDR string `json:"cidr"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.CIDR) == "" {
		return "", errors.New("netdev_nmap: cidr is required")
	}
	start := t.m.liveCmdStart("(nmap)", "nmap sweep "+strings.TrimSpace(a.CIDR), "assess")
	status := AuditOK
	defer func() { t.m.liveCmdEnd("(nmap)", "nmap sweep "+strings.TrimSpace(a.CIDR), "assess", status, start, 0, "") }()
	if err := AssessmentActive(t.m.cfg.NetDev); err != nil {
		t.m.liveCmdRefused("(nmap)", "nmap sweep "+strings.TrimSpace(a.CIDR), "assess", err.Error())
		return "", fmt.Errorf("netdev_nmap: %w", err)
	}
	res, err := t.m.NmapSweep(ctx, a.CIDR)
	if err != nil {
		status = AuditRefused
		return "", fmt.Errorf("netdev_nmap: %w", err)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s: %d hosts / %d open services (%s)\n", res.CIDR, res.Hosts, res.OpenPorts, res.Duration)
	for i, h := range res.Results {
		if i >= 20 {
			fmt.Fprintf(&sb, "… and %d more hosts\n", len(res.Results)-20)
			break
		}
		svcs := make([]string, 0, len(h.Services))
		for _, s := range h.Services {
			svcs = append(svcs, fmt.Sprintf("%d/%s", s.Port, s.Service))
		}
		fmt.Fprintf(&sb, "  %s: %s\n", h.IP, strings.Join(svcs, ", "))
	}
	sb.WriteString("结果已回填待确认区（发现 → 待确认页签）；建议纳管后跑 CVE 匹配。")
	return sb.String(), nil
}
