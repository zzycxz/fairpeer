package main

// browser_console_watch_test.go — pure-logic tests for the watch's aligned
// schedule: whole-minute grid windows anchored at the start instant,
// contiguous reconciliation against the last covered end, catch-up stretch
// with cap, and the next-fire grid computation.

import (
	"testing"
	"time"
)

var watchAnchor = time.Date(2026, 9, 3, 22, 38, 0, 0, time.Local)

func TestWatchAlignedWindowGrid(t *testing.T) {
	// First round (no history): [22:33:00, 22:38:00] — the interval that
	// closed at the anchor.
	start, end, catchup := watchAlignedWindow(watchAnchor, time.Time{}, 5*time.Minute)
	if want := watchAnchor.Add(-5 * time.Minute); !start.Equal(want) || !end.Equal(watchAnchor) || catchup {
		t.Errorf("first round: [%s, %s] catchup=%v", start, end, catchup)
	}
	// Round 2 at 22:43:00 with round 1 covered to 22:38:00 — exactly
	// contiguous [22:38:00, 22:43:00].
	last := watchAnchor
	scheduled := watchAnchor.Add(5 * time.Minute)
	start, end, catchup = watchAlignedWindow(scheduled, last, 5*time.Minute)
	if !start.Equal(last) || !end.Equal(scheduled) || catchup {
		t.Errorf("grid round: [%s, %s] catchup=%v, want contiguous [%s, %s]",
			start, end, catchup, last, scheduled)
	}
}

func TestWatchAlignedWindowCatchup(t *testing.T) {
	// Round at 22:53 but only 22:38 was covered (22:43 and 22:48 skipped):
	// stretch back — [22:38:00, 22:53:00].
	last := watchAnchor
	scheduled := watchAnchor.Add(15 * time.Minute)
	start, end, catchup := watchAlignedWindow(scheduled, last, 5*time.Minute)
	if !catchup || !start.Equal(last) || !end.Equal(scheduled) {
		t.Errorf("catch-up: [%s, %s] catchup=%v, want [%s, %s]", start, end, catchup, last, scheduled)
	}
	// Long pause (3h gap): the stretch caps at 30m.
	scheduled = watchAnchor.Add(3 * time.Hour)
	start, _, catchup = watchAlignedWindow(scheduled, last, 5*time.Minute)
	if !catchup {
		t.Fatal("long pause must be catch-up")
	}
	if want := scheduled.Add(-30 * time.Minute); !start.Equal(want) {
		t.Errorf("catch-up must cap at 30m: start %s, want %s", start, want)
	}
	// Previous catch-up already covered past this grid start: clamp forward,
	// never query the same interval twice.
	last = watchAnchor.Add(16 * time.Minute) // 22:54
	scheduled = watchAnchor.Add(15 * time.Minute)
	start, _, _ = watchAlignedWindow(scheduled, last, 5*time.Minute)
	if !start.Equal(last) {
		t.Errorf("clamp: start %s, want %s", start, last)
	}
}

func TestNextWatchFire(t *testing.T) {
	// Anchor 22:38:00, 5m interval: fires at 22:43:00, 22:48:00… regardless
	// of when the previous round actually finished.
	cases := []struct {
		after time.Time
		want  time.Time
	}{
		{watchAnchor.Add(47 * time.Second), watchAnchor.Add(5 * time.Minute)},   // 22:38:47 → 22:43
		{watchAnchor.Add(5 * time.Minute), watchAnchor.Add(10 * time.Minute)},   // exactly 22:43 → 22:48
		{watchAnchor.Add(7 * time.Minute), watchAnchor.Add(10 * time.Minute)},   // 22:45 (slow round) → 22:48
		{watchAnchor.Add(-time.Minute), watchAnchor.Add(5 * time.Minute)},       // clock skew before anchor → first grid point
	}
	for _, c := range cases {
		if got := nextWatchFire(watchAnchor, 5*time.Minute, c.after); !got.Equal(c.want) {
			t.Errorf("nextWatchFire(after %s): %s, want %s", c.after.Format("15:04:05"), got.Format("15:04:05"), c.want.Format("15:04:05"))
		}
	}
}

func TestParseAlertVerdict(t *testing.T) {
	report := "## 概览\n共 42 条\n\n## 需关注告警\n| … |\n\n```json\n{\"失陷主机\": [\"10.0.0.5\", \"10.0.0.9\"], \"需关注告警数\": 3, \"最高等级\": \"high\", \"需通知\": true, \"通知理由\": \"SSH 爆破成功\"}\n```\n"
	v, ok := parseAlertVerdict(report)
	if !ok {
		t.Fatal("verdict block not parsed")
	}
	if len(v.CompromisedHosts) != 2 || v.CompromisedHosts[0] != "10.0.0.5" || v.attention() != 3 || v.Severity != "high" || !v.Notify {
		t.Errorf("verdict: %+v", v)
	}
	// The generic schema coalesces into the same accessors.
	g, ok := parseAlertVerdict("```json\n{\"关键发现\": [\"订单A0032金额异常\"], \"需关注条数\": 2, \"最高等级\": \"medium\"}\n```")
	if !ok || len(g.findings()) != 1 || g.findings()[0] != "订单A0032金额异常" || g.attention() != 2 {
		t.Errorf("generic verdict: ok=%v %+v", ok, g)
	}
	// The LAST json fence wins (earlier ones may be data samples).
	two := "```json\n{\"x\": 1}\n```\n正文…\n```json\n{\"失陷主机\": [\"1.1.1.1\"], \"需关注告警数\": 1}\n```\n"
	v, ok = parseAlertVerdict(two)
	if !ok || len(v.CompromisedHosts) != 1 || v.CompromisedHosts[0] != "1.1.1.1" {
		t.Errorf("last-fence-wins: ok=%v %+v", ok, v)
	}
	// No fence / broken fence → no verdict (notification gate stays closed).
	if _, ok := parseAlertVerdict("没有结论块的报告"); ok {
		t.Error("report without a json fence must not parse")
	}
	if _, ok := parseAlertVerdict("```json\n{not json}\n```"); ok {
		t.Error("broken json must not parse")
	}
}

func TestWatchNotifyShould(t *testing.T) {
	cases := []struct {
		policy string
		v      alertVerdict
		want   bool
	}{
		{"compromised", alertVerdict{CompromisedHosts: []string{"10.0.0.5"}}, true},
		{"compromised", alertVerdict{KeyFindings: []string{"订单异常"}}, true}, // generic findings gate too
		{"compromised", alertVerdict{AttentionAlerts: 5, Severity: "high"}, false}, // 关注≠失陷
		{"attention", alertVerdict{AttentionAlerts: 1}, true},
		{"attention", alertVerdict{AttentionItems: 1}, true},
		{"attention", alertVerdict{}, false},
		{"always", alertVerdict{}, true},
		{"never", alertVerdict{CompromisedHosts: []string{"x"}}, false},
		{"", alertVerdict{CompromisedHosts: []string{"x"}}, false}, // unset = never
	}
	for _, c := range cases {
		if got := watchNotifyShould(BrowserConsoleWatchNotify{OnEvent: c.policy}, c.v); got != c.want {
			t.Errorf("policy %q verdict %+v: got %v, want %v", c.policy, c.v, got, c.want)
		}
	}
}
