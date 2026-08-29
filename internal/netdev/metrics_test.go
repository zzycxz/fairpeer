package netdev

import (
	"path/filepath"
	"testing"
	"time"
)

func metricTestDB(t *testing.T) {
	t.Helper()
	metricsMu.Lock()
	if metricsDB != nil {
		metricsDB.Close()
		metricsDB = nil
	}
	metricsPath = filepath.Join(t.TempDir(), "metrics.db")
	metricsMu.Unlock()
	t.Cleanup(func() {
		metricsMu.Lock()
		if metricsDB != nil {
			metricsDB.Close()
			metricsDB = nil
		}
		metricsPath = ""
		metricsMu.Unlock()
	})
}

func TestMetricHistoryAndFlapCount(t *testing.T) {
	metricTestDB(t)
	now := time.Now()
	// 6 points over the last hour: up, up, down, up, down, up → 4 flaps.
	seq := []bool{true, true, false, true, false, true}
	for i, up := range seq {
		if err := RecordMetricPoint("sw1", MetricPoint{
			Time:      now.Add(-time.Duration(len(seq)-i) * 5 * time.Minute),
			Reachable: up, IfUp: func() int {
				if up {
					return 8
				}
				return 7
			}(), IfDown: func() int {
				if up {
					return 0
				}
				return 1
			}(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	hist := MetricHistory("sw1", 100)
	if len(hist) != len(seq) {
		t.Fatalf("history rows = %d, want %d", len(hist), len(seq))
	}
	if !hist[0].Time.After(hist[len(hist)-1].Time) {
		t.Error("history must be newest-first")
	}
	if n := FlapCount("sw1", time.Hour); n != 4 {
		t.Errorf("FlapCount = %d, want 4", n)
	}
	// Outside the window: nothing counts.
	if n := FlapCount("sw1", time.Minute); n > 1 {
		t.Errorf("FlapCount(1min window) = %d, want ≤1", n)
	}
}

func TestP90IfDown(t *testing.T) {
	metricTestDB(t)
	now := time.Now()
	// 30 points of baseline (0 down) + today's spike (3 down).
	for i := 0; i < 30; i++ {
		d := 0
		if i == 29 {
			d = 3
		}
		if err := RecordMetricPoint("r1", MetricPoint{Time: now.Add(-time.Duration(30-i) * time.Minute), Reachable: true, IfDown: d}); err != nil {
			t.Fatal(err)
		}
	}
	p90, ok := P90IfDown("r1", 24*time.Hour)
	if !ok {
		t.Fatal("expected a baseline with 30 points")
	}
	if p90 != 0 {
		t.Errorf("p90 = %d, want 0 (one spike of 30 must not lift it)", p90)
	}
	// Thin history → no baseline.
	if _, ok := P90IfDown("nope", 24*time.Hour); ok {
		t.Error("unknown device must report no baseline")
	}
}
