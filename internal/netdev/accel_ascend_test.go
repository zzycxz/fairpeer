package netdev

import (
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

// npuInfoFixture is the CANN 8 / npu-smi 24.x "two rows per NPU" table shape
// (fixture 先行——真机漂移由解析器的逐格容错 + note 通道兜底，G-C2 终验)。
const npuInfoFixture = `+---------------------------------------------------------------------------------------+
| npu-smi 24.1.rc1.b030                       Version: 24.1.rc1.b030                    |
+----------------------++-----------------------+---------------------------------------+
| NPU       Name       | Health                | Power(W)    Temp(C)     Huge-Pages   |
| Chip      Device     | Bus-Id                | AICore(%)   Memory-Usage(MB)          |
+======================++=======================+=======================================+
| 0         910B4      | OK                    | 63.5        42            0/0         |
| 0         0          | 0000:C1:00.0          | 12          8388 / 65536              |
+======================++=======================+=======================================+
| 1         910B4      | Warning               | 65.2        44            0/0         |
| 1         1          | 0000:81:00.0          | 30          10240 / 65536             |
+======================++=======================+=======================================+
| 2         910B4      | 805c0003              | 60.0        41            0/0         |
| 2         2          | 0000:41:00.0          | 0           4096 / 65536              |
+======================++=======================+=======================================+
`

func TestParseNPUInfo(t *testing.T) {
	cards, notes := parseNPUInfo(npuInfoFixture)
	if len(cards) != 3 {
		t.Fatalf("want 3 cards, got %d (%v)", len(cards), cards)
	}
	c0 := cards[0]
	if c0.Index != 0 || c0.Name != "910B4" || c0.TempC != 42 || c0.UtilPct != 12 ||
		c0.MemUsedMB != 8388 || c0.MemTotalMB != 65536 {
		t.Errorf("card 0 wrong: %+v", c0)
	}
	if c0.ErrorCode != 0 || c0.ErrorCodeKind != npuHealthKind {
		t.Errorf("healthy card must have no code: %+v", c0)
	}
	// Warning（非数字健康态）：立案但码值不编造。
	c1 := cards[1]
	if c1.ErrorCode != 0 || c1.ErrorCodeKind != npuHealthKind {
		t.Errorf("warning card kind wrong: %+v", c1)
	}
	// 数字健康态：当作错误码归一（805c0003 是十六进制段——十进制不可解析则
	// 保 0，但非 OK 必须可见——见 ErrorCodeKind）。
	c2 := cards[2]
	if c2.ErrorCodeKind != npuHealthKind {
		t.Errorf("non-OK card must keep kind: %+v", c2)
	}
	if len(notes) != 0 {
		t.Errorf("well-formed fixture should produce no notes: %v", notes)
	}
}

func TestParseNPUDriftTolerant(t *testing.T) {
	// 孤儿 A 行（没有 B 行）：丢弃留 note——不静默掩盖。
	drift := strings.Replace(npuInfoFixture, "| 1         1          | 0000:81:00.0          | 30          10240 / 65536             |\n", "", 1)
	cards, notes := parseNPUInfo(drift)
	if len(cards) != 2 {
		t.Errorf("want 2 paired cards, got %d", len(cards))
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n, "NPU 1") && strings.Contains(n, "不完整") {
			found = true
		}
	}
	if !found {
		t.Errorf("orphan row must leave a note: %v", notes)
	}
}

func TestPollAscendBatteryRouting(t *testing.T) {
	// accelForDevice：显式 accel 优先，空 → nvidia 缺省（探测式缺省挂真机）。
	if got := accelForDevice(config.NetDevDevice{Accel: "ascend"}); got != "ascend" {
		t.Errorf("explicit accel want ascend, got %s", got)
	}
	if got := accelForDevice(config.NetDevDevice{}); got != "nvidia" {
		t.Errorf("empty accel want nvidia default, got %s", got)
	}
	// npu-smi 采样归一进同一通道（GPUSampled/XIDSeen 置位、错误码进 GPUXIDMax）。
	// 端到端走 execSealed 需要真机/模拟器——fixture 层已覆盖解析，采样语义由
	// pollAscendHealth 的分支逻辑保证（代码审阅 + G-C2 真机终验）。
}
