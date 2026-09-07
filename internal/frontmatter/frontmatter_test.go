package frontmatter

import (
	"strings"
	"testing"
)

func TestSplitNoFence(t *testing.T) {
	fm, body := Split("just body text\nno fence")
	if len(fm) != 0 {
		t.Errorf("expected empty fm, got %v", fm)
	}
	if !strings.Contains(body, "just body text") {
		t.Errorf("body = %q", body)
	}
}

func TestSplitUnclosedFence(t *testing.T) {
	fm, body := Split("---\nkey: val\n\nno closing fence")
	if len(fm) != 0 {
		t.Errorf("unclosed fence should return empty fm, got %v", fm)
	}
	if !strings.Contains(body, "---") {
		t.Errorf("body should contain original content: %q", body)
	}
}

func TestSplitEmptyBody(t *testing.T) {
	fm, body := Split("---\nkey: val\n---\n")
	if fm["key"] != "val" {
		t.Errorf("key = %q", fm["key"])
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestSplitNestedMetadata(t *testing.T) {
	fm, body := Split("---\nname: test\ndescription: desc\nmetadata:\n  type: user\n---\n\nbody here")
	if fm["name"] != "test" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["description"] != "desc" {
		t.Errorf("description = %q", fm["description"])
	}
	if fm["type"] != "user" {
		t.Errorf("type = %q, expected flattened from metadata", fm["type"])
	}
	if !strings.Contains(body, "body here") {
		t.Errorf("body = %q", body)
	}
}

func TestSplitCRLF(t *testing.T) {
	fm, body := Split("---\r\nname: test\r\n---\r\nbody\r\n")
	if fm["name"] != "test" {
		t.Errorf("name = %q", fm["name"])
	}
	if !strings.Contains(body, "body") {
		t.Errorf("body = %q", body)
	}
}

func TestSplitQuotedValues(t *testing.T) {
	fm, _ := Split("---\nname: test\ndescription: \"quoted desc\"\n---\n")
	if fm["description"] != "quoted desc" {
		t.Errorf("description should be unquoted: %q", fm["description"])
	}
}

func TestSplitSingleQuotes(t *testing.T) {
	fm, _ := Split("---\nname: test\ndescription: 'single quoted'\n---\n")
	if fm["description"] != "single quoted" {
		t.Errorf("description should be unquoted: %q", fm["description"])
	}
}

func TestSplitEmptyInput(t *testing.T) {
	fm, body := Split("")
	if len(fm) != 0 {
		t.Errorf("empty input should return empty fm, got %v", fm)
	}
	if body != "" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitOnlyFence(t *testing.T) {
	fm, body := Split("---\n---\n")
	if len(fm) != 0 {
		t.Errorf("empty fence should return empty fm, got %v", fm)
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitMultipleKeys(t *testing.T) {
	fm, _ := Split("---\na: 1\nb: 2\nc: 3\n---\n")
	if fm["a"] != "1" || fm["b"] != "2" || fm["c"] != "3" {
		t.Errorf("fm = %v", fm)
	}
}

func TestSplitCaseInsensitive(t *testing.T) {
	fm, _ := Split("---\nName: Test\nDESCRIPTION: desc\n---\n")
	if fm["name"] != "Test" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["description"] != "desc" {
		t.Errorf("description = %q", fm["description"])
	}
}

func TestSplitFoldedScalar(t *testing.T) {
	in := "---\nname: s\ndescription: >\n  This is a long description\n  spanning multiple lines\n  in the source file.\n---\nbody\n"
	fm, _ := Split(in)
	want := "This is a long description spanning multiple lines in the source file."
	if fm["description"] != want {
		t.Errorf("description = %q, want %q", fm["description"], want)
	}
	if fm["name"] != "s" {
		t.Errorf("keys after a block scalar were lost: %v", fm)
	}
}

func TestSplitFoldedScalarParagraphs(t *testing.T) {
	fm, _ := Split("---\ndescription: >\n  para one\n\n  para two\n---\n")
	if want := "para one\npara two"; fm["description"] != want {
		t.Errorf("description = %q, want %q", fm["description"], want)
	}
}

func TestSplitLiteralScalar(t *testing.T) {
	fm, _ := Split("---\ndescription: |\n  line one\n  line two\n    indented more\n---\n")
	want := "line one\nline two\n  indented more"
	if fm["description"] != want {
		t.Errorf("description = %q, want %q", fm["description"], want)
	}
}

func TestSplitBlockScalarStopsAtNonIndentedLine(t *testing.T) {
	fm, _ := Split("---\ndescription: >\n  body text\nother: 1\n---\n")
	if fm["description"] != "body text" {
		t.Errorf("description = %q, want %q", fm["description"], "body text")
	}
	if fm["other"] != "1" {
		t.Errorf("key after the block was swallowed: %v", fm)
	}
}

func TestSplitBlockScalarNoBody(t *testing.T) {
	// A block indicator immediately followed by a non-indented line has no
	// body: the key stays unset (like an empty section), the next line is a key.
	fm, _ := Split("---\ndescription: >\nother: 1\n---\n")
	if _, ok := fm["description"]; ok {
		t.Errorf("description = %q, want unset", fm["description"])
	}
	if fm["other"] != "1" {
		t.Errorf("other = %q, want 1", fm["other"])
	}
}

func TestSplitBlockScalarIndentedContinuation(t *testing.T) {
	// Mixed indentation: the common indent is stripped, deeper lines keep their
	// relative indent (matters for "|" especially).
	fm, _ := Split("---\nnotes: |\n    base\n      deeper\n---\n")
	if want := "base\n  deeper"; fm["notes"] != want {
		t.Errorf("notes = %q, want %q", fm["notes"], want)
	}
}

func TestSplitBlockScalarQuotedIndicatorStaysLiteral(t *testing.T) {
	fm, _ := Split("---\ndescription: \">\"\n---\n")
	if fm["description"] != ">" {
		t.Errorf("quoted > must stay a literal value, got %q", fm["description"])
	}
}

func TestSplitBlockScalarCRLF(t *testing.T) {
	fm, body := Split("---\r\ndescription: >\r\n  folded a\r\n  folded b\r\n---\r\nbody\r\n")
	if want := "folded a folded b"; fm["description"] != want {
		t.Errorf("description = %q, want %q", fm["description"], want)
	}
	if !strings.Contains(body, "body") {
		t.Errorf("body = %q", body)
	}
}
