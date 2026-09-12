package netdev

// series.go — 时序面 v1（NETDEV_SPEC_V2 §5.3）：追加式 JSONL 存储。每设备
// 每指标每次轮询一行，量级极小；读取按时间窗过滤，14 天滚动清理。spec 写
// sqlite——v1 用 JSONL 零新依赖达成同一契约，sqlite 化随 R6 规模化再换。
// 采集入口是 health 轮询（SNMP 可达性/掉线接口数/uptime）；docker/k8s 指标
// 随各自采集器后续接入。

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/fileutil"
)

type SeriesPoint struct {
	T      int64   `json:"t"` // unix seconds
	Device string  `json:"d"`
	Metric string  `json:"m"`
	Value  float64 `json:"v"`
	// Labels（FDE_AIINFRA gap §4.1-2）：可选标签面（如 {"gpu":"0"}）。读取端
	// 仍按 (device, metric) 分组——GPU 指标走 gpu.<index>.<metric> 命名规范
	// 自然分组，labels 供后续聚合查询用（spec §5.3 的 labels_json 前置）。
	Labels map[string]string `json:"l,omitempty"`
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
	RecordSeriesLabeled(device, metric, nil, v)
}

// RecordSeriesLabeled appends one point with optional labels.
func RecordSeriesLabeled(device, metric string, labels map[string]string, v float64) {
	// NaN/Inf 会写出非法 JSON 字面量（strconv 'g' 直接吐 "NaN"/"+Inf"），
	// 该行将在读取端被静默丢弃——入口直接拒掉，静默性从将来时堵死。
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	seriesMu.Lock()
	defer seriesMu.Unlock()
	f, err := os.OpenFile(seriesFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line := `{"t":` + strconv.FormatInt(time.Now().Unix(), 10) + `,"d":` + quoteJSON(device) + `,"m":` + quoteJSON(metric) + `,"v":` + strconv.FormatFloat(v, 'g', -1, 64)
	if len(labels) > 0 {
		lb, _ := json.Marshal(labels)
		line += `,"l":` + string(lb)
	}
	line += "}\n"
	_, _ = f.WriteString(line)
}

// quoteJSON 双引号包裹并转义。与手写 replacer 的区别在控制字符（\n 会把一行
// JSONL 撕成两段坏行）——json.Marshal 的字符串编码覆盖全部转义，错误不可
// 能（string 输入），忽略 err 是安全的。
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
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
	// 一行超过 scanner 上限会让 Scan 提前返回 false——其后所有行本次全部
	// 不可见。打日志留痕（自愈靠 CleanupSeries 重写时丢弃坏行）。
	if err := sc.Err(); err != nil {
		slog.Warn("series: read truncated", "device", device, "err", err)
	}
	seriesMu.Unlock()
	return out
}

var seriesCleanupOnce sync.Once

// CleanupSeriesOnce runs CleanupSeries exactly once per process (the 14-day
// retention used to be dead code — wired at poller start and poll time so
// both desktop and headless callers are covered).
func CleanupSeriesOnce() {
	seriesCleanupOnce.Do(CleanupSeries)
}

// CleanupSeries drops points older than the retention. 流式实现（批次 B8）：
// scanner 逐行读 + 临时文件 + ReplaceFile——整文件 ReadFile 入内存在 GPU
// 通道接入后会到数百 MB。scanner 中途报错（坏行超长等）则放弃本次清理
// 保留原文件——宁可不清，不能截断。
func CleanupSeries() {
	cutoff := time.Now().Add(-seriesRetention).Unix()
	seriesMu.Lock()
	defer seriesMu.Unlock()
	in, err := os.Open(seriesFile())
	if err != nil {
		return
	}
	defer in.Close()
	tmp := seriesFile() + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	w := bufio.NewWriter(out)
	kept := 0
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var p SeriesPoint
		if json.Unmarshal([]byte(line), &p) != nil || p.T < cutoff {
			continue
		}
		if _, err := w.WriteString(line); err == nil {
			_ = w.WriteByte('\n')
			kept++
		}
	}
	if serr := sc.Err(); serr != nil {
		// 读取中断（超长行等）：放弃本次清理，保留原文件。
		slog.Warn("series: cleanup aborted", "err", serr)
		out.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := w.Flush(); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return
	}
	out.Close()
	_ = os.MkdirAll(filepath.Dir(seriesFile()), 0o700)
	if kept == 0 {
		_ = fileutil.AtomicWriteFile(seriesFile(), []byte(""), 0o600)
		return
	}
	_ = fileutil.ReplaceFile(tmp, seriesFile())
}
