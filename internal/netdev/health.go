package netdev

// health.go — the SNMP health sweep (D-batch's foundation): every device
// carrying an [netdev.devices.*.snmp] block is polled on [netdev].
// poll_interval_seconds for reachability, uptime, and interface admin/oper
// status. Results land in a snapshot (App.NetDevHealthSnapshot → the 健康 dock
// tab) and change-notifications stream as "netdev:health" events. Counter-class
// MIB-2 OIDs only — the same allowlist doctrine as SnmpQuery.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
)

// DeviceHealth is one device's latest poll result.
type DeviceHealth struct {
	Device     string     `json:"device"`
	Time       time.Time  `json:"time"`
	Reachable  bool       `json:"reachable"`
	UptimeSec  int64      `json:"uptimeSec"`
	Interfaces []IfHealth `json:"interfaces"`
	LastError  string     `json:"lastError,omitempty"`
	// 水位（SNMP OID 扩展）：0 = 未采集到（厂商 OID 不适用或设备不答）。
	CpuPct int `json:"cpuPct,omitempty"`
	MemPct int `json:"memPct,omitempty"`
	// 接口 octets 计数器累计和（读取端差分成 bps；0 = 未采集）。
	InOct  uint64 `json:"inOct,omitempty"`
	OutOct uint64 `json:"outOct,omitempty"`
	// GPU 段（FDE_AIINFRA gap §4.1，gpuhealth.go 采集）：GPU=true 主机经
	// SSH 只读轮询 nvidia-smi 的结果。GPUSampled=true 表示采集器真正拿到过
	// 设备侧应答——gpu.* 告警规则只对采样过的设备生效（未配置的主机不得
	// 因此告警）。
	GPU            []GPUCard `json:"gpu,omitempty"`
	GPUOnly        bool      `json:"gpuOnly,omitempty"` // 该主机没有 SNMP 块，健康面完全由 GPU 通道承载
	GPUSampled     bool      `json:"gpuSampled,omitempty"`
	GPUXIDSeen     bool      `json:"gpuXidSeen,omitempty"`     // 本轮至少一个 XID 证据源成功执行——XID 生命周期的 resolve 闸
	GPUXIDMax      int       `json:"gpuXidMax,omitempty"`      // 本轮最大 XID 代码（0 = 无）
	GPUXIDCodes    []int     `json:"gpuXidCodes,omitempty"`    // 本轮全部去重代码
	GPUXIDEvidence []string  `json:"gpuXidEvidence,omitempty"` // 命中行（已脱敏，封顶）
	GPUXIDSource   string    `json:"gpuXidSource,omitempty"`   // 证据命令（journalctl/-q）
	// GPUHealthAbnormal（M-1 轮2）：昇腾卡 health 非 OK 但无十进制码值——
	// gpuXidFinding 据此 hold（不 resolve 在案 finding），码值通道不含哨兵。
	GPUHealthAbnormal bool   `json:"gpuHealthAbnormal,omitempty"`
	GPULastError      string `json:"gpuLastError,omitempty"` // GPU 采集失败原因（不影响 SNMP 段）
}

// IfHealth is one interface row (ifDescr/ifAdminStatus/ifOperStatus).
type IfHealth struct {
	Name    string `json:"name"`
	AdminUp bool   `json:"adminUp"`
	OperUp  bool   `json:"operUp"`
}

// UpCount/DownCount helpers for the UI (oper-down among admin-up = real alarms).
func (h DeviceHealth) IfUp() int {
	n := 0
	for _, i := range h.Interfaces {
		if i.OperUp {
			n++
		}
	}
	return n
}
func (h DeviceHealth) IfDown() int {
	n := 0
	for _, i := range h.Interfaces {
		if i.AdminUp && !i.OperUp {
			n++
		}
	}
	return n
}

// HealthSnapshot is the whole fleet's latest state.
type HealthSnapshot struct {
	PollIntervalSeconds int            `json:"pollIntervalSeconds"`
	Devices             []DeviceHealth `json:"devices"`
}

var (
	healthMu       sync.Mutex
	healthState    = map[string]DeviceHealth{}
	healthLastPoll time.Time
	healthPollOnce sync.Once
)

// SetHealthObserver installs the change callback (desktop forwards it as the
// "netdev:health" Wails event). Called once at startup.
var healthObserver func(DeviceHealth)
var healthObserverMu sync.Mutex

func SetHealthObserver(fn func(DeviceHealth)) {
	healthObserverMu.Lock()
	healthObserver = fn
	healthObserverMu.Unlock()
}

// recordHealthSeries feeds the timeline store (§5.3) from each poll.
func recordHealthSeries(h DeviceHealth) {
	v := 0.0
	if h.Reachable {
		v = 1
	}
	RecordSeries(h.Device, "reachable", v)
	RecordSeries(h.Device, "if_down", float64(h.IfDown()))
	if h.UptimeSec > 0 {
		RecordSeries(h.Device, "uptime", float64(h.UptimeSec))
	}
}

