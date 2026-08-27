package netdev

// locate.go — IP/MAC 定位（NETDEV_SPEC_V2 §4.11）：网工「这个 IP 接在哪」与
// 蓝队「可疑 IP 的物理位置」共用的编排工具。并发查清单内设备的 ARP/邻居表
// ——全部在只读白名单内：网络设备用设备侧 `| include` 过滤（网络 CLI 的管道
// 是本机过滤器，不是 shell 链），linux/windows 拉全表客户端过滤。VTY 与命令
// 预算沿用密封 Exec 路径；预算耗尽时如实报告覆盖了多少台。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// LocateHit is one device's answer for the target.
type LocateHit struct {
	Device    string `json:"device"`
	Interface string `json:"interface,omitempty"`
	Line      string `json:"line"`
}

// LocateResult is the fan-out outcome.
type LocateResult struct {
	Target   string       `json:"target"`
	Hits     []LocateHit  `json:"hits"`
	Devices  []string     `json:"searched"`
	Skipped  []string     `json:"skipped,omitempty"`
	Covered  int          `json:"covered_devices"`
	Total    int          `json:"total_devices"`
	BudgetStop bool       `json:"budget_stopped"`
	Note     string       `json:"note,omitempty"`
}

const locateMaxDevices = 50

// locateCmd picks the ARP-surface command per vendor. Network CLIs filter
// server-side (their `| include` is not a shell pipe); hosts pull the full
// table and we match client-side.
func locateCmd(vendor, target string) (string, bool) {
	switch vendor {
	case "huawei":
		return "display arp | include " + target, true
	case "cisco":
		return "show ip arp | include " + target, true
	case "zte":
		return "show arp", true
	case "linux":
		return "ip neigh", true
	case "windows":
		return "arp -a", true
	}
	return "", false
}

// matchLocateLines keeps lines containing the target (MAC matched case-
// insensitively) and crudely extracts the interface (last non-address token).
func matchLocateLines(device string, lines []string, target string) []LocateHit {
	var hits []LocateHit
	lt := strings.ToLower(target)
	for _, ln := range lines {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" || strings.HasPrefix(strings.TrimSpace(ln), "display arp") || strings.HasPrefix(strings.TrimSpace(ln), "show arp") {
			continue
		}
		if !strings.Contains(strings.ToLower(ln), lt) {
			continue
		}
		hits = append(hits, LocateHit{Device: device, Interface: extractIface(ln), Line: ln})
	}
	return hits
}

// extractIface pulls the interface token out of an ARP line: heuristics over
// the two common families (`... Vlan10` / `... GE1/0/5` as the last field;
// linux `... dev eth0 ...`).
func extractIface(ln string) string {
	fields := strings.Fields(ln)
	for i := len(fields) - 1; i >= 0; i-- {
		f := fields[i]
		l := strings.ToLower(f)
		if strings.HasPrefix(l, "vlan") || strings.HasPrefix(l, "ge") || strings.HasPrefix(l, "eth") ||
			strings.HasPrefix(l, "gigabitethernet") || strings.HasPrefix(l, "xge") || strings.HasPrefix(l, "ethernet") {
			return f
		}
		if l == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	if len(fields) > 0 {
		return fields[len(fields)-1]
	}
	return ""
}

// Locate fans one IP/MAC across the inventory's ARP surfaces.
func (m *Manager) Locate(ctx context.Context, target string) LocateResult {
	res := LocateResult{Target: target}
	target = strings.TrimSpace(target)
	if target == "" || (len(strings.Split(target, ".")) != 4 && !strings.Contains(target, ":")) {
		res.Note = "target must be an IPv4 address or a MAC (aa:bb:cc:...)"
		return res
	}
	var names []string
	for _, d := range m.cfg.NetDev.Devices {
		if _, ok := locateCmd(d.Vendor, target); ok {
			names = append(names, d.Name)
		}
	}
	res.Total = len(names)
	if res.Total == 0 {
		res.Note = "no devices with an ARP surface (huawei/cisco/zte/linux/windows) in the inventory"
		return res
	}
	if len(names) > locateMaxDevices {
		names = names[:locateMaxDevices]
		res.Note = fmt.Sprintf("capped at %d devices per sweep", locateMaxDevices)
	}
	for _, name := range names {
		d, _ := m.cfg.NetDevDeviceByName(name)
		cmd, _ := locateCmd(d.Vendor, target)
		r := m.Exec(ctx, name, cmd)
		if r.Refused {
			if strings.Contains(r.Refusal, "budget") {
				res.BudgetStop = true
				break
			}
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s — %s", name, r.Refusal))
			continue
		}
		if r.IsError {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s — device error", name))
			continue
		}
		res.Devices = append(res.Devices, name)
		res.Covered++
		var lines []string
		if strings.HasSuffix(cmd, target) { // server-side filtered already
			lines = strings.Split(r.Output, "\n")
		}
		res.Hits = append(res.Hits, matchLocateLines(name, lines, target)...)
	}
	if res.BudgetStop {
		res.Note += " sweep stopped early: turn budget reached; uncovered devices are NOT reported as absent"
	}
	return res
}

// ── Agent tool ───────────────────────────────────────────────────────────────

type locateTool struct{ m *Manager }

func (t *locateTool) Name() string { return "netdev_locate" }

func (t *locateTool) Description() string {
	return "Locate ONE IP or MAC across the inventory's ARP/neighbor tables — which device and which port it sits behind. " +
		"The classic 网工 question (这个 IP 接在哪) and the blue-team one (可疑 IP 的物理位置) in one fan-out; only inventory devices are queried."
}

func (t *locateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"target": {"type": "string", "description": "IPv4 address or MAC (aa:bb:cc:...)"}
		},
		"required": ["target"]
	}`)
}

func (t *locateTool) ReadOnly() bool { return true }

func (t *locateTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Target) == "" {
		return "", errors.New("netdev_locate: target is required")
	}
	b, err := json.Marshal(t.m.Locate(ctx, a.Target))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
