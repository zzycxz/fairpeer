package netdev

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

// vllmSample is a trimmed but shape-faithful vLLM /metrics export (real names,
// HELP/TYPE comments, labels, a histogram family).
const vllmSample = `# HELP vllm:num_requests_running Number of requests currently running on GPU.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="qwen2.5-7b-instruct"} 3.0
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="qwen2.5-7b-instruct"} 12.0
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc{model_name="qwen2.5-7b-instruct"} 0.83
# TYPE vllm:num_preemptions_total counter
vllm:num_preemptions_total{model_name="qwen2.5-7b-instruct"} 7.0
# TYPE vllm:generation_tokens_total counter
vllm:generation_tokens_total{model_name="qwen2.5-7b-instruct"} 245000.0
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{model_name="qwen2.5-7b-instruct",le="0.005"} 1.0
vllm:time_to_first_token_seconds_bucket{model_name="qwen2.5-7b-instruct",le="0.01"} 4.0
vllm:time_to_first_token_seconds_sum{model_name="qwen2.5-7b-instruct"} 3.2
vllm:time_to_first_token_seconds_count{model_name="qwen2.5-7b-instruct"} 8.0
# TYPE vllm:e2e_request_latency_seconds histogram
vllm:e2e_request_latency_seconds_sum{model_name="qwen2.5-7b-instruct"} 16.0
vllm:e2e_request_latency_seconds_count{model_name="qwen2.5-7b-instruct"} 8.0
badline-without-value
vllm:broken{ 1.0
vllm:nan_metric NaN
vllm:ok_metric 42.0 1726200000000
`

func TestParsePromText(t *testing.T) {
	samples := parsePromText(vllmSample)
	byName := map[string]promSample{}
	for _, s := range samples {
		byName[s.name] = s
	}
	if got := byName["vllm:num_requests_running"]; got.value != 3.0 || got.labels["model_name"] != "qwen2.5-7b-instruct" {
		t.Errorf("gauge parse wrong: %+v", got)
	}
	if got := byName["vllm:gpu_cache_usage_perc"]; got.value != 0.83 {
		t.Errorf("kv parse wrong: %+v", got)
	}
	if _, ok := byName["vllm:nan_metric"]; ok {
		t.Error("NaN sample must be dropped")
	}
	if _, ok := byName["vllm:broken"]; ok {
		t.Error("malformed line must be skipped")
	}
	if got := byName["vllm:ok_metric"]; got.value != 42.0 {
		t.Errorf("timestamp-suffixed sample wrong: %+v", got)
	}
	if got := byName["vllm:time_to_first_token_seconds_count"]; got.value != 8.0 {
		t.Errorf("histogram count wrong: %+v", got)
	}
	// 转义标签值。
	esc := parsePromText(`vllm:x{model_name="a\"b\\c", other="x,y"} 1.0`)
	if len(esc) != 1 || esc[0].labels["model_name"] != `a"b\c` || esc[0].labels["other"] != "x,y" {
		t.Errorf("label escapes wrong: %+v", esc)
	}
}

func TestReduceAndDelta(t *testing.T) {
	resetMetricsPrev()
	t.Cleanup(resetMetricsPrev)
	samples := parsePromText(vllmSample)
	raw := reduceSamples(samples)["qwen2.5-7b-instruct"]
	if raw == nil || raw.running != 3 || raw.queued != 12 || raw.kvUsagePerc != 0.83 {
		t.Fatalf("reduce wrong: %+v", raw)
	}
	t0 := time.Now()
	// 首轮：只有 gauge 点——速率/均值缺基线不造数。
	first := metricsDelta("d|8000|m", t0, raw)
	if _, ok := first["infer.ttft_ms"]; ok {
		t.Error("first round must not produce ttft_ms")
	}
	if first["infer.kv_usage"] != 83 {
		t.Errorf("kv_usage want 83 (percent), got %v", first["infer.kv_usage"])
	}
	// 下一轮：计数器前移 → 速率点出现。
	raw2 := &inferRaw{kvUsagePerc: 0.95, running: 5, queued: 2,
		preemptTotal: 9, tokensTotal: 265000,
		ttftSumS: 6.2, ttftCount: 18, e2eSumS: 31.0, e2eCount: 18}
	second := metricsDelta("d|8000|m", t0.Add(2*time.Minute), raw2)
	if got := second["infer.preemptions_rate"]; got != 1.0 { // (9-7)/2min
		t.Errorf("preemptions_rate want 1/min, got %v", got)
	}
	if got := second["infer.tokens_rate"]; got != 500.0/3.0 { // 20000/120s
		t.Errorf("tokens_rate want %v, got %v", 500.0/3.0, got)
	}
	if got := second["infer.ttft_ms"]; got != 300.0 { // (6.2-3.2)/(18-8)*1000
		t.Errorf("ttft_ms want 300, got %v", got)
	}
	if got := second["infer.e2e_ms"]; got != 1500.0 { // (31-16)/(18-8)*1000
		t.Errorf("e2e_ms want 1500, got %v", got)
	}
	// 导出器重启（计数器回绕）：负增量跳过、基线就地重置。
	raw3 := &inferRaw{preemptTotal: 1, tokensTotal: 500}
	third := metricsDelta("d|8000|m", t0.Add(4*time.Minute), raw3)
	if _, ok := third["infer.preemptions_rate"]; ok {
		t.Error("counter reset must not produce a rate point")
	}
}

