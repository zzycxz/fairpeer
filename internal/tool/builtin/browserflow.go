package builtin

// browserflow.go — the deterministic step-table executor behind
// `executor: browser-flow` skills.
//
// A per-site browser skill (访问 A 站做 XX 拿什么 / 访问 B 站做 YY 拿什么) is
// exactly the shape the 步骤 table already authors — the missing half was
// execution: routed through an LLM subagent the table is *advice* the model
// re-improvises on every run, and long flows drift. The flow runner parses
// the table and executes it verbatim through the same browser primitives the
// agent tools use — one LLM decision (invoking the skill), zero during the
// run.
//
// Breakpoint semantics in this mode (no interactive panel):
//   - human: rides its auto-detect condition (url:/visible:/…) — the browser
//     window is visible, the user does the manual part, the condition
//     releases the step. No condition → clear error at planning time.
//   - ask: values come from run_skill's arguments ("参数=值 参数2=值2");
//     missing bindings error before any browser work happens.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// FlowStep is one executable row of the 步骤 table (same grammar the ops
// editor serializes; JSON tags mirror the console step wire shape).
type FlowStep struct {
	Type       string   `json:"type"`
	Target     string   `json:"target,omitempty"`
	URL        string   `json:"url,omitempty"`
	Text       string   `json:"text,omitempty"`
	Value      string   `json:"value,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	Amount     int      `json:"amount,omitempty"`
	Condition  string   `json:"condition,omitempty"`
	TimeoutSec int      `json:"timeout_sec,omitempty"`
	Files      []string `json:"files,omitempty"`
	Expression string   `json:"expression,omitempty"`
}

var flowOps = map[string]bool{
	"navigate": true, "back": true, "forward": true, "switch_tab": true, "click": true, "type": true,
	"key": true, "hover": true, "scroll": true, "select": true, "upload": true, "wait": true,
	"extract": true, "screenshot": true, "evaluate": true, "human": true, "ask": true,
}

// ParseFlowTable extracts the 步骤 table from a skill body. Tolerant of the
// editor's exact dialect: `| # | 操作 | 目标 | 值 |` header, --- separator,
// backticked targets, trailing empty value cells.
func ParseFlowTable(body string) ([]FlowStep, error) {
	section := findFlowSection(body)
	if section == "" {
		return nil, fmt.Errorf("未找到「## 步骤」段落——browser-flow 技能的正文必须包含步骤表")
	}
	var steps []FlowStep
	for _, raw := range strings.Split(section, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(strings.Trim(strings.TrimSpace(cells[i]), "`"))
		}
		if len(cells) < 2 {
			continue
		}
		op := strings.ToLower(cells[1])
		if op == "" || op == "操作" || strings.TrimLeft(op, "-") == "" || op == "#" {
			continue // header / separator
		}
		if !flowOps[op] {
			return nil, fmt.Errorf("步骤表第 %d 行：未知操作 %q", len(steps)+1, op)
		}
		target, value := "", ""
		if len(cells) > 2 {
			target = cells[2]
		}
		if len(cells) > 3 {
			value = cells[3]
		}
		steps = append(steps, flowStepFromRow(op, target, value))
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("「## 步骤」段落没有可执行的表格行")
	}
	return steps, nil
}

// findFlowSection returns the body of the 步骤 section (up to the next ## or EOF).
func findFlowSection(body string) string {
	re := regexp.MustCompile(`(?im)^##\s*(步骤|steps|procedure)\s*$`)
	loc := re.FindStringIndex(body)
	if loc == nil {
		return ""
	}
	rest := body[loc[1]:]
	if next := regexp.MustCompile(`(?m)^##\s`).FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return rest
}

func flowStepFromRow(op, target, value string) FlowStep {
	st := FlowStep{Type: op}
	switch op {
	case "switch_tab":
		// 目标列 = 1 起始页卡序号；值列 = 页卡标题（备注，执行不依赖）。
		st.Target = target
		if st.Target == "" {
			st.Target = "1"
		}
		st.Text = value
	case "navigate":
		st.URL = target
	case "click", "extract":
		st.Target = target
		if strings.EqualFold(value, "table") {
			st.Value = "table"
		} else if strings.EqualFold(value, "markdown") {
			st.Value = "markdown"
		}
	case "hover":
		st.Target = target
	case "type":
		st.Target, st.Text = target, value
	case "select":
		st.Target, st.Value = target, value
	case "key":
		st.Target = target
		if value == "" {
			value = "enter"
		}
		st.Value = value
	case "scroll":
		st.Direction = target
		if st.Direction == "" {
			st.Direction = "down"
		}
		st.Amount = parseIntOr(value, 3)
	case "wait":
		st.Condition = target
		if st.Condition == "" {
			st.Condition = "networkidle"
		}
		st.TimeoutSec = waitSecondsOr(value, 90)
	case "upload":
		st.Target = target
		for _, f := range strings.Split(value, ",") {
			if f = strings.TrimSpace(f); f != "" {
				st.Files = append(st.Files, f)
			}
		}
	case "evaluate":
		st.Expression = target
	case "human":
		st.Condition, st.Text = target, value
		if st.TimeoutSec == 0 {
			st.TimeoutSec = 600
		}
	case "ask":
		st.Target, st.Text = target, value
		st.TimeoutSec = 600
	}
	return st
}

