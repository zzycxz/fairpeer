package builtin

import (
	"errors"
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
		"| 8 | back |  |  |\n" +
		"| 9 | switch_tab | `2` | 告警详情 |\n" +
		"| 10 | wait | `stable:.out` | 15000 |\n" +
		"| 11 | wait | `download` | 300s |\n" +
		"| 12 | wait | `download:.xlsx` | 480s |\n"
	steps, err := ParseFlowTable(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(steps) != 12 {
		t.Fatalf("got %d steps, want 12", len(steps))
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
	if steps[8].Type != "switch_tab" || steps[8].Target != "2" || steps[8].Text != "告警详情" {
		t.Errorf("switch_tab: %+v", steps[8])
	}
	// ms-habit values fold to seconds: "15000" means 15s, not 15000s.
	if steps[9].TimeoutSec != 15 {
		t.Errorf("wait ms-fold: got %ds, want 15s", steps[9].TimeoutSec)
	}
	// download waits: plain + extension-filtered conditions parse through to
	// the wait step's condition/timeout slots.
	if steps[10].Condition != "download" || steps[10].TimeoutSec != 300 {
		t.Errorf("wait download: %+v", steps[10])
	}
	if steps[11].Condition != "download:.xlsx" || steps[11].TimeoutSec != 480 {
		t.Errorf("wait download ext: %+v", steps[11])
	}
}

func TestParseFlowTableEvaluateCell(t *testing.T) {
	// The cybersituational-awareness skill carries an ExtJS-aware evaluate
	// step: a single-line IIFE with quotes, braces, regex and an embedded
	// {{参数}} ref — the table dialect must survive it verbatim.
	cell := `(function(){var el=document.querySelector('input[name="time_range"]');if(!el){throw new Error('未找到时间范围输入框');}var want='{{时间范围}}';var d=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value');d.set.call(el,want);el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));try{if(window.Ext&&el.id){var c=Ext.getCmp(el.id.replace(new RegExp('-inputEl$'),''));if(c&&c.setValue){c.setValue(want);}}}catch(e){}if(el.value!==want){throw new Error('时间范围写入失败，当前值: '+el.value);}return '时间范围已设置: '+el.value;})()`
	body := "## 步骤\n\n| # | 操作 | 目标 | 值 |\n|---|---|---|---|\n" +
		"| 1 | evaluate | `" + cell + "` |  |\n" +
		"| 2 | type | `input[name=\"query_string\"]` | (NOT src_ip:(10.0.0.0 OR \"172.16.0.0/12\")) |\n"
	steps, err := ParseFlowTable(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(steps) != 2 || steps[0].Type != "evaluate" || steps[0].Expression != cell {
		t.Fatalf("evaluate cell mangled: %+v", steps[0])
	}
	if !strings.Contains(steps[0].Expression, "{{时间范围}}") {
		t.Error("param ref inside evaluate cell must survive for lazy substitution")
	}
	if steps[1].Target != `input[name="query_string"]` {
		t.Errorf("query selector: %q", steps[1].Target)
	}
	if !strings.HasPrefix(steps[1].Text, "(NOT src_ip:") {
		t.Errorf("query value: %q", steps[1].Text)
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

// TestIsLocateMiss pins the safety classification behind the flow runner's
// locate-wait: ONLY errors that prove the action never dispatched may poll —
// anything else must fail through (a retry could double-fire a click).
func TestIsLocateMiss(t *testing.T) {
	miss := []string{
		`页面上找不到可见文本为 "登录" 的可点击元素`,
		`click ref "e6": element not found`,
		`hover "#menu": no node found matching selector`,
		`ref "e3" resolved to no DOM object (page may have changed)`,
	}
	for _, m := range miss {
		if !isLocateMiss(errors.New(m)) {
			t.Errorf("isLocateMiss(%q) = false, want true (locate miss)", m)
		}
	}
	// Never retried: the action may have fired, the config is wrong, or the
	// failure is unrecoverable without a new snapshot.
	noMiss := []string{
		"点击文本 \"登录\" @(120,40): context deadline exceeded", // transport died mid-action
		`目标为空`,            // config error
		`ref "e6" 已失效`,   // page changed; re-locating cannot recover a ref
		`元素函数抛错: x is not a function`,
		"unknown ref \"e9\"",
	}
	for _, m := range noMiss {
		if isLocateMiss(errors.New(m)) {
			t.Errorf("isLocateMiss(%q) = true, want false (unsafe to retry)", m)
		}
	}
	if isLocateMiss(nil) {
		t.Error("isLocateMiss(nil) = true, want false")
	}
}
