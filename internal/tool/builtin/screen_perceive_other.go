//go:build !windows

package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"time"
)

// screen_perceive on macOS/Linux: screenshot → VLM → coordinates. No UIA
// needed — the VLM looks at the screenshot and identifies the target element
// directly. This is the same "screenshot-only fallback" path that Windows
// takes when UIA is unavailable, just as the primary (and only) path.
//
// The VLM returns coordinates (x, y) for screen_click to use. The agent then
// calls screen_click/screen_type with those coordinates.

type screenPerceive struct{}

func (screenPerceive) Name() string { return "screen_perceive" }

func (screenPerceive) Description() string {
	return "Take a screenshot and ask the vision model to find an element matching your task_hint. Returns the screenshot path + the VLM's analysis (which element, approximate coordinates). Then use screen_click with the coordinates. On macOS/Linux this is VLM-only (no UIA element tree); the VLM sees the raw screenshot and locates the target visually."
}

func (screenPerceive) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "task_hint": {"type": "string", "description": "What you're looking for, e.g. 'the submit button' or 'the username input field'. Helps the VLM select the right element."}
  },
  "required": ["task_hint"]
}`)
}

func (screenPerceive) ReadOnly() bool { return true }

func (screenPerceive) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		TaskHint string `json:"task_hint"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.TaskHint == "" {
		p.TaskHint = "interact with the most relevant element"
	}

	// 1. Screenshot (full screen — CaptureFullScreen exists on mac/linux).
	img, err := CaptureFullScreen()
	if err != nil {
		return "", fmt.Errorf("screenshot: %w", err)
	}
	screenW := img.Bounds().Dx()
	screenH := img.Bounds().Dy()

	// 2. Encode as PNG → base64 data URL.
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("encode screenshot: %w", err)
	}
	imgDataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	// 3. Save screenshot for the agent to reference.
	dir := screenAttachmentsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	shotPath := filepath.Join(dir, fmt.Sprintf("perceive-%d.png", time.Now().Unix()))
	if err := os.WriteFile(shotPath, buf.Bytes(), 0o644); err != nil {
		return "", err
	}

	// 4. Build VLM prompt — no UIA labels, just "look at the screenshot and find X".
	prompt := fmt.Sprintf(`You are looking at a screenshot of a computer screen (%dx%d pixels).
Task: Find "%s" on this screen.

Respond with ONLY a JSON object (no markdown, no explanation):
{
  "found": true/false,
  "element": "short description of what you found",
  "x": <pixel x coordinate, integer>,
  "y": <pixel y coordinate, integer>,
  "confidence": <0-100>,
  "note": "any useful detail for the agent"
}

If you cannot find the requested element, set "found": false and explain in "note".
Coordinates are in screen pixels (0,0 = top-left corner).`, screenW, screenH, p.TaskHint)

	// 5. Call VLM.
	vlmText, err := CallVLM(ctx, imgDataURL, prompt)
	vlmErr := ""
	if err != nil {
		vlmErr = "VLM call failed: " + err.Error()
		// Still return the screenshot so the agent can inspect manually.
		return formatPerceiveResultOther(shotPath, screenW, screenH, "", vlmErr), nil
	}

	// 6. Return: screenshot path + VLM analysis + screen dimensions.
	return formatPerceiveResultOther(shotPath, screenW, screenH, vlmText, ""), nil
}

// formatPerceiveResultOther formats the non-Windows perceive output. Simpler
// than the Windows version (no element list) — just the screenshot path,
// VLM response, and screen size so the agent knows the coordinate space.
func formatPerceiveResultOther(shotPath string, screenW, screenH int, vlmRaw, vlmErr string) string {
	var sb bytes.Buffer
	fmt.Fprintf(&sb, "Screenshot: %s\n", shotPath)
	fmt.Fprintf(&sb, "Screen size: %dx%d\n", screenW, screenH)
	if vlmErr != "" {
		fmt.Fprintf(&sb, "VLM error: %s\n", vlmErr)
		fmt.Fprintf(&sb, "(The screenshot was saved — inspect it manually and use screen_click with coordinates.)\n")
		return sb.String()
	}
	fmt.Fprintf(&sb, "VLM analysis:\n%s\n", vlmRaw)
	fmt.Fprintf(&sb, "\nUse screen_click with the coordinates above (if found). The VLM's response is guidance — verify against the screenshot.\n")
	return sb.String()
}
