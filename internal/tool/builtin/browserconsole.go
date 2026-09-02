package builtin

// browserconsole.go — the ops console's manual browser-control API.
//
// 运维 dock 的「浏览器」 tab 驱动同一套 chromedp 会话
// infrastructure the agent tools use, minus the LLM: the user issues
// primitives (navigate/type/click/…) from the panel, watches the external
// browser window, and can record a manual run into a trace that the desktop
// layer later distills into a SKILL.md. Everything routes through the
// existing session pool so mirror frames, idle reaping and attach mode all
// behave identically to agent-driven sessions.
//
// Scope guards (plan: 受控版):
//   - the console primitives are exactly the 11 step verbs; nothing else
//   - recording is capture-only: deterministic filtering happens here in
//     pure functions, AI comprehension lives in the desktop layer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// --- console session slot -------------------------------------------------------

// ConsoleState describes the console's browser session for the panel header.
type ConsoleState struct {
	Open      bool   `json:"open"`
	SessionID string `json:"session_id"`
	Browser   string `json:"browser"`
	Attached  bool   `json:"attached"`
	URL       string `json:"url"`
	// Keep-alive (会话保活): a long-idle console session survives both the
	// kernel's idle reaper and the site's own session expiry.
	KeepAlive     bool   `json:"keep_alive"`
	KeepAliveMode string `json:"keep_alive_mode"` // ping|navigate|local
	KeepAliveURL  string `json:"keep_alive_url"`  // navigate-mode target ("" = current page)
	KeepAliveLast int64  `json:"keep_alive_last"` // unix millis of the last successful refresh
	KeepAliveErr  string `json:"keep_alive_err"`  // last refresh failure ("" = ok)
}

var (
	consoleMu        sync.Mutex
	consoleSessionID string
)

func consoleSession() (*browserSession, error) {
	consoleMu.Lock()
	id := consoleSessionID
	consoleMu.Unlock()
	if id == "" {
		return nil, errors.New("浏览器会话未打开——先点击「打开浏览器」")
	}
	return getBrowserSession(id)
}

// consoleTabCtx derives a bounded context from the session's tab context —
// direct chromedp.Run calls (not routed through runBrowserAction) need it to
// reach the right target; cancelling it never kills the browser.
func consoleTabCtx(s *browserSession) (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.ctx, browserActionTimeout)
}

// ConsoleOpen ensures the console session exists — spawning a visible browser
// (independent tab, isolated from the agent's own sessions) or attaching to
// an external one when cdpURL is set — and optionally navigates to startURL.
func ConsoleOpen(cdpURL, startURL string) (ConsoleState, error) {
	consoleMu.Lock()
	id := consoleSessionID
	consoleMu.Unlock()

	if id != "" {
		if s, err := getBrowserSession(id); err == nil {
			// Session alive — reuse it (this also refreshes lastUsed against
			// the idle reaper).
			if startURL != "" {
				if _, err := ConsoleNavigate(startURL); err != nil {
					return ConsoleState{}, err
				}
			}
			return consoleStateOf(s)
		}
		// Stale slot — clear and respawn below.
		consoleMu.Lock()
		consoleSessionID = ""
		consoleMu.Unlock()
	}

	var (
		s   *browserSession
		err error
	)
	if strings.TrimSpace(cdpURL) != "" {
		s, err = newAttachedSession(cdpURL)
	} else {
		s, err = newBrowserSession()
	}
	if err != nil {
		return ConsoleState{}, err
	}
	consoleMu.Lock()
	consoleSessionID = s.id
	consoleMu.Unlock()
	initConsoleRecorderListener(s)
	if startURL != "" {
		if _, nerr := ConsoleNavigate(startURL); nerr != nil {
			return ConsoleState{}, fmt.Errorf("已打开浏览器但导航失败: %w", nerr)
		}
	}
	return consoleStateOf(s)
}

// ConsoleClose stops any active recording, then closes the session (the
// session-level keep-alive loop dies with the session).
func ConsoleClose() error {
	_ = ConsoleRecordStop()
	consoleMu.Lock()
	id := consoleSessionID
	consoleSessionID = ""
	consoleMu.Unlock()
	if id == "" {
		return nil
	}
	closeBrowserSession(id)
	return nil
}

func consoleStateOf(s *browserSession) (ConsoleState, error) {
	state := ConsoleState{
		Open:      true,
		SessionID: s.id,
		Browser:   s.browser,
		Attached:  s.attached,
	}
	s.keepMu.Lock()
	state.KeepAlive = s.keepOn
	state.KeepAliveMode = s.keepMode
	state.KeepAliveURL = s.keepURL
	state.KeepAliveLast = s.keepLast
	state.KeepAliveErr = s.keepErr
	s.keepMu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	_ = chromedp.Run(ctx, chromedp.Location(&state.URL))
	return state, nil
}

