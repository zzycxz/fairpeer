// Command ndvshot borrows the office profile's browser automation
// (internal/browserlaunch + chromedp — the same engine behind the cowork
// browser_* tools) to visually verify the 运维 layout in the frontend dev
// shell: open the dev server, screenshot the default view, click the 运维
// profile segment, and screenshot the NetDevLayout.
//
// Usage: go run ./cmd/ndvshot -url http://127.0.0.1:5173 -out gui-test-screenshots
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/zzycxz/fairpeer/internal/browserlaunch"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:5173", "frontend dev server URL")
	out := flag.String("out", "gui-test-screenshots", "screenshot output dir")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal("mkdir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()

	handle, err := browserlaunch.Launch(ctx, browserlaunch.LaunchOptions{
		Headless: true,
	})
	if err != nil {
		fatal("launch browser: %v (is Chrome/Edge installed?)", err)
	}
	defer handle.Close()
	fmt.Println("browser:", handle.BrowserName, handle.CDPURL)

	actx, acancel := chromedp.NewRemoteAllocator(ctx, handle.WSURL)
	defer acancel()
	bctx, bcancel := chromedp.NewContext(actx)
	defer bcancel()

	// Navigate in OUR target (StartURL would land in a different tab).
	if err := chromedp.Run(bctx,
		chromedp.Navigate(*url),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(2500*time.Millisecond), // React boot + mocks
		chromedp.ActionFunc(func(ctx context.Context) error {
			return saveShot(ctx, filepath.Join(*out, "ndv-1-initial.png"))
		}),
	); err != nil {
		fatal("initial screenshot: %v", err)
	}
	fmt.Println("shot: ndv-1-initial.png")

	// Click the 运维 profile segment (third tab in the top switcher).
	if err := chromedp.Run(bctx,
		chromedp.Click(".app-chrome__profile-seg-item >> text=运维", chromedp.ByQueryAll, chromedp.NodeVisible),
	); err != nil {
		// Fallback: any segment button containing 运维.
		if err2 := chromedp.Run(bctx, chromedp.Click(`//button[contains(., "运维")]`, chromedp.BySearch)); err2 != nil {
			fatal("click 运维 segment: %v / fallback %v", err, err2)
		}
	}

	// Wait for the netdev shell to mount, then capture it and probe its key
	// regions textually so the report is evidence-based, not just pixels.
	var badge, bottom, dock string
	if err := chromedp.Run(bctx,
		chromedp.WaitReady(".ndv", chromedp.ByQuery),
		chromedp.Sleep(2500*time.Millisecond),
		chromedp.Text(".ndv__badge", &badge, chromedp.ByQuery),
		chromedp.Text(".ndv__bottom", &bottom, chromedp.ByQuery),
		chromedp.Text(".ndv__dock", &dock, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return saveShot(ctx, filepath.Join(*out, "ndv-2-netdev.png"))
		}),
	); err != nil {
		fatal("netdev screenshot: %v", err)
	}
	fmt.Println("shot: ndv-2-netdev.png")
	fmt.Printf("badge: %q\n", oneLine(badge))
	fmt.Printf("bottom: %q\n", oneLine(bottom))
	fmt.Printf("dock: %.120q\n", oneLine(dock))
	var stop, rail string
	if err := chromedp.Run(bctx,
		chromedp.Text(".ndv__stop", &stop, chromedp.ByQuery),
		chromedp.Text(".ndv__rail", &rail, chromedp.ByQuery),
	); err != nil {
		fatal("rail probe: %v", err)
	}
	fmt.Printf("stop: %q\n", oneLine(stop))
	fmt.Printf("rail: %.160q\n", oneLine(rail))
	// Send a chat message and capture the mermaid-rendered reply (dev mock).
	// The dev shell keeps a second, hidden .chat-pane transcript mounted under
	// DIV.layout, so "any svg[id^=mermaid-]" matches a 0×0 duplicate — always
	// filter to the visible diagram before scrolling or counting.
	if err := chromedp.Run(bctx,
		chromedp.SendKeys("textarea", "画一张全网拓扑示意图", chromedp.ByQuery, chromedp.NodeVisible),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.SendKeys("textarea", "\r", chromedp.ByQuery),
		chromedp.Sleep(3500*time.Millisecond),
		chromedp.Evaluate(visibleMermaidScrollJS, new(bool)),
		chromedp.Sleep(600*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return saveShot(ctx, filepath.Join(*out, "ndv-3-mermaid.png"))
		}),
	); err != nil {
		fatal("mermaid shot: %v", err)
	}
	fmt.Println("shot: ndv-3-mermaid.png")
	var svgCount int
	if err := chromedp.Run(bctx,
		chromedp.Evaluate(countVisibleMermaidJS, &svgCount),
	); err != nil || svgCount == 0 {
		fatal("mermaid-svg-rendered: 0 — no visible diagram found in DOM (err: %v)", err)
	}
	fmt.Printf("mermaid-svg-rendered: %d diagram(s)\n", svgCount)

	// Same pipeline with a flowchart (decision diamonds + labelled branches).
	if err := chromedp.Run(bctx,
		chromedp.SendKeys("textarea", "画一张端口故障排查流程图", chromedp.ByQuery, chromedp.NodeVisible),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.SendKeys("textarea", "\r", chromedp.ByQuery),
		chromedp.Sleep(3500*time.Millisecond),
		chromedp.Evaluate(visibleMermaidScrollJS, new(bool)),
		chromedp.Sleep(600*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return saveShot(ctx, filepath.Join(*out, "ndv-4-flowchart.png"))
		}),
	); err != nil {
		fatal("flowchart shot: %v", err)
	}
	fmt.Println("shot: ndv-4-flowchart.png")
	var flowCount int
	if err := chromedp.Run(bctx,
		chromedp.Evaluate(countVisibleMermaidJS, &flowCount),
	); err != nil || flowCount == 0 {
		fatal("flowchart-svg-rendered: 0 — no visible flowchart found in DOM (err: %v)", err)
	}
	fmt.Printf("flowchart-svg-rendered: %d diagram(s)\n", flowCount)

	// Interactive topology: open the 拓扑 tab — the LOCAL IP-plan view must be
	// there instantly (badge), the measured LLDP sweep only on explicit click
	// (badge flips), then click the CORE-01 node → device card.
	if err := chromedp.Run(bctx,
		chromedp.Click(`//button[contains(., "拓扑")]`, chromedp.BySearch),
		chromedp.Sleep(1200*time.Millisecond),
	); err != nil {
		fatal("topology tab: %v", err)
	}
	var planDock string
	if err := chromedp.Run(bctx, chromedp.Text(".ndv__dock", &planDock, chromedp.ByQuery)); err != nil {
		fatal("plan probe: %v", err)
	}
	if planBadge := oneLine(planDock); !strings.Contains(planBadge, "IP 规划推断") {
		fatal("local IP-plan view not shown on tab open (dock: %.120q)", planBadge)
	}
	fmt.Println("topology-local-plan: OK (instant, no device dialing)")
	if err := chromedp.Run(bctx,
		chromedp.Click(`//span[contains(., "LLDP 实测校准")]`, chromedp.BySearch),
		chromedp.Sleep(1200*time.Millisecond),
	); err != nil {
		fatal("lldp calibrate click: %v", err)
	}
	var measuredDock string
	if err := chromedp.Run(bctx, chromedp.Text(".ndv__dock", &measuredDock, chromedp.ByQuery)); err != nil {
		fatal("measured probe: %v", err)
	}
	if m := oneLine(measuredDock); !strings.Contains(m, "LLDP/CDP 实测") {
		fatal("measured badge missing after calibrate click (dock: %.120q)", m)
	}
	fmt.Println("topology-measured-calibrate: OK")
	if err := chromedp.Run(bctx,
		chromedp.Evaluate(`(function(){var gs=document.querySelectorAll(".ndv__dock svg g"); for (var i=0;i<gs.length;i++){ if(gs[i].textContent.indexOf("CORE-01")>=0){ gs[i].dispatchEvent(new MouseEvent("click",{bubbles:true})); return "clicked"; }} return "not-found";})()`, new(string)),
		chromedp.Sleep(800*time.Millisecond),
		chromedp.Evaluate(visibleMermaidScrollJS, new(bool)), // no-op if no diagrams; keeps order stable
	); err != nil {
		fatal("topology click: %v", err)
	}
	var dockText string
	if err := chromedp.Run(bctx,
		chromedp.Text(".ndv__dock", &dockText, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return saveShot(ctx, filepath.Join(*out, "ndv-5-topology.png"))
		}),
	); err != nil {
		fatal("topology dock probe: %v", err)
	}
	fmt.Println("shot: ndv-5-topology.png")
	dockNow := oneLine(dockText)
	fmt.Printf("dock-after-click: %.160q\n", dockNow)
	if !strings.Contains(dockNow, "CORE-01") || !strings.Contains(dockNow, "huawei") {
		fatal("topology click did not open the CORE-01 device card (dock: %.120q)", dockNow)
	}
	fmt.Println("topology-click→device-card: OK")

	// Project (site) switcher: pick 一号机房 in the title bar → the rail's
	// device list must drop ACC-01 (接入组 not in that project).
	if err := chromedp.Run(bctx,
		chromedp.Evaluate(`(function(){var el=[...document.querySelectorAll(".ndv__project")].pop(); if(!el) return "no-switcher"; el.click(); return "opened";})()`, new(string)),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.Evaluate(`(function(){var items=[...document.querySelectorAll(".ndv__project-menu [role=menuitem]")]; var it=items.find(i=>i.textContent.indexOf("一号机房")>=0); if(!it) return "no-item"; it.click(); return "picked";})()`, new(string)),
		chromedp.Sleep(1000*time.Millisecond),
	); err != nil {
		fatal("project switch: %v", err)
	}
	var railAfter string
	if err := chromedp.Run(bctx,
		chromedp.Text(".ndv__rail", &railAfter, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return saveShot(ctx, filepath.Join(*out, "ndv-6-project.png"))
		}),
	); err != nil {
		fatal("project rail probe: %v", err)
	}
	fmt.Println("shot: ndv-6-project.png")
	railNow := oneLine(railAfter)
	if !strings.Contains(railNow, "一号机房") || strings.Contains(railNow, "ACC-01") {
		fatal("project filter wrong (rail: %.160q)", railNow)
	}
	if !strings.Contains(railNow, "CORE-01") {
		fatal("in-project device missing (rail: %.160q)", railNow)
	}
	fmt.Println("project-switch-filter: OK")
}

