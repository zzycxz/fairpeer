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

func TestClassifyToolCreatesAndClassifies(t *testing.T) {
	testStore(t)
	ctx := context.Background()

	// Bad intent / bad risk are rejected without side effects.
	if _, err := (ClassifyTool{}).Execute(ctx, json.RawMessage(`{"text":"x","intent":"fix_it","risk":"read"}`)); err == nil {
		t.Fatal("non-whitelisted intent must be rejected")
	}
	if _, err := (ClassifyTool{}).Execute(ctx, json.RawMessage(`{"text":"x","intent":"reporting","risk":"yolo"}`)); err == nil {
		t.Fatal("non-whitelisted risk must be rejected")
	}
	if _, err := (ClassifyTool{}).Execute(ctx, json.RawMessage(`{"intent":"reporting","risk":"read"}`)); err == nil {
		t.Fatal("creating without text must be rejected")
	}

	out, err := (ClassifyTool{}).Execute(ctx, json.RawMessage(`{
		"text":"core-sw-1 丢包排查","intent":"incident_diagnosis","risk":"read",
		"targets":["core-sw-1"],"clarify":["丢包从什么时候开始的？"]}`))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(strings.Split(out, " ")[1])
	r, err := GetRequest(id)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StateClassified || r.Intent != "incident_diagnosis" || r.Risk != "read" || r.Targets[0] != "core-sw-1" {
		t.Fatalf("classified request = %+v", r)
	}

	// Re-classifying an already-classified request is refused.
	if _, err := (ClassifyTool{}).Execute(ctx, json.RawMessage(`{"request_id":"`+id+`","intent":"reporting","risk":"read"}`)); err == nil {
		t.Fatal("re-classification must be refused")
	}
}

func TestPlanToolValidatesAndInstalls(t *testing.T) {
	testStore(t)
	ctx := context.Background()
	tool := PlanTool{Assets: func() []string { return []string{"core-sw-1", "core-sw-2"} }}

	mk := func(risk string) string {
		out, err := (ClassifyTool{}).Execute(ctx, json.RawMessage(`{"text":"x","intent":"incident_diagnosis","risk":"`+risk+`","targets":["core-sw-1"]}`))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(strings.Split(out, " ")[1])
	}

	cases := []struct {
		name    string
		risk    string
		body    string
		wantErr string
	}{
		{name: "happy read plan", risk: "read", body: `{"request_id":"%s","steps":[{"id":"s1","kind":"read","asset":"core-sw-1","operation":"interface_health","timeout_sec":30,"on_failure":"continue"}],"expected_outputs":["suspect_interface"]}`, wantErr: ""},
		{name: "unknown asset", risk: "read", body: `{"request_id":"%s","steps":[{"id":"s1","kind":"read","asset":"not-managed"}]}`, wantErr: "不在管"},
		{name: "bad kind", risk: "read", body: `{"request_id":"%s","steps":[{"id":"s1","kind":"shell"}]}`, wantErr: "白名单"},
		{name: "duplicate step id", risk: "read", body: `{"request_id":"%s","steps":[{"id":"s1","kind":"read"},{"id":"s1","kind":"read"}]}`, wantErr: "不重复"},
		{name: "implicit write", risk: "read", body: `{"request_id":"%s","steps":[{"id":"s1","kind":"read"},{"id":"s2","kind":"execute","asset":"core-sw-1"}]}`, wantErr: "禁止携带"},
		{name: "timeout out of range", risk: "read", body: `{"request_id":"%s","steps":[{"id":"s1","kind":"read","timeout_sec":900}]}`, wantErr: "600"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := mk(tc.risk)
			_, err := tool.Execute(ctx, json.RawMessage(strings.Replace(tc.body, "%s", id, 1)))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("should pass: %v", err)
				}
				r, gerr := GetRequest(id)
				if gerr != nil || r.State != StatePlanned || r.Plan == nil {
					t.Fatalf("request not planned: %+v err=%v", r, gerr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}

	// execute-classified plans auto-require approval.
	id := mk("execute")
	if _, err := tool.Execute(ctx, json.RawMessage(`{"request_id":"`+id+`","steps":[{"id":"s1","kind":"execute","asset":"core-sw-1"}]}`)); err != nil {
		t.Fatal(err)
	}
	r, _ := GetRequest(id)
	if r.Plan.Approval != "required" {
		t.Fatalf("execute plan approval = %q, want required", r.Plan.Approval)
	}

	// No asset list injected → asset steps fail closed.
	closed := PlanTool{}
	id2 := mk("read")
	if _, err := closed.Execute(ctx, json.RawMessage(`{"request_id":"`+id2+`","steps":[{"id":"s1","kind":"read","asset":"core-sw-1"}]}`)); err == nil {
		t.Fatal("nil asset list must fail closed for asset steps")
	}
}
