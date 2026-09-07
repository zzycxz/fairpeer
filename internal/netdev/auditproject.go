package netdev

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

	"github.com/zzycxz/fairpeer/internal/fileutil"
)

// 项目上线前审计（SCENARIO_SPEC S5-1/S5-2）。项目经理按项目发起安全审计：
// 项目 = 设备清单 + 联系人 + 上线窗口；发起 = 跑审计套餐（基线/漏洞/日志
// 异常/暴露面，信封内含弱口令）；产出 = 风险清单（逐项 open|fixed|accepted），
// 复扫按 finding 签名自动转 fixed，全绿（fixed∨accepted）才放行。

// AuditProject is one project's audit definition.
type AuditProject struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Devices      []string `json:"devices"`
	Contacts     string   `json:"contacts,omitempty"`
	LaunchWindow string   `json:"launch_window,omitempty"` // "YYYY-MM-DD" 或说明
	// Checklist picks the battery stages: baseline|vuln|logs|exposure|weakcred.
	// weakcred 仅在评估信封有效时执行（信封闸不因审计绕过）。
	Checklist []string `json:"checklist"`
	CreatedAt string   `json:"created_at"`
}

// AuditItem is one risk line in a report — a Finding signature + lifecycle.
type AuditItem struct {
	FindingID string   `json:"finding_id"`
	Signature string   `json:"signature"` // device|title|source
	Title     string   `json:"title"`
	Device    string   `json:"device"`
	Severity  string   `json:"severity"`
	Fix       *FixHint `json:"fix,omitempty"`
	Status    string   `json:"status"` // open | fixed | accepted
	FirstSeen string   `json:"first_seen"`
	LastScan  string   `json:"last_scan"`
}

// AuditReport is one scan round's risk list for one project.
type AuditReport struct {
	ProjectID string      `json:"project_id"`
	At        string      `json:"at"`
	Items     []AuditItem `json:"items"`
	// BatteryNotes per-stage run notes (skipped stages say why).
	BatteryNotes map[string]string `json:"battery_notes,omitempty"`
}

var (
	auditProjMu    sync.Mutex
	auditProjOverr string
)

func auditDir() string {
	if auditProjOverr != "" {
		return auditProjOverr
	}
	return filepath.Join(netdevStateDir(), "audit-projects")
}

func auditProjectPath(id string) string {
	return filepath.Join(auditDir(), id+".json")
}

func auditReportPath(projectID, at string) string {
	return filepath.Join(auditDir(), projectID+"-"+at+".json")
}

