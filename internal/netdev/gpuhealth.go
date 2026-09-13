package netdev

// gpuhealth.go — GPU/智算主机采集通道（FDE_AIINFRA_OPS_GAP_SPEC §4.1-1/2/4，
// 范围提案：启动顺序仍以 GPU_TODO 的 dogfooding 痛点清单为准）。health 轮询
// 只覆盖带 [netdev.devices.*.snmp] 的设备（health.go 的 SNMP-only 过滤），
// GPU 主机通常没有 SNMP——这里是它们进入健康面/时序面/告警面的唯一通道。
//
// 采集 = 只读命令（全部在 linux 驱动读表白名单内，hosts.go），走
// execSealed(internal) 密封路径——分类/审计/脱敏/只读密封与 agent 同一套，
// 只跳过 per-turn 护栏（组作用域 + 轮预算是会话控制，后台轮询不占也不该被
// 占，）。
//
// XID → Finding（§4.1-4）：证据主源是内核日志 journalctl -k -g Xid（XID 的
// 正规出口；nvidia-smi -q 在多数真实驱动上不含 Xid 段，
// 真机校准列入 dogfooding 清单），失败落 -q 兜底。XID>0 立 Finding 必带
// 命令输出证据（脱敏后）；catalog 分级决定 severity（severe → critical：
// 隔离送修；其余 → warning：多为可恢复），active 期间出现更高级代码会升级
// （P1-6）；同节点多卡 XID 聚合为一条（防误换好卡）。采样失败的轮次既不
// 立案也不 resolve（P1-5：没采到 ≠ 清除了——与急停的 hold 哲学一致）。

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

// gpuQueryCmd 是与 triage 电池同列序的只读查询（nounits 保证每列都是裸数），
// noheader 省去表头行；读表前缀 "nvidia-smi --query" 已覆盖。
const gpuQueryCmd = "nvidia-smi --query-gpu=index,name,temperature.gpu,memory.used,memory.total,utilization.gpu --format=csv,nounits,noheader"

// gpuXidCmd 与 triage 的「GPU XID 事件」同一命令——作 -q 兜底证据源。
const gpuXidCmd = "nvidia-smi -q"

// gpuJournalXidCmd：内核日志 XID 主证据源（读表裸前缀 "journalctl" 已覆盖）。
// -g 需 systemd ≥237，旧系统上命令失败 → 落 gpuXidCmd 兜底。-n 50 有界。
const gpuJournalXidCmd = "journalctl -k -g Xid --no-pager -n 50"

// gpuPollConcurrencyDefault is the per-round GPU fan-out floor when the site
// config doesn't override [[netdev]] gpu_poll_concurrency（F12）。
const gpuPollConcurrencyDefault = 8

// gpuPollConcurrency resolves the configured fan-out (kept as a function so
// tests can drive both paths; the const default preserves pre-F12 behavior).
func (m *Manager) gpuPollConcurrency() int {
	if n := m.cfg.NetDev.GPUPollConcurrency; n > 0 {
		return n
	}
	return gpuPollConcurrencyDefault
}

// gpuPollPerDeviceTimeout bounds one host's whole battery (dial + 3 commands).
const gpuPollPerDeviceTimeout = 90 * time.Second

// GPUCard is one card's latest poll row.
type GPUCard struct {
	Index      int    `json:"index"`
	Name       string `json:"name,omitempty"`
	TempC      int    `json:"tempC,omitempty"`
	MemUsedMB  uint64 `json:"memUsedMB,omitempty"`
	MemTotalMB uint64 `json:"memTotalMB,omitempty"`
	UtilPct    int    `json:"utilPct,omitempty"`
	// ErrorCode/Kind 归一（M-1，ACCEL_SPEC §4.2）：XID ↔ 昇腾错误码 ↔ 各厂商
	// 错误码共用同两列。nvidia 卡留空（XID 走设备级 GPUXID* 通道）；
	// ErrorCodeKind 现值 "npu-health"（昇腾 health 列非 OK）。
	ErrorCode     int    `json:"errorCode,omitempty"`
	ErrorCodeKind string `json:"errorCodeKind,omitempty"`
}

