package netdev

// cutover.go — 割接模式（NETDEV_SPEC_V2 §7.2）：整份 runbook 带总倒计时逐
// 步执行。步骤要么执行一份「已批准」变更（变更动作的唯一形态），要么跑
// 一条只读命令；步后可挂语义验证门（Expect 持续 SustainSec 才算过），门
// 不过或到预设回退决策点即 hold——回退按钮与影响描述并列，决策是人按
// 的。割接前后各拍一次基线快照（复用配置备份库），结束时产出前后对比
// 报告。把「Word 文档 + 对讲机」的深夜割接变成可执行、可回退、可复盘
// 的流程。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zzycxz/fairpeer/internal/fileutil"
	"log/slog"
	"strconv"
)

// Cutover statuses.
const (
	CutoverRunning = "running"
	CutoverHold    = "hold" // stopped at a decision point / failed gate / countdown spent
	CutoverDone    = "done"
	CutoverFailed  = "failed"
	CutoverAborted = "aborted"
	// S1-1：预检红灯停在窗口前——CutoverPrecheckOverride 人工放行后恢复。
	CutoverPrecheckFailed = "precheck-failed"
	// P0-5：后端重启时 runner 消失但盘上仍是 running——大屏照常倒计时而
	// 每个动作都被拒；标记为 interrupted 后 Continue 可接续、Abort 可放弃。
	CutoverInterrupted = "interrupted"
)

// Cutover step statuses.
const (
	CutoverStepPending  = "pending"
	CutoverStepRunning  = "running"
	CutoverStepGating   = "gating"
	CutoverStepDone     = "done"
	CutoverStepFailed   = "failed"
	CutoverStepSkipped  = "skipped"
	CutoverStepRolled   = "rolled-back"
	CutoverStepApproved = "approved" // a proposal step that finished its change
)

// CutoverGate is a semantic verification gate: Command's output must match
// Expect CONTINUOUSLY for SustainSec (e.g. "OSPF 邻居 Full 且持续 60s").
type CutoverGate struct {
	Device     string `json:"device"`
	Command    string `json:"command"`
	Expect     string `json:"expect"`
	SustainSec int    `json:"sustain_sec,omitempty"` // default 30
	TimeoutSec int    `json:"timeout_sec,omitempty"` // default 2×sustain+90
}

