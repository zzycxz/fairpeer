package bot

// netdevcmds.go — the 运维 leg of the IM gateway (FDE 的耳朵 v1): alert and
// briefing pushes end with "回复 详情 <编号> 查看证据"; these commands are the
// receiving side. Two commands, both read-only:
//
//	/netdev 发现        — active findings (id · severity · title · devices)
//	/netdev 详情 <id>   — one finding's detail + evidence excerpts
//
// The gateway stays netdev-agnostic: desktop injects a NetdevBridge, exactly
// like DesktopBridge (nil = commands explain they need the desktop host).

import (
	"fmt"
	"strings"
)

// NetdevFindingSummary is one row of 「发现」.
type NetdevFindingSummary struct {
	ID       string
	Severity string
	Title    string
	Devices  []string
	Status   string
}

// NetdevFindingDetail is one finding's full text for 「详情」.
type NetdevFindingDetail struct {
	ID       string
	Severity string
	Title    string
	Devices  []string
	Detail   string
	Status   string
	Evidence []NetdevEvidenceView
	NotFound bool
}

// NetdevEvidenceView is one evidence excerpt.
type NetdevEvidenceView struct {
	Device  string
	Command string
	Output  string
}

// NetdevBridge is the netdev-side surface the desktop host injects.
type NetdevBridge interface {
	// NetdevActiveFindings lists unresolved findings, newest first.
	NetdevActiveFindings() []NetdevFindingSummary
	// NetdevFindingByID renders one finding (NotFound=true when absent).
	NetdevFindingByID(id string) NetdevFindingDetail
}

func (gw *BotGateway) handleNetdevCommand(msg InboundMessage) string {
	bridge := gw.cfg.Netdev
	if bridge == nil {
		return "此 bot 未连接运维侧——在桌面端启用运维配置后可用。"
	}
	fields := strings.Fields(msg.Text)
	sub := ""
	arg := ""
	if len(fields) > 1 {
		sub = strings.ToLower(fields[1])
	}
	if len(fields) > 2 {
		arg = fields[2]
	}
	switch sub {
	case "", "发现", "findings":
		fs := bridge.NetdevActiveFindings()
		if len(fs) == 0 {
			return "当前没有未处理的发现。"
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("未处理的发现（%d）：\n", len(fs)))
		for i, f := range fs {
			if i >= 10 {
				sb.WriteString(fmt.Sprintf("…（其余 %d 条）\n", len(fs)-10))
				break
			}
			sb.WriteString(fmt.Sprintf("· %s [%s] %s（%s）\n", f.ID, f.Severity, f.Title, strings.Join(f.Devices, "、")))
		}
		sb.WriteString("回复 /netdev 详情 <编号> 查看证据。")
		return sb.String()
	case "详情", "detail", "d":
		if arg == "" {
			return "用法：/netdev 详情 <编号>（编号见 /netdev 发现，或告警消息尾部）。"
		}
		d := bridge.NetdevFindingByID(arg)
		if d.NotFound {
			return fmt.Sprintf("没有找到发现 %s——用 /netdev 发现 列出当前编号。", arg)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%s [%s]%s %s（%s）\n", d.ID, d.Severity, statusTag(d.Status), d.Title, strings.Join(d.Devices, "、")))
		if d.Detail != "" {
			sb.WriteString(d.Detail + "\n")
		}
		for i, e := range d.Evidence {
			if i >= 3 {
				sb.WriteString(fmt.Sprintf("…（其余 %d 条证据）\n", len(d.Evidence)-3))
				break
			}
			out := e.Output
			if len(out) > 500 {
				out = out[:500] + "…"
			}
			sb.WriteString(fmt.Sprintf("证据 %d · %s ▸ %s\n%s\n", i+1, e.Device, e.Command, out))
		}
		return sb.String()
	default:
		return "用法：/netdev 发现 | /netdev 详情 <编号>"
	}
}

func statusTag(status string) string {
	if status == "resolved" {
		return "（已恢复）"
	}
	if status == "active" {
		return "（告警中）"
	}
	return ""
}