// ConsoleStateOf reports the current session, or a zero (closed) state.
func ConsoleStateOf() (ConsoleState, error) {
	s, err := consoleSession()
	if err != nil {
		return ConsoleState{Open: false}, nil
	}
	return consoleStateOf(s)
}

// ConsoleSetKeepAlive arms or disarms the console session's keep-alive loop
// (session-level machinery lives in browser.go — the same loop the
// browser_keepalive tool arms on agent sessions) and returns the resulting
// state. mode: "" or "ping" (page heartbeat fetch), "navigate" (periodic
// reload), "local" (reaper-side only). intervalSec clamps to [60, 3600]
// with a 300s default.
func ConsoleSetKeepAlive(enabled bool, intervalSec int, mode, url string) (ConsoleState, error) {
	s, err := consoleSession()
	if err != nil {
		if !enabled {
			// Nothing to disarm is not an error for the off-switch.
			return ConsoleStateOf()
		}
		return ConsoleState{}, err
	}
	if err := armBrowserKeepAlive(s, enabled, intervalSec, mode, url); err != nil {
		return ConsoleState{}, err
	}
	return consoleStateOf(s)
}

// ConsoleDetectOnce checks a wait-style condition a single time WITHOUT
// blocking — the human-breakpoint auto-continue poll. Supported conditions:
// visible:<selector>, hidden:<selector>, url:<text> (location contains),
// title:<text> (title contains). Empty condition reports true.
func ConsoleDetectOnce(condition string) (bool, string, error) {
	cond := strings.TrimSpace(condition)
	if cond == "" {
		return true, "", nil
	}
	s, err := consoleSession()
	if err != nil {
		return false, "", err
	}
	return detectOnce(s, cond)
}

// detectOnce checks a wait-style condition on one session, non-blocking —
// shared by the console's human-breakpoint poll and the deterministic flow
// runner's human steps.
func detectOnce(s *browserSession, cond string) (bool, string, error) {
	expr := `(function(){
		try {
			var cond = ` + fmt.Sprintf("%q", cond) + `;
			if (cond.indexOf('visible:') === 0) {
				var el = document.querySelector(cond.slice(8));
				return el && el.offsetParent !== null ? 'yes' : 'no';
			}
			if (cond.indexOf('hidden:') === 0) {
				var h = document.querySelector(cond.slice(7));
				return !h || h.offsetParent === null ? 'yes' : 'no';
			}
			if (cond.indexOf('url:') === 0) {
				return location.href.indexOf(cond.slice(4)) !== -1 ? 'yes' : 'no';
			}
			if (cond.indexOf('title:') === 0) {
				return document.title.indexOf(cond.slice(6)) !== -1 ? 'yes' : 'no';
			}
			return 'unsupported';
		} catch (e) { return 'err ' + (e && e.message ? e.message : 'check failed'); }
	})()`
	ctx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	var out string
	if rerr := chromedp.Run(ctx, chromedp.Evaluate(expr, &out)); rerr != nil {
		return false, "", fmt.Errorf("检测条件 %q: %w", cond, rerr)
	}
	switch {
	case out == "yes":
		return true, "", nil
	case out == "no":
		return false, "", nil
	case strings.HasPrefix(out, "err "):
		return false, "", fmt.Errorf("检测条件 %q: %s", cond, strings.TrimPrefix(out, "err "))
	default:
		return false, "", fmt.Errorf("不支持的检测条件 %q（visible:/hidden:/url:/title:）", cond)
	}
}

// --- element listing --------------------------------------------------------------

// ConsoleElement is one interactive element from the page's accessibility
// tree — the panel's element picker. Refs are snapshot-transient: usable for
// immediate click/type, but persisted skills must use CSS selectors.
type ConsoleElement struct {
	Ref   string `json:"ref"`
	Role  string `json:"role"`
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

const consoleElementsCap = 200

func ConsoleElements() ([]ConsoleElement, error) {
	s, err := consoleSession()
	if err != nil {
		return nil, err
	}
	nodes, err := captureAXTree(s)
	if err != nil {
		return nil, fmt.Errorf("capture accessibility tree: %w", err)
	}
	refs, _ := buildSnapshotRefs(nodes)
	// Publish the refs into the session exactly like browser_snapshot does —
	// the panel's click/type go through the agent tools' ref resolution, which
	// only reads s.refs. Without this store, picking an element from the list
	// then clicking failed with "no snapshot taken for session".
	published := refs
	s.refs.Store(&published)
	out := make([]ConsoleElement, 0, len(refs))
	for ref, info := range refs {
		out = append(out, ConsoleElement{Ref: ref, Role: info.role, Name: info.name, Value: info.value})
	}
	sort.Slice(out, func(i, j int) bool {
		return refNumber(out[i].Ref) < refNumber(out[j].Ref)
	})
	if len(out) > consoleElementsCap {
		out = out[:consoleElementsCap]
	}
	// captureAXTree bypasses runBrowserAction, so the mirror got no frame —
	// push one so the panel preview reflects "where am I".
	mirrorAfterAction(s)
	return out, nil
}

func refNumber(ref string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(ref, "e"))
	return n
}

