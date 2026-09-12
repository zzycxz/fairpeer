package netdev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/fileutil"
	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

// Proposal pipeline (NETDEV_SPEC §6.2–6.4): the ONLY write path. The agent
// may DRAFT (netdev_propose — a read-only act); a human APPROVES via the
// desktop settings/proposal UI (bridge methods, never a tool); execution
// rolls device-by-device (backup → apply → verify) and freezes on the first
// failure for human decision (Appendix B-2: no auto-rollback; the rollback
// plan is authored WITH the change and pressed by a human).

// Proposal statuses.
const (
	ProposalDraft     = "draft"
	ProposalApproved  = "approved"
	ProposalExecuting = "executing"
	ProposalDone      = "done"
	ProposalPartial   = "partial"  // some steps applied, later ones skipped — frozen for a human
	ProposalFailed    = "failed"   // rollback attempted and failed — alert
	ProposalRejected  = "rejected" // human vetoed (completion-spec §4.1): agent sees the reason next turn
)

// Structured step types (§7.1): the proposal step grew from a CLI command
// string into a discriminated union. Type "" behaves as cli everywhere — old
// proposals deserialize unchanged.
const (
	StepCLI           = "cli"
	StepK8sApply      = "k8s-apply"
	StepSQLMigration  = "sql-migration"
	StepFileUpload    = "file-upload"
	StepCertReplace   = "cert-replace"
	StepRestoreVerify = "restore-verify" // §7.3 备份恢复演练
)

// ProposalStep is one device's slice of a change.
type ProposalStep struct {
	Device   string   `json:"device"`
	Type     string   `json:"type,omitempty"`     // "" | cli | k8s-apply | sql-migration | file-upload | cert-replace
	Commands []string `json:"commands"`           // cli: the change, in order
	Rollback []string `json:"rollback,omitempty"` // cli: reverse commands, authored with the change

	// Structured payloads (§7.1) — only the fields of the step's type matter.
	YAML          string `json:"yaml,omitempty"`           // k8s-apply: full manifest
	UpSQL         string `json:"up_sql,omitempty"`         // sql-migration
	DownSQL       string `json:"down_sql,omitempty"`       // sql-migration — REQUIRED (missing ⇒ not submittable)
	LocalPath     string `json:"local_path,omitempty"`     // file-upload / cert-replace: local cert file
	RemotePath    string `json:"remote_path,omitempty"`    // file-upload / cert-replace: absolute target path
	KeyLocalPath  string `json:"key_local_path,omitempty"` // cert-replace: local private key file
	KeyRemotePath string `json:"key_remote_path,omitempty"`
	Checksum      string `json:"checksum,omitempty"`   // optional sha256 the uploaded bytes must match
	ReloadCmd     string `json:"reload_cmd,omitempty"` // cert-replace: service reload after the swap
	// restore-verify（§7.3 备份恢复演练）: restore a config snapshot to a
	// STAGING target and run the verify read. Device = receiver.
	RestoreDevice  string `json:"restore_device,omitempty"`  // snapshot source device
	RestoreVersion string `json:"restore_version,omitempty"` // snapshot id; "" = latest at execute time
	VerifyCmd      string `json:"verify_cmd,omitempty"`      // e.g. `nginx -t`

	Backup  string `json:"backup,omitempty"` // captured pre-change state (redacted)
	Applied bool   `json:"applied"`
	// AppliedCmds is how much of the step actually landed: cli counts applied
	// commands (a mid-step failure keeps the executed prefix — those commands
	// are on the device even though Applied stays false), unitary types count
	// 1 when the whole step applied. Rollback runs for any step with
	// AppliedCmds > 0, not just fully-applied ones (missing field = 0 keeps
	// old proposals' behavior: Applied alone decides).
	AppliedCmds int    `json:"applied_cmds,omitempty"`
	Error       string `json:"error,omitempty"`
	Dangerous   bool   `json:"dangerous,omitempty"` // destructive verb scan ⇒ forces confirm2 (§7.1)
}

// stepType normalizes the discriminator.
func stepType(s *ProposalStep) string {
	if s.Type == "" {
		return StepCLI
	}
	return s.Type
}

// Proposal is one change proposal.
const (
	ProposalWatching = "watching"
	ProposalClosed   = "closed"
)

