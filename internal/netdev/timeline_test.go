package netdev

import (
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

// ExpectedState 的采集面豁免语义（逐行精读 R2）：
//   - 带 SNMP 块的非 linux GPU 设备失联 → Missing（有探针，探针说了算）；
//   - 无采集面的非 linux GPU 设备 → NoProbe（占位卡不算失联）且不重复；
//   - linux GPU 设备由 GPU 通道承载，不可达即 Missing。
func gpuTimelineManager(t *testing.T, devices []config.NetDevDevice) *Manager {
	t.Helper()
	findingsDirOverr = t.TempDir()
	t.Cleanup(func() { findingsDirOverr = "" })
	SetAuditPath(t.TempDir() + "/audit.jsonl")
	t.Cleanup(func() { SetAuditPath("") })
	cfg := config.Default()
	cfg.NetDev.Devices = devices
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	return m
}

func TestExpectedStateCollectorExemptions(t *testing.T) {
	m := gpuTimelineManager(t, []config.NetDevDevice{
		{Name: "win-gpu-snmp", Vendor: "windows", GPU: true,
			SNMP: &config.NetDevSNMP{Version: "v2c", CommunityEnv: "X"}},
		{Name: "win-gpu-bare", Vendor: "windows", GPU: true},
		{Name: "linux-gpu", Vendor: "linux", GPU: true},
		{Name: "plain-sw", Vendor: "huawei", OS: "vrp8", Address: "10.0.0.9"},
	})
	// 预置健康态：SNMP 设备不可达、linux GPU 可达。
	healthMu.Lock()
	healthState["win-gpu-snmp"] = DeviceHealth{Device: "win-gpu-snmp", Interfaces: []IfHealth{}}
	healthState["linux-gpu"] = DeviceHealth{Device: "linux-gpu", Reachable: true, GPUSampled: true, GPUOnly: true, Interfaces: []IfHealth{}}
	healthMu.Unlock()
	t.Cleanup(func() {
		healthMu.Lock()
		healthState = map[string]DeviceHealth{}
		healthMu.Unlock()
	})

	v := m.ExpectedState()
	if len(v.Missing) != 1 || v.Missing[0].Device != "win-gpu-snmp" {
		t.Errorf("SNMP-backed unreachable device must be Missing, got %+v", v.Missing)
	}
	found := map[string]bool{}
	for _, n := range v.NoProbe {
		found[n] = true
	}
	if !found["win-gpu-bare"] {
		t.Errorf("bare non-linux GPU (no collector) belongs in NoProbe, got %v", v.NoProbe)
	}
	if found["linux-gpu"] {
		t.Errorf("reachable linux GPU must not be NoProbe, got %v", v.NoProbe)
	}
	if found["win-gpu-snmp"] {
		t.Errorf("SNMP-backed device must never be NoProbe, got %v", v.NoProbe)
	}
	if v.Reachable != 1 {
		t.Errorf("reachable want 1, got %d", v.Reachable)
	}
}

// sysUpTime PDU 解码（逐行精读 R2 P1-1）：gosnmp v1.38 把 TimeTicks 解码为
// uint32——此前 switch 只接 uint/uint64，UptimeSec 恒 0、uptime_reset 永哑。
func TestUptimeSecondsFromPDU(t *testing.T) {
	if got := uptimeSecondsFromPDU(uint32(123456)); got != 1234 {
		t.Errorf("uint32 TimeTicks want 1234s, got %d", got)
	}
	if got := uptimeSecondsFromPDU(uint64(200)); got != 2 {
		t.Errorf("uint64 want 2s, got %d", got)
	}
	if got := uptimeSecondsFromPDU("garbage"); got != 0 {
		t.Errorf("non-numeric want 0, got %d", got)
	}
}