// SaveAuditProject persists one project (upsert by ID).
func SaveAuditProject(p *AuditProject) error {
	auditProjMu.Lock()
	defer auditProjMu.Unlock()
	if err := os.MkdirAll(auditDir(), 0o755); err != nil {
		return err
	}
	if strings.TrimSpace(p.ID) == "" {
		p.ID = fmt.Sprintf("AP%s", time.Now().Format("20060102-150405"))
	}
	if p.CreatedAt == "" {
		p.CreatedAt = time.Now().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(auditProjectPath(p.ID), data, 0o644)
}

// DeleteAuditProject removes the project and its reports.
func DeleteAuditProject(id string) error {
	auditProjMu.Lock()
	defer auditProjMu.Unlock()
	if err := os.Remove(auditProjectPath(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	entries, _ := os.ReadDir(auditDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+"-") && strings.HasSuffix(e.Name(), ".json") {
			_ = os.Remove(filepath.Join(auditDir(), e.Name()))
		}
	}
	return nil
}

// ListAuditProjects returns projects newest-first.
func ListAuditProjects() ([]*AuditProject, error) {
	auditProjMu.Lock()
	defer auditProjMu.Unlock()
	entries, err := os.ReadDir(auditDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*AuditProject
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(auditDir(), e.Name()))
		if rerr != nil {
			continue
		}
		var p AuditProject
		if jerr := json.Unmarshal(data, &p); jerr != nil || !strings.HasPrefix(p.ID, "AP") {
			continue // not a project file (report or corrupt)
		}
		cp := p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// latestAuditReport returns the project's newest report (nil when none).
func latestAuditReport(projectID string) *AuditReport {
	auditProjMu.Lock()
	defer auditProjMu.Unlock()
	entries, _ := os.ReadDir(auditDir())
	var best *AuditReport
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, projectID+"-") || !strings.HasSuffix(n, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(auditDir(), n))
		if err != nil {
			continue
		}
		var r AuditReport
		if json.Unmarshal(data, &r) != nil {
			continue
		}
		if best == nil || r.At > best.At {
			best = &r
		}
	}
	return best
}

// auditSignature is the rescan-matching key: same device+title+source = same risk.
func auditSignature(f *Finding) string {
	dev := ""
	if len(f.Devices) > 0 {
		dev = f.Devices[0]
	}
	return dev + "|" + f.Title + "|" + f.Source
}

// RunProjectAudit executes the project's checklist battery and files the risk
// list. Stages run through the SAME sealed paths as everywhere else (baseline
// rules / CVE feed / log sweep / topology read); weakcred additionally
// requires the assessment envelope and is skipped (noted) without it.
func (m *Manager) RunProjectAudit(p *AuditProject) (*AuditReport, error) {
	if p == nil || len(p.Devices) == 0 {
		return nil, fmt.Errorf("audit project has no devices")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	runStart := time.Now()
	rep := &AuditReport{ProjectID: p.ID, At: runStart.Format("20060102-150405"), Items: []AuditItem{}, BatteryNotes: map[string]string{}}
	stages := map[string]bool{}
	for _, s := range p.Checklist {
		stages[strings.TrimSpace(s)] = true
	}

	// collect 消费 Findings（SKILL_ORCHESTRATION_SPEC §3.5-G 两侧同源）：
	// 风险清单 = 本轮电池刚立案的（CreatedAt ≥ runStart，含 baseline/cve/
	// log 各 source）∪ chat 套餐立案（source=audit + 项目锚点——跨会话持续
	// 同源）。无关噪音（drift/syslog 等其它来源的历史 finding）不再混进
	// 本项目的账。
	collect := func(severityFloor string) {
		findings, err := ListFindings()
		if err != nil {
			return
		}
		for _, f := range findings {
			if !auditFindingInScope(f, p.Devices) {
				continue
			}
			if severityFloor != "" && severityRank(f.Severity) < severityRank(severityFloor) {
				continue
			}
			if !auditFindingForProject(f, p, runStart) {
				continue
			}
			rep.Items = append(rep.Items, AuditItem{
				FindingID: f.ID, Signature: auditSignature(f), Title: f.Title,
				Device: auditFirstDevice(f), Severity: f.Severity, Fix: f.Fix,
				Status: "open", FirstSeen: rep.At, LastScan: rep.At,
			})
		}
	}

	if stages["baseline"] {
		if _, err := m.runBaseline(ctx, deviceSet(p.Devices)); err != nil {
			rep.BatteryNotes["baseline"] = "基线电池失败：" + err.Error()
		} else {
			rep.BatteryNotes["baseline"] = "已执行"
		}
	}
	if stages["vuln"] {
		if _, err := m.MatchCVEsToFindings(); err != nil {
			rep.BatteryNotes["vuln"] = "CVE 匹配失败：" + err.Error()
		} else {
			rep.BatteryNotes["vuln"] = "已执行（模型逐条核查走 netdev-seccheck-auto sweep）"
		}
	}
	if stages["logs"] {
		res := m.LogSearch(ctx, "error|fail|critical|panic", p.Devices, nil, "")
		rep.BatteryNotes["logs"] = fmt.Sprintf("已执行：%d 台覆盖 / %d 处命中", res.Covered, len(res.Hits))
	}
	if stages["exposure"] {
		edges := 0
		if g, err := m.TopologySnapshot(ctx); err == nil {
			edges = len(g.Edges)
		}
		rep.BatteryNotes["exposure"] = fmt.Sprintf("已执行：邻接 %d 条（暴露面推演走对话/拓扑视图）", edges)
	}
	if stages["weakcred"] {
		if AssessmentActive(m.cfg.NetDev) != nil {
			rep.BatteryNotes["weakcred"] = "跳过：评估信封未开（安全设计——审计不绕过信封闸）"
		} else {
			for _, dev := range p.Devices {
				_, _ = m.WeakCredCheck(ctx, dev, "basic", "")
			}
			rep.BatteryNotes["weakcred"] = "已执行（basic 档，信封内）"
		}
	}

	rep.BatteryNotes["同源"] = "风险清单消费 Findings：本轮电池立案 ∪ chat 套餐立案（source=audit+项目锚点）——两侧同一本账"
	collect("warning") // info 级不进风险清单（噪音控制）

	// 复扫合并：沿用上一轮的 fixed/accepted 判定（签名匹配），未再出现的
	// open 项自动转 fixed。
	if prev := latestAuditReport(p.ID); prev != nil {
		prevStatus := map[string]string{}
		for _, it := range prev.Items {
			prevStatus[it.Signature] = it.Status
		}
		for i := range rep.Items {
			if st := prevStatus[rep.Items[i].Signature]; st == "fixed" || st == "accepted" {
				rep.Items[i].Status = st
			}
		}
		seen := map[string]bool{}
		for _, it := range rep.Items {
			seen[it.Signature] = true
		}
		for _, it := range prev.Items {
			if !seen[it.Signature] && it.Status == "open" {
				rep.Items = append(rep.Items, AuditItem{
					Signature: it.Signature, Title: it.Title, Device: it.Device,
					Severity: it.Severity, Status: "fixed",
					FirstSeen: it.FirstSeen, LastScan: rep.At,
				})
			}
		}
	}

	auditProjMu.Lock()
	defer auditProjMu.Unlock()
	if err := os.MkdirAll(auditDir(), 0o755); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return nil, err
	}
	if werr := fileutil.AtomicWriteFile(auditReportPath(p.ID, rep.At), data, 0o644); werr != nil {
		return nil, werr
	}
	_ = AppendAudit(Audit{Device: "(audit)", Command: "project-audit " + p.ID + " items=" + fmt.Sprint(len(rep.Items)), Class: "read", Status: AuditOK})
	return rep, nil
}

// saveAuditReportForTest persists a report verbatim (test seam).
func saveAuditReportForTest(r *AuditReport) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	auditProjMu.Lock()
	defer auditProjMu.Unlock()
	if err := os.MkdirAll(auditDir(), 0o755); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(auditReportPath(r.ProjectID, r.At), data, 0o644)
}

