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
	"os"
	"path/filepath"
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

	"github.com/gorilla/websocket"

	"github.com/zzycxz/fairpeer/internal/browserlaunch"
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

// --- persistent console browser (接管上次的浏览器) ----------------------------------
//
// The console browser is a PERSISTENT instance: fixed profile dir + fixed CDP
// port (away from the cowork managed browser's 9222). ConsoleOpen first probes
// that port — a browser left over from a previous fairpeer run survives
// fairpeer's exit (nobody calls Handle.Close), so it gets TAKEN OVER with all
// its tabs and login state intact. When nothing answers, we spawn the browser
// with the same profile + port, so the NEXT run can take it over in turn.
// Closing the console session (or fairpeer itself) only disconnects.

// consoleBrowserPort pins the persistent browser's CDP port. Var (not const)
// so tests can point it at an isolated port.
var consoleBrowserPort = 9333

// consoleProfileDir is where the persistent profile lives. Var so tests can
// isolate; default ~/.fairpeer/console-browser (login state survives restarts
// because cookies/localStorage persist in the profile).
var consoleProfileDir = func() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".fairpeer", "console-browser")
	}
	return filepath.Join(os.TempDir(), "fairpeer-console-browser")
}()

// consoleSpawnHandle holds the browser THIS fairpeer run spawned (nil when we
// took over a leftover instance). Production paths never Close it — the
// browser must outlive fairpeer so the next start can take it over. It exists
// for observability ("did we spawn or take over") and so tests can clean up.
var consoleSpawnHandle *browserlaunch.Handle

func consoleBrowserEndpoint() string {
	return fmt.Sprintf("http://127.0.0.1:%d", consoleBrowserPort)
}

// persistentBrowserSession returns a session onto the ONE persistent
// controlled browser (fixed profile + port): take over a leftover instance
// when one answers, else spawn it. resume=true (the console) lands on the
// page the user was looking at; resume=false (agent tools, browser-flow
// skills) gets a fresh tab so a skill's navigate never hijacks the user's
// current page. Degrades to an ephemeral spawn when the pinned port/profile
// is unusable (locked by a zombie, taken by a non-browser).
func persistentBrowserSession(resume bool) (*browserSession, error) {
	endpoint := consoleBrowserEndpoint()

	// Take over a leftover browser from a previous fairpeer run — or one the
	// user started themselves on this port.
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	info, probeErr := browserlaunch.ProbeAttach(probeCtx, endpoint)
	cancel()
	if probeErr == nil {
		if s, err := newAttachedSession(info.WSURL); err == nil {
			s.browser = info.BrowserName + " (接管)"
			s.ownsBrowser = true
			if resume {
				pickFirstConsoleTab(s)
				// Push the first frame at once — the takeover lands on a page
				// the user was already looking at, and the panel preview
				// should show it immediately, not only after the next action.
				mirrorAfterAction(s)
			}
			return s, nil
		}
	}

	// Nothing alive: spawn the persistent instance. The Handle is deliberately
	// never Closed — the browser must outlive this fairpeer process so the
	// next start can take it over.
	launchCtx, launchCancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer launchCancel()
	h, err := browserlaunch.Launch(launchCtx, browserlaunch.LaunchOptions{
		Port:        consoleBrowserPort,
		UserDataDir: consoleProfileDir,
		ExtraArgs: []string{
			// Reopen the tabs from the previous browser run — with the
			// persistent profile this restores "where I left off", not just
			// the logins.
			"--restore-last-session",
			// The visible "浏览器正在受控制" cue. Users relied on it to tell
			// the driven browser apart from their daily one — dropping it
			// read as "sync is broken", so it comes back deliberately.
			"--enable-automation",
		},
	})
	if err != nil {
		// Pinned port taken by a non-browser, or profile locked by a zombie
		// half-process: degrade to the old ephemeral spawn rather than fail.
		h2, err2 := browserlaunch.Launch(launchCtx, browserlaunch.LaunchOptions{})
		if err2 != nil {
			return nil, fmt.Errorf("启动持久浏览器失败: %v；临时浏览器也失败: %w", err, err2)
		}
		return finishConsoleSpawn(h2)
	}
	return finishConsoleSpawn(h)
}

