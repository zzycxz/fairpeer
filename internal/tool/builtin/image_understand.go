package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/gif" // register decoders for image.Decode
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzycxz/fairpeer/internal/tool"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp" // register webp decoder
)

// image_understand is the user-image counterpart to screen_perceive: it reads an
// image file (typically a user-uploaded attachment referenced as
// @.fairpeer/attachments/x.png) and asks the configured vision model (VLM) about
// it, returning the VLM's text response. The prompt is provided by the calling
// agent, so the agent decides WHAT to ask — a layout question, an OCR request, a
// chart readout — rather than getting a fixed generic description.
//
// This is the single-track image path: user images are never sent to the main
// model as image_url parts. Instead ResolveRefs leaves a text <image path="...">
// reference in the message, the main model sees it, and calls this tool with a
// task-specific prompt to obtain the image's content as text. This keeps any
// model (vision-capable or not) from receiving an image_url part it cannot
// handle, and follows the ZCode analyze_image pattern where the agent authors
// the VLM prompt around its current task.
//
// `image_understand` is referenced across browser.go, screen_*.go and
// skill/builtins.go (the computer-auto skill lists it in AllowedTools) but was
// never registered — this is that long-missing implementation.

func init() { tool.RegisterBuiltin(imageUnderstand{}) }

// imageUnderstand reads an image file and returns the VLM's response to a
// caller-supplied prompt. workDir, when non-empty, is the directory a relative
// path is resolved against (see resolveIn); the zero value registered at init
// resolves against the process working directory.
type imageUnderstand struct {
	workDir string
}

const (
	// imageUnderstandMaxBytes mirrors control.ImageDataURL's attachment cap
	// (attachments.go maxImageAttachmentBytes). VLM providers reject oversized
	// request bodies, and a giant image is almost always a mistake.
	imageUnderstandMaxBytes = 10 * 1024 * 1024

	// vlmLongEdge caps the long edge of an image sent to the VLM. Vision models
	// are trained at ~768-1920px; anything larger adds latency and base64 bloat
	// with no accuracy gain. Matches vlmimage_windows.go's vlmMaxDim.
	vlmLongEdge = 1920
)

func (imageUnderstand) Name() string { return "image_understand" }

func (imageUnderstand) Description() string {
	return "Understand a user-uploaded image: read an image file (the path appears in the conversation as an <image path=\"...\"> reference, e.g. .fairpeer/attachments/xxx.png) and ask the configured vision model about it. You MUST pass a `prompt` describing what you want to know — e.g. 'describe the overall layout', 'what does the error message say', 'transcribe the table as markdown'. The more specific your prompt, the more useful the answer. Returns the model's text description of the image. Use this whenever the user attached an image and you need its contents (the main model cannot see the image bytes directly)."
}

func (imageUnderstand) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "path":{
      "type":"string",
      "description":"Image file path — use the path from the <image path=\"...\"> reference in the conversation (e.g. .fairpeer/attachments/xxx.png)."
    },
    "prompt":{
      "type":"string",
      "description":"What you want to know from this image, phrased around your current task. Be specific and structured — e.g. 'describe the overall layout', 'what does the error dialog say', 'transcribe the table as markdown'. A vague prompt yields a vague answer."
    }
  },
  "required":["path","prompt"]
}`)
}

func (imageUnderstand) ReadOnly() bool { return true }

func (i imageUnderstand) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path   string `json:"path"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return "", fmt.Errorf("prompt is required — describe what you want to know from the image")
	}

	path := resolveIn(i.workDir, p.Path)

	// Reject symlinks and directories, and cap size before reading — mirrors the
	// guards in control.ImageDataURL (attachments.go) so a crafted attachment
	// can't redirect to an arbitrary file or blow memory.
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", p.Path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("image path must not be a symlink")
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not an image", p.Path)
	}
	if info.Size() <= 0 || info.Size() > imageUnderstandMaxBytes {
		return "", fmt.Errorf("image must be between 1 byte and 10 MB (got %d bytes)", info.Size())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p.Path, err)
	}

	mime := sniffImageMIME(data, p.Path)
	if mime == "" {
		return "", fmt.Errorf("%s is not a recognized image", p.Path)
	}

	dataURL := encodeForVLMGeneric(data)

	description, err := CallVLM(ctx, dataURL, p.Prompt)
	if err != nil {
		return "", fmt.Errorf("image_understand (%s): %w", filepath.Base(p.Path), err)
	}
	return description, nil
}

// sniffImageMIME reports the image MIME type of data, falling back to the file
// extension when content sniffing is inconclusive (http.DetectContentType only
// recognizes a few formats — e.g. it misdetects webp/bmp/tiff — so the
// extension map covers the rest). Returns "" for non-images.
func sniffImageMIME(data []byte, path string) string {
	peek := data
	if len(peek) > 512 {
		peek = peek[:512]
	}
	mime := http.DetectContentType(peek)
	if strings.HasPrefix(mime, "image/") {
		return mime
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	}
	return ""
}

// encodeForVLMGeneric downscales img bytes (if larger than vlmLongEdge on the
// long edge) and returns a base64 JPEG data URL. It is the cross-platform
// counterpart of vlmimage_windows.go's encodeForVLM: same long-edge cap (1920),
// same CatmullRom downscaler, same JPEG quality 85 — values tuned in
// vlmimage_windows.go to turn 30-47s VLM calls into ~8-12s without losing small
// text legibility. If the bytes cannot be decoded as a known image format, the
// original bytes are base64-encoded as-is so the VLM still receives something
// (the VLM or provider will reject it if truly unsupported, surfacing a clear
// error rather than failing opaquely here).
func encodeForVLMGeneric(data []byte) string {
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		return encodeImageToVLMDataURL(img)
	}
	// Decode failed (unknown/truncated format): fall back to raw bytes. Sniff the
	// MIME once more so the data URL prefix is honest; default to jpeg.
	mime := sniffImageMIME(data, "")
	prefix := "data:" + mime + ";base64,"
	if mime == "" {
		prefix = "data:image/jpeg;base64,"
	}
	return prefix + base64.StdEncoding.EncodeToString(data)
}

// encodeImageToVLMDataURL scales img down to vlmLongEdge on its long edge and
// JPEG-encodes it as a data URL. Mirrors vlmimage_windows.go's encodeForVLM
// logic byte-for-byte, but operates on a decoded image.Image so it is portable.
func encodeImageToVLMDataURL(img image.Image) string {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	scaled := img
	if w > vlmLongEdge || h > vlmLongEdge {
		nw, nh := w, h
		if w >= h {
			nw = vlmLongEdge
			nh = h * vlmLongEdge / w
		} else {
			nh = vlmLongEdge
			nw = w * vlmLongEdge / h
		}
		// CatmullRom preserves UI text edges far better than nearest-neighbor.
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
		scaled = dst
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: 85}); err != nil {
		// Extremely unlikely for a valid image; retry at higher quality, then give
		// up to a PNG fallback so we never return nothing.
		buf.Reset()
		if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: 90}); err != nil {
			buf.Reset()
			_ = png.Encode(&buf, scaled)
			return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
		}
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// gif/png/jpeg/webp are imported above for their decoders' side effect of
// registering with image.Decode. Keep the blank references so go vet's
// "imported and not used" doesn't fire (gif/webp are registration-only).
var (
	_ = gif.Decode
	_ = webp.Decode
)
