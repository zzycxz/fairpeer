package netdev

// probetool_test.go — 测绘三合一红测试（SKILL_ORCHESTRATION_SPEC §6.1）：
// 目标派生（L3 网关候选 / L4 采样点）、引擎选择与回退（mode 显式 >
// auto：netprobe→nmap→隧道）、depth 分发与闸门语义（L3/L4 scopes 护；
// L5 信封护——无信封/无 scopes 时零发包拒绝）。全部无网络。

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev/knowledge"
	"github.com/zzycxz/fairpeer/internal/tool"
)

func TestGatewayAndSampleTargetsFromCIDR(t *testing.T) {
	priors := []string{".1", ".254", ".249"}
	got := gatewayTargetsFromCIDR("10.30.2.0/24", priors)
	want := []string{"10.30.2.1", "10.30.2.254", "10.30.2.249"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("gateway candidates: got %v want %v", got, want)
	}
	// 空 priors → 安全缺省（两端网关）。
	got = gatewayTargetsFromCIDR("10.30.2.0/24", nil)
	if strings.Join(got, ",") != "10.30.2.1,10.30.2.254" {
		t.Fatalf("default candidates wrong: %v", got)
	}
	// L4：区间取起点、random 落在段内、≤ 点数上限。
	pts := []string{".1", ".2-.19", ".100", "random"}
	sampled := sampleTargetsFromCIDR("10.30.2.0/24", pts)
	if len(sampled) != 4 {
		t.Fatalf("expected 4 sampled points, got %v", sampled)
	}
	if sampled[0] != "10.30.2.1" || sampled[1] != "10.30.2.2" || sampled[2] != "10.30.2.100" {
		t.Fatalf("sample points wrong: %v", sampled)
	}
	if !strings.HasPrefix(sampled[3], "10.30.2.") || sampled[3] == "10.30.2.1" {
		t.Fatalf("random point must be a distinct in-segment host: %v", sampled[3])
	}
	// 非 /24 采样拒绝（采样语义只在 /24 上定义）。
	if sampleTargetsFromCIDR("10.30.0.0/16", pts) != nil {
		t.Fatal("sampling must refuse non-/24 nets")
	}
}

func TestPickProbeEngineFallback(t *testing.T) {
	cfg := &config.Config{}
	if got := pickProbeEngine(cfg, ""); got != "tunnel" {
		t.Fatalf("auto with no binaries → tunnel, got %s", got)
	}
	if got := pickProbeEngine(cfg, "nmap"); got != "nmap" {
		t.Fatalf("explicit nmap must win, got %s", got)
	}
	cfg.NetDev.Discovery.NmapPath = `C:\tools\nmap.exe`
	if got := pickProbeEngine(cfg, ""); got != "nmap" {
		t.Fatalf("auto with nmap configured → nmap, got %s", got)
	}
	cfg.NetDev.Discovery.NetprobePath = "/opt/netprobe"
	if got := pickProbeEngine(cfg, ""); got != "netprobe" {
		t.Fatalf("auto prefers netprobe when both configured, got %s", got)
	}
	if got := pickProbeEngine(cfg, "tunnel"); got != "tunnel" {
		t.Fatalf("explicit tunnel overrides auto, got %s", got)
	}
}

// L5 无评估信封 → 拒绝且零发包；L3/L4 无 scopes → 拒绝且零发包。
func TestProbeDepthGatesRefuseWithoutEnvelopeOrScopes(t *testing.T) {
	writeAuthTestEnv(t)
	cfg := &config.Config{}
	cfg.NetDev.Enabled = true
	cfg.NetDev.Devices = []config.NetDevDevice{labDevice()}
	m := NewManager(cfg)
	pt := &probeTool{m: m}

	_, err := pt.Execute(t.Context(), []byte(`{"cidr":"10.30.2.0/24","depth":"L5","mode":"nmap"}`))
	if err == nil || !strings.Contains(err.Error(), "engagement") {
		t.Fatalf("L5 without envelope must refuse citing the envelope: %v", err)
	}
	// L4：目标在配置的 scopes 之外 → 拒绝且零发包（白名单对模型永不可关；
	// 空 scopes 是用户的显式放行，不是拒绝——闸门语义与旧 discover 一致）。
	cfg.NetDev.Discovery.Scopes = []string{"192.0.2.0/24"}
	_, err = pt.Execute(t.Context(), []byte(`{"cidr":"10.30.2.0/24","depth":"L4"}`))
	if err == nil || !strings.Contains(err.Error(), "scopes") {
		t.Fatalf("L4 out-of-scope must refuse citing scopes: %v", err)
	}
}

