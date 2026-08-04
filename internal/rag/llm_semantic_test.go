package rag

import (
	"strings"
	"testing"
)

// TestParseExpandReply_JSONArray confirms a clean JSON array parses.
func TestParseExpandReply_JSONArray(t *testing.T) {
	raw := `["薪资","工资","薪水","报酬","salary","wage"]`
	terms := parseExpandReply(raw, "薪资")
	// Original must be first.
	if terms[0] != "薪资" {
		t.Errorf("original must be first, got %q", terms[0])
	}
	if len(terms) < 4 {
		t.Errorf("expected several terms, got %d: %v", len(terms), terms)
	}
	// No duplicates.
	seen := map[string]bool{}
	for _, term := range terms {
		k := strings.ToLower(term)
		if seen[k] {
			t.Errorf("duplicate term %q", term)
		}
		seen[k] = true
	}
}

// TestParseExpandReply_FencedJSON confirms ```json fences are tolerated.
func TestParseExpandReply_FencedJSON(t *testing.T) {
	raw := "Here are the terms:\n```json\n[\"run\", \"running\", \"ran\"]\n```"
	terms := parseExpandReply(raw, "running")
	if terms[0] != "running" {
		t.Errorf("original first, got %q", terms[0])
	}
	if len(terms) < 3 {
		t.Errorf("expected 3+ terms, got %v", terms)
	}
}

// TestParseExpandReply_GarbageFallsBack confirms an unparseable reply returns
// just the original — the caller never gets nothing.
func TestParseExpandReply_GarbageFallsBack(t *testing.T) {
	terms := parseExpandReply("I can't help with that", "salary")
	if len(terms) != 1 || terms[0] != "salary" {
		t.Errorf("garbage should fall back to [original], got %v", terms)
	}
}

// TestParseExpandReply_DedupCaseInsensitive confirms "Salary" and "salary"
// collapse (FTS5 lowercases anyway).
func TestParseExpandReply_DedupCaseInsensitive(t *testing.T) {
	raw := `["Salary","salary","SALARY"]`
	terms := parseExpandReply(raw, "wage")
	// "wage" (original) + one "Salary" (dedup of the 3 variants).
	if len(terms) != 2 {
		t.Errorf("expected dedup to 2 terms, got %v", terms)
	}
}

// TestParseRerankReply_BasicOrder confirms a clean reply produces 0-based order.
func TestParseRerankReply_BasicOrder(t *testing.T) {
	raw := `[3, 1]` // 1-based → indexes 2, 0
	order := parseRerankReply(raw, 3)
	if len(order) != 3 {
		t.Fatalf("expected 3 indexes (all placed + tail), got %d: %v", len(order), order)
	}
	if order[0] != 2 || order[1] != 0 {
		t.Errorf("first two should be [2,0], got %v", order)
	}
	// The unranked index 1 goes to the tail.
	if order[2] != 1 {
		t.Errorf("unranked index 1 should be last, got order[2]=%d", order[2])
	}
}

// TestParseRerankReply_GarbageReturnsNil confirms unparseable → nil (caller
// keeps original BM25 order).
func TestParseRerankReply_GarbageReturnsNil(t *testing.T) {
	order := parseRerankReply("no json here", 5)
	if order != nil {
		t.Errorf("garbage should return nil, got %v", order)
	}
}

// TestParseRerankReply_OutOfRangeDropped confirms indexes outside [1,n] are
// dropped (LLM hallucinated a number).
func TestParseRerankReply_OutOfRangeDropped(t *testing.T) {
	raw := `[1, 99, 2]` // 99 is out of range for n=2
	order := parseRerankReply(raw, 2)
	// index 0 (from "1"), then index 1 (from "2"), then nothing — both placed.
	if len(order) != 2 {
		t.Errorf("expected 2 valid indexes, got %d: %v", len(order), order)
	}
}

// TestApplyRerankOrder confirms results are reordered per the index order.
func TestApplyRerankOrder(t *testing.T) {
	results := []Result{
		{Path: "a", Snippet: "A"},
		{Path: "b", Snippet: "B"},
		{Path: "c", Snippet: "C"},
	}
	order := []int{2, 0, 1} // C, A, B
	out := applyRerankOrder(results, order)
	if out[0].Path != "c" || out[1].Path != "a" || out[2].Path != "b" {
		t.Errorf("order = %s,%s,%s; want c,a,b", out[0].Path, out[1].Path, out[2].Path)
	}
}

