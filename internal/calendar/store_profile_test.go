package calendar

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Partition tests for the calendar store: one SQLite, a profile column,
// legacy rows backfill to cowork, and the ForProfile queries back the
// agent-facing tools (the human UI keeps the unscoped methods).

func farFutureWindow() (time.Time, time.Time) {
	return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
}

func TestStoreProfilePartitions(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "cal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mk := func(title, profile string) *Event {
		now := time.Date(2099, 6, 24, 9, 0, 0, 0, time.UTC)
		e := &Event{Title: title, StartTime: now, EndTime: now.Add(time.Hour), Profile: profile}
		if err := s.Create(e); err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return e
	}
	devEvent := mk("dev-event", "dev")
	cowEvent := mk("cow-event", "cowork")
	_ = cowEvent

	// Partition-scoped list.
	wSince, wBefore := farFutureWindow()
	devOnly, err := s.ListForProfile("dev", wSince, wBefore)
	if err != nil {
		t.Fatal(err)
	}
	if len(devOnly) != 1 || devOnly[0].Title != "dev-event" {
		t.Fatalf("ListForProfile(dev) = %v", devOnly)
	}

	// Partition-scoped search.
	hits, err := s.SearchForProfile("dev", "event", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Profile != "dev" {
		t.Fatalf("SearchForProfile(dev) = %v", hits)
	}

	// Cross-partition Get reports not-found (no existence oracle).
	if _, err := s.GetForProfile(devEvent.ID, "cowork"); err == nil {
		t.Fatal("GetForProfile across partitions must fail")
	}
	if _, err := s.GetForProfile(devEvent.ID, "dev"); err != nil {
		t.Fatalf("GetForProfile same partition: %v", err)
	}

	// Default on create: empty profile → cowork (the legacy cabinet).
	legacy := mk("legacy-event", "")
	if legacy.Profile != "cowork" {
		t.Errorf("empty profile should default to cowork on create, got %q", legacy.Profile)
	}

	// Unscoped list sees all four (human UI path).
	all, err := s.List(wSince, wBefore)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unscoped List = %d events, want 3", len(all))
	}
}

// TestMigrateBackfillsLegacyProfiles: a database created BEFORE the profile
// column existed (schema without it) must migrate in place, backfilling every
// legacy row to the cowork cabinet.
func TestMigrateBackfillsLegacyProfiles(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Build a pre-partition database directly: the old schema, one row, no
	// profile column.
	old, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE events (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    location TEXT DEFAULT '',
    start_time DATETIME NOT NULL,
    end_time DATETIME NOT NULL,
    all_day INTEGER DEFAULT 0,
    timezone TEXT DEFAULT 'Asia/Shanghai',
    color TEXT DEFAULT '',
    status TEXT DEFAULT 'confirmed',
    source TEXT DEFAULT 'manual',
    recurrence TEXT DEFAULT '',
    recurrence_end DATETIME,
    reminders TEXT DEFAULT '[]',
    task_id TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    reminded_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2099, 6, 24, 9, 0, 0, 0, time.UTC)
	if _, err := old.Exec(`INSERT INTO events (id, title, start_time, end_time) VALUES ('legacy1', 'old-event', ?, ?)`,
		now.UTC(), now.Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	// Open through the normal path: migrate() adds the column + backfills.
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, err := s.Get("legacy1")
	if err != nil {
		t.Fatal(err)
	}
	if NormalizeProfile(e.Profile) != "cowork" {
		t.Fatalf("legacy row should backfill to cowork, got profile %q", e.Profile)
	}
	// And it shows up in the cowork partition query.
	wSince, wBefore := farFutureWindow()
	rows, err := s.ListForProfile("cowork", wSince, wBefore)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Title != "old-event" {
		t.Fatalf("backfilled row missing from cowork partition: %v", rows)
	}
}
