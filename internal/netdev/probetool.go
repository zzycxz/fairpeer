package netdev

// probetool.go — netdev_probe：测绘三合一（SKILL_ORCHESTRATION_SPEC §6.1）。
// netdev_discover / netdev_nmap / netdev_netprobe 的模型面收敛为单一工具：
// "选哪台引擎"是知识，下沉为 depth 分档（对齐网段入口 L3-L5 阶梯）——
//
//	depth=L3 定点指纹：只探已有证据指向的地址（显式 targets，或按
//	         segment-priors 知识表的 gateway_candidates 派生网关候选），
//	         隧道 TCP 探针，个位数包；
//	depth=L4 微采样：按知识表 sample_points 抽 4-6 点（隧道探针），
//	         命中的"分布形状"直接读出段角色；
//	depth=L5 已验证段全扫：整段服务扫——mode 选引擎：
//	         auto = netprobe（若配 netprobe_path）→ nmap（若配 nmap_path）
//	         → 隧道探针兜底（受其 /20 上限约束）；显式 mode 覆盖。
//
// 闸门沿用各引擎自带（闸门在工具里，不在技能里）：L3/L4 隧道探针过 scopes
// 白名单；L5 全扫过评估信封 + scopes。评估信封的闸门面因此从三个工具收成
// 一个（spec §6.1 附带收益）。

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev/knowledge"
)

type probeTool struct{ m *Manager }

func (t *probeTool) Name() string { return "netdev_probe" }

func (t *probeTool) Description() string {
	return "Unified network probing (测绘三合一): ONE tool, engine choice sunk into depth tiers matching the segment-discovery ladder. " +
		"depth=L3 (定点指纹): probe ONLY evidence-pointed addresses — pass targets=[ips], or leave empty to derive gateway candidates from the segment-priors knowledge table; tunnel TCP probe, a handful of packets, scopes-gated. " +
		"depth=L4 (微采样): sample 4-6 points per the knowledge table's sample_points (gateway + infra block + mid + end + random) to read the segment's role from the hit distribution; tunnel probe, scopes-gated. " +
		"depth=L5 (已验证段全扫): full sweep of an ALREADY-VERIFIED segment only — never a bare CIDR guess; mode auto picks netprobe→nmap→tunnel (explicit tunnel|probe|nmap overrides); envelope + scopes gated. " +
		"L3/L4 sampling expects a /24; L5 accepts larger nets per the engine's own caps."
}

func (t *probeTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"cidr": {"type": "string", "description": "target network, e.g. 10.30.2.0/24 (L3/L4 sampling requires /24)"},
			"depth": {"type": "string", "enum": ["L3", "L4", "L5"], "description": "ladder tier; default L4"},
			"targets": {"type": "array", "items": {"type": "string"}, "description": "L3 only: explicit evidence-pointed IPs (overrides gateway-candidate derivation)"},
			"mode": {"type": "string", "enum": ["auto", "tunnel", "probe", "nmap"], "description": "L5 engine; default auto (netprobe→nmap→tunnel fallback)"},
			"via": {"type": "string", "description": "tunnel engine: hop name to probe through; empty = direct"},
			"ports": {"type": "array", "items": {"type": "integer"}, "description": "tunnel engine ports; default [22, 23]"},
			"icmp": {"type": "boolean", "description": "netprobe engine: also ICMP-echo (needs raw-socket privileges)"}
		},
		"required": ["cidr"]
	}`)
}

func (t *probeTool) ReadOnly() bool { return false }

func (t *probeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		CIDR    string   `json:"cidr"`
		Depth   string   `json:"depth"`
		Targets []string `json:"targets"`
		Mode    string   `json:"mode"`
		Via     string   `json:"via"`
		Ports   []int    `json:"ports"`
		ICMP    bool     `json:"icmp"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	cidr := strings.TrimSpace(a.CIDR)
	if cidr == "" {
		return "", fmt.Errorf("netdev_probe: cidr is required")
	}
	depth := strings.ToUpper(strings.TrimSpace(a.Depth))
	if depth == "" {
		depth = "L4"
	}
	switch depth {
	case "L3":
		return t.runSampled(ctx, cidr, a, false)
	case "L4":
		return t.runSampled(ctx, cidr, a, true)
	case "L5":
		return t.runSweep(ctx, cidr, a)
	default:
		return "", fmt.Errorf("netdev_probe: depth must be L3|L4|L5, got %q", a.Depth)
	}
}