// MemPct returns used/total as a 0-100 int (0 = total unknown).
func (c GPUCard) MemPct() int {
	if c.MemTotalMB == 0 {
		return 0
	}
	return int(c.MemUsedMB * 100 / c.MemTotalMB)
}

// gpuCSVNotes caps the per-round skip notes fed to GPULastError.
const gpuCSVNotes = 3

// parseGPUCSV parses the nounits,noheader CSV output. Positional on purpose:
// the query above fixes the column order. Tolerates stray spaces, CRLF and a
// header-ish first row. A card whose numeric fields fail to parse (N/A /
// "[Not Supported]" / unit suffix from old drivers) is SKIPPED and noted —
// silently writing 0 would mask a real watermark ().
func parseGPUCSV(out string) ([]GPUCard, []string) {
	var cards []GPUCard
	var notes []string
	note := func(format string, a ...any) {
		if len(notes) < gpuCSVNotes {
			notes = append(notes, fmt.Sprintf(format, a...))
		}
	}
	for _, ln := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		f := strings.Split(ln, ",")
		if len(f) < 6 || strings.EqualFold(strings.TrimSpace(f[0]), "index") {
			if !strings.EqualFold(strings.TrimSpace(f[0]), "index") {
				// 列数异常/全角逗号等格式漂移：静默丢弃会让采集面零信号，
				// 与"不静默掩盖"的立意相悖。
				note("跳过格式异常行（%d 列）：%s", len(f), firstLineOf(ln))
			}
			continue
		}
		idx, err := strconv.Atoi(strings.TrimSpace(f[0]))
		if err != nil {
			note("跳过无法解析的行：%s", firstLineOf(ln))
			continue
		}
		if idx < 0 {
			note("跳过负 index 卡 %d", idx)
			continue
		}
		temp, errT := strconv.Atoi(strings.TrimSpace(f[2]))
		used, errU := strconv.ParseUint(strings.TrimSpace(f[3]), 10, 64)
		total, errTot := strconv.ParseUint(strings.TrimSpace(f[4]), 10, 64)
		util, errUtil := strconv.Atoi(strings.TrimSpace(f[5]))
		if errT != nil || errU != nil || errTot != nil || errUtil != nil {
			note("跳过卡 %d：数值字段不可解析（N/A 或单位后缀）", idx)
			continue
		}
		cards = append(cards, GPUCard{
			Index: idx, Name: strings.TrimSpace(f[1]),
			TempC: temp, MemUsedMB: used, MemTotalMB: total, UtilPct: util,
		})
	}
	return cards, notes
}

// gpuXidLines bounds the evidence lines kept per round (防巨量输出灌爆 Finding)。
const gpuXidLines = 10

// 两条形态：标准（nvidia-smi -q 的 "Xid 79"）与 dmesg/journalctl 的
// "Xid (PCI:0000:04:00.0): 79"。：旧正则的 [^0-9]* 跨不过 PCI 段。
var (
	gpuXidRe     = regexp.MustCompile(`(?i)\bXid\s+(\d{1,4})\b`)
	gpuXidPCIRe  = regexp.MustCompile(`(?i)\bXid\s*\(PCI[^)]*\):\s*(\d{1,4})\b`)
	gpuXidSevere = map[int]bool{
		// NVIDIA XID catalog 精选硬件级代码：double-bit ECC (8, 48), 页退役
		// (63, 64), NVLink (74), fell off the bus (79), uncorrectable ECC /
		// 内部错误族 (80, 81, 92-95)。82-91 多为 reset/API 族（非硬件故障），
		// 未纳入——分级表随 dogfooding 对照 catalog 校准。
		8: true, 48: true, 63: true, 64: true, 74: true, 79: true,
		80: true, 81: true, 92: true, 93: true, 94: true, 95: true,
	}
)

