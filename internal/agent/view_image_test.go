package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/provider"
)

func TestToolResultContentPlainPassthrough(t *testing.T) {
	if got := toolResultContent("bash", "hello\nworld"); got != "hello\nworld" {
		t.Fatalf("plain tools must pass through unchanged, got %v", got)
	}
	// view_image with a non-marker output (e.g. an error text) passes through
	// as a plain string.
	got := toolResultContent("view_image", "view_image: x.png is not a displayable image (want png/jpg/jpeg/webp/gif)")
	s, isStr := got.(string)
	if !isStr || !strings.HasPrefix(s, "view_image:") {
		t.Fatalf("error text should stay a plain string, got %T = %v", got, got)
	}
}

func TestToolResultContentViewImageBuildsParts(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	// A minimal valid-ish PNG header is unnecessary — the converter only reads
	// bytes for the data URL, no decoding.
	if err := os.WriteFile(img, []byte{0x89, 'P', 'N', 'G'}, 0o644); err != nil {
		t.Fatal(err)
	}
	got := toolResultContent("view_image", viewImageMarker+img+"\n(4 bytes)")
	parts, ok := got.([]provider.ContentPart)
	if !ok {
		t.Fatalf("expected content parts, got %T", got)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[1].Type != "image_url" {
		t.Fatalf("parts = %+v", parts)
	}
	if !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("data URL = %q", parts[1].ImageURL.URL)
	}
}

func TestToolResultContentViewImageMissingFileDegrades(t *testing.T) {
	got := toolResultContent("view_image", viewImageMarker+filepath.Join(t.TempDir(), "gone.png"))
	if _, isParts := got.([]provider.ContentPart); isParts {
		t.Fatal("missing file must degrade to text, not produce parts")
	}
}
