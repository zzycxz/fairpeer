package netdev

import (
	"context"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

func projectDomainManager(t *testing.T) *Manager {
	t.Helper()
	findingsDirOverr = t.TempDir()
	proposalsDirOverride = t.TempDir()
	SetAuditPath(t.TempDir() + "/audit.jsonl")
	cfg := config.Default()
	cfg.NetDev.Enabled = true
	cfg.NetDev.Groups = []config.NetDevGroup{
		{Name: "campus-a", Policy: "proposal"},
		{Name: "campus-b", Policy: "proposal"},
	}
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "sw-a1", Vendor: "huawei", OS: "vrp8", Group: "campus-a"},
		{Name: "sw-b1", Vendor: "huawei", OS: "vrp8", Group: "campus-b"},
	}
	cfg.NetDev.Projects = []config.NetDevProject{
		{Name: "蓝队A", Groups: []string{"campus-a"}, Type: "blueteam",
			Policy: "proposal+confirm2", Deny: []string{"undo stp"}, Confirmers: []string{"张三"}},
		{Name: "智算B", Groups: []string{"campus-b"}, Type: "aicompute"},
	}
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	return m
}

func TestSetActiveProject(t *testing.T) {
	m := projectDomainManager(t)
	if m.ActiveProjectName() != "" {
		t.Fatal("default must be no active project")
	}
	if err := m.SetActiveProject("不存在的项目"); err == nil {
		t.Error("undefined project must be refused")
	}
	if err := m.SetActiveProject("蓝队A"); err != nil {
		t.Fatal(err)
	}
	if m.ActiveProjectName() != "蓝队A" {
		t.Errorf("active want 蓝队A, got %q", m.ActiveProjectName())
	}
	_ = m.SetActiveProject("")
	if m.ActiveProjectName() != "" {
		t.Errorf("clear failed: %q", m.ActiveProjectName())
	}
}

// J4-A：域外设备只读+拒操作——write 拒、read 放行、unknown 拒（fail-closed）。
func TestProjectDomainGate(t *testing.T) {
	m := projectDomainManager(t)
	if err := m.SetActiveProject("蓝队A"); err != nil {
		t.Fatal(err)
	}
	// 域外 write 拒绝，拒绝文案带项目名（审计信号）。
	r, ok := m.projectDomainVerdict("sw-b1", "systemctl restart nginx", driver.Write)
	if ok || !r.Refused || !strings.Contains(r.Refusal, "蓝队A") {
		t.Errorf("out-of-domain write must refuse with project name: ok=%v %+v", ok, r)
	}
	// 域外 read 放行（J4-A 只读）。
	if _, ok := m.projectDomainVerdict("sw-b1", "display version", driver.Read); !ok {
		t.Error("out-of-domain read must pass (J4-A)")
	}
	// 域外 unknown 拒绝（fail-closed）。
	if _, ok := m.projectDomainVerdict("sw-b1", "someunknownthing", driver.Unknown); ok {
		t.Error("out-of-domain unknown must refuse")
	}
	// 域内 write 放行。
	if _, ok := m.projectDomainVerdict("sw-a1", "systemctl restart nginx", driver.Write); !ok {
		t.Error("in-domain write must pass")
	}
	// deny 前缀：域内也拒。
	if r, ok := m.projectDomainVerdict("sw-a1", "undo stp region 1", driver.Write); ok || !strings.Contains(r.Refusal, "deny") {
		t.Errorf("project deny prefix must refuse in-domain: %+v", r)
	}
	// 无活动项目：一切放行（现状语义）。
	_ = m.SetActiveProject("")
	if _, ok := m.projectDomainVerdict("sw-b1", "systemctl restart nginx", driver.Write); !ok {
		t.Error("no active project = legacy passthrough")
	}
}