// extractXIDs pulls unique Xid codes (both forms above, >0) with the evidence
// lines they came from (capped; output already redacted by the Exec path).
// FindAll：同一行可能并列多个事件（"Xid 43, Xid 79"）——只取首个会把 severe
// 静默降级（max 取错码）。
func extractXIDs(out string) (codes []int, lines []string) {
	seen := map[int]bool{}
	for _, ln := range strings.Split(out, "\n") {
		var rowCodes []int
		for _, re := range []*regexp.Regexp{gpuXidRe, gpuXidPCIRe} {
			for _, m := range re.FindAllStringSubmatch(ln, -1) {
				code, err := strconv.Atoi(m[1])
				if err != nil || code <= 0 {
					continue
				}
				rowCodes = append(rowCodes, code)
			}
			if len(rowCodes) > 0 {
				break // 该行已被本形态命中，不再尝试另一种（避免 PCI 行双计）
			}
		}
		if len(rowCodes) == 0 {
			continue
		}
		for _, code := range rowCodes {
			if !seen[code] {
				codes = append(codes, code)
				seen[code] = true
			}
		}
		if len(lines) < gpuXidLines {
			lines = append(lines, strings.TrimSpace(ln))
		}
	}
	sort.Ints(codes)
	return codes, lines
}

// pollGPUHealth runs one GPU host's read battery. GPUSampled is set iff the
// collector got device-side answers (transport reached the device) — alert
// rules over gpu.* metrics and the XID finding lifecycle only act on sampled
// rounds (a refused poll means WE couldn't look, not that the host healed).
// accelForDevice resolves the collection battery: explicit device accel wins;
// empty falls to nvidia (the surveyed default silicon) — 探测式缺省（按
// 命令存在性猜厂商）留真机校准，先不做猜测式执行。
func accelForDevice(d config.NetDevDevice) string {
	if d.Accel != "" {
		return d.Accel
	}
	return "nvidia"
}

func (m *Manager) pollGPUHealth(ctx context.Context, deviceName string) DeviceHealth {
	h := DeviceHealth{Device: deviceName, Time: time.Now().UTC(), Interfaces: []IfHealth{}}
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		h.LastError = "not in inventory"
		return h
	}
	// M-1：电池按 accel 维度分发——薄驱动归一到同一 DeviceHealth/GPUCard。
	if accelForDevice(d) == "ascend" {
		return m.pollAscendHealth(ctx, deviceName)
	}
	res := m.execSealed(ctx, deviceName, gpuQueryCmd, true)
	if res.Refused {
		// Refusal = 我方原因（读表/凭据/连接失败），不是设备故障——不置
		// GPUSampled：gpu.* 告警与 XID 生命周期全部静默，错误进设备卡。
		// Refusal 文案本身已带成因（guardrail/白名单/连接失败），不再另加
		// 前缀误导。
		h.GPULastError = firstLineOf(res.Refusal)
		return h
	}
	// 命令真正执行过 = 传输层可达（设备侧失败≠不可达——驱动挂掉不该伪装成
	// SSH 不通）。
	h.Reachable = true
	h.GPUSampled = true
	if res.IsError {
		// XID 证据面没跑：GPUXIDSeen 不置位——本轮对 XID 是"没看到"而非
		// "已清除"，gpuXidFinding 据此 hold（驱动临终恰是 XID 高发时刻，
		// 绝不能在这里自动消掉 critical 卡）。
		h.GPULastError = "nvidia-smi 失败：" + firstLineOf(res.Output)
		return h
	}
	cards, notes := parseGPUCSV(res.Output)
	h.GPU = cards
	for _, n := range notes {
		h.GPULastError = joinNote(h.GPULastError, n)
	}

	// XID 证据面：主源内核日志（命令成功即以其空/非空为准——-k 已限定内核
	// 日志，无输出就是无事件），失败落 -q 兜底（部分驱动版本带事件段）。
	// 两源皆败 → GPUXIDSeen=false → 本轮 XID hold。
	xidOut, xidSrc := "", ""
	j := m.execSealed(ctx, deviceName, gpuJournalXidCmd, true)
	if !j.Refused && !j.IsError {
		xidOut, xidSrc = j.Output, gpuJournalXidCmd
		h.GPUXIDSeen = true
	} else {
		h.GPULastError = joinNote(h.GPULastError, "内核日志 XID 源不可用，落 -q 兜底："+firstLineOf(refusalOrOutput(j)))
		q := m.execSealed(ctx, deviceName, gpuXidCmd, true)
		if !q.Refused && !q.IsError {
			xidOut, xidSrc = q.Output, gpuXidCmd
			h.GPUXIDSeen = true
		} else {
			h.GPULastError = joinNote(h.GPULastError, "XID 兜底源也不可用："+firstLineOf(refusalOrOutput(q)))
		}
	}
	if xidOut != "" {
		codes, lines := extractXIDs(xidOut)
		if len(codes) > 0 {
			h.GPUXIDMax = codes[len(codes)-1]
			h.GPUXIDCodes = codes
			h.GPUXIDEvidence = lines
			h.GPUXIDSource = xidSrc
		}
	}
	return h
}

