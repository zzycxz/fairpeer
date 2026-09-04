package builtin

import "testing"

// TestMergeScanElements covers the pure fold at the heart of the deep scan:
// per-step batches (only VISIBLE elements per position) collapse into one
// ordered, anchor-deduped list; newInLast counts anchors only the FINAL batch
// introduced — the loop's "is this feed exhausted?" signal.
func TestMergeScanElements(t *testing.T) {
	mk := func(sel string) []ConsoleScanElement {
		return []ConsoleScanElement{{Role: "link", Name: sel, Selector: sel}}
	}
	// Sticky header visible at every step (same anchor) + lazy rows that
	// materialize one batch per screen.
	b1 := append(append(mk("#nav"), mk("text=条目1")...), mk("text=条目2")...)
	b2 := append(append(mk("#nav"), mk("text=条目2")...), mk("text=条目3")...)
	b3 := mk("#nav") // barren tail: nothing new

	merged, newInLast := mergeScanElements([][]ConsoleScanElement{b1, b2, b3})
	if len(merged) != 4 {
		t.Fatalf("merged = %d elements, want 4 (nav + 3 rows): %+v", len(merged), merged)
	}
	if merged[0].Selector != "#nav" {
		t.Fatalf("first-seen order broken: %+v", merged)
	}
	if newInLast != 0 {
		t.Fatalf("newInLast = %d, want 0 (final batch was barren)", newInLast)
	}

	// Growing feed: the final batch introduced exactly one new anchor.
	c1 := mk("text=a")
	c2 := append(mk("text=a"), mk("text=b")...)
	merged, newInLast = mergeScanElements([][]ConsoleScanElement{c1, c2})
	if len(merged) != 2 || newInLast != 1 {
		t.Fatalf("merged=%d newInLast=%d, want 2/1", len(merged), newInLast)
	}

	// Empty-anchor rows (scan skips them in JS, but the fold must too).
	d1 := []ConsoleScanElement{{Role: "x", Name: "n"}}
	merged, _ = mergeScanElements([][]ConsoleScanElement{d1, d1})
	if len(merged) != 0 {
		t.Fatalf("empty anchors should fold away, got %+v", merged)
	}
}
