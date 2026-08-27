package netdev

// handoff.go — 值班交接报告 v1（NETDEV_SPEC_V2 §5.5）：昨天 20:00 以来的
// Finding、审计写操作概览、syslog 环形缓冲尾部与未闭环项，合成一页
// markdown。数据全部来自既有面（Findings/审计/syslog 接收器），零新增采集。

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// HandoffReport builds the shift-handoff page (markdown).
func (m *Manager) HandoffReport(since time.Time) string {
	var b strings.Builder
	b.WriteString("# 值班交接报告\n\n")
	b.WriteString(fmt.Sprintf("生成时间：%s　覆盖时段：%s 起\n\n", time.Now().Format("2006-01-02 15:04"), since.Format("01-02 15:04")))

	fs, _ := ListFindings()
	var open, fresh []*Finding
	for _, f := range fs {
		if f.CreatedAt.After(since) {
			fresh = append(fresh, f)
		}
	}
	for _, f := range fs {
		if !strings.Contains(f.Title, "已解决") && !strings.HasPrefix(f.Severity, "resolved") && f.CreatedAt.Before(since) {
			open = append(open, f)
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].CreatedAt.After(fresh[j].CreatedAt) })

	b.WriteString("## 本时段新 Finding\n\n")
	if len(fresh) == 0 {
		b.WriteString("- （无）\n")
	}
	for _, f := range fresh {
		if len(fresh) > 20 && f.Severity != "critical" && f.Severity != "warning" {
			continue
		}
		b.WriteString(fmt.Sprintf("- **%s**（%s，%s）： %s\n", f.Title, f.Severity, strings.Join(f.Devices, "、"), firstLine(f.Detail)))
	}
	b.WriteString("\n## 未闭环（历史遗留）\n\n")
	if len(open) == 0 {
		b.WriteString("- （无）\n")
	}
	for i, f := range open {
		if i >= 10 {
			b.WriteString(fmt.Sprintf("- …另有 %d 条，见「发现」页签\n", len(open)-10))
			break
		}
		b.WriteString(fmt.Sprintf("- %s（%s，%s 创建）\n", f.Title, f.Severity, f.CreatedAt.Format("01-02 15:04")))
	}

	b.WriteString("\n## syslog 尾部（被动接收）\n\n")
	n := 0
	for _, d := range m.cfg.NetDev.Devices {
		lines := SyslogTail(d.Name, 3, "")
		if len(lines) == 0 {
			continue
		}
		if n++; n > 5 {
			break
		}
		b.WriteString(fmt.Sprintf("**%s**\n", d.Name))
		for _, ln := range lines {
			b.WriteString("- " + ln + "\n")
		}
	}
	if n == 0 {
		b.WriteString("- （缓冲区为空——设备 syslog 指向本机接收端口后这里有内容）\n")
	}
	b.WriteString("\n---\n交接人确认以上内容后交给下一班；未闭环项请继续跟进或转入案例。\n")
	return b.String()
}

