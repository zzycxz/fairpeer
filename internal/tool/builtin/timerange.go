package builtin

// timerange.go resolves human time-range phrases ("最近5分钟", "last 2h",
// "今天") into the literal range string SIEM-style exports demand:
//
//	2006-01-02 15:04:05 - 2006-01-02 15:04:05
//
// It is applied to WHOLE parameter values only (flow params and trial-run
// param seeds), so a phrase embedded in longer text — a search query like
// "(NOT src_ip:…)" — never gets rewritten. The output format matches the
// 态势感知平台's time_range input the recorded skill types into; other
// platforms with other formats pass their literal range through unchanged
// (already-formatted input normalizes to canonical spacing).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const timeRangeLayout = "2006-01-02 15:04:05"

// TimeRangeFormat is the canonical output layout — exported so callers (panel
// preview, docs) can render placeholders consistently.
const TimeRangeFormat = timeRangeLayout + " - " + timeRangeLayout

var (
	// reRangePhrase matches "最近/近/过去 N 分钟|小时|天" and English
	// "last N m|min|h|hour|d|day(s)" — the whole value, nothing else.
	reRangePhrase = regexp.MustCompile(`^(?:最近|近|过去)\s*(\d+)\s*(分钟|小时|天)$`)
	reRangeEn     = regexp.MustCompile(`^last\s+(\d+)\s*(m|min|mins|minute|minutes|h|hr|hour|hours|d|day|days)$`)

	// reRangeNamed matches fixed bucket names: 今天/昨天/前天/本周/上周.
	reRangeNamed = regexp.MustCompile(`^(今天|昨天|前天|本周|上周|today|yesterday|this week|last week)$`)

	// reRangeLiteral matches an already-formatted "start - end" pair (spaces
	// around the dash optional, seconds optional on either side).
	reRangeLiteral = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2})?)\s*-\s*(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2})?)$`)
)

// ResolveTimeRange converts a relative time-range phrase into the literal
// "start - end" string. ok=false means the value is not a recognized phrase —
// callers pass it through untouched. now anchors the relative words.
func ResolveTimeRange(now time.Time, s string) (string, bool) {
	v := strings.TrimSpace(s)
	if v == "" {
		return "", false
	}
	if m := reRangePhrase.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		var d time.Duration
		switch {
		case strings.HasPrefix(m[2], "分钟"):
			d = time.Duration(n) * time.Minute
		case strings.HasPrefix(m[2], "小时"):
			d = time.Duration(n) * time.Hour
		default:
			d = time.Duration(n) * 24 * time.Hour
		}
		return formatTimeRange(now.Add(-d), now), true
	}
	if m := reRangeEn.FindStringSubmatch(v); m != nil {
		n, _ := strconv.Atoi(m[1])
		var d time.Duration
		switch m[2][0] {
		case 'm':
			d = time.Duration(n) * time.Minute
		case 'h':
			d = time.Duration(n) * time.Hour
		default:
			d = time.Duration(n) * 24 * time.Hour
		}
		return formatTimeRange(now.Add(-d), now), true
	}
	if m := reRangeNamed.FindStringSubmatch(v); m != nil {
		start, end := namedRange(now, strings.ToLower(m[1]))
		return formatTimeRange(start, end), true
	}
	if m := reRangeLiteral.FindStringSubmatch(v); m != nil {
		start, ok1 := parseRangeEndpoint(m[1], now)
		end, ok2 := parseRangeEndpoint(m[2], now)
		if ok1 && ok2 {
			return formatTimeRange(start, end), true
		}
	}
	return "", false
}

// namedRange maps a bucket word to its [start, end) window. 今天/昨天 end at
// "now" (not midnight) — an export "今天" mid-day expects everything so far.
func namedRange(now time.Time, word string) (time.Time, time.Time) {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch word {
	case "今天", "today":
		return day, now
	case "昨天", "yesterday":
		return day.AddDate(0, 0, -1), day
	case "前天":
		return day.AddDate(0, 0, -2), day.AddDate(0, 0, -1)
	case "本周", "this week":
		// Monday-start week, per Chinese ops convention.
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		monday := day.AddDate(0, 0, -(wd - 1))
		return monday, now
	case "上周", "last week":
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		monday := day.AddDate(0, 0, -(wd - 1))
		return monday.AddDate(0, 0, -7), monday
	}
	return now, now
}

func parseRangeEndpoint(s string, now time.Time) (time.Time, bool) {
	s = strings.Replace(strings.TrimSpace(s), "T", " ", 1)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// TimeRangeFromSpan builds the literal range covering [now-d, now] — the
// rolling window a periodic watch round exports each interval.
func TimeRangeFromSpan(now time.Time, d time.Duration) string {
	return formatTimeRange(now.Add(-d), now)
}

// TimeRangeBounds parses a literal "start - end" range back into instants —
// the watch uses the end to anchor contiguous coverage across rounds.
func TimeRangeBounds(s string) (start, end time.Time, ok bool) {
	m := reRangeLiteral.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, time.Time{}, false
	}
	start, ok1 := parseRangeEndpoint(m[1], time.Now())
	end, ok2 := parseRangeEndpoint(m[2], time.Now())
	return start, end, ok1 && ok2
}

func formatTimeRange(start, end time.Time) string {
	return fmt.Sprintf("%s - %s", start.Format(timeRangeLayout), end.Format(timeRangeLayout))
}
