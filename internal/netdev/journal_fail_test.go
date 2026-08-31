package netdev

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Journal failure injection (DASHBOARD spec §9.11): journal writes are
// best-effort — a broken state dir must never panic, and producers that
// ignore the error keep working (the store reads stay empty, not fatal).
func TestJournalFailureInjection(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := journalDirOverr
	journalDirOverr = filepath.Join(blocker, "sub") // MkdirAll fails: parent is a file
	t.Cleanup(func() { journalDirOverr = prev })

	if err := AppendInspectionRow(InspectionJournalRow{Kind: "inspection"}); err == nil {
		t.Error("AppendInspectionRow must surface the error for callers that care")
	}
	RecordPromotion("SW-x", "10.0.0.9") // best-effort: no panic, no error return
	if n := CountPromotions(); n != 0 {
		t.Errorf("promotions through broken dir = %d", n)
	}
	AppendPortEvent("10.0.0.9", 22, "newly-opened") // best-effort: silent
	if ev := ListPortEvents(10); len(ev) != 0 {
		t.Errorf("port events through broken dir = %+v", ev)
	}
	syslogCountIncr(time.Now(), "SW-x", "other")
	FlushSyslogCounts() // best-effort: drains buffer, write fails silently
	if rows := SyslogCountTail(10); len(rows) != 0 {
		t.Errorf("syslog counts through broken dir = %+v", rows)
	}
}

// ScheduleStamp roundtrip（巡检合规卡数据源）：nil=从未调度，写后可读回。
func TestScheduleStampRoundtrip(t *testing.T) {
	journalTestDir(t)
	if LoadScheduleStamp() != nil {
		t.Fatal("no stamp yet must read nil")
	}
	SaveScheduleStamp(ScheduleStamp{Kind: "inspection", Ok: true, Title: "巡检 3 台设备，0 项异常"})
	st := LoadScheduleStamp()
	if st == nil || !st.Ok || st.Kind != "inspection" || st.Title == "" || st.At == "" {
		t.Fatalf("stamp = %+v", st)
	}
	SaveScheduleStamp(ScheduleStamp{Kind: "inspection", Ok: false, Note: "boom"})
	if st = LoadScheduleStamp(); st == nil || st.Ok || st.Note != "boom" {
		t.Fatalf("overwrite = %+v", st)
	}
}
