package builtin

import (
	"testing"
	"time"
)

// fixedNow anchors relative phrases: Wed 2026-09-03 15:04:05 local.
var fixedNow = time.Date(2026, 9, 3, 15, 4, 5, 0, time.Local)

func TestResolveTimeRangePhrases(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"最近5分钟", "2026-09-03 14:59:05 - 2026-09-03 15:04:05"},
		{"近 30 分钟", "2026-09-03 14:34:05 - 2026-09-03 15:04:05"},
		{"过去2小时", "2026-09-03 13:04:05 - 2026-09-03 15:04:05"},
		{"最近1天", "2026-09-02 15:04:05 - 2026-09-03 15:04:05"},
		{"last 5m", "2026-09-03 14:59:05 - 2026-09-03 15:04:05"},
		{"last 2 hours", "2026-09-03 13:04:05 - 2026-09-03 15:04:05"},
		{"last 3d", "2026-08-31 15:04:05 - 2026-09-03 15:04:05"},
		{"今天", "2026-09-03 00:00:00 - 2026-09-03 15:04:05"},
		{"昨天", "2026-09-02 00:00:00 - 2026-09-03 00:00:00"},
		// 2026-09-03 is a Thursday: 本周 starts Monday 08-31, 上周 08-24.
		{"本周", "2026-08-31 00:00:00 - 2026-09-03 15:04:05"},
		{"上周", "2026-08-24 00:00:00 - 2026-08-31 00:00:00"},
		{"today", "2026-09-03 00:00:00 - 2026-09-03 15:04:05"},
	}
	for _, c := range cases {
		got, ok := ResolveTimeRange(fixedNow, c.in)
		if !ok {
			t.Errorf("%q: not recognized", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveTimeRangeLiteralPassthrough(t *testing.T) {
	// Already-formatted ranges normalize spacing; seconds are filled in.
	got, ok := ResolveTimeRange(fixedNow, "2026-09-03 10:00:00 - 2026-09-03 12:00:00")
	if !ok || got != "2026-09-03 10:00:00 - 2026-09-03 12:00:00" {
		t.Errorf("literal passthrough: %q %v", got, ok)
	}
	got, ok = ResolveTimeRange(fixedNow, "2026-09-03 10:00-2026-09-03 12:30")
	if !ok || got != "2026-09-03 10:00:00 - 2026-09-03 12:30:00" {
		t.Errorf("minute-precision literal: %q %v", got, ok)
	}
}

func TestTimeRangeBoundsRoundTrip(t *testing.T) {
	r, ok := ResolveTimeRange(fixedNow, "最近5分钟")
	if !ok {
		t.Fatal("phrase not recognized")
	}
	start, end, ok := TimeRangeBounds(r)
	if !ok {
		t.Fatalf("resolved range not parseable: %q", r)
	}
	if want := fixedNow.Add(-5 * time.Minute); !start.Equal(want) || !end.Equal(fixedNow) {
		t.Errorf("bounds: [%s, %s], want [%s, %s]", start, end, want, fixedNow)
	}
	if _, _, ok := TimeRangeBounds("不是范围"); ok {
		t.Error("non-range must not parse")
	}
}

func TestFlowSubstLazyTimeRange(t *testing.T) {
	// A whole-value phrase resolves AT SUBSTITUTION TIME (query instant, not
	// run start) and lands in the cache so later steps share the window.
	st := FlowStep{Type: "type", Target: "#range", Text: "{{时间范围}}"}
	resolved := map[string]string{}
	flowSubst(&st, map[string]string{"时间范围": "最近5分钟"}, resolved)
	if _, _, ok := TimeRangeBounds(st.Text); !ok {
		t.Errorf("phrase must resolve lazily to a literal range, got %q", st.Text)
	}
	if resolved["时间范围"] != st.Text {
		t.Errorf("resolution must be cached, cache=%q text=%q", resolved["时间范围"], st.Text)
	}
	// Cached value wins on later steps — one consistent window per run.
	st2 := FlowStep{Type: "wait", Condition: "{{时间范围}}"}
	flowSubst(&st2, map[string]string{"时间范围": "最近5分钟"}, resolved)
	if st2.Condition != st.Text {
		t.Errorf("later steps must reuse the cached window: %q vs %q", st2.Condition, st.Text)
	}
	// Embedded phrases stay literal — search queries are never rewritten.
	st3 := FlowStep{Type: "type", Target: "#q", Text: "{{查询}}"}
	flowSubst(&st3, map[string]string{"查询": "查询 最近5分钟 的报告"}, map[string]string{})
	if st3.Text != "查询 最近5分钟 的报告" {
		t.Errorf("embedded phrase must stay literal, got %q", st3.Text)
	}
}

func TestResolveTimeRangeRejectsNonPhrases(t *testing.T) {
	// Anything that isn't the WHOLE value being a range phrase passes through
	// untouched — search queries and prose must never be rewritten.
	for _, s := range []string{
		"",
		"   ",
		"(NOT src_ip:(10.0.0.0 OR 192.168.0.0/16))",
		"最近5分钟的报告帮我看看",
		"前5分钟",         // 前不受支持（最近/近/过去 only）
		"5分钟",          // bare duration is not a range phrase
		"查一下告警",
		"last week report", // trailing word breaks the whole-value match
	} {
		if got, ok := ResolveTimeRange(fixedNow, s); ok {
			t.Errorf("%q: unexpected conversion to %q", s, got)
		}
	}
}
