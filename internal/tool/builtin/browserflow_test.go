package builtin

import (
	"strings"
	"testing"
)

// TestParseFlowTable covers the 步骤-table grammar the deterministic executor
// consumes — the same dialect the ops editor serializes.
func TestParseFlowTable(t *testing.T) {
	body := "---\nname: browser-demo\ndescription: d\nexecutor: browser-flow\n---\n\n# 演示\n\n## 何时使用\n\nx\n\n## 步骤\n\n" +
		"| # | 操作 | 目标 | 值 |\n|---|------|------|------|\n" +
		"| 1 | navigate | `https://a/` |  |\n" +
		"| 2 | human | `url:/home` | 请完成登录 |\n" +
		"| 3 | type | `#q` | {{工单号}} |\n" +
		"| 4 | key | `#q` | enter |\n" +
		"| 5 | scroll | `down` | 4 |\n" +
		"| 6 | wait | `stable:.answer` | 300s |\n" +
		"| 7 | extract | `table.logs` | table |\n" +
		"| 8 | back |  |  |\n"
	steps, err := ParseFlowTable(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(steps) != 8 {
		t.Fatalf("got %d steps, want 8", len(steps))
	}
	if steps[0].URL != "https://a/" {
		t.Errorf("step1 url: %q", steps[0].URL)
	}
	if steps[1].Type != "human" || steps[1].Condition != "url:/home" || steps[1].Text != "请完成登录" {
		t.Errorf("human step: %+v", steps[1])
	}
	if steps[2].Text != "{{工单号}}" {
		t.Errorf("type text: %q", steps[2].Text)
	}
	if steps[4].Direction != "down" || steps[4].Amount != 4 {
		t.Errorf("scroll: %+v", steps[4])
	}
	if steps[5].Condition != "stable:.answer" || steps[5].TimeoutSec != 300 {
		t.Errorf("wait: %+v", steps[5])
	}
	if steps[6].Value != "table" {
		t.Errorf("extract table flag: %+v", steps[6])
	}
	if steps[7].Type != "back" {
		t.Errorf("back: %+v", steps[7])
	}
}

func TestParseFlowTableErrors(t *testing.T) {
	if _, err := ParseFlowTable("---\nname: x\n---\n\n## 何时使用\n\nno table"); err == nil {
		t.Error("missing 步骤 section must error")
	}
	bad := "## 步骤\n\n| # | 操作 | 目标 | 值 |\n|---|---|---|---|\n| 1 | telnet | `x` |  |\n"
	if _, err := ParseFlowTable(bad); err == nil || !strings.Contains(err.Error(), "未知操作") {
		t.Errorf("unknown op must error, got %v", err)
	}
}

func TestParseFlowParams(t *testing.T) {
	p := ParseFlowParams("工单号=A123 日期=2026-09-01 备注=\"两个 词\"")
	if p["工单号"] != "A123" || p["日期"] != "2026-09-01" || p["备注"] != "两个 词" {
		t.Errorf("k=v parsing: %+v", p)
	}
	if q := ParseFlowParams("随便一句话")["问题"]; q != "随便一句话" {
		t.Errorf("no-equals fallback to 问题: %q", q)
	}
	if len(ParseFlowParams("")) != 0 {
		t.Error("empty arguments → empty params")
	}
}

// TestRunBrowserFlowPlanningGuards exercises the BEFORE-browser validation:
// missing bindings and condition-less human steps error without opening any
// window (planning happens ahead of newBrowserSession, so no browser is
// touched on these paths).
func TestRunBrowserFlowPlanningGuards(t *testing.T) {
	body := "## 步骤\n\n| # | 操作 | 目标 | 值 |\n|---|---|---|---|\n| 1 | type | `#q` | {{工单号}} |\n"
	_, err := RunBrowserFlow(nil, body, "")
	if err == nil || !strings.Contains(err.Error(), "工单号") {
		t.Errorf("missing binding must name the param, got %v", err)
	}

	humanBody := "## 步骤\n\n| # | 操作 | 目标 | 值 |\n|---|---|---|---|\n| 1 | human |  | 请登录 |\n"
	_, err = RunBrowserFlow(nil, humanBody, "")
	if err == nil || !strings.Contains(err.Error(), "自动检测条件") {
		t.Errorf("condition-less human must error at planning, got %v", err)
	}
}

func TestSplitFlowAnchors(t *testing.T) {
	anchors := splitFlowAnchors("#login-btn;;text=登 录 ;; #fallback")
	if len(anchors) != 3 {
		t.Fatalf("got %d anchors, want 3: %+v", len(anchors), anchors)
	}
	if anchors[0].kind != "css" || anchors[0].val != "#login-btn" {
		t.Errorf("anchor0: %+v", anchors[0])
	}
	if anchors[1].kind != "text" || anchors[1].val != "登 录" {
		t.Errorf("anchor1: %+v", anchors[1])
	}
	if anchors[2].kind != "css" || anchors[2].val != "#fallback" {
		t.Errorf("anchor2: %+v", anchors[2])
	}
	// Plain CSS target (no chain) stays a single css anchor — back-compat.
	single := splitFlowAnchors("#q")
	if len(single) != 1 || single[0].kind != "css" {
		t.Errorf("plain selector must remain single css anchor: %+v", single)
	}
	if got := splitFlowAnchors(";; ;;"); len(got) != 0 {
		t.Errorf("empty segments must drop: %+v", got)
	}
}

func TestNormalizeNavURL(t *testing.T) {
	cases := map[string]string{
		"www.example.com":      "https://www.example.com",
		"example.com/path?q=1": "https://example.com/path?q=1",
		"  baidu.com  ":        "https://baidu.com",
		"http://a.com":         "http://a.com",
		"https://a.com":        "https://a.com",
		"about:blank":          "about:blank",
		"file:///C:/x.html":    "file:///C:/x.html",
		"chrome://version":     "chrome://version",
		"devtools://devtools":  "devtools://devtools",
		"":                     "",
	}
	for in, want := range cases {
		if got := normalizeNavURL(in); got != want {
			t.Errorf("normalizeNavURL(%q) = %q, want %q", in, got, want)
		}
	}
}
