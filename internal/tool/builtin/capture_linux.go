package builtin

// capture_linux.go implements CaptureFullScreen (and region capture) for Linux
// by trying multiple screenshot tools in order of preference. No CGO required.
//
// Supported tools (tried in order):
//   1. scrot            — lightweight, X11, widely available
//   2. gnome-screenshot — GNOME desktop default
//   3. spectacle        — KDE Plasma default (-b background + -o output)
//   4. maim             — modern X11 successor to scrot
//   5. grim             — Wayland compositor screenshot tool
//
// If none are available, returns an error with installation instructions.

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
)

// screenshotTool represents a Linux screenshot command and its arguments.
type screenshotTool struct {
	name string
	args []string
}

// linuxScreenshotTools is the ordered list of tools to try for a full-screen
// capture. The output file is appended after each tool's args.
var linuxScreenshotTools = []screenshotTool{
	{"scrot", []string{"-o"}},                 // -o overwrite output file
	{"gnome-screenshot", []string{"-f"}},      // -f output file
	{"spectacle", []string{"-b", "-n", "-o"}}, // KDE: -b background (no GUI), -n no notification, -o output file
	{"maim", []string{}},                      // writes the output file argument directly
	{"grim", []string{}},                      // Wayland, no extra flags
}

// CaptureFullScreen captures the full primary screen on Linux. Tries scrot,
// gnome-screenshot, spectacle, maim, and grim in order. Returns an image.RGBA
// compatible with the Windows implementation.
func CaptureFullScreen() (*image.RGBA, error) {
	// A unique per-call temp file (not a fixed /tmp name) avoids two concurrent
	// captures clobbering each other and doesn't leave a predictable shared path.
	tmp, err := os.CreateTemp("", "fairpeer-shot-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp screenshot file: %w", err)
	}
	tmpFile := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpFile)

	var lastErr error
	for _, tool := range linuxScreenshotTools {
		if _, err := exec.LookPath(tool.name); err != nil {
			continue // tool not installed, try next
		}

		args := make([]string, len(tool.args))
		copy(args, tool.args)
		args = append(args, tmpFile)

		cmd := exec.Command(tool.name, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("%s failed: %v (%s)", tool.name, err, stderr.String())
			continue // tool errored, try next
		}

		img, err := decodeScreenshotPNG(tmpFile)
		if err != nil {
			lastErr = err
			continue
		}
		return img, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("no screenshot tool worked: %w (install scrot, gnome-screenshot, spectacle, maim, or grim)", lastErr)
	}
	return nil, fmt.Errorf("no screenshot tool found (install scrot, gnome-screenshot, spectacle, maim, or grim)")
}

// captureRegion captures a sub-rectangle on Linux. Only scrot (-a X,Y,W,H) and
// grim (-g "x,y WxH") get region flags wired — the other full-screen tools
// (gnome-screenshot, spectacle, maim) don't take a non-interactive region, so
// when neither region-capable tool is installed (or both fail) the capture
// falls back to the full screen rather than erroring. The result line reports
// the actual dimensions, so the agent can tell it got more than the region.
//
// No explicit clamping: there is no cheap screen-bounds probe common to X11 and
// Wayland, scrot/grim intersect the region with the output themselves, and the
// full-screen fallback covers the fully-off-screen case.
func captureRegion(x, y, w, h int) (*image.RGBA, error) {
	tmp, err := os.CreateTemp("", "fairpeer-shot-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp screenshot file: %w", err)
	}
	tmpFile := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpFile)

	attempts := []screenshotTool{
		{"scrot", []string{"-a", fmt.Sprintf("%d,%d,%d,%d", x, y, w, h)}},
		{"grim", []string{"-g", fmt.Sprintf("%d,%d %dx%d", x, y, w, h)}},
	}
	var lastErr error
	for _, tool := range attempts {
		if _, err := exec.LookPath(tool.name); err != nil {
			continue // tool not installed, try next
		}

		args := append([]string(nil), tool.args...)
		args = append(args, tmpFile)

		cmd := exec.Command(tool.name, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("%s failed: %v (%s)", tool.name, err, stderr.String())
			continue
		}

		img, err := decodeScreenshotPNG(tmpFile)
		if err != nil {
			lastErr = err
			continue
		}
		return img, nil
	}

	// Region capture unavailable — fall back to the full-screen path (keeps
	// gnome-screenshot / spectacle / maim installs working).
	if img, err := CaptureFullScreen(); err == nil {
		return img, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("region capture failed: %w", lastErr)
	}
	return nil, fmt.Errorf("no screenshot tool with region support found (install scrot or grim)")
}

// decodeScreenshotPNG reads and decodes a screenshot tool's output file into
// an *image.RGBA, converting non-RGBA PNGs (e.g. NRGBA) so the result matches
// the Windows CaptureFullScreen signature.
func decodeScreenshotPNG(path string) (*image.RGBA, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read screenshot: %w", err)
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot PNG: %w", err)
	}

	if rgba, ok := img.(*image.RGBA); ok {
		return rgba, nil
	}
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}
	return rgba, nil
}