// 模型面收敛：netdev profile 注册表只含 netdev_probe，三个旧名不再注册
// （引擎封装保留在内部，模型看不到独立探测工具）。
func TestProbeSurfaceMergedInRegistry(t *testing.T) {
	writeAuthTestEnv(t)
	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{labDevice()}
	reg := tool.NewRegistry()
	RegisterTools(reg, cfg)
	if _, ok := reg.Get("netdev_probe"); !ok {
		t.Fatal("netdev_probe must be registered")
	}
	// 旧名不再作为独立引擎工具存在——只以弃用别名（probeAliasTool）保留，
	// 供已装用户技能的旧调用兼容；闸门语义经别名原样透传。
	for _, old := range []string{"netdev_discover", "netdev_nmap", "netdev_netprobe"} {
		got, ok := reg.Get(old)
		if !ok {
			t.Fatalf("compat alias %q must stay registered for installed user skills", old)
		}
		if _, isAlias := got.(*probeAliasTool); !isAlias {
			t.Fatalf("%q must be a deprecated probeAliasTool, not a standalone engine tool", old)
		}
	}
}

// 可见面收口（修正版）：唯一隐藏的是 netdev_knowledge（纯内部数据通道，
// 主循环零场景）；probe/assess 是自带闸门的宏操作（信封/ scopes 在工具
// 里），必须对主循环可见——快查与 inline 用户技能（netdev-security-
// assessment 的测绘/弱口令阶段）依赖可见性，"不可见"不是第二种闸门。
func TestOrchestrationOnlyToolsHiddenFromMainLoop(t *testing.T) {
	writeAuthTestEnv(t)
	cfg := &config.Config{}
	cfg.NetDev.Enabled = true
	cfg.NetDev.Devices = []config.NetDevDevice{labDevice()}
	reg := tool.NewRegistry()
	RegisterTools(reg, cfg)

	// knowledge：藏（去噪，不破门）。
	if !reg.IsHidden("netdev_knowledge") {
		t.Fatal("netdev_knowledge must be hidden (internal data channel, zero main-loop scenarios)")
	}
	if _, ok := reg.Get("netdev_knowledge"); !ok {
		t.Fatal("hidden must stay resolvable for subagent FilterRegistry")
	}
	inSchema := map[string]bool{}
	for _, s := range reg.Schemas() {
		inSchema[s.Name] = true
	}
	if inSchema["netdev_knowledge"] {
		t.Fatal("netdev_knowledge must not ride the main-loop schema")
	}
	// probe/assess：可见（闸门在工具里；inline 用户技能直调依赖此面）。
	for _, name := range []string{"netdev_probe", "netdev_assess",
		"netdev_exec", "netdev_devices", "netdev_fanout", "netdev_backup",
		"netdev_propose", "netdev_baseline", "netdev_cve_match"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("%s must stay registered", name)
		}
		if reg.IsHidden(name) {
			t.Fatalf("%s must stay visible — its gate lives in the tool, invisibility is not a second gate", name)
		}
		if !inSchema[name] {
			t.Fatalf("%s must ride the main-loop schema (quick-path & inline user skills)", name)
		}
	}
}

