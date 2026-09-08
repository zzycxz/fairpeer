package netdev

// opslink_test.go — OPS Phase 1 关联切片红测试：netdev_finding / netdev_propose
// 挂 request_id（台账存在性 fail-closed），ops_status 详情列出关联产出。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/ops"
)

func opsLinkTestEnv(t *testing.T) string {
	t.Helper()
	writeAuthTestEnv(t)
	dir := t.TempDir()
	findingsDirOverr = dir
	proposalsDirOverride = dir
	ops.SetStateDir(dir)
	t.Cleanup(func() {
		findingsDirOverr = ""
		proposalsDirOverride = ""
		ops.SetStateDir("")
	})
	return dir
}

func classifyTestRequest(t *testing.T, risk string) string {
	t.Helper()
	ctx := context.Background()
	out, err := (ops.ClassifyTool{}).Execute(ctx, json.RawMessage(
		`{"text":"core-sw-1 丢包排查","intent":"incident_diagnosis","risk":"`+risk+`","targets":["sw1"]}`))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(strings.Fields(out)[1])
}

func TestFindingLinksToRequest(t *testing.T) {
	opsLinkTestEnv(t)
	ctx := context.Background()
	reqID := classifyTestRequest(t, "read")

	// Bogus request id is rejected before anything persists.
	if _, err := (&findingTool{}).Execute(ctx, json.RawMessage(
		`{"title":"x","severity":"warning","evidence":[{"device":"sw1","command":"show int","output":"drops"}],"request_id":"REQ-20990101-9999"}`)); err == nil || !strings.Contains(err.Error(), "不在统一请求台账") {
		t.Fatalf("bogus request_id must be rejected, got %v", err)
	}

	if _, err := (&findingTool{}).Execute(ctx, json.RawMessage(
		`{"title":"Gi0/1 丢包","severity":"warning","devices":["sw1"],"evidence":[{"device":"sw1","command":"show int gi0/1","output":"drops"}],"request_id":"`+reqID+`"}`)); err != nil {
		t.Fatal(err)
	}
	findings, err := ListFindings()
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings = %d err=%v", len(findings), err)
	}
	if findings[0].RequestID != reqID {
		t.Fatalf("finding RequestID = %q, want %q", findings[0].RequestID, reqID)
	}

	// ops_status surfaces the link through the injected query.
	out, err := (ops.StatusTool{Links: requestLinks}).Execute(ctx, json.RawMessage(`{"request_id":"`+reqID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "关联产出") || !strings.Contains(out, findings[0].ID) || !strings.Contains(out, "finding") {
		t.Fatalf("status detail missing linked finding: %q", out)
	}
}

func TestProposalLinksToRequest(t *testing.T) {
	opsLinkTestEnv(t)
	ctx := context.Background()
	m := newWriteAuthManager(t, "confirm", labDevice())
	reqID := classifyTestRequest(t, "propose")

	// Bogus request id rejected before proposal validation/persist.
	if _, err := (&proposeTool{m: m}).Execute(ctx, json.RawMessage(
		`{"intent":"fix","steps":[{"device":"sw1","commands":["int gi0/1"],"rollback":["no int gi0/1"]}],"request_id":"REQ-bogus"}`)); err == nil || !strings.Contains(err.Error(), "不在统一请求台账") {
		t.Fatalf("bogus request_id must be rejected, got %v", err)
	}

	if _, err := (&proposeTool{m: m}).Execute(ctx, json.RawMessage(
		`{"intent":"修复丢包","steps":[{"device":"sw1","commands":["interface gi0/1"],"rollback":["no interface gi0/1"]}],"request_id":"`+reqID+`"}`)); err != nil {
		t.Fatal(err)
	}
	proposals, err := ListProposals()
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposals = %d err=%v", len(proposals), err)
	}
	if proposals[0].RequestID != reqID {
		t.Fatalf("proposal RequestID = %q, want %q", proposals[0].RequestID, reqID)
	}

	out, err := (ops.StatusTool{Links: requestLinks}).Execute(ctx, json.RawMessage(`{"request_id":"`+reqID+`"}`))
	if err != nil || !strings.Contains(out, "proposal") {
		t.Fatalf("status detail missing linked proposal: %q err=%v", out, err)
	}
}
