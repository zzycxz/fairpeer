package config

import (
	"strings"
	"testing"
)

// netdev addon 的入口形态路由契约（ORCHESTRATION §3.5-A 的最后一公里）：
// 四种入口（清单|套餐|主机|网段）都有委托行，且网段入口的扩半径确认纪律
// 在委托侧成文——子代理一趟跑完不能中途问用户，半径授权只能发生在主循环
// 委托之前（BLUETEAM_SKILL_SPEC §8③）。禁用 seccheck 时三行委托一起剪除。
func TestNetdevAddonEntryRoutingAndRadiusDiscipline(t *testing.T) {
	addon := NetdevPromptAddon(nil)
	for _, marker := range []string{
		`run_skill("netdev-seccheck-auto"`,
		"入口=主机", "入口=网段", "H0-H5", "L0-L5",
		"扩半径确认在委托侧",     // 确认点在主循环不在子代理
		"绝不把无范围的裸 IP 直接甩给子代理", // 裸 IP 不许无范围委托
	} {
		if !strings.Contains(addon, marker) {
			t.Fatalf("netdev addon missing routing marker %q", marker)
		}
	}

	// 禁用 seccheck-auto：三行 seccheck 委托行全部剪除，diag 行保留。
	pruned := NetdevPromptAddon([]string{"netdev-seccheck-auto"})
	if strings.Contains(pruned, `run_skill("netdev-seccheck-auto"`) {
		t.Fatal("disabled seccheck must drop all its routing rows")
	}
	if !strings.Contains(pruned, `run_skill("netdev-diag-auto"`) {
		t.Fatal("diag routing row must survive seccheck pruning")
	}
	// 扩半径纪律提到的是委托行为本身，不指向已禁用技能——保留不算错，
	// 但"入口=网段"的委托行必须没了（否则引导去调一个不存在的技能）。
	if strings.Contains(pruned, "入口=网段 目标=IP") {
		t.Fatal("segment-entry delegation row must be pruned with seccheck disabled")
	}
}
