package netdev

// series.go — 时序面 v1（NETDEV_SPEC_V2 §5.3）：追加式 JSONL 存储。每设备
// 每指标每次轮询一行，量级极小；读取按时间窗过滤，14 天滚动清理。spec 写
// sqlite——v1 用 JSONL 零新依赖达成同一契约，sqlite 化随 R6 规模化再换。
// 采集入口是 health 轮询（SNMP 可达性/掉线接口数/uptime）；docker/k8s 指标
// 随各自采集器后续接入。

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SeriesPoint struct {
	T      int64   `json:"t"` // unix seconds
	Device string  `json:"d"`
	Metric string  `json:"m"`
	Value  float64 `json:"v"`
}

const seriesRetention = 14 * 24 * time.Hour

var (
	seriesMu   sync.Mutex
	seriesPath string
)

func seriesFile() string {
	if seriesPath == "" {
		seriesPath = filepath.Join(netdevStateDir(), "series.jsonl")
	}
	return seriesPath
}

// RecordSeries appends one point (best-effort; failures are silent — the
// timeline is a convenience layer, never a blocker).
func RecordSeries(device, metric string, v float64) {
	seriesMu.Lock()
	defer seriesMu.Unlock()
	f, err := os.OpenFile(seriesFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line := `{"t":` + strconv.FormatInt(time.Now().Unix(), 10) + `,"d":` + quoteJSON(device) + `,"m":` + quoteJSON(metric) + `,"v":` + strconv.FormatFloat(v, 'g', -1, 64) + "}\n"
	_, _ = f.WriteString(line)
}

func quoteJSON(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// SeriesRead returns one device's points (all metrics) inside the window.
func SeriesRead(device string, window time.Duration) map[string][]SeriesPoint {
	cutoff := time.Now().Add(-window).Unix()
	out := map[string][]SeriesPoint{}
	seriesMu.Lock()
	f, err := os.Open(seriesFile())
	if err != nil {
		seriesMu.Unlock()
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		var p SeriesPoint
		line := strings.TrimSpace(sc.Text())
		if line == "" || json.Unmarshal([]byte(line), &p) != nil {
			continue
		}
		if p.Device != device || p.T < cutoff {
			continue
		}
		out[p.Metric] = append(out[p.Metric], p)
	}
	seriesMu.Unlock()
	return out
}

// CleanupSeries drops points older than the retention (called opportunistically
// on app start; a rewrite-in-place under the lock).
func CleanupSeries() {
	cutoff := time.Now().Add(-seriesRetention).Unix()
	seriesMu.Lock()
	defer seriesMu.Unlock()
	raw, err := os.ReadFile(seriesFile())
	if err != nil {
		return
	}
	var kept []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var p SeriesPoint
		if json.Unmarshal([]byte(line), &p) == nil && p.T >= cutoff {
			kept = append(kept, line)
		}
	}
	_ = os.WriteFile(seriesFile(), []byte(strings.Join(kept, "\n")+"\n"), 0o600)
}