// --- primitives (thin wrappers over the agent tools) -------------------------------

// consoleExec marshals args plus the console session id and runs one agent
// tool's Execute — zero changes to existing tool paths, mirror frames flow
// for free through runBrowserAction.
func consoleExec(exec func(ctx context.Context, args json.RawMessage) (string, error), args map[string]any) (string, error) {
	s, err := consoleSession()
	if err != nil {
		return "", err
	}
	args["session_id"] = s.id
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("marshal console args: %w", err)
	}
	ctx, cancel := consoleTabCtx(s)
	defer cancel()
	return exec(ctx, raw)
}

// targetArg splits a ref-or-CSS target into the ref/selector fields the
// type-family tools expect.
func targetArg(args map[string]any, target string) {
	if looksLikeRef(target) {
		args["ref"] = target
	} else {
		args["selector"] = target
	}
}

func ConsoleNavigate(url string) (string, error) {
	return consoleExec(browserNavigate{}.Execute, map[string]any{"url": url})
}

// ConsoleTab is one open tab of the console session's browser.
type ConsoleTab struct {
	Index   int    `json:"index"` // 1-based
	Title   string `json:"title"`
	URL     string `json:"url"`
	Current bool   `json:"current"`
}

// ConsoleTabs lists the console browser's tabs, marking the one the session
// drives — the panel's tab strip.
func ConsoleTabs() ([]ConsoleTab, error) {
	s, err := consoleSession()
	if err != nil {
		return nil, err
	}
	infos, err := pageTargetInfos(s)
	if err != nil {
		return nil, err
	}
	cur := sessionTargetID(s)
	out := make([]ConsoleTab, 0, len(infos))
	for i, t := range infos {
		out = append(out, ConsoleTab{
			Index:   i + 1,
			Title:   strings.TrimSpace(t.Title),
			URL:     t.URL,
			Current: t.TargetID == cur,
		})
	}
	return out, nil
}

// ConsoleSwitchTab switches the console session onto another tab (1-based
// index from ConsoleTabs); the current tab stays open.
func ConsoleSwitchTab(index int) (string, error) {
	return consoleExec(browserSwitchTab{}.Execute, map[string]any{"index": index})
}

// ConsoleHighlight flashes an outline around a target (ref or CSS) and
// scrolls it into view — the panel's hover/click affordance so the user can
// SEE which page element a list row refers to. Purely cosmetic: previous
// inline styles are restored after the duration; no DOM structure changes.
func ConsoleHighlight(target string, durationMs int) error {
	s, err := consoleSession()
	if err != nil {
		return err
	}
	if durationMs <= 0 {
		durationMs = 800
	}
	if durationMs > 5000 {
		durationMs = 5000
	}
	js := `(function(){
  this.scrollIntoView({block: "center", behavior: "instant"});
  var el = this;
  var prevO = el.style.outline, prevOS = el.style.outlineOffset, prevBg = el.style.backgroundColor;
  el.style.outline = "3px solid #ff8c00";
  el.style.outlineOffset = "1px";
  el.style.backgroundColor = "rgba(255,140,0,0.15)";
  setTimeout(function(){
    el.style.outline = prevO; el.style.outlineOffset = prevOS; el.style.backgroundColor = prevBg;
  }, DURATION);
  return true;
})()`
	js = strings.Replace(js, "DURATION", strconv.Itoa(durationMs), 1)
	ctx, cancel := consoleTabCtx(s)
	defer cancel()
	if looksLikeRef(target) {
		if _, err := callOnRef(ctx, s, target, js); err != nil {
			return fmt.Errorf("高亮 %q: %w", target, err)
		}
		return nil
	}
	expr := strings.Replace(`(function(){
  var el = document.querySelector(SELECTOR);
  if (!el) return false;
  el.scrollIntoView({block: "center", behavior: "instant"});
  var prevO = el.style.outline, prevOS = el.style.outlineOffset, prevBg = el.style.backgroundColor;
  el.style.outline = "3px solid #ff8c00";
  el.style.outlineOffset = "1px";
  el.style.backgroundColor = "rgba(255,140,0,0.15)";
  setTimeout(function(){
    el.style.outline = prevO; el.style.outlineOffset = prevOS; el.style.backgroundColor = prevBg;
  }, DURATION);
  return true;
})()`, "SELECTOR", fmt.Sprintf("%q", target), 1)
	expr = strings.Replace(expr, "DURATION", strconv.Itoa(durationMs), 1)
	var ok bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(expr, &ok)); err != nil {
		return fmt.Errorf("高亮 %q: %w", target, err)
	}
	return nil
}

