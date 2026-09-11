package docconv

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScriptCandidatesOrder verifies the probe order: the MANAGED copy under
// ~/.fairpeer/scripts comes first (boot refreshes it from the embedded assets,
// so a stale script next to the exe can never shadow fixes — P0-3), then cwd,
// then the exe-adjacent probes.
func TestScriptCandidatesOrder(t *testing.T) {
	got := ScriptCandidates("doc_converter.py")
	if len(got) == 0 {
		t.Fatal("no candidates")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skipf("no home dir: %v", err)
	}
	wantFirst := filepath.Join(home, ".fairpeer", "scripts", "doc_converter.py")
	if got[0] != wantFirst {
		t.Fatalf("first candidate = %q, want managed copy %q", got[0], wantFirst)
	}
	if got[1] != "doc_converter.py" {
		t.Fatalf("second candidate = %q, want cwd-relative name", got[1])
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