// joinNote appends a note to the device's GPU error line (；-separated).
func joinNote(prev, note string) string {
	if strings.TrimSpace(note) == "" {
		return prev
	}
	if prev == "" {
		return note
	}
	return prev + "；" + note
}

// pollGPUDevices sweeps every GPU-flagged host concurrently and folds results
// into the fresh map BEFORE evaluateAlerts, so GPU rules and SNMP rules
// evaluate in one pass. Devices already SNMP-polled keep their SNMP segment
// (rare: a GPU host with an snmp block) — the GPU segment merges in.
func (m *Manager) pollGPUDevices(ctx context.Context, fresh map[string]DeviceHealth) {
	var targets []*config.NetDevDevice
	for i := range m.cfg.NetDev.Devices {
		d := &m.cfg.NetDev.Devices[i]
		// Windows GPU 主机随后补（盲点 #16）：nvidia-smi 同样可用，先走
		// linux 的读表与解析。
		if d.GPU && d.Vendor == "linux" {
			targets = append(targets, d)
		}
	}
	if len(targets) == 0 {
		return
	}
	var (
		wg      sync.WaitGroup
		freshMu sync.Mutex
		sem     = make(chan struct{}, m.gpuPollConcurrency())
	)
	for _, d := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			pc, cancel := context.WithTimeout(ctx, gpuPollPerDeviceTimeout)
			defer cancel()
			gh := m.pollGPUHealth(pc, name)
			h := gh
			freshMu.Lock()
			if base, ok := fresh[name]; ok {
				// SNMP 段已在：保留 SNMP 结果，并入 GPU 段；可达性取或——
				// 任一通道可达即设备可达。
				base.GPU, base.GPUXIDMax, base.GPUXIDCodes = gh.GPU, gh.GPUXIDMax, gh.GPUXIDCodes
				base.GPUXIDEvidence, base.GPUSampled = gh.GPUXIDEvidence, gh.GPUSampled
				base.GPUXIDSeen, base.GPUXIDSource = gh.GPUXIDSeen, gh.GPUXIDSource
				base.Reachable = base.Reachable || gh.Reachable
				// 无条件覆盖：GPU 轮成功后陈旧错误必须清掉（粘滞错误会让
				// 设备卡永远显示上一轮的失败成因）。
				base.GPULastError = gh.GPULastError
				h = base
			} else {
				// GPU-only 主机：健康面完全由本通道承载（告警冻结判据）。
				gh.GPUOnly = true
				h = gh
			}
			fresh[name] = h
			freshMu.Unlock()
			// 通知基线取上一轮【合并后】的值：SNMP sweep 每轮都会把
			// healthState 覆写成无 GPU 段的快照，若与它比较，双通道主机的
			// GPU 段每轮都"从 0 变 N"，"变化才通知"退化为每轮必发。
			healthMu.Lock()
			prev, had := gpuMergeState[name]
			healthState[name] = h
			gpuMergeState[name] = h
			healthMu.Unlock()
			if !had || gpuStructuralChanged(prev, h) {
				notifyHealth(h)
				recordHealthSeries(h)
			}
			recordGPUSeries(h)
			m.gpuXidFinding(name, h)
		}(d.Name)
	}
	wg.Wait()
}

