package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewImageMarkerOutputAndRejections(t *testing.T) {
	dir := t.TempDir()
	img := `PNG`
	path := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(path, []byte(img), 0o644); err != nil {
		t.Fatal(err)
	}

	tl := viewImageTool{}
	out, err := tl.Execute(context.Background(), mustJSONView(map[string]string{"path": path}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, viewImageMarker) || !strings.Contains(out, path) {
		t.Fatalf("output = %q, want marker + path", out)
	}

	// Missing file.
	if _, err := tl.Execute(context.Background(), mustJSONView(map[string]string{"path": filepath.Join(dir, "nope.png")})); err == nil {
		t.Fatal("missing file must be rejected")
	}
	// Non-image extension.
	txt := filepath.Join(dir, "notes.txt")
	os.WriteFile(txt, []byte("x"), 0o644)
	if _, err := tl.Execute(context.Background(), mustJSONView(map[string]string{"path": txt})); err == nil {
		t.Fatal("non-image extension must be rejected")
	}
	// Directory.
	if _, err := tl.Execute(context.Background(), mustJSONView(map[string]string{"path": dir})); err == nil {
		t.Fatal("directory must be rejected")
	}
}

func mustJSONView(m map[string]string) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}
