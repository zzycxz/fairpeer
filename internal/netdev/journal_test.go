package netdev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// journal_test.go — L1 三件留存（DASHBOARD spec §7.2/§9.11）：
// best-effort 纪律、压实策略、端口突变序列（开→关→重开）、syslog 计数桶。

func journalTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := journalDirOverr
	journalDirOverr = dir
	t.Cleanup(func() { journalDirOverr = prev })
	return dir
}

func TestAppendAndReadInspectionRows(t *testing.T) {
	dir := journalTestDir(t)
	if err := AppendInspectionRow(InspectionJournalRow{Kind: "inspection", Devices: 3, Checked: 3, Critical: 0, Warning: 1, Info: 2}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := AppendInspectionRow(InspectionJournalRow{Kind: "baseline", Devices: 3, Checked: 2, BaselineHits: 4}); err != nil {
		t.Fatal(err)
	}
	rows := ReadInspectionRows(10)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Kind != "inspection" || rows[1].Kind != "baseline" {
		t.Errorf("order broken: %+v", rows)
	}
	if rows := ReadInspectionRows(1); len(rows) != 1 || rows[0].Kind != "baseline" {
		t.Errorf("limit = %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(dir, "inspections.jsonl")); err != nil {
		t.Error(err)
	}
}

func TestCompactInspectionsFoldsOldRows(t *testing.T) {
	dir := journalTestDir(t)
	// Force compaction to run: stale stamp + one old row + one fresh row.
	old := time.Now().AddDate(0, 0, -120).Format("2006-01-02T15:04:05")
	fresh := time.Now().Format("2006-01-02T15:04:05")
	f, _ := os.Create(filepath.Join(dir, "inspections.jsonl"))
	f.WriteString(`{"at":"` + old + `","kind":"inspection","devices":5,"critical":1,"warning":2,"info":3}` + "\n")
	f.WriteString(`{"at":"` + fresh + `","kind":"inspection","devices":2,"critical":0,"warning":0,"info":1}` + "\n")
	f.Close()
	_ = os.WriteFile(filepath.Join(dir, ".inspections-compacted"), []byte("2000-01-01"), 0o600)

	if err := AppendInspectionRow(InspectionJournalRow{Kind: "inspection", Devices: 1}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "inspections.jsonl"))
	if strings.Contains(string(raw), old) {
		t.Errorf("old row survived compaction: %s", raw)
	}
	daily, err := os.ReadFile(filepath.Join(dir, "inspections-daily.jsonl"))
	if err != nil || !strings.Contains(string(daily), `"kind":"daily"`) {
		t.Fatalf("daily fold missing: %v %s", err, daily)
	}
	rows := ReadInspectionRows(10)
	if len(rows) != 3 { // folded daily(1) + fresh(1) + appended(1)
		t.Errorf("rows after compaction = %d, want 3: %+v", len(rows), rows)
	}
}

func TestPortEventsOpenCloseReopen(t *testing.T) {
	dir := journalTestDir(t)
	prev := discoveredDirOverr
	discoveredDirOverr = dir
	t.Cleanup(func() { discoveredDirOverr = prev })

	mk := func(ports ...int) []DiscoverPortProbe {
		var out []DiscoverPortProbe
		for _, p := range ports {
			out = append(out, DiscoverPortProbe{Port: p, Open: true})
		}
		return out
	}
	// First sweep: 22+23 open.
	if err := RecordDiscoveredSwept(SourceDiscover, []DiscoverHostResult{
		{IP: "10.1.1.5", Ports: mk(22, 23)},
	}, []int{22, 23, 161}); err != nil {
		t.Fatal(err)
	}
	// Second sweep: only 22 answers → 23 closes.
	if err := RecordDiscoveredSwept(SourceDiscover, []DiscoverHostResult{
		{IP: "10.1.1.5", Ports: mk(22)},
	}, []int{22, 23, 161}); err != nil {
		t.Fatal(err)
	}
	// Third sweep: 23 re-opens.
	if err := RecordDiscoveredSwept(SourceDiscover, []DiscoverHostResult{
		{IP: "10.1.1.5", Ports: mk(22, 23)},
	}, []int{22, 23, 161}); err != nil {
		t.Fatal(err)
	}

	events := ListPortEvents(10)
	kinds := map[string]int{}
	for _, e := range events {
		if e.IP != "10.1.1.5" {
			continue
		}
		kinds[e.Kind]++
	}
	if kinds["newly-opened"] != 3 || kinds["newly-closed"] != 1 {
		t.Errorf("events = %+v (want opened=3 closed=1)", events)
	}

	hosts, _ := ListDiscoveredHosts()
	if len(hosts) != 1 || len(hosts[0].Ports) != 2 {
		t.Fatalf("final ports = %+v", hosts)
	}
	for _, p := range hosts[0].Ports {
		if p.FirstSeen.IsZero() {
			t.Errorf("port %d missing FirstSeen", p.Port)
		}
	}
}

func TestPortFirstSeenBackfill(t *testing.T) {
	dir := journalTestDir(t)
	prev := discoveredDirOverr
	discoveredDirOverr = dir
	t.Cleanup(func() { discoveredDirOverr = prev })

	// A pre-R2 record: port rows without first_seen.
	at := time.Now().Add(-time.Hour)
	old := DiscoveredHost{IP: "10.1.1.9", FirstSeen: at, LastSeen: at,
		Ports: []DiscoveredPort{{Port: 22, At: at}}}
	b, _ := json.Marshal(old)
	_ = os.WriteFile(filepath.Join(dir, "10.1.1.9.json"), b, 0o600)

	_ = RecordDiscovered(SourceDiscover, []DiscoverHostResult{
		{IP: "10.1.1.9", Ports: []DiscoverPortProbe{{Port: 22, Open: true}}},
	})
	hosts, _ := ListDiscoveredHosts()
	if len(hosts) != 1 || hosts[0].Ports[0].FirstSeen.IsZero() {
		t.Fatalf("backfill failed: %+v", hosts)
	}
	// No newly-opened event for an already-known port.
	for _, e := range ListPortEvents(10) {
		if e.IP == "10.1.1.9" {
			t.Errorf("spurious event %+v", e)
		}
	}
}

func TestPromotionJournal(t *testing.T) {
	journalTestDir(t)
	RecordPromotion("SW-99", "10.2.2.9")
	RecordPromotion("R-99", "10.2.2.10")
	if n := CountPromotions(); n != 2 {
		t.Errorf("promotions = %d", n)
	}
}

func TestSyslogCountsBucketAndFlush(t *testing.T) {
	dir := journalTestDir(t)
	now := time.Now()
	syslogCountIncr(now, "SW-01", "ospf-adjacency")
	syslogCountIncr(now, "SW-01", "ospf-adjacency")
	syslogCountIncr(now, "(unknown)", "other")
	FlushSyslogCounts()

	rows := SyslogCountTail(50)
	got := map[string]int{}
	for _, r := range rows {
		got[r.Device+"/"+r.Class] = r.N
	}
	if got["SW-01/ospf-adjacency"] != 2 {
		t.Errorf("bucket = %+v", rows)
	}
	if got["(unknown)/other"] != 1 {
		t.Errorf("unknown bucket = %+v", rows)
	}
	// Flush drains: a second flush writes nothing new.
	FlushSyslogCounts()
	if rows2 := SyslogCountTail(50); len(rows2) != len(rows) {
		t.Errorf("flush not drained: %d vs %d", len(rows2), len(rows))
	}
	_ = dir
}

func TestSummarizeIfBrief(t *testing.T) {
	out := `Interface   PHY   Protocol  InErrors
GE0/0/1    up    up        0
GE0/0/2    up   down       3
Vlan100    down  down      0`
	c := SummarizeIfBrief(out)
	if c.Up != 1 || c.Down != 2 {
		t.Errorf("counts = %+v (want up=1 down=2)", c)
	}
}

func TestOpenFindingTallies(t *testing.T) {
	dir := journalTestDir(t)
	prev := findingsDirOverr
	findingsDirOverr = dir
	t.Cleanup(func() { findingsDirOverr = prev })

	ev := []Evidence{{Device: "SW-01", Command: "(test)", Output: "(redacted)"}}
	_ = SaveFinding(&Finding{ID: "TF-1", Title: "c1", Severity: SeverityCritical, Status: "active", Evidence: ev})
	_ = SaveFinding(&Finding{ID: "TF-2", Title: "w1", Severity: SeverityWarning, Evidence: ev})
	_ = SaveFinding(&Finding{ID: "TF-3", Title: "i1", Severity: SeverityInfo, Evidence: ev})
	_ = SaveFinding(&Finding{ID: "TF-4", Title: "gone", Severity: SeverityCritical, Status: "resolved", Evidence: ev})
	c, w, i := OpenFindingTallies()
	if c != 1 || w != 1 || i != 1 {
		t.Errorf("tallies = %d/%d/%d", c, w, i)
	}
}

func TestSaveLoadLastBaseline(t *testing.T) {
	journalTestDir(t)
	if LoadLastBaseline() != nil {
		t.Fatal("no baseline yet must read nil (引导态)")
	}
	SaveLastBaseline(BaselineSummary{Devices: 4, Checked: 3, Rules: 41, Hits: 2})
	b := LoadLastBaseline()
	if b == nil || b.Devices != 4 || b.Checked != 3 || b.Rules != 41 || b.Hits != 2 {
		t.Fatalf("baseline = %+v", b)
	}
}
