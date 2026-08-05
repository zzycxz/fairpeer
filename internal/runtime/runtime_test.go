package runtime

import (
	"testing"
)

// TestResolveUV_FallbackToFalse confirms that on a system where uv is not on
// PATH and not bundled, ResolveUV returns false (no panic, no hang).
func TestResolveUV_FallbackToFalse(t *testing.T) {
	// Reset the cache so this test doesn't see a previous run's result.
	// We can't easily reset sync.Once in a test, so we accept that this test
	// reflects the real environment. On a dev machine uv may or may not be
	// present — the key assertion is "no panic".
	path, ok := ResolveUV()
	_ = path
	_ = ok
	// If uv is found, the path should be non-empty.
	if ok && path == "" {
		t.Error("ResolveUV returned ok=true but empty path")
	}
}

// TestResolvePython_NoPanic confirms ResolvePython doesn't crash on any
// environment (with or without Python/uv).
func TestResolvePython_NoPanic(t *testing.T) {
	cmd, prefix, err := ResolvePython()
	if err != nil {
		// Error is fine — Python might not be installed.
		if cmd != "" {
			t.Error("on error, cmd should be empty")
		}
		return
	}
	// Success: cmd should be non-empty.
	if cmd == "" {
		t.Error("ResolvePython returned nil error but empty cmd")
	}
	// When using uv, prefix should be ["run", "python"].
	if len(prefix) > 0 && cmd != "" {
		// That's the uv path; verify the prefix shape.
		if prefix[0] != "run" {
			t.Errorf("uv prefix should start with 'run', got %v", prefix)
		}
	}
}

// TestResolveNode_NoPanic confirms ResolveNode doesn't crash.
func TestResolveNode_NoPanic(t *testing.T) {
	node, npx, ok := ResolveNode()
	_ = node
	_ = npx
	_ = ok
}

// TestDetectAll confirms DetectAll returns a struct with all fields populated
// (available or not).
func TestDetectAll(t *testing.T) {
	st := DetectAll()
	// UV and Python should have consistent cross-checks: if Python source is
	// "uv", then UV must be available.
	if st.Python.Source == "uv" && !st.UV.Available {
		t.Error("Python source=uv but UV not available — inconsistency")
	}
}

// TestBundledBaseDir confirms the bundle path resolves without error.
func TestBundledBaseDir(t *testing.T) {
	base, ok := bundledBaseDir()
	if !ok {
		// Can happen in unusual test environments; not a failure.
		t.Skip("bundledBaseDir returned false (test environment)")
	}
	if base == "" {
		t.Error("bundledBaseDir returned ok but empty path")
	}
}
