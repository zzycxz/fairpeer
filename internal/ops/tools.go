// tools.go — the Phase 1 model-facing surface (§17: 实现 ops_classify、
// ops_plan、ops_status). This slice ships ops_status; classify and plan land
// with the classifier and the plan compiler in the next slices.
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// StatusTool implements `ops_status`: one request's lifecycle trail, or the
// recent request list when no id is given. Read-only by construction.
type StatusTool struct{}

func (StatusTool) Name() string { return "ops_status" }

func (StatusTool) Description() string {
	return "查看统一运维请求（Request）的状态：不传 request_id 时列出最近的请求（编号/来源/意图/风险/状态/更新时间）；传 request_id 时给出该请求的完整生命周期轨迹（状态历史、计划摘要）。运维平台把对话、定时任务、告警统一建模为带 request_id 的请求，后续诊断/变更/评估都挂在它下面——排查“之前那件事做到哪了”用它。"
}

func (StatusTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "request_id":{"type":"string","description":"要查看的请求编号（如 REQ-20260908-0001）；省略则列出最近的请求"},
  "limit":{"type":"integer","description":"列表模式的条数上限，默认 20","minimum":1,"maximum":100}
}
}`)
}

func (StatusTool) ReadOnly() bool { return true }

func (StatusTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		RequestID string `json:"request_id"`
		Limit     int    `json:"limit"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if id := strings.TrimSpace(p.RequestID); id != "" {
		return statusDetail(id)
	}
	return statusList(p.Limit)
}

func statusList(limit int) (string, error) {
	rows := ListRequests(limit)
	if len(rows) == 0 {
		return "尚无统一运维请求记录。", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "最近 %d 条统一运维请求：\n", len(rows))
	for _, r := range rows {
		targets := strings.Join(r.Targets, ",")
		if targets != "" {
			targets = " → " + targets
		}
		fmt.Fprintf(&b, "- %s [%s] %s%s（%s/%s，更新 %s）\n",
			r.ID, r.State, firstLine(r.Text, 60), targets,
			orDash(r.Intent), orDash(r.Risk), r.UpdatedAt)
	}
	b.WriteString("传 request_id 查看单条完整轨迹。")
	return b.String(), nil
}

func statusDetail(id string) (string, error) {
	r, err := GetRequest(id)
	if err != nil {
		return "", fmt.Errorf("找不到请求 %s（%v）——先用不带参数的 ops_status 列出可用编号", id, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]\n来源: %s · 提起人: %s\n意图: %s · 风险: %s\n目标: %s\n内容: %s\n",
		r.ID, r.State, orDash(r.Source), orDash(r.Actor),
		orDash(r.Intent), orDash(r.Risk), orDash(strings.Join(r.Targets, ", ")),
		firstLine(r.Text, 200))
	if r.Plan != nil {
		fmt.Fprintf(&b, "计划 %s：%d 步（审批: %s）\n", r.Plan.ID, len(r.Plan.Steps), r.Plan.Approval)
		for _, s := range r.Plan.Steps {
			fmt.Fprintf(&b, "  %s. %s %s %s（失败时 %s）\n", s.ID, s.Kind, s.Asset, s.Operation, orDash(s.OnFailure))
		}
	}
	if len(r.StateHistory) > 0 {
		b.WriteString("轨迹:\n")
		for _, t := range r.StateHistory {
			reason := ""
			if t.Reason != "" {
				reason = " —— " + t.Reason
			}
			fmt.Fprintf(&b, "  %s %s%s\n", t.At, t.To, reason)
		}
	}
	return b.String(), nil
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	if s == "" {
		return "(无文本)"
	}
	return s
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
