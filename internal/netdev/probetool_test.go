package netdev

// probetool_test.go — 测绘三合一红测试（SKILL_ORCHESTRATION_SPEC §6.1）：
// 目标派生（L3 网关候选 / L4 采样点）、引擎选择与回退（mode 显式 >
// auto：netprobe→nmap→隧道）、depth 分发与闸门语义（L3/L4 scopes 护；
// L5 信封护——无信封/无 scopes 时零发包拒绝）。全部无网络。

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
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
	for _, old := range []string{"netdev_discover", "netdev_nmap", "netdev_netprobe"} {
		if _, ok := reg.Get(old); ok {
			t.Fatalf("old probing tool %q must no longer be registered (merged into netdev_probe)", old)
		}
	}
}
