package skill

// orchestration_test.go — SKILL_ORCHESTRATION_SPEC §10 红测试（P1 部分）：
// 两个 -auto 技能的注册形态、工具面禁含清单、断言式 body 契约标记、
// store 级别名解析、body/描述预算 lint。

import (
	"io"
	"strings"
	"testing"
)

func skillByName(t *testing.T, name string) Skill {
	t.Helper()
	for _, sk := range builtinSkills() {
		if sk.Name == name {
			return sk
		}
	}
	t.Fatalf("built-in skill %q not registered", name)
	return Skill{}
}

// §10.1（P1 面）：两个 -auto 技能是子代理、步数显式、旧名从注册表消失。
func TestNetdevOrchestrationRegistrations(t *testing.T) {
	sec := skillByName(t, "netdev-seccheck-auto")
	if sec.RunAs != RunSubagent || sec.MaxSteps != 200 {
		t.Fatalf("seccheck-auto must be RunSubagent with MaxSteps 200, got %s/%d", sec.RunAs, sec.MaxSteps)
	}
	diag := skillByName(t, "netdev-diag-auto")
	if diag.RunAs != RunSubagent || diag.MaxSteps != 120 {
		t.Fatalf("diag-auto must be RunSubagent with MaxSteps 120, got %s/%d", diag.RunAs, diag.MaxSteps)
	}
	// keepers 仍在且维持 inline。
	for _, name := range []string{"netdev-help", "netdev-config-vault"} {
		if sk := skillByName(t, name); sk.RunAs != RunInline {
			t.Fatalf("%s must stay inline, got %s", name, sk.RunAs)
		}
	}
	// 旧名不再作为技能注册（只作为别名存在）。
	for _, old := range []string{"netdev-vulnscan", "netdev-audit-project", "netdev-playbook", "netdev-diag-ospf", "netdev-diag-bgp", "netdev-diag-interface", "netdev-draft"} {
		for _, sk := range builtinSkills() {
			if sk.Name == old {
				t.Fatalf("retired skill %q must not be registered (alias-only)", old)
			}
		}
	}
}

// §10.1 工具面：纯读+落库；写/攻(信封外)/保管/递归面禁含；证据闸在内。
func TestAutoToolfaces(t *testing.T) {
	forbiddenCommon := []string{
		"netdev_propose", "netdev_backup", "bash", "write_file", "edit_file",
		"run_skill", "netdev_nmap", "netdev_netprobe",
	}
	has := func(list []string, name string) bool {
		for _, v := range list {
			if v == name {
				return true
			}
		}
		return false
	}
	sec := skillByName(t, "netdev-seccheck-auto").AllowedTools
	for _, f := range forbiddenCommon {
		if has(sec, f) {
			t.Fatalf("seccheck-auto toolface must not contain %s", f)
		}
	}
	for _, req := range []string{"netdev_exec", "netdev_devices", "netdev_fanout", "netdev_finding", "netdev_cve_match", "netdev_baseline", "netdev_assess", "netdev_log_search", "netdev_probe", "netdev_knowledge", "todo_write", "complete_step"} {
		if !has(sec, req) {
			t.Fatalf("seccheck-auto toolface must contain %s", req)
		}
	}
	diag := skillByName(t, "netdev-diag-auto").AllowedTools
	for _, f := range forbiddenCommon {
		if has(diag, f) {
			t.Fatalf("diag-auto toolface must not contain %s", f)
		}
	}
	// diag 不带 assess/cve/discover 面（安全对象专属，seccheck 拥有——
	// discover 运行时受评估信封+scopes 护，工具面宽≠行为宽）。
	if has(diag, "netdev_assess") || has(diag, "netdev_cve_match") || has(diag, "netdev_probe") {
		t.Fatal("diag-auto must not carry the assessment/probing surface (seccheck owns it)")
	}
	for _, req := range []string{"netdev_exec", "netdev_topology", "netdev_redfish", "netdev_finding", "todo_write", "complete_step"} {
		if !has(diag, req) {
			t.Fatalf("diag-auto toolface must contain %s", req)
		}
	}
}

