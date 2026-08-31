package main

import "testing"

// riskLevelFromScore 表驱动（DASHBOARD spec §5：阈值 + floor 规则——
// 1 条 critical CVE 或 confirmed 弱口令至少抬到 high）。
func TestRiskLevelFromScore(t *testing.T) {
	cases := []struct {
		score, cveCritical, weak int
		want                     string
	}{
		{0, 0, 0, "safe"},
		{3, 0, 0, "low"},
		{10, 0, 0, "medium"},
		{30, 0, 0, "high"},
		{31, 0, 0, "critical"},
		{1, 1, 0, "high"},   // floor: critical CVE
		{0, 0, 1, "high"},   // floor: weak cred confirmed
		{2, 0, 1, "high"},   // floor beats low
		{31, 1, 0, "critical"}, // floor never lowers
		{0, 0, 0, "safe"},
	}
	for _, c := range cases {
		if got := riskLevelFromScore(c.score, c.cveCritical, c.weak); got != c.want {
			t.Errorf("riskLevelFromScore(%d, cve=%d, weak=%d) = %s, want %s", c.score, c.cveCritical, c.weak, got, c.want)
		}
	}
}