type Proposal struct {
	ID     string         `json:"id"`
	Intent string         `json:"intent"`
	Status string         `json:"status"`
	Steps  []ProposalStep `json:"steps"`
	// 恢复提案来源（备份→恢复闭环）：从备份版本起草的恢复提案记录来源
	// 版本 ID（device@nanos）；审计与前端据此回放「恢复自哪一版」。
	RestoreFrom string    `json:"restore_from,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ApprovedAt  time.Time `json:"approved_at,omitempty"`
	ExecutedAt  time.Time `json:"executed_at,omitempty"`
	Approver    string    `json:"approver,omitempty"`
	Confirm2    bool      `json:"confirm2"`       // secondary confirmation for proposal+confirm2 groups
	Note        string    `json:"note,omitempty"` // freeze/rollback reason trail
	// 驳回（completion-spec §4.1）：draft/approved 可被人否决；Reason 随变更
	// 持久化，agent 下一轮读到变更即见被拒原因。
	RejectedAt   time.Time `json:"rejected_at,omitempty"`
	RejectReason string    `json:"reject_reason,omitempty"`
	// RequestID links the proposal to its unified ops request
	// (OPS_AUTOMATION_PLATFORM_SPEC Phase 1)：起草时校验编号在台账——
	// ops_status 把变更产出归到请求轨迹下。
	RequestID string `json:"request_id,omitempty"`
	// 观察期（§7.1）：done → watching（默认 30 分钟）→ closed；劣化触发 Finding。
	WatchUntil *time.Time `json:"watch_until,omitempty"`
	WatchNote  string     `json:"watch_note,omitempty"`
	// HealthBase is the ifDown count per target device at watch start（-1 =
	// 当时不可达）——观察期劣化检测（健康轮询对比）的基线。仅 SNMP 配置
	// 设备有信号；其余设备跳过对比。
	HealthBase map[string]int `json:"health_base,omitempty"`
}

// proposalsDirOverride isolates proposal storage in tests.
var proposalsDirOverride string

// ProposalsDir stores one JSON per proposal under the netdev state dir.
func ProposalsDir() string {
	if proposalsDirOverride != "" {
		return proposalsDirOverride
	}
	return filepath.Join(netdevStateDir(), "proposals")
}

var (
	proposalMu sync.Mutex
	// proposalSeq is the in-memory same-day counter; proposalSeqDay is the
	// day it was last seeded from disk.
	proposalSeq    int
	proposalSeqDay string
	// proposalInflight marks ids with a long-running transition (execute,
	// rollback) so a concurrent delete can't yank the file mid-flight.
	proposalInflight = map[string]struct{}{}
)

// SaveProposal persists p (create or update).
func SaveProposal(p *Proposal) error {
	if p.ID == "" {
		p.ID = newProposalID()
	}
	if p.Status == "" {
		p.Status = ProposalDraft
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	if err := os.MkdirAll(ProposalsDir(), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	proposalMu.Lock()
	defer proposalMu.Unlock()
	return fileutil.AtomicWriteFile(filepath.Join(ProposalsDir(), p.ID+".json"), b, 0o600)
}

// saveProposalLocked is SaveProposal with the caller holding proposalMu — the
// write half of an atomic load→check→set→save transition.
func saveProposalLocked(p *Proposal) error {
	if err := os.MkdirAll(ProposalsDir(), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(filepath.Join(ProposalsDir(), p.ID+".json"), b, 0o600)
}

// claimProposalInflight reserves id for a long-running transition. false
// means one is already running.
func claimProposalInflight(id string) bool {
	proposalMu.Lock()
	defer proposalMu.Unlock()
	if _, busy := proposalInflight[id]; busy {
		return false
	}
	proposalInflight[id] = struct{}{}
	return true
}

func releaseProposalInflight(id string) {
	proposalMu.Lock()
	delete(proposalInflight, id)
	proposalMu.Unlock()
}

// GetProposal loads one proposal.
func GetProposal(id string) (*Proposal, error) {
	if !validStoreID(id) {
		return nil, fmt.Errorf("proposal %s: invalid id", id)
	}
	b, err := os.ReadFile(filepath.Join(ProposalsDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var p Proposal
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ListProposals returns proposals newest-first. It doubles as the crash
// recovery point for executions orphaned by a dead runner: an executing
// proposal whose file has gone untouched for staleExecutingAge transitions to
// partial here, because executing blocks every lifecycle op and used to stick
// forever after a mid-run crash.
func ListProposals() ([]*Proposal, error) {
	entries, err := os.ReadDir(ProposalsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	recoverStaleExecuting(entries)
	closeExpiredWatching(entries)
	var out []*Proposal
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if p, err := GetProposal(strings.TrimSuffix(e.Name(), ".json")); err == nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// closeExpiredWatching lazily closes watching proposals whose watch window
// has passed (NETDEV-1): the auto-close is a data deadline (WatchUntil), not
// an in-memory timer — a restart during the window used to strand the
// proposal in watching forever (delete refused, health polling spinning).
// Same lazy-sweep slot as recoverStaleExecuting.
func closeExpiredWatching(entries []os.DirEntry) {
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		p, err := GetProposal(id)
		if err != nil || p.Status != ProposalWatching || p.WatchUntil == nil || now.Before(*p.WatchUntil) {
			continue
		}
		proposalMu.Lock()
		// Re-read under the lock and re-check: the auto-close goroutine (or
		// another sweep) may have closed it already.
		if pp, err := GetProposal(id); err == nil && pp.Status == ProposalWatching && pp.WatchUntil != nil && now.After(*pp.WatchUntil) {
			StateEventSnap(StateEventCloseWatch, id, StateActorSystem, filepath.Join(ProposalsDir(), id+".json"))
			pp.Status = ProposalClosed
			pp.WatchNote = "观察期满，自动关闭（重启后惰性扫描补关）"
			_ = saveProposalLocked(pp)
		}
		proposalMu.Unlock()
	}
}

// staleExecutingAge is how long an executing proposal may sit untouched before
// the lazy sweep concludes its runner died: a live execution re-saves after
// every step, so the file's mtime is the runner's heartbeat.
const staleExecutingAge = 30 * time.Minute

// recoverStaleExecuting transitions executing proposals whose file has not
// been written for staleExecutingAge to partial ("runner died — verify
// devices manually"). Ids with an in-flight transition are skipped — that is
// a LIVE run, however slow. Called lazily from ListProposals, so there is no
// init wiring; only entries whose mtime already crossed the threshold are
// examined further. The repair is audited but writes no state-history event —
// rewinding it back to executing would just re-brick the proposal until the
// next sweep caught it again.
func recoverStaleExecuting(entries []os.DirEntry) {
	cutoff := time.Now().Add(-staleExecutingAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		if !validStoreID(id) {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.ModTime().Before(cutoff) {
			continue
		}
		recoverStaleExecutingOne(id)
	}
}

func recoverStaleExecutingOne(id string) {
	proposalMu.Lock()
	defer proposalMu.Unlock()
	if _, busy := proposalInflight[id]; busy {
		return // an execute/rollback transition is genuinely running
	}
	p, err := GetProposal(id)
	if err != nil || p.Status != ProposalExecuting {
		return
	}
	p.Status = ProposalPartial
	p.Note = strings.TrimSpace(p.Note + "\nrunner died — verify devices manually (execution went quiet for " +
		staleExecutingAge.String() + "; applied steps carry their backups)")
	if err := saveProposalLocked(p); err != nil {
		return
	}
	_ = AppendAudit(Audit{Device: "(proposal)", Command: "recover-stale " + id, Class: "proposal",
		Status: AuditDeviceError, Error: "executing proposal untouched for " + staleExecutingAge.String() + " — marked partial; verify devices manually"})
}

// newProposalID assigns P<YYYYMMDD>-N. The sequence is in-memory, so a
// same-day restart used to regenerate P<date>-1… and SaveProposal silently
// overwrote the existing file. On the first issue of a day the sequence is
// re-seeded from the highest suffix on disk, so ids continue past whatever
// the earlier process already created (midnight crossings re-seed too —
// today's files include everything the process itself wrote).
func newProposalID() string {
	proposalMu.Lock()
	defer proposalMu.Unlock()
	day := time.Now().Format("20060102")
	if proposalSeq == 0 || proposalSeqDay != day {
		proposalSeq = maxProposalSeqForDay(day)
		proposalSeqDay = day
	}
	proposalSeq++
	return fmt.Sprintf("P%s-%d", day, proposalSeq)
}

// maxProposalSeqForDay scans the proposals dir for the highest -N suffix of
// the given day (0 when none exist yet).
func maxProposalSeqForDay(day string) int {
	entries, err := os.ReadDir(ProposalsDir())
	if err != nil {
		return 0
	}
	prefix := "P" + day + "-"
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

// ── policy gate ─────────────────────────────────────────────────────────────

// ValidateProposal checks a draft against the inventory policies: every step's
// device must exist, no step may target a read-only group, and each structured
// type's own contract must hold (§7.1 — e.g. sql-migration without a down
// script is not submittable). Group policy `proposal+confirm2` is legal but
// flags ApproveProposal for the secondary confirmation.
func (m *Manager) ValidateProposal(p *Proposal) error {
	if strings.TrimSpace(p.Intent) == "" {
		return errors.New("proposal: intent is required (what & why)")
	}
	if len(p.Steps) == 0 {
		return errors.New("proposal: no steps")
	}
	for i := range p.Steps {
		s := &p.Steps[i]
		d, ok := m.cfg.NetDevDeviceByName(s.Device)
		if !ok && stepType(s) != StepSQLMigration {
			return fmt.Errorf("proposal: step device %q not in inventory", s.Device)
		}
		if ok && d.Group != "" {
			if g, ok := m.cfg.NetDevGroupByName(d.Group); ok && g.Policy == config.NetDevPolicyReadOnly {
				return fmt.Errorf("proposal: device %q is in read-only group %q — proposals are not allowed", s.Device, d.Group)
			}
		}
		if err := m.validateStep(s, d); err != nil {
			return err
		}
	}
	return nil
}

// validateStep enforces one step's type contract (§7.1 表：载荷与回滚依据).
func (m *Manager) validateStep(s *ProposalStep, d config.NetDevDevice) error {
	s.Dangerous = dangerScan(s)
	switch stepType(s) {
	case StepCLI:
		if len(s.Commands) == 0 {
			return fmt.Errorf("proposal: step for %q has no commands", s.Device)
		}
		if len(s.Rollback) == 0 {
			return fmt.Errorf("proposal: step for %q has no rollback plan (authored with the change)", s.Device)
		}
	case StepK8sApply:
		if d.Kind != "k8s" {
			return fmt.Errorf("proposal: k8s-apply step for %q requires a kind=k8s target", s.Device)
		}
		if strings.TrimSpace(s.YAML) == "" {
			return fmt.Errorf("proposal: k8s-apply step for %q has no manifest", s.Device)
		}
		if _, err := kubeResourcePath(s.YAML, "default"); err != nil {
			return fmt.Errorf("proposal: k8s-apply step for %q: %v", s.Device, err)
		}
	case StepSQLMigration:
		src, ok := m.dbSourceByName(s.Device)
		if !ok {
			return fmt.Errorf("proposal: sql-migration step target %q is not a [[netdev.db_sources]] entry", s.Device)
		}
		switch src.Type {
		case "mysql", "postgres", "mssql":
		default:
			return fmt.Errorf("proposal: sql-migration on %q: engine %q not supported (v1: mysql/postgres/mssql)", s.Device, src.Type)
		}
		if strings.TrimSpace(s.UpSQL) == "" {
			return fmt.Errorf("proposal: sql-migration step for %q has no up script", s.Device)
		}
		if strings.TrimSpace(s.DownSQL) == "" {
			return fmt.Errorf("proposal: sql-migration step for %q has no down script — the type is not submittable without one (§7.1)", s.Device)
		}
	case StepFileUpload, StepCertReplace:
		if d.Vendor != "linux" {
			return fmt.Errorf("proposal: %s step for %q requires a linux SSH target (v1: exec-channel upload)", stepType(s), s.Device)
		}
		if err := validateUploadPaths(s.Device, s.LocalPath, s.RemotePath); err != nil {
			return err
		}
		if stepType(s) == StepCertReplace {
			if err := validateUploadPaths(s.Device, s.KeyLocalPath, s.KeyRemotePath); err != nil {
				return fmt.Errorf("proposal: cert-replace step for %q key pair: %v", s.Device, err)
			}
			if strings.TrimSpace(s.ReloadCmd) == "" {
				return fmt.Errorf("proposal: cert-replace step for %q has no reload command (rollback must be able to restore + reload)", s.Device)
			}
		}
	case StepRestoreVerify:
		if err := m.validateRestoreVerify(s, d); err != nil {
			return err
		}
	default:
		return fmt.Errorf("proposal: step for %q has unknown type %q", s.Device, s.Type)
	}
	return nil
}

// ProposalNeedsConfirm2 reports whether any step demands the secondary
// confirmation: a proposal+confirm2 group (§6.3) OR a step whose verbs scanned
// as destructive (§7.1 — delete/scale-down 类动词落 dangerous + confirm2).
func (m *Manager) ProposalNeedsConfirm2(p *Proposal) bool {
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.Dangerous || dangerScan(s) {
			return true
		}
		if d, ok := m.cfg.NetDevDeviceByName(s.Device); ok && d.Group != "" {
			if g, ok := m.cfg.NetDevGroupByName(d.Group); ok && g.Policy == config.NetDevPolicyProposalConf {
				return true
			}
		}
	}
	return false
}

// changeWindow matches "tue,thu 22:00-24:00" (local time). Empty = always.
type changeWindow struct {
	days       map[string]bool // 3-letter lowercase weekdays
	start, end int             // minutes since midnight
}

var windowRe = regexp.MustCompile(`^\s*([a-z]{3}(?:\s*,\s*[a-z]{3})*)\s+(\d{1,2}):(\d{2})-(\d{1,2}):(\d{2})\s*$`)
var weekdayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

func parseChangeWindow(s string) (*changeWindow, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return nil, nil
	}
	m := windowRe.FindStringSubmatch(s)
	if m == nil {
		return nil, fmt.Errorf("change_window %q: want \"tue,thu 22:00-24:00\"", s)
	}
	w := &changeWindow{days: map[string]bool{}}
	for _, d := range strings.Split(m[1], ",") {
		d = strings.TrimSpace(d)
		valid := false
		for _, n := range weekdayNames {
			if d == n {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("change_window: unknown day %q", d)
		}
		w.days[d] = true
	}
	w.start = atoi(m[2])*60 + atoi(m[3])
	w.end = atoi(m[4])*60 + atoi(m[5])
	if w.end <= w.start {
		return nil, fmt.Errorf("change_window %q: end must be after start", s)
	}
	return w, nil
}

func (w *changeWindow) contains(t time.Time) bool {
	if w == nil {
		return true
	}
	day := weekdayNames[int(t.Weekday())]
	if !w.days[day] {
		return false
	}
	mins := t.Hour()*60 + t.Minute()
	return mins >= w.start && mins <= w.end
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// ApproveProposal is the human gate. It enforces: current status draft, group
// policies (confirm2 when demanded), and the change window of every involved
// group. The agent has NO path here — approval arrives only from the desktop
// bridge with the human's click. The load→check→set→save runs as one critical
// section so a racing reject/execute can't interleave (last-write-wins used
// to silently drop a transition).
func (m *Manager) ApproveProposal(id string, confirm2 bool) (*Proposal, error) {
	proposalMu.Lock()
	defer proposalMu.Unlock()
	p, err := GetProposal(id)
	if err != nil {
		return nil, err
	}
	if p.Status != ProposalDraft {
		return nil, fmt.Errorf("proposal %s: status %s, only drafts can be approved", id, p.Status)
	}
	// Re-validate at the approval gate. Draft validation ran when the proposal
	// was authored, but the inventory may have moved since (device re-vendored
	// to windows, group policy changed, step payloads edited). The structured
	// step executors run unix-only commands (base64 -d, sha256sum, sh -c,
	// rm -f) — a file/cert step that slipped onto a windows target would fail
	// there, and its rollback would fail identically, leaving the device
	// changed. This is the last gate before the write path becomes reachable.
	if err := m.ValidateProposal(p); err != nil {
		return nil, fmt.Errorf("proposal %s: re-validation at approval failed: %w", id, err)
	}
	if m.ProposalNeedsConfirm2(p) && !confirm2 {
		return nil, fmt.Errorf("proposal %s demands secondary confirmation (group policy proposal+confirm2)", id)
	}
	// Change window: every involved group's window must contain now.
	for _, s := range p.Steps {
		d, ok := m.cfg.NetDevDeviceByName(s.Device)
		if !ok {
			return nil, fmt.Errorf("proposal %s: device %q vanished from inventory", id, s.Device)
		}
		if d.Group == "" {
			continue
		}
		g, ok := m.cfg.NetDevGroupByName(d.Group)
		if !ok {
			continue
		}
		w, err := parseChangeWindow(g.ChangeWindow)
		if err != nil {
			return nil, fmt.Errorf("proposal %s: %v", id, err)
		}
		if !w.contains(time.Now()) {
			return nil, fmt.Errorf("proposal %s: group %q change window (%s) is closed — approval blocked", id, d.Group, g.ChangeWindow)
		}
	}
	StateEventSnap(StateEventApprove, id, StateActorUser, filepath.Join(ProposalsDir(), id+".json"))
	p.Status = ProposalApproved
	p.ApprovedAt = time.Now()
	p.Approver = "local-user"
	p.Confirm2 = confirm2
	if err := saveProposalLocked(p); err != nil {
		return nil, err
	}
	_ = AppendAudit(Audit{Device: "(proposal)", Command: "approve " + id, Class: "proposal", Status: AuditOK})
	return p, nil
}

// RejectProposal is the human veto (completion-spec §4.1): the write path's
// gate could only say yes — this is the "no". draft/approved may be rejected
// (approved-but-not-executed vetoes cost nothing; executing states are past
// the point of veto and must ride rollback instead). The reason is persisted
// on the proposal so the agent's next turn sees WHY it was turned down.
func (m *Manager) RejectProposal(id, reason string) (*Proposal, error) {
	proposalMu.Lock()
	defer proposalMu.Unlock()
	p, err := GetProposal(id)
	if err != nil {
		return nil, err
	}
	if p.Status != ProposalDraft && p.Status != ProposalApproved {
		return nil, fmt.Errorf("proposal %s: status %s, only draft/approved proposals can be rejected (executing ones ride rollback)", id, p.Status)
	}
	StateEventSnap(StateEventReject, id, StateActorUser, filepath.Join(ProposalsDir(), id+".json"))
	p.Status = ProposalRejected
	p.RejectedAt = time.Now()
	p.RejectReason = strings.TrimSpace(reason)
	if p.RejectReason == "" {
		p.RejectReason = "(no reason given)"
	}
	// Note is the human-readable trail the existing UI already renders.
	p.Note = "驳回：" + p.RejectReason
	if err := saveProposalLocked(p); err != nil {
		return nil, err
	}
	_ = AppendAudit(Audit{Device: "(proposal)", Command: "reject " + id, Class: "proposal", Status: AuditFailure, Error: p.RejectReason})
	return p, nil
}

// DeleteProposal removes a proposal file. Only draft and terminal states
// (rejected/done/failed/closed) may be deleted — the live pipeline
// (approved/executing/partial/watching) must stay auditable. partial keeps
// its record because the applied steps are still on the devices.
func (m *Manager) DeleteProposal(id string) error {
	proposalMu.Lock()
	defer proposalMu.Unlock()
	if _, busy := proposalInflight[id]; busy {
		return fmt.Errorf("proposal %s: a transition is running (execute/rollback) — wait for it to finish before deleting", id)
	}
	p, err := GetProposal(id)
	if err != nil {
		return err
	}
	switch p.Status {
	case ProposalDraft, ProposalRejected, ProposalDone, ProposalFailed, ProposalClosed:
	default:
		return fmt.Errorf("proposal %s: status %s is in the live pipeline — reject or close it before deleting", id, p.Status)
	}
	StateEventSnap(StateEventDelete, id, StateActorUser, filepath.Join(ProposalsDir(), id+".json"))
	if err := os.Remove(filepath.Join(ProposalsDir(), id+".json")); err != nil {
		return err
	}
	_ = AppendAudit(Audit{Device: "(proposal)", Command: "delete " + id, Class: "proposal", Status: AuditOK})
	return nil
}

// backupCommand returns the driver's running-config dump command.
func backupCommand(drvKey string) string {
	switch drvKey {
	case "huawei-vrp":
		return "display current-configuration"
	case "cisco-ios":
		return "show running-config"
	default:
		return ""
	}
}

// ExecuteProposal rolls the approved change device-by-device: backup → apply
// → mark. The FIRST failure freezes the proposal as partial (later steps
// untouched) — a human then decides rollback (which runs the authored plan
// over the already-applied steps) or keep. There is no automatic rollback.
// A fully-applied run enters the observation period as watching (auto-closes
// after 30 minutes).
func (m *Manager) ExecuteProposal(ctx context.Context, id string) (*Proposal, error) {
	// estop 代数必须在任何状态变更/确认往返之前捕获：捕获晚于"已标记
	// executing"的话，estop 落进这个窗口（或落在人工在线确认的往返里——
	// 可达分钟级）会被这场执行漏掉，所有步骤照常写设备（逐行精读 R1 问题1）。
	// estop 之后新发起的 execute 属于人工决定，不在冻结范围。
	estopBefore := EstopGeneration()
	p, err := GetProposal(id)
	if err != nil {
		return nil, err
	}
	if p.Status != ProposalApproved {
		return nil, fmt.Errorf("proposal %s: status %s, only approved proposals execute", id, p.Status)
	}
	StateEventSnap(StateEventExecute, id, stateActorFromCtx(ctx), filepath.Join(ProposalsDir(), id+".json"))

	// 执行前在线检查（§7.1）：发现其他在线人员则**暂停并列出会话**，人确
	// 认后才继续——「我要变更，但同事正登着」是最常见的协作事故。确认语
	// 义 = 再次点击执行：本次会话清单已记入 Note，下一次执行看到同样的清
	// 单即视为已确认（会话有变化则重新要求确认）。已确认清单用带"|已确认"
	// 的独立标记精确比对——子串 Contains 会把 "alice, bob" 已确认误判为
	// "alice" 也已确认（逐行精读 R1 问题8）。
	online := m.preExecOnlineCheck(ctx, p)
	if online != "" && !strings.Contains(p.Note, "[在线人员|已确认] "+online) {
		if strings.Contains(p.Note, "[在线人员] "+online) {
			// 同一清单的再次执行 = 已确认：落确认标记后直接放行（精确比对
			// 基线，会话集变化则标记失配、重新要求确认——逐行精读 R1 问题8
			// 的子串误判修复）。
			p.Note = strings.TrimSpace(p.Note + "\n[在线人员|已确认] " + online)
			_ = SaveProposal(p)
		} else {
			// 首次发现：记入清单并拦下，人确认（再次点击执行）后放行。
			p.Note = strings.TrimSpace(p.Note + "\n[在线人员] " + online)
			_ = SaveProposal(p)
			return p, fmt.Errorf("执行前确认：目标设备上有其他在线人员（%s）——确认没人正操作后再次点击「执行」（清单已记入变更备注；会话有变化会再次要求确认）", online)
		}
	}

	// Claim approved→executing atomically: a racing approve/reject used to
	// interleave with this write (last-write-wins dropped a transition), and
	// the in-flight mark keeps delete away for the run's duration.
	proposalMu.Lock()
	p, err = GetProposal(id) // reload: the online check may have persisted a Note
	if err != nil {
		proposalMu.Unlock()
		return nil, err
	}
	if p.Status != ProposalApproved {
		proposalMu.Unlock()
		return nil, fmt.Errorf("proposal %s: status %s, only approved proposals execute", id, p.Status)
	}
	if _, busy := proposalInflight[id]; busy {
		proposalMu.Unlock()
		return nil, fmt.Errorf("proposal %s: a transition is already running", id)
	}
	proposalInflight[id] = struct{}{}
	p.Status = ProposalExecuting
	p.ExecutedAt = time.Now()
	if err := saveProposalLocked(p); err != nil {
		delete(proposalInflight, id)
		proposalMu.Unlock()
		return nil, err
	}
	proposalMu.Unlock()
	defer releaseProposalInflight(id)

	frozen := ""
	for i := range p.Steps {
		s := &p.Steps[i]

		// 紧急停止（estop.go §4.0）：在步骤边界冻结——已落盘前缀保持原状，
		// 走既有 partial 语义（人决定回滚或保留），后续步骤不执行。
		if proposalEstopFrozen(estopBefore) {
			frozen = "紧急停止：剩余步骤已在步骤边界冻结（本步未开始、未落盘）——回滚（或确认保留）后可重新起草、批准并执行"
			break
		}

		// Structured types carry their own backup/apply/verify semantics
		// (§7.1); cli keeps the classic driver path.
		switch stepType(s) {
		case StepSQLMigration:
			if err := m.execSQLMigration(ctx, s); err != nil {
				s.Error = err.Error()
				frozen = s.Error
			} else {
				s.Applied = true
				s.AppliedCmds = 1
			}
		case StepK8sApply, StepFileUpload, StepCertReplace, StepRestoreVerify:
			d, ok := m.cfg.NetDevDeviceByName(s.Device)
			if !ok {
				s.Error = "device vanished from inventory"
				frozen = s.Error
				break
			}
			var err error
			switch stepType(s) {
			case StepK8sApply:
				err = m.execK8sApply(ctx, s)
			case StepFileUpload:
				err = m.execFileUpload(ctx, d, s)
			case StepCertReplace:
				err = m.execCertReplace(ctx, d, s)
			case StepRestoreVerify:
				err = m.execRestoreVerify(ctx, d, s)
			}
			if err != nil {
				s.Error = err.Error()
				frozen = s.Error
			} else {
				s.Applied = true
				s.AppliedCmds = 1
			}
		default: // cli
			d, ok := m.cfg.NetDevDeviceByName(s.Device)
			if !ok {
				s.Error = "device vanished from inventory"
				frozen = s.Error
				break
			}
			drv, ok := m.driverFor(d)
			if !ok {
				s.Error = "no driver"
				frozen = s.Error
				break
			}

			// Backup (redacted; stored with the proposal for human recovery).
			if bc := backupCommand(drv.Key()); bc != "" {
				if res, err := m.runUnclassified(ctx, d, drv, bc); err == nil && !res.IsError {
					s.Backup = Redact(res.Output)
					// WRITE_AUTHZ §7.1 事件快照：提案执行的备份统一落 vault——
					// 三明治/定时/提案三条历史线汇进同一个版本库。
					_, _ = saveBackup(d.Name, res.Output)
				} else {
					s.Error = "backup failed: " + firstLine(errText(err, res))
					frozen = s.Error
					break
				}
			}

			// Apply (the only write path in netdev; audited per command). The
			// applied-command count survives a mid-step failure: commands
			// before the failing one already changed the device, and the
			// rollback decision below needs to see that prefix.
			for ci, cmd := range s.Commands {
				res, err := m.runUnclassified(ctx, d, drv, cmd)
				_ = AppendAudit(Audit{
					Device: s.Device, Via: d.Via, Command: cmd, Class: "proposal-write",
					Status: auditStatus(res, err), OutputBytes: len(res.Output),
					Error: errText(err, res),
				})
				if err != nil || res.IsError {
					s.Error = fmt.Sprintf("command %q failed after %d/%d applied: %s", cmd, ci, len(s.Commands), firstLine(errText(err, res)))
					frozen = s.Error
					break
				}
				s.AppliedCmds = ci + 1
			}
			if frozen == "" {
				s.Applied = true
				s.AppliedCmds = len(s.Commands)
			}
		}
		if frozen != "" {
			break
		}
		if err := SaveProposal(p); err != nil {
			return nil, err
		}
	}

	if frozen != "" {
		p.Status = ProposalPartial
		// Note 是轨迹字段（在线确认清单、历史冻结原因都在里面）——追加，
		// 不整行覆盖（逐行精读 R1 问题4）。
		p.Note = strings.TrimSpace(p.Note + "\nfrozen: " + frozen + " — later steps untouched; a human decides rollback or keep")
	} else {
		// All steps applied: done work lands in the observation period
		// (§7.1). Status goes straight to watching — "done" used to be the
		// terminal stop and NOTHING ever flipped it to watching, leaving the
		// whole pipeline (auto-close goroutine, CloseProposalWatch,
		// checkWatchingProposals) dead code. watching → closed after the
		// default 30 minutes; degradation inside the window raises a Finding
		// and points at rollback.
		p.Status = ProposalWatching
		// 观察期（§7.1）：默认 30 分钟后自动 closed；观察期内的劣化检测由
		// 健康轮询承担（checkWatchingProposals——与 watch 起点基线对比，
		// 劣化即最高级 Finding + 回滚提示，回滚仍需人批准）。
		wu := time.Now().Add(30 * time.Minute)
		p.WatchUntil = &wu
		p.HealthBase = watchHealthBase(p)
		go func(id string, until time.Time) {
			time.Sleep(time.Until(until))
			proposalMu.Lock()
			defer proposalMu.Unlock()
			if pp, err := GetProposal(id); err == nil && pp.Status == ProposalWatching {
				StateEventSnap(StateEventCloseWatch, id, StateActorSystem, filepath.Join(ProposalsDir(), id+".json"))
				pp.Status = ProposalClosed
				pp.WatchNote = "观察期满，自动关闭"
				_ = saveProposalLocked(pp)
			}
		}(p.ID, wu)
		// P2-3：变更完成即自动复跑关联设备的基线核查——修复有没有生效由
		// 数据说话（命中更新同一告警、不再命中自动 resolve），不等人想起
		// 手动重查。失败不影响变更结果（best-effort，入审计）。
		touched := map[string]bool{}
		for _, s := range p.Steps {
			if s.Device != "" {
				touched[s.Device] = true
			}
		}
		var devs []string
		for d := range touched {
			devs = append(devs, d)
		}
		if len(devs) > 0 {
			p.WatchNote = "已触发自动基线复核（结果见发现）"
			go func(id string, dd []string) {
				// Test seam: the recheck goroutine must not outlive a test's
				// state-dir override into the NEXT test's fixtures (it raced
				// TestWatchDegradationRaisesFinding's finding counts ~1/4 runs).
				if !proposalAutoRecheck {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				if _, err := m.RunBaselineFor(ctx, dd); err != nil {
					_ = AppendAudit(Audit{Time: time.Now(), Device: "(proposal)", Command: "baseline recheck after " + id, Class: "proposal", Status: AuditDeviceError, Error: err.Error()})
					return
				}
				_ = AppendAudit(Audit{Time: time.Now(), Device: "(proposal)", Command: "baseline recheck after " + id, Class: "proposal", Status: AuditOK})
			}(p.ID, devs)
		}
	}
	if err := SaveProposal(p); err != nil {
		return nil, err
	}
	_ = AppendAudit(Audit{Device: "(proposal)", Command: "execute " + id, Class: "proposal", Status: auditStatusFor(p.Status)})
	return p, nil
}

// RollbackProposal runs the authored rollback plan over the APPLIED steps
// only, oldest-last so state unwinds in reverse. A step whose commands failed
// MID-STEP still rolls back (AppliedCmds > 0): the commands before the failure
// are on the device, and the authored plan is the recovery contract for the
// whole step. Partial/done/watching proposals only (watching is the
// degradation flow's exit). A rollback failure marks the proposal failed
// (alert) and stops.
func (m *Manager) RollbackProposal(ctx context.Context, id string) (*Proposal, error) {
	// The rollback loop runs device I/O for minutes — claim the proposal so a
	// concurrent rollback or delete can't interleave (double-rollback would run
	// the reverse commands twice).
	proposalMu.Lock()
	if _, busy := proposalInflight[id]; busy {
		proposalMu.Unlock()
		return nil, fmt.Errorf("proposal %s: a transition is already running", id)
	}
	proposalInflight[id] = struct{}{}
	proposalMu.Unlock()
	defer releaseProposalInflight(id)

	p, err := GetProposal(id)
	if err != nil {
		return nil, err
	}
	// failed 纳入（批次 B3）：上次回退失败的产物——其已落地前缀仍可按
	// 步级 Applied/AppliedCmds 闸回滚；零 applied 的 failed 会被步级闸全跳，
	// 等价"无物可回滚"。
	if p.Status != ProposalPartial && p.Status != ProposalDone && p.Status != ProposalWatching && p.Status != ProposalFailed {
		return nil, fmt.Errorf("proposal %s: status %s — only partial/done/watching/failed proposals roll back", id, p.Status)
	}
	StateEventSnap(StateEventRollback, id, stateActorFromCtx(ctx), filepath.Join(ProposalsDir(), id+".json"))
	for i := len(p.Steps) - 1; i >= 0; i-- {
		s := &p.Steps[i]
		if !s.Applied && s.AppliedCmds == 0 {
			continue // nothing of this step ever reached the device
		}
		var rerr error
		switch stepType(s) {
		case StepSQLMigration:
			rerr = m.execSQLRollback(ctx, s)
		case StepK8sApply:
			rerr = m.execK8sRestore(ctx, s)
		case StepFileUpload:
			if d, ok := m.cfg.NetDevDeviceByName(s.Device); ok {
				rerr = m.execFileRestore(ctx, d, s.RemotePath, s.Backup)
			}
		case StepCertReplace:
			if d, ok := m.cfg.NetDevDeviceByName(s.Device); ok {
				rerr = m.execCertRestore(ctx, d, s)
			}
		case StepRestoreVerify:
			if d, ok := m.cfg.NetDevDeviceByName(s.Device); ok {
				rerr = m.execFileRestore(ctx, d, s.RemotePath, s.Backup)
			}
		default: // cli
			d, ok := m.cfg.NetDevDeviceByName(s.Device)
			if !ok {
				continue
			}
			drv, ok := m.driverFor(d)
			if !ok {
				continue
			}
			// NETDEV-10：mid-step 失败的步骤只回滚已落地的前缀——跑全表会让
			// 第 k+1 条反演命令操作不存在的配置，设备报错中断回滚，更早的
			// 未遍历步骤从此不再回滚（现场与状态不一致）。
			roll := s.Rollback
			if !s.Applied && s.AppliedCmds > 0 && s.AppliedCmds < len(s.Rollback) {
				roll = s.Rollback[:s.AppliedCmds]
			}
			for _, cmd := range roll {
				res, err := m.runUnclassified(ctx, d, drv, cmd)
				_ = AppendAudit(Audit{
					Device: s.Device, Via: d.Via, Command: cmd, Class: "proposal-rollback",
					Status: auditStatus(res, err), Error: errText(err, res),
				})
				if err != nil || res.IsError {
					rerr = fmt.Errorf("%q: %s", cmd, firstLine(errText(err, res)))
					break
				}
			}
		}
		if rerr != nil {
			p.Status = ProposalFailed
			p.Note = "rollback FAILED on " + s.Device + ": " + firstLine(rerr.Error()) + " — manual recovery required; backups are stored in the proposal"
			if err := SaveProposal(p); err != nil {
				return nil, err
			}
			return p, nil
		}
		s.Applied = false
		s.AppliedCmds = 0
	}
	p.Status = ProposalDraft // rolled back cleanly; re-approval required to try again
	// Note 是轨迹字段：追加（与冻结路径同款——不抹在线确认/历史冻结痕迹）。
	p.Note = strings.TrimSpace(p.Note + "\nrolled back via the authored plan")
	if err := SaveProposal(p); err != nil {
		return nil, err
	}
	return p, nil
}

// runUnclassified runs ANY command on the device's session, bypassing the
// read-only classifier. This is the executor's private write path — the ONLY
// caller group outside Run's seal, and every command through it is audited by
// the callers above. Nothing reachable from the agent calls this.
func (m *Manager) runUnclassified(ctx context.Context, d config.NetDevDevice, drv driver.Driver, cmd string) (Result, error) {
	res, err := m.runRead(ctx, d, drv, cmd)
	if err != nil {
		return Result{}, err
	}
	return Result{Command: cmd, Output: res.Output, IsError: res.IsError}, nil
}

func auditStatus(res Result, err error) string {
	switch {
	case err != nil:
		return AuditFailure
	case res.IsError:
		return AuditDeviceError
	default:
		return AuditOK
	}
}

func auditStatusFor(status string) string {
	switch status {
	case ProposalDone, ProposalWatching:
		return AuditOK
	}
	return AuditFailure
}

func errText(err error, res Result) string {
	if err != nil {
		return err.Error()
	}
	if res.IsError {
		return firstLine(res.Output)
	}
	return ""
}

// preExecOnlineCheck runs who/quser on every unique target device — the
// "someone else is on the box" guard (§7.1 执行前置检查). Returns a summary
// line, empty when nobody else is on any target.
func (m *Manager) preExecOnlineCheck(ctx context.Context, p *Proposal) string {
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.Device == "" || seen[s.Device] {
			continue
		}
		seen[s.Device] = true
	}
	var findings []string
	for name := range seen {
		d, ok := m.cfg.NetDevDeviceByName(name)
		if !ok {
			continue
		}
		var cmd string
		switch d.Vendor {
		case "linux", "vmware":
			cmd = "who"
		case "windows":
			cmd = "quser"
		default:
			continue // network CLIs have no meaningful "who"; VTY occupancy is live panel's
		}
		res := m.Exec(ctx, name, cmd)
		if res.Refused || res.IsError {
			continue
		}
		if n := countUserLines(res.Output); n > 0 {
			findings = append(findings, fmt.Sprintf("%s: %d 个会话", name, n))
		}
	}
	return strings.Join(findings, "；")
}

// countUserLines counts the session rows in who/quser output. quser prints a
// header ("USERNAME SESSIONNAME …", localized "用户名 …" on zh-CN) plus the
// occasional blank line — neither is a session; `who` has no header, so the
// filter is a no-op there.
func countUserLines(out string) int {
	n := 0
	for _, ln := range strings.Split(out, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "USERNAME") || strings.HasPrefix(t, "用户名") {
			continue
		}
		n++
	}
	return n
}

// CloseProposalWatch manually ends the watching period.
func CloseProposalWatch(id string) error {
	proposalMu.Lock()
	defer proposalMu.Unlock()
	p, err := GetProposal(id)
	if err != nil {
		return err
	}
	if p.Status != ProposalWatching {
		return fmt.Errorf("proposal %s: status %s, only watching proposals close", id, p.Status)
	}
	StateEventSnap(StateEventCloseWatch, id, StateActorUser, filepath.Join(ProposalsDir(), id+".json"))
	p.Status = ProposalClosed
	p.WatchNote = "人工关闭"
	return saveProposalLocked(p)
}

// ── 观察期劣化检测（§7.1）─────────────────────────────────────────────────

// watchHealthBase snapshots each step device's ifDown count from the LAST
// health poll (-1 = unreachable). Devices without SNMP health have no signal
// and are simply absent — the comparison skips them.
func watchHealthBase(p *Proposal) map[string]int {
	seen := map[string]bool{}
	for _, s := range p.Steps {
		if s.Device != "" {
			seen[s.Device] = true
		}
	}
	base := map[string]int{}
	healthMu.Lock()
	for name := range seen {
		h, ok := healthState[name]
		if !ok {
			continue
		}
		if !h.Reachable {
			base[name] = -1
		} else {
			base[name] = h.IfDown()
		}
	}
	healthMu.Unlock()
	if len(base) == 0 {
		return nil
	}
	return base
}

// watchDegraded reports the devices whose health degraded vs the watch base:
// was reachable → now down, or ifDown grew.
func watchDegraded(p *Proposal, fresh map[string]DeviceHealth) []string {
	var worse []string
	for dev, base := range p.HealthBase {
		h, ok := fresh[dev]
		if !ok {
			continue // no fresh signal
		}
		// GPU-only 主机采集失败的轮次：GPUSampled=false ⇒ Reachable=false，
		// "我方看不了" ≠ "设备掉了"（与告警引擎同一冻结哲学，逐行精读 R2 P2-2）。
		if h.GPUOnly && !h.GPUSampled {
			continue
		}
		if base == -1 {
			// watch 起点就失联的设备：观察期内"恢复"不是劣化——旧逻辑会把
			// 恢复算成 "down 口 -1→0" 立 critical 卡（逐行精读 R2 P2-2）。
			continue
		}
		if !h.Reachable {
			worse = append(worse, dev+"（失联）")
			continue
		}
		if h.IfDown() > base {
			worse = append(worse, fmt.Sprintf("%s（down 口 %d→%d）", dev, base, h.IfDown()))
		}
	}
	sort.Strings(worse)
	return worse
}

// checkWatchingProposals compares every watching proposal's targets against
// the fresh health sweep; the FIRST degradation raises a top-severity Finding
// with the rollback hint (回滚仍需人批准——AI 的手永远慢一步) and marks the
// WatchNote so the alert fires once per proposal.
func (m *Manager) checkWatchingProposals(fresh map[string]DeviceHealth) {
	props, err := ListProposals()
	if err != nil {
		return
	}
	for _, p := range props {
		if p.Status != ProposalWatching || len(p.HealthBase) == 0 {
			continue
		}
		if strings.Contains(p.WatchNote, "劣化") {
			continue // already alerted
		}
		worse := watchDegraded(p, fresh)
		if len(worse) == 0 {
			continue
		}
		// Re-check-and-write under the lock: a concurrent watch-close or
		// auto-close used to interleave with this save.
		proposalMu.Lock()
		cur, err := GetProposal(p.ID)
		if err != nil || cur.Status != ProposalWatching || strings.Contains(cur.WatchNote, "劣化") {
			proposalMu.Unlock()
			continue
		}
		cur.WatchNote = "观察期劣化：" + strings.Join(worse, "；") + " — 可回滚（变更页签「回滚」，仍需人按）"
		_ = saveProposalLocked(cur)
		proposalMu.Unlock()
		_ = SaveFinding(&Finding{
			Title:      "变更观察期劣化：" + p.Intent,
			Severity:   SeverityCritical,
			Devices:    p.stepDevices(),
			Detail:     fmt.Sprintf("变更 %s 执行后观察期内健康劣化：%s。基线（watch 起点）若与变更相关，回滚是第一优先动作。", p.ID, strings.Join(worse, "；")),
			Evidence:   []Evidence{{Device: "(watch)", Command: "proposal " + p.ID, Output: p.WatchNote}},
			Suggestion: "在「变更」页签对 " + p.ID + " 按「回滚」执行已起草的回滚计划（回滚仍需人批准）",
			Source:     "watch:" + p.ID,
			Status:     FindingActive,
		})
		_ = AppendAudit(Audit{Device: "(proposal)", Command: "watch-degraded " + p.ID, Class: "proposal", Status: AuditFailure, Error: strings.Join(worse, "；")})
	}
}

// stepDevices lists the proposal's unique step target devices.
func (p *Proposal) stepDevices() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range p.Steps {
		if s.Device != "" && !seen[s.Device] {
			seen[s.Device] = true
			out = append(out, s.Device)
		}
	}
	return out
}

// proposalAutoRecheck gates the post-done baseline recheck goroutine. It is
// the seam that keeps the goroutine inside its own test: helpers flip it off
// so a finished proposal's async recheck never writes into a LATER test's
// overridden findings dir (the ~1/4 flake in TestWatchDegradationRaisesFinding).
var proposalAutoRecheck = true
