// browserscan — deep element scan for LONG and LAZY-LOADING pages.
//
// Two different problems, two different answers:
//
//   - Long static pages are already covered: the AX tree (ConsoleElements)
//     spans the whole DOM including below-the-fold nodes, and click/highlight
//     scrollIntoView their targets. No scan needed.
//   - Lazy/infinite pages (瀑布流) only materialize rows as they scroll into
//     view — "all elements" is undefined for a truly infinite stream, and no
//     snapshot can list what the DOM doesn't contain yet. The honest approach
//     is scroll-and-collect with explicit STOP CONDITIONS: scroll a viewport
//     at a time, collect what materialized, stop when the page stops
//     producing new elements (or a budget is hit), and report why it stopped.
//
// Collected rows carry STABLE anchors (#id, tag[name="…"], text=可见文字),
// not snapshot refs — refs die with the AX snapshot that minted them, but a
// lazily materialized element from 3 screens down must stay clickable after
// the scan scrolls back up. Anchers are exactly the target vocabulary the
// panel's target input and persisted skills already accept.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// ConsoleScanElement is one row from a deep scan. Selector is a stable anchor
// usable as a click/type target (the ref slot in the panel list).
type ConsoleScanElement struct {
	Role     string `json:"role"`
	Name     string `json:"name"`
	Selector string `json:"selector"` // #id | tag[name="…"] | text=可见文字
}

// ConsoleScanResult is the deep scan's payload plus the transparency the
// "infinite" case demands: how far it scrolled, how much it found, and WHY it
// stopped (no-new | bottom | max-scrolls | cap).
type ConsoleScanResult struct {
	Elements []ConsoleScanElement `json:"elements"`
	Scrolls  int                  `json:"scrolls"`  // scroll steps performed
	Screens  int                  `json:"screens"`  // ≈ viewport heights traversed
	NewLast  int                  `json:"new_last"` // new elements found in the last step
	Stop     string               `json:"stop"`
}

const (
	scanDefaultScrolls = 12  // ~12 screens ≈ enough for most ops lists; more = slow UX
	scanElementCap     = 300 // hard ceiling — beyond this the list stops being scannable
	scanSettleMax      = 2500 * time.Millisecond
)

// Progressive settle (150→350→650ms): most feeds materialize within the first
// check, so fast pages pay 150ms per screen, not a flat 700ms.
var scanSettleSteps = []time.Duration{150 * time.Millisecond, 350 * time.Millisecond, 650 * time.Millisecond}

// scanCollectJS gathers the VISIBLE interactive elements at the current
// scroll position with a stable anchor each. Visibility matters: on lazy
// pages this is exactly the set that just materialized.
const scanCollectJS = `(function(){
  var out = [];
  var sel = 'a[href], button, input, select, textarea, [contenteditable="true"], '
    + '[role="button"], [role="link"], [role="tab"], [role="menuitem"], [role="option"], '
    + '[role="combobox"], [role="textbox"], [onclick]';
  var els = document.querySelectorAll(sel);
  for (var i = 0; i < els.length; i++) {
    var el = els[i];
    var r = el.getBoundingClientRect();
    if (r.width < 1 || r.height < 1) continue;
    if (r.bottom < 0 || r.top > window.innerHeight) continue;
    var role = el.getAttribute('role') || el.tagName.toLowerCase();
    var name = (el.getAttribute('aria-label') || (el.innerText || '').trim()
      || (el.value || '').trim() || el.getAttribute('placeholder')
      || el.getAttribute('title') || '').replace(/\s+/g, ' ').slice(0, 80);
    var anchor = '';
    if (el.id) anchor = '#' + el.id;
    else if (el.getAttribute('name')) anchor = el.tagName.toLowerCase() + '[name="' + el.getAttribute('name') + '"]';
    else if (name) anchor = 'text=' + name;
    else continue; // nothing stable to hold on to — refs would be dead on sight
    out.push({ role: role, name: name || anchor, selector: anchor });
  }
  return JSON.stringify(out);
})()`

// mergeScanElements folds per-step collections into one ordered, deduped
// list keyed by anchor. Pure so it can be unit-tested without a browser.
func mergeScanElements(batches [][]ConsoleScanElement) (merged []ConsoleScanElement, newInLast int) {
	seen := make(map[string]bool, 64)
	merged = make([]ConsoleScanElement, 0, 64)
	for _, batch := range batches {
		for _, el := range batch {
			if el.Selector == "" || seen[el.Selector] {
				continue
			}
			seen[el.Selector] = true
			merged = append(merged, el)
		}
	}
	if n := len(batches); n > 0 {
		// New anchors introduced by the FINAL batch alone.
		prior := make(map[string]bool, 64)
		for _, batch := range batches[:n-1] {
			for _, el := range batch {
				prior[el.Selector] = true
			}
		}
		for _, el := range batches[n-1] {
			if el.Selector != "" && !prior[el.Selector] {
				newInLast++
			}
		}
	}
	return merged, newInLast
}