// runSampled covers L3 (gateway candidates / explicit targets) and L4 (sample
// points) — both ride the tunnel TCP probe (scopes-gated, no envelope: a
// handful of packets, same gate the old netdev_discover carried).
func (t *probeTool) runSampled(ctx context.Context, cidr string, a struct {
	CIDR    string   `json:"cidr"`
	Depth   string   `json:"depth"`
	Targets []string `json:"targets"`
	Mode    string   `json:"mode"`
	Via     string   `json:"via"`
	Ports   []int    `json:"ports"`
	ICMP    bool     `json:"icmp"`
}, l4 bool) (string, error) {
	var ips []string
	priors, err := loadSegmentPriors()
	if err != nil {
		return "", fmt.Errorf("netdev_probe: %v", err)
	}
	if len(a.Targets) > 0 && !l4 {
		for _, s := range a.Targets {
			if s = strings.TrimSpace(s); s != "" {
				ips = append(ips, s)
			}
		}
	} else {
		if l4 {
			ips = sampleTargetsFromCIDR(cidr, priors.SamplePoints)
		} else {
			ips = gatewayTargetsFromCIDR(cidr, priors.GatewayCandidates)
		}
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("netdev_probe: no target addresses derived (L3: pass targets=; L4: check segment-priors sample_points)")
	}
	// Scope pre-flight（一次硬拒，零发包）：scopes 白名单对模型永不可关——
	// 出界的目标在拨号之前拒绝，而不是逐行落进结果摘要。
	if _, ipnet, err := net.ParseCIDR(strings.TrimSpace(ips[0]) + "/32"); err == nil {
		if !t.m.scopeAllows(ipnet) {
			return "", fmt.Errorf("netdev_probe: %s is outside the configured discovery scopes — probing refused (scopes are a never-off guardrail)", ips[0])
		}
	}
	// Tunnel engine per /32 — DiscoverTCP's own scope gate refuses
	// out-of-scope addresses before any packet leaves.
	var hits []string
	portsSeen := map[int]bool{}
	// 网关位 = 采样点 ∩ gateway_candidates 映射集（取 ips 前两位会把 .2 这类
	// 基础设施区起点误当网关、把 .254 误当非网关——形状判读随之失真）。
	gwSet := map[string]bool{}
	for _, g := range gatewayTargetsFromCIDR(cidr, priors.GatewayCandidates) {
		gwSet[g] = true
	}
	var sb strings.Builder
	kind := map[bool]string{true: "L4 微采样", false: "L3 定点指纹"}[l4]
	fmt.Fprintf(&sb, "%s %s：%d 个地址（隧道 TCP 探针，scopes 白名单护）\n", kind, cidr, len(ips))
	for _, ip := range ips {
		res, err := t.m.DiscoverTCP(ctx, a.Via, ip+"/32", a.Ports)
		if err != nil {
			fmt.Fprintf(&sb, "  %s: 拒绝/失败 — %v\n", ip, err)
			continue
		}
		for _, h := range res {
			hits = append(hits, h.IP)
			ports := make([]string, 0, len(h.Ports))
			for _, p := range h.Ports {
				ports = append(ports, fmt.Sprintf("%d", p.Port))
				portsSeen[p.Port] = true
			}
			banner := ""
			if len(h.Ports) > 0 && h.Ports[0].Banner != "" {
				banner = " (" + truncateStr(h.Ports[0].Banner, 40) + ")"
			}
			fmt.Fprintf(&sb, "  %s: 开放 %s%s\n", h.IP, strings.Join(ports, ","), banner)
		}
	}
	if l4 {
		fmt.Fprint(&sb, probeL4Shape(cidr, len(hits), len(ips), gwSet, hits, portsSeen, priors))
	} else {
		fmt.Fprintf(&sb, "命中 %d/%d。网关判定请结合 ping TTL（segment-priors.ttl_map）。\n", len(hits), len(ips))
	}
	return sb.String(), nil
}

