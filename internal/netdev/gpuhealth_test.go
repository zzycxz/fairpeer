package netdev

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

// gpuTestManager — 轻量 Manager：只驱动 evaluateAlerts / gpuXidFinding 这类
// 纯评估路径，不拨任何设备。
func gpuTestManager(t *testing.T, rules []config.NetDevAlertRule) *Manager {
	t.Helper()
	findingsDirOverr = t.TempDir()
	t.Cleanup(func() { findingsDirOverr = "" })
	SetAuditPath(t.TempDir() + "/audit.jsonl")
	t.Cleanup(func() { SetAuditPath("") })
	cfg := config.Default()
	cfg.NetDev.AlertRules = rules
	m := NewManager(cfg)
	t.Cleanup(m.Close)
	// 全局健康态清理（gpuMergeState/healthState 由 pollGPUDevices 写入）
	t.Cleanup(func() {
		healthMu.Lock()
		healthState = map[string]DeviceHealth{}
		gpuMergeState = map[string]DeviceHealth{}
		healthMu.Unlock()
	})
	// 包级评估态跨测试污染防护
	alertStreaksMu.Lock()
	alertStreaks = map[string]int{}
	alertStreaksMu.Unlock()
	prevUptimesMu.Lock()
	prevUptimes = map[string]int64{}
	prevUptimesMu.Unlock()
	return m
}

func TestParseGPUCSV(t *testing.T) {
	out := "index, name, temperature.gpu, memory.used, memory.total, utilization.gpu\r\n" +
		"0, NVIDIA A100-SXM4-40GB, 37, 1234, 40960, 0\n" +
		"1, NVIDIA A100-SXM4-40GB, 91, 38000, 40960, 97\n" +
		"2, NVIDIA A100-SXM4-40GB, N/A, [N/A], 40960, [Not Supported]\n"
	cards, notes := parseGPUCSV(out)
	if len(cards) != 2 {
		t.Fatalf("want 2 cards (N/A row skipped), got %d (%+v)", len(cards), cards)
	}
	if len(notes) == 0 {
		t.Error("skipped card must leave a note")
	}
	if cards[0].Index != 0 || cards[0].TempC != 37 || cards[0].MemUsedMB != 1234 || cards[0].MemTotalMB != 40960 || cards[0].UtilPct != 0 {
		t.Errorf("card0 parse wrong: %+v", cards[0])
	}
	if cards[1].TempC != 91 || cards[1].UtilPct != 97 {
		t.Errorf("card1 parse wrong: %+v", cards[1])
	}
	if got := cards[1].MemPct(); got != 92 { // 38000*100/40960 = 92 (int)
		t.Errorf("mem_pct want 92, got %d", got)
	}
	if cards[0].MemPct() != 3 {
		t.Errorf("card0 mem_pct want 3, got %d", cards[0].MemPct())
	}
	// CRLF 已容错：行数与 LF 输入一致。
	cards2, _ := parseGPUCSV("0, X, 30, 1, 2, 3\r\n")
	if len(cards2) != 1 {
		t.Errorf("CRLF tolerance broken: %+v", cards2)
	}
}

func TestExtractXIDs(t *testing.T) {
	out := "    Events                : Xid 79\n" +
		"    something else\n" +
		"        Xid 43 occurred\n" +
		"        Xid 79 again\n"
	codes, lines := extractXIDs(out)
	if len(codes) != 2 || codes[0] != 43 || codes[1] != 79 {
		t.Errorf("codes want [43 79], got %v", codes)
	}
	if len(lines) != 3 {
		t.Errorf("evidence lines want 3, got %d (%v)", len(lines), lines)
	}
	if c, _ := extractXIDs("no events here"); len(c) != 0 {
		t.Errorf("clean output must yield no codes, got %v", c)
	}
	// dmesg/journalctl 形态（旧 [^0-9]* 跨不过 PCI 段）。
	kernel := "Thu Sep 11 03:14:15 2026 NVRM: Xid (PCI:0000:04:00.0): 79, pid=1234\n" +
		"NVRM: Xid (PCI:0000:04:00.1): 13\n"
	codes, _ = extractXIDs(kernel)
	if len(codes) != 2 || codes[0] != 13 || codes[1] != 79 {
		t.Errorf("kernel-form codes want [13 79], got %v", codes)
	}
	// "Xid 0" 与非事件散文不得产出代码。
	if c, _ := extractXIDs("Xid 0 seen; Xid errors since boot tracked elsewhere"); len(c) != 0 {
		t.Errorf("prose/zero must not yield codes, got %v", c)
	}
}

