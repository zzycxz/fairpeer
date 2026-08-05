package docconv

import "testing"

// TestScriptCandidatesIncludesExeDir verifies the candidate list probes the
// conventional locations (cwd + relative to the executable).
func TestScriptCandidatesIncludesExeDir(t *testing.T) {
	got := ScriptCandidates("doc_converter.py")
	// The cwd-relative name must always be first.
	if len(got) == 0 || got[0] != "doc_converter.py" {
		t.Fatalf("first candidate = %v, want doc_converter.py", got)
	}
}

// TestFindScriptMissingReturnsEmpty verifies that an absent script yields "".
func TestFindScriptMissingReturnsEmpty(t *testing.T) {
	if got := FindScript("definitely_not_here_12345.py"); got != "" {
		t.Fatalf("FindScript(missing) = %q, want empty", got)
	}
}

// TestPythonExePlatform verifies pythonExe returns a usable command (either a
// direct python path or "uv" with prefix args).
func TestPythonExePlatform(t *testing.T) {
	cmd, prefix := pythonExe()
	if cmd == "" {
		t.Fatal("pythonExe() returned empty command")
	}
	// When using uv, prefix is ["run","python"]; otherwise prefix is nil.
	if len(prefix) > 0 && prefix[0] != "run" {
		t.Errorf("uv prefix should start with 'run', got %v", prefix)
	}
}
