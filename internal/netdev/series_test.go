package netdev

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// seriesTestAnchors 重置包级锚点，让每个测试落在自己的 TempDir。
func seriesTestAnchors(t *testing.T) {
	t.Helper()
	netdevStateDirOverr = t.TempDir()
	seriesPath = ""
	seriesDirPath = ""
	seriesMigratedFor = ""
	t.Cleanup(func() {
		netdevStateDirOverr = ""
		seriesPath = ""
		seriesDirPath = ""
		seriesMigratedFor = ""
	})
}

// F11 核心断言：读端只触目标设备的分片——另一设备的大数据量不进本设备的
// 读取路径（v1 里这是全文件扫描）。
func TestSeriesShardIsolation(t *testing.T) {
	seriesTestAnchors(t)
	RecordSeries("dev-a", "reachable", 1)
	RecordSeries("dev-b", "reachable", 1)
	RecordSeries("dev-b", "reachable", 1)

	if got := len(SeriesRead("dev-a", time.Hour)["reachable"]); got != 1 {
		t.Errorf("dev-a want 1 point, got %d", got)
	}
	if got := len(SeriesRead("dev-b", time.Hour)["reachable"]); got != 2 {
		t.Errorf("dev-b want 2 points, got %d", got)
	}
	// 分片文件物理分离。
	if _, err := os.Stat(seriesShardPath("dev-a")); err != nil {
		t.Errorf("shard file missing: %v", err)
	}
	entries, _ := os.ReadDir(seriesDir())
	if len(entries) != 2 {
		t.Errorf("want exactly 2 shard files, got %d", len(entries))
	}
}

// 迁移：旧单文件在首次触碰时拆进分片并改名 .migrated；读端此后只见分片。
func TestSeriesLegacyMigration(t *testing.T) {
	seriesTestAnchors(t)
	legacy := filepath.Join(netdevStateDir(), "series.jsonl")
	body := `{"t":` + itoa64(time.Now().Add(-time.Hour).Unix()) + `,"d":"old-a","m":"m1","v":1}` + "\n" +
		`{"t":` + itoa64(time.Now().Add(-time.Hour).Unix()) + `,"d":"old-b","m":"m1","v":2}` + "\n" +
		"not-json-garbage\n"
	if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// 触碰读取端即迁移。
	got := SeriesRead("old-a", 24*time.Hour)
	if len(got["m1"]) != 1 {
		t.Fatalf("migrated point missing: %v", got)
	}
	if _, err := os.Stat(legacy + ".migrated"); err != nil {
		t.Errorf("legacy file must be renamed: %v", err)
	}
	// 坏行不迁移；另一设备分片同样就位。
	if pts := SeriesRead("old-b", 24*time.Hour)["m1"]; len(pts) != 1 || pts[0].Value != 2 {
		t.Errorf("old-b migration wrong: %v", pts)
	}
	// 迁移只做一次：旧文件路径已标记，后续读不再重试（幂等）。
	SeriesRead("old-a", 24*time.Hour)
	if pts := SeriesRead("old-a", 24*time.Hour)["m1"]; len(pts) != 1 {
		t.Errorf("repeat read must not duplicate: %d", len(pts))
	}
}

// 迁移+新写并存：迁移后 RecordSeries 落分片，读取两侧数据都可见。
func TestSeriesMigrationThenWrite(t *testing.T) {
	seriesTestAnchors(t)
	legacy := filepath.Join(netdevStateDir(), "series.jsonl")
	if err := os.WriteFile(legacy, []byte(`{"t":`+itoa64(time.Now().Add(-2*time.Hour).Unix())+`,"d":"x","m":"m1","v":7}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	RecordSeries("x", "m2", 8) // 首次写触发迁移
	got := SeriesRead("x", 24*time.Hour)
	if len(got["m1"]) != 1 || len(got["m2"]) != 1 {
		t.Errorf("want m1=1 (migrated) + m2=1 (fresh), got %v", got)
	}
}

// 清理：过期点被丢弃、空分片被删除、快照内分片保留。
func TestSeriesShardCleanup(t *testing.T) {
	seriesTestAnchors(t)
	old := time.Now().Add(-30 * 24 * time.Hour).Unix()
	now := time.Now().Unix()
	write := func(dev string, ts int64) {
		p := seriesShardPath(dev)
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte(`{"t":`+itoa64(ts)+`,"d":"`+dev+`","m":"m","v":1}`+"\n"), 0o600)
	}
	write("all-old", old) // 全过期 → 分片删除
	write("all-new", now) // 全保留
	write("mixed", old)   // 先写旧行
	f, _ := os.OpenFile(seriesShardPath("mixed"), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString(`{"t":` + itoa64(now) + `,"d":"mixed","m":"m","v":2}` + "\n")
	f.Close()

	CleanupSeries()
	if _, err := os.Stat(seriesShardPath("all-old")); !os.IsNotExist(err) {
		t.Errorf("empty shard must be removed, err=%v", err)
	}
	if pts := SeriesRead("mixed", 0)["m"]; len(pts) != 1 || pts[0].Value != 2 {
		t.Errorf("mixed shard cleanup wrong: %v", pts)
	}
	if pts := SeriesRead("all-new", 0)["m"]; len(pts) != 1 {
		t.Errorf("fresh shard must survive cleanup: %v", pts)
	}
}

// 分片名的路径安全：注入型设备名不得逃出分片目录。
func TestSeriesShardPathSanitize(t *testing.T) {
	seriesTestAnchors(t)
	dir := seriesDir()
	for _, dev := range []string{"../../etc/passwd", "a/b", "a\\b", "..", ".", "", "sp ace"} {
		p := seriesShardPath(dev)
		if !strings.HasPrefix(p, dir+string(filepath.Separator)) {
			t.Errorf("device %q shard %q escapes series dir", dev, p)
		}
		if strings.Contains(p, "..") && filepath.Base(p) == ".." {
			t.Errorf("device %q produced parent-path shard", dev)
		}
	}
	if filepath.Base(seriesShardPath("../..")) == ".." {
		t.Error("dotdot device must sanitize to a safe name")
	}
}

func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}
