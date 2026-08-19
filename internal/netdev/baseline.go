package netdev

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// baseline.go — the configuration security baseline check: a LOCAL rule
// battery over each device's running-config (read through the sealed Exec
// path, so every line has already been redacted). The doctrine stays the
// same: we only ship rules whose syntax we can state precisely — anything
// ambiguous (e.g. "overly permissive ACL" means different things on a core
// switch vs. a firewall) is deliberately left out.
//
// Rule families (huawei-vrp / cisco-ios only — ZXR10 config syntax differs
// enough that guessing would violate the accuracy bar):
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
	case "zte-zxr10":
		return "show running-config", true
	}
	return "", false
}

type baselineRule struct {
	id       string
	title    string
	severity string
	// present rules fire when pattern MATCHES a config line (violation).
	pattern *regexp.Regexp
	// absent rules fire when presence NEVER matches the whole config.
	absence  bool
	presence *regexp.Regexp
	hint     string
}

var baselineRules = map[string][]baselineRule{
	"huawei-vrp": {
		{id: "telnet-enabled", title: "Telnet 管理服务开启", severity: "warning",
			pattern: regexp.MustCompile(`(?im)^\s*telnet\s+server\s+enable\b`),
			hint:    "关闭 telnet server，管理面仅保留 SSH（可让 agent 起草提案：undo telnet server enable）"},
		{id: "snmp-v1v2c", title: "SNMP v1/v2c community 在用", severity: "warning",
			pattern: regexp.MustCompile(`(?im)^\s*snmp-agent\s+community\b`),
			hint:    "改用 SNMPv3（USM 用户 + authPriv），移除 community 配置"},
		{id: "plaintext-password", title: "存在 simple 明文密码", severity: "critical",
			pattern: regexp.MustCompile(`(?im)^\s*(?:\S+\s+)*password\s+simple\b`),
			hint:    "本地用户密码改为 cipher/irreversible-cipher 存储"},
		{id: "ssh-v1", title: "SSH v1 兼容模式开启", severity: "warning",
			pattern: regexp.MustCompile(`(?im)^\s*ssh\s+server\s+compatible\s+sshv1\s+enable\b`),
			hint:    "关闭 SSH v1 兼容：undo ssh server compatible sshv1 enable"},
		{id: "no-ntp", title: "未配置 NTP 时间同步", severity: "info",
			absence: true, presence: regexp.MustCompile(`(?im)^\s*ntp-service\s+`),
			hint:    "配置 ntp-service unicast-server；日志与审计的时间戳才有取证价值"},
		{id: "no-syslog", title: "未配置日志外发", severity: "info",
			absence: true, presence: regexp.MustCompile(`(?im)^\s*info-center\s+loghost\b`),
			hint:    "配置 info-center loghost 指向日志服务器"},
	},
	"cisco-ios": {
		{id: "telnet-enabled", title: "VTY 线路允许 Telnet 接入", severity: "warning",
			pattern: regexp.MustCompile(`(?im)^\s*transport\s+input\s+(?:all\b|.*\btelnet\b)`),
			hint:    "VTY 改为 transport input ssh 仅允许 SSH"},
		{id: "snmp-v1v2c", title: "SNMP v1/v2c community 在用", severity: "warning",
			pattern: regexp.MustCompile(`(?im)^\s*snmp-server\s+community\b`),
			hint:    "改用 SNMPv3（snmp-server group/user，authPriv）"},
		{id: "plaintext-password", title: "存在 0 级明文密码", severity: "critical",
			pattern: regexp.MustCompile(`(?im)^\s*(?:\S+\s+)*password\s+0\s`),
			hint:    "改用加密存储（service password-encryption + secret）"},
		{id: "ssh-v1", title: "SSH 版本显式设为 v1", severity: "warning",
			pattern: regexp.MustCompile(`(?im)^\s*ip\s+ssh\s+version\s+1\b`),
			hint:    "ip ssh version 2"},
		{id: "no-ntp", title: "未配置 NTP 时间同步", severity: "info",
			absence: true, presence: regexp.MustCompile(`(?im)^\s*ntp\s+(server|peer)\b`),
			hint:    "配置 ntp server；日志与审计的时间戳才有取证价值"},
		{id: "no-syslog", title: "未配置日志外发", severity: "info",
			absence: true, presence: regexp.MustCompile(`(?im)^\s*logging\s+host\b`),
			hint:    "配置 logging host 指向日志服务器"},
	},
}

