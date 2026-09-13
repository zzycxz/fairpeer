package netdev

// infermetrics.go — F14 /metrics 通用 Prometheus 文本抓取通道 + K3
// vllm:*→infer.* 映射（0.2.5 批④；MODEL_DEPLOY_SPEC D-2、GAPS 台账
// F3/F14/K3）。metrics.go 是健康历史库，本文件是推理指标面。
//
// 端点登记制：只有清单设备声明的 metrics_ports 会被抓——这是新的网络出口类
// （工作站→设备 HTTP GET），登记本身就是授权；不走共享代理、10s 超时、
// best-effort（失败只留审计行，不影响健康判定）。
//
// 解析器按 Prometheus 文本 exposition 的公共子集实现（name{labels} value
// [timestamp]；# HELP/# TYPE 跳过；NaN/Inf 行跳过），一套通道覆盖 vLLM/
// SGLang/任何 /metrics 端点——映射表按引擎前缀择行（K3 现收 vLLM，新引擎=
// 加映射行，解析器不动）。
//
// 语义分层（K3 映射表详见 docs/NETDEV_INFER_METRICS.md）：
//   - gauge 直接落 series（kv_usage 存百分数 0-100）；
//   - counter/histogram 落的是【轮内增量派生值】（preemptions_rate 次/分、
//     tokens_rate tok/s、ttft_ms/e2e_ms 增量平均）——累计值对值班没有可
//     设阈的意义。增量状态在内存（metricsPrev），进程重启后首轮无速率点
//     （缺历史基线，诚实缺采，不造 0）。
//
// 告警消费：infer.* 规则从 series 读最新点（跨服务取最大——最忙的实例
// 触发）。服务层区（GpuBoard）从 series 15 分钟窗聚合。

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// promSample is one parsed exposition line.
type promSample struct {
	name   string
	labels map[string]string
	value  float64
}

// parsePromText parses the Prometheus text exposition common subset. Bad lines
// are skipped (a metrics body is advisory data — one broken exporter line must
// not void the round), NaN/Inf samples dropped (the series guard refuses them
// anyway).
func parsePromText(text string) []promSample {
	out := []promSample{}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest := splitPromName(line)
		if name == "" {
			continue
		}
		var labels map[string]string
		if strings.HasPrefix(rest, "{") {
			end := strings.Index(rest, "}")
			if end < 0 {
				continue
			}
			labels = parsePromLabels(rest[1:end])
			rest = rest[end+1:]
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 || len(fields) > 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		out = append(out, promSample{name: name, labels: labels, value: v})
	}
	return out
}

