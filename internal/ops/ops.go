// Package ops implements OPS_AUTOMATION_PLATFORM_SPEC Phase 1: the unified
// request lifecycle. Every user ask that drives the ops platform — chat, bot,
// schedule, alert — becomes a Request with an identity (request_id), a
// classification (intent/risk), a scope snapshot, and a state-machine trail;
// Jobs, Findings, Cases and Proposals gain a request_id to hang from in the
// later phases. This first slice lands the data model, the state machine and
// the persisted store; plan compilation and UI cards follow.
package ops

import "time"

// RequestState is one node of the §7.1 lifecycle.
type RequestState string

const (
	StateReceived        RequestState = "received"
	StateClassified      RequestState = "classified"
	StateScoped          RequestState = "scoped"
	StateClarified       RequestState = "clarified"
	StatePlanned         RequestState = "planned"
	StateApproved        RequestState = "approved"
	StateRunning         RequestState = "running"
	StateWaitingDecision RequestState = "waiting_decision"
	StateAnalyzed        RequestState = "analyzed"
	StateCompleted       RequestState = "completed"
	StateRemediating     RequestState = "remediating"
	StateVerified        RequestState = "verified"
	StateArchived        RequestState = "archived"
	// StateAborted is terminal-from-anywhere; the transition must carry a
	// reason and the last safe checkpoint.
	StateAborted RequestState = "aborted"
)

// transitions is the §7.1 machine: the linear chain plus the documented
// skips (clarified is only when necessary; approved only for assessment/
// change/red-team classes; remediating only when a fix proposal exists).
var transitions = map[RequestState][]RequestState{
	StateReceived:        {StateClassified, StateAborted},
	StateClassified:      {StateScoped, StateAborted},
	StateScoped:          {StateClarified, StatePlanned, StateAborted},
	StateClarified:       {StatePlanned, StateAborted},
	StatePlanned:         {StateApproved, StateRunning, StateAborted},
	StateApproved:        {StateRunning, StateAborted},
	StateRunning:         {StateWaitingDecision, StateAnalyzed, StateAborted},
	StateWaitingDecision: {StateRunning, StateAborted},
	StateAnalyzed:        {StateCompleted, StateAborted},
	StateCompleted:       {StateRemediating, StateArchived, StateAborted},
	StateRemediating:     {StateVerified, StateWaitingDecision, StateAborted},
	StateVerified:        {StateArchived, StateAborted},
	StateArchived:        {},
	StateAborted:         {},
}

// CanTransition reports whether from → to is legal per the §7.1 machine.
func CanTransition(from, to RequestState) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// Budget caps one request's execution envelope (§7.2).
type Budget struct {
	WallSec  int `json:"wall_sec,omitempty"`
	Commands int `json:"commands,omitempty"`
	Parallel int `json:"parallel,omitempty"`
}

// StateTransition is one hop in the request's audit trail. Reason is REQUIRED
// for aborted (§7.1: 任何状态都可以进入 aborted，但必须写明原因和最后一个
// 安全断点).
type StateTransition struct {
	To     RequestState `json:"to"`
	At     string       `json:"at"`
	Reason string       `json:"reason,omitempty"`
}

// PlanStep is one host-validated step of a Plan (§7.3). The model proposes;
// the compiler validates (target exists, capability match, in scope, kind
// whitelist, budget) before anything executes.
type PlanStep struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"` // read | assess | propose | execute | verify
	Asset        string `json:"asset,omitempty"`
	Operation    string `json:"operation,omitempty"`
	Precondition string `json:"precondition,omitempty"`
	TimeoutSec   int    `json:"timeout_sec,omitempty"`
	OnFailure    string `json:"on_failure,omitempty"` // continue | abort
}

// Plan is the host-normalized execution plan (§7.3).
type Plan struct {
	ID              string     `json:"plan_id"`
	RequestID       string     `json:"request_id"`
	Steps           []PlanStep `json:"steps"`
	ExpectedOutputs []string   `json:"expected_outputs,omitempty"`
	Approval        string     `json:"approval"` // none | required
}

// Request is the §7.2 envelope plus its lifecycle trail.
type Request struct {
	ID     string `json:"request_id"`
	Source string `json:"source"` // chat | bot | schedule | alert
	Actor  string `json:"actor"`
	Text   string `json:"text"`
	// Project and Targets scope the request; ScopeSnapshot pins the managed
	// asset set at start time so a mid-run config change can't widen reach.
	Project       string   `json:"project,omitempty"`
	Targets       []string `json:"targets,omitempty"`
	Intent        string   `json:"intent,omitempty"` // incident_diagnosis | health_check | vulnerability_assessment | change_request | ...
	Risk          string   `json:"risk,omitempty"`   // read | assess | change | verify
	ScopeSnapshot string   `json:"scope_snapshot,omitempty"`
	Budget        *Budget  `json:"budget,omitempty"`

	State        RequestState      `json:"state"`
	StateHistory []StateTransition `json:"state_history,omitempty"`
	Plan         *Plan             `json:"plan,omitempty"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Transition moves the request to `to`, recording why. Illegal hops and
// reason-less aborts are rejected — the trail is the audit surface.
func (r *Request) Transition(to RequestState, reason string) error {
	if r.State == "" {
		r.State = StateReceived
	}
	if !CanTransition(r.State, to) {
		return &IllegalTransitionError{From: r.State, To: to}
	}
	if to == StateAborted && reason == "" {
		return &IllegalTransitionError{From: r.State, To: to, MissingReason: true}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	r.StateHistory = append(r.StateHistory, StateTransition{To: to, At: now, Reason: reason})
	r.State = to
	r.UpdatedAt = now
	return nil
}

// IllegalTransitionError explains a rejected state hop.
type IllegalTransitionError struct {
	From          RequestState
	To            RequestState
	MissingReason bool
}

func (e *IllegalTransitionError) Error() string {
	if e.MissingReason {
		return string("aborting a request requires a reason (and the last safe checkpoint)")
	}
	return "illegal request transition: " + string(e.From) + " → " + string(e.To)
}