// finishConsoleSpawn drives a freshly spawned browser over CDP, records the
// spawn handle, and applies the close semantics that make persistence safe.
func finishConsoleSpawn(h *browserlaunch.Handle) (*browserSession, error) {
	s, err := newAttachedSession(h.WSURL)
	if err != nil {
		return nil, err
	}
	s.browser = h.BrowserName
	s.ownsBrowser = true
	consoleMu.Lock()
	consoleSpawnHandle = h
	consoleMu.Unlock()
	// newAttachedSession already sets attached=true: closing the console
	// session (or the idle reaper, or fairpeer's exit) only disconnects —
	// the persistent browser stays alive for the next takeover.
	return s, nil
}

// pickFirstConsoleTab lands a fresh takeover on the first REAL page tab
// instead of the blank tab the attach created — takeover should resume where
// the user left off, not on about:blank.
func pickFirstConsoleTab(s *browserSession) {
	infos, err := pageTargetInfos(s)
	if err != nil {
		return
	}
	cur := sessionTargetID(s)
	for _, t := range infos {
		if t.TargetID == cur {
			continue
		}
		if t.URL == "" || t.URL == "about:blank" || strings.HasPrefix(t.URL, "chrome://") {
			continue
		}
		_ = switchSessionTab(s, t.TargetID)
		return
	}
}

// ConsoleOpen ensures the console session exists — spawning a visible browser
// (independent tab, isolated from the agent's own sessions) or attaching to an
// external one when cdpURL is set — and optionally navigates to startURL.
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
		// Persistent instance: takeover when a leftover browser answers the
		// fixed port, else spawn with the persistent profile + port.
		s, err = persistentBrowserSession(true)
	}
	if err != nil {
		return ConsoleState{}, err
	}
	consoleMu.Lock()
	consoleSessionID = s.id
	consoleMu.Unlock()
	initConsoleRecorderListener(s)
	initDevToolsListener(s)
	if startURL != "" {
		if _, nerr := ConsoleNavigate(startURL); nerr != nil {
			return ConsoleState{}, fmt.Errorf("已打开浏览器但导航失败: %w", nerr)
		}
	}
	return consoleStateOf(s)
}

// ConsoleClose stops any active recording, then closes the session. For the
// PERSISTENT console browser (ownsBrowser) this truly CLOSES the browser —
// graceful CDP Browser.close: every tab plus the process, with the profile
// flushed to disk (login state survives for the next open). Explicit user
// attaches keep the old semantics: disconnect only, their browser lives.
func ConsoleClose() error {
	_ = ConsoleRecordStop()
	consoleMu.Lock()
	id := consoleSessionID
	consoleSessionID = ""
	consoleMu.Unlock()
	if id == "" {
		return nil
	}
	if s, err := getBrowserSession(id); err == nil && s.ownsBrowser {
		// Best-effort graceful close BEFORE tearing the session down — after
		// closeBrowserSession the connection is gone and there is nothing to
		// close with. A failure here (browser already dead) still falls
		// through to the disconnect.
		closeOwnedBrowser()
		consoleMu.Lock()
		consoleSpawnHandle = nil
		consoleMu.Unlock()
	}
	closeBrowserSession(id)
	return nil
}

// closeOwnedBrowser sends a raw Browser.close over the browser-level CDP
// websocket. chromedp's public executor intercepts browser.Close ("use
// chromedp.Cancel" — which for remote allocators only disconnects), so the
// graceful path is one bare websocket frame: Chrome closes every target and
// exits, flushing the persistent profile to disk first (unlike a process
// kill, which can lose recent cookies).
func closeOwnedBrowser() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := browserlaunch.ProbeAttach(ctx, consoleBrowserEndpoint())
	if err != nil {
		return // browser already gone — the disconnect below is enough
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, info.WSURL, nil)
	if err != nil {
		fmt.Printf("[console] browser close dial: %v\n", err)
		return
	}
	defer conn.Close()
	// Browser.close closes the socket before any reply — fire and forget.
	_ = conn.WriteJSON(struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}{ID: 1, Method: "Browser.close"})
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
	// CSS is the element's stable selector (computed at capture time). Picking
	// the row fills the target with `css;;text=name` — recorded skills stay
	// valid across sessions; the bare ref dies with the snapshot.
	CSS string `json:"css,omitempty"`
}