// gpuMergeState is each GPU host's last MERGED health snapshot — the notify
// baseline. healthState is overwritten by the SNMP sweep every round (without
// the GPU segment), so comparing against it would fire "structural change" on
// every round for dual-channel hosts. Guarded by healthMu.
var gpuMergeState = map[string]DeviceHealth{}

// gpuStructuralChanged reports whether anything a human should be nudged
// about moved. Per-card numeric jitter (temp/util/mem) is deliberately
// excluded — it changes every round under load and would turn the "变化才
// 通知" design into firehose (); numerics reach the UI via the
// series sparklines instead.
func gpuStructuralChanged(a, b DeviceHealth) bool {
	// S-39: UptimeSec ticks every round, so comparing it here made dual-channel
	// hosts fire notifyHealth on EVERY poll — the "变化才通知" design was defeated
	// by a field that always changes. Real outages surface via Reachable/IfDown.
	if a.Reachable != b.Reachable || a.IfDown() != b.IfDown() {
		return true
	}
	if a.GPUXIDMax != b.GPUXIDMax || a.GPULastError != b.GPULastError || len(a.GPU) != len(b.GPU) {
		return true
	}
	for i := range a.GPU {
		if a.GPU[i].Index != b.GPU[i].Index || a.GPU[i].Name != b.GPU[i].Name {
			return true
		}
	}
	return false
}

// recordGPUSeries feeds the timeline every round (sparkline 需要全量历史，
// 与 reachable 的「变化才记」不同——量级：N 卡 × 4 指标 / 轮，可忽略）。
// 命名规范 gpu.<index>.<metric>（读取端按 metric 前缀自然分组）+ labels 并存
// （FDE_AIINFRA gap §4.1-2）。
func recordGPUSeries(h DeviceHealth) {
	if !h.GPUSampled {
		return
	}
	RecordSeries(h.Device, "gpu.count", float64(len(h.GPU)))
	RecordSeries(h.Device, "gpu.xid_max", float64(h.GPUXIDMax))
	for _, c := range h.GPU {
		idx := strconv.Itoa(c.Index)
		lbl := map[string]string{"gpu": idx}
		RecordSeriesLabeled(h.Device, "gpu."+idx+".temp", lbl, float64(c.TempC))
		RecordSeriesLabeled(h.Device, "gpu."+idx+".mem_used", lbl, float64(c.MemUsedMB))
		RecordSeriesLabeled(h.Device, "gpu."+idx+".mem_pct", lbl, float64(c.MemPct()))
		RecordSeriesLabeled(h.Device, "gpu."+idx+".util", lbl, float64(c.UtilPct))
	}
}

