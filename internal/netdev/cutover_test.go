package netdev

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

func cutoverTestManager(t *testing.T) *Manager {
	t.Helper()
	m, _ := guardrailManager(t, config.NetDevGuardrails{})
	jobsDirOverride = t.TempDir()
	t.Cleanup(func() { jobsDirOverride = "" })
	cutoversDirOverride = t.TempDir()
	t.Cleanup(func() { cutoversDirOverride = "" })
	netdevStateDirOverr = t.TempDir()
	t.Cleanup(func() { netdevStateDirOverr = "" })
	SetBackupsDir(t.TempDir())
	return m
}

func waitCutover(t *testing.T, id, want string, notePart string) *CutoverRun {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := GetCutover(id)
		if err != nil {
			t.Fatalf("get cutover: %v", err)
		}
		if c.Status == want && (notePart == "" || strings.Contains(c.HoldNote, notePart)) {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("cutover %s: status %s (note %q), want %s/%q; steps %+v", id, c.Status, c.HoldNote, want, notePart, c.Steps)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func approvedVlanProposal(t *testing.T, m *Manager) string {
	t.Helper()
	p := &Proposal{Intent: "cutover payload: vlan 100", Steps: []ProposalStep{{
		Device: "sw1", Commands: []string{"vlan 100"}, Rollback: []string{"undo vlan 100"},
	}}}
	if err := m.ValidateProposal(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := SaveProposal(p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ApproveProposal(p.ID, false); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

// The full §7.2 chain: gate-sustained step → proposal step holds at its
// decision point → 继续 finishes → before/after snapshots + report.
func TestCutoverRunbookE2E(t *testing.T) {
	m := cutoverTestManager(t)
	pid := approvedVlanProposal(t, m)

	run, err := m.CutoverStart(&CutoverRun{
		Name:     "core-sw cutover",
		Deadline: time.Now().Add(30 * time.Minute),
		Steps: []CutoverStep{
			{Label: "确认版本", Device: "sw1", Command: "display version", EstSec: 30,
				Gate: &CutoverGate{Device: "sw1", Command: "display version", Expect: "Versatile", SustainSec: 1, TimeoutSec: 10}},
			{Label: "下发 VLAN 100", ProposalID: pid, EstSec: 60, DecisionPoint: true, Impact: "VLAN 100 已下发，核心侧生效"},
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(run.PreSnapshot) != 1 || run.PreSnapshot["sw1"] == "" {
		t.Fatalf("pre-snapshot = %v", run.PreSnapshot)
	}

	held := waitCutover(t, run.ID, CutoverHold, "决策点")
	if held.Steps[0].Status != CutoverStepDone {
		t.Fatalf("gate step status = %s (err %q)", held.Steps[0].Status, held.Steps[0].Error)
	}
	if held.Steps[1].Status != CutoverStepApproved {
		t.Fatalf("proposal step status = %s", held.Steps[1].Status)
	}

	if _, err := m.CutoverContinue(run.ID); err != nil {
		t.Fatalf("continue: %v", err)
	}
	done := waitCutover(t, run.ID, CutoverDone, "")
	if done.PostSnapshot["sw1"] == "" {
		t.Fatalf("post-snapshot = %v", done.PostSnapshot)
	}
	if !strings.Contains(done.Report, "前后配置对比") || !strings.Contains(done.Report, "sw1") {
		t.Fatalf("report = %.300q", done.Report)
	}
}

// At a decision point the human can press 回退 instead: the executed proposal
// unwinds and the run ends aborted with a report.
func TestCutoverRollbackAtDecisionPoint(t *testing.T) {
	m := cutoverTestManager(t)
	pid := approvedVlanProposal(t, m)

	run, err := m.CutoverStart(&CutoverRun{
		Name:     "rollback path",
		Deadline: time.Now().Add(30 * time.Minute),
		Steps: []CutoverStep{
			{Label: "变更", ProposalID: pid, EstSec: 60, DecisionPoint: true, Impact: "变更已下发"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitCutover(t, run.ID, CutoverHold, "决策点")

	rb, err := m.CutoverRollback(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Status != CutoverAborted || rb.Steps[0].Status != CutoverStepRolled {
		t.Fatalf("run = %s, step = %s (note %q)", rb.Status, rb.Steps[0].Status, rb.HoldNote)
	}
	p, _ := GetProposal(pid)
	if p.Status != ProposalDraft || p.Steps[0].Applied {
		t.Fatalf("proposal after rollback: %s applied=%v", p.Status, p.Steps[0].Applied)
	}
	if !strings.Contains(rb.Report, "↩️") {
		t.Fatalf("report misses rolled marker: %.200q", rb.Report)
	}
}

// A never-matching gate stops the run at the failure with the impact text.
func TestCutoverGateFailureHolds(t *testing.T) {
	m := cutoverTestManager(t)
	pid := approvedVlanProposal(t, m)
	run, err := m.CutoverStart(&CutoverRun{
		Name:     "bad gate",
		Deadline: time.Now().Add(30 * time.Minute),
		Steps: []CutoverStep{
			{Label: "变更", ProposalID: pid, EstSec: 60, Impact: "OSPF 未收敛",
				Gate: &CutoverGate{Device: "sw1", Command: "display version", Expect: "NX-OS", SustainSec: 1, TimeoutSec: 2}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	held := waitCutover(t, run.ID, CutoverHold, "验证门未过")
	if !strings.Contains(held.HoldNote, "OSPF 未收敛") {
		t.Fatalf("hold note = %q — impact text missing", held.HoldNote)
	}
}

// The total countdown is a wall: continuing past the deadline refuses to run
// more steps.
func TestCutoverCountdownExhausted(t *testing.T) {
	m := cutoverTestManager(t)
	run, err := m.CutoverStart(&CutoverRun{
		Name:     "tight window",
		Deadline: time.Now().Add(2 * time.Second),
		Steps: []CutoverStep{
			{Label: "一步", Device: "sw1", Command: "display version", EstSec: 30, DecisionPoint: true, Impact: "检查点"},
			{Label: "下一步", Device: "sw1", Command: "display version", EstSec: 30},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitCutover(t, run.ID, CutoverHold, "决策点")
	time.Sleep(2200 * time.Millisecond) // let the window lapse
	if _, err := m.CutoverContinue(run.ID); err != nil {
		t.Fatalf("continue: %v", err)
	}
	held := waitCutover(t, run.ID, CutoverHold, "倒计时")
	if held.Steps[1].Status != CutoverStepPending {
		t.Fatalf("step 2 ran past the deadline: %+v", held.Steps[1])
	}
}

// Runbooks referencing unapproved proposals never start.
func TestCutoverRequiresApprovedProposals(t *testing.T) {
	m := cutoverTestManager(t)
	p := &Proposal{Intent: "draft only", Steps: []ProposalStep{{Device: "sw1", Commands: []string{"vlan 100"}, Rollback: []string{"undo vlan 100"}}}}
	SaveProposal(p)
	if _, err := m.CutoverStart(&CutoverRun{
		Name: "bad", Deadline: time.Now().Add(time.Hour),
		Steps: []CutoverStep{{Label: "x", ProposalID: p.ID}},
	}); err == nil || !strings.Contains(err.Error(), "approve") {
		t.Fatalf("unapproved proposal accepted: %v", err)
	}
	if _, err := m.CutoverStart(&CutoverRun{
		Name: "bad", Deadline: time.Now().Add(time.Hour),
		Steps: []CutoverStep{{Label: "x", Device: "sw1"}},
	}); err == nil {
		t.Fatal("empty step accepted")
	}
}

// TestCutoverPrecheckRedLightOverride（SCENARIO_SPEC S1-1）：影响设备含一台
// 不可达机 → 预检红灯停窗前（precheck-failed，runner 未启动）；人工放行后
// 恢复 running 并真正进入变更窗口。
func TestCutoverPrecheckRedLightOverride(t *testing.T) {
	m := cutoverTestManager(t)
	pid := approvedVlanProposal(t, m)

	def := &CutoverRun{
		Name:     "precheck-red",
		Deadline: time.Now().Add(20 * time.Minute),
		Steps: []CutoverStep{{
			Label: "payload", ProposalID: pid,
		}},
		Precheck: &CutoverPrecheckDef{Battery: "standard"},
	}
	c, err := m.CutoverStart(def)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if c.Status != CutoverPrecheckFailed {
		t.Fatalf("status = %s, want precheck-failed（dead 设备电池应失败）", c.Status)
	}
	if c.PrecheckReport == nil || c.PrecheckReport.AllPass {
		t.Fatalf("precheck report = %+v", c.PrecheckReport)
	}
	// 窗口未启动：cursor 应仍为 0 且无 StartedAt 推进痕迹——Status 已证。
	// 人工放行 → running。
	c2, err := m.CutoverPrecheckOverride(c.ID)
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if c2.Status != CutoverRunning {
		t.Fatalf("after override status = %s, want running", c2.Status)
	}
	// 放行后 runner 启动并推进到终态（无决策点的提案步直通 approved/done）。
	got := waitCutover(t, c.ID, CutoverDone, "")
	if len(got.Steps) == 0 || got.Steps[0].Status != CutoverStepApproved {
		t.Fatalf("runner did not advance after override: %+v", got.Steps)
	}
}

// TestCutoverPrecheckGreenLight：可达设备全过 → 直接 running，不停窗前。
func TestCutoverPrecheckGreenLight(t *testing.T) {
	m := cutoverTestManager(t)
	pid := approvedVlanProposal(t, m)
	def := &CutoverRun{
		Name:     "precheck-green",
		Deadline: time.Now().Add(20 * time.Minute),
		Steps: []CutoverStep{{Label: "payload", ProposalID: pid,
			Device: "sw1", Command: "display version"}},
		Precheck: &CutoverPrecheckDef{Battery: "off", Probes: []CutoverPreProbe{
			{Kind: "command", Device: "sw1", Cmd: "display version", Expect: "Versatile"},
		}},
	}
	c, err := m.CutoverStart(def)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if c.Status == CutoverPrecheckFailed {
		t.Fatalf("unexpected red light: %+v", c.PrecheckReport)
	}
	if c.Status != CutoverRunning {
		t.Fatalf("status = %s, want running", c.Status)
	}
	// 等 runner 跑到终态再返回：泄漏的 runner 会与下一个测试 cleanup 写目录
	// override 全局变量构成数据竞争（-race 实证，），且落盘会
	// 污染下一个测试的临时目录。
	waitCutover(t, c.ID, CutoverDone, "")
}

// TestSaveCutoverAtomicConcurrentReads: saveCutover lands via tmp+rename, so a
// concurrent reader must never observe a torn JSON store — 50 saves racing a
// read+parse loop. Platform caveat: on Windows, fileutil.ReplaceFile's
// rename→copy fallback (a reader holding dest open blocks the rename, since Go
// opens lack FILE_SHARE_DELETE) briefly truncates dest in place, so a
// zero-length read is tolerated ONLY when the very next reads recover; any
// nonzero partial JSON, a persistent empty store, or a save whose post-read
// fails to parse is a failure.
func TestSaveCutoverAtomicConcurrentReads(t *testing.T) {
	cutoversDirOverride = t.TempDir()
	t.Cleanup(func() { cutoversDirOverride = "" })

	c := &CutoverRun{ID: "CT-atomic", Name: "atomic save", Status: CutoverRunning,
		Deadline: time.Now().Add(time.Hour)}
	if err := saveCutover(c); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	if got, err := GetCutover(c.ID); err != nil || got.Name != c.Name {
		t.Fatalf("read back after save: %v, %+v", err, got)
	}
	path := filepath.Join(cutoversDir(), c.ID+".json")
	parses := func(b []byte) bool { return json.Unmarshal(b, new(CutoverRun)) == nil }

	stop := make(chan struct{})
	var wg sync.WaitGroup
	readFail := make(chan error, 1)
	fail := func(format string, args ...any) {
		select {
		case readFail <- fmt.Errorf(format, args...):
		default:
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		openErrs := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := os.ReadFile(path)
			if err != nil {
				openErrs++ // transient sharing violation while the writer swaps files
				if openErrs > 500 {
					fail("dest unreadable: %v", err)
					return
				}
				continue
			}
			openErrs = 0
			if !parses(b) {
				if len(b) == 0 { // fallback truncate window: must recover immediately
					recovered := false
					for i := 0; i < 200 && !recovered; i++ {
						time.Sleep(100 * time.Microsecond)
						if rb, err := os.ReadFile(path); err == nil && parses(rb) {
							recovered = true
						}
					}
					if !recovered {
						fail("empty store persisted across retries")
						return
					}
					continue
				}
				fail("torn store (%d bytes)", len(b))
				return
			}
			time.Sleep(100 * time.Microsecond) // let some renames land uncontended too
		}
	}()
	for i := 0; i < 50; i++ {
		c.HoldNote = fmt.Sprintf("pass %d", i)
		if err := saveCutover(c); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("save %d: %v", i, err)
		}
		// A returned save must already be a complete, parseable document.
		b, err := os.ReadFile(path)
		if err != nil || !parses(b) {
			close(stop)
			wg.Wait()
			t.Fatalf("post-save read %d: %v (parses=%v, %d bytes)", i, err, parses(b), len(b))
		}
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-readFail:
		t.Fatal(err)
	default:
	}
}
