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
	ProposalPartial   = "partial" // some steps applied, later ones skipped — frozen for a human
	ProposalFailed    = "failed"  // rollback attempted and failed — alert
)

// ProposalStep is one device's slice of a change.
type ProposalStep struct {
	Device   string   `json:"device"`
	Commands []string `json:"commands"`           // the change, in order
	Rollback []string `json:"rollback,omitempty"` // reverse commands, authored with the change
	Backup   string   `json:"backup,omitempty"`   // captured pre-change config (redacted)
	Applied  bool     `json:"applied"`
	Error    string   `json:"error,omitempty"`
}

// Proposal is one change proposal.
type Proposal struct {
	ID         string         `json:"id"`
	Intent     string         `json:"intent"`
	Status     string         `json:"status"`
	Steps      []ProposalStep `json:"steps"`
	CreatedAt  time.Time      `json:"created_at"`
	ApprovedAt time.Time      `json:"approved_at,omitempty"`
	ExecutedAt time.Time      `json:"executed_at,omitempty"`
	Approver   string         `json:"approver,omitempty"`
	Confirm2   bool           `json:"confirm2"`       // secondary confirmation for proposal+confirm2 groups
	Note       string         `json:"note,omitempty"` // freeze/rollback reason trail
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
	proposalMu  sync.Mutex
	proposalSeq int
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
	return os.WriteFile(filepath.Join(ProposalsDir(), p.ID+".json"), b, 0o600)
}

