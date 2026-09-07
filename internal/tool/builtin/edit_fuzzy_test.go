package builtin

import (
	"fmt"
	"strings"
	"testing"
)

func TestFuzzyMatch_Exact(t *testing.T) {
	content := "line1\nline2\nline3"
	old := "line2"
	region, found, unique := fuzzyMatch(content, old)
	if !found || !unique {
		t.Fatalf("expected found+unique, got found=%v unique=%v", found, unique)
	}
	if region != old {
		t.Fatalf("region=%q, want %q", region, old)
	}
}

func TestFuzzyMatch_LineTrim(t *testing.T) {
	// old has extra spaces that content doesn't have.
	content := "func main() {\n\tfmt.Println(\"hello\")\n}"
	old := "  func main() {  \n    fmt.Println(\"hello\")  \n  }"
	region, found, unique := fuzzyMatch(content, old)
	if !found {
		t.Fatal("expected found via line-trim match")
	}
	if !unique {
		t.Fatal("expected unique")
	}
	_ = region // region is the original untrimmed content lines
}

func TestFuzzyMatch_IndentNorm(t *testing.T) {
	// old has 4-space indent, content has 2-space indent.
	content := "if true {\n  x := 1\n  y := 2\n}"
	old := "if true {\n    x := 1\n    y := 2\n}"
	region, found, unique := fuzzyMatch(content, old)
	if !found {
		t.Fatal("expected found via indent-normalize match")
	}
	if !unique {
		t.Fatal("expected unique")
	}
	_ = region
}

func TestFuzzyMatch_BlockAnchor(t *testing.T) {
	content := `func test() {
	// some comment
	x := 1
	y := 2
	return x + y
}`
	old := `func test() {
	x := 1
	y := 2
	return x + y
}`
	region, found, unique := fuzzyMatch(content, old)
	if !found {
		t.Fatal("expected found via block-anchor match")
	}
	if !unique {
		t.Fatal("expected unique")
	}
	_ = region
}

func TestFuzzyMatch_NotFound(t *testing.T) {
	content := "line1\nline2\nline3"
	old := "nonexistent"
	_, found, _ := fuzzyMatch(content, old)
	if found {
		t.Fatal("expected not found")
	}
}

func TestFuzzyMatch_NotUnique(t *testing.T) {
	content := "abc\nabc\ndef"
	old := "abc"
	_, found, unique := fuzzyMatch(content, old)
	if !found {
		t.Fatal("expected found")
	}
	if unique {
		t.Fatal("expected not unique")
	}
}

func TestFuzzyMatch_EmptyOld(t *testing.T) {
	content := "anything"
	_, found, _ := fuzzyMatch(content, "")
	if found {
		t.Fatal("empty old should not match")
	}
}

func TestFuzzyMatch_SingleLineTrim(t *testing.T) {
	// old has no spaces, content line has spaces around it.
	// fuzzyMatch should find it via line-trim (Level 2).
	content := "  hello world  \nfoo\nbar"
	old := "helloworld" // not a substring of content, exact fails
	_ = content
	_ = old
	// Actually test with a realistic case: old has extra spaces
	content2 := "func main() {\n}"
	old2 := "  func main() {  \n  }"
	region, found, unique := fuzzyMatch(content2, old2)
	if !found {
		t.Fatal("expected found via line-trim")
	}
	if !unique {
		t.Fatal("expected unique")
	}
	_ = region
}

func TestLineTrimMatch_MultiLine(t *testing.T) {
	content := "line1\nline2\nline3"
	old := "  line1  \n  line2  \n  line3  "
	region, found, unique := lineTrimMatch(content, old)
	if !found {
		t.Fatal("expected found")
	}
	if !unique {
		t.Fatal("expected unique")
	}
	_ = region
}

func TestIndentNormMatch(t *testing.T) {
	content := "	if true {\n		x = 1\n	}"
	old := "if true {\n\tx = 1\n}"
	region, found, unique := indentNormMatch(content, old)
	if !found {
		t.Fatal("expected found")
	}
	if !unique {
		t.Fatal("expected unique")
	}
	_ = region
}

func TestBlockAnchorMatch(t *testing.T) {
	content := "func A() {\n\t// comment\n\tx := 1\n}"
	old := "func A() {\n\tx := 1\n}"
	region, found, unique := blockAnchorMatch(content, old)
	if !found {
		t.Fatal("expected found")
	}
	if !unique {
		t.Fatal("expected unique")
	}
	_ = region
}

func TestLinesContainInOrder(t *testing.T) {
	tests := []struct {
		content []string
		target  []string
		want    bool
	}{
		{[]string{"a", "b", "c"}, []string{"a", "c"}, true},
		{[]string{"a", "b", "c"}, []string{"c", "a"}, false},
		{[]string{"a", "b"}, []string{"a", "b", "c"}, false},
		{[]string{"a", "b", "c"}, []string{}, true},
	}
	for _, tt := range tests {
		got := linesContainInOrder(tt.content, tt.target)
		if got != tt.want {
			t.Errorf("linesContainInOrder(%v, %v) = %v, want %v", tt.content, tt.target, got, tt.want)
		}
	}
}

