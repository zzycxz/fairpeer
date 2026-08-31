package netdev

// triage.go — one-click host triage battery (NETDEV_SPEC_V2 §4.1, v1: single
// device, sequential reads). Every command is a plain whitelisted read riding
// the sealed Exec path (classifier / per-turn budget / redaction / audit all
// apply). The report is structured (sections + anomalies); anomalies ALSO
// land as a Finding (Source "triage:<device>") so the 发现 queue and the
// 巡检家族 menu see them. v1 analysis is deliberately conservative: only
// high-signal anomalies fire (failed-login bursts, disk watermark, extra
// uid-0 accounts, failed collectors) — the full sections stay the evidence.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type TriageSection struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Ok      bool     `json:"ok"`
	Refused string   `json:"refused,omitempty"`
	Lines   []string `json:"lines,omitempty"`
}

type TriageReport struct {
	Device    string          `json:"device"`
	Vendor    string          `json:"vendor"`
	Sections  []TriageSection `json:"sections"`
	Anomalies []string        `json:"anomalies,omitempty"`
	Summary   string          `json:"summary"`
	CreatedAt time.Time       `json:"created_at"`
}

var linuxTriageBattery = []struct{ name, cmd string }{
	{"进程", "ps auxww"},
	{"监听与连接", "ss -tulnp"},
	{"最近登录", "last -F -n 30"},
	{"失败登录", "lastb -n 100"},
	{"在线用户", "who"},
	{"定时任务", "crontab -l"},
	{"开机自启", "systemctl list-unit-files --state=enabled"},
	{"账户表", "cat /etc/passwd"},
	{"磁盘水位", "df -h"},
	{"内存", "free -m"},
	{"时钟同步", "timedatectl"},
}

var windowsTriageBattery = []struct{ name, cmd string }{
	{"进程", "tasklist"},
	{"网络连接", "netstat -ano"},
	{"在线用户", "quser"},
	{"安全事件(近300)", "get-winevent -logname security -maxevents 300"},
	{"磁盘水位", "get-volume"},
}

// Triage runs the battery on one host and returns the structured report.
// §4.1 runbook 升级：电池作为 Job 引擎 runbook 执行——每步 30s 超时、
// on-fail=continue（诊断电池要尽量收全）、预算封顶（墙钟 10 分钟 / 命令数
// = 电池长度），全程留 job 轨迹（jobs 目录可见、可审计）；步骤语义（expect
// /retry/断点）对电池默认关闭但引擎已具备——R5 入侵排查向导直接复用。
func (m *Manager) Triage(ctx context.Context, deviceName string) TriageReport {
	rep := TriageReport{Device: deviceName, CreatedAt: time.Now()}
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		rep.Summary = fmt.Sprintf("device %q is not in the inventory (add it in 运维设置)", deviceName)
		return rep
	}
	rep.Vendor = d.Vendor
	var battery []struct{ name, cmd string }
	switch d.Vendor {
	case "linux":
		battery = linuxTriageBattery
	case "windows":
		battery = windowsTriageBattery
	default:
		rep.Summary = fmt.Sprintf("triage v1 covers linux/windows hosts (vendor=%s)", d.Vendor)
		return rep
	}

	def := &Job{
		Name:      "triage:" + deviceName,
		CreatedBy: "triage",
		Budget:    JobBudget{MaxWallSec: 600, MaxCommands: len(battery) + 2, FailStreak: len(battery) + 1},
	}
	for _, b := range battery {
		def.Steps = append(def.Steps, JobStep{
			Name: b.name, Device: deviceName, Command: b.cmd,
			TimeoutSec: 30, OnFail: JobOnFailContinue, // 收全优先：单项失败不拦电池
		})
	}
	job, err := m.RunJobSync(ctx, def)
	if err != nil {
		rep.Summary = "体检 runbook 启动失败：" + err.Error()
		return rep
	}
	for i, b := range battery {
		s := TriageSection{Name: b.name, Command: b.cmd}
		if i < len(job.StepState) {
			st := job.StepState[i]
			switch {
			case st.Status == JobStepOK:
				s.Ok = true
				s.Lines = capTriageLines(st.Output)
			case strings.HasPrefix(st.Error, "refused: "):
				s.Refused = strings.TrimPrefix(st.Error, "refused: ")
			case st.Status == JobStepFailed:
				s.Ok = false
				s.Lines = capTriageLines(st.Output)
			default: // skipped/pending (watchdog trip mid-battery)
				note := "电池未跑完（job " + job.Status
				if job.PauseNote != "" {
					note += "：" + job.PauseNote
				}
				s.Refused = note + "）"
			}
		}
		rep.Sections = append(rep.Sections, s)
	}
	rep.Anomalies = analyzeTriage(rep)
	rep.Summary = fmt.Sprintf("体检 %d 项，%d 项异常", len(rep.Sections), len(rep.Anomalies))
	if job.Status != JobDone {
		rep.Summary += "；电池未完整跑完（job " + job.Status + "）"
	}
	if len(rep.Anomalies) > 0 {
		_ = SaveFinding(&Finding{
			Title:      "主机体检：" + deviceName,
			Severity:   "warning",
			Devices:    []string{deviceName},
			Detail:     strings.Join(rep.Anomalies, "\n"),
			Evidence:   sectionsEvidence(rep),
			Suggestion: "结合「实况」输出复核异常项；若持续恶化，用日志工作台的跨设备搜索追 IOC，必要时走提案隔离。",
			Source:     "triage:" + deviceName,
		})
	}
	return rep
}