// waitSecondsOr parses a wait-table cell as seconds. Values above 600 are
// read as milliseconds — nobody types 10000s waits by hand, but "15000" (ms
// habit from the panel's other fields) shows up in hand-edited skills.
func waitSecondsOr(v string, def int) int {
	n := parseIntOr(v, def)
	if n > 600 {
		if ms := n / 1000; ms >= 1 {
			return ms
		}
	}
	return n
}

func parseIntOr(v string, def int) int {
	m := regexp.MustCompile(`(\d+)`).FindString(strings.TrimSpace(v))
	if m == "" {
		return def
	}
	n, err := strconv.Atoi(m)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// ParseFlowParams turns run_skill arguments into the runtime parameter map:
// "工单号=A123 日期=2026-09-01" → {"工单号":"A123","日期":"2026-09-01"}.
// Quoted values ("k=a b") keep their spaces. A string with no '=' at all is
// stored under "问题" (the stream-query convention's ask bind).
func ParseFlowParams(arguments string) map[string]string {
	params := map[string]string{}
	args := strings.TrimSpace(arguments)
	if args == "" {
		return params
	}
	if !strings.Contains(args, "=") {
		params["问题"] = args
		return params
	}
	re := regexp.MustCompile(`(\S+)=("([^"]*)"|\S*)`)
	for _, m := range re.FindAllStringSubmatch(args, -1) {
		key, val := m[1], m[2]
		val = strings.Trim(val, `"`)
		if key != "" && val != "" {
			params[key] = val
		}
	}
	return params
}

var flowParamRef = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// --- multi-anchor targets (多锚定位) --------------------------------------------
//
// A step's target may carry a fallback chain separated by ";;" (NOT "|" — a
// pipe would split the markdown table cell):
//
//	#login-btn;;text=登录
//
// Anchors run in order, first hit wins: CSS selectors go through the regular
// browser tools; `text=<可见文本>` locates the element by its visible label
// in the DOM (exact match first, then contains) and acts on it directly —
// attribute-independent, survives redesigns that keep the label.

// flowAnchor is one parsed anchor of a target chain.
type flowAnchor struct {
	kind string // "css" | "text"
	val  string
}

// splitFlowAnchors parses "a;;b;;c" into ordered anchors. Empty segments drop
// out; a chain with no ";;" is a single anchor (plain CSS for back-compat).
func splitFlowAnchors(target string) []flowAnchor {
	var out []flowAnchor
	for _, seg := range strings.Split(target, ";;") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if t, ok := strings.CutPrefix(seg, "text="); ok && strings.TrimSpace(t) != "" {
			out = append(out, flowAnchor{kind: "text", val: strings.TrimSpace(t)})
			continue
		}
		out = append(out, flowAnchor{kind: "css", val: seg})
	}
	return out
}

// locateFlowTextJS is the shared finder for text anchors: locate an element
// by visible label (exact then contains) among clickable/labelable elements,
// then run the /*ACTION*/ payload with the resolved element in `click`.
// Evaluation only composes here — the action decides how to act.
//
// Label sources are matched as a SET (aria-label/alt/placeholder/title/
// innerText/value): the picker's nameOf picks ONE by priority, so any fixed
// priority here would disagree with it on elements carrying two differing
// sources and the produced text= anchor would miss. The pool sweeps shadow
// roots — the scanners that emit text= anchors already look inside them.
const locateFlowTextJS = `(function(){
  var want = TEXT;
  var els = [];
  var poolSel = 'a,button,input,select,textarea,label,[role=button],[role=link],[role=tab],[onclick],[contenteditable=true]';
  function collect(root) {
    Array.prototype.slice.call(root.querySelectorAll(poolSel)).forEach(function(n){ els.push(n); });
    // Bare pointer divs/spans (component-library clickables) join the pool.
    root.querySelectorAll('div,span').forEach(function(n){
      if (getComputedStyle(n).cursor === 'pointer') els.push(n);
    });
    root.querySelectorAll('*').forEach(function(n){
      if (n.shadowRoot) collect(n.shadowRoot);
    });
  }
  collect(document);
  var pick = null;
  function labels(el) {
    var out = [];
    var push = function(v){
      v = ((v == null ? '' : v) + '').replace(/\s+/g, ' ').trim().slice(0, 80);
      if (v && out.indexOf(v) === -1) out.push(v);
    };
    push(el.getAttribute('aria-label'));
    push(el.getAttribute('alt'));
    push(el.getAttribute('placeholder'));
    push(el.getAttribute('title'));
    push(el.innerText || el.value || '');
    return out;
  }
  function matches(el, test) {
    var ls = labels(el);
    for (var i = 0; i < ls.length; i++) { if (test(ls[i])) return true; }
    return false;
  }
  for (var i = 0; i < els.length; i++) {
    if (matches(els[i], function(l){ return l === want; })) { pick = els[i]; break; }
  }
  if (!pick) {
    for (var j = 0; j < els.length; j++) {
      if (matches(els[j], function(l){ return l.length < 40 && l.indexOf(want) !== -1; })) { pick = els[j]; break; }
    }
  }
  if (!pick) return JSON.stringify({found: false});
  // Act on the located element itself — click events bubble, so a delegated
  // ancestor listener still fires. Climb ONLY when pick is not itself
  // actable (a bare <label>/inner node) and take the NEAREST actable
  // ancestor; the old unconditional 3-hop climb landed on toolbar-level
  // containers and clicked dead space between buttons (probe 2026-09-04:
  // text=查询 resolved to the body center, 600px away from the button).
  var actable = function(n) {
    return n.tagName === 'A' || n.tagName === 'BUTTON' || n.tagName === 'INPUT'
      || n.tagName === 'SELECT' || n.tagName === 'TEXTAREA'
      || n.getAttribute('role') === 'button' || n.getAttribute('role') === 'link'
      || n.getAttribute('role') === 'tab' || n.hasAttribute('onclick')
      || n.isContentEditable
      || getComputedStyle(n).cursor === 'pointer';
  };
  var click = pick;
  if (!actable(pick)) {
    var cur = pick;
    for (var k = 0; k < 5 && cur.parentElement; k++) {
      var p = cur.parentElement;
      if (actable(p)) { click = p; break; }
      cur = p;
    }
  }
  /*ACTION*/
})()`

