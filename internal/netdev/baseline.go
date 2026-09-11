package netdev

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/zzycxz/fairpeer/internal/netdev/knowledge"
)

// baseline.go — the configuration security baseline check: a LOCAL rule
// battery over each device's running-config (read through the sealed Exec
// path, so every line has already been redacted). The doctrine stays the
// same: we only ship rules whose syntax we can state precisely — anything
// ambiguous (e.g. "overly permissive ACL" means different things on a core
// switch vs. a firewall) is deliberately left out.
//
// Rule families: huawei-vrp / cisco-ios / h3c-comware（锐捷 RGOS 经驱动映射
// 归入 cisco-ios）；zte-zxr10 仍因语法无法精确表述而不收——准确性纪律不松。
// 规则本体住在 knowledge/data/baseline-rules.yaml（§9 批 A 外置）：
//   telnet-enabled      telnet 管理面开启（明文协议）
//   snmp-v1v2c          SNMP v1/v2c community 在用（v2c 报文可被嗅探）
//   plaintext-password  simple/0 形式的明文密码（配置文件中可读）
//   ssh-v1              SSH v1 兼容开启
//   no-ntp              未配置 NTP（日志时间不可信，info 级）
//   no-syslog           未外发日志（info 级）

// RunningConfigCommand returns the read command that dumps the running
// configuration for a driver key.
func RunningConfigCommand(driverKey string) (string, bool) {
	switch driverKey {
	case "huawei-vrp":
		return "display current-configuration", true
	case "cisco-ios":
		return "show running-config", true
	case "h3c-comware":
		return "display current-configuration", true
	case "zte-zxr10":
		return "show running-config", true
	}
	return "", false
}

type baselineRule struct {
	id       string
	title    string
	severity string
	// fix 是该规则的结构化修复（S2-1）：基线违例属"已核实配置问题"，
	// Confidence=verified，Ref 即修复命令。
	fixType string // config|credential
	fixRef  string
	// present rules fire when pattern MATCHES a config line (violation).
	pattern *regexp.Regexp
	// absent rules fire when presence NEVER matches the whole config.
	absence  bool
	presence *regexp.Regexp
	hint     string
}

// baselineRulesLoad lazily resolves the rule table from the knowledge store
// (BLUETEAM §9 批 A：规则外置 baseline-rules.yaml，user-knowledge 同 id 覆盖）。
// A corrupt user override degrades to the EMBEDDED builtin and the error is
// surfaced via BaselineRulesError——显式报错不静默，也绝不静默空表运行
// （覆盖与内置双失败的角隅：nil 表各路径安全，但那就是空表运行）。
var (
	baselineTblOnce sync.Once
	baselineTbl     map[string][]baselineRule
	baselineTblErr  error
)

func baselineRulesTable() (map[string][]baselineRule, error) {
	baselineTblOnce.Do(func() {
		if b, err := knowledge.Load("baseline-rules"); err == nil {
			// Load 结果先过 schema 校验再解析——手工编辑的 user-knowledge 覆盖
			// 不经 SaveUser，坏数据（空 pattern=全行误报、坏 severity、空
			// driver 清空整族）必须在这里被拒，而不是静默进引擎。
			if err = knowledge.Validate("baseline-rules", b); err == nil {
				baselineTbl, err = parseBaselineRules(b)
			}
			if err == nil {
				return
			}
			baselineTblErr = err
		} else {
			baselineTblErr = err
		}
		if eb, e := knowledge.LoadBuiltin("baseline-rules"); e == nil {
			if tbl, e := parseBaselineRules(eb); e == nil {
				baselineTbl = tbl
			}
		}
	})
	return baselineTbl, baselineTblErr
}

// BaselineRulesError reports a rule-table load/parse problem (nil = healthy).
// runBaseline writes it into the summary finding's problems — the UI 发现
// 队列 is the authoritative surface; CheckBaseline only consumes the
// (possibly degraded) table.
func BaselineRulesError() error {
	_, err := baselineRulesTable()
	return err
}

// baselineRuleFile mirrors the YAML schema; the authoritative validation
// lives in knowledge.Validate("baseline-rules", …) — the engine path runs it
// before parsing so hand-edited overrides fail loudly.
type baselineRuleFile struct {
	Version int    `yaml:"version"`
	Source  string `yaml:"source"`
	Drivers map[string][]struct {
		ID       string `yaml:"id"`
		Title    string `yaml:"title"`
		Severity string `yaml:"severity"`
		FixType  string `yaml:"fix_type"`
		FixRef   string `yaml:"fix_ref"`
		Pattern  string `yaml:"pattern"`
		Absence  bool   `yaml:"absence"`
		Presence string `yaml:"presence"`
		Hint     string `yaml:"hint"`
	} `yaml:"drivers"`
}

