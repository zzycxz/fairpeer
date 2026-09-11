package netdev

import (
	"context"
	"strings"
	"testing"
	"time"
)

// estop 统一语义（FDE_AIINFRA gap §4.0）：running 割接置 Hold（不是 abort）、
// running Job 走边界暂停、代数冻结 executing 提案。红线：不绕过回退决策点。

// TestEstopHoldsRunningCutover：急停把 running 割接翻成 Hold（带引导文案），
// 报告列出被停的 run；abort 计数不受影响（Abort 是独立的人工动作）。
func TestEstopHoldsRunningCutover(t *testing.T) {
	m := cutoverTestManager(t)
	run := &CutoverRun{
		ID: "CesT001", Name: "estop drill", Deadline: time.Now().Add(time.Hour),
		Status: CutoverRunning, Cursor: 0,
		Steps: []CutoverStep{{Label: "只读核查", Device: "sw1", Command: "display version"}},
	}
	if err := saveCutover(run); err != nil {
		t.Fatal(err)
	}
	// 注册一个"活 runner"——否则 ListCutovers 的 P0-5 恢复逻辑会先把它翻成
	// interrupted（后端重启遗留态），estop 就看不到 running 了。真实运行的
	// 割接总有活 runner（cutoverLaunch 注册），这里 mirror 同一状态。
	cutoverRunsMu.Lock()
	_, cancel := context.WithCancel(context.Background())
	cutoverRuns["CesT001"] = &cutoverRunHandle{cancel: cancel}
	cutoverRunsMu.Unlock()
	t.Cleanup(func() {
		cancel()
		cutoverRunsMu.Lock()
		delete(cutoverRuns, "CesT001")
		cutoverRunsMu.Unlock()
	})
	rep := m.EstopAll()
	if len(rep.CutoversHeld) != 1 || rep.CutoversHeld[0] != "CesT001" {
		t.Fatalf("estop must hold the running cutover, got %+v", rep)
	}
	c, err := GetCutover("CesT001")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != CutoverHold {
		t.Fatalf("status want hold, got %s", c.Status)
	}
	if !strings.Contains(c.HoldNote, "紧急停止") || !strings.Contains(c.HoldNote, "由人按") {
		t.Errorf("hold note must hand the decision to a human, got %q", c.HoldNote)
	}
}

// TestEstopGenerationFreezesProposal：ExecuteProposal 在步骤边界比对代数，
// estop 后代数变化 → 冻结。seam 级测试（设备无关）。
func TestEstopGenerationFreezesProposal(t *testing.T) {
	gen := EstopGeneration()
	if proposalEstopFrozen(gen) {
		t.Fatal("no estop yet — must not freeze")
	}
	estopGen.Add(1)
	if !proposalEstopFrozen(gen) {
		t.Fatal("after estop the captured generation must be stale (freeze)")
	}
}

// TestEstopIdempotentOnQuietFleet：没有 running job 时报告为空、不报错——
// 急停是幂等的安全动作，可以连按。
func TestEstopIdempotentOnQuietFleet(t *testing.T) {
	m := cutoverTestManager(t)
	for i := 0; i < 2; i++ {
		rep := m.EstopAll()
		if rep.Connections != 0 || len(rep.CutoversHeld) != 0 || len(rep.JobsPaused) != 0 || len(rep.Errors) != 0 {
			t.Fatalf("quiet fleet estop #%d must be a no-op, got %+v", i+1, rep)
		}
	}
}

