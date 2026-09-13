package netdev

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

func TestAccelProfileForDevice(t *testing.T) {
	nd := config.NetDevConfig{AccelProfiles: []config.NetDevAccelProfile{
		{Accel: "nvidia", SKU: "H20", Cards: 8, VRAMGB: 96, Readiness: "production"},
		{Accel: "nvidia", SKU: "H20×8", Cards: 8, VRAMGB: 96},
		{Accel: "ascend", SKU: "910B", Cards: 8, VRAMGB: 64, Readiness: "production"},
	}}
	long := AccelProfileForDevice(nd, config.NetDevDevice{Model: "SuperServer H20×8 服务器"})
	if long == nil || long.SKU != "H20×8" {
		t.Fatalf("longest-SKU match want H20×8, got %+v", long)
	}
	if p := AccelProfileForDevice(nd, config.NetDevDevice{Model: "Atlas 800I A2 910B*8"}); p == nil || p.Accel != "ascend" {
		t.Fatalf("910B match failed: %+v (大小写/星号不敏感匹配按子串)", p)
	}
	if p := AccelProfileForDevice(nd, config.NetDevDevice{Model: "S2750"}); p != nil {
		t.Fatalf("无匹配应 nil, got %+v", p)
	}
	if p := AccelProfileForDevice(nd, config.NetDevDevice{}); p != nil {
		t.Fatalf("空 model 应 nil, got %+v", p)
	}
}