// ConsoleHover hovers the pointer over a target (ref or CSS) — a real
// mousemove to the element center, so CSS :hover menus open.
func ConsoleHover(target string) (string, error) {
	return consoleExec(browserHover{}.Execute, map[string]any{"target": target})
}

// ConsoleBack goes back one history entry in the console session.
func ConsoleBack() (string, error) {
	return consoleExec(browserBack{}.Execute, map[string]any{})
}

// ConsoleForward goes forward one history entry in the console session.
func ConsoleForward() (string, error) {
	return consoleExec(browserForward{}.Execute, map[string]any{})
}

// ConsoleClick accepts a snapshot ref ("e5") or a CSS selector.
func ConsoleClick(target string) (string, error) {
	return consoleExec(browserClick{}.Execute, map[string]any{"target": target})
}

// ConsoleType types text into the element named by target (ref or CSS),
// clearing it first.
func ConsoleType(target, text string) (string, error) {
	args := map[string]any{"text": text, "clear": true}
	targetArg(args, target)
	return consoleExec(browserType{}.Execute, args)
}

// ConsoleKey presses a special key: enter | tab | escape.
func ConsoleKey(key string) error {
	s, err := consoleSession()
	if err != nil {
		return err
	}
	return dispatchSpecialKey(s, key)
}

// dispatchSpecialKey presses enter/tab/escape on a session's page — shared by
// the console primitive and the deterministic flow runner.
func dispatchSpecialKey(s *browserSession, key string) error {
	var keyName, code string
	var vk int64
	var text string
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "enter", "return":
		keyName, code, vk, text = "Enter", "Enter", 13, "\r"
	case "tab":
		keyName, code, vk = "Tab", "Tab", 9
	case "escape", "esc":
		keyName, code, vk = "Escape", "Escape", 27
	default:
		return fmt.Errorf("unsupported key %q (enter|tab|escape)", key)
	}
	down := &input.DispatchKeyEventParams{
		Type:                  input.KeyRawDown,
		Key:                   keyName,
		Code:                  code,
		WindowsVirtualKeyCode: vk,
		NativeVirtualKeyCode:  vk,
	}
	if text != "" {
		down.Type = input.KeyDown
		down.Text = text
	}
	up := &input.DispatchKeyEventParams{
		Type:                  input.KeyUp,
		Key:                   keyName,
		Code:                  code,
		WindowsVirtualKeyCode: vk,
		NativeVirtualKeyCode:  vk,
	}
	ctx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	return chromedp.Run(ctx, down, up)
}

func ConsoleScroll(direction string, amount int) (string, error) {
	if amount <= 0 {
		amount = 3
	}
	return consoleExec(browserScroll{}.Execute, map[string]any{"direction": direction, "amount": amount})
}

// ConsoleSelectOption picks an option on a <select> by value or label.
func ConsoleSelectOption(target, value string) (string, error) {
	args := map[string]any{"value": value}
	targetArg(args, target)
	return consoleExec(browserSelectOption{}.Execute, args)
}

func ConsoleUploadFile(target string, files []string) (string, error) {
	args := map[string]any{"files": files}
	targetArg(args, target)
	return consoleExec(browserUploadFile{}.Execute, args)
}

func ConsoleWait(condition string, timeoutSec int) (string, error) {
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	return consoleExec(browserWait{}.Execute, map[string]any{"condition": condition, "timeout": timeoutSec})
}

func ConsoleExtract(selector string) (string, error) {
	args := map[string]any{}
	if selector != "" {
		args["selector"] = selector
	}
	return consoleExec(browserExtract{}.Execute, args)
}

// ConsoleExtractTable extracts every <table> under selector (or the page) as
// markdown — structure-preserving for log grids and result tables.
func ConsoleExtractTable(selector string) (string, error) {
	args := map[string]any{"format": "table"}
	if selector != "" {
		args["selector"] = selector
	}
	return consoleExec(browserExtract{}.Execute, args)
}

