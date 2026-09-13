package netdev

// accel_ascend.go — 昇腾薄驱动（M-1，ACCEL_SPEC §4.2；批⑥ fixture 先行，
// 真机终验挂 dogfooding/G-C2）。只读电池走读表白名单内的 npu-smi 形态
// （npu-smi info / npu-smi info -t），经 execSealed 密封路径——密封/审计/
// 脱敏/预算零改动。
//
// 解析对象：`npu-smi info` 主表的"两行一芯"布局（CANN 8.x / npu-smi 24.x
// 实勘口径）——A 行 = NPU 序号/型号/Health/功耗/温度，B 行 = Bus/AICore%/
// 显存 used / total。字段随驱动版本漂移是已知风险：解析器逐格容错（解析
// 不到的格静默跳过、无法配对的行丢弃并留 note），缺什么显什么——值班看到
// 的就是采集到的，不造数。
//
// 错误码分级：昇腾错误码 catalog 存在登录墙（调研 M-2 勘误），本批诚实地
// 把 Health 列非 OK 一律按 warning 立案、catalog 对齐留真机校准——不猜
// 分级表。

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// npuInfoCmd is the ascend inventory command (read-table form).
const npuInfoCmd = "npu-smi info"

// npuHealthKind marks the error-code kind for Ascend health anomalies.
const npuHealthKind = "npu-health"

// pollAscendHealth is the ascend thin-driver battery: one npu-smi info round
// for inventory + health. 成功采样即 GPUXIDSeen（错误码证据面已见）——hold/
// resume 语义与 XID 通道一致。
func (m *Manager) pollAscendHealth(ctx context.Context, deviceName string) DeviceHealth {
	h := DeviceHealth{Device: deviceName, Time: time.Now().UTC(), Interfaces: []IfHealth{}}
	res := m.execSealed(ctx, deviceName, npuInfoCmd, true)
	if res.Refused {
		// 拒绝=我方原因：不置 GPUSampled（告警与错误码生命周期全部静默）。
		h.GPULastError = firstLineOf(res.Refusal)
		return h
	}
	h.Reachable = true
	h.GPUSampled = true
	if res.IsError {
		// 轮1审查 P1-4：失败轮不置 GPUXIDSeen——"我方没看到"≠"已清除"，
		// 在案错误 finding 走 hold（与 XID 通道的 P1-5 纪律一致）。
		h.GPULastError = "npu-smi 失败：" + firstLineOf(res.Output)
		return h
	}
	h.GPUXIDSeen = true
	cards, notes := parseNPUInfo(res.Output)
	h.GPU = cards
	maxCode := 0
	for _, c := range cards {
		if c.ErrorCode > maxCode {
			maxCode = c.ErrorCode
		}
	}
	// 轮1审查 P2-6：非数值 Health（Warning/Error/十六进制段）无十进制码值
	// 可归一——但"卡在异常"必须设备级可见且**不 resolve** 在案 finding：
	// 哨兵码 -1 表"异常待人工读卡"（不出现在任何 catalog 分级，值班看证据）。
	for _, c := range cards {
		if c.ErrorCodeKind == npuHealthKind && c.ErrorCode == 0 {
			maxCode = -1
			break
		}
	}
	for _, n := range notes {
		h.GPULastError = joinNote(h.GPULastError, n)
	}
	if maxCode != 0 {
		h.GPUXIDMax = maxCode
		h.GPUXIDCodes = []int{maxCode}
		h.GPUXIDEvidence = npuEvidenceLines(res.Output)
		h.GPUXIDSource = npuInfoCmd
	}
	return h
}

// npuEvidenceLines keeps a bounded evidence excerpt: non-OK health rows from
// the table, capped like the XID evidence channel.
func npuEvidenceLines(out string) []string {
	lines := []string{}
	for _, ln := range strings.Split(out, "\n") {
		if len(lines) >= gpuXidLines {
			break
		}
		if strings.Contains(ln, "|") && strings.TrimSpace(ln) != "" && !strings.Contains(ln, " OK ") {
			lines = append(lines, strings.TrimSpace(ln))
		}
	}
	return lines
}