const consoleElementsCap = 200

// ConsoleElementsResult is the picker's payload plus a degradation note:
// when the full AX-tree capture fails (huge pages, streaming renderers that
// starve the main thread past the 60s action timeout), the DOM-complement
// sweep still returns the interactive elements — selector-based rows work
// for click/type/highlight, only e-ref rows are unavailable.
type ConsoleElementsResult struct {
	Elements []ConsoleElement `json:"elements"`
	Note     string           `json:"note,omitempty"`
}

func ConsoleElements() (ConsoleElementsResult, error) {
	s, err := consoleSession()
	if err != nil {
		return ConsoleElementsResult{}, err
	}
	out := make([]ConsoleElement, 0, 64)
	nodes, axErr := captureAXTree(s)
	if axErr != nil {
		// Degraded mode: skip the AX half entirely (refs stay untouched —
		// stale is worse than none), the DOM sweep below still fills the
		// list. Surface the cause through the note instead of failing the
		// whole refresh.
		axErr = fmt.Errorf("capture accessibility tree: %w", axErr)
	} else {
		refs, _ := buildSnapshotRefs(nodes)
		// Publish the refs into the session exactly like browser_snapshot
		// does — the panel's click/type go through the agent tools' ref
		// resolution, which only reads s.refs.
		published := refs
		s.refs.Store(&published)
		for ref, info := range refs {
			// Interactive roles only: the snapshot's refs also cover named
			// nodes (static text, the WebArea root) because the AGENT reads
			// page content through them — but they have no scrollIntoView/
			// click, so listing them in the picker produced
			// "this.scrollIntoView is not a function" highlights.
			if !axInteractiveRoles[info.role] {
				continue
			}
			out = append(out, ConsoleElement{Ref: ref, Role: info.role, Name: info.name, Value: info.value})
		}
		sort.Slice(out, func(i, j int) bool {
			return refNumber(out[i].Ref) < refNumber(out[j].Ref)
		})
	}
	// Stable CSS per AX row: match rows to DOM elements by accessible name
	// over the interactive pool, then the selector ladder. Best effort —
	// unmatched rows simply keep ref-only semantics.
	axCtx, axCancel := consoleTabCtx(s)
	if cssMap, cerr := axRowCSS(axCtx, axRowsOf(out)); cerr == nil {
		for i := range out {
			if css := cssMap[out[i].Ref]; css != "" {
				out[i].CSS = css
			}
		}
	}
	axCancel()

	// DOM complement: component libraries (Vue/Naive-UI, React/MUI) render
	// buttons as imgs/divs with JS-attached listeners — invisible to the AX
	// tree (no role, often no name). The pointer-cursor/button-class
	// heuristic (browser-use style) catches them; rows carry a generated CSS
	// selector in the ref slot, the same convention deep-scan rows use.
	//
	// Dedup: the AX tree and DOM scan often find the same elements. When they
	// do, keep the AX entry (it carries a snapshot ref for click/type) and
	// merge the DOM scan's CSS into AX entries that are still missing one.
	domCtx, domCancel := consoleTabCtx(s)
	defer domCancel()
	if domEls, derr := scanDomCandidates(domCtx); derr == nil {
		// The DOM rows are the AX-blind complement this pass exists to
		// surface, and they append AFTER the AX rows — the final cap
		// truncation always cut the tail, so on AX-heavy pages (hundreds of
		// interactive nodes) the img/div buttons never made the list.
		// Reserve their room up front; 40 floors it so tiny DOM scans never
		// decimate the AX listing either.
		axBudget := consoleElementsCap - len(domEls)
		if axBudget < 40 {
			axBudget = 40
		}
		if len(out) > axBudget {
			out = out[:axBudget]
		}
		axCSS := make(map[string]bool, len(out))
		for _, el := range out {
			if el.CSS != "" {
				axCSS[el.CSS] = true
			}
		}
		for _, dom := range domEls {
			if dom.Ref != "" && axCSS[dom.Ref] {
				continue // duplicate: AX tree already has this selector
			}
			// AX found the element but couldn't compute CSS — merge the
			// DOM scan's selector into the existing AX entry.
			if dom.Ref != "" {
				merged := false
				for i := range out {
					if out[i].CSS == "" && out[i].Name != "" && out[i].Name == dom.Name {
						out[i].CSS = dom.Ref
						merged = true
						break
					}
				}
				if merged {
					continue
				}
			}
			out = append(out, dom)
		}
	}
	if len(out) > consoleElementsCap {
		out = out[:consoleElementsCap]
	}
	// captureAXTree bypasses runBrowserAction, so the mirror got no frame —
	// push one so the panel preview reflects "where am I".
	mirrorAfterAction(s)
	note := ""
	if axErr != nil {
		note = "无障碍树读取超时/失败（页面过大或正在流式输出），已降级为 DOM 扫描——选择器类目标仍可点击/输入/高亮"
	}
	return ConsoleElementsResult{Elements: out, Note: note}, nil
}