// probeL4Shape renders the L4 verdict (批 D②：采样判读下沉为输出注解——
// 分布形状读段角色，ports 型 role_signals 命中时按表给角色候选；OUI/SNMP 型
// 信号如实注明不在 probe 判读面内）。
func probeL4Shape(cidr string, hits, total int, gwSet map[string]bool, hitIPs []string, portsSeen map[int]bool, priors *segmentPriors) string {
	gwHits := 0
	for _, ip := range hitIPs {
		if gwSet[ip] {
			gwHits++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "形状注解（%s）：命中 %d/%d（网关位命中 %d；隧道探针默认只探 ports [22,23]，传 ports= 扩面）。\n", cidr, hits, total, gwHits)
	fired := false
	for _, s := range priors.RoleSignals {
		if s.Role == "" {
			continue
		}
		match := false
		for _, ps := range s.Ports {
			// 端口号不是八位组——atoiSafe 的 255 上限会把 445/88 全部拒掉。
			if n, err := strconv.Atoi(strings.TrimSpace(ps)); err == nil && portsSeen[n] {
				match = true
				break
			}
		}
		if match {
			fmt.Fprintf(&b, "  信号命中（ports %s）→ 角色 %s → %s\n", strings.Join(s.Ports, ","), s.Role, s.Action)
			fired = true
		}
	}
	if !fired {
		switch {
		case hits == 0:
			b.WriteString("  0 活：证据不足即止，绝不重扫。\n")
		case hits == gwHits:
			b.WriteString("  仅网关位活：候选已验证段，可起草 L5 全扫。\n")
		default:
			b.WriteString("  多点分布：生产段候选，可起草 L5 全扫。\n")
		}
	}
	b.WriteString("  ≥2 活才入地图（min_alive）；0 活标\"证据不足\"即止。printer_oui_dense / snmp_community_hit 信号由 L2 读表 / SNMP 证据消费，不在本注解判读面。\n")
	return b.String()
}

// runSweep covers L5 — full sweep of an already-verified segment. Engine per
// mode (auto: netprobe→nmap→tunnel fallback); each engine keeps its own gates
// (envelope + scopes for netprobe/nmap; scopes for tunnel).
func (t *probeTool) runSweep(ctx context.Context, cidr string, a struct {
	CIDR    string   `json:"cidr"`
	Depth   string   `json:"depth"`
	Targets []string `json:"targets"`
	Mode    string   `json:"mode"`
	Via     string   `json:"via"`
	Ports   []int    `json:"ports"`
	ICMP    bool     `json:"icmp"`
}) (string, error) {
	engine := pickProbeEngine(t.m.cfg, a.Mode)
	label := "probe L5 " + cidr + " [" + engine + "]"
	start := t.m.liveCmdStart("(probe)", label, "assess")
	status := AuditOK
	defer func() { t.m.liveCmdEnd("(probe)", label, "assess", status, start, 0, "") }()
	switch engine {
	case "netprobe":
		res, err := t.m.NetprobeSweep(ctx, cidr, a.ICMP)
		if err != nil {
			status = AuditRefused
			return "", fmt.Errorf("netdev_probe(L5/netprobe): %w", err)
		}
		return fmt.Sprintf("netprobe 全扫 %s：%d 存活 / %d 有开放端口（icmp=%v）。存活主机已回填待确认区；只应扫本阶梯已验证的段。", res.CIDR, res.Alive, res.WithPorts, res.ICMP), nil
	case "nmap":
		res, err := t.m.NmapSweep(ctx, cidr)
		if err != nil {
			status = AuditRefused
			return "", fmt.Errorf("netdev_probe(L5/nmap): %w", err)
		}
		return fmt.Sprintf("nmap 全扫 %s：%d 主机 / %d 开放服务。结果已回填待确认区；只应扫本阶梯已验证的段。", res.CIDR, res.Hosts, res.OpenPorts), nil
	default: // tunnel
		res, err := t.m.DiscoverTCP(ctx, a.Via, cidr, a.Ports)
		if err != nil {
			status = AuditRefused
			return "", fmt.Errorf("netdev_probe(L5/tunnel): %w", err)
		}
		return fmt.Sprintf("隧道全扫 %s：命中 %d 台（/20 上限内）。只应扫本阶梯已验证的段。", cidr, len(res)), nil
	}
}

// pickProbeEngine resolves the L5 engine: explicit mode wins; auto follows
// netprobe→nmap→tunnel (configured binary first, tunnel as the never-missing
// fallback — its own /20 cap refuses oversized nets loudly).
func pickProbeEngine(cfg *config.Config, mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "tunnel":
		return "tunnel"
	case "probe", "netprobe":
		return "netprobe"
	case "nmap":
		return "nmap"
	}
	if strings.TrimSpace(cfg.NetDev.Discovery.NetprobePath) != "" {
		return "netprobe"
	}
	if strings.TrimSpace(cfg.NetDev.Discovery.NmapPath) != "" {
		return "nmap"
	}
	return "tunnel"
}