func notifyHealth(h DeviceHealth) {
	healthObserverMu.Lock()
	fn := healthObserver
	healthObserverMu.Unlock()
	if fn != nil {
		fn(h)
	}
}

// EnsureHealthPoller starts the singleton sweep loop (idempotent); it reads
// the interval from the Manager's live config each tick, so a settings change
// to poll_interval_seconds applies within one interval without a restart.
func (m *Manager) EnsureHealthPoller() {
	healthPollOnce.Do(func() {
		// 时序面 14 天滚动清理（series.go）：曾是无接线死代码。CleanupSeriesOnce
		// 保证进程内恰好一次；PollHealthOnce 也会触发（覆盖不经过本函数的
		// headless 调用方）。
		CleanupSeriesOnce()
		go func() {
			for {
				iv := m.cfg.NetDev.PollIntervalSeconds
				if iv <= 0 {
					time.Sleep(15 * time.Second)
					continue
				}
				m.PollHealthOnce(context.Background())
				d := time.Duration(iv) * time.Second
				if d < 10*time.Second {
					d = 10 * time.Second
				}
				time.Sleep(d)
			}
		}()
	})
}

// HealthSnapshot returns the fleet's latest state.
func (m *Manager) HealthSnapshot() HealthSnapshot {
	healthMu.Lock()
	defer healthMu.Unlock()
	out := HealthSnapshot{PollIntervalSeconds: m.cfg.NetDev.PollIntervalSeconds, Devices: []DeviceHealth{}}
	for _, d := range m.cfg.NetDev.Devices {
		// GPU 主机没有 SNMP 块也进健康面（gpuhealth.go 采集通道）。
		if d.SNMP == nil && !d.GPU {
			continue
		}
		if h, ok := healthState[d.Name]; ok {
			out.Devices = append(out.Devices, h)
		} else if d.SNMP == nil && d.GPU && d.Vendor != "linux" {
			// 非 linux 的 GPU 设备采集器尚未接入（盲点 #16）——占位卡注明，
			// 不显示为"尚未轮询"这种看似可用的灰卡。
			out.Devices = append(out.Devices, DeviceHealth{Device: d.Name, LastError: "该 vendor 的 GPU 采集未接入（暂支持 linux）", Interfaces: []IfHealth{}})
		} else {
			out.Devices = append(out.Devices, DeviceHealth{Device: d.Name, LastError: "尚未轮询", Interfaces: []IfHealth{}})
		}
	}
	return out
}

// healthPollConcurrency bounds the per-poll fan-out: each in-flight SNMP poll
// holds a UDP socket, and servers commonly run with `ulimit -n 1024` — an
// unbounded fleet sweep exhausts descriptors (EMFILE) long before the sweep
// finishes.
const healthPollConcurrency = 64

// PollHealthOnce sweeps every SNMP-configured device once, then evaluates the
// alert rules over the fresh results.
func (m *Manager) PollHealthOnce(ctx context.Context) {
	// headless/CLI 调用方不经过 EnsureHealthPoller——清理在这里兜底触发
	// （进程内恰好一次，见 CleanupSeriesOnce）。
	CleanupSeriesOnce()
	fresh := map[string]DeviceHealth{}
	var freshMu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, healthPollConcurrency)
	for _, d := range m.cfg.NetDev.Devices {
		if d.SNMP == nil {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			h := m.pollDeviceHealth(ctx, name)
			healthMu.Lock()
			prev, had := healthState[name]
			healthState[name] = h
			healthMu.Unlock()
			freshMu.Lock()
			fresh[name] = h
			freshMu.Unlock()
			_ = RecordMetricPoint(name, MetricPoint{
				Time: h.Time, Reachable: h.Reachable, UptimeSec: h.UptimeSec,
				IfUp: h.IfUp(), IfDown: h.IfDown(),
				Cpu: h.CpuPct, Mem: h.MemPct, InOct: h.InOct, OutOct: h.OutOct,
			})
			if !had || prev.Reachable != h.Reachable || prev.IfDown() != h.IfDown() {
				notifyHealth(h)
				recordHealthSeries(h)
			}
		}(d.Name)
	}
	wg.Wait()
	// GPU 采集段（gpuhealth.go）：GPU=true 主机在 SNMP sweep 之后追加一轮
	// SSH 只读采集，结果并入 fresh——SNMP 规则与 gpu.* 规则一次评估。
	m.pollGPUDevices(ctx, fresh)
	// 推理指标段（infermetrics.go，批④）：登记了 metrics_ports 的主机直连
	// GET /metrics，K3 映射落 series infer.*——必须在 evaluateAlerts 之前，
	// infer.* 规则读的是本轮最新点。
	m.pollMetricsEndpoints(ctx)
	healthMu.Lock()
	healthLastPoll = time.Now()
	healthMu.Unlock()
	m.evaluateAlerts(fresh)
	// 观察期劣化检测（§7.1）：watching 变更的目标与 watch 起点基线对比。
	m.checkWatchingProposals(fresh)
}