func TestGPUMetricValues(t *testing.T) {
	h := DeviceHealth{
		Device: "g1", Reachable: true, GPUSampled: true, GPUXIDMax: 79,
		GPU: []GPUCard{
			{Index: 0, TempC: 61, MemUsedMB: 1000, MemTotalMB: 40960},
			{Index: 1, TempC: 88, MemUsedMB: 38000, MemTotalMB: 40960},
		},
	}
	if got := ruleMetricValue("gpu.xid", h, 0); got != 79 {
		t.Errorf("gpu.xid want 79, got %g", got)
	}
	if got := ruleMetricValue("gpu.temp", h, 0); got != 88 {
		t.Errorf("gpu.temp want 88 (max across cards), got %g", got)
	}
	if got := ruleMetricValue("gpu.mem_pct", h, 0); got != 92 {
		t.Errorf("gpu.mem_pct want 92, got %g", got)
	}
	if got := ruleMetricValue("gpu.count", h, 0); got != 2 {
		t.Errorf("gpu.count want 2, got %g", got)
	}
	// 未采样的设备：GPU 指标一律 0（配合 evaluateAlerts 的跳过 = 双保险）。
	empty := DeviceHealth{Device: "n1", Reachable: true}
	if got := ruleMetricValue("gpu.temp", empty, 0); got != 0 {
		t.Errorf("unsampled gpu.temp want 0, got %g", got)
	}
}

// for_rounds：条件须连续 N 轮成立才立案；一轮不成立即断流并自动恢复；
// 未采样设备不参与 gpu.* 规则（非 GPU 舰队零误报）。
func TestGPUAlertForRounds(t *testing.T) {
	m := gpuTestManager(t, []config.NetDevAlertRule{
		{Name: "hot", Metric: "gpu.temp", Op: ">=", Value: 85, Severity: "warning", Enabled: true, ForRounds: 2},
	})
	hot := map[string]DeviceHealth{"g1": {Device: "g1", Reachable: true, GPUSampled: true,
		GPU: []GPUCard{{Index: 0, TempC: 88}}}}
	cool := map[string]DeviceHealth{"g1": {Device: "g1", Reachable: true, GPUSampled: true,
		GPU: []GPUCard{{Index: 0, TempC: 40}}}}
	unsampled := map[string]DeviceHealth{"g1": {Device: "g1", Reachable: true}}

	m.evaluateAlerts(hot) // 第 1 轮热：streak 1 < 2，不立案
	if fs, _ := ListFindings(); len(fs) != 0 {
		t.Fatalf("round 1 must not fire (for_rounds=2), got %d findings", len(fs))
	}
	m.evaluateAlerts(hot) // 第 2 轮热：立案
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Status != "active" || fs[0].Source != "alert:hot:g1" {
		t.Fatalf("round 2 must fire one active finding, got %+v", fs)
	}
	if alertStreak("alert:hot:g1") != 2 {
		t.Errorf("streak want 2, got %d", alertStreak("alert:hot:g1"))
	}
	m.evaluateAlerts(cool) // 降温：自动恢复
	fs, _ = ListFindings()
	if len(fs) != 1 || fs[0].Status != "resolved" {
		t.Fatalf("cool round must resolve, got %+v", fs)
	}
	if alertStreak("alert:hot:g1") != 0 {
		t.Errorf("streak must reset on clear, got %d", alertStreak("alert:hot:g1"))
	}
	m.evaluateAlerts(hot)
	m.evaluateAlerts(unsampled) // 未采样：规则整条跳过，不断不立
	if got := alertStreak("alert:hot:g1"); got != 1 {
		t.Errorf("unsampled round must not touch the streak, want 1 got %d", got)
	}
}