// scanStep is one collect pass at the current position.
func scanStep(ctx context.Context) ([]ConsoleScanElement, error) {
	var out string
	if err := chromedp.Run(ctx, chromedp.Evaluate(scanCollectJS, &out)); err != nil {
		return nil, err
	}
	var els []ConsoleScanElement
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &els); err != nil {
		return nil, fmt.Errorf("scan collect: %w", err)
	}
	return els, nil
}

// scanScroll advances one viewport and waits for lazy content to settle —
// polls until scrollHeight stops growing (or a short cap), so Intersection-
// Observer driven feeds get a chance to materialize before the next collect.
// Returns false when the page cannot scroll further (bottom reached).
func scanScroll(ctx context.Context) (bool, error) {
	var pos string
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`window.scrollY + ',' + document.documentElement.scrollHeight`, &pos),
	); err != nil {
		return false, err
	}
	var y0, h0 float64
	if _, err := fmt.Sscanf(strings.TrimSpace(pos), "%f,%f", &y0, &h0); err != nil {
		return false, err
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(
		`window.scrollBy({top: Math.floor(window.innerHeight * 0.85), behavior: 'instant'})`, nil)); err != nil {
		return false, err
	}
	deadline := time.Now().Add(scanSettleMax)
	lastH := h0
	step := 0
	for time.Now().Before(deadline) {
		wait := scanSettleSteps[len(scanSettleSteps)-1]
		if step < len(scanSettleSteps) {
			wait = scanSettleSteps[step]
		}
		time.Sleep(wait)
		step++
		if err := chromedp.Run(ctx, chromedp.Evaluate(
			`window.scrollY + ',' + document.documentElement.scrollHeight`, &pos)); err != nil {
			return false, err
		}
		var y, h float64
		if _, err := fmt.Sscanf(strings.TrimSpace(pos), "%f,%f", &y, &h); err != nil {
			return false, err
		}
		if h == lastH {
			return y > y0+10, nil // moved down = can still scroll
		}
		lastH = h
	}
	return true, nil
}

// ScanPage runs the scroll-collect loop against any chromedp tab context —
// exported so the console binding and headless verification harnesses share
// one implementation. Restores the original scroll position on return.
func ScanPage(ctx context.Context, maxScrolls int) (ConsoleScanResult, error) {
	if maxScrolls <= 0 {
		maxScrolls = scanDefaultScrolls
	}
	sctx, cancel := context.WithTimeout(ctx, time.Duration(maxScrolls)*3*time.Second)
	defer cancel()

	var restoreY string
	if err := chromedp.Run(sctx, chromedp.Evaluate(`String(window.scrollY)`, &restoreY)); err != nil {
		return ConsoleScanResult{}, err
	}

	var batches [][]ConsoleScanElement
	stop := "max-scrolls"
	scrolls := 0
	noNewStreak := 0
	for i := 0; i <= maxScrolls; i++ {
		els, err := scanStep(sctx)
		if err != nil {
			return ConsoleScanResult{}, err
		}
		batches = append(batches, els)
		merged, newInLast := mergeScanElements(batches)

		if i > 0 {
			if newInLast == 0 {
				noNewStreak++
			} else {
				noNewStreak = 0
			}
			// Two consecutive barren steps = the feed is exhausted (or the
			// extra step past the bottom found nothing new). One barren step
			// is tolerated: heavy feeds hiccup between batches.
			if noNewStreak >= 2 {
				stop = "no-new"
				break
			}
		}
		if len(merged) >= scanElementCap {
			stop = "cap"
			break
		}
		if i == maxScrolls {
			break
		}
		moved, err := scanScroll(sctx)
		if err != nil {
			return ConsoleScanResult{}, err
		}
		scrolls++
		if !moved {
			// Bottom reached: one final collect there, then stop.
			els2, err := scanStep(sctx)
			if err != nil {
				return ConsoleScanResult{}, err
			}
			batches = append(batches, els2)
			stop = "bottom"
			break
		}
	}

	merged, newInLast := mergeScanElements(batches)
	_ = chromedp.Run(sctx, chromedp.Evaluate(`window.scrollTo(0, `+strings.TrimSpace(restoreY)+`)`, nil))
	if len(merged) > scanElementCap {
		merged = merged[:scanElementCap]
	}
	return ConsoleScanResult{
		Elements: merged,
		Scrolls:  scrolls,
		Screens:  scrolls + 1,
		NewLast:  newInLast,
		Stop:     stop,
	}, nil
}

// ConsoleDeepScan is the panel binding: scroll-collect over the console
// session's current tab.
func ConsoleDeepScan(maxScrolls int) (ConsoleScanResult, error) {
	s, err := consoleSession()
	if err != nil {
		return ConsoleScanResult{}, err
	}
	ctx, cancel := consoleTabCtx(s)
	defer cancel()
	res, err := ScanPage(ctx, maxScrolls)
	if err != nil {
		return ConsoleScanResult{}, errors.New("深度扫描失败：" + err.Error())
	}
	return res, nil
}
