package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestValidateNetDev(t *testing.T) {
	ok := NetDevConfig{
		Enabled: true,
		Hops:    []NetDevHop{{Name: "l1", Host: "202.1.1.1"}},
		Devices: []NetDevDevice{{
			Name: "core", Vendor: "huawei", OS: "vrp8", Address: "10.0.0.1",
			Via: []string{"l1"}, Group: "core",
		}},
		Groups:    []NetDevGroup{{Name: "core", Policy: NetDevPolicyReadOnly}},
		Discovery: NetDevDiscovery{Scopes: []string{"10.30.0.0/16"}},
	}
	if err := ValidateNetDev(ok); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	bad := []NetDevConfig{
		{Enabled: true, DefaultMode: "yolo"},
		{Enabled: true, Devices: []NetDevDevice{{Name: "", Address: "10.0.0.1"}}},
		{Enabled: true, Devices: []NetDevDevice{{Name: "a"}}},                                       // no address
		{Enabled: true, Devices: []NetDevDevice{{Name: "a", Address: "x", Vendor: "juniper"}}},      // unknown vendor
		{Enabled: true, Devices: []NetDevDevice{{Name: "a", Address: "x", Encoding: "big5"}}},       // bad encoding
		{Enabled: true, Devices: []NetDevDevice{{Name: "a", Address: "x", Via: []string{"ghost"}}}}, // unknown hop
		{Enabled: true, Devices: []NetDevDevice{{Name: "a", Address: "x", Group: "ghost"}}},         // unknown group
		{Enabled: true, Hops: []NetDevHop{{Name: "l1", ProxyJump: "l1"}}},                           // self jump
		{Enabled: true, Groups: []NetDevGroup{{Name: "g", Policy: "full-access"}}},                  // bad policy
		{Enabled: true, Discovery: NetDevDiscovery{Scopes: []string{"10.30.0.0/64"}}},               // bad CIDR
		// Duplicate device names.
		{Enabled: true, Devices: []NetDevDevice{
			{Name: "a", Address: "x"}, {Name: "a", Address: "y"},
		}},
	}
	for i, c := range bad {
		if err := ValidateNetDev(c); err == nil {
			t.Fatalf("case %d: invalid config accepted", i)
		}
	}
}

