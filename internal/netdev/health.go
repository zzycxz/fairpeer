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
	Device    string          `json:"device"`
	Time      time.Time       `json:"time"`
	Reachable bool            `json:"reachable"`
	UptimeSec int64           `json:"uptimeSec"`
	Interfaces []IfHealth     `json:"interfaces"`
	LastError string          `json:"lastError,omitempty"`
}

// IfHealth is one interface row (ifDescr/ifAdminStatus/ifOperStatus).
type IfHealth struct {
	Name    string `json:"name"`
	AdminUp bool   `json:"adminUp"`
	OperUp  bool   `json:"operUp"`
}

// UpCount/DownCount helpers for the UI (oper-down among admin-up = real alarms).
func (h DeviceHealth) IfUp() int    { n := 0; for _, i := range h.Interfaces { if i.OperUp { n++ } }; return n }
func (h DeviceHealth) IfDown() int  { n := 0; for _, i := range h.Interfaces { if i.AdminUp && !i.OperUp { n++ } }; return n }

// HealthSnapshot is the whole fleet's latest state.
type HealthSnapshot struct {
	PollIntervalSeconds int           `json:"pollIntervalSeconds"`
	Devices             []DeviceHealth `json:"devices"`
}

var (
	healthMu      sync.Mutex
	healthState   = map[string]DeviceHealth{}
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
	out := HealthSnapshot{PollIntervalSeconds: m.cfg.NetDev.PollIntervalSeconds}
	for _, d := range m.cfg.NetDev.Devices {
		if d.SNMP == nil {
			continue
		}
		if h, ok := healthState[d.Name]; ok {
			out.Devices = append(out.Devices, h)
		} else {
			out.Devices = append(out.Devices, DeviceHealth{Device: d.Name, LastError: "尚未轮询"})
		}
	}
	return out
}

// PollHealthOnce sweeps every SNMP-configured device once, then evaluates the
// alert rules over the fresh results.
func (m *Manager) PollHealthOnce(ctx context.Context) {
	fresh := map[string]DeviceHealth{}
	var freshMu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range m.cfg.NetDev.Devices {
		if d.SNMP == nil {
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			h := m.pollDeviceHealth(ctx, name)
			healthMu.Lock()
			prev, had := healthState[name]
			healthState[name] = h
			healthMu.Unlock()
			freshMu.Lock()
			fresh[name] = h
			freshMu.Unlock()
			if !had || prev.Reachable != h.Reachable || prev.IfDown() != h.IfDown() {
				notifyHealth(h)
				recordHealthSeries(h)
			}
		}(d.Name)
	}
	wg.Wait()
	healthMu.Lock()
	healthLastPoll = time.Now()
	healthMu.Unlock()
	m.evaluateAlerts(fresh)
}

// pollDeviceHealth runs one device's MIB-2 battery: sysUpTime + the ifTable
// (ifDescr / ifAdminStatus / ifOperStatus). Bounded to snmpMaxVars rows.
func (m *Manager) pollDeviceHealth(ctx context.Context, deviceName string) DeviceHealth {
	h := DeviceHealth{Device: deviceName, Time: time.Now().UTC()}
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
		switch v := res.Variables[0].Value.(type) {
		case uint:
			h.UptimeSec = int64(v) / 100
		case uint64:
			h.UptimeSec = int64(v) / 100
		}
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
		if s, ok := p.Value.(string); ok {
			descr[oid] = s
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
	if len(h.Interfaces) == 0 && h.UptimeSec == 0 {
		h.LastError = "no SNMP response"
		return h
	}
	h.Reachable = true
	return h
}
