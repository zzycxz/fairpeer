package builtin

// capture_darwin.go implements CaptureFullScreen (and region capture) for
// macOS using the built-in screencapture command. No CGO required — shelling
// out to the CLI tool that ships with every macOS install.
//
// screencapture flags:
//   -x  no sound
//   -C  include cursor
//   -R<x,y,w,h>  capture a rectangle (in logical points, not pixels)

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"os/exec"
)

// CaptureFullScreen captures the full primary screen on macOS via the built-in
// screencapture command. Returns an image.RGBA compatible with the Windows
// implementation so the screenshot hotkey pipeline works identically.
func CaptureFullScreen() (*image.RGBA, error) {
	// Write to a temp file — screencapture doesn't support stdout on macOS.
	// A unique per-call temp file (not a fixed /tmp name) avoids two concurrent
	// captures clobbering each other and doesn't leave a predictable shared path.
	tmp, err := os.CreateTemp("", "fairpeer-shot-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp screenshot file: %w", err)
	}
	tmpFile := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpFile)

	cmd := exec.Command("screencapture", "-x", "-C", tmpFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture failed: %v (%s)", err, stderr.String())
	}

	return decodeScreenshotPNG(tmpFile)
}

// captureRegion captures a sub-rectangle on macOS via `screencapture -R`.
// The region arrives in physical screenshot pixels — the coordinate space the
// agent sees in returned images and VLM analyses (same convention as the
// Windows region tool) — and is converted to the logical points -R expects
// using the Retina scale probe (input_other.go). The converted rect is clamped
// to the main display's logical bounds; if that probe failed, the rect is used
// as-is (screencapture intersects off-screen rects with the display itself).
func captureRegion(x, y, w, h int) (*image.RGBA, error) {
	scale, screenW, screenH, ok := macScreenMetrics()
	fx, fy := float64(x), float64(y)
	fw, fh := float64(w), float64(h)
	if ok && scale > 1 {
		fx, fy, fw, fh = fx/scale, fy/scale, fw/scale, fh/scale
	}
	if ok {
		// Clamp to the display's logical bounds (0,0 screenW x screenH).
		x0, y0 := math.Max(0, fx), math.Max(0, fy)
		x1 := math.Min(float64(screenW), fx+fw)
		y1 := math.Min(float64(screenH), fy+fh)
		if x1 <= x0 || y1 <= y0 {
			return nil, fmt.Errorf("region {x:%d, y:%d, w:%d, h:%d} lies outside the screen bounds (0,0 %dx%d in logical points)", x, y, w, h, screenW, screenH)
		}
		fx, fy, fw, fh = x0, y0, x1-x0, y1-y0
	}

	tmp, err := os.CreateTemp("", "fairpeer-shot-*.png")
	if err != nil {
		return nil, fmt.Errorf("create temp screenshot file: %w", err)
	}
	tmpFile := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpFile)

	rect := fmt.Sprintf("-R%d,%d,%d,%d",
		int(math.Round(fx)), int(math.Round(fy)), int(math.Round(fw)), int(math.Round(fh)))
	cmd := exec.Command("screencapture", "-x", "-C", rect, tmpFile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture %s failed: %v (%s)", rect, err, stderr.String())
	}

	return decodeScreenshotPNG(tmpFile)
}

// decodeScreenshotPNG reads and decodes a screencapture output file into an
// *image.RGBA, converting non-RGBA PNGs (e.g. NRGBA) so the result matches the
// Windows CaptureFullScreen signature.
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