// loadSegmentPriors fetches + validates the priors table through the
// knowledge channel (user override wins; hash lands in the audit chain).
type segmentPriors struct {
	Version           int      `yaml:"version"`
	GatewayCandidates []string `yaml:"gateway_candidates"`
	SamplePoints      []string `yaml:"sample_points"`
	// RoleSignals 批 D②：段职能指纹→动作。只有 ports 型信号由 L4 形状注解
	// 判读（隧道探针默认 ports=[22,23]，模型显式传 ports 才能命中 445/88）；
	// printer_oui_dense / snmp_community_hit 需要 ARP-OUI / SNMP 证据面，
	// probe 判读不了——注解里如实注明由 L2/SNMP 证据消费，不假装判读。
	RoleSignals []struct {
		Ports           []string `yaml:"ports"`
		PrinterOUIDense bool     `yaml:"printer_oui_dense"`
		SNMPHit         bool     `yaml:"snmp_community_hit"`
		Role            string   `yaml:"role"`
		Action          string   `yaml:"action"`
	} `yaml:"role_signals"`
}

func loadSegmentPriors() (*segmentPriors, error) {
	b, err := knowledge.Load("segment-priors")
	if err != nil {
		return nil, err
	}
	var p segmentPriors
	if err := yaml.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("segment-priors: %v", err)
	}
	return &p, nil
}

// gatewayTargetsFromCIDR maps the priors' host suffixes (".1", ".254"…) onto
// a /24's base. Non-/24 nets fall back to the first two candidates on the
// network base (still a handful of packets; full mapping needs a verified
// segment anyway).
func gatewayTargetsFromCIDR(cidr string, suffixes []string) []string {
	if len(suffixes) == 0 {
		suffixes = []string{".1", ".254"}
	}
	base, bits, err := parseCIDRBase(cidr)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(suffixes))
	for _, s := range suffixes {
		if ip := hostWithSuffix(base, bits, s); ip != "" {
			out = append(out, ip)
		}
	}
	return out
}

