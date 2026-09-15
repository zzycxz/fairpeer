package netdev

import (
	"fmt"
	"sync/atomic"
)

// estop.go — 紧急停止的统一语义（FDE_AIINFRA_OPS_GAP_SPEC §4.0）。此前
// NetDevEmergencyStop 只杀连接：running 状态的 Job / Cutover / Proposal 不受
// 影响——客户现场按下急停后部署 runbook 还在跑，是安全事故剧本。本文件把
// 急停扩成四件事，并守住一条红线：
//
//  1. KillAllConnections（既有：设备连接 + 人工终端 + 发现任务，已入审计）
//  2. running Job → JobPause（既有边界冻结：当前步骤收尾后暂停落盘）。job
//     腿在割接腿之前执行（新轮2并发审查）：cutover hold 要拿 cutoverMu，
//     若操作员红钮前已发起回退（持锁跨分钟级 I/O），job 腿会被无界拖住。
//  3. running 割接 → Hold。**不是 abort**：CutoverAbort 只 cancel 运行器并把
//     pending 步骤标 skipped，不回退任何已执行变更——直接 abort 会把设备留在
//     半割接状态。Hold 让 runner 在当前步骤收尾后停在步骤边界（每步都有
//     超时，有界），继续 / 回退 / 终止由人按。
//  4. executing 提案 → 代际冻结：estopGen 代数 +1；ExecuteProposal 在步骤
//     边界检测到代数变化即走既有 frozen 路径（partial，后续步骤不落盘）。
//
// 红线：任何急停不得绕过割接的回退决策点——hold + 人决策，不自动 abort、
// 不自动回滚（"AI 的手永远慢一步"对急停同样成立）。

// estopGen is bumped on every emergency stop; long-running executors capture
// the generation at start and freeze when it changes. Atomic: no lock needed
// on the hot path (ExecuteProposal's per-step check).
var estopGen atomic.Int64

// EstopGeneration returns the current emergency-stop generation. Executors
// capture it at start and compare per step (see proposal.go's step loop).
func EstopGeneration() int64 { return estopGen.Load() }

// proposalEstopFrozen reports whether an emergency stop happened since the
// executor captured its generation — the seam is a named function so the
// freeze semantics are testable without devices.
func proposalEstopFrozen(captured int64) bool { return estopGen.Load() != captured }

// EstopReport is what one emergency stop actually stopped.
type EstopReport struct {
	Connections  int      `json:"connections"`
	CutoversHeld []string `json:"cutoversHeld,omitempty"`
	JobsPaused   []string `json:"jobsPaused,omitempty"`
	// Errors carries enumeration/pause failures — an estop that couldn't see
	// the running runs must not look like a quiet fleet (：静默吞错会让
	// 值班误判"没有在跑的割接")。
	Errors []string `json:"errors,omitempty"`
}

// EstopAll runs the full emergency stop (red button): kill connections, hold
// running cutovers at their step boundary, pause running jobs, and freeze
// executing proposals at their next step boundary. Everything is audited —
// KillAllConnections audits its own sweep, this adds one summary line for the
// orchestration side.
func (m *Manager) EstopAll() EstopReport {
	// 代数最先升：冻结是唯一不依赖连接的闸（提案写路径的 runRead 会按需
	// 重连——杀连接挡不住它，代数才挡得住）。顺序 = 冻结 → 杀 → hold/pause。
	estopGen.Add(1)
	rep := EstopReport{Connections: m.KillAllConnections()}
	// job 腿在 cutover 腿**之前**（新轮2并发审查 P2）：cutoverHold 要拿
	// cutoverMu——若操作员在红钮前已发起回退（CutoverRollback 持锁跨分钟级
	// 设备 I/O），cutover 腿会阻塞到回退收尾，排在后面的 job 暂停腿被无界
	// 拖住（running job 继续在设备上跑）。先停 job（快、无 cutoverMu 依赖），
	// 再逐台 hold 割接。
	if js, err := ListJobs(); err != nil {
		rep.Errors = append(rep.Errors, "list jobs: "+err.Error())
	} else {
		for _, j := range js {
			if j.Status != JobRunning {
				continue
			}
			if err := JobPauseEstop(j.ID); err == nil {
				rep.JobsPaused = append(rep.JobsPaused, j.ID)
			} else {
				rep.Errors = append(rep.Errors, "pause job "+j.ID+": "+err.Error())
			}
		}
	}
	if cs, err := ListCutovers(); err != nil {
		rep.Errors = append(rep.Errors, "list cutovers: "+err.Error())
	} else {
		for _, c := range cs {
			if c.Status != CutoverRunning {
				continue
			}
			// cutoverHold flips running→hold under cutoverMu; the runner
			// notices at its next status check and exits without advancing.
			if err := m.cutoverHold(c.ID, "紧急停止：已在步骤边界暂停（当前步骤收尾后不再前进）——继续 / 回退 / 终止由人按"); err != nil {
				rep.Errors = append(rep.Errors, "hold cutover "+c.ID+": "+err.Error())
				continue
			}
			rep.CutoversHeld = append(rep.CutoversHeld, c.ID)
		}
	}
	status := AuditOK
	if len(rep.Errors) > 0 {
		status = AuditFailure
	}
	_ = AppendAudit(Audit{Device: "(emergency-stop)",
		Command: fmt.Sprintf("estop summary: cutovers held %v, jobs paused %v, executing proposals frozen at step boundary, errors %v", rep.CutoversHeld, rep.JobsPaused, rep.Errors),
		Class:   "guardrail", Status: status})
	return rep
}