func TestBuiltinModelCardsSanity(t *testing.T) {
	rows := BuiltinModelCards()
	if len(rows) < 40 {
		t.Fatalf("内置矩阵行数异常少: %d", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		// 量化/硬件族字符集必须与 config 校验一致（用户重存配置不拒收）。
		for _, s := range []string{r.Quant, r.HWFamily} {
			if !strings.EqualFold(s, strings.ToLower(s)) || strings.TrimSpace(s) == "" {
				t.Errorf("row %s: charset %q", r.Name, s)
			}
		}
		if r.ParamsB <= 0 || r.ActiveB > r.ParamsB {
			t.Errorf("row %s: params %v active %v", r.Name, r.ParamsB, r.ActiveB)
		}
		key := r.Name + "/" + r.Quant + "/" + r.HWFamily
		if seen[key] {
			t.Errorf("duplicate builtin row %s", key)
		}
		seen[key] = true
	}
	// 台账验收对照行必须在位：DeepSeek-R1 昇腾 W8A8、Qwen3-30B-A3B 910B、
	// R1-Distill 7B、K2（标 API）、GLM-4.5-Air。
	for _, key := range []string{
		"deepseek-r1/w8a8/ascend-910b", "qwen3-30b-a3b/w8a8/ascend-910b",
		"deepseek-r1-distill-qwen-7b/bf16/nvidia-4090", "kimi-k2/fp8/nvidia-h",
		"glm-4.5-air/bf16/nvidia-4090",
	} {
		if !seen[key] {
			t.Errorf("内置矩阵缺验收对照行 %s", key)
		}
	}
}

func TestResolveModelCardUserOverride(t *testing.T) {
	nd := config.NetDevConfig{ModelCards: []config.NetDevModelCard{
		{Name: "qwen2.5-7b", ParamsB: 7, Quant: "bf16", HWFamily: "nvidia-4090", VRAMGB: 15, Notes: "site-calibrated"},
	}}
	c, ok := ResolveModelCard(nd, "Qwen2.5-7B", "bf16", "nvidia-4090")
	if !ok || c.VRAMGB != 15 {
		t.Fatalf("user row should override builtin: ok=%v %+v", ok, c)
	}
	c, ok = ResolveModelCard(nd, "qwen2.5-72b", "fp8", "nvidia-h")
	if !ok || c.Name != "qwen2.5-72b" {
		t.Fatalf("builtin fallback failed: ok=%v %+v", ok, c)
	}
	if _, ok := ResolveModelCard(nd, "not-a-model", "fp8", "nvidia-h"); ok {
		t.Fatal("unknown model must miss")
	}
}

func TestCheckDeploymentAdvisories(t *testing.T) {
	// 台账验收 1（E4）：H20 档案声明 TP=8×不匹配卡数（cards=4）→ 告警可见。
	h20 := config.NetDevAccelProfile{Accel: "nvidia", SKU: "H20-4卡", Cards: 4, VRAMGB: 96, Readiness: "production"}
	got := strings.Join(CheckDeployment(&h20, config.NetDevModelCard{Name: "qwen2.5-72b", ParamsB: 72, Quant: "fp8", HWFamily: "nvidia-h"}, 8), "\n")
	if !strings.Contains(got, "TP=8 超过机型") {
		t.Errorf("E4 验收：TP 超卡数告警缺失:\n%s", got)
	}

	// 台账验收 2（E7）：DeepSeek-671B BF16 vs 8×64G（tp=8，单机）→ 显存告警+台数。
	a2 := config.NetDevAccelProfile{Accel: "ascend", SKU: "910B", Cards: 8, VRAMGB: 64, Readiness: "production"}
	card, ok := ResolveModelCard(config.NetDevConfig{}, "deepseek-r1", "bf16", "ascend-910b")
	if !ok {
		t.Fatal("deepseek-r1 bf16/ascend-910b 应命中内置矩阵")
	}
	got = strings.Join(CheckDeployment(&a2, card, 8), "\n")
	if !strings.Contains(got, "超出") || !strings.Contains(got, "台") {
		t.Errorf("E7 验收：671B BF16 显存告警缺失:\n%s", got)
	}

	// 台账验收 3（E7）：Qwen3-30B W8A8 vs 910B 单卡 → 通过（无 TP/显存告警，
	// MoE 说明属信息行不算失败）。
	card, ok = ResolveModelCard(config.NetDevConfig{}, "qwen3-30b-a3b", "w8a8", "ascend-910b")
	if !ok {
		t.Fatal("qwen3-30b-a3b w8a8/ascend-910b 应命中内置矩阵")
	}
	var fails []string
	for _, s := range CheckDeployment(&a2, card, 1) {
		if strings.Contains(s, "超出") || strings.Contains(s, "超过机型") || strings.Contains(s, "不同族") {
			fails = append(fails, s)
		}
	}
	if len(fails) != 0 {
		t.Errorf("E7 验收：30B W8A8 单卡应通过, got %v", fails)
	}

	// 硬件族×机型配对：nvidia-h 模型行上昇腾机型 → 不同族告警。
	got = strings.Join(CheckDeployment(&a2, config.NetDevModelCard{Name: "x", ParamsB: 7, Quant: "fp8", HWFamily: "nvidia-h"}, 8), "\n")
	if !strings.Contains(got, "不同族") {
		t.Errorf("跨族告警缺失:\n%s", got)
	}

	// readiness 徽标面：experimental 机型必须提示真机验证。
	exp := config.NetDevAccelProfile{Accel: "enflame", SKU: "S60", Cards: 8, VRAMGB: 48, Readiness: "experimental", Notes: "vllm-gcu 绑定 TopsRider"}
	got = strings.Join(CheckDeployment(&exp, config.NetDevModelCard{Name: "qwen2.5-7b", ParamsB: 7, Quant: "bf16", HWFamily: "enflame-s60"}, 8), "\n")
	if !strings.Contains(got, "experimental") || !strings.Contains(got, "真机验证") {
		t.Errorf("readiness 提示缺失:\n%s", got)
	}

	// 无档案：建议先建档，绝不硬失败。
	got = strings.Join(CheckDeployment(nil, card, 8), "\n")
	if !strings.Contains(got, "建档") {
		t.Errorf("无档案应提示建档:\n%s", got)
	}
}

func TestGPUBoardProfileBadge(t *testing.T) {
	findingsDirOverr = t.TempDir()
	t.Cleanup(func() { findingsDirOverr = "" })
	SetAuditPath(t.TempDir() + "/audit.jsonl")
	t.Cleanup(func() { SetAuditPath("") })
	cfg := config.Default()
	cfg.NetDev.AccelProfiles = []config.NetDevAccelProfile{
		{Accel: "nvidia", SKU: "A100", Cards: 8, VRAMGB: 40, Readiness: "production", Interconnect: "nvlink4"},
		{Accel: "nvidia", SKU: "A100-S", Cards: 1, VRAMGB: 40, Readiness: "production"},
	}
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "g1", Vendor: "linux", GPU: true, Model: "A100×8"},
		{Name: "g2", Vendor: "linux", GPU: true, Model: "A100×8"},
	}
	m := NewManager(cfg)
	t.Cleanup(m.Close)

	healthMu.Lock()
	// g1: 卡数对不上（档案 8，实测 2）→ 偏差告警；显存对上（40GB）。
	healthState["g1"] = DeviceHealth{Device: "g1", Reachable: true, GPUSampled: true,
		GPU: []GPUCard{{Index: 0, MemTotalMB: 40960}, {Index: 1, MemTotalMB: 40960}}, Interfaces: []IfHealth{}}
	// g2: 全对上 → 无告警。
	healthState["g2"] = DeviceHealth{Device: "g2", Reachable: true, GPUSampled: true,
		GPU: []GPUCard{{Index: 0, MemTotalMB: 40960}}, Interfaces: []IfHealth{}}
	healthMu.Unlock()
	t.Cleanup(func() {
		healthMu.Lock()
		healthState = map[string]DeviceHealth{}
		healthMu.Unlock()
	})
	cfg.NetDev.Devices[1].Model = "A100-S" // 单卡机型档案，与实测 1 卡一致

	b := m.BuildGPUBoard()
	var g1, g2 *GPUBoardDevice
	for i := range b.Devices {
		switch b.Devices[i].Device {
		case "g1":
			g1 = &b.Devices[i]
		case "g2":
			g2 = &b.Devices[i]
		}
	}
	if g1 == nil || g2 == nil {
		t.Fatalf("board devices missing: %+v", b.Devices)
	}
	if g1.ProfileSKU != "A100" || g1.Readiness != "production" || g1.Interconn != "nvlink4" {
		t.Errorf("g1 badge want A100/production/nvlink4, got %+v", g1)
	}
	found := false
	for _, s := range g1.ProfileAdvisories {
		if strings.Contains(s, "借调/降配") {
			found = true
		}
	}
	if !found {
		t.Errorf("g1 卡数偏差告警缺失: %v", g1.ProfileAdvisories)
	}
	if len(g2.ProfileAdvisories) != 0 {
		t.Errorf("g2 不应告警: %v", g2.ProfileAdvisories)
	}
}