// TestEstopSkipsAlreadyPausedJob：已在断点暂停的 job 不被 estop 重复暂停、
// 不计入 JobsPaused（运行中 job 被暂停的场景见 TestEstopPausesRunningJobWithNote）。
func TestEstopSkipsAlreadyPausedJob(t *testing.T) {
	m := cutoverTestManager(t)
	j, err := m.JobStart(&Job{
		Name: "estop-job",
		Steps: []JobStep{
			{Name: "s1", Device: "sw1", Command: "display version"},
			{Name: "s2", Device: "sw1", Command: "display device", PauseBefore: true},
		},
		Budget: JobBudget{MaxWallSec: 60, MaxCommands: 100, FailStreak: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	// s1 立即完成、s2 断点暂停——等它到断点再 estop，验证 pause 路径幂等。
	waitJobStatus(t, j.ID, JobPaused, "断点")
	rep := m.EstopAll()
	if len(rep.JobsPaused) != 0 {
		t.Errorf("already-paused job must not be re-paused, got %+v", rep)
	}
	got, err := GetJob(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != JobPaused {
		t.Fatalf("job must stay paused, got %s", got.Status)
	}
}

// ── 急停落在执行链路中段（割接/Job）的折叠与暂停语义 ──

// Cursor 不动、run 停在 hold（红线：不 abort、不丢提案去向）。
func TestEstopMidGateFoldsStepAndHolds(t *testing.T) {
	m := cutoverTestManager(t)
	pid := approvedVlanProposal(t, m)
	def := &CutoverRun{
		Name: "estop mid-gate", Deadline: time.Now().Add(time.Hour),
		Steps: []CutoverStep{{
			Label: "payload", ProposalID: pid, Device: "sw1", Command: "display version",
			Gate: &CutoverGate{Device: "sw1", Command: "display version", Expect: "Versatile", SustainSec: 2, TimeoutSec: 20},
		}},
	}
	c, err := m.CutoverStart(def)
	if err != nil {
		t.Fatal(err)
	}
	// 立即急停：落在提案执行或 gate 持续窗内（gate 在折叠前不落盘，无法从
	// 磁盘观察 gating——直接靠折叠后的终态断言）。gate 的 ctx 不被 estop 取消，
	// 会攒满 sustain 后带着 approved 状态到达折叠段。
	rep := m.EstopAll()
	if len(rep.CutoversHeld) != 1 {
		t.Fatalf("estop must hold the running cutover, got %+v", rep)
	}
	waitCutover(t, c.ID, CutoverHold, "紧急停止")
	// gate 攒满 sustain 后 runner 到折叠段：外部 hold 下状态落盘、Cursor 不动。
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := GetCutover(c.ID)
		if err != nil {
			t.Fatalf("get cutover during fold wait: %v", err)
		}
		if got.Cursor == 0 && (got.Steps[0].Status == CutoverStepApproved || got.Steps[0].Status == CutoverStepDone) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fold never landed: status %s cursor %d step %s", got.Status, got.Cursor, got.Steps[0].Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, _ := GetCutover(c.ID)
	if got.Status != CutoverHold {
		t.Errorf("status must stay hold (never aborted), got %s", got.Status)
	}
}

// G2：急停冻结的 partial 提案——割接里「继续」确定性失败并回 hold（安全方向），

// 「回退」能撤销 partial 已落盘前缀（此前 failed/skipped 提案步被回退漏掉）。
func TestEstopFrozenPartialContinueFailsRollbackUnwinds(t *testing.T) {
	m := cutoverTestManager(t)
	// 磁盘直接构造 partial 提案（模拟急停冻结后的现场：第 1 步已落盘）。
	p := &Proposal{Intent: "estop partial payload", Steps: []ProposalStep{{
		Device: "sw1", Commands: []string{"vlan 100"}, Rollback: []string{"undo vlan 100"},
	}}}
	if err := m.ValidateProposal(p); err != nil {
		t.Fatal(err)
	}
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}
	p.Status = ProposalPartial
	p.Steps[0].Applied = true
	p.Steps[0].AppliedCmds = 1
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	run := &CutoverRun{
		ID: "CesG2a", Name: "partial continue", Deadline: now.Add(time.Hour),
		Status: CutoverHold, Cursor: 0, HoldNote: "紧急停止", StartedAt: &now,
		Steps: []CutoverStep{{Label: "payload", ProposalID: p.ID, Device: "sw1", Command: "display version"}},
	}
	if err := saveCutover(run); err != nil {
		t.Fatal(err)
	}
	// 继续：ExecuteProposal 拒绝非 approved → 步骤失败 → 回 hold（失败关闭，
	// 不重复打设备）。
	if _, err := m.CutoverContinue("CesG2a"); err != nil {
		t.Fatal(err)
	}
	got := waitCutover(t, "CesG2a", CutoverHold, "执行失败")
	if got.Steps[0].Status != CutoverStepFailed {
		t.Errorf("continue against a partial proposal must fail the step, got %s", got.Steps[0].Status)
	}
	// 回退：failed 提案步纳入回退范围——partial 前缀被撤销。
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rb, err := m.CutoverRollback(ctx, "CesG2a")
	if err != nil {
		t.Fatal(err)
	}
	if rb.Status != CutoverAborted {
		t.Fatalf("rollback should end aborted, got %s", rb.Status)
	}
	if rb.Steps[0].Status != CutoverStepRolled {
		t.Errorf("failed proposal step must be rolled back, got %s", rb.Steps[0].Status)
	}
	if !strings.Contains(rb.HoldNote, "1/1") {
		t.Errorf("hold note should carry rollback progress, got %q", rb.HoldNote)
	}
}

// G3：EstopAll 真正暂停一个 running job，PauseNote 带急停文案（区别于人工暂停）。
func TestEstopPausesRunningJobWithNote(t *testing.T) {
	m := cutoverTestManager(t)
	steps := make([]JobStep, 0, 40)
	for i := 0; i < 40; i++ {
		steps = append(steps, JobStep{Name: "s", Device: "sw1", Command: "display version"})
	}
	j, err := m.JobStart(&Job{Name: "estop-note", Steps: steps,
		Budget: JobBudget{MaxWallSec: 120, MaxCommands: 500, FailStreak: 500}})
	if err != nil {
		t.Fatal(err)
	}
	// 等 runner 真正起跑再急停——否则 stub 过快时 40 步可能在列举前跑完。
	waitJobStatus(t, j.ID, JobRunning, "")
	rep := m.EstopAll()
	paused := false
	for _, id := range rep.JobsPaused {
		if id == j.ID {
			paused = true
		}
	}
	if !paused {
		t.Fatalf("running job must be paused by estop, got %+v (job %s)", rep, j.ID)
	}
	waitJobStatus(t, j.ID, JobPaused, "紧急停止")
}