// CheckBaseline runs the rule battery over one config text and returns the
// violated rules with their evidence lines (already-redacted text). Pure
// function — unit-testable without any device.
func CheckBaseline(driverKey, config string) []BaselineViolation {
	rules, ok := baselineRules[driverKey]
	if !ok {
		return nil // unknown family: no rules is honest, wrong rules are not
	}
	var out []BaselineViolation
	for _, r := range rules {
		if r.absence {
			if !r.presence.MatchString(config) {
				out = append(out, BaselineViolation{Rule: r.id, Title: r.title, Severity: r.severity, Suggestion: r.hint, Evidence: []string{"（整份配置未出现 " + r.presence.String() + "）"}})
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
			out = append(out, BaselineViolation{Rule: r.id, Title: r.title, Severity: r.severity, Suggestion: r.hint, Evidence: ev})
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
	Evidence   []string `json:"evidence"`
}

// BaselineSummary is the aggregate the UI and the agent tool see.
type BaselineSummary struct {
	Devices int `json:"devices"`
	Checked int `json:"checked"` // devices whose config was actually read
	Rules   int `json:"rules"`   // applicable rule count across devices
	Hits    int `json:"hits"`    // total violations
	At      string `json:"at"`
}

// RunBaseline reads every device's running-config through the sealed path
// (full audit, redaction before rules run) and files one Finding per violated
// rule plus a summary Finding. Mirror of RunInspection's flow.
func (m *Manager) RunBaseline(ctx context.Context) (*Finding, error) {
	if !m.cfg.NetDev.Enabled || len(m.cfg.NetDev.Devices) == 0 {
		return nil, fmt.Errorf("netdev disabled or no devices configured")
	}
	var mu sync.Mutex
	byRule := map[string]*Finding{}
	summary := &BaselineSummary{At: time.Now().Format("01-02 15:04")}
	var problems []string
	var summaryEvidence []Evidence
	for _, d := range m.cfg.NetDev.Devices {
		summary.Devices++
		drv, ok := m.driverFor(d)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: no driver (%s/%s)", d.Name, d.Vendor, d.OS))
			continue
		}
		rules, ok := baselineRules[drv.Key()]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: %s 无基线规则（仅 huawei/cisco 提供准确规则）", d.Name, drv.Key()))
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
		summary.Rules += len(rules)
		summaryEvidence = append(summaryEvidence, Evidence{Device: d.Name, Command: cmd,
			Output: fmt.Sprintf("running-config 已读取并核查（%d 行，已脱敏）", strings.Count(res.Output, "\n")+1)})
		for _, v := range CheckBaseline(drv.Key(), res.Output) {
			mu.Lock()
			f, ok := byRule[v.Rule]
			if !ok {
				f = &Finding{Title: "基线：" + v.Title, Severity: v.Severity, Suggestion: v.Suggestion}
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
	for _, f := range byRule {
		if err := SaveFinding(f); err != nil {
			problems = append(problems, "save finding: "+err.Error())
		}
	}
	summaryFinding := &Finding{
		Title:    fmt.Sprintf("安全基线核查完成：%d 台受检 / %d 台读取成功，命中 %d 项", summary.Devices, summary.Checked, summary.Hits),
		Severity: "info",
		Detail:   fmt.Sprintf("规则族覆盖 huawei-vrp / cisco-ios（仅收录可精确表述的规则）。%s", strings.Join(problems, "；")),
		Evidence: summaryEvidence,
	}
	if err := SaveFinding(summaryFinding); err != nil {
		return nil, err
	}
	return summaryFinding, nil
}
