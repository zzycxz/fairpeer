package netdev

// writeauth.go — 设备写授权（NETDEV_WRITE_AUTHZ_SPEC v1 · P1）。
//
// 三档 enforcement 全部住 Manager 层：会话无关，headless/定时任务里
// permission 的 Ask 自动放行绕不过这里。对话模式那把锁只在 auto 档经
// execTool.ReadOnlyCall 合成（agent gate 的 writer 回退）；confirm 档的
// 审批卡走 Manager 自己的 WriteApprover 通道——完全访问跳过的是
// permission 系统，触不到这层（spec §6/§2.4）。
//
// 本文件承载：confirmed lock state（TOML 放宽拦截，§5.3）、
// EffectiveWriteTier、turn write budget、三明治管线（§6）、OpStep 操作
// 台账（§7.3）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/evidence"
	"github.com/zzycxz/fairpeer/internal/fileutil"
	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

// WriteApprover is the confirm-tier approval channel (spec §6 step ②): the
// desktop wires a dialog-backed implementation; nil (headless/CLI runs) means
// confirm-tier writes are REFUSED with an explanation — headless must not
// silently pass (red test §12.9). reason explains the refusal to the model.
type WriteApprover func(device, command string) (approved bool, reason string)

// OpStep is one row of the operation ledger (spec §7.3): every write action —
// direct write, proposal step, rollback itself — lands exactly one. Pre/Post
// point at vault versions; RollbackTo is the restore pointer (pre version).
// Turn anchors the row to the user turn that produced it — the session-level
// rollback ("回退本次全部写") filters on it.
type OpStep struct {
	ID          string `json:"id"`
	At          string `json:"at"`
	Actor       string `json:"actor"` // agent | user | scheduled | rollback
	Device      string `json:"device"`
	Command     string `json:"command"`
	Status      string `json:"status"` // ok | device-error | failure
	Turn        int    `json:"turn"`
	PreID       string `json:"pre_id,omitempty"`
	PostID      string `json:"post_id,omitempty"`
	DiffSummary string `json:"diff_summary,omitempty"`
	Error       string `json:"error,omitempty"`
	RollbackTo  string `json:"rollback_to,omitempty"`
	Link        string `json:"link,omitempty"`
}

// confirmedLock records the last tier a HUMAN confirmed through the alert
// dialog for one device — the baseline the TOML-relaxation clamp compares
// against (spec §5.3): config is an application, not an authority.
type confirmedLock struct {
	Tier string    `json:"tier"`
	At   time.Time `json:"at"`
	By   string    `json:"by"`
}

var (
	writeLocksMu      sync.Mutex
	writeLocksPathOvr string // test override
	opstepsDirOvr     string // test override
)

// SetWriteLocksPath overrides the confirmed-lock store location (tests).
func SetWriteLocksPath(p string) {
	writeLocksMu.Lock()
	defer writeLocksMu.Unlock()
	writeLocksPathOvr = p
}

// SetOpStepsDir overrides the op-step ledger location (tests).
func SetOpStepsDir(p string) {
	writeLocksMu.Lock()
	defer writeLocksMu.Unlock()
	opstepsDirOvr = p
}

func writeLocksPath() string {
	writeLocksMu.Lock()
	defer writeLocksMu.Unlock()
	if writeLocksPathOvr != "" {
		return writeLocksPathOvr
	}
	return filepath.Join(netdevStateDir(), "write-locks.json")
}

func opstepsDir() string {
	writeLocksMu.Lock()
	defer writeLocksMu.Unlock()
	if opstepsDirOvr != "" {
		return opstepsDirOvr
	}
	return filepath.Join(netdevStateDir(), "opsteps")
}

// ── confirmed lock state ─────────────────────────────────────────────────────

func (m *Manager) loadConfirmed() map[string]confirmedLock {
	m.waMu.Lock()
	defer m.waMu.Unlock()
	if m.waConfirmed != nil {
		return m.waConfirmed
	}
	m.waConfirmed = map[string]confirmedLock{}
	data, err := os.ReadFile(writeLocksPath())
	if err != nil {
		return m.waConfirmed
	}
	_ = json.Unmarshal(data, &m.waConfirmed)
	return m.waConfirmed
}

func (m *Manager) saveConfirmedLocked(locks map[string]confirmedLock) error {
	b, err := json.MarshalIndent(locks, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(writeLocksPath()), 0o700); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(writeLocksPath(), b, 0o600)
}