func parseBaselineRules(b []byte) (map[string][]baselineRule, error) {
	var f baselineRuleFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Version < 1 || len(f.Drivers) == 0 {
		return nil, fmt.Errorf("baseline-rules: version and drivers are required")
	}
	out := make(map[string][]baselineRule, len(f.Drivers))
	for drv, rules := range f.Drivers {
		for _, r := range rules {
			br := baselineRule{id: r.ID, title: r.Title, severity: r.Severity, fixType: r.FixType, fixRef: r.FixRef, absence: r.Absence, hint: r.Hint}
			var err error
			if r.Absence {
				if br.presence, err = regexp.Compile(r.Presence); err != nil {
					return nil, fmt.Errorf("baseline-rules: %s %s presence: %v", drv, r.ID, err)
				}
			} else {
				if br.pattern, err = regexp.Compile(r.Pattern); err != nil {
					return nil, fmt.Errorf("baseline-rules: %s %s pattern: %v", drv, r.ID, err)
				}
			}
			out[drv] = append(out[drv], br)
		}
	}
	return out, nil
}

// CheckBaseline runs the rule battery over one config text and returns the
// violated rules with their evidence lines (already-redacted text). Pure
// function — unit-testable without any device.
func CheckBaseline(driverKey, config string) []BaselineViolation {
	tbl, _ := baselineRulesTable()
	rules, ok := tbl[driverKey]
	if !ok {
		return nil // unknown family: no rules is honest, wrong rules are not
	}
	var out []BaselineViolation
	for _, r := range rules {
		if r.absence {
			if !r.presence.MatchString(config) {
				out = append(out, BaselineViolation{Rule: r.id, Title: r.title, Severity: r.severity, Suggestion: r.hint, Fix: baselineRuleFix(r), Evidence: []string{"（整份配置未出现 " + r.presence.String() + "）"}})
			}
			continue
		}
		var ev []string
		for _, line := range strings.Split(config, "\n") {
			if r.pattern.MatchString(line) {
				ev = append(ev, strings.TrimSpace(line))
				if len(ev) >= 5 {
					break
				}
			}
		}
		if len(ev) > 0 {
			out = append(out, BaselineViolation{Rule: r.id, Title: r.title, Severity: r.severity, Suggestion: r.hint, Fix: baselineRuleFix(r), Evidence: ev})
		}
	}
	return out
}

// BaselineViolation is one rule hit on one device.
type BaselineViolation struct {
	Rule       string   `json:"rule"`
	Title      string   `json:"title"`
	Severity   string   `json:"severity"`
	Suggestion string   `json:"suggestion,omitempty"`
	Fix        *FixHint `json:"fix,omitempty"` // S2-1：结构化修复（verified）
	Evidence   []string `json:"evidence"`
}

// BaselineSummary is the aggregate the UI and the agent tool see.
type BaselineSummary struct {
	Devices int    `json:"devices"`
	Checked int    `json:"checked"` // devices whose config was actually read
	Rules   int    `json:"rules"`   // applicable rule count across devices
	Hits    int    `json:"hits"`    // total violations
	At      string `json:"at"`
}