func ConsoleEvaluate(expression string) (string, error) {
	return consoleExec(browserEvaluate{}.Execute, map[string]any{"expression": expression})
}

// ConsoleScreenshot captures the current viewport as a PNG data URL — the
// panel's visual check plus a potential verification artifact.
func ConsoleScreenshot() (string, error) {
	s, err := consoleSession()
	if err != nil {
		return "", err
	}
	var buf []byte
	if err := runBrowserAction(context.Background(), s, chromedp.CaptureScreenshot(&buf)); err != nil {
		return "", fmt.Errorf("screenshot: %w", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf), nil
}

// --- recorder ----------------------------------------------------------------------

// ConsoleRecordEvent is one captured user interaction (or navigation) while
// recording. Password values are never captured — Password marks the field
// so the generated skill prompts at run time instead.
type ConsoleRecordEvent struct {
	Type     string `json:"type"` // click|input|change|submit|navigate|scroll|effect
	Selector string `json:"selector,omitempty"`
	Role     string `json:"role,omitempty"`
	Name     string `json:"name,omitempty"`
	Value    string `json:"value,omitempty"` // scroll: "<direction> <screens>"
	URL      string `json:"url,omitempty"`
	Time     int64  `json:"time"` // unix millis
	// Effective (click only, merged from follow-up "effect" events): the
	// click observably changed the DOM. nil = unknown.
	Effective *bool `json:"effective,omitempty"`
	Password  bool  `json:"password,omitempty"`
}

// The page→Go channel name and the stop flag must match the injected script.
const consoleRecordBinding = "__fpRec"

// browserRecordSink forwards each captured event to the desktop (live step
// stream in the recording sub-tab). nil (CLI) → no forwarding.
var browserRecordSink func(ConsoleRecordEvent)

// SetBrowserRecordSink registers the desktop's event forwarder. Set once at
// startup; the listener no-ops on a nil sink.
func SetBrowserRecordSink(fn func(ConsoleRecordEvent)) { browserRecordSink = fn }

func emitRecordEvent(rec ConsoleRecordEvent) {
	if browserRecordSink != nil {
		browserRecordSink(rec)
	}
}

var (
	// recorderInstalling is a CAS guard so only one Start runs at a time.
	recorderInstalling atomic.Bool
	// recorderActive is read by the target listener (event-loop context) —
	// atomic so the listener never needs a lock.
	recorderActive atomic.Bool
	// recorderEventMu guards ONLY the events slice + script id. It is held
	// briefly inside the listener and around swaps — NEVER across a
	// chromedp.Run, which would deadlock chromedp's event loop (a hung Run
	// holding a lock the listener needs blocks response dispatch for the
	// whole session; verified live on 2026-08-29 E2E).
	recorderEventMu  sync.Mutex
	recorderEvents   []ConsoleRecordEvent
	recorderScriptID page.ScriptIdentifier // AddScriptToEvaluateOnNewDocument id
)

// ConsoleRecordStart (re)arms the recorder on the console session: a CDP
// binding carries page events back, and the injected script persists across
// navigations (new-document hook) so a multi-page run records end-to-end.
// Installation is a handful of millisecond-level CDP calls — a 15s budget
// fails fast instead of piling onto a wedged session for a full minute.
func ConsoleRecordStart() error {
	s, err := consoleSession()
	if err != nil {
		return err
	}
	if recorderActive.Load() {
		return nil
	}
	if !recorderInstalling.CompareAndSwap(false, true) {
		return errors.New("录制器正在启动，请稍候")
	}
	defer recorderInstalling.Store(false)
	// Install for future documents, then for the current page (the script
	// self-guards with __fpRecInstalled so re-runs never double-bind).
	var scriptID page.ScriptIdentifier
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx,
		runtime.AddBinding(consoleRecordBinding),
		chromedp.ActionFunc(func(c context.Context) error {
			id, err := page.AddScriptToEvaluateOnNewDocument(consoleRecorderJS).Do(c)
			scriptID = id
			return err
		}),
		chromedp.Evaluate("window.__fpRecOff=false; void 0;", nil),
		chromedp.Evaluate(consoleRecorderJS, nil),
	); err != nil {
		return fmt.Errorf("install recorder: %w", err)
	}
	recorderEventMu.Lock()
	recorderEvents = nil
	recorderScriptID = scriptID
	recorderEventMu.Unlock()
	recorderActive.Store(true)
	return nil
}

