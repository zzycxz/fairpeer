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
	"fmt"
	"strings"

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
	return drv.Classify(command)
}

// projectDenyMatches checks the project's deny prefixes against the normalized
// command (word-boundary prefix, same discipline as the classifier tables).
func projectDenyMatches(p config.NetDevProject, command string) bool {
	normalized := strings.Join(strings.Fields(strings.ToLower(command)), " ")
	for _, prefix := range p.Deny {
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
		return ExecResult{}, true
	}
	// deny 前缀：域内外都拒（项目级收紧，与全局 guardrail 叠加）。
	if projectDenyMatches(proj, command) {
		r := ExecResult{Device: deviceName, Command: command, Refused: true, Class: "guardrail",
			Refusal: fmt.Sprintf("command matches project %q deny list — the active project forbids this operation shape. Do not retry.", proj.Name)}
		return r, false
	}
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return ExecResult{}, true // 清单外设备走既有路径
	}
	if deviceInProject(d, proj) {
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
