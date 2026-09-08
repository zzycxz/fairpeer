package ops

// ops_test.go — OPS_AUTOMATION_PLATFORM_SPEC Phase 1 红测试：状态机合法性、
// 请求台账读写、ops_status 工具行为。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func testStore(t *testing.T) {
	t.Helper()
	SetStateDir(t.TempDir())
	t.Cleanup(func() { SetStateDir("") })
}

func TestStateMachineLegality(t *testing.T) {
	legal := []struct{ from, to RequestState }{
		{StateReceived, StateClassified},
		{StateClassified, StateScoped},
		{StateScoped, StatePlanned},  // clarified 可跳（"必要时"）
		{StatePlanned, StateRunning}, // 读类无需审批
		{StateRunning, StateWaitingDecision},
		{StateWaitingDecision, StateRunning},
		{StateCompleted, StateRemediating},
		{StateRemediating, StateVerified},
	}
	for _, tc := range legal {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("%s → %s should be legal", tc.from, tc.to)
		}
	}
	illegal := []struct{ from, to RequestState }{
		{StateReceived, StateRunning}, // 必须先分类
		{StateScoped, StateApproved},  // 审批只属于计划之后
		{StateArchived, StateRunning}, // 归档终态
		{StateAborted, StateRunning},  // 中止终态
		{StateAnalyzed, StateScoped},  // 不可回退
	}
	for _, tc := range illegal {
		if CanTransition(tc.from, tc.to) {
			t.Errorf("%s → %s should be illegal", tc.from, tc.to)
		}
	}
	// aborted 可从任意非终态进入
	for _, from := range []RequestState{StateReceived, StateScoped, StateRunning, StateVerified} {
		if !CanTransition(from, StateAborted) {
			t.Errorf("%s → aborted should be legal", from)
		}
	}
}

func TestTransitionRecordsTrailAndRequiresAbortReason(t *testing.T) {
	r := NewRequest("chat", "local-user", "生产区 core-sw-1 丢包，查一下")
	if err := r.Transition(StateClassified, ""); err != nil {
		t.Fatalf("classify: %v", err)
	}
	if err := r.Transition(StateAborted, ""); err == nil {
		t.Fatal("abort without a reason must be rejected")
	}
	if err := r.Transition(StateAborted, "用户要求停止；安全断点=只读诊断完成"); err != nil {
		t.Fatalf("abort with reason: %v", err)
	}
	if r.State != StateAborted || len(r.StateHistory) != 2 {
		t.Fatalf("trail = %+v", r.StateHistory)
	}
	if err := r.Transition(StateRunning, ""); err == nil {
		t.Fatal("aborted is terminal")
	}
}

func TestRequestStoreRoundtripAndSequencing(t *testing.T) {
	testStore(t)

	r1 := NewRequest("chat", "local-user", "first")
	r1.Intent, r1.Risk = "incident_diagnosis", "read"
	r1.Targets = []string{"core-sw-1"}
	if err := SaveRequest(r1); err != nil {
		t.Fatal(err)
	}
	r2 := NewRequest("schedule", "scheduler", "second")
	if err := SaveRequest(r2); err != nil {
		t.Fatal(err)
	}
	if r1.ID == r2.ID {
		t.Fatalf("ids not unique: %s", r1.ID)
	}
	if !strings.HasSuffix(r2.ID, "-0002") {
		t.Fatalf("daily sequence = %s, want ...-0002", r2.ID)
	}

	got, err := GetRequest(r1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "first" || got.Intent != "incident_diagnosis" || got.Targets[0] != "core-sw-1" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}

	rows := ListRequests(10)
	if len(rows) != 2 || rows[0].ID != r2.ID {
		t.Fatalf("list = %+v, want newest-first [second first]", rows)
	}
}

func TestStatusToolListAndDetail(t *testing.T) {
	testStore(t)
	ctx := context.Background()

	out, err := (StatusTool{}).Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "尚无") {
		t.Fatalf("empty ledger message = %q", out)
	}

	r := NewRequest("chat", "local-user", "core-sw-1 丢包\n详细描述")
	r.Intent, r.Risk = "incident_diagnosis", "read"
	if err := r.Transition(StateClassified, ""); err != nil {
		t.Fatal(err)
	}
	if err := SaveRequest(r); err != nil {
		t.Fatal(err)
	}

	out, err = (StatusTool{}).Execute(ctx, json.RawMessage(`{}`))
	if err != nil || !strings.Contains(out, r.ID) || !strings.Contains(out, "classified") {
		t.Fatalf("list output = %q err=%v", out, err)
	}

	out, err = (StatusTool{}).Execute(ctx, json.RawMessage(`{"request_id":"`+r.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{r.ID, "incident_diagnosis", "丢包", "classified"} {
		if !strings.Contains(out, want) {
			t.Fatalf("detail missing %q in %q", want, out)
		}
	}

	if _, err := (StatusTool{}).Execute(ctx, json.RawMessage(`{"request_id":"../etc/passwd"}`)); err == nil {
		t.Fatal("path traversal id must be rejected")
	}
}