// ConsoleRecordStop disarms the recorder and returns the captured trace
// as-is (deterministic filtering is a separate, pure step —
// FilterRecordEvents; AI comprehension lives in the desktop layer).
func ConsoleRecordStop() []ConsoleRecordEvent {
	if !recorderActive.Swap(false) {
		return nil
	}
	recorderEventMu.Lock()
	events := recorderEvents
	recorderEvents = nil
	script := recorderScriptID
	recorderScriptID = ""
	recorderEventMu.Unlock()
	if s, err := consoleSession(); err == nil {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		defer cancel()
		stops := []chromedp.Action{chromedp.Evaluate("window.__fpRecOff=true; void 0;", nil)}
		if script != "" {
			stops = append(stops, page.RemoveScriptToEvaluateOnNewDocument(script))
		}
		_ = chromedp.Run(ctx, stops...)
	}
	return events
}

// initConsoleRecorderListener wires the binding/navigation listener for one
// console session. Called once per session from ConsoleOpen; the listener
// no-ops unless recorderActive is set, so it can stay installed for the
// session's whole life (chromedp listeners cannot be removed individually).
//
// The handler runs ON chromedp's event loop and must never block on a lock
// that any chromedp.Run path holds — that deadlock wedges every later
// command on the session.
func initConsoleRecorderListener(s *browserSession) {
	chromedp.ListenTarget(s.ctx, func(ev interface{}) {
		if !recorderActive.Load() {
			return
		}
		switch e := ev.(type) {
		case *runtime.EventBindingCalled:
			if e.Name != consoleRecordBinding {
				return
			}
			var rec ConsoleRecordEvent
			if err := json.Unmarshal([]byte(e.Payload), &rec); err != nil {
				return
			}
			rec.Time = time.Now().UnixMilli()
			recorderEventMu.Lock()
			recorderEvents = append(recorderEvents, rec)
			recorderEventMu.Unlock()
			emitRecordEvent(rec)
		case *page.EventFrameNavigated:
			if e.Frame != nil && e.Frame.ParentID == "" {
				nav := ConsoleRecordEvent{
					Type: "navigate",
					URL:  e.Frame.URL,
					Time: time.Now().UnixMilli(),
				}
				recorderEventMu.Lock()
				recorderEvents = append(recorderEvents, nav)
				recorderEventMu.Unlock()
				emitRecordEvent(nav)
			}
		}
	})
}

// --- recorder script -----------------------------------------------------------------

