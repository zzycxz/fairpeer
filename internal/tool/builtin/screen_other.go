//go:build !windows

package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/zzycxz/fairpeer/internal/tool"
)

// ScreenTools returns the desktop-automation tools available on macOS/Linux.
//
// All tools work cross-platform:
//   - screen_click/type/scroll/key: via cliclick (macOS) / xdotool (Linux)
//   - screenshot: full-screen or region capture (screencapture on macOS;
//     scrot/gnome-screenshot/spectacle/maim/grim on Linux — see
//     capture_darwin.go / capture_linux.go)
//   - screen_perceive: VLM-only (screenshot → vision model → coordinates, no UIA)
//   - get_ui_tree: window-level tree (uitree_other.go) — same tool name and
//     result shape as the Windows getUITreeEnhanced, minus child controls
//
// The screenshot tool here is the same name, schema, and result shape as the
// Windows implementation (screen_windows.go), including the optional region
// {x,y,w,h} argument.
func ScreenTools() []tool.Tool {
	tools := baseScreenTools()
	tools = append(tools,
		screenCapture{},
		screenPerceive{},
		getUITreeLite{},
	)
	return tools
}

// --- screenshot -------------------------------------------------------------

// screenCapture is the non-Windows screenshot tool. Same tool name, schema,
// and result text as the Windows screenCapture so the agent sees one tool
// across platforms; capture goes through CaptureFullScreen/captureRegion
// (screencapture on macOS, scrot/gnome-screenshot/spectacle/maim/grim on
// Linux).
type screenCapture struct{}

func (screenCapture) Name() string { return "screenshot" }

func (screenCapture) Description() string {
	return "Capture the current screen (or a region) as a PNG and return its file path plus a base64 thumbnail. The image is ready to pass to image_understand for visual analysis, or use the path as an attachment. This is the primary perception channel for desktop automation — take a screenshot, have image_understand describe what's on screen, decide the next action. Optional region {x,y,w,h} captures a sub-rectangle; default is the full primary screen."
}

func (screenCapture) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "region":{"type":"object","description":"Optional sub-rectangle to capture. Omit for the full screen.","properties":{"x":{"type":"integer"},"y":{"type":"integer"},"w":{"type":"integer"},"h":{"type":"integer"}}}
},
"required":[]
}`)
}

func (screenCapture) ReadOnly() bool { return true }

func (screenCapture) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Region *struct {
			X int `json:"x"`
			Y int `json:"y"`
			W int `json:"w"`
			H int `json:"h"`
		} `json:"region"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	var img *image.RGBA
	var err error
	if p.Region != nil {
		r := p.Region
		if r.W <= 0 || r.H <= 0 {
			return "", fmt.Errorf("invalid region {x:%d, y:%d, w:%d, h:%d}: w and h must be positive", r.X, r.Y, r.W, r.H)
		}
		img, err = captureRegion(r.X, r.Y, r.W, r.H)
	} else {
		img, err = CaptureFullScreen()
	}
	if err != nil {
		return "", err
	}
	dir := screenAttachmentsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create attachments dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("screen-%d.png", time.Now().Unix()))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("encode png: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write screenshot: %w", err)
	}
	thumb := base64.StdEncoding.EncodeToString(buf.Bytes())
	if len(thumb) > 4096 {
		thumb = thumb[:4096] + "…"
	}
	// Emit slash-separated paths so the result matches the `.fairpeer/attachments/…`
	// convention the attachment pipeline and frontend key on (same as Windows).
	return fmt.Sprintf("screenshot saved: %s (%dx%d)\nbase64 (first 4k): %s", filepath.ToSlash(path), img.Bounds().Dx(), img.Bounds().Dy(), thumb), nil
}
