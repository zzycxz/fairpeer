package netdev

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

func runbookTplTestEnv(t *testing.T) *Manager {
	t.Helper()
	runbookTplDirOverride = t.TempDir()
	proposalsDirOverride = t.TempDir()
	findingsDirOverr = t.TempDir()
	cutoversDirOverride = t.TempDir()
	// 注：StateEventSnap 的路径相对 state root——测试目录在 root 外时事件
	// 只记 warning 跳过，不影响本机制的行为断言。
	t.Cleanup(func() {
		runbookTplDirOverride = ""
		proposalsDirOverride = ""
		findingsDirOverr = ""
		cutoversDirOverride = ""
	})
	cfg := config.Default()
	cfg.NetDev.Devices = []config.NetDevDevice{{Name: "sw1", Vendor: "huawei", OS: "vrp8"}}
	return NewManager(cfg)
}

func sampleRunbookTpl() *RunbookTemplate {
	return &RunbookTemplate{
		Name:      "vLLM 单机部署骨架",
		Scenario:  "model-deploy",
		Vars:      []string{"gpu_host", "svc_port"},
		WindowMin: 120,
		Steps: []RunbookTplStep{
			{Label: "前置检查", Device: "{{gpu_host}}", Command: "nvidia-smi"},
			{Label: "变更：拉起服务", Device: "{{gpu_host}}", ProposalIntent: "启动 vLLM 服务",
				ProposalCmds: []string{"systemctl start vllm-{{svc_port}}"}},
			{Label: "语义门", Device: "{{gpu_host}}", Command: "systemctl is-active vllm-{{svc_port}}",
				GateCmd: "systemctl is-active vllm-{{svc_port}}", GateExpect: "active"},
			{Label: "决策点", DecisionPoint: true, Impact: "放流量前人工确认基线压测"},
		},
	}
}

