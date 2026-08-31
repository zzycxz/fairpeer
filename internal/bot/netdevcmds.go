package bot

// netdevcmds.go — 运维模块在 IM 网关中的指令入口 (FDE 的耳朵 v1): alert and
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

// NetdevProposalSummary is one row of 「提案」 for the IM command surface.
type NetdevProposalSummary struct {
	ID     string
	Status string
	Title  string
}

// NetdevActionResult reports a mutating IM command's outcome in human text.
type NetdevActionResult struct {
	OK  bool
	Msg string
}

// NetdevBridge is the netdev-side surface the desktop host injects.
type NetdevBridge interface {
	// NetdevActiveFindings lists unresolved findings, newest first.
	NetdevActiveFindings() []NetdevFindingSummary
	// NetdevFindingByID renders one finding (NotFound=true when absent).
	NetdevFindingByID(id string) NetdevFindingDetail
	// NetdevAckFinding acknowledges one finding from IM (completion-spec §5.3:
	// 收到告警后可操作——ack 不解决，只标记已被人看过).
	NetdevAckFinding(id string) NetdevActionResult
	// NetdevProposals lists proposals pending a human decision.
	NetdevProposals() []NetdevProposalSummary
	// NetdevProposalApprove / NetdevProposalReject run the SAME guarded path
	// as the desktop approval UI (group policy + change window + audit).
	NetdevProposalApprove(id string) NetdevActionResult
	NetdevProposalReject(id, reason string) NetdevActionResult
}

func (gw *BotGateway) handleNetdevCommand(msg InboundMessage) string {
	bridge := gw.cfg.Netdev
	if bridge == nil {
		return "此 bot 未连接运维侧——在桌面端启用运维配置后可用。"
	}
	fields := strings.Fields(msg.Text)
	sub := ""
	arg := ""
	rest := ""
	if len(fields) > 1 {
		sub = strings.ToLower(fields[1])
	}
	if len(fields) > 2 {
		arg = fields[2]
	}
	if len(fields) > 3 {
		rest = strings.Join(fields[3:], " ")
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
	case "ack", "确认":
		if arg == "" {
			return "用法：/netdev ack <编号>（编号见 /netdev 发现或告警消息尾部）。"
		}
		r := bridge.NetdevAckFinding(arg)
		if !r.OK {
			return r.Msg
		}
		return fmt.Sprintf("已确认 %s（告警仍在队列，处理完用桌面端标记已处理）。", arg)
	case "提案", "proposals", "p":
		act := strings.ToLower(arg)
		switch act {
		case "":
			ps := bridge.NetdevProposals()
			if len(ps) == 0 {
				return "当前没有待决策的提案。"
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("提案（%d）：\n", len(ps)))
			for i, pr := range ps {
				if i >= 10 {
					sb.WriteString(fmt.Sprintf("…（其余 %d 条）\n", len(ps)-10))
					break
				}
				sb.WriteString(fmt.Sprintf("· %s [%s] %s\n", pr.ID, pr.Status, pr.Title))
			}
			sb.WriteString("回复 /netdev 提案 批准 <编号> 或 /netdev 提案 驳回 <编号> 原因…")
			return sb.String()
		case "批准", "approve", "ok":
			if rest == "" {
				// /netdev 提案 批准 <id> → fields[2]=批准, fields[3]=id
				return "用法：/netdev 提案 批准 <编号>。"
			}
			r := bridge.NetdevProposalApprove(rest)
			if !r.OK {
				return r.Msg
			}
			return "已批准：" + r.Msg + "（执行仍在桌面端运维页操作）。"
		case "驳回", "拒绝", "reject":
			parts := strings.SplitN(rest, " ", 2)
			if len(parts) == 0 || parts[0] == "" {
				return "用法：/netdev 提案 驳回 <编号> 原因…。"
			}
			r := bridge.NetdevProposalReject(parts[0], strings.TrimSpace(parts[1]))
			if !r.OK {
				return r.Msg
			}
			return "已驳回：" + r.Msg
		default:
			// /netdev 提案 <id> 之外的第一段当编号处理无意义——引导用法。
			return "用法：/netdev 提案 | /netdev 提案 批准 <编号> | /netdev 提案 驳回 <编号> 原因…"
		}
	default:
		return "用法：/netdev 发现 | /netdev 详情 <编号> | /netdev ack <编号> | /netdev 提案 [批准|驳回 <编号>]"
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