// GetProposal loads one proposal.
func GetProposal(id string) (*Proposal, error) {
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

// ListProposals returns proposals newest-first.
func ListProposals() ([]*Proposal, error) {
	entries, err := os.ReadDir(ProposalsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
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

func newProposalID() string {
	proposalMu.Lock()
	defer proposalMu.Unlock()
	proposalSeq++
	return fmt.Sprintf("P%s-%d", time.Now().Format("20060102"), proposalSeq)
}

// ── policy gate ─────────────────────────────────────────────────────────────

// ValidateProposal checks a draft against the inventory policies: every step's
// device must exist, and no step may target a read-only group. Group policy
// `proposal+confirm2` is legal but flags ApproveProposal for the secondary
// confirmation.
func (m *Manager) ValidateProposal(p *Proposal) error {
	if strings.TrimSpace(p.Intent) == "" {
		return errors.New("proposal: intent is required (what & why)")
	}
	if len(p.Steps) == 0 {
		return errors.New("proposal: no steps")
	}
	for _, s := range p.Steps {
		d, ok := m.cfg.NetDevDeviceByName(s.Device)
		if !ok {
			return fmt.Errorf("proposal: step device %q not in inventory", s.Device)
		}
		if d.Group != "" {
			if g, ok := m.cfg.NetDevGroupByName(d.Group); ok && g.Policy == config.NetDevPolicyReadOnly {
				return fmt.Errorf("proposal: device %q is in read-only group %q — proposals are not allowed", s.Device, d.Group)
			}
		}
		if len(s.Commands) == 0 {
			return fmt.Errorf("proposal: step for %q has no commands", s.Device)
		}
		if len(s.Rollback) == 0 {
			return fmt.Errorf("proposal: step for %q has no rollback plan (authored with the change)", s.Device)
		}
	}
	return nil
}

// ProposalNeedsConfirm2 reports whether any step's group demands the secondary
// confirmation.
func (m *Manager) ProposalNeedsConfirm2(p *Proposal) bool {
	for _, s := range p.Steps {
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
// bridge with the human's click.
func (m *Manager) ApproveProposal(id string, confirm2 bool) (*Proposal, error) {
	p, err := GetProposal(id)
	if err != nil {
		return nil, err
	}
	if p.Status != ProposalDraft {
		return nil, fmt.Errorf("proposal %s: status %s, only drafts can be approved", id, p.Status)
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
	p.Status = ProposalApproved
	p.ApprovedAt = time.Now()
	p.Approver = "local-user"
	p.Confirm2 = confirm2
	if err := SaveProposal(p); err != nil {
		return nil, err
	}
	_ = AppendAudit(Audit{Device: "(proposal)", Command: "approve " + id, Class: "proposal", Status: AuditOK})
	return p, nil
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
func (m *Manager) ExecuteProposal(ctx context.Context, id string) (*Proposal, error) {
	p, err := GetProposal(id)
	if err != nil {
		return nil, err
	}
	if p.Status != ProposalApproved {
		return nil, fmt.Errorf("proposal %s: status %s, only approved proposals execute", id, p.Status)
	}
	p.Status = ProposalExecuting
	p.ExecutedAt = time.Now()
	if err := SaveProposal(p); err != nil {
		return nil, err
	}

	frozen := ""
	for i := range p.Steps {
		s := &p.Steps[i]
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
			} else {
				s.Error = "backup failed: " + firstLine(errText(err, res))
				frozen = s.Error
				break
			}
		}

		// Apply (the only write path in netdev; audited per command).
		failed := false
		for _, cmd := range s.Commands {
			res, err := m.runUnclassified(ctx, d, drv, cmd)
			_ = AppendAudit(Audit{
				Device: s.Device, Via: d.Via, Command: cmd, Class: "proposal-write",
				Status: auditStatus(res, err), OutputBytes: len(res.Output),
				Error: errText(err, res),
			})
			if err != nil || res.IsError {
				s.Error = fmt.Sprintf("command %q failed: %s", cmd, firstLine(errText(err, res)))
				failed = true
				break
			}
		}
		if failed {
			frozen = s.Error
			break
		}
		s.Applied = true
		if err := SaveProposal(p); err != nil {
			return nil, err
		}
	}

	if frozen != "" {
		p.Status = ProposalPartial
		p.Note = "frozen: " + frozen + " — later steps untouched; a human decides rollback or keep"
	} else {
		p.Status = ProposalDone
	}
	if err := SaveProposal(p); err != nil {
		return nil, err
	}
	_ = AppendAudit(Audit{Device: "(proposal)", Command: "execute " + id, Class: "proposal", Status: auditStatusFor(p.Status)})
	return p, nil
}

// RollbackProposal runs the authored rollback plan over the APPLIED steps
// only, oldest-last so state unwinds in reverse. Frozen/failed proposals only.
// A rollback failure marks the proposal failed (alert) and stops.
func (m *Manager) RollbackProposal(ctx context.Context, id string) (*Proposal, error) {
	p, err := GetProposal(id)
	if err != nil {
		return nil, err
	}
	if p.Status != ProposalPartial && p.Status != ProposalDone {
		return nil, fmt.Errorf("proposal %s: status %s — only partial/done proposals roll back", id, p.Status)
	}
	for i := len(p.Steps) - 1; i >= 0; i-- {
		s := &p.Steps[i]
		if !s.Applied {
			continue
		}
		d, ok := m.cfg.NetDevDeviceByName(s.Device)
		if !ok {
			continue
		}
		drv, ok := m.driverFor(d)
		if !ok {
			continue
		}
		for _, cmd := range s.Rollback {
			res, err := m.runUnclassified(ctx, d, drv, cmd)
			_ = AppendAudit(Audit{
				Device: s.Device, Via: d.Via, Command: cmd, Class: "proposal-rollback",
				Status: auditStatus(res, err), Error: errText(err, res),
			})
			if err != nil || res.IsError {
				p.Status = ProposalFailed
				p.Note = "rollback FAILED on " + s.Device + ": " + firstLine(errText(err, res)) + " — manual recovery required; backups are stored in the proposal"
				if err := SaveProposal(p); err != nil {
					return nil, err
				}
				return p, nil
			}
		}
		s.Applied = false
	}
	p.Status = ProposalDraft // rolled back cleanly; re-approval required to try again
	p.Note = "rolled back via the authored plan"
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
	if status == ProposalDone {
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