// XID → Finding：severe 代码 critical、其余 warning；同设备去重；证据带命令
// 输出；事件清除自动 resolve（多卡聚合 = 每设备一条）。
func TestGPUXidFinding(t *testing.T) {
	m := gpuTestManager(t, nil)
	hot := DeviceHealth{Device: "gpu-1", Reachable: true, GPUSampled: true, GPUXIDSeen: true,
		GPUXIDMax: 79, GPUXIDCodes: []int{43, 79},
		GPUXIDEvidence: []string{"Events : Xid 79"},
		GPU:            []GPUCard{{Index: 0}, {Index: 1}}}
	m.gpuXidFinding("gpu-1", hot)
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Severity != SeverityCritical {
		t.Fatalf("XID 79 must file one critical finding, got %+v", fs)
	}
	if len(fs[0].Evidence) == 0 || fs[0].Evidence[0].Command != gpuJournalXidCmd {
		t.Errorf("finding must carry command evidence (default source = kernel log), got %+v", fs[0].Evidence)
	}
	m.gpuXidFinding("gpu-1", hot) // 同轮重复：不重复立案
	fs, _ = ListFindings()
	if len(fs) != 1 {
		t.Fatalf("duplicate XID round must dedup, got %d", len(fs))
	}

	mild := DeviceHealth{Device: "gpu-2", Reachable: true, GPUSampled: true, GPUXIDSeen: true,
		GPUXIDMax: 13, GPUXIDCodes: []int{13},
		GPUXIDEvidence: []string{"Xid 13 occurred"}}
	m.gpuXidFinding("gpu-2", mild)
	fs, _ = ListFindings()
	if len(fs) != 2 {
		t.Fatalf("expected two findings (gpu-1 + gpu-2), got %d", len(fs))
	}
	for _, f := range fs {
		if f.Source == "gpu:xid:gpu-2" && f.Severity != SeverityWarning {
			t.Errorf("XID 13 (contained) must be warning, got %+v", f)
		}
	}

	clear := DeviceHealth{Device: "gpu-1", Reachable: true, GPUSampled: true, GPUXIDSeen: true}
	m.gpuXidFinding("gpu-1", clear)
	fs, _ = ListFindings()
	for _, f := range fs {
		if f.Source == "gpu:xid:gpu-1" && f.Status != "resolved" {
			t.Fatalf("cleared XID must auto-resolve, got %+v", f)
		}
	}
}

// 采样闸 + 升级（/P1-6）：采集失败的轮次（GPUSampled=false，
// XIDMax=0）既不 resolve 活动卡也不立案；先 warning 后 severe 代码要升级原卡。
func TestGPUXidFindingSampleGateAndUpgrade(t *testing.T) {
	m := gpuTestManager(t, nil)
	mild := DeviceHealth{Device: "gpu-3", Reachable: true, GPUSampled: true, GPUXIDSeen: true,
		GPUXIDMax: 13, GPUXIDCodes: []int{13}, GPUXIDEvidence: []string{"Xid 13 occurred"}}
	m.gpuXidFinding("gpu-3", mild)
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Severity != SeverityWarning {
		t.Fatalf("baseline warning finding expected, got %+v", fs)
	}

	// 采样失败轮：不 resolve、不立案、不升级。
	unsampled := DeviceHealth{Device: "gpu-3", GPUSampled: false, GPULastError: "connection refused"}
	m.gpuXidFinding("gpu-3", unsampled)
	fs, _ = ListFindings()
	if len(fs) != 1 || fs[0].Status != "active" || fs[0].Severity != SeverityWarning {
		t.Fatalf("unsampled round must hold the finding as-is, got %+v", fs)
	}

	// 升级轮：severe 代码把原 warning 卡升为 critical。
	severe := DeviceHealth{Device: "gpu-3", Reachable: true, GPUSampled: true, GPUXIDSeen: true,
		GPUXIDMax: 79, GPUXIDCodes: []int{13, 79}, GPUXIDEvidence: []string{"Xid (PCI:0000:04:00.0): 79"}}
	m.gpuXidFinding("gpu-3", severe)
	fs, _ = ListFindings()
	var card *Finding
	for _, f := range fs {
		if f.Source == "gpu:xid:gpu-3" {
			card = f
		}
	}
	if card == nil {
		t.Fatalf("gpu-3 card vanished after upgrade: %+v", fs)
	}
	if card.Severity != SeverityCritical || !strings.Contains(card.Detail, "升级") {
		t.Fatalf("13→79 must upgrade warning→critical, got %+v", card)
	}
}

