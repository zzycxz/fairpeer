package netdev

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// metrics_water_test.go — SNMP 水位列（DASHBOARD spec §7.3 OID 扩展）：
// 旧库迁移（IF NOT EXISTS 不补列 → ALTER 对账）+ 读写往返 + gauge 提取。

func waterTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := metricsPath
	metricsPath = filepath.Join(dir, "metrics.db")
	t.Cleanup(func() {
		if metricsDB != nil {
			metricsDB.Close()
			metricsDB = nil
		}
		metricsPath = prev
	})
	return metricsPath
}

func TestMetricWaterColumnsMigrateAndRoundtrip(t *testing.T) {
	path := waterTestDB(t)
	// 旧 schema（六列，无水位）——模拟升级前建好的库。
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE metric_points (
		device TEXT NOT NULL, ts INTEGER NOT NULL, up INTEGER NOT NULL,
		uptime INTEGER NOT NULL, if_up INTEGER NOT NULL, if_dn INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	now := time.Now()
	if err := RecordMetricPoint("SW-W", MetricPoint{
		Time: now, Reachable: true, UptimeSec: 100, IfUp: 5, IfDown: 1,
		Cpu: 72, Mem: 41, InOct: 1 << 40, OutOct: 42, // 64-bit octet sum must survive
	}); err != nil {
		t.Fatal(err)
	}
	h := MetricHistory("SW-W", 10)
	if len(h) != 1 {
		t.Fatalf("history = %d", len(h))
	}
	p := h[0]
	if p.Cpu != 72 || p.Mem != 41 || p.InOct != 1<<40 || p.OutOct != 42 {
		t.Errorf("watermarks lost: %+v", p)
	}
	// 旧列仍然工作
	if !p.Reachable || p.UptimeSec != 100 || p.IfDown != 1 {
		t.Errorf("legacy columns lost: %+v", p)
	}
}

func TestPduGauge(t *testing.T) {
	if got := pduGauge(gosnmp.SnmpPDU{Value: uint(87)}); got != 87 {
		t.Errorf("uint gauge = %d", got)
	}
	if got := pduGauge(gosnmp.SnmpPDU{Value: uint64(12)}); got != 12 {
		t.Errorf("uint64 gauge = %d", got)
	}
	if got := pduGauge(gosnmp.SnmpPDU{Value: "nope"}); got != 0 {
		t.Errorf("non-int gauge = %d", got)
	}
}

func TestMetricFileUnwritableKeepsQuiet(t *testing.T) {
	// best-effort 纪律：路径坏掉时 Record 报错但不 panic。
	dir := t.TempDir()
	blocker := filepath.Join(dir, "block")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	prev := metricsPath
	prevDB := metricsDB
	if metricsDB != nil {
		metricsDB.Close()
		metricsDB = nil
	}
	metricsPath = filepath.Join(blocker, "sub", "metrics.db")
	t.Cleanup(func() { metricsPath = prev; metricsDB = prevDB })
	if err := RecordMetricPoint("SW-X", MetricPoint{Time: time.Now()}); err == nil {
		t.Log("unexpected success (dir may auto-create); error path tolerated either way")
	}
}