// locateTextAction reports the viewport click point (for hover, which needs
// real pointer coordinates to trigger CSS :hover).
const locateTextAction = `var r = click.getBoundingClientRect();
  var shown = labels(pick);
  return JSON.stringify({found: true, x: r.left + r.width / 2, y: r.top + r.height / 2,
    tag: click.tagName.toLowerCase(), label: shown.length ? shown[0] : want});`

// hoverTextAction hovers in the SAME evaluation that located the element —
// JS-dispatched pointer/mouse events. The flow's hover used a real CDP
// MouseEvent, but the Input domain never reaches the page on console browser
// setups where input dispatch is broken (same class of issue as
// browserClick's selector path), making hover steps dead weight there.
// JS menus (mouseenter/mouseover listeners — ExtJS/ jQuery components)
// activate; CSS :hover STYLES cannot trigger without a real pointer, which
// the step output states.
const hoverTextAction = `var r = click.getBoundingClientRect();
  var cx = r.left + r.width / 2, cy = r.top + r.height / 2;
  var o = {bubbles: true, cancelable: true, view: window, clientX: cx, clientY: cy};
  ['pointerover', 'mouseover', 'pointermove', 'mousemove'].forEach(function(t) {
    click.dispatchEvent(new MouseEvent(t, o));
  });
  click.dispatchEvent(new MouseEvent('mouseenter', {bubbles: false, view: window, clientX: cx, clientY: cy}));
  var shown = labels(pick);
  return JSON.stringify({found: true, tag: click.tagName.toLowerCase(), label: shown.length ? shown[0] : want});`

// clickTextAction clicks in the SAME evaluation that located the element —
// el.click(), the contract browser_click's ref path uses. Raw CDP mouse
// events (MouseClickXY) report transport success while the page never sees
// the click in the persistent/takeover console browser (probe 2026-09-04:
// dispatch succeeded, __clicked stayed empty, JS .click() landed) — the same
// reason browser_type migrated off chromedp.SendKeys.
const clickTextAction = `click.scrollIntoView({block: "center", behavior: "instant"});
  click.click();
  var shown = labels(pick);
  return JSON.stringify({found: true, clicked: true, tag: click.tagName.toLowerCase(),
    label: shown.length ? shown[0] : want});`