// CutoverStep is one runbook entry.
type CutoverStep struct {
	Label         string       `json:"label"`
	EstSec        int          `json:"est_sec,omitempty"`     // 预计耗时（倒计时排程显示）
	ProposalID    string       `json:"proposal_id,omitempty"` // execute an APPROVED proposal
	Device        string       `json:"device,omitempty"`      // …or one sealed read command
	Command       string       `json:"command,omitempty"`
	Gate          *CutoverGate `json:"gate,omitempty"`           // post-step verification gate
	DecisionPoint bool         `json:"decision_point,omitempty"` // 回退决策点：完成后 hold 等人
	Impact        string       `json:"impact,omitempty"`         // 影响描述（决策点并列展示）

	Status    string     `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Output    string     `json:"output,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// CutoverPrecheckDef（SCENARIO_SPEC S1-1）：变更窗口前的故障先行探测——
// 执行前对影响设备跑只读电池/基线比对/探测，红灯需人工放行才进入变更。
type CutoverPrecheckDef struct {
	// Battery: "standard"（每设备只读电池，默认）| "baseline"（+基线违例比对）| "off"。
	Battery string            `json:"battery,omitempty"`
	Probes  []CutoverPreProbe `json:"probes,omitempty"` // 追加探测（设备可达/业务命令）
}

// CutoverPreProbe is one extra pre-window probe.
type CutoverPreProbe struct {
	Kind   string `json:"kind"`             // ping | command
	Device string `json:"device"`           // 执行设备（ping 从该设备源发）
	Target string `json:"target,omitempty"` // ping 的目标 IP
	Cmd    string `json:"cmd,omitempty"`    // command 的只读命令
	Expect string `json:"expect,omitempty"` // 输出须命中的正则（空=仅要不报错）
}

// PrecheckItem is one check's outcome.
type PrecheckItem struct {
	Device string `json:"device"`
	Check  string `json:"check"` // battery:<cmd> | probe:<kind>:<target> | baseline
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

// PrecheckReport is the window-front health snapshot (S1-1)，与步后 Gate 对照。
type PrecheckReport struct {
	StartedAt time.Time      `json:"started_at"`
	AllPass   bool           `json:"all_pass"`
	Items     []PrecheckItem `json:"items"`
}

// CutoverRun is one cutover execution.
type CutoverRun struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Deadline time.Time     `json:"deadline"` // 总倒计时（割接窗口结束）
	Steps    []CutoverStep `json:"steps"`
	Status   string        `json:"status"`
	HoldNote string        `json:"hold_note,omitempty"`
	Cursor   int           `json:"cursor"`

	// S1-1：变更前预检定义与报告（红灯 → precheck-failed，人工放行后进入）。
	Precheck       *CutoverPrecheckDef `json:"precheck,omitempty"`
	PrecheckReport *PrecheckReport     `json:"precheck_report,omitempty"`

	// 割接前后基线快照（device → backup id）+ 对比报告。
	PreSnapshot  map[string]string `json:"pre_snapshot,omitempty"`
	PostSnapshot map[string]string `json:"post_snapshot,omitempty"`
	Report       string            `json:"report,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// cutoversDirOverride isolates cutover storage in tests.
var cutoversDirOverride string

func cutoversDir() string {
	if cutoversDirOverride != "" {
		return cutoversDirOverride
	}
	return filepath.Join(netdevStateDir(), "cutovers")
}

var (
	cutoverMu     sync.Mutex
	cutoverSeq    int
	cutoverSeqDay string
)

// newCutoverID mints C<day>-N seeded from the CUTOVERS DIR, not memory: the
// in-process counter reset on restart, so the first cutover after a restart
// silently overwrote the day's existing file (P0-5). Mirrors newProposalID.
func newCutoverID() string {
	cutoverMu.Lock()
	defer cutoverMu.Unlock()
	day := time.Now().Format("20060102")
	if cutoverSeq == 0 || cutoverSeqDay != day {
		cutoverSeq = maxCutoverSeqForDay(day)
		cutoverSeqDay = day
	}
	cutoverSeq++
	return fmt.Sprintf("C%s-%d", day, cutoverSeq)
}

// maxCutoverSeqForDay scans the cutovers dir for the highest -N suffix of the
// day (0 when none exist yet).
func maxCutoverSeqForDay(day string) int {
	entries, err := os.ReadDir(cutoversDir())
	if err != nil {
		return 0
	}
	prefix := "C" + day + "-"
	best := 0
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".json")
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(name, prefix)); err == nil && n > best {
			best = n
		}
	}
	return best
}

func saveCutover(c *CutoverRun) error {
	if err := os.MkdirAll(cutoversDir(), 0o700); err != nil {
		return err
	}
	cutoverMu.Lock()
	defer cutoverMu.Unlock()
	return saveCutoverLocked(c)
}

// saveCutoverLocked is saveCutover with the caller holding cutoverMu — the
// write half of an atomic load→check→set→save transition.
func saveCutoverLocked(c *CutoverRun) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(filepath.Join(cutoversDir(), c.ID+".json"), b, 0o600)
}

// GetCutover loads one run.
func GetCutover(id string) (*CutoverRun, error) {
	// 轮2攻击面审查：id 从 webview 可达（NetDevCutoverGet/RunbookTplExtract
	// 等直塞进来）——无校验即路径穿越读任意 .json。对齐 storeid 纪律。
	if !validStoreID(id) {
		return nil, fmt.Errorf("cutover %q: invalid id", id)
	}
	b, err := os.ReadFile(filepath.Join(cutoversDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var c CutoverRun
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCutovers returns runs newest-first.
func ListCutovers() ([]*CutoverRun, error) {
	entries, err := os.ReadDir(cutoversDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	recoverOrphanedRunningCutovers(entries)
	var out []*CutoverRun
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if c, err := GetCutover(strings.TrimSuffix(e.Name(), ".json")); err == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].CreatedAt.After(out[k].CreatedAt) })
	return out, nil
}

// recoverOrphanedRunningCutovers sweeps lazily from ListCutovers (mirrors
// proposals' recoverStaleExecuting / jobs' recoverOrphanedRunningJobs): a
// persisted status=running cutover with NO live runner context means the
// backend restarted mid-run. Mark interrupted so the board can say
// “执行已中断” and Continue/Abort both work again (P0-5).
func recoverOrphanedRunningCutovers(entries []os.DirEntry) {
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		c, err := GetCutover(id)
		if err != nil || c.Status != CutoverRunning {
			continue
		}
		cutoverRunsMu.Lock()
		_, live := cutoverRuns[id]
		cutoverRunsMu.Unlock()
		if live {
			continue
		}
		cutoverMu.Lock()
		if c2, err := GetCutover(id); err == nil && c2.Status == CutoverRunning {
			c2.Status = CutoverInterrupted
			c2.HoldNote = "后端重启，执行已中断（已完成步骤保留，可继续或放弃）"
			if err := saveCutoverLocked(c2); err != nil {
				slog.Warn("netdev: mark interrupted cutover failed", "id", id, "err", err)
			} else {
				_ = AppendAudit(Audit{Device: "(cutover)", Command: "interrupted " + id, Class: "cutover", Status: AuditFailure})
			}
		}
		cutoverMu.Unlock()
	}
}

// ── runner registry ──────────────────────────────────────────────────────────

// cutoverRunHandle pairs a runner's cancel with its launch generation: a
// relaunched runner (Continue after estop-hold) invalidates the stale one, and
// the stale runner's fold section must not write state (逐行精读 R2 P1-2).
type cutoverRunHandle struct {
	cancel context.CancelFunc
	gen    int64
}

var cutoverLaunchGen atomic.Int64

var (
	cutoverRunsMu sync.Mutex
	cutoverRuns   = map[string]*cutoverRunHandle{}
)

// CutoverStart validates the runbook, snapshots the network baseline, and
// launches the runner. Every proposal a step references must already be
// approved — the cutover executes the approved change, it never approves.
func (m *Manager) CutoverStart(def *CutoverRun) (*CutoverRun, error) {
	if def.ID != "" {
		return nil, fmt.Errorf("cutover: new runs take no ID (assigned here)")
	}
	if strings.TrimSpace(def.Name) == "" {
		return nil, fmt.Errorf("cutover: name is required")
	}
	if len(def.Steps) == 0 {
		return nil, fmt.Errorf("cutover %q: no steps", def.Name)
	}
	if def.Deadline.IsZero() || !def.Deadline.After(time.Now()) {
		return nil, fmt.Errorf("cutover %q: deadline must be in the future (总倒计时)", def.Name)
	}
	devices := map[string]bool{}
	for i := range def.Steps {
		s := &def.Steps[i]
		if s.Label == "" {
			s.Label = fmt.Sprintf("step-%d", i+1)
		}
		if s.EstSec <= 0 {
			s.EstSec = 60
		}
		switch {
		case s.ProposalID != "":
			p, err := GetProposal(s.ProposalID)
			if err != nil {
				return nil, fmt.Errorf("cutover step %q: proposal %s: %v", s.Label, s.ProposalID, err)
			}
			if p.Status != ProposalApproved {
				return nil, fmt.Errorf("cutover step %q: proposal %s is %s — approve it before the cutover starts", s.Label, s.ProposalID, p.Status)
			}
			for _, st := range p.Steps {
				devices[st.Device] = true
			}
		case s.Device != "" && s.Command != "":
			if strings.ContainsAny(s.Command, "\n\r\x00") {
				return nil, fmt.Errorf("cutover step %q: one command per step", s.Label)
			}
			if _, ok := m.cfg.NetDevDeviceByName(s.Device); !ok {
				return nil, fmt.Errorf("cutover step %q: device %q not in inventory", s.Label, s.Device)
			}
			devices[s.Device] = true
		default:
			return nil, fmt.Errorf("cutover step %q: needs proposal_id or device+command", s.Label)
		}
		if s.Gate != nil {
			if s.Gate.Device == "" || s.Gate.Command == "" || s.Gate.Expect == "" {
				return nil, fmt.Errorf("cutover step %q: gate needs device+command+expect", s.Label)
			}
			if _, err := regexp.Compile(s.Gate.Expect); err != nil {
				return nil, fmt.Errorf("cutover step %q: gate expect: %v", s.Label, err)
			}
			if s.Gate.SustainSec <= 0 {
				s.Gate.SustainSec = 30
			}
		}
		s.Status = CutoverStepPending
	}

	def.ID = newCutoverID()
	// B4：先预注册 runner 句柄再落盘——落盘与 launch 之间的间隙里，并发
	// ListCutovers（大屏/estop 高频调用）的孤儿扫描查不到活 runner 会把
	// 新 run 误判 interrupted。precheck-failed 路径在返回前清理。
	_, regCancel := context.WithCancel(context.Background())
	cutoverRunsMu.Lock()
	cutoverRuns[def.ID] = &cutoverRunHandle{cancel: regCancel, gen: cutoverLaunchGen.Add(1)}
	cutoverRunsMu.Unlock()
	cleanupReg := func() {
		regCancel()
		cutoverRunsMu.Lock()
		delete(cutoverRuns, def.ID)
		cutoverRunsMu.Unlock()
	}
	StateEventSnap(StateEventCutoverStart, def.ID, StateActorUser, filepath.Join(cutoversDir(), def.ID+".json"))
	def.Status = CutoverRunning
	def.CreatedAt = time.Now()
	now := time.Now()
	def.StartedAt = &now
	def.Cursor = 0

	// S1-1：变更窗口前预检（故障先行探测）。红灯 → precheck-failed 停在窗口
	// 前，CutoverPrecheckOverride 人工放行后才会启动 runner。
	if err := m.runCutoverPrecheck(def, devices); err != nil {
		return nil, err
	}
	if def.Status == CutoverPrecheckFailed {
		if err := saveCutover(def); err != nil {
			cleanupReg()
			return nil, err
		}
		_ = AppendAudit(Audit{Device: "(cutover)", Command: "precheck-failed " + def.ID, Class: "cutover", Status: AuditDeviceError})
		cleanupReg()
		return def, nil
	}

	// 割接前全网基线快照（§7.2：割接前后自动各拍一次）。
	def.PreSnapshot = map[string]string{}
	for name := range devices {
		d, ok := m.cfg.NetDevDeviceByName(name)
		if !ok {
			continue
		}
		if bc := backupCommand(drvKey(d)); bc == "" {
			continue // snapshots cover config-bearing network devices
		}
		vers, err := m.RunBackup(context.Background(), name)
		if err != nil || len(vers) == 0 {
			continue // snapshot best-effort; the report notes what it missed
		}
		def.PreSnapshot[name] = vers[len(vers)-1].ID
	}

	if err := saveCutover(def); err != nil {
		return nil, err
	}
	_ = AppendAudit(Audit{Device: "(cutover)", Command: "start " + def.ID + " " + def.Name, Class: "cutover", Status: AuditOK})
	m.cutoverLaunch(def.ID)
	return def, nil
}

func (m *Manager) cutoverLaunch(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	gen := cutoverLaunchGen.Add(1)
	cutoverRunsMu.Lock()
	if old, ok := cutoverRuns[id]; ok {
		old.cancel()
	}
	cutoverRuns[id] = &cutoverRunHandle{cancel: cancel, gen: gen}
	cutoverRunsMu.Unlock()
	go m.cutoverRunner(ctx, id, gen)
}

// CutoverContinue presses 继续 at a hold.
// runCutoverPrecheck executes the window-front battery over the run's devices
// (S1-1). nil Precheck or Battery=="off" skips (report marks skipped).
func (m *Manager) runCutoverPrecheck(def *CutoverRun, devices map[string]bool) error {
	pc := def.Precheck
	if pc == nil || pc.Battery == "off" {
		def.PrecheckReport = &PrecheckReport{StartedAt: time.Now(), AllPass: true}
		return nil
	}
	rep := &PrecheckReport{StartedAt: time.Now(), Items: []PrecheckItem{}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	battery := pc.Battery
	if battery == "" {
		battery = "standard"
	}
	for name := range devices {
		d, ok := m.cfg.NetDevDeviceByName(name)
		if !ok {
			continue
		}
		drv, ok := m.driverFor(d)
		if !ok {
			continue
		}
		for _, cmd := range inspectionBattery(drv) {
			res := m.Exec(ctx, name, cmd)
			item := PrecheckItem{Device: name, Check: "battery:" + cmd, Pass: !res.Refused && !res.IsError, Detail: firstLineOf(refusalOrOutput(res))}
			rep.Items = append(rep.Items, item)
		}
		if battery == "baseline" {
			// 读 running-config + 纯函数规则比对（不立案——预检只出报告）。
			if cmd, ok := RunningConfigCommand(drv.Key()); ok {
				if res, rerr := m.runRead(ctx, d, drv, cmd); rerr == nil {
					vs := CheckBaseline(drv.Key(), res.Output)
					pass := true
					for _, v := range vs {
						if v.Severity == SeverityCritical || v.Severity == SeverityWarning {
							pass = false
							rep.Items = append(rep.Items, PrecheckItem{Device: name, Check: "baseline:" + v.Rule, Pass: false, Detail: v.Title})
						}
					}
					if pass {
						rep.Items = append(rep.Items, PrecheckItem{Device: name, Check: "baseline", Pass: true, Detail: "no critical/warning violations"})
					}
				} else {
					rep.Items = append(rep.Items, PrecheckItem{Device: name, Check: "baseline", Pass: false, Detail: "config read failed: " + rerr.Error()})
				}
			}
		}
	}
	for _, pr := range pc.Probes {
		switch pr.Kind {
		case "ping":
			cmd := "ping " + strings.TrimSpace(pr.Target)
			if strings.ContainsAny(cmd, "\n\r") || pr.Target == "" {
				rep.Items = append(rep.Items, PrecheckItem{Device: pr.Device, Check: "probe:ping:" + pr.Target, Pass: false, Detail: "bad probe target"})
				continue
			}
			res := m.Exec(ctx, pr.Device, cmd)
			rep.Items = append(rep.Items, PrecheckItem{Device: pr.Device, Check: "probe:ping:" + pr.Target, Pass: !res.Refused && !res.IsError, Detail: firstLineOf(refusalOrOutput(res))})
		case "command":
			res := m.Exec(ctx, pr.Device, pr.Cmd)
			pass := !res.Refused && !res.IsError
			if pass && pr.Expect != "" {
				re, err := regexp.Compile(pr.Expect)
				pass = err == nil && re.MatchString(res.Output)
			}
			rep.Items = append(rep.Items, PrecheckItem{Device: pr.Device, Check: "probe:command:" + pr.Cmd, Pass: pass, Detail: firstLineOf(refusalOrOutput(res))})
		default:
			// 未知 probe kind（拼写错误）必须显式失败——静默丢弃会让预检
			// 假绿灯，值班以为查了其实没查（逐行精读 R2 P3-13）。
			rep.Items = append(rep.Items, PrecheckItem{Device: pr.Device, Check: "probe:" + pr.Kind, Pass: false, Detail: "unknown probe kind"})
		}
	}
	rep.AllPass = true
	for _, it := range rep.Items {
		if !it.Pass {
			rep.AllPass = false
			break
		}
	}
	def.PrecheckReport = rep
	if !rep.AllPass {
		def.Status = CutoverPrecheckFailed
		def.HoldNote = "预检红灯——人工放行后进入变更窗口（放行动作入审计）"
	}
	return nil
}

// refusalOrOutput picks the human-readable line from an ExecResult.
func refusalOrOutput(res ExecResult) string {
	if res.Refusal != "" {
		return res.Refusal
	}
	return res.Output
}

// firstLineOf keeps item details to one line.
func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r\\"); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 120 {
		return string(r[:117]) + "…"
	}
	return s
}

// CutoverPrecheckOverride is the human 放行 after a red precheck (S1-1):
// audit-logged, then the runner starts as if precheck passed.
func (m *Manager) CutoverPrecheckOverride(id string) (*CutoverRun, error) {
	// NETDEV-9：状态翻转与落盘同临界区（saveCutoverLocked）——旧形状解锁后
	// 才写文件，双击"放行"时第二个 override 能通过文件里仍是 precheck-failed
	// 的检查，且第一个的迟到保存把 Cursor 回卷，runner 重读后对已执行的
	// direct-command step 再打一遍设备。同文件 CutoverContinue 同范式。
	cutoverMu.Lock()
	c, err := GetCutover(id)
	if err != nil {
		cutoverMu.Unlock()
		return nil, err
	}
	if c.Status != CutoverPrecheckFailed {
		cutoverMu.Unlock()
		return nil, fmt.Errorf("cutover %s is %s — 只有 precheck-failed 可放行", id, c.Status)
	}
	c.Status = CutoverRunning
	c.HoldNote = ""
	_ = saveCutoverLocked(c)
	cutoverMu.Unlock()
	StateEventSnap(StateEventCutoverStart, c.ID, StateActorUser, filepath.Join(cutoversDir(), c.ID+".json"))
	_ = AppendAudit(Audit{Device: "(cutover)", Command: "precheck-override " + c.ID, Class: "cutover", Status: AuditOK})
	m.cutoverLaunch(c.ID)
	return c, nil
}

func (m *Manager) CutoverContinue(id string) (*CutoverRun, error) {
	cutoverMu.Lock()
	c, err := GetCutover(id)
	if err != nil {
		cutoverMu.Unlock()
		return nil, err
	}
	if c.Status != CutoverHold && c.Status != CutoverInterrupted {
		cutoverMu.Unlock()
		return nil, fmt.Errorf("cutover %s: status %s — only held/interrupted cutovers continue", id, c.Status)
	}
	StateEventSnap(StateEventCutoverGo, id, StateActorUser, filepath.Join(cutoversDir(), id+".json"))
	c.Status = CutoverRunning
	c.HoldNote = ""
	if err := saveCutoverLocked(c); err != nil {
		cutoverMu.Unlock()
		return nil, err
	}
	cutoverMu.Unlock()
	_ = AppendAudit(Audit{Device: "(cutover)", Command: "continue " + id, Class: "cutover", Status: AuditOK})
	m.cutoverLaunch(id)
	return c, nil
}

// CutoverRollback presses 回退 at a hold: the run's executed proposals unwind
// newest-first (each still audited); the run ends aborted.
//
// P1-E1④: the whole unwind runs under cutoverMu like Continue/Abort. It used
// to be a lock-free read-modify-write, so an operator pressing 继续 while
// another pressed 回退 could have the runner execute step N+1 on a device
// while step N was being unwound — both sides reporting success.
//
// Known trade-off (, accepted): the lock is held across the whole
// unwind (minutes of device I/O), so an EstopAll arriving mid-rollback waits
// for it — the red button's cutover-hold is delayed, never skipped. Weakening
// the lock would break the rollback-vs-continue atomicity this exists for.
func (m *Manager) CutoverRollback(ctx context.Context, id string) (*CutoverRun, error) {
	cutoverMu.Lock()
	c, err := GetCutover(id)
	if err != nil {
		cutoverMu.Unlock()
		return nil, err
	}
	if c.Status != CutoverHold {
		cutoverMu.Unlock()
		return nil, fmt.Errorf("cutover %s: status %s — rollback happens at a decision point", id, c.Status)
	}
	StateEventSnap(StateEventCutoverBack, id, StateActorUser, filepath.Join(cutoversDir(), id+".json"))
	unresolved := []string{}
	candidates, rolled := 0, 0
	for i := len(c.Steps) - 1; i >= 0; i-- {
		s := &c.Steps[i]
		if s.ProposalID == "" {
			continue // read steps leave nothing to roll back
		}
		// approved/done = 正常落盘的变更步；failed/skipped = 提案已部分执行
		// 或被跳过但变更仍在设备上（急停冻结的 partial、门未过被跳过）。
		// 617-621 的 skip 注释一直承诺这一行为，实现此前漏掉了 failed/skipped
		// ——急停场景（estop 冻结提案 → 步骤 failed）是它最重要的用例。未真正
		// 执行过的提案会被 RollbackProposal 的前置检查安全拒绝，落入 failed
		// 记录，不误回滚。
		switch s.Status {
		case CutoverStepApproved, CutoverStepDone, CutoverStepFailed, CutoverStepSkipped:
		default:
			continue
		}
		// 从未落盘的提案（estop 落在第一步边界 → 零步 applied 的 partial、
		// 或 skipped 时提案还没执行）：无物可回滚——跳过且不计入候选，
		// 避免"成功回退了一次从未发生的变更"的审计谎报。
		if pr, err := GetProposal(s.ProposalID); err == nil {
			landed := false
			for i := range pr.Steps {
				if pr.Steps[i].Applied || pr.Steps[i].AppliedCmds > 0 {
					landed = true
					break
				}
			}
			if !landed {
				continue
			}
		}
		candidates++
		if _, err := m.RollbackProposal(ctx, s.ProposalID); err != nil {
			// 单个坏点不再终结其余候选的回退（批次 B3）：逐项汇总，
			// HoldNote 列出未回滚清单，人工接管的范围从"全部"缩小到 M。
			unresolved = append(unresolved, fmt.Sprintf("%s（%v）", s.ProposalID, err))
			s.Error = fmt.Sprintf("rollback failed: %v", err)
			continue
		}
		rolled++
		s.Status = CutoverStepRolled
		_ = saveCutoverLocked(c)
	}
	if len(unresolved) > 0 {
		c.Status = CutoverFailed
		c.HoldNote = fmt.Sprintf("回退失败（已回滚 %d/%d 个变更步）——未回滚：%s。人工接管（备份在变更里）",
			rolled, candidates, strings.Join(unresolved, "；"))
		now := time.Now()
		c.EndedAt = &now
		_ = saveCutoverLocked(c)
		cutoverMu.Unlock()
		// failed 终态也补前后对比报告（逐行精读 R2 P3-10 附注）。finishReport
		// 锁内合并，不改已落盘的 failed 状态。
		m.cutoverFinishReport(context.Background(), c)
		return c, nil
	}
	c.Status = CutoverAborted
	c.HoldNote = fmt.Sprintf("已按决策点回退（%d/%d 个变更步已回滚）", rolled, candidates)
	// S-13: the success path lands EndedAt HERE — cutoverFinishReport only
	// sets it when fresh.Status is still Running, and we just saved Aborted,
	// so without this the report has no 结束 line and EndedAt stays nil.
	now := time.Now()
	c.EndedAt = &now
	_ = saveCutoverLocked(c)
	// cutoverFinishReport takes cutoverMu itself — call it AFTER releasing, or
	// this deadlocks (found by TestCutoverRollbackAtDecisionPoint).
	cutoverMu.Unlock()
	m.cutoverFinishReport(context.Background(), c)
	return c, nil
}

// CutoverSkipStep presses 跳过 at a gate-failure hold (P1-E1①): the gate
// failed but the operator accepts it — the step is marked skipped with the
// reason, the cursor advances, and the run continues. The reason is audited;
// a proposal step skipped here keeps its change on the device (the proposal
// already executed), and a later decision-point 回退 still unwinds it.
func (m *Manager) CutoverSkipStep(id, reason string) (*CutoverRun, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, fmt.Errorf("cutover %s: skip requires a reason (audited)", id)
	}
	cutoverMu.Lock()
	defer cutoverMu.Unlock()
	c, err := GetCutover(id)
	if err != nil {
		return nil, err
	}
	if c.Status != CutoverHold {
		return nil, fmt.Errorf("cutover %s: status %s — only held cutovers skip a step", id, c.Status)
	}
	if c.Cursor >= len(c.Steps) {
		return nil, fmt.Errorf("cutover %s: cursor %d out of range", id, c.Cursor)
	}
	step := &c.Steps[c.Cursor]
	// 步状态前置（逐行精读 R2 P2-5）：只有门失败的步可跳过——决策点/倒计时
	// hold 的 Cursor 指向未执行的 pending 步，不设防会把"跳过"用在从未执行
	// 的步上。
	switch step.Status {
	case CutoverStepFailed, CutoverStepGating:
	default:
		return nil, fmt.Errorf("cutover %s: step %q is %s — only a failed/gating step can be skipped", id, step.Label, step.Status)
	}
	prev := step.Status
	step.Status = CutoverStepSkipped
	if step.Error == "" {
		step.Error = "skipped: " + reason
	}
	StateEventSnap(StateEventCutoverGo, id, StateActorUser, filepath.Join(cutoversDir(), id+".json"))
	c.Status = CutoverRunning
	c.HoldNote = ""
	c.Cursor++
	if err := saveCutoverLocked(c); err != nil {
		return nil, err
	}
	// Neither AppendAudit nor cutoverLaunch takes cutoverMu — hold it through.
	_ = AppendAudit(Audit{Device: "(cutover)", Command: "skip " + id + " @" + step.Label, Class: "cutover", Status: AuditOK, Error: "step was " + prev + "; reason: " + reason})
	m.cutoverLaunch(id)
	return c, nil
}

// CutoverAbort stops everything for good (running or held).
func (m *Manager) CutoverAbort(id string) (*CutoverRun, error) {
	cutoverMu.Lock()
	c, err := GetCutover(id)
	if err != nil {
		cutoverMu.Unlock()
		return nil, err
	}
	// interrupted（P0-5 后端重启遗留态）同样可放弃——值班对孤儿 run 的
	// 唯一合法出口不应只有"先继续再终止"。
	// precheck-failed 也接受 abort（逐行精读 R2 P2-4）：红灯后值班决定今晚
	// 不割了，必须有放弃出口，否则 run 永久挂在大屏或被逼着按放行。
	if c.Status != CutoverRunning && c.Status != CutoverHold && c.Status != CutoverInterrupted && c.Status != CutoverPrecheckFailed {
		cutoverMu.Unlock()
		return nil, fmt.Errorf("cutover %s: status %s", id, c.Status)
	}
	StateEventSnap(StateEventCutoverAbort, id, StateActorUser, filepath.Join(cutoversDir(), id+".json"))
	cutoverRunsMu.Lock()
	if handle, ok := cutoverRuns[id]; ok {
		handle.cancel()
	}
	cutoverRunsMu.Unlock()
	c.Status = CutoverAborted
	c.HoldNote = "人工终止"
	for i := range c.Steps {
		if c.Steps[i].Status == CutoverStepPending {
			c.Steps[i].Status = CutoverStepSkipped
		}
	}
	now := time.Now()
	c.EndedAt = &now
	if err := saveCutoverLocked(c); err != nil {
		cutoverMu.Unlock()
		return nil, err
	}
	cutoverMu.Unlock()
	_ = AppendAudit(Audit{Device: "(cutover)", Command: "abort " + id, Class: "cutover", Status: AuditFailure})
	return c, nil
}

// ── the runner ───────────────────────────────────────────────────────────────

func (m *Manager) cutoverRunner(ctx context.Context, id string, myGen int64) {
	defer func() {
		cutoverRunsMu.Lock()
		// 只撤自己的 handle——被 Continue 重启后注册表里已是新 runner 的
		// （旧实现无条件 delete 会把 Abort 的取消句柄弄丢）。
		if cur, ok := cutoverRuns[id]; ok && cur.gen == myGen {
			delete(cutoverRuns, id)
		}
		cutoverRunsMu.Unlock()
	}()
	for {
		c, err := GetCutover(id)
		if err != nil || c.Status != CutoverRunning {
			return
		}

		// 总倒计时：窗口耗尽即 hold——深夜割接最不该发生的是「计划外继续」。
		if time.Now().After(c.Deadline) {
			_ = m.cutoverHold(id, "总倒计时耗尽——剩余步骤未执行，等待决策")
			return
		}
		if c.Cursor >= len(c.Steps) {
			m.cutoverFinish(id)
			return
		}
		step := c.Steps[c.Cursor]
		startCursor := c.Cursor

		// Execute the step.
		state, gateErr := m.cutoverExecStep(ctx, c, step)

		// 步后折叠（NETDEV-9 同族 TOCTOU 的急停补丁，FDE_AIINFRA gap §4.0）：
		// 重读→复核→折叠→保存整段持 cutoverMu。此前「锁外重读、随后保存旧
		// 快照」的窗口里，急停的 cutoverHold（running→hold）会被迟到保存整体
		// 写回 running 且 Cursor 已前进——红钮对这条割接完全失效。复核发现
		// 状态已被外部翻转（急停 Hold 等）时，仍要把已执行的步骤状态落盘
		// （不推进 Cursor、不覆盖状态机）——否则提案 partial 的去向丢失，
		// 之后「继续」必败、「回退」够不着这份变更。
		cutoverMu.Lock()
		// 代数失效检查：急停 hold 窗口内值班按了「继续」→ launch 代数已换，
		// 新 runner 接管游标。旧 runner 的 ctx 取消会产生假 gateErr——若在这里
		// 折叠，会把 "context canceled" 写成验证门失败并再次 hold（值班明明按
		// 了继续），且与新 runner 交错。直接退场，一个字节都不写。
		cutoverRunsMu.Lock()
		cur, live := cutoverRuns[id]
		stale := !live || cur.gen != myGen
		cutoverRunsMu.Unlock()
		if stale {
			cutoverMu.Unlock()
			return
		}
		c, err = GetCutover(id)
		if err != nil {
			cutoverMu.Unlock()
			return
		}
		// 步身份复核：急停 hold 窗口内值班可能已 SkipStep（hold→running、
		// Cursor 前进、新 runner 已 launch）。旧 runner 的 state 属于已被
		// 接管的游标——既不能折叠进新槽位，也不能继续循环（会双 runner），
		// 直接退场。
		if c.Cursor != startCursor {
			cutoverMu.Unlock()
			return
		}
		if c.Cursor < len(c.Steps) {
			c.Steps[c.Cursor] = state
		}
		if c.Status != CutoverRunning {
			_ = saveCutoverLocked(c)
			_ = AppendAudit(Audit{Device: "(cutover)", Command: "step folded under external hold " + id + " @" + step.Label + " (" + state.Status + ")", Class: "cutover", Status: AuditOK})
			cutoverMu.Unlock()
			return
		}

		if gateErr != nil {
			// 门不过即停在回退决策点（§7.2）：回退按钮 + 影响描述并列。
			// P1-E1②：提案步的变更此时已落盘（提案早已执行完），旧文案只说
			// “验证门未过”会让值班误以为执行失败而不敢跳过/继续——明确三分：
			// 变更状态 / 验证结果 / 可选动作。
			impact := step.Impact
			if impact == "" {
				impact = "步骤 " + step.Label + " 验证未通过"
			}
			landed := ""
			// NEW-29: a gate-failed proposal step has status Failed, but the
			// proposal itself was already EXECUTED — the change IS on the
			// device. The old Approved/Done-only check never fired here, so
			// the operator saw "执行失败" with no landed-change warning.
			if step.ProposalID != "" && (state.Status == CutoverStepApproved || state.Status == CutoverStepDone || state.Status == CutoverStepFailed) {
				landed = fmt.Sprintf("（变更已落盘：提案 %s 已执行，回退清单包含本步骤）", step.ProposalID)
			}
			c.HoldNote = "验证门未过：" + gateErr.Error() + landed + " — " + impact + "。可选：跳过本步继续（需填原因）/ 回退 / 终止"
			c.Status = CutoverHold
			_ = saveCutoverLocked(c)
			cutoverMu.Unlock()
			_ = AppendAudit(Audit{Device: "(cutover)", Command: "hold " + id + " @" + step.Label, Class: "cutover", Status: AuditFailure, Error: gateErr.Error()})
			// 深链召回（§4.12）：半夜窗口期的"回来决策"——IM 推送带
			// fairpeer://cutover/<id>，点开直达割接大屏（无出口配置时静默）。
			NotifyPushText("cutover", "[fairpeer 运维] 割接验证门未过："+c.Name,
				"步骤 "+step.Label+" 验证未过，已停在回退决策点。\n"+c.HoldNote+"\nfairpeer://cutover/"+c.ID)
			return
		}

		c.Cursor++
		if step.DecisionPoint {
			impact := step.Impact
			if impact == "" {
				impact = "决策点 " + step.Label
			}
			c.HoldNote = "决策点：" + impact + " — 继续 or 回退，决策是人按的"
			c.Status = CutoverHold
			_ = saveCutoverLocked(c)
			cutoverMu.Unlock()
			NotifyPushText("cutover", "[fairpeer 运维] 割接到达决策点："+c.Name,
				c.HoldNote+"\nfairpeer://cutover/"+c.ID)
			_ = AppendAudit(Audit{Device: "(cutover)", Command: "hold " + id + " @" + step.Label + " (decision)", Class: "cutover", Status: AuditOK})
			return
		}
		if err := saveCutoverLocked(c); err != nil {
			cutoverMu.Unlock()
			return
		}
		cutoverMu.Unlock()
	}
}

// cutoverExecStep runs one step (change or read) plus its gate.
func (m *Manager) cutoverExecStep(ctx context.Context, c *CutoverRun, step CutoverStep) (CutoverStep, error) {
	now := time.Now()
	step.Status = CutoverStepRunning
	step.StartedAt = &now
	step.Error = ""

	if step.ProposalID != "" {
		// 已执行完的提案（watching/done——急停后「继续」重入的典型态）：
		// 只重验门，不重发变更。重发会被 ExecuteProposal 的 approved-only 闸
		// 拒绝并打成"执行失败"，值班在门失败 hold 上按「继续」就进了必败
		// 循环（批次 B2 / 逐行精读 R2 P2-6）。partial/failed 仍走原路径失败
		// 关闭（变更去向交人裁决）。
		if p, perr := GetProposal(step.ProposalID); perr == nil &&
			(p.Status == ProposalWatching || p.Status == ProposalDone) {
			step.Status = CutoverStepApproved
		} else {
			p, err := m.ExecuteProposal(CtxStateActor(ctx, StateActorSystem), step.ProposalID)
			if err != nil {
				step.Status = CutoverStepFailed
				step.Error = err.Error()
				end := time.Now()
				step.EndedAt = &end
				return step, fmt.Errorf("变更 %s 执行失败: %v", step.ProposalID, err)
			}
			if p.Status == ProposalPartial || p.Status == ProposalFailed {
				step.Status = CutoverStepFailed
				step.Error = "变更 " + step.ProposalID + " 冻结为 " + p.Status + "（首败冻结）"
				end := time.Now()
				step.EndedAt = &end
				return step, fmt.Errorf("%s", step.Error)
			}
			step.Status = CutoverStepApproved
		}
	} else {
		res := m.Exec(ctx, step.Device, step.Command)
		step.Output = tailStr(res.Output, 2048)
		if res.Refused {
			step.Status = CutoverStepFailed
			step.Error = res.Refusal
			end := time.Now()
			step.EndedAt = &end
			return step, fmt.Errorf("命令被拒：%s", res.Refusal)
		}
		if res.IsError {
			step.Status = CutoverStepFailed
			step.Error = "device error: " + firstLine(res.Output)
			end := time.Now()
			step.EndedAt = &end
			return step, fmt.Errorf("%s", step.Error)
		}
		step.Status = CutoverStepDone
	}

	if step.Gate != nil {
		step.Status = CutoverStepGating
		if err := m.gateWait(ctx, step.Gate); err != nil {
			step.Status = CutoverStepFailed
			step.Error = "gate: " + err.Error()
			end := time.Now()
			step.EndedAt = &end
			return step, err
		}
		if step.Status == CutoverStepApproved {
			// keep proposal semantics
		} else {
			step.Status = CutoverStepDone
		}
	}
	end := time.Now()
	step.EndedAt = &end
	return step, nil
}

// gateWait polls the gate command until Expect matches CONTINUOUSLY for
// SustainSec. Timeout = TimeoutSec, default 2×sustain + 90s.
func (m *Manager) gateWait(ctx context.Context, g *CutoverGate) error {
	re, rerr := regexp.Compile(g.Expect)
	if rerr != nil {
		// 磁盘文件可能绕过 CutoverStart 校验（手改/旧版本）——构造错误的
		// gate 报失败而不是 panic runner（逐行精读 R2 P3-11）。
		return fmt.Errorf("gate expect: %v", rerr)
	}
	sustain := time.Duration(g.SustainSec) * time.Second
	timeout := time.Duration(g.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = sustain*2 + 90*time.Second
	}
	if timeout < sustain {
		// SustainSec > TimeoutSec 在数学上永不可能通过——夹到可持续窗口。
		timeout = sustain + 90*time.Second
	}
	interval := sustain / 4
	if interval < time.Second {
		interval = time.Second
	}
	deadline := time.Now().Add(timeout)
	var matchedSince time.Time
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		res := m.Exec(ctx, g.Device, g.Command)
		ok := !res.Refused && !res.IsError && re.MatchString(res.Output)
		if ok {
			if matchedSince.IsZero() {
				matchedSince = time.Now()
			}
			if time.Since(matchedSince) >= sustain {
				return nil
			}
		} else {
			matchedSince = time.Time{} // continuity broken — restart the window
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s %q 在 %v 内未持续 %v 匹配 %q", g.Device, g.Command, timeout, sustain, g.Expect)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// cutoverHold flips a running run to hold. err 非 nil = 翻转没有发生（run 不
// 存在或已不在 running 态）——急停路径据此把失败记进 EstopReport.Errors，
// 而不是把 hold 成功虚报给值班。
func (m *Manager) cutoverHold(id, note string) error {
	cutoverMu.Lock()
	defer cutoverMu.Unlock()
	c, err := GetCutover(id)
	if err != nil {
		return err
	}
	if c.Status != CutoverRunning {
		return fmt.Errorf("cutover %s: status %s — only running cutovers hold", id, c.Status)
	}
	StateEventSnap(StateEventCutoverHold, id, StateActorSystem, filepath.Join(cutoversDir(), id+".json"))
	c.Status = CutoverHold
	c.HoldNote = note
	if err := saveCutoverLocked(c); err != nil {
		return err
	}
	_ = AppendAudit(Audit{Device: "(cutover)", Command: "hold " + id, Class: "cutover", Status: AuditFailure, Error: note})
	return nil
}

// cutoverFinish completes the run: post-snapshot + before/after report.
func (m *Manager) cutoverFinish(id string) {
	c, err := GetCutover(id)
	if err != nil || c.Status != CutoverRunning {
		return
	}
	m.cutoverFinishReport(context.Background(), c)
}

func (m *Manager) cutoverFinishReport(ctx context.Context, c *CutoverRun) {
	// PostSnapshot 是逐设备网络 I/O（可达分钟级）。此前整段无锁：期间 Abort/
	// 急停 Hold 落盘的新状态，会被最后这份几分钟前读出的陈旧 c 整文件回写
	// 掉——abort 变 done、HoldNote"人工终止"丢失（逐行精读 R2 P1-1）。
	// 现改为：I/O 完成后锁内重读磁盘最新副本，只合并报告产物；终态仅在
	// 仍是 running 时才落。
	c.PostSnapshot = map[string]string{}
	for name := range c.PreSnapshot {
		vers, err := m.RunBackup(ctx, name)
		if err != nil || len(vers) == 0 {
			continue
		}
		c.PostSnapshot[name] = vers[len(vers)-1].ID
	}
	c.Report = m.cutoverReport(c)
	now := time.Now()
	cutoverMu.Lock()
	fresh, err := GetCutover(c.ID)
	if err != nil {
		cutoverMu.Unlock()
		return
	}
	fresh.PostSnapshot = c.PostSnapshot
	fresh.Report = c.Report
	ended := false
	if fresh.Status == CutoverRunning {
		fresh.Status = CutoverDone
		// 前缀匹配：回退路径的 HoldNote 带「已回滚 N/M」进度后缀。
		if strings.HasPrefix(fresh.HoldNote, "已按决策点回退") {
			fresh.Status = CutoverAborted
		}
		fresh.EndedAt = &now
		ended = true
	}
	StateEventSnap(StateEventCutoverDone, c.ID, StateActorSystem, filepath.Join(cutoversDir(), c.ID+".json"))
	_ = saveCutoverLocked(fresh)
	c.Status = fresh.Status
	c.EndedAt = fresh.EndedAt
	cutoverMu.Unlock()
	_ = AppendAudit(Audit{Device: "(cutover)", Command: "end " + c.ID + " " + c.Status, Class: "cutover", Status: AuditOK})
	_ = ended
}

// cutoverReport builds the before/after comparison (§7.2: 哪些接口/路由/流量变了).
func (m *Manager) cutoverReport(c *CutoverRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 割接对比报告 %s — %s\n\n", c.ID, c.Name)
	started := "(unknown)"
	if c.StartedAt != nil {
		started = c.StartedAt.Format("15:04:05")
	}
	fmt.Fprintf(&b, "- 开始：%s\n", started)
	if c.EndedAt != nil {
		fmt.Fprintf(&b, "- 结束：%s（%s）\n", c.EndedAt.Format("15:04:05"), c.Status)
	}
	fmt.Fprintf(&b, "- 步骤：%d 项\n\n", len(c.Steps))
	for _, s := range c.Steps {
		mark := map[string]string{
			CutoverStepApproved: "✅", CutoverStepDone: "✅", CutoverStepFailed: "❌",
			CutoverStepRolled: "↩️", CutoverStepSkipped: "⏭", CutoverStepPending: "⬜",
			CutoverStepRunning: "…", CutoverStepGating: "…",
		}[s.Status]
		fmt.Fprintf(&b, "## %s %s（%s）\n", mark, s.Label, s.Status)
		if s.Error != "" {
			fmt.Fprintf(&b, "- 错误：%s\n", s.Error)
		}
	}
	if len(c.PreSnapshot) == 0 {
		b.WriteString("\n（无基线快照——涉及设备不提供配置备份）\n")
		return b.String()
	}
	b.WriteString("\n## 前后配置对比\n")
	for name, preID := range c.PreSnapshot {
		postID, ok := c.PostSnapshot[name]
		if !ok {
			fmt.Fprintf(&b, "\n### %s\n（割接后快照缺失）\n", name)
			continue
		}
		diff, err := DiffBackups(name, preID, postID)
		if err != nil {
			fmt.Fprintf(&b, "\n### %s\n（对比失败：%v）\n", name, err)
			continue
		}
		if strings.TrimSpace(diff) == "" {
			fmt.Fprintf(&b, "\n### %s\n无变化\n", name)
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n```diff\n%s\n```\n", name, diff)
	}
	return b.String()
}
