package builtin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestConsoleWaitStableSemantics pins the streaming-completion detector:
// a block that sits still with a placeholder before the model's first token
// must NOT count as finished (the chat-site failure: extract returned
// "(AI生成) …"), while a block that changes and then goes quiet must.
func TestConsoleWaitStableSemantics(t *testing.T) {
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

	// out2 grows at t=1s (streaming start) — rising edge, then quiet.
	page := "<html><body>" +
		"<div id=out1>(AI生成) ...</div>" +
		"<div id=out2>(AI生成) ...</div>" +
		"<script>setTimeout(function(){document.getElementById('out2').textContent='(AI生成) 呼和浩特池共有3条告警';},1000)</script>" +
		"</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()
	if _, err := ConsoleNavigate(srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	// Streaming shape FIRST: content changes at 1s while THIS watch runs
	// (the rising edge), then quiet — stable within the budget. Later cases
	// wait 26s+, which would consume the scripted change before a watch
	// starts and turn this into the static path.
	var werr error
	if _, werr = ConsoleWait("stable:#out2", 15); werr != nil {
		t.Fatalf("changed-then-quiet block must pass as stable: %v", werr)
	}

	// Placeholder-static since watch start: 2s quiet must NOT declare stable.
	// With a 4s budget (< the 10s static confirmation) this must time out
	// and error — the pre-fix code returned success here.
	start := time.Now()
	_, werr = ConsoleWait("stable:#out1", 4)
	if werr == nil {
		t.Fatal("static placeholder must not pass as stable within 4s (pre-first-token thinking pause)")
	}
	if !strings.Contains(werr.Error(), "超时") {
		t.Fatalf("want timeout error, got: %v", werr)
	}
	if elapsed := time.Since(start); elapsed < 3*time.Second {
		t.Fatalf("returned too early (%v) — quiet window is not being enforced", elapsed)
	}

	// Incident pin (哈尔滨池 query): the model's pre-first-token pause can
	// run 30-60s on slow backends — a SHORT static placeholder must not be
	// confirmed while the answer may still arrive. 26s budget: past the old
	// flat 10s fallback and the old 30s short-content tier; the short tier is
	// now 90s, so this must still time out, not extract "(AI生成)".
	if _, werr = ConsoleWait("stable:#out1", 26); werr == nil {
		t.Fatal("short static placeholder must not pass within 26s (thinking pause >10s)")
	}

	// title: condition never met — must error as a timeout, not "waited".
	if _, werr = ConsoleWait("title:不存在的标题", 2); werr == nil || !strings.Contains(werr.Error(), "超时") {
		t.Fatalf("unmet title condition must fail with timeout error, got: %v", werr)
	}
}

// TestConsoleWaitStableStaticFallback verifies the 10s static confirmation:
// SUBSTANTIAL content static since watch start eventually passes (already-
// complete blocks on re-runs must not hang until the full timeout). Short
// static text (<40 chars, no rendered children — placeholder-shaped) instead
// takes the 90s tier so a live query's 30-60s pre-first-token pause cannot be
// misread as completion; that contract is pinned by TestConsoleWaitStableSemantics
// (placeholder must not pass within 26s).
func TestConsoleWaitStableStaticFallback(t *testing.T) {
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><div id=done>完整答案，早已渲染完毕。共计 12 条记录：严重告警 2 条、重要告警 5 条、提示 5 条，均已同步至值班看板，处置建议详见附录。</div></body></html>"))
	}))
	defer srv.Close()
	if _, err := ConsoleNavigate(srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	start := time.Now()
	if _, werr := ConsoleWait("stable:#done", 60); werr != nil {
		t.Fatalf("static-complete block must pass via 10s confirmation: %v", werr)
	}
	if elapsed := time.Since(start); elapsed >= 55*time.Second {
		t.Fatalf("static confirmation waited nearly the full budget (%v)", elapsed)
	}
}
