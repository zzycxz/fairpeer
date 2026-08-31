package bot

import (
	"errors"
	"strings"
	"testing"
)

// netdevcmds_test — completion-spec §5.3：IM 闭环命令（ack / 提案 批准驳回）
// 的解析与回执文案。桥为 fake，守卫路径在后端实现侧另有测试。

type fakeNetdevBridge struct {
	ackErr    error
	proposals []NetdevProposalSummary
	approveID string
	rejectID  string
	reason    string
}

func (f *fakeNetdevBridge) NetdevActiveFindings() []NetdevFindingSummary { return nil }
func (f *fakeNetdevBridge) NetdevFindingByID(id string) NetdevFindingDetail {
	return NetdevFindingDetail{NotFound: true}
}
func (f *fakeNetdevBridge) NetdevAckFinding(id string) NetdevActionResult {
	if f.ackErr != nil {
		return NetdevActionResult{Msg: f.ackErr.Error()}
	}
	return NetdevActionResult{OK: true}
}
func (f *fakeNetdevBridge) NetdevProposals() []NetdevProposalSummary { return f.proposals }
func (f *fakeNetdevBridge) NetdevProposalApprove(id string) NetdevActionResult {
	f.approveID = id
	return NetdevActionResult{OK: true, Msg: "调整核心链路 MTU"}
}
func (f *fakeNetdevBridge) NetdevProposalReject(id, reason string) NetdevActionResult {
	f.rejectID, f.reason = id, reason
	return NetdevActionResult{OK: true, Msg: "调整核心链路 MTU"}
}

func newNetdevTestGW(b NetdevBridge) *BotGateway {
	return &BotGateway{cfg: GatewayConfig{Netdev: b}}
}

func TestNetdevAckCommand(t *testing.T) {
	gw := newNetdevTestGW(&fakeNetdevBridge{})
	out := gw.handleNetdevCommand(InboundMessage{Text: "/netdev ack F-1"})
	if !strings.Contains(out, "已确认 F-1") {
		t.Fatalf("ack success text: %q", out)
	}
	if out := gw.handleNetdevCommand(InboundMessage{Text: "/netdev ack"}); !strings.Contains(out, "用法") {
		t.Fatalf("ack without id should show usage, got %q", out)
	}
	gwErr := newNetdevTestGW(&fakeNetdevBridge{ackErr: errors.New("no such finding")})
	if out := gwErr.handleNetdevCommand(InboundMessage{Text: "/netdev ack F-9"}); !strings.Contains(out, "no such finding") {
		t.Fatalf("ack failure should surface error, got %q", out)
	}
}

func TestNetdevProposalCommands(t *testing.T) {
	fb := &fakeNetdevBridge{proposals: []NetdevProposalSummary{{ID: "p-1", Status: "draft", Title: "调整核心链路 MTU"}}}
	gw := newNetdevTestGW(fb)

	out := gw.handleNetdevCommand(InboundMessage{Text: "/netdev 提案"})
	if !strings.Contains(out, "p-1") || !strings.Contains(out, "draft") {
		t.Fatalf("proposal list: %q", out)
	}

	out = gw.handleNetdevCommand(InboundMessage{Text: "/netdev 提案 批准 p-1"})
	if !strings.Contains(out, "已批准") || fb.approveID != "p-1" {
		t.Fatalf("approve: out=%q id=%s", out, fb.approveID)
	}

	out = gw.handleNetdevCommand(InboundMessage{Text: "/netdev 提案 驳回 p-1 回滚窗口冲突"})
	if !strings.Contains(out, "已驳回") || fb.rejectID != "p-1" || fb.reason != "回滚窗口冲突" {
		t.Fatalf("reject: out=%q id=%s reason=%q", out, fb.rejectID, fb.reason)
	}

	if out := gw.handleNetdevCommand(InboundMessage{Text: "/netdev 提案 批准"}); !strings.Contains(out, "用法") {
		t.Fatalf("approve without id should show usage, got %q", out)
	}
}

func TestNetdevProposalEmptyList(t *testing.T) {
	gw := newNetdevTestGW(&fakeNetdevBridge{})
	if out := gw.handleNetdevCommand(InboundMessage{Text: "/netdev 提案"}); !strings.Contains(out, "没有待决策") {
		t.Fatalf("empty list text: %q", out)
	}
}