// parseNPUInfo parses `npu-smi info` table output into normalized cards.
// Layout (CANN 8 / npu-smi 24.x): a header frame, then per NPU a pair of
// data rows inside `| ... | ... | ... |` cells:
//
//	| 0    910B4  | OK           | 63.5   42    0/0       |   ← NPU/Name/Health/Power/Temp/Huge
//	| 0    0      | 0000:C1:00.0 | 0      8388 / 65536   |   ← Chip/Device/BusId/AICore/Mem
//
// notes carry the format-drift skips (same 不静默掩盖 discipline as nvidia CSV).
func parseNPUInfo(out string) (cards []GPUCard, notes []string) {
	cards = []GPUCard{}
	rows := [][3]string{} // 每行 3 个 cell（左右去框线）
	for _, ln := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "|") || !strings.Contains(ln, "|") || len(ln) < 2 {
			continue
		}
		cells := strings.Split(strings.Trim(ln, "|"), "|")
		if len(cells) != 3 {
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, [3]string{cells[0], cells[1], cells[2]})
	}
	// 数据行识别：第一格以数字开头的行（NPU 序号或 Chip 序号）。
	pendingA := -1
	var aNPU int
	var aRow [3]string
	for _, r := range rows {
		first := r[0]
		fields := strings.Fields(first)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			continue // 表头/分隔行
		}
		// A 行：第二格是型号名（非纯数字）；B 行：第二格是纯数字 chip 序号。
		switch {
		case len(fields) >= 2 && !allDigits(fields[1]):
			// 新的 A 行（若上一张 A 行没等到 B 行，丢弃留 note）。
			if pendingA >= 0 {
				notes = append(notes, "NPU "+strconv.Itoa(aNPU)+" 的数据行不完整，已跳过")
			}
			pendingA, aNPU, aRow = 1, n, r
		case len(fields) >= 2 && allDigits(fields[1]) && pendingA == 1:
			card, note := mergeNPURows(aRow, r)
			if note != "" {
				notes = append(notes, note)
			}
			card.Index = aNPU
			cards = append(cards, card)
			pendingA = -1
		default:
			// 表头残片等
		}
	}
	if pendingA >= 0 {
		notes = append(notes, "NPU "+strconv.Itoa(aNPU)+" 的数据行不完整，已跳过")
	}
	return cards, notes
}

// mergeNPURows folds one A-row + B-row pair into a normalized card.
func mergeNPURows(a, b [3]string) (GPUCard, string) {
	card := GPUCard{}
	// A 行：cell1 = Health、cell2 = "Power Temp Huge"。
	aF := strings.Fields(a[0]) // [npu, name...]
	if len(aF) >= 2 {
		card.Name = strings.Join(aF[1:], " ")
	}
	health := strings.Fields(a[1])
	if len(health) >= 1 && health[0] != "OK" {
		// 非 OK：Kind 标记异常；十进制码可解析则归一，字面态（Warning/
		// Error/十六进制段）保 0 由设备级哨兵立案（不编造码值）。
		card.ErrorCodeKind = npuHealthKind
		if code, err := strconv.Atoi(health[0]); err == nil {
			card.ErrorCode = code
		}
	}
	aStats := strings.Fields(a[2])
	if len(aStats) >= 2 {
		card.TempC = atoiOrZero(aStats[1])
	}
	// B 行：cell1 = BusId（不采）、cell2 = "AICore% 显存used / total"。
	bStats := strings.Fields(b[2])
	if len(bStats) >= 1 {
		if u, err := strconv.Atoi(bStats[0]); err == nil {
			card.UtilPct = u
		}
	}
	used, total := parseMemPair(bStats[1:])
	card.MemUsedMB, card.MemTotalMB = used, total
	return card, ""
}

func parseMemPair(fields []string) (uint64, uint64) {
	joined := strings.Join(fields, " ")
	if i := strings.Index(joined, "/"); i >= 0 {
		used := atoiOrZero(strings.TrimSpace(joined[:i]))
		total := atoiOrZero(strings.TrimSpace(joined[i+1:]))
		return uint64(used), uint64(total)
	}
	return 0, 0
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