// 旧名兼容别名：注册在案、参数映射到 probe 的 L5 显式 mode、闸门语义
// 原样透传（discover=出界 scopes 拒；netprobe=无信封拒）——已装用户
// 技能的旧调用不断。
func TestProbeAliasesPreserveOldCalls(t *testing.T) {
	writeAuthTestEnv(t)
	cfg := &config.Config{}
	cfg.NetDev.Enabled = true
	cfg.NetDev.Discovery.Scopes = []string{"192.0.2.0/24"}
	cfg.NetDev.Devices = []config.NetDevDevice{labDevice()}
	reg := tool.NewRegistry()
	RegisterTools(reg, cfg)
	for _, name := range []string{"netdev_discover", "netdev_nmap", "netdev_netprobe"} {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("compat alias %s must stay registered for installed user skills", name)
		}
	}
	// discover（隧道引擎）：出界 → scopes 拒（零发包，语义原样）。
	d := &probeAliasTool{oldName: "netdev_discover", mode: "tunnel", inner: &probeTool{m: NewManager(cfg)}}
	if _, err := d.Execute(t.Context(), []byte(`{"cidr":"10.30.2.0/24"}`)); err == nil || !strings.Contains(err.Error(), "scopes") {
		t.Fatalf("discover alias must pass the scope gate through: %v", err)
	}
	// netprobe（信封引擎）：无信封 → engagement 拒。
	n := &probeAliasTool{oldName: "netdev_netprobe", mode: "netprobe", inner: &probeTool{m: NewManager(cfg)}}
	if _, err := n.Execute(t.Context(), []byte(`{"cidr":"192.0.2.0/24"}`)); err == nil || !strings.Contains(err.Error(), "engagement") {
		t.Fatalf("netprobe alias must pass the envelope gate through: %v", err)
	}
}

// 批 D②审查修复：形状注解的网关位以 gateway_candidates 交集为准（.254 是
// 网关候选、.2 不是），ports 型信号命中按表给角色候选。
func TestProbeL4Shape(t *testing.T) {
	priors := &segmentPriors{
		GatewayCandidates: []string{".1", ".254"},
		RoleSignals: []struct {
			Ports           []string `yaml:"ports"`
			PrinterOUIDense bool     `yaml:"printer_oui_dense"`
			SNMPHit         bool     `yaml:"snmp_community_hit"`
			Role            string   `yaml:"role"`
			Action          string   `yaml:"action"`
		}{
			{Ports: []string{"445", "88"}, Role: "域段", Action: "优先核查队列"},
		},
	}
	// 仅网关位活（.1+.254 都在候选集）→ 候选已验证段，而非「多点分布」。
	out := probeL4Shape("10.30.2.0/24", 2, 5,
		map[string]bool{"10.30.2.1": true, "10.30.2.254": true},
		[]string{"10.30.2.1", "10.30.2.254"},
		map[int]bool{22: true}, priors)
	if !strings.Contains(out, "仅网关位活") {
		t.Fatalf(".1+.254 hits must read gateway-only: %q", out)
	}
	if !strings.Contains(out, "printer_oui_dense") {
		t.Fatalf("non-ports signals must be honestly declared out of scope: %q", out)
	}
	// 445 开放 → 域段信号行出现。
	out = probeL4Shape("10.30.2.0/24", 2, 5,
		map[string]bool{"10.30.2.1": true},
		[]string{"10.30.2.1", "10.30.2.100"},
		map[int]bool{22: true, 445: true}, priors)
	if !strings.Contains(out, "域段") {
		t.Fatalf("445 hit must fire the role signal: %q", out)
	}
}

// D2 审查补钉：真实 segment-priors.yaml → segmentPriors 的往返——具名字段
// 的 yaml tag 不受编译保护，写错会静默零值（role_signals 永不触发）。
func TestLoadSegmentPriorsShipped(t *testing.T) {
	knowledge.SetStateDir(t.TempDir())
	p, err := loadSegmentPriors()
	if err != nil {
		t.Fatal(err)
	}
	if len(p.GatewayCandidates) == 0 || len(p.SamplePoints) == 0 {
		t.Fatalf("gateway/sample points must round-trip: %+v", p)
	}
	if len(p.RoleSignals) == 0 {
		t.Fatalf("role_signals must round-trip (445/88 域段信号依赖它): %+v", p)
	}
	hasDomain := false
	for _, s := range p.RoleSignals {
		for _, port := range s.Ports {
			if port == "445" {
				hasDomain = true
			}
		}
	}
	if !hasDomain {
		t.Fatalf("445 域段信号 missing from shipped table: %+v", p.RoleSignals)
	}
}
