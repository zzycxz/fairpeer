package netdev

import "testing"

// SCENARIO_SPEC S4-1/K2-3：权威评分表的分带与排序契约。
func TestAlertDangerRubric(t *testing.T) {
	if got := AlertDangerBand(70); got != "critical" {
		t.Errorf("Band(70) = %s", got)
	}
	if got := AlertDangerBand(69); got != "warning" {
		t.Errorf("Band(69) = %s", got)
	}
	if got := AlertDangerBand(40); got != "warning" {
		t.Errorf("Band(40) = %s", got)
	}
	if got := AlertDangerBand(39); got != "info" {
		t.Errorf("Band(39) = %s", got)
	}
	// 失陷40+横移25=65 warning；+暴露20=85 critical；全权重=100。
	if AlertDangerSignalWeights["失陷确认"]+AlertDangerSignalWeights["横向移动"] != 65 {
		t.Fatal("weights drifted")
	}
	if AlertDangerRank("critical") <= AlertDangerRank("warning") || AlertDangerRank("warning") <= AlertDangerRank("info") {
		t.Fatal("rank order broken")
	}
	if AlertDangerRank("garbage") != 0 {
		t.Fatal("unknown band must rank as info")
	}
}
