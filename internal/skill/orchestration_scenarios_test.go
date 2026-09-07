package skill

// orchestration_scenarios_test.go — CI mock 场景组第一层（SKILL_ORCHESTRATION
//_SPEC §11-L6 的 runner-mock 切片）：用 mock SubagentRunner 驱动 run_skill 的
// 真实分发路径，验收两个 -auto 技能的三入口调度、别名兼容、参数契约与
// findings-first 合同谓词。LLM mock-provider × fake-driver 的全链路 e2e 属
// 深水区（需要 boot 级装配），本层先钉死调度与合同语义不回归。

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

type scenarioDispatch struct {
	Name     string
	Task     string
	Contract bool
	Calls    int
}

func newScenarioStore() *Store {
	return New(Options{Stderr: io.Discard})
}

func runScenario(t *testing.T, skillName, args string) *scenarioDispatch {
	t.Helper()
	d := &scenarioDispatch{}
	store := newScenarioStore()
	tool := NewRunSkillTool(store, func(_ context.Context, sk Skill, task string, _ SubagentRunOptions) (string, error) {
		d.Name, d.Task = sk.Name, task
		d.Contract = RequiresFindingsContract(sk)
		d.Calls++
		return "mock-final-answer", nil
	})
	out, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"name": skillName, "arguments": args}))
	if err != nil {
		t.Fatalf("run_skill(%s): %v", skillName, err)
	}
	if !strings.Contains(out, "mock-final-answer") {
		t.Fatalf("runner output must reach the caller: %s", out)
	}
	return d
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// S1-S3：seccheck 三入口的显式语法原样到达子代理（入口路由在 body，
// 调度层保证 arguments 不被吞改），且合同生效。
func TestScenarioSeccheckThreeEntries(t *testing.T) {
	for _, sc := range []struct {
		name, args string
	}{
		{"S1 清单入口", "入口=清单 范围=dmz组 关注=中间件版本"},
		{"S2 网段入口", "入口=网段 目标=10.30.2.7 只收敛所在段"},
		{"S3 主机入口", "入口=主机 目标=10.30.2.7 已拿到权限，纵深排查"},
	} {
		d := runScenario(t, "netdev-seccheck-auto", sc.args)
		if d.Calls != 1 || d.Name != "netdev-seccheck-auto" {
			t.Fatalf("%s: dispatch wrong (%s/%d)", sc.name, d.Name, d.Calls)
		}
		if d.Task != sc.args {
			t.Fatalf("%s: arguments must pass through verbatim: %q", sc.name, d.Task)
		}
		if !d.Contract {
			t.Fatalf("%s: seccheck must be findings-contract-bound", sc.name)
		}
	}
	// 套餐入口（第四形态）。
	d := runScenario(t, "netdev-seccheck-auto", "入口=套餐 项目=上线项目X 跑五阶段")
	if !strings.Contains(d.Task, "入口=套餐") {
		t.Fatalf("套餐入口 arguments lost: %q", d.Task)
	}
}

// S4：diag 委托——症状参数原样到达，合同同样生效。
func TestScenarioDiagDispatch(t *testing.T) {
	d := runScenario(t, "netdev-diag-auto", "症状=邻居down 范围=核心-汇聚 起始=09:00")
	if d.Name != "netdev-diag-auto" || !d.Contract {
		t.Fatalf("diag dispatch/contract wrong: %+v", d)
	}
}

// S5：别名兼容——旧名调用路由到合并后的新技能。
func TestScenarioAliasDispatch(t *testing.T) {
	for old, want := range map[string]string{
		"netdev-vulnscan": "netdev-seccheck-auto",
		"netdev-playbook": "netdev-diag-auto",
		"netdev-diag-bgp": "netdev-diag-auto",
	} {
		d := runScenario(t, old, "场景参数")
		if d.Name != want {
			t.Fatalf("alias %s dispatched to %s, want %s", old, d.Name, want)
		}
	}
}

// S6：subagent 技能必须带 arguments（无上下文——空任务是合同违背）。
func TestScenarioSubagentRequiresArguments(t *testing.T) {
	store := newScenarioStore()
	tool := NewRunSkillTool(store, func(_ context.Context, _ Skill, _ string, _ SubagentRunOptions) (string, error) {
		t.Fatal("runner must not be called for an argument-less subagent task")
		return "", nil
	})
	_, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{"name": "netdev-seccheck-auto"}))
	if err == nil || !strings.Contains(err.Error(), "arguments") {
		t.Fatalf("missing-arguments must error loudly: %v", err)
	}
}

// S7：非 netdev 子代理（explore）不受 findings 合同约束。
func TestScenarioNonNetdevSkillExempt(t *testing.T) {
	d := runScenario(t, "explore", "找出所有调用 X 的地方")
	if d.Name != "explore" || d.Contract {
		t.Fatalf("explore must dispatch without the findings contract: %+v", d)
	}
}