// XID 源缺席轮（跑通了指标但两 XID 源皆败）：hold——"没观测到" ≠ "已清除"。
func TestGPUXidHoldsWhenSourceAbsent(t *testing.T) {
	m := gpuTestManager(t, nil)
	mild := DeviceHealth{Device: "gpu-9", Reachable: true, GPUSampled: true, GPUXIDSeen: true,
		GPUXIDMax: 13, GPUXIDCodes: []int{13}, GPUXIDEvidence: []string{"Xid 13 occurred"}}
	m.gpuXidFinding("gpu-9", mild)
	absent := DeviceHealth{Device: "gpu-9", Reachable: true, GPUSampled: true, GPUXIDSeen: false,
		GPULastError: "XID 兜底源也不可用：exit 127"}
	m.gpuXidFinding("gpu-9", absent)
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Status != "active" {
		t.Fatalf("source-absent round must hold the finding, got %+v", fs)
	}
	clear := DeviceHealth{Device: "gpu-9", Reachable: true, GPUSampled: true, GPUXIDSeen: true}
	m.gpuXidFinding("gpu-9", clear)
	fs, _ = ListFindings()
	if len(fs) != 1 || fs[0].Status != "resolved" {
		t.Fatalf("authoritative clear (source seen, no events) must resolve, got %+v", fs)
	}
}

// 禁用的规则必须自动恢复其遗留 active finding，且 streak 随
// 规则消失被清理（P2-1：同名重建不得继承旧轮次）。
func TestDisabledRuleResolvesAndPrunes(t *testing.T) {
	rules := []config.NetDevAlertRule{
		{Name: "hot", Metric: "gpu.temp", Op: ">=", Value: 85, Enabled: true},
	}
	m := gpuTestManager(t, rules)
	hot := map[string]DeviceHealth{"g1": {Device: "g1", Reachable: true, GPUSampled: true,
		GPU: []GPUCard{{Index: 0, TempC: 88}}}}
	m.evaluateAlerts(hot)
	if fs, _ := ListFindings(); len(fs) != 1 {
		t.Fatalf("enabled rule should fire (ForRounds defaults 1), got %d", len(fs))
	}
	// 禁用后：active 自动 resolve + streak 清零。
	disabled := []config.NetDevAlertRule{{Name: "hot", Metric: "gpu.temp", Op: ">=", Value: 85, Enabled: false}}
	m.cfg.NetDev.AlertRules = disabled
	m.evaluateAlerts(hot)
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Status != "resolved" {
		t.Fatalf("disabled rule must resolve its finding, got %+v", fs)
	}
	if alertStreak("alert:hot:g1") != 0 {
		t.Errorf("disabled rule streak must clear, got %d", alertStreak("alert:hot:g1"))
	}
	// streak 惰性清理：删除规则/设备后残留的 key 在下一轮被清掉——同名重建
	// 不得继承旧轮次（P2-1）。
	setAlertStreak("alert:hot:g1", 5)   // 本规则本轮仍出现（cool 不触发但 key 在 seen 里，会在评估中清零）
	setAlertStreak("alert:old:gone", 5) // 已删除的规则×已删除的设备：孤儿 key
	m.cfg.NetDev.AlertRules = []config.NetDevAlertRule{
		{Name: "hot", Metric: "gpu.temp", Op: ">=", Value: 85, Enabled: true, ForRounds: 3},
	}
	cool := map[string]DeviceHealth{"g1": {Device: "g1", Reachable: true, GPUSampled: true,
		GPU: []GPUCard{{Index: 0, TempC: 40}}}}
	m.evaluateAlerts(cool)
	if alertStreak("alert:hot:g1") != 0 {
		t.Errorf("cool round must reset the live streak, got %d", alertStreak("alert:hot:g1"))
	}
	if alertStreak("alert:old:gone") != 0 {
		t.Errorf("prune must drop orphaned keys, got %d", alertStreak("alert:old:gone"))
	}
}