// splitPromName extracts the metric name (Prometheus ident: letters, digits,
// '_', ':') from the head of a sample line.
func splitPromName(line string) (string, string) {
	i := 0
	for i < len(line) {
		c := line[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == ':' {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return "", line
	}
	return line[:i], strings.TrimLeft(line[i:], " \t")
}

// parsePromLabels parses `k="v",k2="v2"` with the three documented escapes.
func parsePromLabels(s string) map[string]string {
	labels := map[string]string{}
	for _, part := range splitPromLabelPairs(s) {
		eq := strings.Index(part, "=")
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(part[:eq])
		v := strings.TrimSpace(part[eq+1:])
		v = strings.TrimPrefix(v, `"`)
		v = strings.TrimSuffix(v, `"`)
		labels[k] = unescapePromLabel(v)
	}
	return labels
}

// splitPromLabelPairs splits on commas outside quotes (values may contain ',').
func splitPromLabelPairs(s string) []string {
	var parts []string
	var b strings.Builder
	inQ, esc := false, false
	for _, r := range s {
		if esc {
			b.WriteRune(r)
			esc = false
			continue
		}
		switch {
		case r == '\\' && inQ:
			b.WriteRune(r)
			esc = true
		case r == '"':
			inQ = !inQ
			b.WriteRune(r)
		case r == ',' && !inQ:
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	parts = append(parts, b.String())
	return parts
}

func unescapePromLabel(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n").Replace(s)
}

// ── K3 映射（vllm:* → infer.*）────────────────────────────────────────────

// inferRaw carries the raw gauge/counter values one scrape contributed, before
// delta computation.
type inferRaw struct {
	kvUsagePerc  float64 // vllm:gpu_cache_usage_perc ×100 → infer.kv_usage
	running      float64 // vllm:num_requests_running → infer.running
	queued       float64 // vllm:num_requests_waiting → infer.queued
	preemptTotal float64 // vllm:num_preemptions_total → infer.preemptions_rate
	tokensTotal  float64 // vllm:generation_tokens_total → infer.tokens_rate
	ttftSumS     float64 // vllm:time_to_first_token_seconds_sum → infer.ttft_ms
	ttftCount    float64 // vllm:time_to_first_token_seconds_count
	e2eSumS      float64 // vllm:e2e_request_latency_seconds_sum → infer.e2e_ms
	e2eCount     float64 // vllm:e2e_request_latency_seconds_count
}

// reduceSamples folds one endpoint scrape's samples into inferRaw per model
// label (vLLM exports per-model series; the empty model label is the
// single-instance case).
func reduceSamples(samples []promSample) map[string]*inferRaw {
	out := map[string]*inferRaw{}
	for _, s := range samples {
		var r *inferRaw
		switch s.name {
		case "vllm:gpu_cache_usage_perc", "vllm:num_requests_running",
			"vllm:num_requests_waiting", "vllm:num_preemptions_total",
			"vllm:generation_tokens_total",
			"vllm:time_to_first_token_seconds_sum", "vllm:time_to_first_token_seconds_count",
			"vllm:e2e_request_latency_seconds_sum", "vllm:e2e_request_latency_seconds_count":
			model := s.labels["model_name"]
			r = out[model]
			if r == nil {
				r = &inferRaw{}
				out[model] = r
			}
		default:
			continue
		}
		switch s.name {
		case "vllm:gpu_cache_usage_perc":
			r.kvUsagePerc = s.value
		case "vllm:num_requests_running":
			r.running = s.value
		case "vllm:num_requests_waiting":
			r.queued = s.value
		case "vllm:num_preemptions_total":
			r.preemptTotal = s.value
		case "vllm:generation_tokens_total":
			r.tokensTotal = s.value
		case "vllm:time_to_first_token_seconds_sum":
			r.ttftSumS = s.value
		case "vllm:time_to_first_token_seconds_count":
			r.ttftCount = s.value
		case "vllm:e2e_request_latency_seconds_sum":
			r.e2eSumS = s.value
		case "vllm:e2e_request_latency_seconds_count":
			r.e2eCount = s.value
		}
	}
	return out
}

// metricsPrevSnap is the delta baseline for one (device, port, model) key.
type metricsPrevSnap struct {
	t                                    time.Time
	preemptTotal, tokensTotal            float64
	ttftSumS, ttftCount, e2eSumS, e2eCnt float64
}

var (
	metricsPrev   = map[string]metricsPrevSnap{}
	metricsPrevMu sync.Mutex
)

// metricsDelta computes per-round derived points from raw values vs the
// previous snapshot. Counters only produce points when a baseline exists AND
// moved forward (exporter restart resets counters → negative delta skipped,
// baseline re-armed on the spot).
func metricsDelta(key string, now time.Time, raw *inferRaw) map[string]float64 {
	out := map[string]float64{
		"infer.kv_usage": raw.kvUsagePerc * 100,
		"infer.running":  raw.running,
		"infer.queued":   raw.queued,
	}
	metricsPrevMu.Lock()
	prev, had := metricsPrev[key]
	metricsPrev[key] = metricsPrevSnap{
		t: now, preemptTotal: raw.preemptTotal, tokensTotal: raw.tokensTotal,
		ttftSumS: raw.ttftSumS, ttftCount: raw.ttftCount,
		e2eSumS: raw.e2eSumS, e2eCnt: raw.e2eCount,
	}
	metricsPrevMu.Unlock()
	if !had {
		return out // 首轮：只有 gauge 点——速率/均值缺基线不造数
	}
	dt := now.Sub(prev.t)
	if dt <= 0 {
		return out
	}
	if raw.preemptTotal >= prev.preemptTotal {
		out["infer.preemptions_rate"] = (raw.preemptTotal - prev.preemptTotal) / dt.Minutes()
	}
	if raw.tokensTotal >= prev.tokensTotal {
		out["infer.tokens_rate"] = (raw.tokensTotal - prev.tokensTotal) / dt.Seconds()
	}
	if raw.ttftCount > prev.ttftCount {
		out["infer.ttft_ms"] = (raw.ttftSumS - prev.ttftSumS) / (raw.ttftCount - prev.ttftCount) * 1000
	}
	if raw.e2eCount > prev.e2eCnt {
		out["infer.e2e_ms"] = (raw.e2eSumS - prev.e2eSumS) / (raw.e2eCount - prev.e2eCnt) * 1000
	}
	return out
}

// resetMetricsPrev clears delta baselines (tests only — production restarts
// re-arm by the missing-baseline path).
func resetMetricsPrev() {
	metricsPrevMu.Lock()
	metricsPrev = map[string]metricsPrevSnap{}
	metricsPrevMu.Unlock()
}

// ── 抓取通道 ────────────────────────────────────────────────────────────────

var metricsHTTPClient = &http.Client{Timeout: 10 * time.Second}

// pollMetricsEndpoints scrapes every registered endpoint and folds the mapped
// points into series. Called from PollHealthOnce BEFORE evaluateAlerts so
// infer.* rules read this round's values.
func (m *Manager) pollMetricsEndpoints(ctx context.Context) {
	for i := range m.cfg.NetDev.Devices {
		d := m.cfg.NetDev.Devices[i]
		if len(d.MetricsPorts) == 0 {
			continue
		}
		path := d.MetricsPath
		if path == "" {
			path = "/metrics"
		}
		for _, port := range d.MetricsPorts {
			select {
			case <-ctx.Done():
				return
			default:
			}
			m.scrapeEndpoint(ctx, d.Name, d.Address, port, path)
		}
	}
}

// metricsBodyCap bounds how much of a response enters memory — a runaway
// exporter must not OOM the workstation (8MB ≈ 40× a large vLLM body).
const metricsBodyCap = 8 * 1024 * 1024

// scrapeEndpoint GETs one registered endpoint, maps via K3, records series,
// and leaves one audit line per round per endpoint (not per metric — 审计降噪
// 与 gpuhealth 轮询同一口径）。
func (m *Manager) scrapeEndpoint(ctx context.Context, device, addr string, port int, path string) {
	url := fmt.Sprintf("http://%s:%d%s", addr, port, path)
	svc := fmt.Sprintf("%d", port)
	// 直接 AppendAudit（m.audit 面向设备命令执行；HTTP 抓取没有 driver.Class
	// 语境，按读类等价标注，URL 过中心脱敏）。
	auditScrape := func(status string, n int) {
		_ = AppendAudit(Audit{Device: device, Command: "GET " + Redact(url), Class: "read", Status: status, OutputBytes: n})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		auditScrape(AuditRefused, 0)
		return
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := metricsHTTPClient.Do(req)
	if err != nil {
		slog.Warn("metrics: scrape failed", "device", device, "url", url, "err", err)
		auditScrape(AuditFailure, 0)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("metrics: scrape non-200", "device", device, "url", url, "status", resp.StatusCode)
		auditScrape(AuditFailure, 0)
		return
	}
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 && len(buf) < metricsBodyCap {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	auditScrape(AuditOK, len(buf))
	now := time.Now()
	byModel := reduceSamples(parsePromText(string(buf)))
	for _, model := range sortedInferModels(byModel) {
		raw := byModel[model]
		labels := map[string]string{"svc": svc}
		if model != "" {
			labels["model"] = model
		}
		key := device + "|" + svc + "|" + model
		for name, v := range metricsDelta(key, now, raw) {
			RecordSeriesLabeled(device, name, labels, v)
		}
	}
}

func sortedInferModels(m map[string]*inferRaw) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// inferRuleWindow bounds how fresh a series point may be for alert evaluation
// (10 min: a few poll intervals — a stalled endpoint stops feeding rules and
// the freeze semantics take over rather than firing on stale data).
const inferRuleWindow = 10 * time.Minute

// latestInferValue returns the freshest point value for one infer.* metric,
// max across svc labels (the busiest instance triggers the rule). ok=false
// when nothing fresh exists — the rule freezes instead of comparing 0.
func latestInferValue(device, metric string) (float64, bool) {
	series := SeriesRead(device, inferRuleWindow)
	pts := series[metric]
	if len(pts) == 0 {
		return 0, false
	}
	latest := map[string]SeriesPoint{} // svc → newest point
	for _, p := range pts {
		svc := p.Labels["svc"]
		if cur, ok := latest[svc]; !ok || p.T >= cur.T {
			latest[svc] = p
		}
	}
	best := 0.0
	found := false
	for _, p := range latest {
		if !found || p.Value > best {
			best, found = p.Value, true
		}
	}
	return best, found
}

// deviceHasMetricsEndpoints reports whether the device registered any
// metrics_ports — the freeze gate for infer.* rules (unregistered hosts never
// participate; no false fires on a fleet that never opted in).
func (m *Manager) deviceHasMetricsEndpoints(name string) bool {
	d, ok := m.cfg.NetDevDeviceByName(name)
	return ok && len(d.MetricsPorts) > 0
}

// ── GpuBoard 服务层区（批④消费面三）───────────────────────────────────────

// GPUBoardService is one registered inference service's aggregated row.
type GPUBoardService struct {
	Device   string  `json:"device"`
	Svc      string  `json:"svc"` // 登记端口（字符串标签）
	Model    string  `json:"model,omitempty"`
	AgeMin   int     `json:"ageMin"` // 数据新鲜度（分钟；0=刚刚）
	KVUsage  int     `json:"kvUsage"`
	Running  int     `json:"running"`
	Queued   int     `json:"queued"`
	TTFTms   float64 `json:"ttftMs,omitempty"` // 15 分钟窗均值
	E2Ems    float64 `json:"e2eMs,omitempty"`  // 同上
	PreemptR float64 `json:"preemptRate,omitempty"`
	TokensR  float64 `json:"tokensRate,omitempty"`
}

// buildServicesBoard assembles the 推理服务 rows from the series window: one
// row per (device, svc, model) with fresh data; gauges take the latest point,
// latency/rate columns take the window mean (per-poll derived values averaged
// smooth per-poll noise).
func buildServicesBoard(device string, window time.Duration) []GPUBoardService {
	series := SeriesRead(device, window)
	if len(series) == 0 {
		return nil
	}
	type agg struct {
		svc, model                         string
		kv, running, queued                SeriesPoint
		ttftSum, ttftN, e2eSum, e2eN       float64
		preemptSum, preemptN, tokSum, tokN float64
		newest                             int64
	}
	aggs := map[string]*agg{}
	for metric, pts := range series {
		if !strings.HasPrefix(metric, "infer.") {
			continue
		}
		for _, p := range pts {
			key := p.Labels["svc"] + "|" + p.Labels["model"]
			a := aggs[key]
			if a == nil {
				a = &agg{svc: p.Labels["svc"], model: p.Labels["model"], newest: p.T}
				aggs[key] = a
			}
			if p.T > a.newest {
				a.newest = p.T
			}
			switch metric {
			case "infer.kv_usage":
				if p.T >= a.kv.T {
					a.kv = p
				}
			case "infer.running":
				if p.T >= a.running.T {
					a.running = p
				}
			case "infer.queued":
				if p.T >= a.queued.T {
					a.queued = p
				}
			case "infer.ttft_ms":
				a.ttftSum += p.Value
				a.ttftN++
			case "infer.e2e_ms":
				a.e2eSum += p.Value
				a.e2eN++
			case "infer.preemptions_rate":
				a.preemptSum += p.Value
				a.preemptN++
			case "infer.tokens_rate":
				a.tokSum += p.Value
				a.tokN++
			}
		}
	}
	age := func(t int64) int { return int(time.Now().Unix()-t) / 60 }
	out := []GPUBoardService{}
	for _, a := range aggs {
		s := GPUBoardService{
			Device: device, Svc: a.svc, Model: a.model, AgeMin: age(a.newest),
			KVUsage: int(a.kv.Value), Running: int(a.running.Value), Queued: int(a.queued.Value),
		}
		if a.ttftN > 0 {
			s.TTFTms = a.ttftSum / a.ttftN
		}
		if a.e2eN > 0 {
			s.E2Ems = a.e2eSum / a.e2eN
		}
		if a.preemptN > 0 {
			s.PreemptR = a.preemptSum / a.preemptN
		}
		if a.tokN > 0 {
			s.TokensR = a.tokSum / a.tokN
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Device != out[j].Device {
			return out[i].Device < out[j].Device
		}
		return out[i].Svc < out[j].Svc
	})
	return out
}