// AuditProjectStatus returns the project + its latest report (nil report when
// never scanned) + the green-light verdict: 全部 fixed∨accepted 才 true.
func AuditProjectStatus(id string) (*AuditProject, *AuditReport, bool, error) {
	auditProjMu.Lock()
	data, err := os.ReadFile(auditProjectPath(id))
	auditProjMu.Unlock()
	if err != nil {
		return nil, nil, false, fmt.Errorf("audit project %s: %w", id, err)
	}
	var p AuditProject
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, nil, false, err
	}
	rep := latestAuditReport(id)
	// A scanned project with ZERO open risks is green — the old
	// len(rep.Items)>0 requirement kept a clean project forever amber.
	green := rep != nil
	if rep != nil {
		for _, it := range rep.Items {
			if it.Status == "open" {
				green = false
				break
			}
		}
	}
	return &p, rep, green, nil
}

// SetAuditItemStatus marks one risk line fixed|accepted|open (human decision).
func SetAuditItemStatus(projectID, signature, status string) error {
	if status != "open" && status != "fixed" && status != "accepted" {
		return fmt.Errorf("bad status %q", status)
	}
	rep := latestAuditReport(projectID)
	if rep == nil {
		return fmt.Errorf("project %s has no report yet", projectID)
	}
	for i := range rep.Items {
		if rep.Items[i].Signature == signature {
			rep.Items[i].Status = status
		}
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	auditProjMu.Lock()
	defer auditProjMu.Unlock()
	return fileutil.AtomicWriteFile(auditReportPath(projectID, rep.At), data, 0o644)
}

// ── helpers ──

// auditFindingForProject is the 同源 membership rule: filed during THIS
// battery run (any source — baseline/CVE/log land here), or a chat-side 套餐
// finding (source=audit carrying this project's anchor — detail 首行
// "project:<name>" per the seccheck body contract, or the config-stamped
// Project field matching the project name).
func auditFindingForProject(f *Finding, p *AuditProject, runStart time.Time) bool {
	if !f.CreatedAt.Before(runStart) {
		return true
	}
	if f.Source != "audit" {
		return false
	}
	if f.Project != "" && (f.Project == p.Name || f.Project == p.ID) {
		return true
	}
	return strings.Contains(f.Detail, "project:"+p.Name) || strings.Contains(f.Detail, "project:"+p.ID)
}

func auditFindingInScope(f *Finding, devices []string) bool {
	if len(f.Devices) == 0 {
		return false
	}
	for _, d := range devices {
		if f.Devices[0] == d {
			return true
		}
	}
	return false
}

func auditFirstDevice(f *Finding) string {
	if len(f.Devices) > 0 {
		return f.Devices[0]
	}
	return ""
}

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	default:
		return 1
	}
}

func deviceSet(names []string) map[string]bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return set
}