// G-P1-B：提案盖章 + 跨域执行拒绝。
func TestProposalProjectStampAndCrossDomainRefusal(t *testing.T) {
	m := projectDomainManager(t)
	if err := m.SetActiveProject("蓝队A"); err != nil {
		t.Fatal(err)
	}
	// 跨域目标：蓝队A 的提案目标落在 campus-b（域外）→ 执行链拒绝
	// （批准后仍拒——视图放行≠执行放行）。
	p := &Proposal{Intent: "跨域变更", Status: ProposalDraft, Project: "蓝队A",
		Steps: []ProposalStep{{Device: "sw-b1", Type: "cli", Commands: []string{"sys"}, Rollback: []string{"undo sys"}}}}
	SaveProposal(p)
	p.Status = ProposalApproved
	SaveProposal(p)
	if _, err := m.ExecuteProposal(context.Background(), p.ID); err == nil ||
		!strings.Contains(err.Error(), "outside the project domain") {
		t.Errorf("cross-domain execution must refuse: %v", err)
	}
	// 域内目标：不因项目域拒绝（走到既有 estop/状态机为止——这里只验不再报域错）。
	p2 := &Proposal{Intent: "域内", Status: ProposalApproved, Project: "蓝队A",
		Steps: []ProposalStep{{Device: "sw-a1", Type: "cli", Commands: []string{"sys"}, Rollback: []string{"undo sys"}}}}
	SaveProposal(p2)
	_, err := m.ExecuteProposal(context.Background(), p2.ID)
	if err != nil && strings.Contains(err.Error(), "outside the project domain") {
		t.Errorf("in-domain proposal must not hit the domain gate: %v", err)
	}
}

// J5：confirmers 名单——名单内放行、名单外拒、缺操作者拒、空名单任意。
func TestApproveOperatorVerdict(t *testing.T) {
	m := projectDomainManager(t)
	proj := m.cfg.NetDev.Projects[0]
	if ok, why := approveOperatorVerdict(proj, "张三"); !ok {
		t.Errorf("listed operator must pass: %s", why)
	}
	if ok, _ := approveOperatorVerdict(proj, "zhangsan"); ok {
		t.Error("case-insensitive match should NOT apply to different names")
	}
	if ok, why := approveOperatorVerdict(proj, ""); ok || !strings.Contains(why, "qualified approver") {
		t.Errorf("empty operator must refuse when confirmers set: %v", why)
	}
	if ok, _ := approveOperatorVerdict(proj, "李四"); ok {
		t.Error("unlisted operator must refuse")
	}
	empty := config.NetDevProject{Name: "智算B"}
	if ok, _ := approveOperatorVerdict(empty, "任何人"); !ok {
		t.Error("empty confirmers = any human")
	}
	// 端到端：ApproveProposalAs 走项目名单（提案盖章项目优先）。
	p := &Proposal{Intent: "四眼", Status: ProposalDraft, Project: "蓝队A",
		Steps: []ProposalStep{{Device: "sw-a1", Type: "cli", Commands: []string{"sys"}, Rollback: []string{"undo sys"}}}}
	SaveProposal(p)
	if _, err := m.ApproveProposalAs(p.ID, true, "李四"); err == nil || !strings.Contains(err.Error(), "confirmers") {
		t.Errorf("unlisted approver must be refused: %v", err)
	}
	if _, err := m.ApproveProposalAs(p.ID, true, "张三"); err != nil {
		t.Errorf("listed approver must pass the verdict (later gates may still fire): %v", err)
	}
}

// 蓝队信封：config 校验强制双锁（validation 层）。
func TestBlueteamEnvelopeValidation(t *testing.T) {
	nd := config.NetDevConfig{Enabled: true, Projects: []config.NetDevProject{
		{Name: "蓝队", Type: "blueteam", Policy: "proposal"},
	}}
	err := config.ValidateNetDev(nd)
	if err == nil || !strings.Contains(err.Error(), "proposal+confirm2") {
		t.Errorf("blueteam without double-lock must be refused: %v", err)
	}
}