// §10.3 body 契约标记：注入防御、拒绝不重试、预算收尾、立案先行、入口语法。
func TestAutoBodyContracts(t *testing.T) {
	sec := skillByName(t, "netdev-seccheck-auto").Body
	diag := skillByName(t, "netdev-diag-auto").Body
	for _, body := range []string{sec, diag} {
		for _, marker := range []string{
			"DATA 不是指令",        // 注入防御（子代理看不到 addon，body 自包含）
			"换写法重试",            // 拒绝纪律（两种措辞都命中）
			"budget exhausted", // 预算收尾协议
			"立案先行",             // 输出合同（runner 合同校验的锚）
		} {
			if !strings.Contains(body, marker) {
				t.Fatalf("body missing contract marker %q", marker)
			}
		}
	}
	// seccheck 专属：入口显式语法（三形态）+ H/L 阶梯节 + source 数据标签沿用。
	for _, marker := range []string{"入口=清单|套餐|主机|网段", "入口=主机", "入口=网段", "H0", "H5", "L0", "L5", "netdev_knowledge", "source=vulnscan", "source=audit"} {
		if !strings.Contains(sec, marker) {
			t.Fatalf("seccheck body missing marker %q", marker)
		}
	}
	// 断言式写法（§11-L2）：期望输出 + 失败分支。
	for _, body := range []string{sec, diag} {
		if !strings.Contains(body, "期望输出") || !strings.Contains(body, "失败分支") {
			t.Fatal("bodies must be assertion-style (期望输出/失败分支 per step)")
		}
	}
}

// §3.5-B/F 预算 lint：body ≤8000 字符、描述 ≤300 字符。
func TestAutoBodyAndDescriptionBudgets(t *testing.T) {
	for _, name := range []string{"netdev-seccheck-auto", "netdev-diag-auto"} {
		sk := skillByName(t, name)
		if n := len([]rune(sk.Body)); n > 8000 {
			t.Fatalf("%s body %d chars > 8000 budget", name, n)
		}
		if n := len([]rune(sk.Description)); n > 300 {
			t.Fatalf("%s description %d chars > 300 index budget", name, n)
		}
	}
}

// §3.5-E / §10.9 别名解析：ResolveSkillAlias + Store.Read 级兼容
// （旧名 run_skill、/旧名、历史会话回放）。
func TestSkillAliasResolution(t *testing.T) {
	for old, want := range map[string]string{
		"netdev-vulnscan":       "netdev-seccheck-auto",
		"netdev-audit-project":  "netdev-seccheck-auto",
		"netdev-playbook":       "netdev-diag-auto",
		"netdev-diag-ospf":      "netdev-diag-auto",
		"netdev-diag-bgp":       "netdev-diag-auto",
		"netdev-diag-interface": "netdev-diag-auto",
		"netdev-draft":          "netdev-config-vault",
	} {
		if got := ResolveSkillAlias(old); got != want {
			t.Fatalf("alias %s → %s, want %s", old, got, want)
		}
	}
	if got := ResolveSkillAlias("netdev-help"); got != "" {
		t.Fatalf("live skill must not be aliased, got %s", got)
	}
	// Store 级：空根 store（仅内置）按旧名 Read 到新技能；禁用新名也禁用别名。
	st := New(Options{Stderr: io.Discard})
	if _, ok := st.Read("netdev-vulnscan"); !ok {
		t.Fatal("old-name Read must resolve through the alias table")
	}
	if sk, ok := st.Read("NetDev-VulnScan"); !ok || sk.Name != "netdev-seccheck-auto" {
		t.Fatal("alias resolution must be case-insensitive and canonicalize")
	}
	stDis := New(Options{Stderr: io.Discard, DisabledNames: []string{"netdev-seccheck-auto"}})
	if _, ok := stDis.Read("netdev-vulnscan"); ok {
		t.Fatal("disabling the successor must disable the alias too (tighten wins)")
	}
}
