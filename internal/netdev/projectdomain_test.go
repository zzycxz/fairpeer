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
	t.Cleanup(func() { SetAuditPath("") })
	cfg := config.Default()
	cfg.NetDev.Enabled = true
	cfg.NetDev.Groups = []config.NetDevGroup{
		{Name: "campus-a", Policy: "proposal"},
		{Name: "campus-b", Policy: "proposal"},
	}
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "sw-a1", Vendor: "huawei", OS: "vrp8", Group: "campus-a"},
		{Name: "sw-b1", Vendor: "huawei", OS: "vrp8", Group: "campus-b"},
		{Name: "srv-b1", Vendor: "linux", Group: "campus-b"},
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

// v1.1-1：allow 是 deny 的域内例外白名单——命中 allow 的命令豁免项目 deny；
// 未命中 allow 的 deny 命中照拒。
func TestProjectAllowException(t *testing.T) {
	m := projectDomainManager(t)
	m.cfg.NetDev.Projects[0].Allow = []string{"undo stp region"} // 例外：区域视图的 undo 放行
	proj := m.cfg.NetDev.Projects[0]
	if err := m.SetActiveProject("蓝队A"); err != nil {
		t.Fatal(err)
	}
	// deny 命中且 allow 也命中 → 豁免（verdict 放行）。
	if _, ok := m.projectDomainVerdict("sw-a1", "undo stp region 1", driver.Write); !ok {
		t.Error("allow exception must exempt the deny refusal")
	}
	// deny 命中但 allow 未命中 → 拒。
	if _, ok := m.projectDomainVerdict("sw-a1", "undo stp all", driver.Write); ok {
		t.Error("deny hit without allow match must still refuse")
	}
	// 只许收紧：allow 豁免的是项目层——危险分类在分类器层照旧（此处仅验
	// 项目层不拒；全局层不可被 allow 打开）。
	if projectDenyVerdict(proj, "undo stp region 1") {
		t.Error("allow-matched command must skip the project deny verdict")
	}
}

// v1.1-2：policy=read-only 运行时写地板——域内 write 也拒，read 放行。
func TestProjectReadOnlyFloor(t *testing.T) {
	m := projectDomainManager(t)
	ro := config.NetDevProject{Name: "只读域", Groups: []string{"campus-a"}, Policy: "read-only"}
	m.cfg.NetDev.Projects = append(m.cfg.NetDev.Projects, ro)
	if err := m.SetActiveProject("只读域"); err != nil {
		t.Fatal(err)
	}
	if r, ok := m.projectDomainVerdict("sw-a1", "systemctl restart nginx", driver.Write); ok || !strings.Contains(r.Refusal, "read-only") {
		t.Errorf("read-only floor must refuse in-domain write: ok=%v %+v", ok, r)
	}
	if _, ok := m.projectDomainVerdict("sw-a1", "display version", driver.Read); !ok {
		t.Error("read must pass under read-only policy")
	}
	// 提案层：盖章到只读项目的提案整条拒执行（含 cli 私有写路径步骤）。
	p := &Proposal{Intent: "只读域内变更", Status: ProposalApproved, Project: "只读域",
		Steps: []ProposalStep{{Device: "sw-a1", Type: "cli", Commands: []string{"sys"}, Rollback: []string{"undo sys"}}}}
	SaveProposal(p)
	if _, err := m.ExecuteProposal(context.Background(), p.ID); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Errorf("read-only project proposal must refuse execution: %v", err)
	}
}

// v1.1-3：trustdomain 启用时的批准签名升格——启用但未入域 = fail-closed 拒批
// （SharedRemoteNode 无法建立节点）；名单校验照旧先行。
func TestTrustDomainApprovalUpgrade(t *testing.T) {
	m := projectDomainManager(t)
	m.cfg.TrustDomain.Enabled = true // 启用但测试环境无账本/数据目录
	p := &Proposal{Intent: "四眼", Status: ProposalDraft, Project: "蓝队A",
		Steps: []ProposalStep{{Device: "sw-a1", Type: "cli", Commands: []string{"sys"}, Rollback: []string{"undo sys"}}}}
	SaveProposal(p)
	_, err := m.ApproveProposalAs(p.ID, true, "张三")
	if err == nil || !strings.Contains(err.Error(), "trustdomain") {
		t.Errorf("enabled-but-not-joined must fail closed: %v", err)
	}
}

