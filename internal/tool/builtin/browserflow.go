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

	"github.com/chromedp/cdproto/input"
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
	"navigate": true, "back": true, "forward": true, "click": true, "type": true,
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
	case "navigate":
		st.URL = target
	case "click", "extract":
		st.Target = target
		if strings.EqualFold(value, "table") {
			st.Value = "table"
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
		st.TimeoutSec = parseIntOr(value, 15)
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

// locateFlowTextJS finds an element by visible label (exact then contains)
// among clickable/labelable elements and returns a JSON descriptor: whether
// found, the click point (viewport coords), the tag, and a stable-enough CSS
// path for reporting. Evaluation only — the caller decides how to act.
const locateFlowTextJS = `(function(){
  var want = TEXT;
  var sel = 'a,button,input,select,textarea,label,[role=button],[role=link],[role=tab],[onclick],[contenteditable=true]';
  var els = document.querySelectorAll(sel);
  var pick = null;
  function label(el) {
    return ((el.innerText || el.value || '') + '').trim()
      || (el.getAttribute('placeholder') || '').trim()
      || (el.getAttribute('aria-label') || '').trim()
      || (el.getAttribute('title') || '').trim();
  }
  for (var i = 0; i < els.length; i++) {
    var l = label(els[i]);
    if (l && l === want) { pick = els[i]; break; }
  }
  if (!pick) {
    for (var j = 0; j < els.length; j++) {
      var l2 = label(els[j]);
      if (l2 && l2.length < 40 && l2.indexOf(want) !== -1) { pick = els[j]; break; }
    }
  }
  if (!pick) return JSON.stringify({found: false});
  // Climb to the clickable ancestor when the label sits on an inner node.
  var click = pick;
  for (var k = 0; k < 3 && click.parentElement; k++) {
    var p = click.parentElement;
    if (p.tagName === 'A' || p.tagName === 'BUTTON' || p.getAttribute('role') === 'button') { click = p; break; }
    click = p;
  }
  var r = click.getBoundingClientRect();
  return JSON.stringify({found: true, x: r.left + r.width / 2, y: r.top + r.height / 2,
    tag: click.tagName.toLowerCase(), label: label(pick)});
})()`

// flowLocateByText resolves a text anchor to a viewport click point.
func flowLocateByText(ctx context.Context, s *browserSession, text string) (x, y float64, err error) {
	expr := strings.Replace(locateFlowTextJS, "TEXT", fmt.Sprintf("%q", text), 1)
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

// flowTypeByText locates a field by visible label/placeholder and types into
// it — focus + native value set + input event, the same contract the
// browser_type tool's selector path uses (React-controlled inputs stay in
// sync).
const flowTypeByTextJS = `(function(){
  var want = TEXT;
  var els = document.querySelectorAll('input,textarea,[contenteditable=true]');
  function label(el) {
    return (el.getAttribute('placeholder') || '').trim()
      || (el.getAttribute('aria-label') || '').trim()
      || (el.getAttribute('title') || '').trim()
      || (((el.innerText || el.value || '') + '').trim());
  }
  var pick = null;
  for (var i = 0; i < els.length; i++) {
    if (label(els[i]) === want) { pick = els[i]; break; }
  }
  if (!pick) {
    for (var j = 0; j < els.length; j++) {
      var l = label(els[j]);
      if (l && l.length < 40 && l.indexOf(want) !== -1) { pick = els[j]; break; }
    }
  }
  // Inputs inside a <label>文字</label> qualify via the label's own text.
  if (!pick) {
    var labels = document.querySelectorAll('label');
    for (var k = 0; k < labels.length; k++) {
      if (((labels[k].innerText || '') + '').trim() === want) {
        var f = labels[k].querySelector('input,textarea');
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
  var setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
  if (!setter && el.tagName === 'TEXTAREA') { setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value'); }
  el.value = '';
  if (setter && setter.set) { setter.set.call(el, VALUE); } else { el.setAttribute('value', VALUE); }
  el.dispatchEvent(new Event('input', {bubbles: true}));
  el.dispatchEvent(new Event('change', {bubbles: true}));
  return JSON.stringify({ok: true, value: el.value});
})()`

func flowTypeByText(ctx context.Context, s *browserSession, text, value string) (string, error) {
	expr := strings.Replace(flowTypeByTextJS, "TEXT", fmt.Sprintf("%q", text), 1)
	expr = strings.Replace(expr, "VALUE", fmt.Sprintf("%q", value), 2)
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
func flowClickAnchor(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
	if a.kind == "text" {
		x, y, err := flowLocateByText(ctx, s, a.val)
		if err != nil {
			return "", err
		}
		actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
		defer cancel()
		if err := chromedp.Run(actx, chromedp.MouseClickXY(x, y)); err != nil {
			return "", fmt.Errorf("点击文本 %q @(%.0f,%.0f): %w", a.val, x, y, err)
		}
		return fmt.Sprintf("已点击「%s」(文本锚)", a.val), nil
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

// tryFlowAnchors walks a target's anchor chain, returning the first success
// (and its output). cssErr/textErr carry the last failure of each kind for
// the combined error message when everything misses.
func tryFlowAnchors(ctx context.Context, s *browserSession, target string,
	act func(ctx context.Context, s *browserSession, a flowAnchor) (string, error)) (string, error) {
	anchors := splitFlowAnchors(target)
	if len(anchors) == 0 {
		return "", fmt.Errorf("目标为空")
	}
	var lastErr error
	for _, a := range anchors {
		out, err := act(ctx, s, a)
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	kind := "锚点"
	if len(anchors) > 1 {
		kind = fmt.Sprintf("%d 个锚点", len(anchors))
	}
	return "", fmt.Errorf("%s（%s）全部失败，最后错误: %w", target, kind, lastErr)
}

// flowSubst substitutes {{参数}} refs in one step's fields. Unbound refs stay
// literal — validation upstream guarantees they're absent by run time.
func flowSubst(st *FlowStep, params map[string]string) {
	sub := func(v string) string {
		return flowParamRef.ReplaceAllStringFunc(v, func(m string) string {
			name := strings.TrimSpace(flowParamRef.FindStringSubmatch(m)[1])
			if val, ok := params[name]; ok {
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

	var report []string
	fail := func(i int, st FlowStep, stepErr error) (string, error) {
		report = append(report, fmt.Sprintf("%d. %s — ✕ %v", i+1, st.Type, stepErr))
		summary := fmt.Sprintf("browser-flow 第 %d 步（%s）失败：%v\n已执行：\n%s\n会话 %s 保留（可 browser_* 工具接手排查）",
			i+1, st.Type, stepErr, strings.Join(report, "\n"), s.id)
		return summary, nil // step failure is a result, not a tool error
	}

	for i := range steps {
		st := steps[i]
		flowSubst(&st, params)
		out, stepErr := flowExecStep(ctx, s, st)
		if stepErr != nil {
			return fail(i, st, stepErr)
		}
		if len(out) > 400 {
			out = out[:400] + "…"
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
		// Real mousemove to coordinates — synthetic events never trigger
		// CSS :hover, and hover-opened menus need it before the click.
		return tryFlowAnchors(ctx, s, st.Target, func(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
			x, y, herr := flowHoverPoint(ctx, s, a)
			if herr != nil {
				return "", herr
			}
			actx, cancel := context.WithTimeout(s.ctx, browserActionTimeout)
			defer cancel()
			if err := chromedp.Run(actx, chromedp.MouseEvent(input.MouseMoved, x, y)); err != nil {
				return "", fmt.Errorf("hover %q: %w", a.val, err)
			}
			return fmt.Sprintf("已悬停 %s（CSS :hover 生效）", a.val), nil
		})
	case "scroll":
		return call(browserScroll{}, map[string]any{"direction": st.Direction, "amount": st.Amount})
	case "select":
		return tryFlowAnchors(ctx, s, st.Target, func(ctx context.Context, s *browserSession, a flowAnchor) (string, error) {
			if a.kind == "text" {
				return flowSelectByText(ctx, s, a.val, st.Value)
			}
			args := map[string]any{"value": st.Value}
			if looksLikeRef(st.Target) {
				args["ref"] = st.Target
			} else {
				args["selector"] = st.Target
			}
			return call(browserSelectOption{}, args)
		})
	case "upload":
		args := map[string]any{"files": st.Files}
		if looksLikeRef(st.Target) {
			args["ref"] = st.Target
		} else {
			args["selector"] = st.Target
		}
		return call(browserUploadFile{}, args)
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
			if st.Value == "table" {
				args["format"] = "table"
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
