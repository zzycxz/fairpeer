package netdev

// alert.go — the alert-rule engine (P2): threshold rules over the SNMP health
// snapshot evaluated on every poll. A firing rule creates ONE active Finding
// (deduped by source); when the condition clears, the same Finding flips to
// resolved. Human/AI findings are untouched (empty source/status).

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

// ruleCmp evaluates value op threshold. float64 since the GPU metrics are
// decimal watermarks (mem_pct); TOML integer literals decode into the float
// config field unchanged.
func ruleCmp(value, threshold float64, op string) bool {
	if op == "" {
		op = ">="
	}
	switch op {
	case ">=":
		return value >= threshold
	case "<=":
		return value <= threshold
	case "==":
		return value == threshold
	}
	return false
}

// ruleMetricValue extracts a rule's metric from one device's health. GPU
// metrics (gpu.*) read the gpuhealth.go segment — always 0 for devices the
// collector never sampled (evaluateAlerts skips those rules entirely, so a
// non-GPU fleet cannot fire GPU rules).
func ruleMetricValue(metric string, h DeviceHealth, prevUptime int64) float64 {
	switch metric {
	case "reachable":
		if h.Reachable {
			return 1
		}
		return 0
	case "if_down_count":
		return float64(h.IfDown())
	case "flap_count":
		// Reachability up↔down transitions in the last hour — a flapping
		// device answers most polls yet is clearly unhealthy.
		// 口径（D3 裁决）：双通道主机的 SNMP 面抖动（GPU 通道在线）也会计入
		// flap——保守取向，"面不稳"本身值得立案；豁免需可信的面归属判定，
		// 留待 dogfooding 反馈。
		return float64(FlapCount(h.Device, time.Hour))
	case "if_down_above_p90":
		// Current down interfaces minus the historical 90th percentile:
		// "worse than usual" instead of a static threshold. Thin history →
		// no baseline → 0, never fires.
		p90, ok := P90IfDown(h.Device, 24*time.Hour)
		if !ok {
			return 0
		}
		v := float64(h.IfDown() - p90)
		if v < 0 {
			return 0
		}
		return v
	case "uptime_reset":
		// A reboot: uptime dropped since the previous poll (and the device
		// is still up). prevUptime 0 = no baseline yet → never fire.
		if prevUptime > 0 && h.Reachable && h.UptimeSec > 0 && h.UptimeSec < prevUptime {
			return 1
		}
		return 0
	case "gpu.xid":
		// Max XID code this round (0 = none) — a TOML rule like gpu.xid >= 79
		// can raise only the severe codes to critical; the collector always
		// files its own XID finding at catalog severity.
		return float64(h.GPUXIDMax)
	case "gpu.temp":
		max := 0
		for _, c := range h.GPU {
			if c.TempC > max {
				max = c.TempC
			}
		}
		return float64(max)
	case "gpu.mem_pct":
		max := 0
		for _, c := range h.GPU {
			if p := c.MemPct(); p > max {
				max = p
			}
		}
		return float64(max)
	case "gpu.count":
		// Visible cards — the掉卡 metric: gpu.count <= N fires when a card
		// (or nvidia-smi itself) disappears. Only meaningful on sampled hosts.
		return float64(len(h.GPU))
	}
	return 0
}

// prevUptimes carries the previous poll's uptime per device (the uptime_reset
// baseline) across polls. Guarded by prevUptimesMu — same discipline as
// alertStreaks (逐行精读 R3 P3-3：评估入口是导出方法，不能靠单 goroutine 约定).
var (
	prevUptimesMu sync.Mutex
	prevUptimes   = map[string]int64{}
)

func prevUptime(name string) int64 {
	prevUptimesMu.Lock()
	defer prevUptimesMu.Unlock()
	return prevUptimes[name]
}

// setPrevUptime records the baseline; sec<=0（采集失败轮）不覆写——
// "没看到"不能清掉"已看到的基线"（重启漏检防线，逐行精读 R1 P3-3）。
func setPrevUptime(name string, sec int64) {
	if sec <= 0 {
		return
	}
	prevUptimesMu.Lock()
	defer prevUptimesMu.Unlock()
	prevUptimes[name] = sec
}

// prunePrevUptimes drops keys for devices no longer in the fresh sweep —
// 设备删光后不残留（与 alertStreaksPrune 对称）.
func prunePrevUptimes(fresh map[string]DeviceHealth) {
	prevUptimesMu.Lock()
	defer prevUptimesMu.Unlock()
	for name := range prevUptimes {
		if _, ok := fresh[name]; !ok {
			delete(prevUptimes, name)
		}
	}
}

// alertStreaks carries per source-key consecutive-fire counts across polls —
// the for_rounds debounce ("温度 ≥85 连续 3 轮" instead of single-poll fire).
// evaluateAlerts runs on the singleton poller goroutine; the mutex exists for
// tests running evaluations directly.
var (
	alertStreaksMu sync.Mutex
	alertStreaks   = map[string]int{}
)