// flowLocateByText resolves a text anchor to a viewport click point.
func flowLocateByText(ctx context.Context, s *browserSession, text string) (x, y float64, err error) {
	expr := strings.Replace(locateFlowTextJS, "/*ACTION*/", locateTextAction, 1)
	expr = strings.Replace(expr, "TEXT", fmt.Sprintf("%q", text), 1)
	actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	var out string
	if rerr := chromedp.Run(actx, chromedp.Evaluate(expr, &out)); rerr != nil {
		return 0, 0, fmt.Errorf("文本定位 %q: %w", text, rerr)
	}
	var res struct {
		Found bool    `json:"found"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		Tag   string  `json:"tag"`
		Label string  `json:"label"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || !res.Found {
		return 0, 0, fmt.Errorf("页面上找不到可见文本为 %q 的可点击元素", text)
	}
	return res.X, res.Y, nil
}

// flowClickByText locates a text anchor and clicks it in the same evaluation
// (see clickTextAction for why not raw CDP mouse events).
func flowClickByText(ctx context.Context, s *browserSession, text string) (string, error) {
	expr := strings.Replace(locateFlowTextJS, "/*ACTION*/", clickTextAction, 1)
	expr = strings.Replace(expr, "TEXT", fmt.Sprintf("%q", text), 1)
	actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	var out string
	if rerr := chromedp.Run(actx, chromedp.Evaluate(expr, &out)); rerr != nil {
		return "", fmt.Errorf("文本点击 %q: %w", text, rerr)
	}
	var res struct {
		Found bool   `json:"found"`
		Tag   string `json:"tag"`
		Label string `json:"label"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || !res.Found {
		// Same phrasing as the locator — isLocateMiss keys on it so the
		// anchor chain's wait-and-retry still applies.
		return "", fmt.Errorf("页面上找不到可见文本为 %q 的可点击元素", text)
	}
	return fmt.Sprintf("已点击「%s」(文本锚, %s)", text, res.Tag), nil
}

// flowTypeByText locates a field by visible label/placeholder and types into
// it — focus + native value set + input event, the same contract the
// browser_type tool's selector path uses (React-controlled inputs stay in
// sync).
const flowTypeByTextJS = `(function(){
  var want = TEXT;
  // Fields + wrapping labels, swept through shadow roots so text= anchors
  // produced for Web Component targets resolve here too. Labels match as a
  // SET of sources (aria-label/alt/placeholder/title/innerText/value) —
  // mirrors locateFlowTextJS; a fixed priority would disagree with the
  // picker's nameOf on fields carrying two differing sources.
  var els = [], labelEls = [];
  function collect(root) {
    Array.prototype.slice.call(root.querySelectorAll('input,textarea,[contenteditable=true],label')).forEach(function(n){
      if (n.tagName === 'LABEL') labelEls.push(n); else els.push(n);
    });
    root.querySelectorAll('*').forEach(function(n){
      if (n.shadowRoot) collect(n.shadowRoot);
    });
  }
  collect(document);
  function labels(el) {
    var out = [];
    var push = function(v){
      v = ((v == null ? '' : v) + '').replace(/\s+/g, ' ').trim().slice(0, 80);
      if (v && out.indexOf(v) === -1) out.push(v);
    };
    push(el.getAttribute('aria-label'));
    push(el.getAttribute('alt'));
    push(el.getAttribute('placeholder'));
    push(el.getAttribute('title'));
    push(el.innerText || el.value || '');
    return out;
  }
  function matches(el, test) {
    var ls = labels(el);
    for (var i = 0; i < ls.length; i++) { if (test(ls[i])) return true; }
    return false;
  }
  var pick = null;
  for (var i = 0; i < els.length; i++) {
    if (matches(els[i], function(l){ return l === want; })) { pick = els[i]; break; }
  }
  if (!pick) {
    for (var j = 0; j < els.length; j++) {
      if (matches(els[j], function(l){ return l.length < 40 && l.indexOf(want) !== -1; })) { pick = els[j]; break; }
    }
  }
  // Inputs inside a <label>文字</label> qualify via the label's own text.
  if (!pick) {
    for (var k = 0; k < labelEls.length; k++) {
      if (((labelEls[k].innerText || '') + '').trim() === want) {
        var f = labelEls[k].querySelector('input,textarea');
        if (f) { pick = f; break; }
      }
    }
  }
  if (!pick) return JSON.stringify({ok: false});
  var el = pick;
  el.focus();
  if (el.isContentEditable) {
    el.textContent = '';
    el.textContent = VALUE;
    el.dispatchEvent(new InputEvent('input', {bubbles: true, data: VALUE, inputType: 'insertText'}));
    return JSON.stringify({ok: true, value: el.textContent});
  }
  // Readonly fields (ExtJS pickers, temporarily disabled inputs): the same
  // contract as browser_type's selector path — the native prototype setter
  // throws "Illegal invocation" on framework-mangled readonly chains, so
  // write .value directly and fire change (readonly fields are picker-
  // controlled, no React reactivity needed).
  if (el.hasAttribute && el.hasAttribute('readonly')) {
    el.value = VALUE;
    el.dispatchEvent(new Event('change', {bubbles: true}));
    return JSON.stringify({ok: true, value: el.value});
  }
  // Native prototype setter (framework-controlled fields ignore a plain
  // .value=): pick THIS element's interface — calling HTMLInputElement's
  // setter on a <textarea> throws "Illegal invocation" (native setters
  // verify the receiver's type).
  var valueProto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement.prototype : window.HTMLInputElement.prototype;
  var setter = Object.getOwnPropertyDescriptor(valueProto, 'value');
  el.value = '';
  if (setter && setter.set) { setter.set.call(el, VALUE); } else { el.setAttribute('value', VALUE); }
  el.dispatchEvent(new Event('input', {bubbles: true}));
  el.dispatchEvent(new Event('change', {bubbles: true}));
  return JSON.stringify({ok: true, value: el.value});
})()`

func flowTypeByText(ctx context.Context, s *browserSession, text, value string) (string, error) {
	expr := strings.Replace(flowTypeByTextJS, "TEXT", fmt.Sprintf("%q", text), 1)
	expr = strings.Replace(expr, "VALUE", fmt.Sprintf("%q", value), -1)
	actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	var out string
	if rerr := chromedp.Run(actx, chromedp.Evaluate(expr, &out)); rerr != nil {
		return "", fmt.Errorf("文本定位输入 %q: %w", text, rerr)
	}
	var res struct {
		OK    bool   `json:"ok"`
		Value string `json:"value"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || !res.OK {
		return "", fmt.Errorf("页面上找不到可见文本为 %q 的输入框", text)
	}
	return "已输入（文本锚 " + text + "）：" + res.Value, nil
}

// flowHoverPoint resolves an anchor to hover coordinates (css via the tool's
// resolver, text via the visible-label locator).
func flowHoverPoint(ctx context.Context, s *browserSession, a flowAnchor) (float64, float64, error) {
	if a.kind == "text" {
		return flowLocateByText(ctx, s, a.val)
	}
	raw, _ := json.Marshal(a.val)
	return hoverPointForTarget(ctx, s, raw)
}

// flowHoverByText hovers a text anchor via JS-dispatched pointer/mouse events
// (see hoverTextAction for why not a real CDP MouseEvent).
func flowHoverByText(ctx context.Context, s *browserSession, text string) (string, error) {
	expr := strings.Replace(locateFlowTextJS, "/*ACTION*/", hoverTextAction, 1)
	expr = strings.Replace(expr, "TEXT", fmt.Sprintf("%q", text), 1)
	actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	var out string
	if rerr := chromedp.Run(actx, chromedp.Evaluate(expr, &out)); rerr != nil {
		return "", fmt.Errorf("文本悬停 %q: %w", text, rerr)
	}
	var res struct {
		Found bool   `json:"found"`
		Tag   string `json:"tag"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || !res.Found {
		return "", fmt.Errorf("页面上找不到可见文本为 %q 的可悬停元素", text)
	}
	return fmt.Sprintf("已悬停「%s」(%s, JS 事件派发——悬停菜单生效；CSS :hover 样式需真实指针，不适用)", text, res.Tag), nil
}

// flowHoverByCSS hovers a CSS anchor via the same JS event dispatch.
func flowHoverByCSS(ctx context.Context, s *browserSession, sel string) (string, error) {
	expr := `(function(sel){
		var el = document.querySelector(sel);
		if (!el || el.nodeType !== 1) { return JSON.stringify({error: 'not-found'}); }
		el.scrollIntoView({block: 'center'});
		var r = el.getBoundingClientRect();
		var cx = r.left + r.width / 2, cy = r.top + r.height / 2;
		var o = {bubbles: true, cancelable: true, view: window, clientX: cx, clientY: cy};
		['pointerover', 'mouseover', 'pointermove', 'mousemove'].forEach(function(t) {
			el.dispatchEvent(new MouseEvent(t, o));
		});
		el.dispatchEvent(new MouseEvent('mouseenter', {bubbles: false, view: window, clientX: cx, clientY: cy}));
		return JSON.stringify({found: true, tag: el.tagName.toLowerCase()});
	})(` + fmt.Sprintf("%q", sel) + `)`
	actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	var out string
	if rerr := chromedp.Run(actx, chromedp.Evaluate(expr, &out)); rerr != nil {
		return "", fmt.Errorf("悬停 %q: %w", sel, rerr)
	}
	var res struct {
		Found bool   `json:"found"`
		Error string `json:"error"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || res.Error == "not-found" {
		return "", fmt.Errorf("悬停 %q: element not found", sel)
	}
	return fmt.Sprintf("已悬停 %q（JS 事件派发——悬停菜单生效；CSS :hover 样式需真实指针，不适用）", sel), nil
}

// flowSelectByText locates a <select> by its visible label (label text,
// aria-label, name) and picks the option — completing the text-anchor chain
// for selects.
func flowSelectByText(ctx context.Context, s *browserSession, label, value string) (string, error) {
	expr := strings.Replace(`(function(){
  var want = WANT;
  var els = document.querySelectorAll('select');
  function lab(el) {
    var n = (el.getAttribute('aria-label') || el.getAttribute('name') || '').trim();
    if (!n && el.id) { var l = document.querySelector('label[for="' + el.id + '"]'); if (l) n = (l.innerText || '').trim(); }
    if (!n) { var p = el.closest('label'); if (p) n = (p.innerText || '').trim(); }
    return n;
  }
  var pick = null;
  for (var i = 0; i < els.length; i++) { if (lab(els[i]) === want) { pick = els[i]; break; } }
  if (!pick) { for (var j = 0; j < els.length; j++) { var l = lab(els[j]); if (l && l.length < 40 && l.indexOf(want) !== -1) { pick = els[j]; break; } } }
  if (!pick) return JSON.stringify({ok: false});
  pick.value = VALUE;
  pick.dispatchEvent(new Event('input', {bubbles: true}));
  pick.dispatchEvent(new Event('change', {bubbles: true}));
  return JSON.stringify({ok: true, value: pick.value});
})()`, "WANT", fmt.Sprintf("%q", label), 1)
	expr = strings.Replace(expr, "VALUE", fmt.Sprintf("%q", value), 1)
	actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	var out string
	if rerr := chromedp.Run(actx, chromedp.Evaluate(expr, &out)); rerr != nil {
		return "", fmt.Errorf("文本定位下拉 %q: %w", label, rerr)
	}
	var res struct {
		OK    bool   `json:"ok"`
		Value string `json:"value"`
	}
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || !res.OK {
		return "", fmt.Errorf("页面上找不到标签为 %q 的下拉框", label)
	}
	return "已选择（文本锚 " + label + "）：" + res.Value, nil
}

// flowExtractByText extracts the text content of the element labeled `text`.
func flowExtractByText(ctx context.Context, s *browserSession, text string) (string, error) {
	expr := `(function(){
    var want = WANT;
    var els = document.querySelectorAll('a,button,input,select,textarea,label,div,span,td,th,p,section,article,table');
    var pick = null;
    for (var i = 0; i < els.length; i++) {
      var t = ((els[i].innerText || els[i].value || '') + '').trim();
      if (t && t === want) { pick = els[i]; break; }
    }
    if (!pick) return null;
    return pick.innerText || pick.value || '';
  })()`
	expr = strings.Replace(expr, "WANT", fmt.Sprintf("%q", text), 1)
	actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
	defer cancel()
	var out *string
	if rerr := chromedp.Run(actx, chromedp.Evaluate(expr, &out)); rerr != nil {
		return "", fmt.Errorf("文本定位提取 %q: %w", text, rerr)
	}
	if out == nil {
		return "", fmt.Errorf("页面上找不到文本为 %q 的元素", text)
	}
	return *out, nil
}

// flowClickAnchor clicks one anchor (css via the tool, text via located
// coordinates). Shared by the flow runner's fallback loop.
// flowClickAnchor clicks one anchor (css via the tool, text via the locate-
// and-click evaluation).
func flowClickAnchor(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
	if a.kind == "text" {
		return flowClickByText(ctx, s, a.val)
	}
	return callBrowserTool(ctx, s, browserClick{}, map[string]any{"target": a.val})
}

// callBrowserTool marshals args + session id into one browser tool Execute.
func callBrowserTool(ctx context.Context, s *browserSession, executor interface {
	Execute(context.Context, json.RawMessage) (string, error)
}, args map[string]any) (string, error) {
	args["session_id"] = s.id
	raw, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	actx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	return executor.Execute(actx, raw)
}

// flowLocateWait is the shared budget each step's anchor chain may spend
// WAITING for its target to appear (SPA pages render asynchronously; "element
// exists" usually lags "page loaded" by a beat). One budget per target, not
// per anchor, bounds the worst case per step.
const flowLocateWait = 8 * time.Second

// isLocateMiss classifies an anchor failure as "the target isn't on the page
// (yet)". These errors are raised BEFORE any action dispatches, so polling is
// safe. Anything else (the action ran and errored, a config mistake, a dead
// ref) must NOT be retried — a re-run could double-fire a click.
func isLocateMiss(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, p := range []string{
		"页面上找不到",      // text-anchor finder misses
		"element not found", // chromedp selector misses
		"no node found",
		"no DOM object",
	} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// cssAnchorPresent is the cheap existence probe tryFlowAnchors runs before
// dispatching a CSS-anchor action: a missing selector must fail in
// milliseconds so the chain waits/retries inside its shared 8s budget and
// falls through to the next anchor — instead of burning the tool's whole
// action window (~40s) waiting for visibility first. A probe failure (invalid
// selector syntax, mid-navigation frame) is NOT a verdict: return true and
// let the action itself produce the real error.
func cssAnchorPresent(ctx context.Context, s *browserSession, sel string) bool {
	actx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var present bool
	if err := chromedp.Run(actx, chromedp.Evaluate(
		fmt.Sprintf("(function(){try{return document.querySelector(%q) !== null}catch(e){return true}})()", sel), &present)); err != nil {
		return true
	}
	return present
}

// tryFlowAnchors walks a target's anchor chain, returning the first success
// (and its output). Locate-class misses poll until the step's shared wait
// budget runs out before falling through to the next anchor — "wait for the
// target to appear, then act once" is the Playwright-style default the flow
// vocabulary implies.
func tryFlowAnchors(ctx context.Context, s *browserSession, target string,
	act func(ctx context.Context, s *browserSession, a flowAnchor) (string, error)) (string, error) {
	anchors := splitFlowAnchors(target)
	if len(anchors) == 0 {
		return "", fmt.Errorf("目标为空")
	}
	deadline := time.Now().Add(flowLocateWait)
	var lastErr error
	for _, a := range anchors {
		waited := time.Duration(0)
		for {
			var out string
			var err error
			if a.kind == "css" && !cssAnchorPresent(ctx, s, a.val) {
				// Existence probe missed — fail THIS attempt in milliseconds
				// (locate-miss phrasing) so the shared budget, not the tool's
				// action window, governs the wait-and-fallback.
				err = fmt.Errorf("锚点 %q: element not found", a.val)
			} else {
				out, err = act(ctx, s, a)
			}
			if err == nil {
				if waited > 0 {
					out = fmt.Sprintf("%s（目标 %.1fs 后才出现——该站渲染较慢，建议在步骤前加 wait visible:）", out, waited.Seconds())
				}
				return out, nil
			}
			lastErr = err
			if !isLocateMiss(err) || time.Now().After(deadline) {
				break
			}
			select {
			case <-time.After(300 * time.Millisecond):
				waited += 300 * time.Millisecond
			case <-ctx.Done():
				return "", fmt.Errorf("%s: 等待目标出现时中断: %w", target, ctx.Err())
			}
		}
	}
	kind := "锚点"
	if len(anchors) > 1 {
		kind = fmt.Sprintf("%d 个锚点", len(anchors))
	}
	return "", fmt.Errorf("%s（%s）全部失败，最后错误: %w", target, kind, lastErr)
}

// flowSubst substitutes {{参数}} refs in one step's fields. Unbound refs stay
// literal — validation upstream guarantees they're absent by run time. A
// param whose WHOLE value is a time-range phrase ("最近5分钟") resolves at
// THIS instant, not at argument-parse time — the window end is the moment the
// step (typically the time-range type right before 查询) executes, immune to
// session-open and step latency. resolved caches the first resolution so
// steps sharing a param see one consistent window per run.
func flowSubst(st *FlowStep, params map[string]string, resolved map[string]string) {
	sub := func(v string) string {
		return flowParamRef.ReplaceAllStringFunc(v, func(m string) string {
			name := strings.TrimSpace(flowParamRef.FindStringSubmatch(m)[1])
			if val, ok := params[name]; ok {
				if r, cached := resolved[name]; cached {
					return r
				}
				if r, rok := ResolveTimeRange(time.Now(), val); rok {
					resolved[name] = r
					return r
				}
				return val
			}
			return m
		})
	}
	st.Target, st.URL, st.Text = sub(st.Target), sub(st.URL), sub(st.Text)
	st.Value, st.Expression, st.Condition = sub(st.Value), sub(st.Expression), sub(st.Condition)
	for i, f := range st.Files {
		st.Files[i] = sub(f)
	}
}

// flowRefs lists every {{参数}} a flow needs at run time.
func flowRefs(steps []FlowStep) []string {
	seen := map[string]bool{}
	var out []string
	for _, st := range steps {
		for _, v := range []string{st.Target, st.URL, st.Text, st.Value, st.Expression, st.Condition} {
			for _, m := range flowParamRef.FindAllStringSubmatch(v, -1) {
				name := strings.TrimSpace(m[1])
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// RunBrowserFlow executes a browser-flow skill body deterministically: parse
// the 步骤 table, validate bindings, open one browser session, run every step
// through the agent's own browser primitives, and return a per-step report.
// wired into run_skill by boot (skill.SetFlowRunner).
func RunBrowserFlow(ctx context.Context, body, arguments string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	steps, err := ParseFlowTable(body)
	if err != nil {
		return "", err
	}
	params := ParseFlowParams(arguments)

	// Planning-time validation — fail BEFORE any browser window opens. An
	// ask step's binding must arrive via arguments (flow mode has no pause),
	// so only the parsed params count as provided.
	provided := map[string]bool{}
	for k := range params {
		provided[k] = true
	}
	var missing []string
	for _, ref := range flowRefs(steps) {
		if !provided[ref] {
			missing = append(missing, ref)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("流程缺少运行参数：%s——调用时用 arguments 传入（如 \"%s=值\"）",
			strings.Join(missing, "、"), missing[0])
	}
	for i, st := range steps {
		if st.Type == "human" && strings.TrimSpace(st.Condition) == "" {
			return "", fmt.Errorf("第 %d 步 human（%s）没有自动检测条件——确定性执行模式下人工步骤必须写完成条件（如 url:/home 或 visible:.login-done），否则改在运维面板里交互试运行", i+1, firstNonEmpty(st.Text, "人工操作"))
		}
	}

	s, err := newBrowserSession()
	if err != nil {
		return "", fmt.Errorf("打开浏览器会话: %w", err)
	}
	// A skill whose FIRST step is switch_tab targets an EXISTING tab — rebind
	// to the first real page instead of keeping the attach-created fresh
	// about:blank around (the per-run blank-tab litter bug; the skill's own
	// switch_tab then moves to the named tab and the rebind closes the
	// blank). navigate-first skills keep the fresh tab — an agent's navigate
	// must never hijack the page the user is looking at.
	if len(steps) > 0 && steps[0].Type == "switch_tab" {
		pickFirstConsoleTab(s)
	}

	var report []string
	fail := func(i int, st FlowStep, stepErr error) (string, error) {
		report = append(report, fmt.Sprintf("%d. %s — ✕ %v", i+1, st.Type, stepErr))
		summary := fmt.Sprintf("browser-flow 第 %d 步（%s）失败：%v\n已执行：\n%s\n会话 %s 保留（在运维面板浏览器页签可继续排查；修复技能步骤表后重跑）",
			i+1, st.Type, stepErr, strings.Join(report, "\n"), s.id)
		return summary, nil // step failure is a result, not a tool error
	}

	resolved := map[string]string{}
	for i := range steps {
		st := steps[i]
		flowSubst(&st, params, resolved)
		out, stepErr := flowExecStep(ctx, s, st)
		if stepErr != nil {
			return fail(i, st, stepErr)
		}
		// Payload steps (extract/evaluate) carry content the caller needs —
		// flowExecStep already bounds them at 6000; the generic 400-char
		// report trim must not cut an extracted table to its first rows
		// (the "answer arrived but the model only ever saw the header" trap).
		capAt := 400
		if st.Type == "extract" || st.Type == "evaluate" {
			capAt = 6000
		}
		if len(out) > capAt {
			out = out[:capAt] + "…"
		}
		report = append(report, fmt.Sprintf("%d. %s — %s", i+1, st.Type, firstNonEmpty(strings.TrimSpace(out), "ok")))
	}
	report = append(report, fmt.Sprintf("会话 %s 保持打开（10 分钟空闲后自动回收；长间隔复用请 browser_keepalive）", s.id))
	return "browser-flow 执行完成：\n" + strings.Join(report, "\n"), nil
}

// flowExecStep runs one step through the agent's browser tools (the same
// Execute paths run_skill's subagents would drive — minus the improvisation).
func flowExecStep(ctx context.Context, s *browserSession, st FlowStep) (string, error) {
	call := func(tool interface {
		Execute(context.Context, json.RawMessage) (string, error)
	}, args map[string]any) (string, error) {
		args["session_id"] = s.id
		raw, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		actx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		return tool.Execute(actx, raw)
	}
	switch st.Type {
	case "navigate":
		return call(browserNavigate{}, map[string]any{"url": st.URL})
	case "back":
		return call(browserBack{}, map[string]any{})
	case "forward":
		return call(browserForward{}, map[string]any{})
	case "switch_tab":
		// Target column: 1-based index OR a tab title (humans know tabs by
		// name; indexes drift between recording and replay). Numeric → index,
		// otherwise match by title (exact then contains, in the tool).
		if idx, err := strconv.Atoi(strings.TrimSpace(st.Target)); err == nil && idx >= 1 {
			return call(browserSwitchTab{}, map[string]any{"index": idx})
		}
		return call(browserSwitchTab{}, map[string]any{"target": strings.TrimSpace(st.Target)})
	case "click":
		// Multi-anchor chain: CSS first, then text= visible-label fallback.
		return tryFlowAnchors(ctx, s, st.Target, flowClickAnchor)
	case "type":
		return tryFlowAnchors(ctx, s, st.Target, func(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
			if a.kind == "text" {
				return flowTypeByText(ctx, s, a.val, st.Text)
			}
			return call(browserType{}, map[string]any{"target": a.val, "text": st.Text, "clear": true})
		})
	case "key":
		if err := dispatchSpecialKey(s, st.Value); err != nil {
			return "", err
		}
		return "pressed " + st.Value, nil
	case "hover":
		// JS-dispatched pointer/mouse events on the located element (see
		// hoverTextAction): the old real CDP MouseEvent rides the Input
		// domain, which never reaches the page on console setups where
		// input dispatch is broken. Hover MENUS activate; CSS :hover
		// styles need a real pointer and are stated as not applicable.
		return tryFlowAnchors(ctx, s, st.Target, func(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
			if a.kind == "text" {
				return flowHoverByText(ctx, s, a.val)
			}
			return flowHoverByCSS(ctx, s, a.val)
		})
	case "scroll":
		return call(browserScroll{}, map[string]any{"direction": st.Direction, "amount": st.Amount})
	case "select":
		return tryFlowAnchors(ctx, s, st.Target, func(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
			if a.kind == "text" {
				return flowSelectByText(ctx, s, a.val, st.Value)
			}
			// a.val, NOT st.Target: the css branch of a chain must try each
			// segment — st.Target here fed the WHOLE chain (a;;b) to
			// querySelector and SyntaxError'd even when segment a matched.
			args := map[string]any{"value": st.Value}
			if looksLikeRef(a.val) {
				args["ref"] = a.val
			} else {
				args["selector"] = a.val
			}
			return call(browserSelectOption{}, args)
		})
	case "upload":
		// Anchors like every other op: css segments try in order (a chain
		// target used to be fed to querySelector whole and SyntaxError'd).
		// A text= segment can't name a file input meaningfully — error so
		// the chain moves on instead of feeding "text=..." to querySelector.
		return tryFlowAnchors(ctx, s, st.Target, func(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
			if a.kind == "text" {
				return "", fmt.Errorf("upload 目标不支持文字锚点（file input 请用 CSS/ref 定位）")
			}
			args := map[string]any{"files": st.Files}
			if looksLikeRef(a.val) {
				args["ref"] = a.val
			} else {
				args["selector"] = a.val
			}
			return call(browserUploadFile{}, args)
		})
	case "wait":
		return call(browserWait{}, map[string]any{"condition": st.Condition, "timeout": st.TimeoutSec})
	case "extract":
		out, err := tryFlowAnchors(ctx, s, st.Target, func(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
			if a.kind == "text" {
				return flowExtractByText(ctx, s, a.val)
			}
			args := map[string]any{}
			if a.val != "" {
				args["selector"] = a.val
			}
			if st.Value == "table" || st.Value == "markdown" {
				args["format"] = st.Value
			}
			return call(browserExtract{}, args)
		})
		if err != nil {
			return "", err
		}
		// Extract carries the payload — keep more of it than the report cap.
		if len(out) > 6000 {
			out = out[:6000] + "…(截断)"
		}
		return out, nil
	case "screenshot":
		return call(browserScreenshot{}, map[string]any{})
	case "evaluate":
		return call(browserEvaluate{}, map[string]any{"expression": st.Expression})
	case "human":
		return flowWaitCondition(ctx, s, st)
	case "ask":
		// Planning-time validation guarantees the binding came in via
		// arguments — an ask step in flow mode is a satisfied parameter, not
		// a pause (pauses belong to the panel's interactive trial runner).
		return fmt.Sprintf("参数 %s 已由调用参数提供", firstNonEmpty(st.Target, "未命名")), nil
	default:
		return "", fmt.Errorf("未知步骤类型 %q", st.Type)
	}
}

// flowWaitCondition parks a human step until its detect condition passes —
// the user does the manual part in the visible browser window; this loop
// notices and continues. No condition → planning-time error upstream.
func flowWaitCondition(ctx context.Context, s *browserSession, st FlowStep) (string, error) {
	timeout := time.Duration(st.TimeoutSec) * time.Second
	if timeout <= 0 || timeout > 6*time.Hour {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		ok, _, err := detectOnce(s, st.Condition)
		if err == nil && ok {
			return fmt.Sprintf("人工步骤完成（条件 %s 已满足）", st.Condition), nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("等待人工操作超时（%s，条件 %s 未满足）", formatFlowTimeout(timeout), st.Condition)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

func formatFlowTimeout(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%.0f 小时", d.Hours())
	}
	return fmt.Sprintf("%.0f 分钟", d.Minutes())
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
