// tools.go — the Phase 1 model-facing surface (§17: 实现 ops_classify、
// ops_plan、ops_status). ops_classify is the entry point (mints the request
// and records the §4.2 classification); ops_plan validates and installs a
// host-checked plan (§7.3: 目标存在、步骤类型合法、禁止隐式写入、审批一致);
// ops_status reports the lifecycle.
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// intents is the §4.2 taxonomy — the classify whitelist.
var intents = map[string]bool{
	"incident_diagnosis": true, "health_check": true, "network_path": true,
	"configuration_audit": true, "security_incident": true, "vulnerability_assess": true,
	"credential_assess": true, "change_request": true, "cutover_runbook": true,
	"reporting": true, "scheduled_monitoring": true, "inventory_discovery": true,
	"knowledge_lookup": true,
}

// risks is the four-class risk boundary (§18 门槛 4：只读、评估、提案、执行).
var risks = map[string]bool{"read": true, "assess": true, "propose": true, "execute": true}

// ClassifyTool implements `ops_classify`: mint (or pick up) a request and
// record its classification — intent (§4.2 whitelist), risk class, targets,
// optional clarification questions. Drives received → classified.
type ClassifyTool struct{}

func (ClassifyTool) Name() string { return "ops_classify" }

func (ClassifyTool) Description() string {
	return "统一运维请求的入口：把用户的运维诉求登记为带编号的 Request 并分类（received→classified）。收到明确的运维任务（诊断/巡检/评估/变更/割接/报告…）时先调它拿到 request_id，后续 ops_plan/结论都挂在这个编号下。intent 必须取：incident_diagnosis|health_check|network_path|configuration_audit|security_incident|vulnerability_assess|credential_assess|change_request|cutover_runbook|reporting|scheduled_monitoring|inventory_discovery|knowledge_lookup；risk 必须取：read|assess|propose|execute（只读/评估/提案/执行四类边界，宁低勿高）。"
}

func (ClassifyTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "text":{"type":"string","description":"用户原话诉求（新建请求时必填，作为请求文本留档）"},
  "request_id":{"type":"string","description":"已有请求编号（补分类时用；省略则新建请求，此时 text 必填）"},
  "intent":{"type":"string","description":"§4.2 意图分类（见工具说明白名单）"},
  "risk":{"type":"string","enum":["read","assess","propose","execute"],"description":"风险边界四分法；拿不准就取更保守的一档"},
  "targets":{"type":"array","items":{"type":"string"},"description":"目标资产名（设备名/主机名）"},
  "project":{"type":"string","description":"所属项目/范围（可选）"},
  "clarify":{"type":"array","items":{"type":"string"},"description":"需要向用户澄清的问题（可选）"}
},
"required":["intent","risk"]
}`)
}

func (ClassifyTool) ReadOnly() bool { return true }

func (ClassifyTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Text      string   `json:"text"`
		RequestID string   `json:"request_id"`
		Intent    string   `json:"intent"`
		Risk      string   `json:"risk"`
		Targets   []string `json:"targets"`
		Project   string   `json:"project"`
		Clarify   []string `json:"clarify"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if !intents[p.Intent] {
		return "", fmt.Errorf("intent %q 不在 §4.2 分类法内（13 个意图，见工具说明）", p.Intent)
	}
	if !risks[p.Risk] {
		return "", fmt.Errorf("risk %q 不在四类边界内（read|assess|propose|execute）", p.Risk)
	}

	var r *Request
	if id := strings.TrimSpace(p.RequestID); id != "" {
		var err error
		r, err = GetRequest(id)
		if err != nil {
			return "", fmt.Errorf("找不到请求 %s: %v", id, err)
		}
		if r.State != StateReceived {
			return "", fmt.Errorf("请求 %s 已处于 %s 状态，无需再分类", r.ID, r.State)
		}
	} else {
		if strings.TrimSpace(p.Text) == "" {
			return "", fmt.Errorf("新建请求必须带 text（用户原话诉求）")
		}
		// Mint+persist atomically (concurrent creators must not mint the
		// same id); classification fields land on the follow-up save below.
		var err error
		r, err = MintAndSaveRequest("chat", "agent", p.Text)
		if err != nil {
			return "", err
		}
	}

	intent, risk, targets := "", "", ""
	if err := UpdateRequest(r.ID, func(r *Request) error {
		r.Intent = p.Intent
		r.Risk = p.Risk
		if len(p.Targets) > 0 {
			r.Targets = p.Targets
		}
		if strings.TrimSpace(p.Project) != "" {
			r.Project = p.Project
		}
		intent, risk, targets = r.Intent, r.Risk, strings.Join(r.Targets, ", ")
		return r.Transition(StateClassified, "")
	}); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "请求 %s 已登记并分类：intent=%s risk=%s targets=%s。\n", r.ID, intent, risk, orDash(targets))
	if len(p.Clarify) > 0 {
		fmt.Fprintf(&b, "待澄清（先问用户再计划）：%s\n", strings.Join(p.Clarify, "；"))
	}
	b.WriteString("下一步：ops_plan 产出并校验执行计划（步骤 kind 白名单 read|assess|propose|execute|verify；目标必须在管）。")
	return b.String(), nil
}

