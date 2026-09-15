package netdev

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/netdev/driver"
)

// inspectionConcurrencyDefault is the sweep fan-out when the site config
// doesn't override [[netdev]] inspection_concurrency（F12）。
const inspectionConcurrencyDefault = 4

// inspectionConcurrencyMax caps the configured fan-out — the sweep is still
// one sealed exec per command, but 16 concurrent SSH dials per process is the
// sane ceiling for a management workstation.
const inspectionConcurrencyMax = 16

func (m *Manager) inspectionConcurrency() int {
	if n := m.cfg.NetDev.InspectionConcurrency; n > 0 {
		if n > inspectionConcurrencyMax {
			return inspectionConcurrencyMax
		}
		return n
	}
	return inspectionConcurrencyDefault
}

// RunInspection sweeps every inventory device with a fixed read battery and
// files one Finding with the evidence — the manual form of 定时巡检 (the
// scheduler wiring lands with the jobs integration). Everything runs through
// the sealed read path (classifier + redaction + audit per command).
func (m *Manager) RunInspection(ctx context.Context) (*Finding, error) {
	return m.RunInspectionProgress(ctx, nil)
}

// RunInspectionProgress is RunInspection with a per-device progress callback:
// progress(done, total) fires after each device's battery completes, total
// being the driver-resolved device count. The desktop's task-ified 立即巡检
// renders 巡检中…（N/M） from it without polling.
//
// F12 并发化：设备电池按 inspection_concurrency（0=4，上限 16）有界并发——
// 纯串行在 100 节点要 20-35 分钟/轮。两处确定性纪律：
//  1. progress 回调全程持锁串行触发（回调方多为 Wails EventsEmit，不假设
//     其线程安全）；done 计数与回调原子。
//  2. evidence/devices/problems 按配置清单序组装——Finding 内容不随完成
//     时序漂移（审计与对比依赖稳定输出）。
func (m *Manager) RunInspectionProgress(ctx context.Context, progress func(done, total int)) (*Finding, error) {
	if !m.cfg.NetDev.Enabled || len(m.cfg.NetDev.Devices) == 0 {
		return nil, fmt.Errorf("netdev disabled or no devices configured")
	}
	start := time.Now()

	total := 0
	for _, d := range m.cfg.NetDev.Devices {
		if _, ok := m.driverFor(d); ok {
			total++
		}
	}

	type devResult struct {
		name     string
		evidence []Evidence
		problems []string
	}
	results := make([]devResult, len(m.cfg.NetDev.Devices))
	var (
		wg    sync.WaitGroup
		resMu sync.Mutex // 保护 progress 计数与回调；results 按 idx 写互不重叠
		done  int
		sem   = make(chan struct{}, m.inspectionConcurrency())
	)
	for i := range m.cfg.NetDev.Devices {
		d := m.cfg.NetDev.Devices[i]
		drv, ok := m.driverFor(d)
		if !ok {
			// name 留空：无驱动设备不进 devices 清单与 evidence（对齐串行版）。
			results[i] = devResult{problems: []string{fmt.Sprintf("%s: no driver (%s/%s)", d.Name, d.Vendor, d.OS)}}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, name string, drv driver.Driver) {
			defer wg.Done()
			defer func() { <-sem }()
			var r devResult
			r.name = name
			for _, cmd := range inspectionBattery(drv) {
				res := m.Exec(ctx, name, cmd)
				if res.Refused {
					r.problems = append(r.problems, fmt.Sprintf("%s: %s refused (%s)", name, cmd, res.Class))
					continue
				}
				r.evidence = append(r.evidence, Evidence{Device: name, Command: cmd, Output: res.Output})
				if res.IsError {
					r.problems = append(r.problems, fmt.Sprintf("%s: %s → device error", name, cmd))
				}
			}
			results[i] = r
			resMu.Lock()
			done++
			if progress != nil {
				progress(done, total)
			}
			resMu.Unlock()
		}(i, d.Name, drv)
	}
	wg.Wait()

	var (
		evidence []Evidence
		devices  = make([]string, 0, len(results))
		problems []string
	)
	for _, r := range results {
		// problems 先于 name 空检组装：无驱动设备的 "no driver" 记录不能丢
		// （F12 改写引入的回归，轮3测试补齐时发现）。
		problems = append(problems, r.problems...)
		if r.name == "" {
			continue
		}
		devices = append(devices, r.name)
		evidence = append(evidence, r.evidence...)
	}

	severity := SeverityInfo
	if len(problems) > 0 {
		severity = SeverityWarning
	}
	f := &Finding{
		Title:    fmt.Sprintf("巡检 %d 台设备，%d 项异常", len(devices), len(problems)),
		Severity: severity,
		Devices:  devices,
		Detail: fmt.Sprintf("battery ran %s–%s over %d devices; %d evidence items collected",
			start.Format("15:04:05"), time.Now().Format("15:04:05"), len(devices), len(evidence)),
		Evidence: evidence,
		Source:   "inspect:summary", // 单条滚动：历史在巡检日志，发现队列只留一张活卡
	}
	if len(problems) > 0 {
		f.Detail += "; problems: " + joinAll(problems, "; ")
	}
	if err := SaveRollingFinding(f); err != nil {
		return nil, err
	}
	// R1 journal（best-effort）：巡检一行汇总 + 电池里 interface-brief 输出的
	// 行数启发式接口计数（趋势输入，不是逐口台账）。失败不影响巡检结果。
	crit, warn, info := OpenFindingTallies()
	ifBrief := map[string]IfBriefCounts{}
	for _, ev := range evidence {
		if ev.Device == "" || ev.Output == "" {
			continue
		}
		cl := strings.ToLower(ev.Command)
		if strings.Contains(cl, "interface brief") || strings.Contains(cl, "interfaces status") {
			c := SummarizeIfBrief(ev.Output)
			prev := ifBrief[ev.Device]
			ifBrief[ev.Device] = IfBriefCounts{Up: prev.Up + c.Up, Down: prev.Down + c.Down}
		}
	}
	_ = AppendInspectionRow(InspectionJournalRow{
		Kind: "inspection", Devices: len(devices), Checked: len(devices),
		Critical: crit, Warning: warn, Info: info, IfBrief: ifBrief,
	})
	return f, nil
}

// inspectionBattery is the per-driver read battery (all display/show-prefixed,
// so the classifier passes them).
func inspectionBattery(drv driver.Driver) []string {
	switch drv.Key() {
	case "huawei-vrp":
		return []string{
			"display version",
			"display cpu-usage",
			"display memory-usage",
			"display interface brief",
		}
	case "cisco-ios":
		return []string{
			"show version",
			"show processes cpu sorted | exclude 0.00",
			"show interfaces status",
		}
	case "zte-zxr10":
		return []string{
			"show version",
			"show processor cpu",
			"show interface brief",
		}
	default:
		return []string{"show version"}
	}
}

func joinAll(items []string, sep string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}
