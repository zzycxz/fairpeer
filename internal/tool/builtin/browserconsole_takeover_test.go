package builtin

import (
	"context"
	"net"

	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/zzycxz/fairpeer/internal/browserlaunch"
)

// isolatedConsolePort returns a free TCP port for a test's persistent console
// browser, so tests never touch the real 9333 / ~/.fairpeer state.
func isolatedConsolePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func withIsolatedConsoleBrowser(t *testing.T) (port int) {
	t.Helper()
	port = isolatedConsolePort(t)
	// NOT t.TempDir: its cleanup runs right after ours with no grace period,
	// and Windows releases the killed browser's profile files asynchronously
	// (BrowserMetrics etc.) — RemoveAll then fails the test spuriously. We
	// manage the dir ourselves with retries instead.
	dir, err := os.MkdirTemp("", "fairpeer-console-test-")
	if err != nil {
		t.Fatal(err)
	}
	oldPort, oldDir := consoleBrowserPort, consoleProfileDir
	consoleBrowserPort, consoleProfileDir = port, dir
	oldHandle := consoleSpawnHandle
	t.Cleanup(func() {
		consoleBrowserPort, consoleProfileDir = oldPort, oldDir
		consoleSpawnHandle = oldHandle
		// A browser the spawn test left behind must not leak past the test.
		consoleMu.Lock()
		h := consoleSpawnHandle
		consoleSpawnHandle = nil
		consoleMu.Unlock()
		if h != nil {
			_ = h.Close()
		}
		for i := 0; i < 15; i++ {
			if os.RemoveAll(dir) == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	return port
}

// TestConsoleBrowserSpawnAndTakeover covers the persistent console browser's
// lifecycle with a REAL Chrome: first open SPAWNS on the pinned port/profile →
// dropping the session (what ConsoleClose / a fairpeer exit does) leaves the
// browser ALIVE → reopening TAKES OVER the leftover instance instead of
// spawning another one. Uses an isolated port + temp profile; skipped when no
// Chromium exists or the real 9333 already serves the user's live browser.
func TestConsoleBrowserSpawnAndTakeover(t *testing.T) {
	port := withIsolatedConsoleBrowser(t)
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)

	// Phase 1 — first open: nothing answers, must SPAWN and record the handle.
	s1, err := persistentBrowserSession(true)
	if err != nil {
		if strings.Contains(err.Error(), "no Chromium browser found") {
			t.Skipf("no usable Chromium on this host: %v", err)
		}
		t.Fatalf("first open (spawn): %v", err)
	}
	if s1.attached != true {
		t.Fatal("persistent session must carry attached semantics (close = disconnect only)")
	}
	consoleMu.Lock()
	h1 := consoleSpawnHandle
	consoleMu.Unlock()
	if h1 == nil {
		t.Fatalf("spawn path must record the handle; browser label %q", s1.browser)
	}
	if strings.Contains(s1.browser, "接管") {
		t.Fatalf("first open should be a fresh spawn, got %q", s1.browser)
	}
	// Phase 3 nils consoleSpawnHandle to simulate a restart — without this
	// registration the Phase-1 browser would leak past the test.
	t.Cleanup(func() { _ = h1.Close() })
	t.Cleanup(func() { closeBrowserSession(s1.id) })

	// Phase 2 — drop the session; the BROWSER must survive (the persistence
	// contract: fairpeer exits, the browser stays for the next takeover).
	closeBrowserSession(s1.id)
	time.Sleep(300 * time.Millisecond)
	if _, err := browserlaunch.ProbeAttach(context.Background(), endpoint); err != nil {
		t.Fatalf("browser must survive the session close: %v", err)
	}

	// Phase 3 — reopen: must TAKE OVER the leftover instance, not respawn.
	consoleMu.Lock()
	consoleSpawnHandle = nil // as it would be after a fairpeer restart
	consoleSessionID = ""    // and the console slot is empty again
	consoleMu.Unlock()
	s2, err := persistentBrowserSession(true)
	if err != nil {
		t.Fatalf("reopen (takeover): %v", err)
	}
	t.Cleanup(func() { closeBrowserSession(s2.id) })
	if !strings.Contains(s2.browser, "(接管)") {
		t.Fatalf("reopen should be marked a takeover, got %q", s2.browser)
	}
	consoleMu.Lock()
	h2 := consoleSpawnHandle
	consoleMu.Unlock()
	if h2 != nil {
		_ = h2.Close()
		t.Fatal("takeover must not spawn a second browser")
	}

	// Phase 4 — the takeover session must actually WORK (the historical
	// "takeover looks connected but every action dies" regression): capture
	// mirror frames through a test sink, register the console slot, navigate,
	// and assert both the preview pipeline (frame pushed) and the state sync
	// (Location readable) on the taken-over session.
	frames := make(chan BrowserPanelFrame, 8)
	SetBrowserPanelSink(func(f BrowserPanelFrame) { frames <- f })
	t.Cleanup(func() { SetBrowserPanelSink(nil) })
	consoleMu.Lock()
	consoleSessionID = s2.id
	consoleMu.Unlock()
	if _, nerr := ConsoleNavigate("about:blank"); nerr != nil {
		t.Fatalf("navigate on takeover session: %v", nerr)
	}
	st, serr := ConsoleStateOf()
	if serr != nil || !st.Open {
		t.Fatalf("state after navigate: err=%v open=%v", serr, st.Open)
	}
	gotFrame := false
	deadline := time.After(5 * time.Second)
	for !gotFrame {
		select {
		case f := <-frames:
			if f.Kind == "frame" && f.SessionID == s2.id {
				gotFrame = true
			}
		case <-deadline:
			t.Fatal("no mirror frame pushed for the takeover session — preview pipeline dead")
		}
	}

	// Phase 5 — 关闭浏览器 must truly CLOSE the owned persistent browser
	// (graceful Browser.close), not just disconnect: the endpoint must stop
	// answering once ConsoleClose returns.
	if cerr := ConsoleClose(); cerr != nil {
		t.Fatalf("ConsoleClose: %v", cerr)
	}
	shutDeadline := time.Now().Add(8 * time.Second)
	for {
		if _, perr := browserlaunch.ProbeAttach(context.Background(), endpoint); perr != nil {
			break // endpoint gone — browser closed
		}
		if time.Now().After(shutDeadline) {
			t.Fatal("ConsoleClose left the owned browser running (endpoint still answers)")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// TestAgentSessionJoinsPersistentBrowser pins the chat-skill requirement:
// newBrowserSession (browser_open / browser-flow) must land on the SAME
// persistent controlled browser — taking over a live instance instead of
// popping a second Chrome window — and on its OWN tab, never the page the
// console session is currently driving.
func TestAgentSessionJoinsPersistentBrowser(t *testing.T) {
	port := withIsolatedConsoleBrowser(t)

	// Console session first (spawns the persistent browser, opens a page).
	_ = port
	console, err := persistentBrowserSession(true)
	if err != nil {
		if strings.Contains(err.Error(), "no Chromium browser found") {
			t.Skipf("no usable Chromium on this host: %v", err)
		}
		t.Fatalf("console session: %v", err)
	}
	consoleMu.Lock()
	consoleSessionID = console.id
	consoleMu.Unlock()
	t.Cleanup(func() {
		consoleMu.Lock()
		if consoleSessionID == console.id {
			consoleSessionID = ""
		}
		consoleMu.Unlock()
		closeBrowserSession(console.id)
	})
	if _, nerr := ConsoleNavigate("about:blank"); nerr != nil {
		t.Fatalf("navigate: %v", nerr)
	}
	consoleTab := sessionTargetID(console)

	// Agent session: same browser (takeover, no second spawn), own tab.
	agent, err := newBrowserSession()
	if err != nil {
		t.Fatalf("agent session: %v", err)
	}
	t.Cleanup(func() { closeBrowserSession(agent.id) })
	if !strings.Contains(agent.browser, "(接管)") {
		t.Fatalf("agent session should take over the live browser, got %q", agent.browser)
	}
	if id := sessionTargetID(agent); id == consoleTab {
		t.Fatal("agent session must open its own tab, not drive the console's page")
	}

	// Title-based switching: the human semantic. Give the console tab a
	// distinctive title, then switch the AGENT session by name (exact) and
	// back (contains) — indexes must not be required to know.
	if terr := chromedp.Run(console.ctx, chromedp.Evaluate(
		`document.title = 'AI智能助手测试页'`, nil)); terr != nil {
		t.Fatalf("set title: %v", terr)
	}
	out, serr := ConsoleSwitchTabByTitle("AI智能助手测试页")
	if serr != nil || !strings.Contains(out, "AI智能助手测试页") {
		t.Fatalf("switch by exact title: out=%q err=%v", out, serr)
	}
	out, serr = ConsoleSwitchTabByTitle("不存在的页卡XYZ")
	if serr == nil {
		t.Fatalf("switch to unknown title must error, got %q", out)
	}

	// One browser, two tabs: the endpoint serves a single instance and lists
	// both page targets.
	infos, terr := pageTargetInfos(console)
	if terr != nil || len(infos) < 2 {
		t.Fatalf("expected both tabs on the persistent browser, got %d (err %v)", len(infos), terr)
	}
}
