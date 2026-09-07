package netdev

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zzycxz/fairpeer/internal/fileutil"
)

// 运维简报（SCENARIO_SPEC S6-1）：netdev 侧一键生成结构化简报（巡检摘要/
// 告警分级统计/风险清单/变更记录），Markdown+JSON 双格式落
// ~/.fairpeer/briefings/（latest 覆盖 + 日期归档）。**内容仅取已脱敏产物**
// （Finding 摘要/统计/巡检日志行）——原始命令输出不入简报（安全边界）。
// 办公侧技能（ppt-auto/Excel）读 latest JSON 生成周报——文件级单向发布，
// 不打破 profile 隔离。

// BriefingSection is one report section.
type BriefingSection struct {
	Title    string            `json:"title"`
	Summary  string            `json:"summary"` // markdown
	Metrics  map[string]int    `json:"metrics,omitempty"`
	Findings []BriefingFinding `json:"findings,omitempty"`
}

// BriefingFinding is a Finding REFERENCE (title/severity/fix only — redacted
// summary, never raw evidence output).
type BriefingFinding struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	Devices  []string `json:"devices,omitempty"`
	Fix      *FixHint `json:"fix,omitempty"`
}

// Briefing is the whole document.
type Briefing struct {
	GeneratedAt string           `json:"generated_at"`
	Kind        string           `json:"kind"` // inspection | daily | weekly
	Network     string           `json:"network,omitempty"`
	Sections    []BriefingSection `json:"sections"`
}

// BriefingsDir is the publish root (office skills read from here).
func BriefingsDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = home
	}
	dir := filepath.Join(base, "fairpeer", "briefings")
	return dir, os.MkdirAll(dir, 0o755)
}

var briefingsDirOverr string

// BuildBriefing assembles a briefing from CURRENT stored state only: open
// findings (redacted summaries), inspection journal tail, proposal counts.
// No device is contacted — a briefing is a pure read-side digest.
func BuildBriefing(kind string) (*Briefing, error) {
	if kind == "" {
		kind = "daily"
	}
	b := &Briefing{GeneratedAt: time.Now().Format(time.RFC3339), Kind: kind, Sections: []BriefingSection{}}

	// ① 风险清单（open findings 摘要）
	if findings, err := ListFindings(); err == nil {
		sec := BriefingSection{Title: "风险清单", Metrics: map[string]int{}}
		for _, f := range findings {
			if f.Status == "resolved" {
				continue
			}
			sec.Metrics[f.Severity]++
			sec.Findings = append(sec.Findings, BriefingFinding{ID: f.ID, Title: f.Title, Severity: f.Severity, Devices: f.Devices, Fix: f.Fix})
		}
		sec.Summary = fmt.Sprintf("当前未关闭发现 %d 条（critical %d / warning %d / info %d）",
			sec.Metrics[SeverityCritical]+sec.Metrics[SeverityWarning]+sec.Metrics[SeverityInfo],
			sec.Metrics[SeverityCritical], sec.Metrics[SeverityWarning], sec.Metrics[SeverityInfo])
		b.Sections = append(b.Sections, sec)
	}

	// ② 巡检摘要（R1 journal 尾部；weekly 取近 7 天）
	rows := ReadInspectionRows(30)
	limit := time.Now().AddDate(0, 0, -1)
	if kind == "weekly" {
		limit = time.Now().AddDate(0, 0, -7)
	}
	sec2 := BriefingSection{Title: "巡检摘要", Metrics: map[string]int{"rounds": 0, "abnormal": 0}}
	var lines []string
	for _, r := range rows {
		ts, err := time.Parse("2006-01-02T15:04:05", r.At)
		if err != nil || ts.Before(limit) {
			continue
		}
		sec2.Metrics["rounds"]++
		if r.Critical > 0 || r.Warning > 0 {
			sec2.Metrics["abnormal"]++
		}
		lines = append(lines, fmt.Sprintf("- %s %s：设备 %d，critical %d / warning %d", r.At, map[bool]string{true: "⚠", false: "✓"}[r.Critical > 0 || r.Warning > 0], r.Devices, r.Critical, r.Warning))
	}
	sort.Slice(lines, func(i, j int) bool { return false }) // journal 已按时间
	sec2.Summary = fmt.Sprintf("%s 窗口内巡检 %d 轮（%d 轮有异常发现）", map[string]string{"daily": "24h", "weekly": "7 天", "inspection": "本轮"}[kind], sec2.Metrics["rounds"], sec2.Metrics["abnormal"])
	if len(lines) > 10 {
		lines = lines[:10]
	}
	sec2.Summary += "\n" + strings.Join(lines, "\n")
	b.Sections = append(b.Sections, sec2)

	// ③ 变更记录（近窗口提案统计）
	if proposals, err := ListProposals(); err == nil {
		sec3 := BriefingSection{Title: "变更记录", Metrics: map[string]int{}}
		for _, p := range proposals {
			if p.CreatedAt.After(limit) || p.CreatedAt.IsZero() {
				sec3.Metrics[p.Status]++
			}
		}
		total := 0
		for _, v := range sec3.Metrics {
			total += v
		}
		sec3.Summary = fmt.Sprintf("窗口内变更 %d 份（approved %d / done %d / rejected %d / draft %d）",
			total, sec3.Metrics["approved"], sec3.Metrics["done"], sec3.Metrics["rejected"], sec3.Metrics["draft"])
		b.Sections = append(b.Sections, sec3)
	}
	return b, nil
}

// WriteBriefing persists a briefing: latest-<kind>.{json,md} (overwrite) plus
// a timestamped archive copy. Returns the directory.
func WriteBriefing(b *Briefing) (string, error) {
	dir := briefingsDirOverr
	if dir == "" {
		d, err := BriefingsDir()
		if err != nil {
			return "", err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	jdata, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "", err
	}
	stamp := time.Now().Format("20060102-150405")
	for name, data := range map[string][]byte{
		fmt.Sprintf("latest-%s.json", b.Kind): jdata,
		fmt.Sprintf("%s-%s.json", b.Kind, stamp): jdata,
		fmt.Sprintf("latest-%s.md", b.Kind):       []byte(b.Markdown()),
	} {
		if err := fileutil.AtomicWriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// Markdown renders the human-readable form (email/paste friendly).
func (b *Briefing) Markdown() string {
	var sb strings.Builder
	sb.WriteString("# 运维简报（" + b.Kind + "）\n\n生成时间：" + b.GeneratedAt + "\n\n")
	for _, s := range b.Sections {
		sb.WriteString("## " + s.Title + "\n\n" + s.Summary + "\n\n")
		for _, f := range s.Findings {
			mark := map[string]string{SeverityCritical: "🔴", SeverityWarning: "⚠"}[f.Severity]
			if mark == "" {
				mark = "ℹ"
			}
			line := fmt.Sprintf("- %s %s", mark, f.Title)
			if len(f.Devices) > 0 {
				line += "（" + strings.Join(f.Devices, "、") + "）"
			}
			if f.Fix != nil {
				line += fmt.Sprintf(" → %s：%s", f.Fix.Type, f.Fix.Ref)
			}
			sb.WriteString(line + "\n")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
