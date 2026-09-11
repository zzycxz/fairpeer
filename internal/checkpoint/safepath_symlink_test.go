package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NEW-09 regression: a symlinked directory inside the workspace must not
// tunnel restore writes outside it.
func TestSafePathResolvesSymlinkTunnel(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The link points OUTSIDE the workspace: resolving it must surface the
	// escape and REJECT the path (fail-closed) — the old textual-only check
	// happily returned a path that would write through the link.
	got, err := safePath(root, filepath.Join("link", "file.txt"))
	if err == nil {
		t.Fatalf("symlink tunnel accepted: safePath returned %q", got)
	}

	// A deeper escape under the link is equally rejected at the textual layer
	// if it escapes before resolution — the resolved form is what's returned.
	got2, err := safePath(root, filepath.Join("..", "elsewhere", "x"))
	if err == nil {
		if rel2, rerr := filepath.Rel(root, got2); rerr != nil || strings.HasPrefix(rel2, "..") || !filepath.IsLocal(rel2) {
			t.Fatalf("parent escape accepted: %q", got2)
		}
	}
}
