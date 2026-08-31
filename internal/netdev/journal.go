package netdev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// journal.go — L1 三件留存（DASHBOARD spec §7.2）。全部 best-effort：
// journal 写入失败绝不 fail 生产者（与 RecordDiscovered 同纪律）。
//
//	R1 inspections.jsonl      每次巡检/基线一行汇总（风险分走势/发现增长的数据源）
//	R2 port-events.jsonl      端口 newly-opened/newly-closed 事件（discovered.go 调用）
//	R3 syslog-counts-*.jsonl  按天滚动的 {小时,设备,类} 计数（syslogrecv.go 调用）
//	+  promotions.jsonl       待确认区→转正计数（漏斗第 4 级的分母账本）
//	+  baseline-summary.json  最近一次基线汇总（总览 BaselineAgg 的持久位）
//
// 压实策略：R1 原始行保留 90 天，更旧的聚为按日一行进 inspections-daily.jsonl
// （§7.2 压实策略；R2 只在变化时追加天然小、R3 本身按天，不压实）。

var (
	journalDirOverr string
	journalMu       sync.Mutex
)

func journalDir() string {
	if journalDirOverr != "" {
		return journalDirOverr
	}
	return netdevStateDir()
}

// ---------------------------------------------------------------------------
// R1 — 巡检/基线汇总行
// ---------------------------------------------------------------------------

// IfBriefCounts is the per-device interface tally lifted from the inspection
// battery's interface-brief output (line-count heuristic, not a per-interface
// ledger — good enough for a trend, labeled as such).
type IfBriefCounts struct {
	Up   int `json:"up"`
	Down int `json:"down"`
}

// InspectionJournalRow is one run's summary line (R1).
type InspectionJournalRow struct {
	At           string                   `json:"at"`   // 2006-01-02T15:04:05
	Kind         string                   `json:"kind"` // inspection | baseline
	Devices      int                      `json:"devices"`
	Checked      int                      `json:"checked"`
	Critical     int                      `json:"critical"` // open tallies at run end — the risk-score trend source
	Warning      int                      `json:"warning"`
	Info         int                      `json:"info"`
	BaselineHits int                      `json:"baseline_hits,omitempty"`
	IfBrief      map[string]IfBriefCounts `json:"if_brief,omitempty"`
}

