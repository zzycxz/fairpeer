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
	// 两轮 false-positive → 抑制计数 = 2
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
	// 第三次同键触发 → 降级 info
	sev, degraded := suppressedSeverity("syslog:sw-1:link-flap", SeverityWarning)
	if sev != SeverityInfo || !degraded {
		t.Fatalf("degraded: %v %v", sev, degraded)
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
	// 聚合：同键 3 条（2 误报 + 1 ack），open=1
	aggs := AggregateFindings()
	for _, a := range aggs {
		if a.Key == "syslog:sw-1:link-flap" {
			if a.Count != 3 || a.Open != 1 || a.Suppressed != 2 {
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
