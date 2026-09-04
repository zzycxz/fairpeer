package builtin

import (
	"strconv"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// TestConsoleElementsInteractiveOnly reproduces the Naive-UI/Vue report:
// snapshot refs also cover named non-Elements (static text nodes, the
// WebArea root titled with the page title) — those used to flood the panel's
// element list, and highlighting them died with "this.scrollIntoView is not
// a function at Text.<anonymous>". The picker must list interactive roles
// only, and highlighting a real element must succeed.
func TestConsoleElementsInteractiveOnly(t *testing.T) {
	withIsolatedConsoleBrowser(t)
	s, err := persistentBrowserSession(true)
	if err != nil {
		if strings.Contains(err.Error(), "no Chromium browser found") {
			t.Skipf("no usable Chromium on this host: %v", err)
		}
		t.Fatalf("spawn console browser: %v", err)
	}
	// Register the console slot — ConsoleNavigate/ConsoleElements resolve it.
	consoleMu.Lock()
	consoleSessionID = s.id
	consoleMu.Unlock()
	t.Cleanup(func() {
		consoleMu.Lock()
		if consoleSessionID == s.id {
			consoleSessionID = ""
		}
		consoleMu.Unlock()
		closeBrowserSession(s.id)
	})

	// A Vue-ish page: app root, static paragraphs (named AX nodes), a link
	// with text, a button, an input — mirroring the failing site's shape.
	// A SECOND same-class textarea earlier in the DOM: the single-class
	// selector is no longer unique — the ladder must still produce a css that
	// hits exactly the RIGHT element (full-class/scope/path escalation).
	page := "<html><title>AI智能助手</title><body><div id=app>" +
		"<div class='search-box'><textarea class='n-input__textarea-el' placeholder='搜索'></textarea></div>" +
		"<p>这是静态段落文字</p><p>另一段静态文字</p>" +
		"<a href='#' id=link1>登录</a>" +
		"<button id=btn1>提交</button>" +
		// Unnamed icon button (no text/aria): the name-matching pass can't
		// touch it — the positional role-order pass must supply its css.
		"<button class='icon-btn refresh-icon'></button>" +
		"<input id=in1 placeholder='请输入问题'>" +
		// The two real-world Naive-UI shapes: a textarea input and an img
		// "send button" (JS-attached listener, no role, no name — AX-blind).
		// The textarea mirrors the chat site: AX merges aria-labelledby text
		// into its name (placeholder + decorative tail) while the DOM-side
		// label stays the bare placeholder — matching must be bidirectional.
		"<div class='question-input-cover'><span id=ask-label>有什么我可以帮您吗，@选择智能体 &引用收藏提问</span>" +
		"<textarea class='n-input__textarea-el' rows='3' placeholder='有什么我可以帮您吗' aria-labelledby='ask-label'></textarea></div>" +
		"<div class='question-input-btns'><img src='send.png' style='cursor:pointer'></div>" +
		// Component-library tab strips: bare pointer divs WITH a label — must
		// be listed after the heuristic relaxation.
		"<div class='chat-tabs-container'><div><div style='cursor:pointer'>新对话</div></div></div>" +
		// Icon-only pointer div with NO label (avatar-style): intentionally
		// NOT listed (nothing to show); target it by pasted CSS instead.
		"<div class='user-info-cover'><div><div style='cursor:pointer'></div></div></div>" +
		"</div></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()
	if _, nerr := ConsoleNavigate(srv.URL); nerr != nil {
		t.Fatalf("navigate: %v", nerr)
	}

	res, err := ConsoleElements()
	if err != nil {
		t.Fatalf("elements: %v", err)
	}
	if res.Note != "" {
		t.Fatalf("unexpected degradation note: %s", res.Note)
	}
	els := res.Elements
	if len(els) == 0 {
		t.Fatal("no elements listed")
	}
	for _, el := range els {
		// AX rows (e-refs) must be interactive roles; DOM-complement rows
		// (selector refs — the img-button heuristic) may carry any tag.
		if regexpRefNum.MatchString(el.Ref) && !axInteractiveRoles[el.Role] {
			t.Errorf("non-interactive role %q listed (ref %s, name %q)", el.Role, el.Ref, el.Name)
		}
	}

	// The textarea must be listed — Chrome's AX tree reports <textarea> as
	// role "textbox" (verified empirically), so look for the placeholder.
	hasTextarea, hasImgBtn, hasLabeledDiv := false, false, false
	var taCSS string
	for _, el := range els {
		if el.Role == "textbox" && strings.Contains(el.Name, "有什么我可以帮您吗") {
			hasTextarea = true
			taCSS = el.CSS
		}
		if el.Role == "img" && !regexpRefNum.MatchString(el.Ref) {
			hasImgBtn = true
		}
		if el.Role == "div" && el.Name == "新对话" && !regexpRefNum.MatchString(el.Ref) {
			hasLabeledDiv = true
		}
	}
	if !hasTextarea {
		t.Fatal("textarea (AX role textbox) not listed")
	}
	if !hasImgBtn {
		t.Fatal("img send-button not listed — DOM clickable heuristic missed it")
	}
	if !hasLabeledDiv {
		t.Fatal("labeled pointer div (chat tab) not listed — heuristic too strict")
	}
	// AX rows must carry a computed CSS (the picker's stable target) — the
	// textarea row's css must locate the element.
	if taCSS == "" {
		t.Fatal("textarea row has no computed css — picks would fall back to the transient ref")
	}
	var hit int
	if verr := chromedp.Run(s.ctx, chromedp.Evaluate(
		"document.querySelectorAll(" + strconv.Quote(taCSS) + ").length", &hit)); verr != nil || hit != 1 {
		t.Fatalf("textarea css %q must match exactly one element, got %d (err %v)", taCSS, hit, verr)
	}
	for _, el := range els {
		if el.Role == "div" && strings.Contains(el.Ref, "user-info") {
			t.Fatalf("unlabeled avatar div should NOT be listed, got %q", el.Ref)
		}
	}

	// Highlight every listed element — none may error (the Text-node crash).
	for _, el := range els {
		if herr := ConsoleHighlight(el.Ref, 0); herr != nil {
			t.Errorf("highlight %s (%s/%s): %v", el.Ref, el.Role, el.Name, herr)
		}
	}

	// Typing into the textarea must land — the native-setter prototype must
	// follow the element's interface (HTMLInputElement's setter on a
	// textarea threw "Illegal invocation").
	var taRef string
	for _, el := range els {
		if el.Role == "textbox" && strings.Contains(el.Name, "有什么我可以帮您吗") {
			taRef = el.Ref
			break
		}
	}
	if taRef == "" {
		t.Fatal("textarea row not found for typing test")
	}
	if _, terr := ConsoleType(taRef, "你好"); terr != nil {
		t.Fatalf("type into textarea: %v", terr)
	}
	var typed string
	if verr := chromedp.Run(s.ctx, chromedp.Evaluate(
		`document.querySelectorAll('textarea.n-input__textarea-el')[1].value`, &typed)); verr != nil {
		t.Fatalf("read textarea value: %v", verr)
	}
	if typed != "你好" {
		t.Fatalf("textarea value after type = %q, want 你好", typed)
	}

	// Multi-anchor chain (selector;;text=placeholder): the panel/trial path
	// must split it instead of feeding querySelector one invalid selector —
	// and the text anchor alone must still land when the CSS side is junk.
	chain := "textarea.no-such-class;;text=有什么我可以帮您吗"
	if _, terr := ConsoleType(chain, "链路验证"); terr != nil {
		t.Fatalf("type via anchor chain: %v", terr)
	}
	var chainVal string
	if verr := chromedp.Run(s.ctx, chromedp.Evaluate(
		`document.querySelectorAll('textarea.n-input__textarea-el')[1].value`, &chainVal)); verr != nil {
		t.Fatalf("read chain value: %v", verr)
	}
	if chainVal != "链路验证" {
		t.Fatalf("chain-typed value = %q, want 链路验证", chainVal)
	}

	// Bare text= anchor (no ;; chain): the pick fallback emits this shape
	// when a row got no CSS. It must route through the anchor machinery,
	// not hit querySelector as a raw selector.
	if _, terr := ConsoleType("text=有什么我可以帮您吗", "纯文字锚"); terr != nil {
		t.Fatalf("type via bare text anchor: %v", terr)
	}
	var bareVal string
	if verr := chromedp.Run(s.ctx, chromedp.Evaluate(
		`document.querySelectorAll('textarea.n-input__textarea-el')[1].value`, &bareVal)); verr != nil {
		t.Fatalf("read bare-anchor value: %v", verr)
	}
	if bareVal != "纯文字锚" {
		t.Fatalf("bare-anchor value = %q, want 纯文字锚", bareVal)
	}
}

var regexpRefNum = regexp.MustCompile(`^e\d+$`)

// TestConsoleExtractMarkdown verifies the format=markdown renderer on a
// structured block: headings, bold, inline code, ordered/unordered lists and
// a code fence must survive extraction.
func TestConsoleExtractMarkdown(t *testing.T) {
	withIsolatedConsoleBrowser(t)
	s, err := persistentBrowserSession(true)
	if err != nil {
		if strings.Contains(err.Error(), "no Chromium browser found") {
			t.Skipf("no usable Chromium on this host: %v", err)
		}
		t.Fatalf("spawn console browser: %v", err)
	}
	consoleMu.Lock()
	consoleSessionID = s.id
	consoleMu.Unlock()
	t.Cleanup(func() {
		consoleMu.Lock()
		if consoleSessionID == s.id {
			consoleSessionID = ""
		}
		consoleMu.Unlock()
		closeBrowserSession(s.id)
	})
	page := "<html><body><div id=answer><h2>结论</h2>" +
		"<p>这是<strong>重点</strong>，代码是 <code>show ip route</code></p>" +
		"<ul><li>第一项</li><li>第二项</li></ul>" +
		"<pre>interface gi0/0\n ip addr 10.0.0.1/30</pre>" +
		"</div></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()
	if _, nerr := ConsoleNavigate(srv.URL); nerr != nil {
		t.Fatalf("navigate: %v", nerr)
	}
	out, err := ConsoleExtractAs("#answer", "markdown")
	if err != nil {
		t.Fatalf("extract markdown: %v", err)
	}
	for _, want := range []string{"## 结论", "**重点**", "`show ip route`", "- 第一项", "interface gi0/0"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output missing %q:\n%s", want, out)
		}
	}
}