func inferSeriesTestEnv(t *testing.T) *Manager {
	t.Helper()
	seriesTestAnchors(t)
	findingsDirOverr = t.TempDir()
	SetAuditPath(t.TempDir() + "/audit.jsonl")
	cfg := config.Default()
	cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "gpu1", Vendor: "linux", GPU: true, MetricsPorts: []int{8000}},
		{Name: "plain", Vendor: "linux", GPU: false},
	}
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	return m
}

func TestInferAlertRuleEvaluation(t *testing.T) {
	m := inferSeriesTestEnv(t)
	// 未登记端点的主机：冻结闸（latestInferValue 无数据 → 不参与比较由闸完成）。
	if m.deviceHasMetricsEndpoints("plain") {
		t.Error("plain host must not pass the infer gate")
	}
	if !m.deviceHasMetricsEndpoints("gpu1") {
		t.Error("registered host must pass the infer gate")
	}
	// 落两个 svc 的 kv_usage：跨服务取最大。
	RecordSeriesLabeled("gpu1", "infer.kv_usage", map[string]string{"svc": "8000"}, 70)
	RecordSeriesLabeled("gpu1", "infer.kv_usage", map[string]string{"svc": "8001"}, 92)
	v, ok := latestInferValue("gpu1", "infer.kv_usage")
	if !ok || v != 92 {
		t.Fatalf("want max 92, got %v ok=%v", v, ok)
	}
	h := DeviceHealth{Device: "gpu1"}
	if got := ruleMetricValue("infer.kv_usage", h, 0); got != 92 {
		t.Errorf("ruleMetricValue want 92, got %v", got)
	}
	// 过期窗口无数据 → 0（由闸与 for_rounds 兜底，不会误触发 >= 规则）。
	if got := ruleMetricValue("infer.ttft_ms", h, 0); got != 0 {
		t.Errorf("missing metric want 0, got %v", got)
	}
	// 未登记主机的设备名：latestInferValue 也拿不到数据。
	if got := ruleMetricValue("infer.kv_usage", DeviceHealth{Device: "plain"}, 0); got != 0 {
		t.Errorf("unregistered host want 0, got %v", got)
	}
}

func TestBuildServicesBoardZone(t *testing.T) {
	inferSeriesTestEnv(t)
	now := time.Now().Unix()
	pt := func(name, svc string, v float64, ageSec int64) {
		RecordSeriesLabeled("gpu1", name, map[string]string{"svc": svc, "model": "qwen"}, float64(v))
		_ = ageSec // 时间由 Record 决定——直接写文件以便控制时间戳
	}
	_ = pt
	// 直写分片控时间戳：kv 最新 95、均值算另一 svc。
	write := func(name, svc string, ts int64, v float64) {
		line := `{"t":` + strconv.FormatInt(ts, 10) + `,"d":"gpu1","m":` + strconv.Quote(name) +
			`,"v":` + strconv.FormatFloat(v, 'g', -1, 64) + `,"l":{"svc":"` + svc + `","model":"qwen"}}` + "\n"
		f, err := os.OpenFile(seriesShardPath("gpu1"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(line)
		f.Close()
	}
	_ = os.MkdirAll(seriesDir(), 0o700)
	write("infer.kv_usage", "8000", now-60, 95)
	write("infer.running", "8000", now-60, 4)
	write("infer.queued", "8000", now-60, 2)
	write("infer.ttft_ms", "8000", now-600, 200) // 10min 前：窗内
	write("infer.ttft_ms", "8000", now-60, 300)  // 1min 前
	rows := buildServicesBoard("gpu1", 15*time.Minute)
	if len(rows) != 1 {
		t.Fatalf("want 1 service row, got %d", len(rows))
	}
	r := rows[0]
	if r.Svc != "8000" || r.Model != "qwen" || r.KVUsage != 95 || r.Running != 4 || r.Queued != 2 {
		t.Errorf("gauges wrong: %+v", r)
	}
	if r.TTFTms != 250 { // 15min 窗均值 (200+300)/2
		t.Errorf("ttft avg want 250, got %v", r.TTFTms)
	}
	// 未登记端点的设备不出行（board 循环以登记为准，这里直接验 build 的空态）。
	if rows := buildServicesBoard("ghost", 15*time.Minute); rows != nil {
		t.Errorf("ghost device must produce no rows, got %+v", rows)
	}
	if strings.Contains("", "never") {
		t.Fail()
	}
}
