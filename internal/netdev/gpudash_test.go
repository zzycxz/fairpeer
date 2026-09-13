package netdev

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

func gpuDashItoa(n int64) string   { return strconv.FormatInt(n, 10) }
func gpuDashFtoa(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
func gpuDashQ(s string) string     { return "\"" + s + "\"" }

func gpuBoardManager(t *testing.T) *Manager {
	t.Helper()
	findingsDirOverr = t.TempDir()
	t.Cleanup(func() { findingsDirOverr = "" })
	SetAuditPath(t.TempDir() + "/audit.jsonl")
	t.Cleanup(func() { SetAuditPath("") })
	// HealthSnapshot 按 cfg 设备清单迭代——夹具设备必须同时在清单与
	// healthState 两边（GPU 标记决定是否进快照）。
	cfg := config.Default()
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "g1", Vendor: "linux", GPU: true},
		{Name: "g2", Vendor: "linux", GPU: true},
		{Name: "win-gpu", Vendor: "windows", GPU: true},
	}
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	return m
}

func TestBuildGPUBoard(t *testing.T) {
	m := gpuBoardManager(t)

	// 健康态：一台全卡在跑 + 一台采集失败 + 一台未接入占位。
	healthMu.Lock()
	healthState["g1"] = DeviceHealth{Device: "g1", Reachable: true, GPUSampled: true, GPUXIDMax: 79,
		GPU: []GPUCard{
			{Index: 0, Name: "A100", TempC: 61, UtilPct: 30, MemUsedMB: 1000, MemTotalMB: 40960},
			{Index: 1, Name: "A100", TempC: 88, UtilPct: 95, MemUsedMB: 38000, MemTotalMB: 40960},
		}, Interfaces: []IfHealth{}}
	healthState["g2"] = DeviceHealth{Device: "g2", GPUSampled: false, GPULastError: "connection refused", Interfaces: []IfHealth{}}
	healthState["win-gpu"] = DeviceHealth{Device: "win-gpu", LastError: "该 vendor 的 GPU 采集未接入（暂支持 linux）", Interfaces: []IfHealth{}}
	healthMu.Unlock()
	t.Cleanup(func() {
		healthMu.Lock()
		healthState = map[string]DeviceHealth{}
		healthMu.Unlock()
	})

	// XID 活动 Finding（g1 的 severe 事件）。
	if err := SaveFinding(&Finding{
		Title: "[GPU] XID 事件 #79 @ g1", Severity: SeverityCritical,
		Devices: []string{"g1"}, Source: "gpu:xid:g1", Status: "active",
		Evidence: []Evidence{{Device: "g1", Command: gpuJournalXidCmd, Output: "NVRM: Xid (PCI:0000:04:00.0): 79"}},
	}); err != nil {
		t.Fatal(err)
	}

	b := m.BuildGPUBoard()
	if b.TotalCards != 2 {
		t.Errorf("total_cards want 2, got %d", b.TotalCards)
	}
	if b.WorstTemp != 88 || b.WorstTempDev != "g1" {
		t.Errorf("worst temp want 88@g1, got %d@%s", b.WorstTemp, b.WorstTempDev)
	}
	if b.SampledDevs != 1 {
		t.Errorf("sampled want 1, got %d", b.SampledDevs)
	}
	if b.XIDActive != 1 || len(b.XIDEvents) != 1 {
		t.Fatalf("xid events want 1 active, got active=%d n=%d", b.XIDActive, len(b.XIDEvents))
	}
	if b.XIDEvents[0].MaxCode != 79 || b.XIDEvents[0].Device != "g1" {
		t.Errorf("xid event parse wrong: %+v", b.XIDEvents[0])
	}
	byName := map[string]GPUBoardDevice{}
	for _, d := range b.Devices {
		byName[d.Device] = d
	}
	if len(byName["g1"].Cards) != 2 || byName["g1"].Cards[1].MemPct != 92 {
		t.Errorf("g1 matrix wrong: %+v", byName["g1"].Cards)
	}
	if byName["g2"].GPUSampled || byName["g2"].LastError == "" {
		t.Errorf("g2 (refused) must surface lastError without sampled, got %+v", byName["g2"])
	}
	if !strings.Contains(byName["win-gpu"].LastError, "未接入") {
		t.Errorf("win-gpu placeholder must carry not-wired note, got %+v", byName["win-gpu"])
	}
}

// TempSpark：gpu.<i>.temp 时序按 30 分钟桶取全卡最大值降采样。
func TestGPUTempSpark(t *testing.T) {
	netdevStateDirOverr = t.TempDir()
	t.Cleanup(func() { netdevStateDirOverr = "" })
	seriesPath = ""
	seriesDirPath = ""
	seriesMigratedFor = ""
	t.Cleanup(func() { seriesPath = ""; seriesDirPath = ""; seriesMigratedFor = "" })

	now := time.Now().Unix()
	// 两个桶、两张卡：桶内取 max。直写 JSONL（RecordSeriesLabeled 不收时间戳）。
	writeSeries := func(ts int64, metric string, v float64) {
		line := `{"t":` + gpuDashItoa(ts) + `,"d":"s1","m":` + gpuDashQ(metric) + `,"v":` + gpuDashFtoa(v) + "}\n"
		_ = os.MkdirAll(seriesDir(), 0o700) // 直写绕过 Record 入口，父目录自己建
		f, err := os.OpenFile(seriesShardPath("s1"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatalf("open shard: %v", err)
		}
		_, _ = f.WriteString(line)
		f.Close()
	}
	writeSeries(now-3600, "gpu.0.temp", 61)
	writeSeries(now-3600, "gpu.1.temp", 88)
	writeSeries(now-60, "gpu.0.temp", 70)

	sp := gpuTempSpark("s1")
	if len(sp) == 0 {
		t.Fatal("spark should have points")
	}
	maxSeen := 0.0
	for _, p := range sp {
		if p[1] > maxSeen {
			maxSeen = p[1]
		}
	}
	if maxSeen != 88 {
		t.Errorf("bucketed max-across-cards want 88, got %v", maxSeen)
	}
}