func capTriageLines(out string) []string {
	lines := strings.Split(out, "\n")
	if len(lines) > 400 {
		lines = lines[:400]
	}
	return lines
}

var triageIPRe = regexp.MustCompile(`(\d{1,3}(?:\.\d{1,3}){3})`)
var triagePctRe = regexp.MustCompile(`(\d{1,3})%`)

// analyzeTriage extracts the conservative high-signal anomalies.
func analyzeTriage(rep TriageReport) []string {
	var anomalies []string
	section := func(name string) *TriageSection {
		for i := range rep.Sections {
			if rep.Sections[i].Name == name {
				return &rep.Sections[i]
			}
		}
		return nil
	}

	// Failed logins: lastb lines (linux) or 4625 events (windows), aggregated
	// by source IP. A burst is an anomaly even before it is an incident.
	failed := map[string]int{}
	total := 0
	if s := section("失败登录"); s != nil && s.Ok {
		for _, ln := range s.Lines {
			if strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "lastb:") {
				continue
			}
			total++
			if ip := triageIPRe.FindString(ln); ip != "" {
				failed[ip]++
			}
		}
	}
	if s := section("安全事件(近300)"); s != nil && s.Ok {
		for _, ln := range s.Lines {
			if strings.Contains(ln, "4625") {
				total++
				if ip := triageIPRe.FindString(ln); ip != "" {
					failed[ip]++
				}
			}
		}
	}
	if total >= 10 {
		anomalies = append(anomalies, fmt.Sprintf("失败登录 %d 次（Top 源：%s）", total, topIPs(failed, 3)))
	}

	// Disk watermark: highest usage percentage across filesystems.
	if s := section("磁盘水位"); s != nil && s.Ok {
		max := 0
		for _, ln := range s.Lines {
			for _, m := range triagePctRe.FindAllStringSubmatch(ln, -1) {
				if v, err := strconv.Atoi(m[1]); err == nil && v > max {
					max = v
				}
			}
		}
		if max >= 85 {
			anomalies = append(anomalies, fmt.Sprintf("磁盘使用率最高 %d%%（≥85%% 水位）", max))
		}
	}

	// Extra uid-0 accounts beyond root.
	if s := section("账户表"); s != nil && s.Ok {
		var uid0 []string
		for _, ln := range s.Lines {
			f := strings.Split(ln, ":")
			if len(f) >= 3 && f[2] == "0" && f[0] != "root" {
				uid0 = append(uid0, f[0])
			}
		}
		if len(uid0) > 0 {
			anomalies = append(anomalies, fmt.Sprintf("uid=0 账户 %d 个（root 之外）：%s", len(uid0), strings.Join(uid0, ", ")))
		}
	}

	// Clock sync: an unsynchronized host poisons every cross-source timeline
	// merge (NETDEV_SPEC_V2 §4.1 时钟行 / §10.3 偏差徽标的数据来源).
	if s := section("时钟同步"); s != nil && s.Ok {
		notSynced, ntpOff := false, false
		for _, ln := range s.Lines {
			l := strings.ToLower(ln)
			if strings.Contains(l, "synchronized: no") || strings.Contains(l, "synchronized: n") {
				notSynced = true
			}
			if strings.Contains(l, "ntp service: inactive") || strings.Contains(l, "ntp service: 没有运行") {
				ntpOff = true
			}
		}
		if notSynced || ntpOff {
			anomalies = append(anomalies, "时钟未同步（跨源日志合并可信度受损——先修 NTP 再看时间线）")
		}
	}

	// Failed collectors: a refused/errored section means the report has a
	// hole — say so instead of implying coverage.
	for i := range rep.Sections {
		s := &rep.Sections[i]
		if s.Refused != "" {
			anomalies = append(anomalies, fmt.Sprintf("采集被拒：%s（%s）", s.Name, s.Refused))
		} else if !s.Ok && len(s.Lines) == 0 {
			anomalies = append(anomalies, fmt.Sprintf("采集失败：%s（%s）", s.Name, s.Command))
		}
	}
	return anomalies
}