// 轮3覆盖 P1：盖章项目在配置中消失 → 批准/执行双 fail-closed。
func TestStampedProjectVanishedFailClosed(t *testing.T) {
	m := projectDomainManager(t)
	p := &Proposal{Intent: "孤儿", Status: ProposalDraft, Project: "已删项目",
		Steps: []ProposalStep{{Device: "sw-a1", Type: "cli", Commands: []string{"sys"}, Rollback: []string{"undo sys"}}}}
	SaveProposal(p)
	if _, err := m.ApproveProposalAs(p.ID, false, "张三"); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("approve must fail closed: %v", err)
	}
	p.Status = ProposalApproved
	SaveProposal(p)
	if _, err := m.ExecuteProposal(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("execute must fail closed: %v", err)
	}
}

// 轮3覆盖 P1：盖章项目 policy=proposal+confirm2 → 蓝队双锁运行时生效。
func TestStampedProjectForcesConfirm2(t *testing.T) {
	m := projectDomainManager(t)
	p := &Proposal{Intent: "良性命令", Status: ProposalDraft, Project: "蓝队A",
		Steps: []ProposalStep{{Device: "sw-a1", Type: "cli", Commands: []string{"display version"}, Rollback: []string{"display version"}}}}
	SaveProposal(p)
	if !m.ProposalNeedsConfirm2(p) {
		t.Fatal("blueteam-stamped proposal must demand confirm2 (runtime floor)")
	}
	if _, err := m.ApproveProposalAs(p.ID, false, "张三"); err == nil || !strings.Contains(err.Error(), "secondary confirmation") {
		t.Errorf("single-lock approve must refuse: %v", err)
	}
}

// 轮3覆盖 P1：活动项目从 config 消失 → 只读停摆（write 拒 read 放）。
func TestActiveProjectVanishedReadOnlyStall(t *testing.T) {
	m := projectDomainManager(t)
	if err := m.SetActiveProject("蓝队A"); err != nil {
		t.Fatal(err)
	}
	m.cfg.NetDev.Projects = nil // 热重载窗口：项目被删
	if r, ok := m.projectDomainVerdict("sw-a1", "systemctl restart nginx", driver.Write); ok || !strings.Contains(r.Refusal, "vanished") {
		t.Errorf("vanished project must stall writes: ok=%v %+v", ok, r)
	}
	if _, ok := m.projectDomainVerdict("sw-a1", "display version", driver.Read); !ok {
		t.Error("reads must stay available during stall")
	}
}

// 轮3覆盖 P2：域闸×curlReadOverride 端到端——域外设备的探活形态放行、
// 写形态拒绝，域闸分类与密封执行器一致（经 guardrailCheck 真路径）。
func TestDomainGateCurlOverrideEndToEnd(t *testing.T) {
	m := projectDomainManager(t)
	if err := m.SetActiveProject("蓝队A"); err != nil {
		t.Fatal(err)
	}
	// 域外 linux 主机（srv-b1）+ 探活形态 → curlReadOverride 判 Read → 放行。
	if r, ok := m.guardrailCheck("srv-b1", "curl -I http://127.0.0.1:8000/health"); !ok {
		t.Errorf("out-of-domain curl probe must pass the domain gate: %+v", r)
	}
	// 域外 linux 主机 + 写形态 → 拒。
	if _, ok := m.guardrailCheck("srv-b1", "systemctl restart nginx"); ok {
		t.Error("out-of-domain write must refuse")
	}
	// 域外 huawei（sw-b1）：curl 在网络 CLI 驱动下本就是 Unknown → 拒（与
	// 密封执行器同判——域闸不比执行器宽）。
	if _, ok := m.guardrailCheck("sw-b1", "curl -I http://127.0.0.1:8000/health"); ok {
		t.Error("network-CLI host curl must classify Unknown and refuse out-of-domain")
	}
}
