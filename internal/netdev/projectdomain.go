package netdev

// projectdomain.go — G-P1 项目安全域的运行时面（批⑤；J4/J5 已拍板：J4=
// 项目外设备只读+拒操作、J5=confirmers 自报基线+trustdomain 升格；设计=
// docs/NETDEV_PROJECT_DOMAIN_DESIGN.md）。
//
// 活动项目是 Manager 级后端态（单操作员工作站的"会话"就是应用实例——
// 标题栏切换器一次写两处：前端 store + SetActiveProject）。切换即审计。
//
// 域语义（视图零改动，J4-A）：
//   - 域内设备：一切照旧 + 项目 deny 前缀拒绝；
//   - 域外设备：read 放行、write/dangerous/unknown 拒绝（"只读+拒操作"，
//     拒绝本身带项目名进审计）；
//   - 项目 policy/confirmers 在提案审批链生效（approve.go 侧）。

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

// SetActiveProject pins the instance's active project ("" = clear). The name
// must exist in the inventory config — switching to an undefined project is a
// typo, not a mode.
func (m *Manager) SetActiveProject(name string) error {
	name = strings.TrimSpace(name)
	if name != "" {
		found := false
		for _, p := range m.cfg.NetDev.Projects {
			if p.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("project %q is not defined in [[netdev.projects]]", name)
		}
	}
	m.mu.Lock()
	m.activeProject = name
	m.mu.Unlock()
	_ = AppendAudit(Audit{Device: "(project)", Command: "switch project " + quotedOrNone(name), Class: "guardrail", Status: AuditOK})
	return nil
}

func quotedOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

// ActiveProjectName returns the pinned project ("" = none).
func (m *Manager) ActiveProjectName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeProject
}

// ActiveProjectDef returns the pinned project's config row.
func (m *Manager) ActiveProjectDef() (config.NetDevProject, bool) {
	name := m.ActiveProjectName()
	if name == "" {
		return config.NetDevProject{}, false
	}
	for _, p := range m.cfg.NetDev.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return config.NetDevProject{}, false
}

// deviceInProject reports whether the device belongs to the project's groups
// (group-less devices belong to no project domain).
func deviceInProject(d config.NetDevDevice, p config.NetDevProject) bool {
	for _, g := range p.Groups {
		if d.Group == g {
			return true
		}
	}
	return false
}

// classifyForDomain classifies a command for the domain gate when the caller
// runs ahead of the classifier (guardrailCheck ordering). Falls back to
// Unknown — the domain gate treats Unknown on an OUT-of-domain device as
// refused (fail-closed), while in-domain devices never reach that branch.
func (m *Manager) classifyForDomain(deviceName, command string) driver.Class {
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return driver.Unknown
	}
	drv, ok := m.driverFor(d)
	if !ok {
		return driver.Unknown
	}
	class := drv.Classify(command)
	if class == driver.Unknown {
		// 与密封执行器同套旁路（logsource/curlread）——域闸的分类结论
		// 必须与真正执行时的结论一致，否则"域外只读放行"对这两族失效。
		if _, allow := logPathReadOverride(d, drv, command); allow {
			class = driver.Read
		} else if c, allow := curlReadOverride(drv, command); allow {
			class = c
		}
	}
	return class
}

// projectDenyVerdict applies the project's deny prefixes with the allow
// exception (v1.1): deny 命中且 allow 也命中 → 不拒（allow 只豁免项目层——
// 全局 guardrail 与分类器在后续照常裁决，所以仍只许收紧，不会放大权限）。
func projectDenyVerdict(p config.NetDevProject, command string) bool {
	if !projectDenyMatches(p.Deny, command) {
		return false
	}
	if projectDenyMatches(p.Allow, command) {
		return false // allow 例外：豁免本项目 deny
	}
	return true
}

// projectDenyMatches checks deny/allow prefixes against the normalized command
// (word-boundary prefix, same discipline as the classifier tables).
func projectDenyMatches(prefixes []string, command string) bool {
	normalized := strings.Join(strings.Fields(strings.ToLower(command)), " ")
	for _, prefix := range prefixes {
		if normalized == prefix || strings.HasPrefix(normalized, prefix+" ") {
			return true
		}
	}
	return false
}