const consoleRecorderJS = `(() => {
  if (window.__fpRecInstalled) return;
  window.__fpRecInstalled = true;
  const send = (o) => {
    if (window.__fpRecOff) return;
    try { if (window.__fpRec) window.__fpRec(JSON.stringify(o)); } catch (e) {}
  };
  const nthIndex = (el) => {
    let n = 1;
    for (let sib = el; sib; sib = sib.previousElementSibling) {
      if (sib.tagName === el.tagName) n++;
    }
    return n;
  };
  const sel = (el) => {
    if (!el || el === document || el === document.documentElement) return "body";
    if (el.id) return "#" + CSS.escape(el.id);
    const tid = el.getAttribute("data-testid");
    if (tid) return '[data-testid="' + tid + '"]';
    const aria = el.getAttribute("aria-label");
    if (aria) return '[aria-label="' + aria.replace(/"/g, '\\"') + '"]';
    const nm = el.getAttribute("name");
    if (nm) return el.tagName.toLowerCase() + '[name="' + nm + '"]';
    const parts = [];
    let cur = el, depth = 0;
    while (cur && cur !== document.body && depth < 5) {
      parts.unshift(cur.tagName.toLowerCase() + ":nth-of-type(" + nthIndex(cur) + ")");
      cur = cur.parentElement;
      depth++;
    }
    return (cur === document.body ? "body > " : "") + parts.join(" > ");
  };
  const label = (el) => {
    if (!el) return "";
    const attr = (...ns) => {
      for (const n of ns) { const v = el.getAttribute(n); if (v) return v.slice(0, 60); }
      return "";
    };
    // aria-label > title first; for form fields the NAME attribute before
    // placeholder — sites like Baidu dynamically stuff placeholders with
    // trending-query ads, which would leak into recorded step labels
    // (E2E 2026-08-29: every input was labeled with an unrelated hot search).
    const tag = el.tagName.toLowerCase();
    if (tag === "input" || tag === "select" || tag === "textarea") {
      return attr("aria-label", "title", "name", "placeholder") || (el.textContent || "").trim().slice(0, 60);
    }
    return attr("aria-label", "title") || (el.textContent || "").trim().slice(0, 60) || attr("name");
  };
  const roleOf = (el) => {
    if (!el) return "";
    const r = el.getAttribute("role");
    if (r) return r;
    const tag = el.tagName.toLowerCase();
    if (tag === "a") return "link";
    return tag;
  };
  const pending = {};
  // Scroll capture (debounced like input): replay needs the page scrolled
  // far enough for targets to be in-view. Value = "<direction> <screens>".
  const scrollPos = {};
  const scrollAcc = {};
  document.addEventListener("scroll", (e) => {
    var onDoc = (e.target === document || e.target === document.documentElement);
    var node = onDoc ? document.body : e.target;
    var selector = onDoc ? "body" : sel(node);
    var top = onDoc ? (window.scrollY || document.documentElement.scrollTop || 0) : e.target.scrollTop;
    var prev = scrollPos[selector];
    scrollPos[selector] = top;
    if (prev === undefined || prev === top) return;
    scrollAcc[selector] = (scrollAcc[selector] || 0) + (top - prev);
    clearTimeout(pending["scroll:" + selector]);
    pending["scroll:" + selector] = setTimeout(() => {
      var total = scrollAcc[selector] || 0;
      scrollAcc[selector] = 0;
      if (Math.abs(total) < 200) return; // ignore sub-pixel jitter
      var dir = total > 0 ? "down" : "up";
      var amount = Math.max(1, Math.round(Math.abs(total) / 600));
      send({ type: "scroll", selector: selector, value: dir + " " + amount });
    }, 800);
  }, true);
  // Hover capture (menu-open pattern): mouseenter on clickable-ish elements,
  // debounced; the deterministic filter keeps only hovers whose NEXT action
  // lands inside the hovered subtree (menu opened → item clicked).
  let hoverSel = "", hoverTimer = 0;
  document.addEventListener("mouseover", (e) => {
    const el = (e.target.closest && e.target.closest("a,button,[role],li,.menu-item,[onclick],[aria-haspopup]")) || null;
    const nowSel = el ? sel(el) : "";
    if (!nowSel || nowSel === hoverSel) return;
    hoverSel = nowSel;
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(() => send({ type: "hover", selector: hoverSel }), 700);
  }, true);
  document.addEventListener("click", (e) => {
    const el = (e.target.closest && e.target.closest("a,button,input,select,textarea,[role],[onclick],label")) || e.target;
    const selector = sel(el);
    send({ type: "click", selector, role: roleOf(el), name: label(el), password: el.type === "password" });
    try {
      let effective = false;
      const mo = new MutationObserver(() => { effective = true; });
      mo.observe(document.documentElement, { childList: true, subtree: true, attributes: true });
      setTimeout(() => { mo.disconnect(); send({ type: "effect", selector, value: effective ? "true" : "false" }); }, 600);
    } catch (err) { /* observer unavailable — effectiveness stays unknown */ }
  }, true);
  document.addEventListener("input", (e) => {
    const el = e.target;
    if (!el || !el.tagName) return;
    const selector = sel(el);
    clearTimeout(pending[selector]);
    pending[selector] = setTimeout(() => {
      send({
        type: "input", selector, role: roleOf(el), name: label(el),
        value: el.type === "password" ? "" : String(el.value || "").slice(0, 200),
        password: el.type === "password",
      });
    }, 800);
  }, true);
  document.addEventListener("change", (e) => {
    const el = e.target;
    if (!el || el.tagName !== "SELECT") return;
    send({ type: "change", selector: sel(el), role: roleOf(el), name: label(el), value: String(el.value || "").slice(0, 200) });
  }, true);
  document.addEventListener("keydown", (e) => {
    if (e.key !== "Enter") return;
    const el = e.target;
    send({ type: "submit", selector: sel(el), role: roleOf(el), name: label(el) });
  }, true);
})()`

// --- deterministic noise filter (pure, unit-tested) -----------------------------------

// FilterRecordEvents applies the deterministic pass of the recording filter
// (pass one — input debouncing — already ran in the page script; pass three
// — AI semantic cleanup — runs in the desktop layer). It returns the kept
// events plus everything dropped, so the panel can show the
// 「已过滤 N 条」disclosure.
// filterHoverEvents keeps a hover only when the NEXT recorded action clicks
// or types inside the hovered element's subtree — the menu-open pattern. All
// other mouseovers are noise (pointer wandering across the page).
func filterHoverEvents(events []ConsoleRecordEvent) []ConsoleRecordEvent {
	out := make([]ConsoleRecordEvent, 0, len(events))
	for i, ev := range events {
		if ev.Type != "hover" {
			out = append(out, ev)
			continue
		}
		if i+1 < len(events) {
			next := events[i+1]
			if next.Selector != "" && isDescendantSelector(next.Selector, ev.Selector) {
				out = append(out, ev) // menu opened, item inside it acted on
			}
		}
	}
	return out
}