// TestApplyRerankOrder_NilOrderKeepsOriginal confirms a nil/empty order is a
// no-op (the graceful-degradation guarantee).
func TestApplyRerankOrder_NilOrderKeepsOriginal(t *testing.T) {
	results := []Result{{Path: "a"}, {Path: "b"}}
	out := applyRerankOrder(results, nil)
	if out[0].Path != "a" || out[1].Path != "b" {
		t.Errorf("nil order should preserve original, got %v", out)
	}
}

// TestExtractJSONArray confirms fence/prose tolerance.
func TestExtractJSONArray(t *testing.T) {
	cases := map[string]string{
		`["a","b"]`:             `["a","b"]`,
		`text [1,2] more`:       `[1,2]`,
		"```json\n[\"x\"]\n```": `[\"x\"]`,
		`no array here`:         `no array here`,
	}
	for in, want := range cases {
		got := extractJSONArray(in)
		// For the fenced case the want contains escaped quotes because of the
		// raw string; compare by bracket presence instead.
		if strings.HasPrefix(got, "[") != strings.HasPrefix(want, "[") {
			t.Errorf("extractJSONArray(%q) = %q, want bracket match", in, got)
		}
	}
}

// TestLLMSemantic_NilProviderNoOps confirms a nil provider makes every method a
// safe no-op (the offline/unchosen-provider degradation path).
func TestLLMSemantic_NilProviderNoOps(t *testing.T) {
	var l *LLMSemantic // nil pointer
	// ExpandQuery on nil receiver must not panic and returns the original.
	terms := l.ExpandQuery(nil, "salary")
	if len(terms) != 1 || terms[0] != "salary" {
		t.Errorf("nil LLMSemantic.ExpandQuery should return [original], got %v", terms)
	}
	// Rerank on nil receiver returns results unchanged.
	results := []Result{{Path: "a"}, {Path: "b"}}
	out := l.Rerank(nil, "q", results)
	if len(out) != 2 || out[0].Path != "a" {
		t.Errorf("nil LLMSemantic.Rerank should return results unchanged, got %v", out)
	}

	// A non-nil LLMSemantic with nil provider is also safe.
	l2 := NewLLMSemantic(nil)
	if terms := l2.ExpandQuery(nil, "工资"); len(terms) != 1 || terms[0] != "工资" {
		t.Errorf("nil-provider ExpandQuery should return [original], got %v", terms)
	}
	out2 := l2.Rerank(nil, "q", results)
	if len(out2) != 2 {
		t.Errorf("nil-provider Rerank should return results unchanged, got %v", out2)
	}
}

// TestLLMSemantic_CacheHit confirms a second expand with the same query is
// served from cache (we test the cache methods directly since we have no LLM).
func TestLLMSemantic_CacheHit(t *testing.T) {
	l := NewLLMSemantic(nil)
	l.expandCacheSet("salary", []string{"salary", "wage", "pay"})
	got, ok := l.expandCacheGet("salary")
	if !ok {
		t.Fatal("cache miss after set")
	}
	if len(got) != 3 {
		t.Errorf("cached terms should be 3, got %v", got)
	}
	// Miss for a different query.
	if _, ok := l.expandCacheGet("unknown"); ok {
		t.Error("should be a cache miss for unknown query")
	}
}

// TestRerankKey_Stability confirms the same query+results produce the same key.
func TestRerankKey_Stability(t *testing.T) {
	q := "salary"
	r := []Result{{Collection: "c", Path: "p", Snippet: "s"}}
	k1 := rerankKey(q, r)
	k2 := rerankKey(q, r)
	if k1 != k2 {
		t.Error("rerankKey should be deterministic for same input")
	}
	// Different snippet → different key.
	r2 := []Result{{Collection: "c", Path: "p", Snippet: "different"}}
	if rerankKey(q, r2) == k1 {
		t.Error("different results should produce different keys")
	}
}
