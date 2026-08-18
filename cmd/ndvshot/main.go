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
}

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