// ConfirmWriteTier records a human's alert-confirmation of one device's tier
// (the desktop bridge calls this AFTER the risk dialog — spec §5.2). Tightening
// needs no ceremony and is recorded the same way so the clamp baseline tracks.
func (m *Manager) ConfirmWriteTier(device, tier, by string) error {
	t, err := config.ParseNetDevWriteTier(tier)
	if err != nil {
		return fmt.Errorf("write tier: %v", err)
	}
	if strings.TrimSpace(device) == "" {
		return fmt.Errorf("write tier: device is required")
	}
	m.waMu.Lock()
	defer m.waMu.Unlock()
	if m.waConfirmed == nil {
		m.waConfirmed = map[string]confirmedLock{}
	}
	m.waConfirmed[device] = confirmedLock{Tier: string(t), At: time.Now(), By: by}
	return m.saveConfirmedLocked(m.waConfirmed)
}

// ConfirmedTier returns the last human-confirmed tier for a device
// ("" = never confirmed → the sealed baseline). Settings UI reads this to
// show what a widening still needs before it takes effect.
func (m *Manager) ConfirmedTier(device string) string {
	locks := m.loadConfirmed()
	if c, ok := locks[device]; ok {
		return c.Tier
	}
	return ""
}

// EffectiveWriteTier resolves the tier a write must obey: the configured tier
// (group chain) CLAMPED by the confirmed-lock baseline — a wider tier without
// a matching human confirmation degrades to the confirmed tier (default
// sealed), emits a warning the bridge surfaces, and audits the clamp (§5.3).
func (m *Manager) EffectiveWriteTier(d config.NetDevDevice) config.NetDevWriteTier {
	configured := m.cfg.NetDev.NetDevWriteTierFor(d)
	locks := m.loadConfirmed()
	confirmed, ok := locks[d.Name]
	if !ok {
		confirmed = confirmedLock{Tier: string(config.NetDevWriteSealed)}
	}
	ct, _ := config.ParseNetDevWriteTier(confirmed.Tier)
	if tierWider(configured, ct) {
		m.waMu.Lock()
		if m.waWarned == nil {
			m.waWarned = map[string]bool{}
		}
		if !m.waWarned[d.Name] {
			m.waWarned[d.Name] = true
			w := fmt.Sprintf("设备 %s 的写档被配置为 %s，但未经告警确认（已确认基线：%s）——已按 %s 降级生效。请在运维设置重新完成放宽确认。", d.Name, configured, ct, ct)
			m.waWarnings = append(m.waWarnings, w)
			_ = AppendAudit(Audit{Device: d.Name, Command: fmt.Sprintf("(write tier %s→%s unconfirmed, clamped)", configured, ct), Class: "write-tier", Status: AuditRefused, OutputBytes: 0})
		}
		m.waMu.Unlock()
		return ct
	}
	// 时间盒（spec §3 auto_expires）：auto 是限时授权——确认窗口 elapsed 后
	// 自动降回 confirm，临时开给的实验室权限不会被遗忘在开启状态。
	if configured == config.NetDevWriteAuto {
		if g, ok := m.cfg.NetDevGroupByName(d.Group); ok {
			if dur, err := time.ParseDuration(strings.TrimSpace(g.AutoExpires)); err == nil && dur > 0 {
				if c, has := locks[d.Name]; has && time.Since(c.At) > dur {
					m.waMu.Lock()
					if m.waWarned == nil {
						m.waWarned = map[string]bool{}
					}
					if !m.waWarned[d.Name+"/timebox"] {
						m.waWarned[d.Name+"/timebox"] = true
						w := fmt.Sprintf("设备 %s 的 auto 写档时间盒（%s）已到期——自动降级为 confirm。", d.Name, dur)
						m.waWarnings = append(m.waWarnings, w)
						_ = AppendAudit(Audit{Device: d.Name, Command: "(auto tier timebox expired, degraded to confirm)", Class: "write-tier", Status: AuditRefused, OutputBytes: 0})
					}
					m.waMu.Unlock()
					return config.NetDevWriteConfirm
				}
			}
		}
	}
	return configured
}

// WriteTierWarnings returns (and clears) the clamp warnings surfaced for the UI.
func (m *Manager) WriteTierWarnings() []string {
	m.waMu.Lock()
	defer m.waMu.Unlock()
	w := m.waWarnings
	m.waWarnings = nil
	return w
}

func tierWider(a, b config.NetDevWriteTier) bool {
	return tierRank(a) > tierRank(b)
}

