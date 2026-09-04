package builtin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/zzycxz/fairpeer/internal/browserlaunch"
)

// TestKeepAlivePingAllTabs verifies the ping keep-alive beats EVERY open tab
// (the multi-site contract: tab 1 and tab 2 hold different sites' sessions).
// Two local pages are opened as two tabs; two ticks later each tab's
// window.__fpKA must read "http 200". Runs a real browser on an isolated
// port/profile; skips when no Chromium exists or the user's live console
// browser holds 9333.
func TestKeepAlivePingAllTabs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "<html><body>page ", r.URL.Path, "</body></html>")
	}))
	defer srv.Close()

	port := withIsolatedConsoleBrowser(t)
	s, err := persistentBrowserSession(true)
	if err != nil {
		if strings.Contains(err.Error(), "no Chromium browser found") {
			t.Skipf("no usable Chromium on this host: %v", err)
		}
		t.Fatalf("spawn console browser: %v", err)
	}
	// Register the console slot like ConsoleOpen does — ConsoleNavigate (used
	// below) resolves the session through it.
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
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)

	// Tab 1 = the session's current tab; tab 2 = a second tab on the same
	// browser (a different "site" as far as the path is concerned).
	if _, nerr := ConsoleNavigate(srv.URL + "/site-a"); nerr != nil {
		t.Fatalf("navigate tab1: %v", nerr)
	}
	if oerr := browserlaunch.OpenTab(context.Background(), endpoint, srv.URL+"/site-b"); oerr != nil {
		t.Fatalf("open tab2: %v", oerr)
	}
	// Give the new tab a moment to register as a page target.
	time.Sleep(700 * time.Millisecond)

	// Tick 1 fires the beats (results land in window.__fpKA), tick 2 reads
	// them back — same cadence as the production loop.
	if terr := sessionKeepaliveTick(s, "ping", ""); terr != nil {
		t.Fatalf("tick 1: %v", terr)
	}
	if terr := sessionKeepaliveTick(s, "ping", ""); terr != nil {
		t.Fatalf("tick 2: %v", terr)
	}

	s.keepMu.Lock()
	tabs := s.keepTabs
	s.keepMu.Unlock()
	if len(tabs) < 2 {
		t.Fatalf("expected a heartbeat session per tab, got %d", len(tabs))
	}
	for id, kt := range tabs {
		// The beat fired by tick 2 resolves asynchronously — poll briefly
		// instead of racing "pending".
		var state string
		deadline := time.Now().Add(3 * time.Second)
		for {
			actx, cancel := context.WithTimeout(kt.ctx, 5*time.Second)
			var cur string
			rerr := chromedp.Run(actx, chromedp.Evaluate(`String(window.__fpKA)`, &cur))
			cancel()
			if rerr == nil {
				state = cur
				if strings.HasPrefix(cur, "http") || strings.HasPrefix(cur, "err") {
					break
				}
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if state != "http 200" {
			t.Errorf("tab %s: heartbeat state %q, want %q", id, state, "http 200")
		}
	}
}
