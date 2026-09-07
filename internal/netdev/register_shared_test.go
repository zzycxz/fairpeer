package netdev

import (
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/tool"
)

// The agent's netdev tools and the desktop bridge must share ONE Manager:
// NetDevTurnBegin's budget reset and NetDevEmergencyStop's KillAllConnections
// only reach the tool side if RegisterTools hands out SharedManager. A private
// NewManager here was the split-brain that silently disabled both guardrails.
func TestRegisterToolsUsesSharedManager(t *testing.T) {
	sharedMu.Lock()
	saved := shared
	shared = nil
	sharedMu.Unlock()
	t.Cleanup(func() {
		sharedMu.Lock()
		shared = saved
		sharedMu.Unlock()
	})

	cfg := config.Default()
	reg := tool.NewRegistry()
	RegisterTools(reg, cfg)

	for _, name := range []string{"netdev_exec", "netdev_probe", "netdev_topology", "netdev_propose", "netdev_netconf", "netdev_baseline"} {
		got, ok := reg.Get(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		var m *Manager
		switch tt := got.(type) {
		case *execTool:
			m = tt.m
		case *probeTool:
			m = tt.m
		case *topologyTool:
			m = tt.m
		case *proposeTool:
			m = tt.m
		case *netconfTool:
			m = tt.m
		case *baselineTool:
			m = tt.m
		default:
			t.Fatalf("%s has unexpected tool type %T", name, got)
		}
		if m != SharedManager(cfg) {
			t.Fatalf("%s holds a private Manager — budget reset / emergency stop miss the agent path", name)
		}
	}
}

// TestRegisterToolsScenarioOrder pins K3 (SCENARIO_SPEC): 注册顺序=场景分组
// （诊断→评估测绘→主机中间件→变更保管→知识日志），组序即 system prompt 的
// 工具清单顺序。改动分组时同步 docs/SCENARIO_CAPABILITY_MAP.md 工具列。
func TestRegisterToolsScenarioOrder(t *testing.T) {
	sharedMu.Lock()
	saved := shared
	shared = nil
	sharedMu.Unlock()
	t.Cleanup(func() {
		sharedMu.Lock()
		shared = saved
		sharedMu.Unlock()
	})

	reg := tool.NewRegistry()
	RegisterTools(reg, config.Default())
	names := reg.Names()

	// 每组的第一个工具按组序出现；组内顺序由各组语义决定，这里只钉组头。
	groupHeads := []string{
		"netdev_exec",    // ① 诊断
		"netdev_assess",  // ② 评估/测绘
		"netdev_triage",  // ③ 主机与中间件
		"netdev_propose", // ④ 变更保管
		"netdev_finding", // ⑤ 知识/日志横切
	}
	last := -1
	for _, head := range groupHeads {
		idx := -1
		for i, n := range names {
			if n == head {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Fatalf("group head %s not registered", head)
		}
		if idx <= last {
			t.Fatalf("group order broken: %s at %d after previous head at %d (names=%v)", head, idx, last, names)
		}
		last = idx
	}
}
