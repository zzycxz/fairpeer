package main

// reference_image_vision.go — Phase 2 of ppt-vision-enhancement-spec.
//
// Analyze a single reference image (typically a one-page PPT screenshot the user
// gave) with the VLM and write ~/.fairpeer/reference-style.json. ppt-auto reads
// this in Step 0 to draw a similar slide — text content verbatim, layout+density,
// font-size ratios, and color/style cues all come from the VLM's description.
//
// This is the "image → VLM → PPT" core link. It does NOT use image_understand
// (that tool is main-session-only by design); instead it calls builtin.CallVLM
// directly from the desktop layer, mirroring ppt_template_vision.go. The same
// pdfPageVLMPrompt (4 sections: CONTENT/LAYOUT/FORMAT/DESIGN) is reused from
// pdf_pages_vision.go — a reference image and a PDF page are both "describe this
// one image in 4 sections" to the VLM.
//
// Output: ~/.fairpeer/reference-style.json = {image, description}. ppt-auto reads
// `description` for layout/content/font-ratio guidance. Colors in the description
// are hue-name level (DESIGN section, e.g. "deep blue background"); structured
// hex colors for mechanical merge still come from ppt-template-style.json (Phase 1's
// merge_vlm_style.py). Lifting reference-image colors into structured hex is a
// follow-up (a 2nd CallVLM with vlmColorPrompt) — not in Phase 2's MVP.

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zzycxz/fairpeer/internal/tool/builtin"
)

// referenceStyleResult is written to ~/.fairpeer/reference-style.json.
type referenceStyleResult struct {
	Image       string `json:"image"`                 // source image filename
	Description string `json:"description,omitempty"` // VLM 4-section markdown (content/layout/format/design)
}

// AnalyzeReferenceImage is the desktop entry point: read one image, ask the VLM
// for its 4-section description (pdfPageVLMPrompt), write ~/.fairpeer/reference-style.json.
// Intended to run when the user gives a reference image for PPT generation; ppt-auto
// Step 0 then reads it. Failures return an error (the caller can degrade to the
// normal topic-driven flow if the reference image can't be analyzed).
func (a *App) AnalyzeReferenceImage(imgPath string) error {
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	return analyzeReferenceImage(ctx, imgPath)
}

func analyzeReferenceImage(ctx context.Context, imgPath string) error {
	imgBytes, err := os.ReadFile(imgPath)
	if err != nil {
		return fmt.Errorf("read reference image: %w", err)
	}
	// PNG-lossless data URL — no JPEG re-encode, no downscale. Keeps text/edges/
	// solid-color blocks sharp for the VLM (same lesson as image_understand's
	// format-preservation fix: JPEG q85 smears exactly what OCR/layout/color
	// recognition need). The image was already sized by whoever produced it
	// (screenshot/reference), so no rescale here either.
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBytes)

	// One CallVLM with the shared 4-section prompt. Description carries text
	// (verbatim) + layout+density + font-size ratios + design/color cues.
	desc, err := builtin.CallVLM(ctx, dataURL, pdfPageVLMPrompt)
	if err != nil {
		return fmt.Errorf("reference image VLM: %w", err)
	}

	result := referenceStyleResult{
		Image:       filepath.Base(imgPath),
		Description: desc,
	}
	home, _ := os.UserHomeDir()
	outDir := filepath.Join(home, ".fairpeer")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("ensure ~/.fairpeer: %w", err)
	}
	data, _ := jsonMarshal(result)
	return os.WriteFile(filepath.Join(outDir, "reference-style.json"), data, 0o644)
}
