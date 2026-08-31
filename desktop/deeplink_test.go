package main

import "testing"

// parseDeepLink 表驱动（DASHBOARD spec §4.12 红线）：导航型 only、
// fail-closed——白名单外的 host（尤其是动作型 approve/execute）、query、
// 路径穿越、多级路径、未知屏名一律拒绝。
func TestParseDeepLink(t *testing.T) {
	valid := map[string]DeepLinkRoute{
		"fairpeer://finding/F-100":    {Kind: "finding", ID: "F-100"},
		"fairpeer://case/C20260831-1": {Kind: "case", ID: "C20260831-1"},
		"fairpeer://cutover/CO-9":     {Kind: "cutover", ID: "CO-9"},
		"fairpeer://proposal/P_7":     {Kind: "proposal", ID: "P_7"},
		"fairpeer://screen/chain":     {Kind: "screen", ID: "chain"},
		"fairpeer://screen/exposure":  {Kind: "screen", ID: "exposure"},
		"  fairpeer://finding/x9  ":   {Kind: "finding", ID: "x9"}, // 首尾空白容忍
	}
	for raw, want := range valid {
		got, ok := parseDeepLink(raw)
		if !ok || got != want {
			t.Errorf("parseDeepLink(%q) = %+v,%v want %+v", raw, got, ok, want)
		}
	}
	invalid := []string{
		"",                             // 空
		"http://finding/F-1",           // 非 fairpeer 协议
		"FAIRPEER://finding/F-1",       // scheme 大小写敏感（严格）
		"fairpeer://",                  // 无 host
		"fairpeer://finding",           // 无 id
		"fairpeer://finding/",          // 空 id
		"fairpeer://approve/P-1",       // 动作型目的地——永久红线
		"fairpeer://execute/x",         // 动作型目的地
		"fairpeer://rollback/x",        // 动作型目的地
		"fairpeer://finding/F-1?x=1",   // query 拒绝
		"fairpeer://finding/../etc",    // 路径穿越
		"fairpeer://finding/a/b",       // 多级路径
		"fairpeer://finding/F 1",       // id 含空格
		"fairpeer://finding/F%201",     // id 含百分号
		"fairpeer://unknown/x",         // 未知 host
		"fairpeer://screen/notascreen", // 未知屏名
		"fairpeer://finding/F-1/extra", // 尾段
	}
	for _, raw := range invalid {
		if _, ok := parseDeepLink(raw); ok {
			t.Errorf("parseDeepLink(%q) must fail (fail-closed)", raw)
		}
	}
}

func TestDeepLinkArgScan(t *testing.T) {
	if got := deepLinkArg([]string{"fairpeer.exe", "--deep-link", "fairpeer://finding/F-7"}); got != "fairpeer://finding/F-7" {
		t.Errorf("split form = %q", got)
	}
	if got := deepLinkArg([]string{"fairpeer.exe", "fairpeer://cutover/CO-1"}); got != "fairpeer://cutover/CO-1" {
		t.Errorf("bare form = %q", got)
	}
	if got := deepLinkArg([]string{"fairpeer.exe", "--flag", "http://x", "fairpeer://approve/P-1"}); got != "" {
		t.Errorf("invalid URLs must not pass through: %q", got)
	}
	if got := deepLinkArg(nil); got != "" {
		t.Errorf("empty argv = %q", got)
	}
}

func TestPendingDeepLinkStashConsume(t *testing.T) {
	if r := consumePendingDeepLink(); r != nil {
		t.Fatalf("clean state consumed %+v", r)
	}
	stashPendingDeepLink("fairpeer://finding/F-42")
	r := consumePendingDeepLink()
	if r == nil || r.Kind != "finding" || r.ID != "F-42" {
		t.Fatalf("consume = %+v", r)
	}
	if again := consumePendingDeepLink(); again != nil {
		t.Fatalf("second consume must be nil, got %+v", again)
	}
	// fail-closed：坏 URL 不落暂存。
	stashPendingDeepLink("fairpeer://approve/P-1")
	if r := consumePendingDeepLink(); r != nil {
		t.Fatalf("invalid stash leaked %+v", r)
	}
}