// axRowsOf projects the AX rows into the matcher's input shape.
func axRowsOf(els []ConsoleElement) []axRow {
	rows := make([]axRow, 0, len(els))
	for _, el := range els {
		rows = append(rows, axRow{Ref: el.Ref, Role: el.Role, Name: el.Name, Value: el.Value})
	}
	return rows
}

// axRow is the matcher's input; lowercase JSON keys — the page JS reads
// row.ref/row.name (capitalized Go field names marshaled to undefined).
type axRow struct {
	Ref   string `json:"ref"`
	Role  string `json:"role,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
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

// ConsoleSwitchTabByTitle switches by tab title (exact then contains) —
// users know tabs by name, and indexes drift as tabs open and close.
func ConsoleSwitchTabByTitle(title string) (string, error) {
	return consoleExec(browserSwitchTab{}.Execute, map[string]any{"target": title})
}

// ConsoleHighlight marks a target in the page so the user can SEE which
// element a list row refers to — the mirror preview must catch it, so the
// mark is designed for screenshots:
//
//	durationMs > 0: transient flash (hover affordance), restored after the
//	  duration via the element's previous inline styles.
//	durationMs <= 0: PERSISTENT mark — a __fp-hl class backed by one injected
//	  style rule. Selecting another target replaces the mark; highlighting
//	  the SAME target toggles it off. Survives until then, so any poll cadence
//	  (sidebar frames, the workbench's 5s) catches it.
//
// Both paths scroll the element into view and push a mirror frame right
// away — the preview refreshes the instant the mark lands, not on the next
// action.
func ConsoleHighlight(target string, durationMs int) error {
	s, err := consoleSession()
	if err != nil {
		return err
	}
	persist := durationMs <= 0
	if durationMs > 5000 {
		durationMs = 5000
	}
	ctx, cancel := consoleTabCtx(s)
	defer cancel()
	var js string
	// callOnRef passes these through Runtime.callFunctionOn, which resolves the
	// string as a FUNCTION and calls it with the element as `this`. An IIFE
	// form like `(function(){...})()` executes at resolution time with
	// this=undefined — every access of `this` throws and the highlight never
	// paints (verified empirically against Chrome). Plain declarations only.
	//
	// `this` may resolve to a NON-ELEMENT (snapshot refs also cover Text nodes
	// and the document root): resolveActable walks up to the parent Element —
	// the containing link/button is what the user meant anyway (Playwright
	// text-selector semantics) — or reports not-an-element.
	if persist {
		js = `function() {
  var el = this.nodeType === 1 ? this : this.parentElement;
  if (!el || typeof el.scrollIntoView !== 'function') return 'not-an-element';
  if (!document.getElementById('__fp_hl_style')) {
    var st = document.createElement('style');
    st.id = '__fp_hl_style';
    st.textContent = '.__fp_hl{outline:3px solid #ff8c00 !important;outline-offset:1px !important;background-color:rgba(255,140,0,0.18) !important;}';
    document.head.appendChild(st);
  }
  var prev = document.querySelector('.__fp_hl');
  if (prev) prev.classList.remove('__fp_hl');
  if (prev === el) return 'cleared';
  el.scrollIntoView({block: "center", behavior: "instant"});
  el.classList.add('__fp_hl');
  return 'marked';
}`
	} else {
		js = `function() {
  var el = this.nodeType === 1 ? this : this.parentElement;
  if (!el || typeof el.scrollIntoView !== 'function') return 'not-an-element';
  el.scrollIntoView({block: "center", behavior: "instant"});
  var prevO = el.style.outline, prevOS = el.style.outlineOffset, prevBg = el.style.backgroundColor;
  el.style.outline = "3px solid #ff8c00";
  el.style.outlineOffset = "1px";
  el.style.backgroundColor = "rgba(255,140,0,0.15)";
  setTimeout(function(){
    el.style.outline = prevO; el.style.outlineOffset = prevOS; el.style.backgroundColor = prevBg;
  }, DURATION);
  return true;
}`
		js = strings.Replace(js, "DURATION", strconv.Itoa(durationMs), 1)
	}
	if looksLikeRef(target) {
		out, err := callOnRef(ctx, s, target, js)
		if err != nil {
			return fmt.Errorf("高亮 %q: %w", target, err)
		}
		if strings.Contains(out, "not-an-element") {
			return fmt.Errorf("元素 %q 不是可交互节点（文本/文档节点且无元素父级）——列表已过滤此类节点，请重新「刷新元素」后选择", target)
		}
		mirrorAfterAction(s)
		return nil
	}
	// Target vocabulary: CSS selector, or a text=可见文字 anchor (the deep
	// scan emits these for lazy-page rows without id/name). The scanners
	// that produce these selectors reach inside shadow roots — resolution
	// must too, or Web Component rows highlight as "not found".
	finder := `(function(){
  function queryDeep(sel) {
    try { var hit = document.querySelector(sel); if (hit) return hit; } catch (e) { return null; }
    var all = document.querySelectorAll('*');
    for (var i = 0; i < all.length; i++) {
      if (all[i].shadowRoot) {
        try { var s = all[i].shadowRoot.querySelector(sel); if (s) return s; } catch (e) {}
      }
    }
    return null;
  }
  return queryDeep(` + fmt.Sprintf("%q", target) + `);
})()`
	if want, ok := strings.CutPrefix(target, "text="); ok {
		finder = `(function(){
  var want = ` + fmt.Sprintf("%q", strings.TrimSpace(want)) + `;
  var cand = [];
  function collect(root) {
    root.querySelectorAll('a[href],button,input,select,textarea,[contenteditable="true"],[role="button"],[role="link"],[role="tab"],[onclick]').forEach(function(n){ cand.push(n); });
    // Component libraries render clickables as bare pointer divs/spans —
    // sweep them in so text= anchors cover them too.
    root.querySelectorAll('div,span').forEach(function(n){
      if (getComputedStyle(n).cursor === 'pointer') cand.push(n);
    });
    root.querySelectorAll('*').forEach(function(n){ if (n.shadowRoot) collect(n.shadowRoot); });
  }
  collect(document);
  // Match any label source — the ladder's nameOf picks one by priority, so
  // a single fixed order here would disagree with the produced anchor.
  function labels(el) {
    var out = [];
    var push = function(v){
      v = ((v == null ? '' : v) + '').replace(/\s+/g,' ').trim().slice(0,80);
      if (v && out.indexOf(v) === -1) out.push(v);
    };
    push(el.getAttribute('aria-label'));
    push(el.getAttribute('alt'));
    push(el.getAttribute('placeholder'));
    push(el.getAttribute('title'));
    push(el.innerText || el.value || '');
    return out;
  }
  for (var i=0;i<cand.length;i++){
    var ls = labels(cand[i]);
    for (var j=0;j<ls.length;j++){ if (ls[j] === want) return cand[i]; }
  }
  return null;
})()`
	}
	// The outer IIFE must RETURN the body's result — a bare BODY statement
	// evaluates to undefined and chromedp reports "encountered an undefined
	// value" (previously masked by the panel swallowing highlight errors).
	expr := strings.Replace(`(function(){
  var el = FINDER;
  if (!el) return false;
  return (BODY);
})()`, "FINDER", finder, 1)
	if persist {
		body := `(function(){
  if (!document.getElementById('__fp_hl_style')) {
    var st = document.createElement('style');
    st.id = '__fp_hl_style';
    st.textContent = '.__fp_hl{outline:3px solid #ff8c00 !important;outline-offset:1px !important;background-color:rgba(255,140,0,0.18) !important;}';
    document.head.appendChild(st);
  }
  var target = el;
  var wasPrev = target.classList.contains('__fp_hl');
  document.querySelectorAll('.__fp_hl').forEach(function(n){ n.classList.remove('__fp_hl'); });
  if (wasPrev) return 'cleared';
  target.scrollIntoView({block: "center", behavior: "instant"});
  target.classList.add('__fp_hl');
  return 'marked';
})()`
		expr = strings.Replace(expr, "BODY", body, 1)
	} else {
		body := `el.scrollIntoView({block: "center", behavior: "instant"});
  var prevO = el.style.outline, prevOS = el.style.outlineOffset, prevBg = el.style.backgroundColor;
  el.style.outline = "3px solid #ff8c00";
  el.style.outlineOffset = "1px";
  el.style.backgroundColor = "rgba(255,140,0,0.15)";
  setTimeout(function(){
    el.style.outline = prevO; el.style.outlineOffset = prevOS; el.style.backgroundColor = prevBg;
  }, DURATION);
  return true;`
		body = strings.Replace(body, "DURATION", strconv.Itoa(durationMs), 1)
		expr = strings.Replace(expr, "BODY", body, 1)
	}
	var out any
	if err := chromedp.Run(ctx, chromedp.Evaluate(expr, &out)); err != nil {
		return fmt.Errorf("高亮 %q: %w", target, err)
	}
	outText := fmt.Sprintf("%v", out)
	// The finder returns false when nothing matched — evaluate still succeeds,
	// so without this check a miss silently no-ops (and the preview never
	// paints, which reads as "highlight is broken").
	if b, isBool := out.(bool); isBool && !b {
		return fmt.Errorf("页面上找不到目标 %q（CSS/文字锚点未命中，元素可能未加载或已变化）", target)
	}
	if strings.Contains(outText, "false") {
		return fmt.Errorf("页面上找不到目标 %q（CSS/文字锚点未命中，元素可能未加载或已变化）", target)
	}
	mirrorAfterAction(s)
	return nil
}

// ConsoleHover hovers the pointer over a target (ref or CSS) — a real
// mousemove to the element center, so CSS :hover menus open.
func ConsoleHover(target string) (string, error) {
	if anchoredTarget(target) {
		return runWithAnchors("hover", target, nil)
	}
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

// anchoredTarget reports whether the target needs the anchor machinery:
// a `;;` fallback chain OR a bare text= anchor. Plain CSS/refs take the
// direct path.
func anchoredTarget(target string) bool {
	t := strings.TrimSpace(target)
	return strings.Contains(t, ";;") || strings.HasPrefix(t, "text=")
}

// runWithAnchors routes one console action through the deterministic flow
// runner's anchor machinery when the target carries a `;;` fallback chain
// (selector;;text=visible text). Panel manual actions and trial runs share
// this with /browser-flow skills — without it a pasted chain hit
// querySelector as ONE invalid selector.
func runWithAnchors(kind, target string, fill func(st *FlowStep)) (string, error) {
	s, err := consoleSession()
	if err != nil {
		return "", err
	}
	st := FlowStep{Type: kind, Target: target}
	if fill != nil {
		fill(&st)
	}
	ctx, cancel := consoleTabCtx(s)
	defer cancel()
	return flowExecStep(ctx, s, st)
}

// ConsoleClick accepts a snapshot ref ("e5") or a CSS selector.
func ConsoleClick(target string) (string, error) {
	if anchoredTarget(target) {
		return runWithAnchors("click", target, nil)
	}
	return consoleExec(browserClick{}.Execute, map[string]any{"target": target})
}

// ConsoleType types text into the element named by target (ref or CSS),
// clearing it first.
func ConsoleType(target, text string) (string, error) {
	if anchoredTarget(target) {
		return runWithAnchors("type", target, func(st *FlowStep) { st.Text = text })
	}
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
// Anchored targets (`;;` chains, bare text=) route through the flow runner —
// the picker's rows carry selector refs, and a raw "text=..." used to reach
// querySelector and SyntaxError.
func ConsoleSelectOption(target, value string) (string, error) {
	if anchoredTarget(target) {
		return runWithAnchors("select", target, func(st *FlowStep) { st.Value = value })
	}
	args := map[string]any{"value": value}
	targetArg(args, target)
	return consoleExec(browserSelectOption{}.Execute, args)
}

func ConsoleUploadFile(target string, files []string) (string, error) {
	if anchoredTarget(target) {
		return runWithAnchors("upload", target, func(st *FlowStep) { st.Files = files })
	}
	args := map[string]any{"files": files}
	targetArg(args, target)
	return consoleExec(browserUploadFile{}.Execute, args)
}

func ConsoleWait(condition string, timeoutSec int) (string, error) {
	if timeoutSec > 600 { // ms habit — same disambiguation as waitSecondsOr
		timeoutSec /= 1000
	}
	if timeoutSec <= 0 {
		timeoutSec = 90
	}
	return consoleExec(browserWait{}.Execute, map[string]any{"condition": condition, "timeout": timeoutSec})
}

// DownloadInfo describes one finished browser download — the structured form
// of the "wait download" condition, for panel trial runs and watch rounds.
type DownloadInfo struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"` // "completed"
}