// TestLineAnchoredCount locks the line-boundary semantics shared by the
// line-trim and indent-normalized matchers: only occurrences that start at a
// line boundary (offset 0 or after '\n') AND end at one (end of string or
// before '\n') count.
func TestLineAnchoredCount(t *testing.T) {
	tests := []struct {
		joined, needle string
		wantCount      int
		wantSoleIdx    int
	}{
		{"a\nb\nc", "b", 1, 2},                          // whole line
		{"a\nb\nb", "b", 2, -1},                         // two whole lines → not sole
		{"ab\nb", "ab", 1, 0},                           // starts at 0 even though line has more
		{"x = foobar\ny = baz", "bar", 0, -1},           // mid-line start (suffix of line 1)
		{"x = foobar\ny = baz", "bar\ny = baz", 0, -1},  // starts mid-line
		{"x = foobar\ny = baz", "x = foobar\ny", 0, -1}, // ends mid-line
		{"a\nb", "", 0, -1},                             // empty needle never counts
	}
	for _, tt := range tests {
		count, idx := lineAnchoredCount(tt.joined, tt.needle)
		if count != tt.wantCount || idx != tt.wantSoleIdx {
			t.Errorf("lineAnchoredCount(%q, %q) = (%d, %d), want (%d, %d)",
				tt.joined, tt.needle, count, idx, tt.wantCount, tt.wantSoleIdx)
		}
	}
}

// TestLineTrimMatch_RejectsMidLineMatches guards the audit's data-loss case:
// old's first line must not match a SUFFIX of a content line (nor end mid-line)
// — such a "hit" maps back to whole original lines, and strings.Replace would
// then delete text outside old_string.
func TestLineTrimMatch_RejectsMidLineMatches(t *testing.T) {
	content := "x = foobar\ny = baz"

	// old starts mid-line ("bar" is a suffix of "x = foobar") — the trimmed
	// join DOES contain this substring, so only the boundary check rejects it.
	if region, found, _ := lineTrimMatch(content, "bar\ny = baz"); found {
		t.Fatalf("lineTrimMatch matched mid-line start, region=%q — Replace would delete text outside old_string", region)
	}
	// old ends mid-line ("y = b" is a prefix of "y = baz").
	if region, found, _ := lineTrimMatch(content, "x = foobar\ny = b"); found {
		t.Fatalf("lineTrimMatch matched mid-line end, region=%q", region)
	}
	// A boundary-aligned old still matches and maps to exactly the original lines.
	region, found, unique := lineTrimMatch(content, "x = foobar\ny = baz")
	if !found || !unique {
		t.Fatalf("boundary-aligned match should still work, found=%v unique=%v", found, unique)
	}
	if region != content {
		t.Fatalf("region=%q, want %q", region, content)
	}
}

// TestFuzzyMatch_MidLineTrimDeletesOutsideOld is the audit's counterexample
// end-to-end: old padded with whitespace (the case line-trim matching exists
// for) whose trimmed form sits MID-LINE in the trimmed content. The old code
// mapped that hit to whole original lines and Replace deleted "x = foo" —
// text outside old_string. All levels must now decline.
func TestFuzzyMatch_MidLineTrimDeletesOutsideOld(t *testing.T) {
	content := "x = foobar\nbaz"
	old := "  bar\n  baz" // "bar" is a suffix of the content's first line
	region, found, _ := fuzzyMatch(content, old)
	if found {
		t.Fatalf("fuzzyMatch matched mid-line start (region=%q); replacing it would delete text outside old_string", region)
	}
}

// TestIndentNormMatch_RejectsMidLineMatches is the indent-normalized variant
// of the same boundary rule: stripAllIndent output is still newline-joined
// lines, so a hit that begins mid-line must be rejected.
func TestIndentNormMatch_RejectsMidLineMatches(t *testing.T) {
	content := "aif true {\nx = 1\ny = 2\n}"
	// old's first stripped line ("true {") is a suffix of the content's first
	// line ("aif true {") — the stripped content contains oldNorm as a
	// substring, but not at a line boundary.
	if region, found, _ := indentNormMatch(content, "  true {\n  x = 1\n  y = 2\n  }"); found {
		t.Fatalf("indentNormMatch matched mid-line start, region=%q", region)
	}
}

// TestBlockAnchorMatch_BlankMiddleBoundedGap guards the audit's data-loss case:
// with an all-blank middle the anchors verify nothing, so old "func f() {\n\n}"
// used to match ANY span between the anchors — deleting a 5000-line body.
// The gap is now capped at blankMiddleGapCap; large gaps are no-match.
func TestBlockAnchorMatch_BlankMiddleBoundedGap(t *testing.T) {
	// Big body between the anchors: must NOT match.
	var b strings.Builder
	b.WriteString("func f() {\n")
	for i := 0; i < blankMiddleGapCap+50; i++ {
		b.WriteString(fmt.Sprintf("\tline %d\n", i))
	}
	b.WriteString("}")
	if region, found, _ := blockAnchorMatch(b.String(), "func f() {\n\n}"); found {
		t.Fatalf("blank-middle blockAnchor matched a %d-line gap (cap %d), region starts %q", blankMiddleGapCap+50, blankMiddleGapCap, truncate(region, 40))
	}

	// Small gap (a couple of lines): the anchors plus a modest blank middle
	// still match — that's the feature's purpose (skeleton with extra lines).
	small := "func f() {\n\ta := 1\n\tb := 2\n}"
	region, found, unique := blockAnchorMatch(small, "func f() {\n\n}")
	if !found || !unique {
		t.Fatalf("small blank gap should match, found=%v unique=%v", found, unique)
	}
	if region != small {
		t.Fatalf("region=%q, want %q", region, small)
	}
}
