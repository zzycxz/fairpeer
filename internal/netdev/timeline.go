package netdev

// timeline.go — 时间关联层（NETDEV_SPEC_V2 §5.4）：把「最近谁改了什么」和
// 「这台机器发生了什么」合成一条事件流。数据全部来自既有面——审计的写
// 路径（变更）、Findings（结论）、syslog/trap 环形（事件）。UI 在日志工作台
// 里把这些作为「关联源」合并进同一条时间线（实体 360°），不新增界面面。

import (
	"sort"
	"strings"
	"time"
)

// NetDevEvent is one structured ring entry (syslog/trap).
type NetDevEvent struct {
	Time time.Time
	Text string
}

// TimelineEvent is one correlation entry on the unified axis.
type TimelineEvent struct {
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"` // change | finding | event
	Device string    `json:"device"`
	Title  string    `json:"title"`
	Detail string    `json:"detail,omitempty"`
}

// timelineChangeClasses: audit classes that mean "something changed" —
// plain reads stay out (they would flood the axis).
var timelineChangeClasses = map[string]bool{
	"write": true, "dangerous": true, "proposal-write": true, "proposal-rollback": true,
}

const timelineCap = 300

// Timeline assembles the correlation stream for one device ("" = all) inside
// the window (hours, default 24, cap 168).
func (m *Manager) Timeline(device string, hours int) []TimelineEvent {
	if hours <= 0 || hours > 168 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var out []TimelineEvent

	// 变更：审计写路径
	for _, e := range readAuditSince(since) {
		if !timelineChangeClasses[e.Class] {
			continue
		}
		if device != "" && e.Device != device {
			continue
		}
		out = append(out, TimelineEvent{Time: e.Time, Kind: "change", Device: e.Device, Title: e.Command, Detail: e.Class})
	}

	// Finding：结论
	if fs, err := ListFindings(); err == nil {
		for _, f := range fs {
			if f.CreatedAt.Before(since) {
				continue
			}
			if device != "" && !containsStr(f.Devices, device) {
				continue
			}
			out = append(out, TimelineEvent{Time: f.CreatedAt, Kind: "finding", Device: strings.Join(f.Devices, ","), Title: f.Title, Detail: f.Severity})
		}
	}

	// 事件：syslog / trap 环形缓冲
	for _, ev := range SyslogEventsSince(device, since) {
		out = append(out, TimelineEvent{Time: ev.Time, Kind: "event", Device: device, Title: ev.Text})
	}
	for _, ev := range TrapEventsSince(device, since) {
		out = append(out, TimelineEvent{Time: ev.Time, Kind: "event", Device: device, Title: ev.Text})
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	if len(out) > timelineCap {
		out = out[len(out)-timelineCap:]
	}
	return out
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ExpectedStateView — 期望状态对比（§5.4 第三件）：清单声明 vs 健康采集。
type ExpectedStateView struct {
	Total     int            `json:"total"` // 有采集面的设备数
	Reachable int            `json:"reachable"`
	Missing   []DeviceHealth `json:"missing"` // 期望在线但不可达
	NoProbe   []string       `json:"noProbe"` // 清单里暂无健康采集面的设备
}

// ExpectedState diffs the inventory against the latest SNMP health sweep.
func (m *Manager) ExpectedState() ExpectedStateView {
	snap := m.HealthSnapshot()
	view := ExpectedStateView{Total: len(snap.Devices)}
	for _, h := range snap.Devices {
		if h.Reachable {
			view.Reachable++
		} else {
			view.Missing = append(view.Missing, h)
		}
	}
	seen := map[string]bool{}
	for _, h := range snap.Devices {
		seen[h.Device] = true
	}
	for _, d := range m.cfg.NetDev.Devices {
		if d.SNMP == nil && !seen[d.Name] {
			view.NoProbe = append(view.NoProbe, d.Name)
		}
	}
	return view
}
