package netdev

// metrics.go — the health-metric history store (自适应的地基): every health
// poll appends one row per device into a shared pure-Go SQLite database
// (<state>/metrics.db). Ring semantics via trimming (keep the newest
// retainRows per device). On top of the history two derived signals feed the
// alert engine: flap_count (up↔down transitions in a window) and
// if_down_above_p90 (current down-interfaces minus the 90th percentile of
// history — "worse than usual" instead of a static threshold).

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// MetricPoint is one poll's rollup for one device.
type MetricPoint struct {
	Time      time.Time `json:"time"`
	Reachable bool      `json:"up"`
	UptimeSec int64     `json:"us"`
	IfUp      int       `json:"iu"`
	IfDown    int       `json:"id"`
	// 水位（DASHBOARD spec §7.3 SNMP OID 扩展）：cpu/mem 为百分比，octets
	// 为 ifTable 计数器累计和——速率（bps）由读取端按相邻点差分计算。
	Cpu    int    `json:"cu,omitempty"`
	Mem    int    `json:"me,omitempty"`
	InOct  uint64 `json:"io,omitempty"`
	OutOct uint64 `json:"oo,omitempty"`
}

const metricsRetainRows = 20160 // 7 days @ 30s; 14 days @ 1min

var (
	metricsMu   sync.Mutex
	metricsDB   *sql.DB
	metricsPath string // test override
)

func metricsFile() string {
	if metricsPath != "" {
		return metricsPath
	}
	return filepath.Join(netdevStateDir(), "metrics.db")
}

func metricsOpen() (*sql.DB, error) {
	if db := func() *sql.DB { metricsMu.Lock(); defer metricsMu.Unlock(); return metricsDB }(); db != nil {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(metricsFile()), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", metricsFile()+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(3000)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS metric_points (
		device TEXT NOT NULL,
		ts     INTEGER NOT NULL,
		up     INTEGER NOT NULL,
		uptime INTEGER NOT NULL,
		if_up  INTEGER NOT NULL,
		if_dn  INTEGER NOT NULL,
		cpu    INTEGER NOT NULL DEFAULT 0,
		mem    INTEGER NOT NULL DEFAULT 0,
		in_oct  INTEGER NOT NULL DEFAULT 0,
		out_oct INTEGER NOT NULL DEFAULT 0
	); CREATE INDEX IF NOT EXISTS idx_metric_device_ts ON metric_points(device, ts);`); err != nil {
		db.Close()
		return nil, err
	}
	// 迁移：IF NOT EXISTS 建表不会给旧库补列——PRAGMA 对账后 ALTER 补齐。
	if _, err := db.Exec(`ALTER TABLE metric_points ADD COLUMN cpu INTEGER NOT NULL DEFAULT 0`); err == nil {
		_, _ = db.Exec(`ALTER TABLE metric_points ADD COLUMN mem INTEGER NOT NULL DEFAULT 0`)
		_, _ = db.Exec(`ALTER TABLE metric_points ADD COLUMN in_oct INTEGER NOT NULL DEFAULT 0`)
		_, _ = db.Exec(`ALTER TABLE metric_points ADD COLUMN out_oct INTEGER NOT NULL DEFAULT 0`)
	}
	metricsMu.Lock()
	metricsDB = db
	metricsMu.Unlock()
	return db, nil
}

// RecordMetricPoint appends one poll rollup and trims the ring.
func RecordMetricPoint(device string, p MetricPoint) error {
	db, err := metricsOpen()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT INTO metric_points (device, ts, up, uptime, if_up, if_dn, cpu, mem, in_oct, out_oct) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		device, p.Time.Unix(), b2i(p.Reachable), p.UptimeSec, p.IfUp, p.IfDown, p.Cpu, p.Mem, int64(p.InOct), int64(p.OutOct)); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM metric_points WHERE device=? AND ts <= (
		SELECT ts FROM metric_points WHERE device=? ORDER BY ts DESC LIMIT 1 OFFSET ?)`,
		device, device, metricsRetainRows); err != nil {
		return err
	}
	return nil
}

// MetricHistory returns the device's newest-first points (bounded).
func MetricHistory(device string, limit int) []MetricPoint {
	if limit <= 0 || limit > metricsRetainRows {
		limit = 720
	}
	db, err := metricsOpen()
	if err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT ts, up, uptime, if_up, if_dn, cpu, mem, in_oct, out_oct FROM metric_points WHERE device=? ORDER BY ts DESC LIMIT ?`, device, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []MetricPoint
	for rows.Next() {
		var ts int64
		var up, cpu, mem int
		var inOct, outOct int64
		var p MetricPoint
		if err := rows.Scan(&ts, &up, &p.UptimeSec, &p.IfUp, &p.IfDown, &cpu, &mem, &inOct, &outOct); err != nil {
			return out
		}
		p.Cpu, p.Mem, p.InOct, p.OutOct = cpu, mem, uint64(inOct), uint64(outOct)
		p.Time = time.Unix(ts, 0)
		p.Reachable = up == 1
		out = append(out, p)
	}
	return out
}

// FlapCount counts reachability up↔down transitions within the window. The
// row limit assumes a poll interval ≥10s (window/10 rows covers the window
// even at the densest supported cadence).
func FlapCount(device string, window time.Duration) int {
	pts := MetricHistory(device, int(window.Seconds()/10)+8)
	cutoff := time.Now().Add(-window)
	var chron []MetricPoint
	for i := len(pts) - 1; i >= 0; i-- { // newest-first → oldest-first
		if pts[i].Time.After(cutoff) {
			chron = append(chron, pts[i])
		}
	}
	flaps := 0
	for i := 1; i < len(chron); i++ {
		if chron[i].Reachable != chron[i-1].Reachable {
			flaps++
		}
	}
	return flaps
}

// P90IfDown is the 90th percentile of ifDown over the window; ok=false when
// history is too thin to judge "usual" (callers treat that as no signal).
func P90IfDown(device string, window time.Duration) (int, bool) {
	pts := MetricHistory(device, int(window.Seconds()/10)+8)
	cutoff := time.Now().Add(-window)
	var vals []int
	for _, p := range pts {
		if p.Time.After(cutoff) {
			vals = append(vals, p.IfDown)
		}
	}
	if len(vals) < 20 {
		return 0, false
	}
	sort.Ints(vals)
	return vals[int(float64(len(vals)-1)*0.9)], true
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