// uptimeSecondsFromPDU converts a sysUpTime TimeTicks PDU value to seconds.
// gosnmp v1.38 decodes TimeTicks via parseUint32 → Value 的动态类型是
// uint32——此前的 switch 只接 uint/uint64，UptimeSec 恒 0，uptime_reset
// 告警永久哑火（逐行精读 R2 P1-1，全仓无该解码的测试所以一直存活）。
func uptimeSecondsFromPDU(v any) int64 {
	switch t := v.(type) {
	case uint32:
		return int64(t) / 100
	case uint:
		return int64(t) / 100
	case uint64:
		return int64(t) / 100
	case int:
		if t > 0 {
			return int64(t) / 100
		}
	}
	return 0
}

// pollDeviceHealth runs one device's MIB-2 battery: sysUpTime + the ifTable
// (ifDescr / ifAdminStatus / ifOperStatus). Bounded to snmpMaxVars rows.
func (m *Manager) pollDeviceHealth(ctx context.Context, deviceName string) DeviceHealth {
	// Interfaces stays non-nil on every path: a nil slice marshals to JSON
	// null and the frontend health table filters it unguarded.
	h := DeviceHealth{Device: deviceName, Time: time.Now().UTC(), Interfaces: []IfHealth{}}
	device, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		h.LastError = "not in inventory"
		return h
	}
	community := "public"
	if device.SNMP != nil && device.SNMP.CommunityEnv != "" {
		if v, ok2, _ := secretGetter(SecretKindPassword, device.SNMP.CommunityEnv); ok2 && v != "" {
			community = v
		}
	}
	port := device.Port
	if port == 0 || device.Vendor != "snmp" {
		port = 161 // health polls the SNMP port regardless of the SSH port
	}
	g := &gosnmp.GoSNMP{
		Target:    device.Address,
		Port:      uint16(port),
		Community: community,
		Version:   gosnmp.Version2c,
		Timeout:   3 * time.Second,
		Retries:   0,
		MaxOids:   gosnmp.MaxOids,
		Context:   ctx,
	}
	if err := g.Connect(); err != nil {
		h.LastError = err.Error()
		return h
	}
	defer g.Conn.Close()

	// sysUpTime: 1.3.6.1.2.1.1.3.0 (TimeTicks, hundredths of a second).
	if res, err := g.Get([]string{"1.3.6.1.2.1.1.3.0"}); err == nil && len(res.Variables) == 1 {
		h.UptimeSec = uptimeSecondsFromPDU(res.Variables[0].Value)
	}

	// ifTable columns: descr=2.2.1.2, admin=2.2.1.7, oper=2.2.1.8.
	descr := map[string]string{}
	admin := map[string]bool{}
	oper := map[string]bool{}
	collect := func(prefix string, into func(oid string, v gosnmp.SnmpPDU)) {
		_ = g.Walk(prefix, func(p gosnmp.SnmpPDU) error {
			if len(descr)+len(admin)+len(oper) >= snmpMaxVars*3 {
				return fmt.Errorf("bounded")
			}
			into(strings.TrimPrefix(p.Name, prefix), p)
			return nil
		})
	}
	collect("1.3.6.1.2.1.2.2.1.2", func(oid string, p gosnmp.SnmpPDU) {
		// gosnmp decodes OctetString to []byte (never string) — without the
		// []byte branch this assert never matched and Interfaces stayed empty.
		if b, ok := p.Value.([]byte); ok {
			if s := snmpOctetText(b); s != "" {
				descr[oid] = s
			}
		}
	})
	collect("1.3.6.1.2.1.2.2.1.7", func(oid string, p gosnmp.SnmpPDU) {
		admin[oid] = p.Value == 1
	})
	collect("1.3.6.1.2.1.2.2.1.8", func(oid string, p gosnmp.SnmpPDU) {
		oper[oid] = p.Value == 1
	})
	seen := map[string]bool{}
	for oid, name := range descr {
		if seen[oid] || name == "" {
			continue
		}
		seen[oid] = true
		h.Interfaces = append(h.Interfaces, IfHealth{Name: name, AdminUp: admin[oid], OperUp: oper[oid]})
	}
	// 水位（DASHBOARD spec §7.3）：cpu/mem 厂商 OID 单 GET + 接口 octets 求和
	// walk——都走已配置的 SNMP 只读通道，一次也不多连。
	h.CpuPct, h.MemPct, h.InOct, h.OutOct = pollWatermarks(g, device.Vendor)
	if len(h.Interfaces) == 0 && h.UptimeSec == 0 {
		h.LastError = "no SNMP response"
		return h
	}
	h.Reachable = true
	return h
}