// 时序面：gpu.<index>.<metric> 命名 + labels 并存，读取端按 metric 分组。
func TestRecordGPUSeriesLabeled(t *testing.T) {
	netdevStateDirOverr = t.TempDir()
	t.Cleanup(func() { netdevStateDirOverr = "" })
	// series 锚点缓存首次解析的路径（包级）——前一个测试的 TempDir 已被
	// 清理，缓存路径会静默写失败；重置让本测试的 override 生效。
	seriesPath = ""
	seriesDirPath = ""
	seriesMigratedFor = ""
	t.Cleanup(func() { seriesPath = ""; seriesDirPath = ""; seriesMigratedFor = "" })
	h := DeviceHealth{Device: "g9", Reachable: true, GPUSampled: true,
		GPU: []GPUCard{{Index: 1, TempC: 66, MemUsedMB: 2048, MemTotalMB: 40960, UtilPct: 30}}}
	recordGPUSeries(h)
	got := SeriesRead("g9", time.Hour)
	for _, metric := range []string{"gpu.count", "gpu.xid_max", "gpu.1.temp", "gpu.1.mem_used", "gpu.1.mem_pct", "gpu.1.util"} {
		if len(got[metric]) != 1 {
			t.Fatalf("series %s want 1 point, got %d (%v)", metric, len(got[metric]), got)
		}
	}
	if got["gpu.1.temp"][0].Value != 66 {
		t.Errorf("gpu.1.temp want 66, got %g", got["gpu.1.temp"][0].Value)
	}
	if got["gpu.1.mem_pct"][0].Value != 5 { // 2048*100/40960
		t.Errorf("gpu.1.mem_pct want 5, got %g", got["gpu.1.mem_pct"][0].Value)
	}
	// 未采样的主机不产 GPU 时序。
	recordGPUSeries(DeviceHealth{Device: "g9", GPUSampled: false})
	if pts := SeriesRead("g9", time.Hour)["gpu.1.temp"]; len(pts) != 1 {
		t.Errorf("unsampled round must not append, got %d", len(pts))
	}
}

// ── 采集/告警引擎的集成补强（合并语义、采样冻结、execSealed 地板） ──

// 结构性拒绝——confirm/auto 分级路由不可达。
func TestInternalExecWriteFloor(t *testing.T) {
	m, _ := guardrailManager(t, config.NetDevGuardrails{})
	res := m.execSealed(context.Background(), "sw1", "undo stp", true)
	if !res.Refused {
		t.Fatalf("internal write must be structurally refused, got %+v", res)
	}
	if !strings.Contains(res.Refusal, "read-only by definition") {
		t.Errorf("refusal should explain the floor, got %q", res.Refusal)
	}
	// 读命令照常（分类器路径不受地板影响）。
	ok := m.execSealed(context.Background(), "sw1", "display version", true)
	if ok.Refused {
		t.Fatalf("internal read must pass, got %+v", ok)
	}
}

// GPUOnly 冻结（回归审查 P2-1）：GPU-only 主机采集失败（GPUSampled=false ⇒