func TestRunbookTplLifecycle(t *testing.T) {
	runbookTplTestEnv(t)
	tpl := sampleRunbookTpl()
	if err := SaveRunbookTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	if tpl.ID == "" || !strings.HasPrefix(tpl.ID, "RB") {
		t.Fatalf("ID not assigned: %q", tpl.ID)
	}
	got, err := GetRunbookTemplate(tpl.ID)
	if err != nil || got.Name != tpl.Name || len(got.Steps) != 4 {
		t.Fatalf("get: %v %+v", err, got)
	}
	list, err := ListRunbookTemplates()
	if err != nil || len(list) != 2 { // 用户 1 条 + 内置骨架
		t.Fatalf("list: %v %d", err, len(list))
	}
	if err := DeleteRunbookTemplate(tpl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetRunbookTemplate(tpl.ID); err == nil {
		t.Fatal("deleted template still loads")
	}
	// 内置骨架：Get 可读、Delete 拒绝、List 始终在列。
	skel, err := GetRunbookTemplate("RBB-skel-vllm-standalone")
	if err != nil || len(skel.Steps) != 4 {
		t.Fatalf("builtin skeleton: %v %+v", err, skel)
	}
	if err := DeleteRunbookTemplate("RBB-skel-vllm-standalone"); err == nil {
		t.Error("builtin template delete must be refused")
	}
}

func TestRunbookTplValidation(t *testing.T) {
	runbookTplTestEnv(t)
	cases := []struct {
		name string
		mut  func(*RunbookTemplate)
	}{
		{"no steps", func(t *RunbookTemplate) { t.Steps = nil }},
		{"bad var name", func(t *RunbookTemplate) { t.Vars = []string{"bad name"} }},
		{"two bodies", func(t *RunbookTemplate) { t.Steps[0].DecisionPoint = true }},
		{"command without device", func(t *RunbookTemplate) { t.Steps[0].Device = "" }},
		{"gate without expect", func(t *RunbookTemplate) { t.Steps[2].GateExpect = "" }},
	}
	for _, tc := range cases {
		tpl := sampleRunbookTpl()
		tc.mut(tpl)
		if err := SaveRunbookTemplate(tpl); err == nil {
			t.Errorf("%s: expected rejection", tc.name)
		}
	}
}

func TestRunbookTplPreview(t *testing.T) {
	m := runbookTplTestEnv(t)
	tpl := sampleRunbookTpl()
	if err := SaveRunbookTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	p, err := m.PreviewRunbookTemplate(tpl.ID, map[string]string{"gpu_host": "sw1", "svc_port": "8000"})
	if err != nil {
		t.Fatal(err)
	}
	// 只读步分类：nvidia-smi 在 linux 读表……sw1 是 huawei——注意分类按设备
	// 驱动；huawei 驱动对 nvidia-smi 是 unknown（诚实展示，不假装 read）。
	if p.Steps[0].Class == "" {
		t.Errorf("read step verdict missing: %+v", p.Steps[0])
	}
	if p.Steps[1].Class != "" || len(p.Steps[1].ProposalCmd) != 1 {
		t.Errorf("change segment shape wrong: %+v", p.Steps[1])
	}
	if p.Steps[3].Label != "决策点" || !p.Steps[3].Decision {
		t.Errorf("decision point lost: %+v", p.Steps[3])
	}
	if p.Run == nil || len(p.Run.Steps) != 4 || p.Run.Status != "" {
		t.Errorf("preview run must be assembled but NOT started: %+v", p.Run)
	}
	// 变量完整性：缺值拒绝。
	if _, err := m.PreviewRunbookTemplate(tpl.ID, map[string]string{"gpu_host": "sw1"}); err == nil {
		t.Error("missing var value must be rejected")
	}
	// 值白名单：分号注入拒绝。
	if _, err := m.PreviewRunbookTemplate(tpl.ID, map[string]string{"gpu_host": "sw1; reboot", "svc_port": "8000"}); err == nil {
		t.Error("metachar var value must be rejected")
	}
	// 未声明变量（模板正文里的 {{typo}}）在渲染时报错。
	tpl2 := sampleRunbookTpl()
	tpl2.Steps[0].Command = "display version {{typo}}"
	SaveRunbookTemplate(tpl2)
	if _, err := m.PreviewRunbookTemplate(tpl2.ID, map[string]string{"gpu_host": "sw1", "svc_port": "8000"}); err == nil {
		t.Error("undeclared placeholder must fail rendering")
	}
}

func TestRunbookTplApply(t *testing.T) {
	m := runbookTplTestEnv(t)
	tpl := sampleRunbookTpl()
	if err := SaveRunbookTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	res, err := m.ApplyRunbookTemplate(tpl.ID, map[string]string{"gpu_host": "sw1", "svc_port": "8000"}, "vLLM 上线 0913")
	if err != nil {
		t.Fatal(err)
	}
	// 变更段 → draft 提案，人批前 start 会被 CutoverStart 拒（不代批语义）。
	if len(res.Proposals) != 1 || res.Proposals[0].Status != ProposalDraft {
		t.Fatalf("want 1 draft proposal, got %+v", res.Proposals)
	}
	if len(res.Proposals[0].Steps) != 1 || res.Proposals[0].Steps[0].Commands[0] != "systemctl start vllm-8000" {
		t.Errorf("proposal steps wrong: %+v", res.Proposals[0].Steps)
	}
	// run 定义未启动：无 ID、无状态；步骤引用提案 ID。
	run := res.Run
	if run.ID != "" || run.Status != "" || run.Name != "vLLM 上线 0913" {
		t.Errorf("apply must not start the run: %+v", run)
	}
	if run.Steps[1].ProposalID != res.Proposals[0].ID {
		t.Errorf("change step must reference the draft proposal")
	}
	if run.Steps[2].Gate == nil || run.Steps[2].Gate.Expect != "active" {
		t.Errorf("gate mapping lost: %+v", run.Steps[2].Gate)
	}
	if run.Steps[3].DecisionPoint != true {
		t.Errorf("decision point lost")
	}
	// 磁盘上确实有一份 draft 提案。
	p2, err := GetProposal(res.Proposals[0].ID)
	if err != nil || p2.Status != ProposalDraft {
		t.Fatalf("proposal not persisted: %v", err)
	}
}

// F1c 出口②：跑完的 run 反向抽取为模板（提案命令回拉为变更段）。
func TestRunbookExtractFromRun(t *testing.T) {
	m := runbookTplTestEnv(t)
	// 先 apply 出一份提案与 run 定义，手工把 run 推到终态。
	res, err := m.ApplyRunbookTemplate(mustSeedTpl(t), map[string]string{"gpu_host": "sw1", "svc_port": "8000"}, "抽取源")
	if err != nil {
		t.Fatal(err)
	}
	run := res.Run
	run.ID = "C-test-1"
	run.Status = CutoverDone
	tpl, err := ExtractRunbookTemplate(run, "沉淀模板")
	if err != nil {
		t.Fatal(err)
	}
	if len(tpl.Steps) != 4 {
		t.Fatalf("steps want 4, got %d", len(tpl.Steps))
	}
	// 变更段命令回拉：提案里的 cli 命令进 ProposalCmds。
	if len(tpl.Steps[1].ProposalCmds) != 1 || tpl.Steps[1].ProposalCmds[0] != "systemctl start vllm-8000" {
		t.Errorf("proposal cmds not dereferenced: %+v", tpl.Steps[1])
	}
	if tpl.Steps[0].Device != "sw1" || tpl.Steps[0].Command != "nvidia-smi" {
		t.Errorf("read step not carried: %+v", tpl.Steps[0])
	}
	if !tpl.Steps[3].DecisionPoint {
		t.Errorf("decision point lost in extraction")
	}
	if !strings.Contains(tpl.Notes, "C-test-1") {
		t.Errorf("provenance note missing: %q", tpl.Notes)
	}
	// 抽取出的模板能再走一遍渲染（回放闭环）。
	if _, err := m.PreviewRunbookTemplate(mustSaveTpl(t, tpl), map[string]string{"gpu_host": "sw1", "svc_port": "8000"}); err != nil {
		t.Fatalf("extracted template must re-render: %v", err)
	}
	// 非终态拒绝。
	run2 := *run
	run2.ID, run2.Status = "C-test-2", CutoverRunning
	if _, err := ExtractRunbookTemplate(&run2, "x"); err == nil {
		t.Error("running run must refuse extraction")
	}
}

func mustSeedTpl(t *testing.T) string {
	t.Helper()
	tpl := sampleRunbookTpl()
	if err := SaveRunbookTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	return tpl.ID
}

func mustSaveTpl(t *testing.T, tpl *RunbookTemplate) string {
	t.Helper()
	tpl.ID = ""
	if err := SaveRunbookTemplate(tpl); err != nil {
		t.Fatal(err)
	}
	return tpl.ID
}