// AppendInspectionRow appends one R1 line (best-effort; also kicks the
// once-a-day compaction check).
func AppendInspectionRow(row InspectionJournalRow) error {
	if row.At == "" {
		row.At = time.Now().Format("2006-01-02T15:04:05")
	}
	journalMu.Lock()
	defer journalMu.Unlock()
	if err := os.MkdirAll(journalDir(), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(journalDir(), "inspections.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	compactJournalsLocked(time.Now())
	return nil
}

// ReadInspectionRows returns up to limit newest R1 rows (oldest→newest order
// preserved for sparklines).
func ReadInspectionRows(limit int) []InspectionJournalRow {
	journalMu.Lock()
	defer journalMu.Unlock()
	rows := readInspectionRowsLocked(limit)
	return rows
}

func readInspectionRowsLocked(limit int) []InspectionJournalRow {
	var out []InspectionJournalRow
	for _, name := range []string{"inspections-daily.jsonl", "inspections.jsonl"} {
		b, err := os.ReadFile(filepath.Join(journalDir(), name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var r InspectionJournalRow
			if json.Unmarshal([]byte(line), &r) == nil && r.At != "" {
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// compactJournalsLocked folds R1 rows older than keepDays into one line per
// day (averages for counts, max for severity tallies). Runs at most once a
// day — the stamp file gates it.
func compactJournalsLocked(now time.Time) {
	stampPath := filepath.Join(journalDir(), ".inspections-compacted")
	if b, err := os.ReadFile(stampPath); err == nil && strings.TrimSpace(string(b)) == now.Format("2006-01-02") {
		return
	}
	_ = os.WriteFile(stampPath, []byte(now.Format("2006-01-02")), 0o600)

	cut := now.AddDate(0, 0, -90).Format("2006-01-02T15:04:05")
	raw, err := os.ReadFile(filepath.Join(journalDir(), "inspections.jsonl"))
	if err != nil {
		return
	}
	var keep []string
	byDay := map[string][]InspectionJournalRow{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var r InspectionJournalRow
		if json.Unmarshal([]byte(line), &r) != nil {
			keep = append(keep, line) // unparseable lines are preserved verbatim
			continue
		}
		if r.At >= cut {
			keep = append(keep, line)
			continue
		}
		byDay[r.At[:journalMin(10, len(r.At))]] = append(byDay[r.At[:journalMin(10, len(r.At))]], r)
	}
	if len(byDay) == 0 && len(keep) == strings.Count(strings.TrimSpace(string(raw)), "\n")+1 {
		return // nothing to fold
	}
	var folded []string
	days := make([]string, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, d := range days {
		rs := byDay[d]
		agg := InspectionJournalRow{At: d + "T00:00:00", Kind: "daily", Devices: rs[0].Devices, Checked: rs[0].Checked}
		if n := len(rs); n > 0 {
			for _, r := range rs {
				agg.Critical = maxOf(agg.Critical, r.Critical)
				agg.Warning = maxOf(agg.Warning, r.Warning)
				agg.Info = maxOf(agg.Info, r.Info)
				agg.BaselineHits = maxOf(agg.BaselineHits, r.BaselineHits)
			}
		}
		if b, err := json.Marshal(agg); err == nil {
			folded = append(folded, string(b))
		}
	}
	if len(folded) > 0 {
		f, err := os.OpenFile(filepath.Join(journalDir(), "inspections-daily.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return
		}
		for _, l := range folded {
			_, _ = f.WriteString(l + "\n")
		}
		f.Close()
	}
	_ = os.WriteFile(filepath.Join(journalDir(), "inspections.jsonl"), []byte(strings.Join(keep, "\n")+"\n"), 0o600)
}

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// R2 — 端口突变事件
// ---------------------------------------------------------------------------

// PortEvent is one observed port state change on an unmanaged lead.
type PortEvent struct {
	At   string `json:"at"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
	Kind string `json:"kind"` // newly-opened | newly-closed
}

// AppendPortEvent appends one R2 line (best-effort, called from discovered.go).
func AppendPortEvent(ip string, port int, kind string) {
	journalMu.Lock()
	defer journalMu.Unlock()
	if err := os.MkdirAll(journalDir(), 0o700); err != nil {
		return
	}
	b, err := json.Marshal(PortEvent{At: time.Now().Format("2006-01-02T15:04:05"), IP: ip, Port: port, Kind: kind})
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(journalDir(), "port-events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// ListPortEvents returns up to limit newest port-change events.
func ListPortEvents(limit int) []PortEvent {
	journalMu.Lock()
	defer journalMu.Unlock()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	b, err := os.ReadFile(filepath.Join(journalDir(), "port-events.jsonl"))
	if err != nil {
		return nil
	}
	var out []PortEvent
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var e PortEvent
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// ---------------------------------------------------------------------------
// 转正账本（漏斗第 4 级的分母——待确认区转正后即从 store 移除，历史只能靠记）
// ---------------------------------------------------------------------------

// RecordPromotion appends one promotion line (best-effort).
func RecordPromotion(device, ip string) {
	journalMu.Lock()
	defer journalMu.Unlock()
	if err := os.MkdirAll(journalDir(), 0o700); err != nil {
		return
	}
	b, err := json.Marshal(map[string]string{"at": time.Now().Format("2006-01-02T15:04:05"), "device": device, "ip": ip})
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(journalDir(), "promotions.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// CountPromotions returns the historical total of 待确认区→纳管 promotions.
func CountPromotions() int {
	journalMu.Lock()
	defer journalMu.Unlock()
	b, err := os.ReadFile(filepath.Join(journalDir(), "promotions.jsonl"))
	if err != nil {
		return 0
	}
	n := strings.Count(string(b), "\n")
	if s := strings.TrimSpace(string(b)); s != "" && !strings.HasSuffix(string(b), "\n") {
		n++
	}
	return n
}

// ---------------------------------------------------------------------------
// R3 — syslog 计数（按天滚动）
// ---------------------------------------------------------------------------

// SyslogCountRow is one {hour, device, class} bucket.
type SyslogCountRow struct {
	Hour   string `json:"hour"` // YYYY-MM-DDTHH
	Device string `json:"device"`
	Class  string `json:"class"`
	N      int    `json:"n"`
}

var (
	syslogCountMu   sync.Mutex
	syslogCountBuf  = map[SyslogCountRow]int{}
	syslogCountLast time.Time
)

// syslogCountIncr bumps the in-memory bucket for one received line (called
// with syslogMu held by the receiver loop is NOT required — its own lock).
func syslogCountIncr(t time.Time, device, class string) {
	syslogCountMu.Lock()
	defer syslogCountMu.Unlock()
	syslogCountBuf[SyslogCountRow{Hour: t.Format("2006-01-02T15"), Device: device, Class: class}]++
}

// FlushSyslogCounts appends pending buckets to the day file (best-effort,
// drains the buffer). Called by the receiver's 30s flusher and by tests.
func FlushSyslogCounts() {
	syslogCountMu.Lock()
	if len(syslogCountBuf) == 0 {
		syslogCountMu.Unlock()
		return
	}
	rows := syslogCountBuf
	syslogCountBuf = map[SyslogCountRow]int{}
	syslogCountMu.Unlock()

	journalMu.Lock()
	defer journalMu.Unlock()
	if err := os.MkdirAll(journalDir(), 0o700); err != nil {
		return
	}
	byFile := map[string][]string{}
	for r, n := range rows {
		b, err := json.Marshal(SyslogCountRow{Hour: r.Hour, Device: r.Device, Class: r.Class, N: n})
		if err != nil {
			continue
		}
		day := r.Hour[:journalMin(10, len(r.Hour))]
		byFile["syslog-counts-"+day+".jsonl"] = append(byFile["syslog-counts-"+day+".jsonl"], string(b))
	}
	for name, lines := range byFile {
		f, err := os.OpenFile(filepath.Join(journalDir(), name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			continue
		}
		for _, l := range lines {
			_, _ = f.WriteString(l + "\n")
		}
		f.Close()
	}
}

// SyslogCountTail returns up to limit newest R3 rows across day files.
func SyslogCountTail(limit int) []SyslogCountRow {
	journalMu.Lock()
	defer journalMu.Unlock()
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	entries, err := os.ReadDir(journalDir())
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "syslog-counts-") && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // day prefix = chronological
	var out []SyslogCountRow
	for _, name := range files {
		b, err := os.ReadFile(filepath.Join(journalDir(), name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var r SyslogCountRow
			if json.Unmarshal([]byte(line), &r) == nil {
				out = append(out, r)
			}
		}
		if len(out) > limit*2 {
			out = out[len(out)-limit*2:]
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// OpenFindingTallies counts open findings by severity (R1 rows carry the
// run-end tallies — the risk-score trend source). Status "resolved" is out.
func OpenFindingTallies() (critical, warning, info int) {
	fs, err := ListFindings()
	if err != nil {
		return 0, 0, 0
	}
	for _, f := range fs {
		if f.Status == "resolved" {
			continue
		}
		switch f.Severity {
		case SeverityCritical:
			critical++
		case SeverityWarning:
			warning++
		default:
			info++
		}
	}
	return critical, warning, info
}

// ---------------------------------------------------------------------------
// 基线汇总持久位（总览 BaselineAgg 的数据源；RunBaseline 写，总览读）
// ---------------------------------------------------------------------------

// BaselineAggView is the JSON-persisted last-run summary.
type BaselineAggView struct {
	Devices int    `json:"devices"`
	Checked int    `json:"checked"`
	Rules   int    `json:"rules"`
	Hits    int    `json:"hits"`
	At      string `json:"at"`
}

// SaveLastBaseline persists the latest baseline summary (best-effort).
func SaveLastBaseline(s BaselineSummary) {
	journalMu.Lock()
	defer journalMu.Unlock()
	if err := os.MkdirAll(journalDir(), 0o700); err != nil {
		return
	}
	v := BaselineAggView{Devices: s.Devices, Checked: s.Checked, Rules: s.Rules, Hits: s.Hits, At: time.Now().Format("2006-01-02T15:04")}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(journalDir(), "baseline-summary.json"), b, 0o600)
}

// LoadLastBaseline reads it back; nil when never run (引导态，不是 0).
func LoadLastBaseline() *BaselineAggView {
	journalMu.Lock()
	defer journalMu.Unlock()
	b, err := os.ReadFile(filepath.Join(journalDir(), "baseline-summary.json"))
	if err != nil {
		return nil
	}
	var v BaselineAggView
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return &v
}

// ---------------------------------------------------------------------------
// ifBrief 行数启发式（R1 行内接口汇总——不是逐口台账，趋势够用）
// ---------------------------------------------------------------------------

var (
	ifBriefUpRe   = regexp.MustCompile(`(?i)\b(up)\b`)
	ifBriefDownRe = regexp.MustCompile(`(?i)\b(down)\b`)
)

// SummarizeIfBrief counts interface rows by link state from a brief-style
// output (huawei `display interface brief` / cisco `show interfaces status`).
// Heuristic line counting — the R1 row is a trend input, not an interface DB.
func SummarizeIfBrief(output string) IfBriefCounts {
	var c IfBriefCounts
	for _, line := range strings.Split(output, "\n") {
		// interface rows carry a PHY/link column; header/blank rows are skipped
		// by requiring at least 3 whitespace-separated fields.
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		lower := strings.ToLower(line)
		// down wins when both words appear: "up * down" is an interface row
		// whose protocol went down (the alarm that matters).
		switch {
		case ifBriefDownRe.MatchString(lower):
			c.Down++
		case ifBriefUpRe.MatchString(lower):
			c.Up++
		}
	}
	return c
}

// journalMin clamps day-prefix slice bounds (test files declare their own
// min, so the name avoids the clash).
func journalMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// 调度执行戳（巡检合规卡的数据源——调度循环写，总览读）
// ---------------------------------------------------------------------------

// ScheduleStamp records the last SCHEDULED run's outcome (manual runs do
// not stamp — the compliance card answers "did the scheduler fire").
type ScheduleStamp struct {
	At    string `json:"at"`   // 2006-01-02T15:04:05
	Kind  string `json:"kind"` // inspection
	Ok    bool   `json:"ok"`
	Title string `json:"title,omitempty"` // the filed Finding's title
	Note  string `json:"note,omitempty"`  // failure note
}

// SaveScheduleStamp persists the latest stamp (best-effort, single file).
func SaveScheduleStamp(st ScheduleStamp) {
	if st.At == "" {
		st.At = time.Now().Format("2006-01-02T15:04:05")
	}
	journalMu.Lock()
	defer journalMu.Unlock()
	if err := os.MkdirAll(journalDir(), 0o700); err != nil {
		return
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(journalDir(), "schedule-last.json"), b, 0o600)
}

// LoadScheduleStamp reads it back; nil = the scheduler never fired.
func LoadScheduleStamp() *ScheduleStamp {
	journalMu.Lock()
	defer journalMu.Unlock()
	b, err := os.ReadFile(filepath.Join(journalDir(), "schedule-last.json"))
	if err != nil {
		return nil
	}
	var st ScheduleStamp
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	return &st
}