func tierRank(t config.NetDevWriteTier) int {
	switch t {
	case config.NetDevWriteConfirm:
		return 1
	case config.NetDevWriteAuto:
		return 2
	default:
		return 0
	}
}

// ── confirm-tier approval channel ────────────────────────────────────────────

// SetWriteApprover installs the confirm-tier approval channel (desktop wires a
// dialog). nil restores headless semantics: confirm-tier writes refuse.
func (m *Manager) SetWriteApprover(fn WriteApprover) {
	m.waMu.Lock()
	m.waApprover = fn
	m.waMu.Unlock()
}

func (m *Manager) writeApproved(device, command string) (bool, string) {
	m.waMu.Lock()
	fn := m.waApprover
	m.waMu.Unlock()
	if fn == nil {
		return false, "confirm 档写命令需要逐条审批，但当前会话没有审批通道（headless）——请在桌面端操作，或让用户通过变更提案执行。Do not retry."
	}
	approved, reason := fn(device, command)
	if !approved {
		if strings.TrimSpace(reason) == "" {
			reason = "用户未批准这条写命令"
		}
		return false, "写命令审批被拒绝：" + reason + "。说明意图并询问用户下一步；不要换写法重试。"
	}
	return true, ""
}

// ── turn write budget ────────────────────────────────────────────────────────

func (m *Manager) writeBudgetLeft() bool {
	budget := m.cfg.NetDev.Write.TurnWriteBudgetOrDefault()
	m.waMu.Lock()
	spent := m.turnWrites
	m.waMu.Unlock()
	return spent < budget
}

func (m *Manager) writeSpend() {
	m.waMu.Lock()
	m.turnWrites++
	m.waMu.Unlock()
}

// ── sandwich pipeline (spec §6) ──────────────────────────────────────────────

// runControlledWrite executes ONE write command under the sandwich:
// pre-snapshot → execute → post-snapshot → diff → audit + OpStep ledger.
// Any step failing stops the pipeline — wherever it stops, the pre-write state
// is already in the vault (§6 失败即停语义).
func (m *Manager) runControlledWrite(ctx context.Context, d config.NetDevDevice, drv driver.Driver, command string) ExecResult {
	base := ExecResult{Device: d.Name, Command: command, Class: "write"}

	// ① pre snapshot — the restore point this write can always roll back to.
	preID := ""
	if bc := backupCommand(drv.Key()); bc != "" {
		res, err := m.runUnclassified(ctx, d, drv, bc)
		if err != nil || res.IsError {
			reason := fmt.Sprintf("写前快照失败，写命令未执行（失败即停——写前状态保护优先）：first line: %s", firstLine(errText(err, res)))
			m.audit(d, command, driver.Write, AuditFailure, 0, fmt.Errorf("%s", reason))
			m.liveCmdRefused(d.Name, command, "write", reason)
			m.appendOpStep(ctx, OpStep{At: time.Now().Format(time.RFC3339), Actor: "agent", Device: d.Name, Command: command, Status: "failure", Error: reason})
			base.Refused = true
			base.Refusal = reason
			return base
		}
		v, err := saveBackup(d.Name, res.Output)
		if err == nil {
			preID = v.ID
		}
	}

	// ② execute the write (single command, already classifier-checked).
	start := m.liveCmdStart(d.Name, command, "write")
	res, err := m.runUnclassified(ctx, d, drv, command)
	status := AuditOK
	switch {
	case err != nil:
		status = AuditFailure
	case res.IsError:
		status = AuditDeviceError
	}
	redacted, _ := RedactCounted(res.Output)
	m.audit(d, command, driver.Write, status, len(redacted), err)
	if status == AuditOK {
		m.writeSpend()
	}

	// ③ post snapshot + ④ diff (only meaningful when the write itself landed).
	postID, diffSummary := "", ""
	if status == AuditOK {
		if bc := backupCommand(drv.Key()); bc != "" {
			if pres, perr := m.runUnclassified(ctx, d, drv, bc); perr == nil && !pres.IsError {
				if v, serr := saveBackup(d.Name, pres.Output); serr == nil {
					postID = v.ID
					if preID != "" {
						if diff, derr := DiffBackups(d.Name, preID, postID); derr == nil {
							diffSummary = summarizeDiff(diff)
						}
					}
				}
			}
		}
	}
	m.liveCmdEnd(d.Name, command, "write", liveStatus(status), start, len(redacted), errText(err, res))

	// ⑤ OpStep ledger (§7.3) — the diff and rollback pointer survive the turn.
	stepStatus := "ok"
	if status == AuditDeviceError {
		stepStatus = "device-error"
	} else if status == AuditFailure {
		stepStatus = "failure"
	}
	m.appendOpStep(ctx, OpStep{
		At: time.Now().Format(time.RFC3339), Actor: "agent", Device: d.Name,
		Command: command, Status: stepStatus, PreID: preID, PostID: postID,
		DiffSummary: diffSummary, Error: errText(err, res), RollbackTo: preID,
	})

	base.Output = redacted
	base.IsError = res.IsError
	if err != nil {
		base.Refused = true
		base.Refusal = "connection/session failure: " + err.Error()
		return base
	}
	var sb strings.Builder
	sb.WriteString(redacted)
	fmt.Fprintf(&sb, "\n\n[写操作·三明治] pre 快照=%s post 快照=%s", displayID(preID), displayID(postID))
	if diffSummary != "" {
		fmt.Fprintf(&sb, "\n实际变更：\n%s", diffSummary)
	}
	sb.WriteString("\n（已落操作台账与审计；回退此步 = 恢复 pre 版本，走变更提案或设备写档通道。）")
	base.Output = sb.String()
	return base
}