// Reachable=false）不按「设备不可达」立案；采集正常后恢复评估。
func TestAlertsFreezeUnsampledGPUOnlyHost(t *testing.T) {
	rules := []config.NetDevAlertRule{
		{Name: "up", Metric: "reachable", Op: "==", Value: 0, Enabled: true},
	}
	m := gpuTestManager(t, rules)
	dead := map[string]DeviceHealth{"g1": {Device: "g1", GPUOnly: true, GPUSampled: false,
		Reachable: false, GPULastError: "connection refused"}}
	m.evaluateAlerts(dead) // 我方看不了 ≠ 设备掉了：不立案
	if fs, _ := ListFindings(); len(fs) != 0 {
		t.Fatalf("unsampled GPU-only host must be frozen for non-GPU rules, got %+v", fs)
	}
	// 采集正常但设备真的不可达：立案（此时证据里带 GPULastError）。
	down := map[string]DeviceHealth{"g1": {Device: "g1", GPUOnly: true, GPUSampled: true,
		Reachable: false, GPULastError: "nvidia-smi 失败：driver barfed"}}
	m.evaluateAlerts(down)
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Status != "active" {
		t.Fatalf("sampled + unreachable must fire, got %+v", fs)
	}
	if !strings.Contains(fs[0].Evidence[0].Output, "nvidia-smi 失败") {
		t.Errorf("evidence must carry gpuLastError, got %+v", fs[0].Evidence)
	}
	if !strings.Contains(fs[0].Evidence[0].Command, "GPU 健康轮询") {
		t.Errorf("evidence command must be channel-labelled, got %q", fs[0].Evidence[0].Command)
	}
}

// G4：pollGPUDevices 的 SNMP+GPU 合并（SNMP 段保留、可达性取或）与 GPU-only
// 标记。活机用 linux-shell sim（nvidia-smi 在 sim 上 exec 127 = 命令执行过 =

// 传输可达）；死机 127.0.0.1:1 秒拒。
func TestPollGPUMergeAndGPUOnly(t *testing.T) {
	shellSim := startSimDeviceWithPrompt(t, "root@sim:~# ")
	m, _ := guardrailManager(t, config.NetDevGuardrails{})
	// guardrailManager 的清单是 huawei 设备——换成 GPU 测试清单（复用它的
	// 密钥/known_hosts/审计基建）。
	host, portStr, _ := net.SplitHostPort(shellSim.addr)
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)
	m.cfg.NetDev.Devices = []config.NetDevDevice{
		{Name: "gpu-live", Vendor: "linux", Address: host, Port: port,
			Username: "admin", PasswordEnv: "TEST_ENV", Group: "edge", GPU: true},
		{Name: "gpu-dead", Vendor: "linux", Address: "127.0.0.1", Port: 1,
			Username: "admin", PasswordEnv: "TEST_ENV", Group: "edge", GPU: true},
	}
	netdevStateDirOverr = t.TempDir()
	t.Cleanup(func() { netdevStateDirOverr = "" })

	fresh := map[string]DeviceHealth{
		// gpu-live 同时配了 SNMP（预置【失联】段——验证合并的可达性取或：
		// GPU 通道通则设备可达）；gpu-dead 纯 GPU-only。
		"gpu-live": {Device: "gpu-live", Reachable: false, UptimeSec: 123,
			Interfaces: []IfHealth{{Name: "eth0", AdminUp: true, OperUp: true}}},
	}
	m.pollGPUDevices(context.Background(), fresh)

	live := fresh["gpu-live"]
	if !live.GPUSampled || !live.Reachable {
		t.Errorf("live host: sampled/reachable must hold, got %+v", live)
	}
	if live.GPUOnly {
		t.Errorf("host with SNMP base must not be marked GPUOnly")
	}
	if live.UptimeSec != 123 || len(live.Interfaces) != 1 {
		t.Errorf("SNMP segment must survive the merge, got uptime=%d if=%d", live.UptimeSec, len(live.Interfaces))
	}
	dead := fresh["gpu-dead"]
	if !dead.GPUOnly {
		t.Errorf("gpu-dead has no SNMP base → GPUOnly expected true, got %+v", dead)
	}
	if dead.GPUSampled {
		t.Errorf("refused poll must not claim sampled, got %+v", dead)
	}
	if dead.GPULastError == "" {
		t.Errorf("refused poll must surface lastError")
	}
}