// TestNetDevPinnedToUserConfig is the supply-chain guard: a project
// fairpeer.toml declaring [netdev] devices must NOT survive the merge, while
// the user config's own [netdev] must.
func TestNetDevPinnedToUserConfig(t *testing.T) {
	// os.UserConfigDir on Windows = %AppData%; on Unix = $XDG_CONFIG_HOME|$HOME/.config.
	cfgRoot := t.TempDir()
	t.Setenv("APPDATA", cfgRoot)
	t.Setenv("XDG_CONFIG_HOME", cfgRoot)
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	// Minimal isolated user config carrying the real [netdev].
	userDir := filepath.Join(cfgRoot, "fairpeer")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userTOML := `
[netdev]
enabled = true

[[netdev.hops]]
name = "user-hop"
host = "203.0.113.1"

[[netdev.devices]]
name = "user-device"
address = "10.99.0.1"
via = ["user-hop"]
`
	if err := os.WriteFile(filepath.Join(userDir, "config.toml"), []byte(userTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	// A cloned project that tries to inject its own device + scope.
	proj := t.TempDir()
	projectTOML := `
[netdev]
enabled = true

[[netdev.devices]]
name = "evil-device"
address = "198.51.100.7"

[netdev.discovery]
scopes = ["198.51.100.0/24"]
`
	if err := os.WriteFile(filepath.Join(proj, "fairpeer.toml"), []byte(projectTOML), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoot(proj)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if !cfg.NetDev.Enabled {
		t.Fatal("user [netdev].enabled lost")
	}
	if _, ok := cfg.NetDevDeviceByName("user-device"); !ok {
		t.Fatal("user device missing after pin")
	}
	if _, ok := cfg.NetDevDeviceByName("evil-device"); ok {
		t.Fatal("project-injected device survived the pin — supply-chain hole")
	}
	if len(cfg.NetDev.Discovery.Scopes) != 0 {
		t.Fatalf("project-injected scopes survived: %v", cfg.NetDev.Discovery.Scopes)
	}
}

// The builtin netdev profile is a FLOOR: a user [[profiles]] override can
// retune model/skills but can NEVER clear the tool seal or re-enable project
// instructions (an accidental tool_scope="" would silently unseal).
func TestNetDevProfileOverrideFloored(t *testing.T) {
	c := Default()
	c.Profiles = []Profile{{
		Name:                    "netdev",
		Model:                   "some-model",
		ToolScope:               "",               // attempted unseal
		LoadProjectInstructions: &[]bool{true}[0], // attempted re-enable
	}}
	p, err := c.ResolveProfile("netdev")
	if err != nil {
		t.Fatal(err)
	}
	if !p.SealsExecutionTools() {
		t.Fatal("user override cleared the tool seal")
	}
	if !p.SkipProjectInstructions() {
		t.Fatal("user override re-enabled project instructions")
	}
	if p.Model != "some-model" {
		t.Fatal("model override should still work (floor is security-only)")
	}
}

// Projects are site-level scopes; validation must catch duplicate names and
// references to groups that don't exist (the title-bar switcher trusts them).
func TestValidateNetDevProjects(t *testing.T) {
	base := func() NetDevConfig {
		return NetDevConfig{
			Enabled: true,
			Groups:  []NetDevGroup{{Name: "核心"}, {Name: "接入"}},
			Projects: []NetDevProject{
				{Name: "一号机房", Groups: []string{"核心"}},
			},
		}
	}
	if err := ValidateNetDev(base()); err != nil {
		t.Fatalf("valid project rejected: %v", err)
	}
	dup := base()
	dup.Projects = append(dup.Projects, NetDevProject{Name: "一号机房"})
	if err := ValidateNetDev(dup); err == nil {
		t.Fatal("duplicate project name accepted")
	}
	ghost := base()
	ghost.Projects = append(ghost.Projects, NetDevProject{Name: "二号机房", Groups: []string{"不存在"}})
	if err := ValidateNetDev(ghost); err == nil {
		t.Fatal("project referencing unknown group accepted")
	}
}

// Inventory names become file names in the backup/golden vaults — path
// separators and ".." must be rejected at the door.
func TestValidateNetDevRejectsPathLikeNames(t *testing.T) {
	base := func() NetDevConfig {
		return NetDevConfig{
			Enabled: true,
			Devices: []NetDevDevice{{Name: "core-sw-1", Vendor: "huawei", Address: "10.0.0.1"}},
			Hops:    []NetDevHop{{Name: "bastion", Host: "1.2.3.4"}},
		}
	}
	for _, bad := range []string{"../evil", "a/b", `a\b`, "a b", ".hidden", strings.Repeat("x", 65)} {
		nd := base()
		nd.Devices[0].Name = bad
		if err := ValidateNetDev(nd); err == nil {
			t.Errorf("device name %q should be rejected", bad)
		}
		nd = base()
		nd.Hops[0].Name = bad
		if err := ValidateNetDev(nd); err == nil {
			t.Errorf("hop name %q should be rejected", bad)
		}
	}
	for _, good := range []string{"core-sw-1", "SW_2.Edge@bj"} {
		nd := base()
		nd.Devices[0].Name = good
		if err := ValidateNetDev(nd); err != nil {
			t.Errorf("device name %q should pass: %v", good, err)
		}
	}
}

// TOML 整数字面量 → float64 字段的解码契约（BurntSushi unifyFloat64 接受
// |n| ≤ 2^53 的整数）：用户 TOML 里 value = 85 必须解码为精确 85.0——
// 逐行精读 P3-10 要求的解码路径覆盖（此前只测过 Go 字面量构造）。
func TestNetDevAlertRuleTOMLIntToFloat(t *testing.T) {
	var cfg struct {
		NetDev NetDevConfig `toml:"netdev"`
	}
	doc := `
[netdev]
[[netdev.alert_rules]]
name = "hot"
metric = "gpu.temp"
op = ">="
value = 85
severity = "warning"
enabled = true
for_rounds = 3
`
	if _, err := toml.Decode(doc, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(cfg.NetDev.AlertRules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(cfg.NetDev.AlertRules))
	}
	r := cfg.NetDev.AlertRules[0]
	if r.Value != 85.0 {
		t.Errorf("value want exactly 85.0, got %v", r.Value)
	}
	if r.ForRounds != 3 {
		t.Errorf("for_rounds want 3, got %d", r.ForRounds)
	}
	if err := ValidateNetDev(cfg.NetDev); err != nil {
		t.Errorf("validate: %v", err)
	}
}

// preset_key 唯一性（GAPS E2）：同 key 双规则硬失败——向导按 key 去重的语义
// 依赖唯一性，重复即编辑错误。
func TestNetDevAlertRulePresetKeyUnique(t *testing.T) {
	ok := NetDevConfig{
		Enabled: true,
		AlertRules: []NetDevAlertRule{
			{Name: "a", Metric: "gpu.temp", Op: ">=", Value: 85, Enabled: true, PresetKey: "gpu-hot"},
			{Name: "b", Metric: "gpu.temp", Op: ">=", Value: 90, Enabled: true, PresetKey: "other"},
		},
	}
	if err := ValidateNetDev(ok); err != nil {
		t.Fatalf("distinct keys must pass: %v", err)
	}
	dup := ok
	dup.AlertRules[1].PresetKey = "gpu-hot"
	if err := ValidateNetDev(dup); err == nil || !strings.Contains(err.Error(), "duplicate preset_key") {
		t.Fatalf("duplicate preset_key must fail, got %v", err)
	}
	// 空 key 不参与唯一性（手工规则无 key 合法并存）。
	manual := ok
	manual.AlertRules[1].PresetKey = ""
	if err := ValidateNetDev(manual); err != nil {
		t.Fatalf("empty preset_key must not collide: %v", err)
	}
}

// 轮3覆盖 P2：项目 deny/allow 前缀校验层归一化（大写/多空白就地收敛，空条目拒）。
func TestProjectPrefixNormalization(t *testing.T) {
	nd := NetDevConfig{Enabled: true, Groups: []NetDevGroup{{Name: "g1"}}, Projects: []NetDevProject{
		{Name: "P", Groups: []string{"g1"}, Deny: []string{"  Systemctl   STOP "}, Allow: []string{"undo  stp region"}},
	}}
	if err := ValidateNetDev(nd); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	got := nd.Projects[0]
	if got.Deny[0] != "systemctl stop" || got.Allow[0] != "undo stp region" {
		t.Errorf("prefixes must normalize in place, got deny=%q allow=%q", got.Deny[0], got.Allow[0])
	}
	bad := nd
	bad.Projects = []NetDevProject{{Name: "P", Groups: []string{"g1"}, Deny: []string{"  "}}}
	if err := ValidateNetDev(bad); err == nil {
		t.Error("empty prefix entry must be refused")
	}
}
