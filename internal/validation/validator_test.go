package validation

import (
	"strings"
	"testing"
)

// TestValidateSyntax_Go covers the go/parser path: valid Go passes, broken Go
// is rejected with a line number, and a non-.go extension isn't checked.
func TestValidateSyntax_Go(t *testing.T) {
	// Valid Go.
	if err := ValidateSyntax("main.go", "package main\n\nfunc main() {}\n"); err != nil {
		t.Errorf("valid Go should pass, got %v", err)
	}
	// Broken Go (missing closing brace).
	err := ValidateSyntax("main.go", "package main\n\nfunc main( {\n")
	if err == nil {
		t.Fatal("broken Go should fail validation")
	}
	se, ok := err.(*SyntaxError)
	if !ok {
		t.Fatalf("expected *SyntaxError, got %T", err)
	}
	if se.Path != "main.go" {
		t.Errorf("Path = %q, want main.go", se.Path)
	}
	if se.Line == 0 {
		t.Errorf("Line should be > 0 for a Go syntax error, got 0; msg=%q", se.Message)
	}
	// The error string should contain the path and line for the model.
	if !strings.Contains(err.Error(), "main.go") {
		t.Errorf("error string should contain path: %q", err.Error())
	}
}

// TestValidateSyntax_JSON covers the JSON path: valid passes, broken is
// rejected, empty is allowed (init pattern).
func TestValidateSyntax_JSON(t *testing.T) {
	valid := []string{
		`{"key": "value"}`,
		`[1, 2, 3]`,
		`"hello"`,
		`42`,
		`null`,
	}
	for _, c := range valid {
		if err := ValidateSyntax("config.json", c); err != nil {
			t.Errorf("valid JSON %q should pass, got %v", c, err)
		}
	}
	// Broken JSON.
	err := ValidateSyntax("config.json", `{"key": }`)
	if err == nil {
		t.Fatal("broken JSON should fail validation")
	}
	if _, ok := err.(*SyntaxError); !ok {
		t.Fatalf("expected *SyntaxError, got %T", err)
	}
	// Empty file is allowed (initialization pattern).
	if err := ValidateSyntax("empty.json", ""); err != nil {
		t.Errorf("empty JSON file should be allowed, got %v", err)
	}
	if err := ValidateSyntax("empty.json", "   \n  "); err != nil {
		t.Errorf("whitespace-only JSON file should be allowed, got %v", err)
	}
}

// TestValidateSyntax_UnrecognizedExtension confirms unknown extensions pass
// through without a check — we never block writes to languages we don't parse.
func TestValidateSyntax_UnrecognizedExtension(t *testing.T) {
	for _, path := range []string{"readme.md", "script.py", "config.yaml", "data.csv", "Makefile", "file.txt"} {
		// Even garbage content passes — we don't check these.
		if err := ValidateSyntax(path, "{{{ broken }}}"); err != nil {
			t.Errorf("%s should pass unchecked (unrecognized ext), got %v", path, err)
		}
	}
}

// TestValidateSyntax_GoValidatesProposedContent confirms the check runs on the
// PROPOSED content string, not the on-disk file — this is what makes it a true
// pre-write guard. We pass a path that doesn't exist and content that's valid;
// it should pass without trying to read the (nonexistent) file.
func TestValidateSyntax_GoValidatesProposedContent(t *testing.T) {
	if err := ValidateSyntax("/nonexistent/path/foo.go", "package x\n"); err != nil {
		t.Errorf("should validate the content string, not read the file; got %v", err)
	}
}

// TestSyntaxErrorMessage_Clean confirms the error message doesn't carry a
// redundant "path:line:col:" prefix (SyntaxError re-adds path:line).
func TestSyntaxErrorMessage_Clean(t *testing.T) {
	err := ValidateSyntax("broken.go", "package main\n\nfunc (\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	se := err.(*SyntaxError)
	// The Message field should be the actual error text, not a re-prefixed path.
	if strings.HasPrefix(se.Message, "broken.go:") {
		t.Errorf("Message should not re-contain the path prefix: %q", se.Message)
	}
}
