package netdev

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 状态机 + 误报学习 + 聚合：false-positive 两次后，同键的 syslog 升级降级
// 为 info；聚合视图按根因键收拢计数。
func TestAlertQueueLifecycle(t *testing.T) {
	dir := t.TempDir()
	oldCases := casesDirOverr
	oldFind := findingsDirOverr
	oldState := netdevStateDirOverr
	defer func() {
		casesDirOverr, findingsDirOverr, netdevStateDirOverr = oldCases, oldFind, oldState
		suppressPath = ""
	}()
	findingsDirOverr = dir
	netdevStateDirOverr = dir
	suppressPath = filepath.Join(dir, "suppressed.json")

	mk := func(id string) *Finding {
		return &Finding{Title: "[syslog] link-flap @ sw-1", Severity: SeverityWarning,
			Devices: []string{"sw-1"}, Source: "syslog:sw-1:link-flap",
			Evidence: []Evidence{{Device: "sw-1", Command: "t", Output: "o"}}}
	}
	// P1-F12：阈值升到 3——两轮只是坏了一晚，三轮才算模式。两轮后不降级。
	for i := 0; i < 2; i++ {
		f := mk("")
		_ = SaveFinding(f)
		if err := FalsePositiveFindingByID(f.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n := suppressCount("syslog:sw-1:link-flap"); n != 2 {
		t.Fatalf("suppression count = %d, want 2", n)
	}
	if sev, degraded := suppressedSeverity("syslog:sw-1:link-flap", SeverityWarning); degraded {
		t.Fatalf("two marks must not degrade yet: %v %v", sev, degraded)
	}
	// 第三轮 false-positive → 达到阈值，同键触发降级 info
	f3 := mk("")
	_ = SaveFinding(f3)
	if err := FalsePositiveFindingByID(f3.ID); err != nil {
		t.Fatal(err)
	}
	sev, degraded := suppressedSeverity("syslog:sw-1:link-flap", SeverityWarning)
	if sev != SeverityInfo || !degraded {
		t.Fatalf("degraded: %v %v", sev, degraded)
	}
	// 解除入口：人工清除后恢复原级别
	if err := UnsuppressSource("syslog:sw-1:link-flap"); err != nil {
		t.Fatal(err)
	}
	if sev, degraded := suppressedSeverity("syslog:sw-1:link-flap", SeverityWarning); degraded || sev != SeverityWarning {
		t.Fatalf("after unsuppress: %v %v", sev, degraded)
	}
	// ack 路径
	f := mk("")
	_ = SaveFinding(f)
	if err := AckFindingByID(f.ID); err != nil {
		t.Fatal(err)
	}
	fs, _ := ListFindings()
	acked := false
	for _, x := range fs {
		if x.ID == f.ID && x.Status == FindingAck {
			acked = true
		}
	}
	if !acked {
		t.Fatal("ack transition missing")
	}
	// 聚合：同键 4 条（3 误报 + 1 ack），open=1；P1-F12 解除抑制后
	// Suppressed 归零（该键不再处于抑制态）。
	aggs := AggregateFindings()
	for _, a := range aggs {
		if a.Key == "syslog:sw-1:link-flap" {
			if a.Count != 4 || a.Open != 1 || a.Suppressed != 0 {
				t.Fatalf("aggregate: %+v", a)
			}
			if !strings.Contains(a.Title, "link-flap") {
				t.Fatalf("title: %s", a.Title)
			}
			return
		}
	}
	t.Fatalf("aggregate key missing: %+v", aggs)
	_ = time.Now
}