// isDescendantSelector reports whether child plausibly lives inside parent's
// subtree: exact match, prefix path match, or child carries parent as an
// ancestor segment ("nav > li" ⊂ "nav").
func isDescendantSelector(child, parent string) bool {
	if child == parent || strings.HasPrefix(child, parent+" ") || strings.HasPrefix(child, parent+">") {
		return true
	}
	return strings.HasPrefix(child, parent)
}

func FilterRecordEvents(events []ConsoleRecordEvent) (kept, dropped []ConsoleRecordEvent) {
	merged := mergeRecordEffects(events)
	merged = filterHoverEvents(merged)
	kept = []ConsoleRecordEvent{}
	dropped = []ConsoleRecordEvent{}

	for i := 0; i < len(merged); i++ {
		ev := merged[i]

		// Malformed capture — nothing to anchor a step on.
		if ev.Selector == "" && ev.URL == "" {
			dropped = append(dropped, ev)
			continue
		}

		// Consecutive clicks on the same target within 1s: keep the first.
		if ev.Type == "click" {
			j := i + 1
			for j < len(merged) && merged[j].Type == "click" && merged[j].Selector == ev.Selector && merged[j].Time-ev.Time < 1000 {
				j++
			}
			if j > i+1 {
				dropped = append(dropped, merged[i+1:j]...)
				merged = append(merged[:i+1], merged[j:]...)
			}
		}

		// Ineffective clicks (no DOM change, no navigation within 1.5s):
		// wandering clicks that never did anything.
		if ev.Type == "click" && ev.Effective != nil && !*ev.Effective && !navigationFollows(merged, i, 1500) {
			dropped = append(dropped, ev)
			continue
		}

		// Later input on the same field supersedes the earlier one (the page
		// debounce already merged keystrokes; this catches field re-edits):
		// an input immediately followed by another input on the same field is
		// superseded — only the last one per run survives.
		if ev.Type == "input" && i+1 < len(merged) && merged[i+1].Type == "input" && merged[i+1].Selector == ev.Selector {
			dropped = append(dropped, ev)
			continue
		}

		// Round-trip navigation A→B→A with nothing in between: drop both B
		// legs (mis-typed address / wrong link, immediately backed out).
		if ev.Type == "navigate" {
			if j, ok := roundTripReturn(merged, i); ok {
				dropped = append(dropped, merged[i+1:j+1]...)
				merged = append(merged[:i+1], merged[j+1:]...)
				kept = append(kept, ev)
				continue
			}
			// Consecutive duplicate navigations to the same URL: keep one.
			if i+1 < len(merged) && merged[i+1].Type == "navigate" && merged[i+1].URL == ev.URL {
				dropped = append(dropped, merged[i+1])
				merged = append(merged[:i+1], merged[i+2:]...)
			}
		}

		kept = append(kept, ev)
	}
	return kept, dropped
}

// mergeRecordEffects folds "effect" events into the nearest preceding click
// with the same selector (the page script reports DOM impact ~600ms after
// the click) and drops the spent effect events.
func mergeRecordEffects(events []ConsoleRecordEvent) []ConsoleRecordEvent {
	out := make([]ConsoleRecordEvent, 0, len(events))
	for _, ev := range events {
		if ev.Type != "effect" {
			out = append(out, ev)
			continue
		}
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Type != "click" {
				continue
			}
			if out[i].Selector == ev.Selector {
				effective := ev.Value == "true"
				out[i].Effective = &effective
			}
			break // only the nearest click is a candidate
		}
	}
	return out
}

// navigationFollows reports whether any navigate event happens within ms
// after index i (an "ineffective" click that navigated is NOT wandering —
// the MutationObserver just missed the unload).
func navigationFollows(events []ConsoleRecordEvent, i int, ms int64) bool {
	for j := i + 1; j < len(events) && events[j].Time-events[i].Time <= ms; j++ {
		if events[j].Type == "navigate" {
			return true
		}
	}
	return false
}

// roundTripReturn checks events[i] ("navigate A") for the A→B→A pattern with
// no user action between B and the return, returning the return's index.
func roundTripReturn(events []ConsoleRecordEvent, i int) (int, bool) {
	if i+2 >= len(events) {
		return 0, false
	}
	if events[i+1].Type != "navigate" || events[i+2].Type != "navigate" {
		return 0, false
	}
	if events[i+2].URL != events[i].URL || events[i+1].URL == events[i].URL {
		return 0, false
	}
	return i + 2, true
}