// gpuXidFinding keeps ONE active XID finding per device (source-deduped, same
// lifecycle as alert findings). 双闸：GPUSampled=false（传输面没通）与
// GPUXIDSeen=false（跑通了但 XID 证据源缺席/失败）的轮次既不立案也不
// resolve——采集失败不是事件清除；active 期间出现更高级代码则原卡升级
// （先 13 后 79 不能停在 warning）。
func (m *Manager) gpuXidFinding(deviceName string, h DeviceHealth) {
	if !h.GPUSampled || !h.GPUXIDSeen {
		return
	}
	src := "gpu:xid:" + deviceName
	if h.GPUXIDMax <= 0 {
		if h.GPUHealthAbnormal {
			// 昇腾"异常但无码"：hold——异常还在，不能自动消卡（轮2复核）。
			return
		}
		m.resolveFindingBySource(src)
		return
	}
	sev := SeverityWarning
	action := "多为可恢复事件（应用/驱动级）：确认受影响任务后重启应用或 GPU reset（写动作走提案）。"
	if gpuXidSevere[h.GPUXIDMax] {
		sev = SeverityCritical
		action = "硬件级错误：建议隔离该节点（cordon/drain 走提案）、收集 nvidia-bug-report 并进入送修流程；勿盲目 reset 后复用。"
	}
	affected := ""
	if len(h.GPU) > 1 {
		affected = fmt.Sprintf("（同节点 %d 卡聚合，避免多卡雪崩误判为多卡故障）", len(h.GPU))
	}
	detail := fmt.Sprintf("检测到 XID 事件，最大代码 %d%s；本轮全部代码：%v。\n%s",
		h.GPUXIDMax, affected, h.GPUXIDCodes, action)

	active, err := m.activeFindingsBySource()
	if err != nil {
		return
	}
	if id, isActive := active[src]; isActive {
		m.refreshXidFinding(id, h, sev, detail)
		return // already on the queue — no duplicate per round
	}
	f := &Finding{
		Title:      fmt.Sprintf("[GPU] XID 事件 #%d @ %s", h.GPUXIDMax, deviceName),
		Severity:   sev,
		Devices:    []string{deviceName},
		Detail:     detail,
		Evidence:   gpuXidEvidence(h),
		Suggestion: "用「一键诊断」跑该主机体检电池核对 ECC/温度；处置动作（隔离/reset）一律走提案，人按按钮。",
		Source:     src,
		Status:     "active", // 与告警引擎同款显式 active——SaveFinding 不落默认值
	}
	if err := SaveFinding(f); err == nil {
		notifyHealth(h) // nudge the UI
	}
}

// refreshXidFinding updates an existing active XID finding: warning → critical
// when the round's max code turns severe (the card's disposition advice must
// follow the worst observed event, not the first one), and evidence/detail
// refresh when the max code changed at all (先 13 后 43 的 warning 卡也不能
// 停在立案轮的旧证据上). Already-critical cards with an unchanged max code
// are left untouched — no per-round churn.
//
// Known narrow window (accepted): a manual resolve landing between
// ListFindings and SaveFinding is overwritten by this active copy; the next
// sampled round re-fires (XID persists) or resolves (it doesn't). Same
// read-modify-write shape as the pre-existing rolling/resolve helpers.
func (m *Manager) refreshXidFinding(id string, h DeviceHealth, sev, detail string) {
	marker := fmt.Sprintf("最大代码 %d", h.GPUXIDMax)
	fs, err := ListFindings()
	if err != nil {
		return
	}
	for _, f := range fs {
		if f.ID != id || f.Status == "resolved" {
			continue
		}
		if f.Severity == SeverityCritical && sev != SeverityCritical && strings.Contains(f.Detail, marker) {
			return // 已是 critical 且无新码：不重写
		}
		if strings.Contains(f.Detail, marker) && f.Severity == sev {
			return // 无变化：不重写（防每轮 churn）
		}
		if sev == SeverityCritical && f.Severity != SeverityCritical {
			f.Detail = detail + "\n（升级：本轮出现硬件级代码，原卡为 warning）"
		} else {
			f.Detail = detail
		}
		f.Severity = sev
		if ev := gpuXidEvidence(h); len(ev) > 0 {
			f.Evidence = ev
		}
		_ = SaveFinding(f)
		return
	}
}

// gpuXidEvidence wraps the XID lines as finding evidence (output already
// redacted by the Exec path — same doctrine as triage sections).
func gpuXidEvidence(h DeviceHealth) []Evidence {
	if len(h.GPUXIDEvidence) == 0 {
		return nil
	}
	cmd := h.GPUXIDSource
	if cmd == "" {
		cmd = gpuJournalXidCmd
	}
	return []Evidence{{Device: h.Device, Command: cmd, Output: strings.Join(h.GPUXIDEvidence, "\n")}}
}
