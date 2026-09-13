package netdev

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Audit P2: a metrics.db that isn't a usable SQLite database (truncated copy,
// disk fault) used to fail every open forever. The store must move the corrupt
// file aside (keeping it on disk for inspection) and recreate fresh.
func TestMetricsCorruptDBRecovery(t *testing.T) {
	metricTestDB(t)
	if err := os.WriteFile(metricsPath, []byte("this is definitely not a sqlite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RecordMetricPoint("sw1", MetricPoint{Time: time.Now(), Reachable: true}); err != nil {
		t.Fatalf("record must recover from a corrupt db: %v", err)
	}
	aside, err := filepath.Glob(metricsPath + ".corrupt-*")
	if err != nil || len(aside) != 1 {
		t.Fatalf("expected the corrupt file moved aside, got %v (err %v)", aside, err)
	}
	if hist := MetricHistory("sw1", 10); len(hist) != 1 {
		t.Fatalf("fresh db must serve the recorded point, got %d rows", len(hist))
	}
}

// Audit P3: the migration used to gate every ALTER on the first one succeeding
// (`ADD COLUMN cpu` err == nil), so a crash between the four left the gate
// closed forever. The probe must add each missing column independently — a
// pre-migration (column-less) database with existing rows must come up fully
// migrated, keeping its history.
func TestMetricsMigrationProbesEachColumn(t *testing.T) {
	metricTestDB(t)
	// Hand-craft the pre-cpu schema with one historical row.
	old, err := sql.Open("sqlite", metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE metric_points (
		device TEXT NOT NULL, ts INTEGER NOT NULL, up INTEGER NOT NULL,
		uptime INTEGER NOT NULL, if_up INTEGER NOT NULL, if_dn INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO metric_points VALUES ('sw1', 100, 1, 60, 8, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen through the store path: the migration must add all four columns
	// even though the first ALTER ("cpu") will report "already exists" never —
	// none of them exist, so this only proves the probe path runs per column;
	// the old row surviving with zeroed extensions proves the columns landed.
	if err := RecordMetricPoint("sw1", MetricPoint{Time: time.Unix(200, 0), Reachable: true, Cpu: 42, Mem: 55}); err != nil {
		t.Fatalf("record on a pre-migration db must migrate and write: %v", err)
	}
	hist := MetricHistory("sw1", 10)
	if len(hist) != 2 {
		t.Fatalf("rows = %d, want 2 (old row must survive the migration)", len(hist))
	}
	newest := hist[0]
	if newest.Cpu != 42 || newest.Mem != 55 {
		t.Errorf("newest point cpu/mem = %d/%d, want 42/55", newest.Cpu, newest.Mem)
	}
	oldest := hist[len(hist)-1]
	if oldest.Time.Unix() != 100 {
		t.Errorf("old row lost: ts = %d, want 100", oldest.Time.Unix())
	}
}