// sampleTargetsFromCIDR picks ≤6 sample addresses: each sample_points entry
// contributes one host (".2-.19" style ranges take the start; "random" takes
// a random host in the /24).
func sampleTargetsFromCIDR(cidr string, points []string) []string {
	if len(points) == 0 {
		points = []string{".1", ".100", ".254", "random"}
	}
	base, bits, err := parseCIDRBase(cidr)
	if err != nil || bits != 24 {
		if err == nil {
			return nil // sampling needs a /24
		}
		return nil
	}
	out := make([]string, 0, len(points))
	for _, p := range points {
		if p == "random" {
			out = append(out, hostWithSuffix(base, bits, fmt.Sprintf(".%d", 2+rand.Intn(252))))
			continue
		}
		// ".2-.19" → ".2"
		if i := strings.IndexByte(p, '-'); i > 0 {
			p = p[:i]
		}
		if ip := hostWithSuffix(base, bits, p); ip != "" {
			out = append(out, ip)
		}
	}
	return out
}

func parseCIDRBase(cidr string) (net.IP, int, error) {
	_, n, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, 0, err
	}
	ones, _ := n.Mask.Size()
	return n.IP, ones, nil
}

// hostWithSuffix replaces the host part of a /24 base with the suffix's last
// octet; returns "" for unparseable suffixes. Non-/24 nets: only exact
// suffix-free base allowed — callers treat "" as skip.
func hostWithSuffix(base net.IP, bits int, suffix string) string {
	if bits != 24 || len(suffix) == 0 || suffix[0] != '.' {
		return ""
	}
	oct, ok := atoiSafe(strings.TrimPrefix(suffix, "."))
	if !ok || oct < 0 || oct > 255 {
		return ""
	}
	out := make(net.IP, 4)
	copy(out, base.To4())
	out[3] = byte(oct)
	return out.String()
}

func atoiSafe(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 255 {
			return 0, false
		}
	}
	return n, true
}

// probeAliasTool — 旧工具名兼容别名（netdev_discover / netdev_nmap /
// netdev_netprobe → netdev_probe）。已装用户技能（如 netdev-security-
// assessment，inline 在主循环执行）的 body 还写着旧名——别名以显式
// mode 精确等价旧行为（discover=隧道全扫；nmap/netprobe=信封内编排），
// 闸门语义分毫不变（scopes/信封在底层引擎里照常拒）。描述标注弃用，
// 新代码一律 netdev_probe。
type probeAliasTool struct {
	oldName string
	mode    string // tunnel | nmap | netprobe
	inner   *probeTool
}

func (t *probeAliasTool) Name() string { return t.oldName }

func (t *probeAliasTool) Description() string {
	return "[弃用别名 → netdev_probe] 本次调用等价 netdev_probe(depth=L5, mode=" + t.mode + ")。旧名仅为已安装技能的兼容保留，新代码请直接使用 netdev_probe。"
}

func (t *probeAliasTool) ReadOnly() bool { return t.oldName == "netdev_discover" }

func (t *probeAliasTool) Schema() json.RawMessage {
	switch t.oldName {
	case "netdev_discover":
		return json.RawMessage(`{"type":"object","properties":{"cidr":{"type":"string"},"ports":{"type":"array","items":{"type":"integer"}},"via":{"type":"string"}},"required":["cidr"]}`)
	case "netdev_netprobe":
		return json.RawMessage(`{"type":"object","properties":{"cidr":{"type":"string"},"icmp":{"type":"boolean"}},"required":["cidr"]}`)
	default: // nmap
		return json.RawMessage(`{"type":"object","properties":{"cidr":{"type":"string"}},"required":["cidr"]}`)
	}
}

func (t *probeAliasTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		CIDR  string `json:"cidr"`
		Ports []int  `json:"ports"`
		Via   string `json:"via"`
		ICMP  bool   `json:"icmp"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	mapped, err := json.Marshal(map[string]any{
		"cidr": a.CIDR, "depth": "L5", "mode": t.mode,
		"via": a.Via, "ports": a.Ports, "icmp": a.ICMP,
	})
	if err != nil {
		return "", err
	}
	out, err := t.inner.Execute(ctx, mapped)
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.oldName, err)
	}
	return "[经由弃用别名，等价 netdev_probe L5/" + t.mode + "]\n" + out, nil
}