func alertStreak(src string) int {
	alertStreaksMu.Lock()
	defer alertStreaksMu.Unlock()
	return alertStreaks[src]
}

func setAlertStreak(src string, n int) {
	alertStreaksMu.Lock()
	defer alertStreaksMu.Unlock()
	if n <= 0 {
		delete(alertStreaks, src)
		return
	}
	alertStreaks[src] = n
}

// evaluateAlerts runs every enabled rule against the fresh poll results and
// updates the uptime baseline. Called at the end of PollHealthOnce. for_rounds
// semantics: a rule fires only after the condition held for N consecutive
// polls (0/1 = immediately); one poll of clear resets the streak and resolves
// the active finding (unchanged lifecycle).
func (m *Manager) evaluateAlerts(fresh map[string]DeviceHealth) {
	rules := m.cfg.NetDev.AlertRules
	if len(rules) == 0 {
		for name, h := range fresh {
			setPrevUptime(name, h.UptimeSec)
		}
		return
	}
	// findings 目录读失败时跳过本轮评估（只更新基线）——否则 active 视图为
	// 空会让条件仍成立的规则重复立案，同 source 开出多张卡。
	active, err := m.activeFindingsBySource()
	if err != nil {
		for name, h := range fresh {
			setPrevUptime(name, h.UptimeSec)
		}
		return
	}
	seen := map[string]bool{} // 本轮仍有效的 streak key——轮末惰性清理（删规则后同名重建不得继承旧轮次）
	for _, r := range rules {
		if !r.Enabled {
			// 禁用的规则不再评估，但其 active finding 必须走恢复路径——
			// 否则一条被关掉的规则留下一张永远无法自动消除的卡。
			// 用 active map 里现成的 ID 走 ResolveFindingByID——避免每设备
			// 全量重扫 findings 目录。
			for name := range fresh {
				src := "alert:" + r.Name + ":" + name
				if id, isActive := active[src]; isActive {
					_ = ResolveFindingByID(id)
					delete(active, src)
				}
				setAlertStreak(src, 0)
			}
			continue
		}
		isGPU := strings.HasPrefix(r.Metric, "gpu.")
		for name, h := range fresh {
			src := "alert:" + r.Name + ":" + name
			if isGPU && !h.GPUSampled {
				// 采集器从未应答的设备不参与 GPU 规则（非 GPU 舰队零误报）。
				// streak 冻结不清零——"连续"以采样为准，一次 SSH 抖动不应
				// 把攒了 N-1 轮的防抖清空。
				seen[src] = true
				continue
			}
			if !isGPU && h.GPUOnly && !h.GPUSampled {
				// GPU-only 主机采集失败（GPUSampled=false ⇒ Reachable=false）：
				// "我方看不了" ≠ "设备掉了"——SNMP 形状规则同样冻结，
				// 成因留在设备卡的 GPULastError。
				seen[src] = true
				continue
			}
			seen[src] = true
			v := ruleMetricValue(r.Metric, h, prevUptime(name))
			fired := ruleCmp(v, r.Value, r.Op)
			need := r.ForRounds
			if need < 1 {
				need = 1
			}
			streak := 0
			if fired {
				streak = alertStreak(src) + 1
				setAlertStreak(src, streak)
			} else {
				setAlertStreak(src, 0)
			}
			_, hasActive := active[src]
			switch {
			case fired && streak >= need && !hasActive:
				m.fireAlertFinding(src, r, name, h, v)
			case !fired && hasActive:
				m.resolveFindingBySource(src)
				delete(active, src)
			}
		}
	}
	alertStreaksPrune(seen)
	// 基线只在采到正 uptime 时更新：失败轮（uptime=0）覆写基线会让随后的
	// 真实重启因 prevUptime==0 永久漏检（与 streak 冻结同一哲学）。
	// 键随 fresh 淘汰——设备删光后不残留（与 streaksPrune 对称）。
	for name, h := range fresh {
		setPrevUptime(name, h.UptimeSec)
	}
	prunePrevUptimes(fresh)
	// 离场清理：active 的 alert:* 键若其 (rule, device) 已不在本轮任何评估
	// 集合中（规则删除/设备移出清单），遗留卡自动恢复——与禁用规则同哲学。
	// 其余前缀（gpu:xid:、syslog:、triage:…）有自己的生命周期，不在此触碰。
	for src, id := range active {
		if strings.HasPrefix(src, "alert:") && !seen[src] {
			_ = ResolveFindingByID(id)
		}
	}
}

