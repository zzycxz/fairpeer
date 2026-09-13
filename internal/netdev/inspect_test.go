package netdev

import (
	"context"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

// F12：并发参数解析——0 取默认、超上限截断。
func TestInspectionConcurrencyResolution(t *testing.T) {
	cfg := config.Default()
	m := NewManager(cfg)
	if got := m.inspectionConcurrency(); got != inspectionConcurrencyDefault {
		t.Errorf("default want %d, got %d", inspectionConcurrencyDefault, got)
	}
	cfg.NetDev.InspectionConcurrency = 99
	if got := m.inspectionConcurrency(); got != inspectionConcurrencyMax {
		t.Errorf("cap want %d, got %d", inspectionConcurrencyMax, got)
	}
	cfg.NetDev.InspectionConcurrency = 7
	if got := m.inspectionConcurrency(); got != 7 {
		t.Errorf("configured want 7, got %d", got)
	}

	if got := m.gpuPollConcurrency(); got != gpuPollConcurrencyDefault {
		t.Errorf("gpu default want %d, got %d", gpuPollConcurrencyDefault, got)
	}
	cfg.NetDev.GPUPollConcurrency = 32
	if got := m.gpuPollConcurrency(); got != 32 {
		t.Errorf("gpu configured want 32, got %d", got)
	}
}

// F12：并发巡检的结果确定性——同一批设备跑两轮（并发 4），evidence 的
// (device, command) 序列必须逐条一致（Finding 内容不随完成时序漂移）。
func TestInspectionConcurrentDeterminism(t *testing.T) {
	sim := startSimDevice(t)
	m, _ := testManager(t, sim)
	m.cfg.NetDev.InspectionConcurrency = 4

	run := func() []string {
		f, err := m.RunInspection(context.Background())
		if err != nil || f == nil {
			t.Fatalf("inspection: %v", err)
		}
		var seq []string
		for _, ev := range f.Evidence {
			seq = append(seq, ev.Device+"|"+ev.Command)
		}
		return seq
	}
	a, b := run(), run()
	if len(a) == 0 {
		t.Fatal("no evidence collected")
	}
	if len(a) != len(b) {
		t.Fatalf("evidence count drifted: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("evidence order drifted at %d: %q vs %q", i, a[i], b[i])
		}
	}
}
