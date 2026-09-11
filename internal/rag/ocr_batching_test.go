package rag

// Tests for the page-batched OCR walker and the import feedback builder
// (spec F-E1 fix + R2-5 skip feedback).

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRunOCRBatchesAccumulatesUntilLastPage(t *testing.T) {
	var ranges [][2]int
	run := func(first, last int) (ocrBatchResult, error) {
		ranges = append(ranges, [2]int{first, last})
		// 45-page doc: batches [1..20],[21..40],[41..60→41..45]
		switch first {
		case 1:
			return ocrBatchResult{Text: "pages 1-20", PagesTotal: 45, First: 1, Last: 20}, nil
		case 21:
			return ocrBatchResult{Text: "pages 21-40", PagesTotal: 45, First: 21, Last: 40}, nil
		default:
			return ocrBatchResult{Text: "pages 41-45", PagesTotal: 45, First: 41, Last: 45}, nil
		}
	}
	text, ok := runOCRBatches(run)
	if !ok || text == "" {
		t.Fatalf("ok=%v text=%q", ok, text)
	}
	if len(ranges) != 3 || ranges[2][0] != 41 {
		t.Errorf("batch ranges = %v, want 3 batches ending at first=41", ranges)
	}
	for _, want := range []string{"pages 1-20", "pages 21-40", "pages 41-45"} {
		if !strings.Contains(text, want) {
			t.Errorf("accumulated text missing %q", want)
		}
	}
}

// A mid-document failure keeps earlier batches (no total loss), marks the
// lost page range in [OCR warnings], and stops the walk instead of hammering
// a broken endpoint.
func TestRunOCRBatchesStopsOnErrorKeepsPartial(t *testing.T) {
	calls := 0
	run := func(first, last int) (ocrBatchResult, error) {
		calls++
		if first > 1 {
			return ocrBatchResult{}, errors.New("ocr crashed on batch 2")
		}
		return ocrBatchResult{Text: "pages 1-20", PagesTotal: 300, First: 1, Last: 20}, nil
	}
	text, ok := runOCRBatches(run)
	if !ok || !strings.Contains(text, "pages 1-20") {
		t.Errorf("partial text lost: ok=%v text=%q", ok, text)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (walk stopped after the error)", calls)
	}
	if !strings.Contains(text, "pages 21 and beyond unavailable") {
		t.Errorf("failed batch must leave a warnings marker, got %q", text)
	}
}

// res.Warnings travel in the struct field and are attached ONCE at the end —
// never interleaved per batch (P0-3), so the searchable body indexes one block.
func TestRunOCRBatchesAggregatesResWarningsOnce(t *testing.T) {
	run := func(first, last int) (ocrBatchResult, error) {
		w := []string{fmt.Sprintf("slow pages %d-%d", first, last)}
		switch first {
		case 1:
			return ocrBatchResult{Text: "pages 1-20", PagesTotal: 60, First: 1, Last: 20, Warnings: w}, nil
		case 21:
			return ocrBatchResult{Text: "pages 21-40", PagesTotal: 60, First: 21, Last: 40, Warnings: w}, nil
		default:
			return ocrBatchResult{Text: "pages 41-60", PagesTotal: 60, First: 41, Last: 60, Warnings: w}, nil
		}
	}
	text, ok := runOCRBatches(run)
	if !ok {
		t.Fatal("ok=false, want true")
	}
	if n := strings.Count(text, "- slow pages"); n != 3 {
		t.Fatalf("[OCR warnings] lines = %d, want exactly 3 (one per batch)", n)
	}
	for _, want := range []string{"slow pages 1-20", "slow pages 21-40", "slow pages 41-60"} {
		if !strings.Contains(text, want) {
			t.Errorf("aggregated warning %q missing from: %q", want, text)
		}
	}
	// The single block must trail all document text (ops report never
	// interleaves with the clauses).
	if idx, body := strings.Index(text, "[OCR warnings]"), strings.Index(text, "pages 41-60"); idx < body {
		t.Errorf("[OCR warnings] block interleaves with document text: %q", text)
	}
}

// The import feedback surfaces skipped files instead of losing them silently
// (buildImportMessage lives in desktop's package main — see its test there).