// projectDomainVerdict is the guardrail-layer decision for one command under
// the active project. ok=false carries a refusal meant for the caller's audit
// and the live stream.
func (m *Manager) projectDomainVerdict(deviceName, command string, class driver.Class) (ExecResult, bool) {
	proj, active := m.ActiveProjectDef()
	if !active {
		// 轮2复核 P1-1：活动项目名仍在但配置里已删（热重载窗口）→ 域定义
		// 不可得时**不静默放行**——降级为只读停摆（域外同款 J4-A 语义），
		// 直到操作员重新选择项目；read 放行维持值班可见性。
		if m.ActiveProjectName() != "" && class != driver.Read {
			r := ExecResult{Device: deviceName, Command: command, Refused: true, Class: "guardrail",
				Refusal: "active project vanished from config — 域定义缺失，写操作停摆（只读）：请重新选择项目或修正配置。Do not retry."}
			return r, false
		}
		return ExecResult{}, true
	}
	// deny 前缀（allow 可豁免）：域内外都拒（项目级收紧，与全局 guardrail 叠加）。
	if projectDenyVerdict(proj, command) {
		r := ExecResult{Device: deviceName, Command: command, Refused: true, Class: "guardrail",
			Refusal: fmt.Sprintf("command matches project %q deny list — the active project forbids this operation shape. Do not retry.", proj.Name)}
		return r, false
	}
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return ExecResult{}, true // 清单外设备走既有路径
	}
	if deviceInProject(d, proj) {
		// v1.1：项目 policy=read-only 运行时写地板——域内也一样，非 read 全拒
		// （提案 cli 步骤走私有写路径不经此处，由 ExecuteProposal 侧同口径拦截）。
		if proj.Policy == "read-only" && class != driver.Read {
			r := ExecResult{Device: deviceName, Command: command, Refused: true, Class: "guardrail",
				Refusal: fmt.Sprintf("project %q policy is read-only — write/dangerous operations are refused even in-domain. Do not retry.", proj.Name)}
			return r, false
		}
		return ExecResult{}, true // 域内：放行
	}
	// 域外（J4-A）：只读放行，其余拒绝——拒绝写明项目与操作类别。
	if class == driver.Read {
		return ExecResult{}, true
	}
	what := "the command class"
	switch class {
	case driver.Write:
		what = "a write command"
	case driver.Dangerous:
		what = "a dangerous command"
	}
	r := ExecResult{Device: deviceName, Command: command, Refused: true, Class: "guardrail",
		Refusal: fmt.Sprintf("device %q is outside the active project %q — 只读（J4）：%s is refused here. Switch projects or route the change through a proposal scoped to the owning project. Do not retry.", deviceName, proj.Name, what)}
	return r, false
}

// approveOperatorVerdict enforces confirmers (J5): an empty list means any
// human; a configured list requires the operator to match (case-insensitive).
// trustdomain 签名升格在其启用时叠加（v1 基线=自报名进审计；升格随
// trustdomain 接线，K1 §二.2）。
func approveOperatorVerdict(p config.NetDevProject, operator string) (bool, string) {
	if len(p.Confirmers) == 0 {
		return true, ""
	}
	op := strings.TrimSpace(operator)
	if op == "" {
		return false, "project " + p.Name + " requires a qualified approver (confirmers configured) — supply the operator name"
	}
	for _, c := range p.Confirmers {
		if strings.EqualFold(c, op) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("operator %q is not in project %q confirmers — approval refused", op, p.Name)
}

// signApprovalWithDomain is the J5 升格 half: when trustdomain is enabled AND
// the local node is admitted, the operator name must match the LOCAL domain
// member's display name (the key lives on this machine — self-report cannot
// claim someone else), and the approval is signed with the domain identity
// key as tamper-evident evidence. 启用但未入域 → fail-closed。
func (m *Manager) signApprovalWithDomain(proposalID, operator string) (string, error) {
	node, err := SharedRemoteNode(m.cfg)
	if err != nil {
		return "", fmt.Errorf("trustdomain 已启用但本机未入域——第二把锁拒绝降级为自报（先 init/join，或关闭 trustdomain）：%w", err)
	}
	self := node.Self()
	if self == nil {
		return "", fmt.Errorf("trustdomain 本机身份不可用")
	}
	id := node.Identity()
	var display string
	if st := node.State(); st != nil {
		if info := st.Member(id); info != nil {
			display = info.DisplayName
		}
	}
	if strings.TrimSpace(operator) == "" || display == "" ||
		!strings.EqualFold(strings.TrimSpace(operator), display) {
		return "", fmt.Errorf("operator %q 不匹配本域身份 %q（display %q）——域开启时批准必须以域身份进行", operator, id, display)
	}
	unix := time.Now().Unix()
	msg := fmt.Sprintf("fairpeer/approve|%s|%s|%d", proposalID, strings.TrimSpace(operator), unix)
	sig := self.Sign([]byte(msg))
	if len(sig) == 0 {
		return "", fmt.Errorf("域身份签名失败")
	}
	// 轮2攻击面审查：签名消息必须可从提案记录重构（Approver+ApprovedAt）
	// ——时间随签名一起落盘为 "hex|unix"，验证方以 ApprovedAt.Unix() 交叉
	// 校验，不再依赖两次取时一致。
	return fmt.Sprintf("%s|%d", hex.EncodeToString(sig), unix), nil
}
