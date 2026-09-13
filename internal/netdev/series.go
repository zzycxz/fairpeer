package netdev

// series.go — 时序面 v2：按设备分片的追加式 JSONL（F11，GAPS 台账批次 F 规模
// 化项）。v1 单文件全扫描在 ~20-30 节点开始退化（100 节点×14 天 ≈ 3.4GB/
// 6860 万行：SeriesRead 每查一台全扫、GpuBoard 构建=100 次全扫、CleanupSeries
// 整文件重写）。v2 布局 = <state>/series/<device>.jsonl：读/写/清理全部只触
// 目标设备的分片，量级从 O(全 fleet) 降到 O(单设备)。零新依赖；sqlite 化
// （spec §5.3 原案）仅在跨设备聚合查询成为需求时再评估。
//
// 迁移：进程内首次触碰 series 时把旧单文件 series.jsonl 一次性拆进分片并
// 改名为 series.jsonl.migrated（读端迁移，GAPS F11 口径）——之后旧文件不再
// 被读，也不阻塞新写入。

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
	seriesMu      sync.Mutex
	seriesPath    string // 旧单文件锚点（迁移源）；测试覆写
	seriesDirPath string // 分片目录锚点；测试覆写
	// seriesMigratedFor 记录已完成迁移的旧文件路径——目录锚点变化（测试/
	// 状态目录搬家）时重新迁移新锚点下的旧文件。
	seriesMigratedFor string
)

func legacySeriesFile() string {
	if seriesPath == "" {
		seriesPath = filepath.Join(netdevStateDir(), "series.jsonl")
	}
	return seriesPath
}

func seriesDir() string {
	if seriesDirPath == "" {
		seriesDirPath = filepath.Join(netdevStateDir(), "series")
	}
	return seriesDirPath
}

// seriesShardPath maps a device to its shard file. Device names from the
// inventory are already path-safe (ndNameRe); defensively sanitize anything
// else (discovered hosts, tests): path separators and whitespace become "_",
// and degenerate results fall back to a hash-ish suffix-free name.
func seriesShardPath(device string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '@', r == '-':
			return r
		}
		return '_'
	}, strings.TrimSpace(device))
	switch name {
	case "", ".", "..":
		name = "unnamed"
	}
	return filepath.Join(seriesDir(), name+".jsonl")
}

// ensureMigrated performs the one-time legacy split and guarantees the shard
// directory exists (O_CREATE won't create parents). Caller holds seriesMu.
func ensureMigrated() {
	_ = os.MkdirAll(seriesDir(), 0o700) // 首次后是廉价 stat；分片写入不因缺父目录静默失败
	legacy := legacySeriesFile()
	if seriesMigratedFor == legacy {
		return
	}
	seriesMigratedFor = legacy // 无论成败都标记：坏文件不重试，别每轮都撞
	in, err := os.Open(legacy)
	if err != nil {
		return // 没有旧文件 = 全新部署
	}
	defer in.Close()
	shards := map[string]*bufio.Writer{}
	files := map[string]*os.File{}
	defer func() {
		for _, w := range shards {
			_ = w.Flush()
		}
		for _, f := range files {
			f.Close()
		}
	}()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	moved := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var p SeriesPoint
		if json.Unmarshal([]byte(line), &p) != nil || p.Device == "" {
			continue // 坏行不迁移（与清理语义一致）
		}
		shard := seriesShardPath(p.Device)
		if _, ok := shards[shard]; !ok {
			f, err := os.OpenFile(shard, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				continue
			}
			files[shard] = f
			shards[shard] = bufio.NewWriter(f)
		}
		if _, err := shards[shard].WriteString(line + "\n"); err == nil {
			moved++
		}
	}
	if serr := sc.Err(); serr != nil {
		// 旧文件读一半失败：保留原文件（改名会丢数据），分片侧已有前半——
		// 重复迁移被 seriesMigratedFor 挡住，不会双写。
		slog.Warn("series: legacy migration aborted midway", "err", serr)
		return
	}
	for _, w := range shards {
		_ = w.Flush()
	}
	for _, f := range files {
		f.Close()
	}
	in.Close() // Windows：打开中的文件不能改名——rename 前显式关掉读端
	_ = os.Rename(legacy, legacy+".migrated")
	slog.Info("series: legacy file split into per-device shards", "points", moved)
}

// RecordSeries appends one point (best-effort; failures are silent — the
// timeline is a convenience layer, never a blocker).
func RecordSeries(device, metric string, v float64) {
	RecordSeriesLabeled(device, metric, nil, v)
}

// RecordSeriesLabeled appends one point with optional labels to the device's
// shard file.
func RecordSeriesLabeled(device, metric string, labels map[string]string, v float64) {
	// NaN/Inf 会写出非法 JSON 字面量（strconv 'g' 直接吐 "NaN"/"+Inf"），
	// 该行将在读取端被静默丢弃——入口直接拒掉，静默性从将来时堵死。
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	seriesMu.Lock()
	defer seriesMu.Unlock()
	ensureMigrated()
	path := seriesShardPath(device)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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
// v2：只扫该设备的分片——单设备 14 天数据 ~34MB/680 万行的量级不再随 fleet
// 规模相乘。
func SeriesRead(device string, window time.Duration) map[string][]SeriesPoint {
	cutoff := time.Now().Add(-window).Unix()
	out := map[string][]SeriesPoint{}
	seriesMu.Lock()
	defer seriesMu.Unlock()
	ensureMigrated()
	f, err := os.Open(seriesShardPath(device))
	if err != nil {
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
		if p.T < cutoff {
			continue
		}
		out[p.Metric] = append(out[p.Metric], p)
	}
	// 一行超过 scanner 上限会让 Scan 提前返回 false——其后所有行本次全部
	// 不可见。打日志留痕（自愈靠 CleanupSeries 重写时丢弃坏行）。
	if err := sc.Err(); err != nil {
		slog.Warn("series: read truncated", "device", device, "err", err)
	}
	return out
}

var seriesCleanupOnce sync.Once

// CleanupSeriesOnce runs CleanupSeries exactly once per process (the 14-day
// retention used to be dead code — wired at poller start and poll time so
// both desktop and headless callers are covered).
func CleanupSeriesOnce() {
	seriesCleanupOnce.Do(CleanupSeries)
}

// CleanupSeries drops points older than the retention, per shard. v2：分片后
// 单文件量级有界，流式实现（批次 B8）原样下沉到每个分片；scanner 中途报错
// 的分片放弃本次清理保留原文件——宁可不清，不能截断。
func CleanupSeries() {
	cutoff := time.Now().Add(-seriesRetention).Unix()
	seriesMu.Lock()
	defer seriesMu.Unlock()
	ensureMigrated()
	entries, err := os.ReadDir(seriesDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(seriesDir(), e.Name())
		cleanupSeriesShard(path, cutoff)
	}
}

func cleanupSeriesShard(path string, cutoff int64) {
	in, err := os.Open(path)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		in.Close()
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
		// 读取中断（超长行等）：放弃本分片清理，保留原文件。
		slog.Warn("series: shard cleanup aborted", "path", path, "err", serr)
		out.Close()
		in.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := w.Flush(); err != nil {
		out.Close()
		in.Close()
		_ = os.Remove(tmp)
		return
	}
	out.Close()
	in.Close() // Windows：打开中的文件不能删除/替换——收尾前关掉读端
	if kept == 0 {
		_ = os.Remove(path) // 空分片直接删——目录保持紧凑
		return
	}
	_ = fileutil.ReplaceFile(tmp, path)
}