// visibleMermaidScrollJS scrolls the last visible mermaid diagram into view,
// skipping the hidden duplicate transcript's 0×0 copies.
const visibleMermaidScrollJS = `(function(){var s=document.querySelectorAll("svg[id^='mermaid-']"); var vis=[]; s.forEach(function(el){ if(el.getBoundingClientRect().width>0) vis.push(el); }); if(vis.length){ vis[vis.length-1].scrollIntoView({block:"center"}); } return true;})()`

// countVisibleMermaidJS counts rendered mermaid diagrams with non-zero size.
const countVisibleMermaidJS = `(function(){var n=0; document.querySelectorAll("svg[id^='mermaid-']").forEach(function(el){ if(el.getBoundingClientRect().width>0) n++; }); return n;})()`

// saveShot captures a full-page screenshot into path.
func saveShot(ctx context.Context, path string) error {
	var buf []byte
	if err := chromedp.FullScreenshot(&buf, 90).Do(ctx); err != nil {
		return err
	}
	return os.WriteFile(path, buf, 0o644)
}

func oneLine(s string) string {
	out := ""
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\t' {
			r = ' '
		}
		if r == ' ' && prevSpace {
			continue
		}
		prevSpace = r == ' '
		out += string(r)
	}
	return out
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "ndvshot: "+f+"\n", a...)
	os.Exit(1)
}
