package main

// Tests for the multi-shot screenshot stitching used by the silent debounce
// burst mode (see screenshot_solve.go / docs/SCREENSHOT_MULTI_SPEC.md).

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func solidPNGDataURL(t *testing.T, w, h int, c color.RGBA) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode fixture png: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func decodeDataURL(t *testing.T, url string) image.Image {
	t.Helper()
	idx := strings.Index(url, ",")
	if idx == -1 {
		t.Fatal("stitched result is not a data URL")
	}
	data, err := base64.StdEncoding.DecodeString(url[idx+1:])
	if err != nil {
		t.Fatalf("decode stitched result: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode stitched image: %v", err)
	}
	return img
}

func pixelIs(img image.Image, x, y int, want color.RGBA) bool {
	r, g, b, a := img.At(x, y).RGBA()
	return uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(b>>8) == want.B && uint8(a>>8) == want.A
}

func TestStitchImagesVerticallyOrderSizeAndBackdrop(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	urls := []string{
		solidPNGDataURL(t, 20, 10, red),
		solidPNGDataURL(t, 10, 30, blue),
	}

	out, err := stitchImagesVertically(urls)
	if err != nil {
		t.Fatalf("stitch: %v", err)
	}
	img := decodeDataURL(t, out)
	if got := img.Bounds(); got.Dx() != 20 || got.Dy() != 40 {
		t.Fatalf("stitched bounds = %v, want 20x40", got)
	}
	// Top half red, bottom half blue, narrow-image padding white.
	if !pixelIs(img, 5, 5, red) {
		t.Error("pixel in first image region is not red")
	}
	if !pixelIs(img, 5, 35, blue) {
		t.Error("pixel in second image region is not blue")
	}
	if !pixelIs(img, 15, 35, color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Error("padding right of narrower image is not white")
	}
}

func TestStitchImagesVerticallySinglePassthrough(t *testing.T) {
	url := solidPNGDataURL(t, 4, 4, color.RGBA{G: 255, A: 255})
	out, err := stitchImagesVertically([]string{url})
	if err != nil {
		t.Fatalf("stitch single: %v", err)
	}
	if out != url {
		t.Error("single image should pass through untouched")
	}
}

func TestStitchImagesVerticallyDownscalesOversize(t *testing.T) {
	// 10x5000 + 10x5000 → 10000px tall, above maxStitchDim: must come back
	// downscaled to fit, preserving both halves.
	urls := []string{
		solidPNGDataURL(t, 10, 5000, color.RGBA{R: 255, A: 255}),
		solidPNGDataURL(t, 10, 5000, color.RGBA{B: 255, A: 255}),
	}
	out, err := stitchImagesVertically(urls)
	if err != nil {
		t.Fatalf("stitch oversize: %v", err)
	}
	img := decodeDataURL(t, out)
	b := img.Bounds()
	if b.Dy() > maxStitchDim || b.Dx() > maxStitchDim {
		t.Fatalf("stitched bounds %v exceed maxStitchDim=%d", b, maxStitchDim)
	}
	if b.Dy() != maxStitchDim {
		t.Fatalf("downscale should saturate height to %d, got %d", maxStitchDim, b.Dy())
	}
	if !pixelIs(img, 5, 10, color.RGBA{R: 255, A: 255}) {
		t.Error("top downscaled half is not red")
	}
	if !pixelIs(img, 5, b.Dy()-10, color.RGBA{B: 255, A: 255}) {
		t.Error("bottom downscaled half is not blue")
	}
}

func TestStitchImagesVerticallyCorruptInputFailsLoudly(t *testing.T) {
	urls := []string{
		solidPNGDataURL(t, 8, 8, color.RGBA{R: 255, A: 255}),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("definitely not a png")),
	}
	if _, err := stitchImagesVertically(urls); err == nil {
		t.Fatal("corrupt input must fail the whole stitch so the caller falls back to multi-image send")
	}
}

func TestStitchImagesVerticallyEmpty(t *testing.T) {
	if _, err := stitchImagesVertically(nil); err == nil {
		t.Fatal("empty input must error")
	}
}
