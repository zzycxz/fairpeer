package netdev

import (
	"strings"

	"github.com/gosnmp/gosnmp"
)

// healthwater.go — SNMP 水位点采（DASHBOARD spec §7.3 停车场的 OID 扩展）。
// cpu/mem 是厂商私有 OID 的单 GET，接口流量是 ifXTable/ifTable 计数器的
// 求和 walk——全部走 pollDeviceHealth 已建立的只读 v2c 连接，零额外连接、
// 零重试（探测宪法：标准读操作，只对已配置 SNMP 轮询的设备生效）。
// 厂商表外的设备 cpu/mem 留 0（诚实：未采集 ≠ 0% 使用）。

// vendorWaterOids: [cpu, mem] per vendor（值均为 Gauge 百分比）。
var vendorWaterOids = map[string][2]string{
	// huawei VRP: hwEntityCpuUsage / hwEntityMemUsage（堆叠取各板之和有
	// 偏差，v1 取第一个应答值——趋势用途足够，非精确账）。
	"huawei": {"1.3.6.1.4.1.2011.5.25.31.1.1.1.1.5.1", "1.3.6.1.4.1.2011.5.25.31.1.1.1.1.7.1"},
	// cisco IOS: avgBusy（5min） / ciscoMemoryPoolUsed 里的 pool#1。
	"cisco": {"1.3.6.1.4.1.9.2.1.58.0", "1.3.6.1.4.1.9.9.48.1.1.1.6.1"},
}

const (
	oidIfHCInOctets  = "1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCOutOctets = "1.3.6.1.2.1.31.1.1.1.10"
	oidIfInOctets    = "1.3.6.1.2.1.2.2.1.10"
	oidIfOutOctets   = "1.3.6.1.2.1.2.2.1.16"
)

// pollWatermarks collects cpu/mem percentages and the summed interface octet
// counters. Any failure leaves that part at zero — the poll never fails on
// watermarks (they are additive telemetry, not gates).
func pollWatermarks(g *gosnmp.GoSNMP, vendor string) (cpu, mem int, inOct, outOct uint64) {
	if pair, ok := vendorWaterOids[strings.ToLower(strings.TrimSpace(vendor))]; ok {
		if res, err := g.Get([]string{pair[0], pair[1]}); err == nil && len(res.Variables) == 2 {
			cpu = pduGauge(res.Variables[0])
			mem = pduGauge(res.Variables[1])
		}
	}
	inOct = walkSumOctets(g, oidIfHCInOctets, oidIfInOctets)
	outOct = walkSumOctets(g, oidIfHCOutOctets, oidIfOutOctets)
	return cpu, mem, inOct, outOct
}

// walkSumOctets sums one counter column over all interfaces (HC 64-bit first,
// 32-bit fallback for old boxes). Bounded to snmpMaxVars rows.
func walkSumOctets(g *gosnmp.GoSNMP, hcOID, v1OID string) uint64 {
	for _, oid := range []string{hcOID, v1OID} {
		var sum uint64
		n := 0
		err := g.Walk(oid, func(p gosnmp.SnmpPDU) error {
			if n >= snmpMaxVars {
				return errBoundedWalk
			}
			n++
			switch v := p.Value.(type) {
			case uint64:
				sum += v
			case uint:
				sum += uint64(v)
			}
			return nil
		})
		if err == nil && n > 0 {
			return sum
		}
	}
	return 0
}

// errBoundedWalk stops a walk without poisoning the error path.
var errBoundedWalk = &boundedWalkError{}

type boundedWalkError struct{}

func (*boundedWalkError) Error() string { return "bounded" }

// pduGauge extracts an integer gauge value (percent) from a PDU.
func pduGauge(p gosnmp.SnmpPDU) int {
	switch v := p.Value.(type) {
	case uint:
		return int(v)
	case uint64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}