func displayID(id string) string {
	if id == "" {
		return "(无)"
	}
	return id
}

func liveStatus(auditStatus string) string {
	switch auditStatus {
	case AuditOK:
		return "ok"
	case AuditDeviceError:
		return "device-error"
	default:
		return "failure"
	}
}

// summarizeDiff keeps the model-facing diff compact: changed-line count plus
// the first few +/- lines (redacted — secrets never reach context).
func summarizeDiff(diff string) string {
	var changes []string
	added, removed := 0, 0
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
			continue
		case strings.HasPrefix(l, "+"):
			added++
			if len(changes) < 6 {
				changes = append(changes, Redact(l))
			}
		case strings.HasPrefix(l, "-"):
			removed++
			if len(changes) < 6 {
				changes = append(changes, Redact(l))
			}
		}
	}
	if added == 0 && removed == 0 {
		return "(配置无文本差异——命令可能未产生持久变更)"
	}
	head := fmt.Sprintf("+%d/-%d 行", added, removed)
	if len(changes) == 0 {
		return head
	}
	return head + "\n" + strings.Join(changes, "\n")
}

// ── OpStep ledger (§7.3) ─────────────────────────────────────────────────────

func (m *Manager) appendOpStep(ctx context.Context, s OpStep) {
	// ID 复用 backup 的单调纳秒守卫（nextBackupNanos）：Windows 时钟
	// 粒度下两次连续落库会拿到同一纳秒——裸 UnixNano 曾让第二行静默
	// 覆盖第一行（TestOpStepTurnAnchor 间歇失败的根因）。
	s.ID = fmt.Sprintf("%s@%d", s.Device, nextBackupNanos())
	if s.Turn == 0 {
		m.waMu.Lock()
		s.Turn = m.turnSeq
		m.waMu.Unlock()
	}
	dir := opstepsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = fileutil.AtomicWriteFile(filepath.Join(dir, s.ID+".json"), b, 0o600)

	// Evidence bridge (NETDEV_OPSTEP_EVIDENCE_SPEC): mirror the ledger row
	// into the turn's evidence ledger as a "device:<name>" receipt so
	// complete_step can cite a config change as diff/files evidence. Only ok
	// rows carry Success+Write — failure/device-error rows stay audit-only
	// and never authorize a sign-off.
	if ledger, ok := evidence.FromContext(ctx); ok {
		ledger.Record(evidence.Receipt{
			ToolName: "netdev_opstep",
			Success:  s.Status == "ok",
			Write:    s.Status == "ok",
			Command:  s.Command,
			Paths:    []string{"device:" + s.Device},
		})
	}
}

// ListOpSteps returns a device's ledger rows newest-first ("" = every device).
func ListOpSteps(device string, limit int) []OpStep {
	dir := opstepsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	var out []OpStep
	for i := len(entries) - 1; i >= 0 && len(out) < limit; i-- {
		data, err := os.ReadFile(filepath.Join(dir, entries[i].Name()))
		if err != nil {
			continue
		}
		var s OpStep
		if json.Unmarshal(data, &s) != nil {
			continue
		}
		if device != "" && s.Device != device {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}