// ConsoleWaitDownload blocks until a download triggered by an earlier action
// reaches a terminal state (SIEM exports run 20s–5min) and returns the file's
// verified full path. Structured sibling of the "download" wait condition.
func ConsoleWaitDownload(timeoutSec int) (DownloadInfo, error) {
	if timeoutSec > 600 { // ms habit
		timeoutSec /= 1000
	}
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	s, err := consoleSession()
	if err != nil {
		return DownloadInfo{}, err
	}
	// The poll loop needs no page target; reuse the session context so it
	// still unblocks when the console browser closes.
	rec, path, err := waitDownloadRecord(s.ctx, s, "download", time.Duration(timeoutSec)*time.Second)
	if err != nil {
		return DownloadInfo{}, err
	}
	return DownloadInfo{Name: rec.SuggestedName, Path: path, State: rec.State}, nil
}

func ConsoleExtract(selector string) (string, error) {
	return ConsoleExtractAs(selector, "")
}

// ConsoleExtractAs extracts with an explicit format: "" plain text,
// "table" markdown tables, "markdown" structure-preserving render.
func ConsoleExtractAs(selector, format string) (string, error) {
	if anchoredTarget(selector) {
		return runWithAnchors("extract", selector, func(st *FlowStep) { st.Value = format })
	}
	args := map[string]any{"format": format}
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
  const isDynId = (id) => {
    if (!id) return false;
    if (/^ext-gen\d+$/.test(id)) return true;
    if (/^widget-[a-z]+-\d+/i.test(id)) return true;
    if (/-(\d{3,})(?:-|$)/.test(id) && !/^[a-z]+-[a-z]/i.test(id)) return true;
    return false;
  };
  const escAttr = (v) => v.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
  const sel = (el) => {
    if (!el || el === document || el === document.documentElement) return "body";
    if (el.id && !isDynId(el.id)) return "#" + CSS.escape(el.id);
    const nm = el.getAttribute("name");
    if (nm) return el.tagName.toLowerCase() + '[name="' + escAttr(nm) + '"]';
    const tid = el.getAttribute("data-testid") || el.getAttribute("data-ref");
    if (tid) return '[' + (el.hasAttribute('data-ref') ? 'data-ref' : 'data-testid') + '="' + escAttr(tid) + '"]';
    const aria = el.getAttribute("aria-label");
    if (aria) return '[aria-label="' + escAttr(aria) + '"]';
    if (el.id) {
      const stable = el.id.replace(/-\d+(?:-\w+)?$/, '');
      if (stable && stable !== el.id) {
        const tag = el.tagName.toLowerCase();
        const type = el.getAttribute('type');
        const combo = '[id^="' + stable + '"]' + (type ? '[type="' + type + '"]' : '');
        try { if (document.querySelectorAll(combo).length === 1) return combo; } catch(e) {}
      }
    }
    const parts = [];
    let cur = el, depth = 0;
    while (cur && cur !== document.body && depth < 5) {
      if (cur.id && !isDynId(cur.id)) {
        parts.unshift('#' + CSS.escape(cur.id));
        break;
      }
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