// RunBaseline reads every device's running-config through the sealed path
// (full audit, redaction before rules run) and files one Finding per violated
// rule plus a summary Finding. Mirror of RunInspection's flow.
// baselineRuleFix renders a rule's structured fix (S2-1)：verified 来源。
func baselineRuleFix(r baselineRule) *FixHint {
	if r.fixRef == "" {
		return nil
	}
	return &FixHint{Type: orDefault(r.fixType, "config"), Ref: r.fixRef, Confidence: "verified"}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// baselineFamilyNames lists the loaded driver keys sorted — the summary
// finding's honest statement of what was actually checked.
func baselineFamilyNames(tbl map[string][]baselineRule) []string {
	if len(tbl) == 0 {
		return []string{"（规则表为空）"}
	}
	out := make([]string, 0, len(tbl))
	for k := range tbl {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) RunBaseline(ctx context.Context) (*Finding, error) {
	return m.runBaseline(ctx, nil)
}

// RunBaselineFor is RunBaseline scoped to a device set (nil/empty = all) —
// the P2-3 proposal-driven re-check runs only the devices a finished
// proposal actually touched, so an in-flight fix elsewhere can't be
// auto-resolved out from under its own alert.
func (m *Manager) RunBaselineFor(ctx context.Context, devices []string) (*Finding, error) {
	if len(devices) == 0 {
		return m.RunBaseline(ctx)
	}
	set := map[string]bool{}
	for _, d := range devices {
		set[d] = true
	}
	return m.runBaseline(ctx, set)
}

func (m *Manager) runBaseline(ctx context.Context, only map[string]bool) (*Finding, error) {
	if !m.cfg.NetDev.Enabled || len(m.cfg.NetDev.Devices) == 0 {
		return nil, fmt.Errorf("netdev disabled or no devices configured")
	}
	var mu sync.Mutex
	byRule := map[string]*Finding{}
	summary := &BaselineSummary{At: time.Now().Format("01-02 15:04")}
	var problems []string
	if err := BaselineRulesError(); err != nil {
		problems = append(problems, fmt.Sprintf("基线规则表异常（已回退内置表）：%v", err))
	}
	tbl, _ := baselineRulesTable()
	var summaryEvidence []Evidence
	var checked []string
	for _, d := range m.cfg.NetDev.Devices {
		if only != nil && !only[d.Name] {
			continue
		}
		summary.Devices++
		drv, ok := m.driverFor(d)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: no driver (%s/%s)", d.Name, d.Vendor, d.OS))
			continue
		}
		rules, ok := tbl[drv.Key()]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: %s 无基线规则（规则表 knowledge/baseline-rules.yaml）", d.Name, drv.Key()))
			continue
		}
		cmd, ok := RunningConfigCommand(drv.Key())
		if !ok {
			continue
		}
		res := m.Exec(ctx, d.Name, cmd)
		if res.Refused {
			problems = append(problems, fmt.Sprintf("%s: %s refused (%s)", d.Name, cmd, res.Class))
			continue
		}
		if res.IsError {
			problems = append(problems, fmt.Sprintf("%s: %s device error", d.Name, cmd))
			continue
		}
		summary.Checked++
		checked = append(checked, d.Name)
		summary.Rules += len(rules)
		summaryEvidence = append(summaryEvidence, Evidence{Device: d.Name, Command: cmd,
			Output: fmt.Sprintf("running-config 已读取并核查（%d 行，已脱敏）", strings.Count(res.Output, "\n")+1)})
		for _, v := range CheckBaseline(drv.Key(), res.Output) {
			mu.Lock()
			f, ok := byRule[v.Rule]
			if !ok {
				f = &Finding{Title: "基线：" + v.Title, Severity: v.Severity, Suggestion: v.Suggestion, Fix: v.Fix}
				byRule[v.Rule] = f
			}
			f.Devices = append(f.Devices, d.Name)
			for _, line := range v.Evidence {
				f.Evidence = append(f.Evidence, Evidence{Device: d.Name, Command: cmd, Output: line})
			}
			summary.Hits++
			mu.Unlock()
		}
	}
	for rule, f := range byRule {
		// P2-3：基线发现接入 Source 生命周期——同一规则重复核查更新同一告警
		//（不再堆积），规则不再命中且全部受检设备已复核的自动 resolve。
		f.Source = "baseline:" + rule
		f.Status = "active"
	}
	if err := reconcileBaselineFindings(byRule, checked); err != nil {
		problems = append(problems, "reconcile: "+err.Error())
	}
	for _, f := range byRule {
		if err := SaveFinding(f); err != nil {
			problems = append(problems, "save finding: "+err.Error())
		}
	}
	summaryFinding := &Finding{
		Title:    fmt.Sprintf("安全基线核查完成：%d 台受检 / %d 台读取成功，命中 %d 项", summary.Devices, summary.Checked, summary.Hits),
		Severity: "info",
		Detail:   fmt.Sprintf("规则族覆盖 %s（仅收录可精确表述的规则）。%s", strings.Join(baselineFamilyNames(tbl), " / "), strings.Join(problems, "；")),
		Evidence: summaryEvidence,
		Source:   "baseline:summary", // 单条滚动：历次运行在巡检日志，发现队列只留一张活卡
	}
	if err := SaveRollingFinding(summaryFinding); err != nil {
		return nil, err
	}
	// R1 journal + 总览 BaselineAgg 的持久位（best-effort，不影响核查结果）。
	SaveLastBaseline(*summary)
	crit, warn, info := OpenFindingTallies()
	_ = AppendInspectionRow(InspectionJournalRow{
		Kind: "baseline", Devices: summary.Devices, Checked: summary.Checked,
		Critical: crit, Warning: warn, Info: info, BaselineHits: summary.Hits,
	})
	return summaryFinding, nil
}

// reconcileBaselineFindings folds a fresh run into the active baseline
// findings (P2-3): a rule that still hits UPDATES the existing alert in
// place (same ID — re-runs stop piling up "基线：…" copies); a rule that no
// longer hits auto-resolves WHEN every device on the old alert was in this
// run's checked set — a scoped re-check (proposal devices only) must never
// resolve an alert it didn't actually re-verify.
func reconcileBaselineFindings(hit map[string]*Finding, checked []string) error {
	inRun := map[string]bool{}
	for _, d := range checked {
		inRun[d] = true
	}
	active, err := ListFindings()
	if err != nil {
		return err
	}
	for _, f := range active {
		if !strings.HasPrefix(f.Source, "baseline:") || f.Status == "resolved" || f.Source == "baseline:summary" {
			continue
		}
		if fresh, ok := hit[strings.TrimPrefix(f.Source, "baseline:")]; ok {
			// Still violated: the fresh finding inherits the alert's identity
			// and original raise time so the timeline stays one alert.
			fresh.ID = f.ID
			fresh.CreatedAt = f.CreatedAt
			continue
		}
		allChecked := len(f.Devices) > 0
		for _, d := range f.Devices {
			if !inRun[d] {
				allChecked = false
				break
			}
		}
		if !allChecked {
			continue
		}
		now := time.Now()
		f.Status = "resolved"
		f.ResolvedAt = &now
		if f.Detail != "" && !strings.Contains(f.Detail, "已恢复") {
			f.Detail += "（复核通过，自动恢复）"
		}
		if err := SaveFinding(f); err != nil {
			return err
		}
	}
	return nil
}