// alertStreaksPrune drops streak keys that no current (rule, device) pair
// produced this round — renamed/deleted rules must not lend their accumulated
// rounds to a re-created namesake's debounce (). Keys whose pair
// still exists but was skipped this round (unsampled GPU hosts) are kept:
// their streak freezes rather than breaks.
func alertStreaksPrune(seen map[string]bool) {
	alertStreaksMu.Lock()
	defer alertStreaksMu.Unlock()
	for k := range alertStreaks {
		if !seen[k] {
			delete(alertStreaks, k)
		}
	}
}

func (m *Manager) fireAlertFinding(src string, r config.NetDevAlertRule, device string, h DeviceHealth, v float64) {
	sev := r.Severity
	if sev == "" {
		sev = SeverityWarning
	}
	detail := fmt.Sprintf("规则「%s」：%s %s %g（当前值 %g）。", r.Name, r.Metric, opLabel(r.Op), r.Value, v)
	// 证据行按通道标注：GPU-only 主机没有 SNMP 段，"SNMP 健康轮询" 会误导
	// ；GPULastError 一并带上——它是采集成因的唯一记录点。
	label := "健康轮询"
	if h.GPUOnly {
		label = "GPU 健康轮询"
	}
	ev := Evidence{Device: device, Command: label, Output: fmt.Sprintf("reachable=%v uptime=%ds ifUp=%d ifDown=%d lastError=%s gpuLastError=%q",
		h.Reachable, h.UptimeSec, h.IfUp(), h.IfDown(), h.LastError, h.GPULastError)}
	// GPU 指标规则：SNMP 形状的证据行对 GPU-only 主机是噪声——补一行 GPU 摘要
	// 摘要行：卡数/最高温/最大 XID 才是定位时要看的东西。
	if strings.HasPrefix(r.Metric, "gpu.") {
		ev.Output += fmt.Sprintf("\nGPU: cards=%d xidMax=%d gpuLastError=%q", len(h.GPU), h.GPUXIDMax, h.GPULastError)
		for _, c := range h.GPU {
			ev.Output += fmt.Sprintf("\n  card %d %s temp=%dC mem=%d/%dMB (%d%%) util=%d%%",
				c.Index, c.Name, c.TempC, c.MemUsedMB, c.MemTotalMB, c.MemPct(), c.UtilPct)
		}
	}
	f := &Finding{
		Title:    fmt.Sprintf("[告警] %s @ %s", ruleTitle(r.Metric), device),
		Severity: sev,
		Devices:  []string{device},
		Detail:   detail,
		Evidence: []Evidence{ev},
		Source:   src,
		Status:   "active",
	}
	_ = SaveFinding(f)
	notifyHealth(h) // nudge the UI (findings + health cards refresh)
}

func opLabel(op string) string {
	if op == "" {
		return ">="
	}
	return op
}

func ruleTitle(metric string) string {
	switch metric {
	case "reachable":
		return "设备不可达"
	case "if_down_count":
		return "接口掉线"
	case "flap_count":
		return "链路抖动（一小时翻转）"
	case "if_down_above_p90":
		return "掉线口数偏离基线（>P90）"
	case "uptime_reset":
		return "设备重启（uptime 回绕）"
	case "gpu.xid":
		return "GPU XID 硬件事件"
	case "gpu.temp":
		return "GPU 温度水位"
	case "gpu.mem_pct":
		return "GPU 显存水位"
	case "gpu.count":
		return "GPU 可见卡数异常（掉卡）"
	}
	return metric
}

// activeFindingsBySource lists auto-findings still active, keyed by source.
func (m *Manager) activeFindingsBySource() (map[string]string, error) {
	fs, err := ListFindings()
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, f := range fs {
		if f.Source != "" && f.Status != "resolved" {
			out[f.Source] = f.ID
		}
	}
	return out, nil
}

// ResolveFindingByID manually resolves one finding (the 发现 card's button).
func ResolveFindingByID(id string) error {
	fs, err := ListFindings()
	if err != nil {
		return err
	}
	for _, f := range fs {
		if f.ID == id && f.Status != "resolved" {
			now := time.Now()
			f.Status = "resolved"
			f.ResolvedAt = &now
			StateEventSnap(StateEventFindResolve, id, StateActorUser, filepath.Join(FindingsDir(), id+".json"))
			return SaveFinding(f)
		}
	}
	return nil
}

// resolveFindingBySource rewrites one auto-finding as resolved.
func (m *Manager) resolveFindingBySource(src string) {
	fs, err := ListFindings()
	if err != nil {
		return
	}
	for _, f := range fs {
		if f.Source == src && f.Status != "resolved" {
			now := time.Now()
			f.Status = "resolved"
			f.ResolvedAt = &now
			if f.Detail != "" && !strings.Contains(f.Detail, "已恢复") {
				f.Detail += "（条件已清除，自动恢复）"
			}
			_ = SaveFinding(f)
			return
		}
	}
}