// PlanTool implements `ops_plan`: the model proposes steps, the HOST validates
// them (§7.3) and installs the plan — nothing the model says executes as-is.
// Assets injects the in-scope asset names (netdev passes its configured
// devices); nil fails closed: any asset-naming step is rejected.
type PlanTool struct {
	Assets func() []string
}

func (PlanTool) Name() string { return "ops_plan" }

func (PlanTool) Description() string {
	return "为已分类的统一运维请求提交执行计划（classified→scoped→planned）。计划由你起草、由主机校验——目标必须在管资产、kind 仅限 read|assess|propose|execute|verify、on_failure 仅限 continue|abort、timeout 1-600 秒、只读/评估类请求禁止携带 propose/execute 步骤（禁止隐式写入）、含 execute/verify 步骤的计划自动标记需要审批。校验通过后计划落档到请求上。"
}

func (PlanTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "request_id":{"type":"string","description":"已分类请求的编号"},
  "steps":{"type":"array","minItems":1,"items":{
    "type":"object",
    "properties":{
      "id":{"type":"string","description":"步骤短编号，如 s1"},
      "kind":{"type":"string","enum":["read","assess","propose","execute","verify"]},
      "asset":{"type":"string","description":"目标资产名（必须在管）"},
      "operation":{"type":"string","description":"操作语义（如 interface_health）"},
      "timeout_sec":{"type":"integer","minimum":1,"maximum":600},
      "on_failure":{"type":"string","enum":["continue","abort"]}
    },
    "required":["id","kind"]
  }},
  "expected_outputs":{"type":"array","items":{"type":"string"},"description":"计划预期产出（结论要素）"}
},
"required":["request_id","steps"]
}`)
}

func (PlanTool) ReadOnly() bool { return true }

func (t PlanTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		RequestID       string     `json:"request_id"`
		Steps           []PlanStep `json:"steps"`
		ExpectedOutputs []string   `json:"expected_outputs"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	r, err := GetRequest(strings.TrimSpace(p.RequestID))
	if err != nil {
		return "", fmt.Errorf("找不到请求: %v", err)
	}
	if r.State != StateClassified && r.State != StateScoped {
		return "", fmt.Errorf("请求 %s 处于 %s 状态——先 ops_classify 再计划", r.ID, r.State)
	}
	if err := t.validate(r, p.Steps); err != nil {
		return "", err
	}

	needsApproval := false
	for _, s := range p.Steps {
		if s.Kind == "execute" || s.Kind == "verify" {
			needsApproval = true
			break
		}
	}
	planID, planApproval := "", ""
	if err := UpdateRequest(r.ID, func(r *Request) error {
		// Both transitions belong inside the locked span: UpdateRequest reloads
		// the request, so a Scoped transition applied to the outer copy would
		// be lost and Planned would jump straight from Classified.
		if r.State == StateClassified {
			if err := r.Transition(StateScoped, ""); err != nil {
				return err
			}
		}
		r.Plan = &Plan{
			ID:              "PLAN-" + r.ID + "-01",
			RequestID:       r.ID,
			Steps:           p.Steps,
			ExpectedOutputs: p.ExpectedOutputs,
			Approval:        map[bool]string{true: "required", false: "none"}[needsApproval],
		}
		planID, planApproval = r.Plan.ID, r.Plan.Approval
		return r.Transition(StatePlanned, "")
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("计划 %s 已校验并落档：%d 步，审批=%s（请求 %s → planned）。执行编排属后续阶段；当前按计划逐步执行并保持证据链。", planID, len(p.Steps), planApproval, r.ID), nil
}

// validate enforces the §7.3 host checks. It rejects, never repairs — an
// invalid plan goes back to the model, not to a best-effort rewrite.
func (t PlanTool) validate(r *Request, steps []PlanStep) error {
	if len(steps) == 0 {
		return fmt.Errorf("计划至少一步")
	}
	assets := map[string]bool{}
	if t.Assets != nil {
		for _, a := range t.Assets() {
			assets[a] = true
		}
	}
	seen := map[string]bool{}
	readOnlyRisk := r.Risk == "read" || r.Risk == "assess"
	for i, s := range steps {
		label := fmt.Sprintf("步骤 %d(%s)", i+1, orDash(s.ID))
		if strings.TrimSpace(s.ID) == "" || seen[s.ID] {
			return fmt.Errorf("%s: id 必须非空且不重复", label)
		}
		seen[s.ID] = true
		switch s.Kind {
		case "read", "assess", "propose", "execute", "verify":
		default:
			return fmt.Errorf("%s: kind %q 不在白名单", label, s.Kind)
		}
		switch s.OnFailure {
		case "", "continue", "abort":
		default:
			return fmt.Errorf("%s: on_failure %q 仅限 continue|abort", label, s.OnFailure)
		}
		if s.TimeoutSec < 0 || s.TimeoutSec > 600 {
			return fmt.Errorf("%s: timeout_sec 超出 1-600", label)
		}
		if strings.TrimSpace(s.Asset) != "" {
			if !assets[s.Asset] {
				return fmt.Errorf("%s: 目标 %q 不在管（资产必须人工纳管；发现≠可连）", label, s.Asset)
			}
		}
		// 禁止隐式写入：只读/评估类请求不得夹带提案/执行步骤。
		if readOnlyRisk && (s.Kind == "propose" || s.Kind == "execute") {
			return fmt.Errorf("%s: 请求风险级为 %s，禁止携带 %s 步骤——先与用户确认变更意图并重新分类", label, r.Risk, s.Kind)
		}
	}
	return nil
}

// Link is one data-plane artifact hanging off a request (finding / proposal /
// case / job) — injected by the profile so ops stays decoupled from netdev.
type Link struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// StatusTool implements `ops_status`: one request's lifecycle trail, or the
// recent request list when no id is given. Read-only by construction.
// Links, when injected, lists the request's linked artifacts (Phase 1:
// findings and proposals stamped with request_id).
type StatusTool struct {
	Links func(requestID string) []Link
}

func (StatusTool) Name() string { return "ops_status" }

func (StatusTool) Description() string {
	return "查看统一运维请求（Request）的状态：不传 request_id 时列出最近的请求（编号/来源/意图/风险/状态/更新时间）；传 request_id 时给出该请求的完整生命周期轨迹（状态历史、计划摘要、挂在该请求下的发现与变更提案）。运维平台把对话、定时任务、告警统一建模为带 request_id 的请求，后续诊断/变更/评估都挂在它下面——排查“之前那件事做到哪了”用它。"
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

func (t StatusTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
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
		return statusDetail(id, t.Links)
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

func statusDetail(id string, links func(string) []Link) (string, error) {
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
	if links != nil {
		if ls := links(r.ID); len(ls) > 0 {
			b.WriteString("关联产出:\n")
			for _, l := range ls {
				fmt.Fprintf(&b, "  [%s] %s %s\n", l.Kind, l.ID, firstLine(l.Title, 60))
			}
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
