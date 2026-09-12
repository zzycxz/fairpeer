package builtin

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzycxz/fairpeer/internal/tool"
)

func init() { tool.RegisterBuiltin(viewImageTool{}) }

// maxViewImageBytes caps one image read at 3 MiB raw (≈4 MiB base64-encoded) —
// inside Anthropic's 5 MB-per-image wire limit with headroom, and far below
// every openai-compatible vision endpoint's practical ceiling. Larger files
// are rejected with guidance.
const maxViewImageBytes = 3 << 20

// viewImageMarker prefixes the success output; everything after it is the
// absolute path the agent loop converts into an image content part
// (CODEX_GAP_AUDIT G6,对标 codex view_image).
const viewImageMarker = "view_image: "

// viewExts are the raster formats providers accept as image parts.
var viewExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true,
}

// viewImageTool reads an image file so a vision-capable model can see it
// directly. The zero value (init-registered) is unconfined; ConfineReaders
// binds roots when [sandbox] read_roots is configured — same discipline as
// read_file.
type viewImageTool struct {
	// workDir, when non-empty, resolves relative paths (same as read_file).
	workDir string
	// roots, when non-empty, confines reads to these directories (the
	// read-roots boundary). Empty = unconfined, the default.
	roots []string
}

func (viewImageTool) Name() string { return "view_image" }

func (viewImageTool) Description() string {
	return "Read an image file (png/jpg/jpeg/webp/gif, up to 3MB) so you can SEE it: on vision-capable models the image is attached to your context directly. Use it to inspect screenshots, generated images, diagrams, or photos referenced in the task. For text files use read_file."
}

func (viewImageTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "path":{"type":"string","description":"Absolute path to the image file"}
},
"required":["path"]
}`)
}

func (viewImageTool) ReadOnly() bool { return true }

func (t viewImageTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	p.Path = resolveIn(t.workDir, p.Path)
	if err := confineRead(t.roots, p.Path); err != nil {
		return "", err
	}
	st, err := os.Stat(p.Path)
	if err != nil {
		return "", fmt.Errorf("view_image: %w", err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("view_image: %s is a directory", p.Path)
	}
	if st.Size() > maxViewImageBytes {
		return "", fmt.Errorf("view_image: %s is %d bytes — over the %d MB image limit; downscale or crop it first", p.Path, st.Size(), maxViewImageBytes>>20)
	}
	ext := strings.ToLower(filepath.Ext(p.Path))
	if !viewExts[ext] {
		return "", fmt.Errorf("view_image: %s is not a displayable image (want png/jpg/jpeg/webp/gif)", p.Path)
	}
	if _, err := os.ReadFile(p.Path); err != nil {
		return "", fmt.Errorf("view_image: read %s: %w", p.Path, err)
	}
	abs, err := filepath.Abs(p.Path)
	if err != nil {
		abs = p.Path
	}
	// Copy into .fairpeer/attachments/ so the existing attachment rendering and
	// AttachmentDataURL bridge work without relaxing any security boundary —
	// the same pipeline image_generate uses. Idempotent (overwrite by name).
	attachDir := filepath.Join(".fairpeer", "attachments")
	_ = os.MkdirAll(attachDir, 0o755)
	attachName := fmt.Sprintf("view_%x%s", md5sum(abs), ext)
	attachPath := filepath.Join(attachDir, attachName)
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return "", fmt.Errorf("view_image: read %s: %w", p.Path, err)
	}
	if err := os.WriteFile(attachPath, data, 0o644); err != nil {
		return "", fmt.Errorf("view_image: cache copy: %w", err)
	}
	// The FIRST line must stay exactly `view_image: <abs>` — the agent loop
	// parses the path from it; the size rides the second line for humans.
	return fmt.Sprintf("%s%s\n(%d bytes)", viewImageMarker, abs, st.Size()), nil
}

// md5sum returns a short hex hash of s for filename dedup.
func md5sum(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:4])
}