func topIPs(freq map[string]int, n int) string {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range freq {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	var parts []string
	for i, e := range list {
		if i >= n {
			break
		}
		parts = append(parts, fmt.Sprintf("%s×%d", e.k, e.v))
	}
	if len(parts) == 0 {
		return "无源 IP"
	}
	return strings.Join(parts, "、")
}

// sectionsEvidence caps each section's output for the Finding evidence chain.
func sectionsEvidence(rep TriageReport) []Evidence {
	ev := make([]Evidence, 0, len(rep.Sections))
	for _, s := range rep.Sections {
		out := strings.Join(s.Lines, "\n")
		if len(out) > 2000 {
			out = out[:2000] + "\n…（截断）"
		}
		if out == "" {
			out = "(" + firstNonEmpty(s.Refused, "no output / error") + ")"
		}
		ev = append(ev, Evidence{Device: rep.Device, Command: s.Command, Output: out})
	}
	return ev
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ── Agent tool ───────────────────────────────────────────────────────────────

// triageTool — netdev_triage: the one-click host checkup battery.
type triageTool struct{ m *Manager }

func (t *triageTool) Name() string { return "netdev_triage" }

func (t *triageTool) Description() string {
	return "Run the one-click host TRIAGE battery on ONE linux/windows host: processes, listeners, recent & failed logins, online users, crontab, enabled units, /etc/passwd, disk/memory watermark, clock sync. " +
		"Every command rides the sealed read-only path; anomalies (failed-login bursts, disk ≥85%, extra uid-0 accounts, failed collectors) also become a Finding. " +
		"Use it first for health checks and security sweep starting points."
}

func (t *triageTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"device": {"type": "string", "description": "linux/windows host name from netdev_devices"}
		},
		"required": ["device"]
	}`)
}

func (t *triageTool) ReadOnly() bool { return true }

func (t *triageTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Device string `json:"device"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Device) == "" {
		return "", errors.New("netdev_triage: device is required")
	}
	b, err := json.Marshal(t.m.Triage(ctx, a.Device))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
