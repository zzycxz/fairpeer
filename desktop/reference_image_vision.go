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
// Output: ~/.fairpeer/reference-style.json = {image, description, + structured
// color fields}. ppt-auto reads `description` for layout/content/font-ratio
// guidance; merge_vlm_style.py reads the structured color fields (background/
// accent_colors/is_dark/text_color) to recolor the deck to match the reference.

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// referenceImageMaxBytes caps the image size sent to the VLM. VLM providers reject
// oversized request bodies, and a giant image is almost always a mistake (an
// uncropped full-page export, a raw photo). 10 MiB matches image_understand's cap.
const referenceImageMaxBytes = 10 * 1024 * 1024

// readImageForVLM reads an image file and enforces the VLM size cap. Shared by
// the visual-analysis path (analyzeReferenceImage) and the plain-text OCR path
// (extractImageText) so both get the same stability guard — a too-large image
// returns an error instead of being base64-encoded into a request the VLM rejects.
func readImageForVLM(imgPath string) ([]byte, error) {
	b, err := os.ReadFile(imgPath)
	if err != nil {
		return nil, err
	}
	if len(b) > referenceImageMaxBytes {
		return nil, fmt.Errorf("image too large: %d bytes (max %d) — downscale or crop before using as reference", len(b), referenceImageMaxBytes)
	}
	return b, nil
}

// referenceColorPrompt asks the VLM for the reference image's color scheme as a
// structured JSON object. The shape matches templateStyleResult so
// parseVLMStyleResponse (shared with ppt_template_vision) parses it, and
// merge_vlm_style.py's _apply_vlm_style picks up background/accent_colors/
// is_dark/text_color directly — that's how the reference's palette actually
// reaches the generated PPT.
//
// Distinct from vlmColorPrompt (ppt_template_vision.go): that one assumes a
// template background photo and forces background_type="image"; a reference page
// may have a flat solid background, so here background_type is the VLM's call.
const referenceColorPrompt = `Analyze this image's color scheme. Respond with ONLY a JSON object (no markdown, no explanation), in this exact format:
{"background":"#RRGGBB","is_dark":false,"accent_colors":["#RRGGBB","#RRGGBB"],"text_color":"#RRGGBB","style_keywords":["keyword1","keyword2"],"background_type":"solid"}
Rules:
- background: the dominant background color (sample the largest area)
- is_dark: true if the background is dark (brightness < 50%), false if light
- accent_colors: 2-4 non-background colors that stand out (titles, accents, charts)
- text_color: the color text should be for readability on this background (#FFFFFF if dark, #1A1A1A if light)
- style_keywords: 2-3 style descriptors in Chinese (e.g. "商务简约","科技感","活泼")
- background_type: "solid" if a flat color background, "image" if a photo/textured background`

// referenceStyleResult is written to ~/.fairpeer/reference-style.json.
//
// Two kinds of info ride in here:
//   - Description: the VLM's 4-section markdown (content/layout/format/design) —
//     ppt-auto Step 3 reads this for layout/content/font-ratio guidance.
//   - Color fields (background/accent_colors/is_dark/text_color/...): structured
//     hex from a 2nd VLM call — merge_vlm_style.py reads THESE (not Description)
//     and merges them into template_config.json.colors, so the deck's palette
//     matches the reference. Without these hex fields the merge skips the
//     reference and the deck falls back to template/default colors.
type referenceStyleResult struct {
	Image          string   `json:"image"`                     // source image filename
	Description    string   `json:"description,omitempty"`     // VLM 4-section markdown
	Background     string   `json:"background,omitempty"`      // #RRGGBB — merge_vlm_style reads this
	IsDark         bool     `json:"is_dark,omitempty"`         // background brightness < 50%
	AccentColors   []string `json:"accent_colors,omitempty"`   // top accent hexes
	TextColor      string   `json:"text_color,omitempty"`      // recommended text color
	StyleKeywords  []string `json:"style_keywords,omitempty"`  // style descriptors
	BackgroundType string   `json:"background_type,omitempty"` // "solid" | "image"
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
	pptVisionDebugLog("analyzeReferenceImage start: imgPath=%s", imgPath)
	imgBytes, err := readImageForVLM(imgPath)
	if err != nil {
		pptVisionDebugLog("analyzeReferenceImage readImageForVLM err: %v", err)
		return fmt.Errorf("read reference image: %w", err)
	}
	pptVisionDebugLog("analyzeReferenceImage img read OK: size=%d", len(imgBytes))
	// PNG-lossless data URL — no JPEG re-encode, no downscale. Keeps text/edges/
	// solid-color blocks sharp for the VLM (same lesson as image_understand's
	// format-preservation fix: JPEG q85 smears exactly what OCR/layout/color
	// recognition need). The image was already sized by whoever produced it
	// (screenshot/reference), so no rescale here either.
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBytes)

	// Call 1: 4-section description (content/layout/format/design) — ppt-auto
	// Step 3 reads Description for layout/content/font-ratio guidance.
	desc, err := callVLMWithTimeout(ctx, dataURL, pdfPageVLMPrompt)
	if err != nil {
		pptVisionDebugLog("analyzeReferenceImage describe CallVLM FAILED: %v", err)
		return fmt.Errorf("reference image VLM (describe): %w", err)
	}
	pptVisionDebugLog("analyzeReferenceImage describe OK (len=%d)", len(desc))

	result := referenceStyleResult{
		Image:       filepath.Base(imgPath),
		Description: desc,
	}

	// Call 2: structured colors (background/accent/text/is_dark). These hex fields
	// are what merge_vlm_style.py reads to recolor the deck — without them the
	// reference's palette never reaches the generated PPT. Best-effort: a color-call
	// failure leaves Description intact; merge just skips colors (template fallback).
	if colorResp, cerr := callVLMWithTimeout(ctx, dataURL, referenceColorPrompt); cerr == nil {
		if cs := parseVLMStyleResponse(colorResp); cs != nil {
			result.Background = cs.Background
			result.IsDark = cs.IsDark
			result.AccentColors = cs.AccentColors
			result.TextColor = cs.TextColor
			result.StyleKeywords = cs.StyleKeywords
			result.BackgroundType = cs.BackgroundType
		}
	}

	home, _ := os.UserHomeDir()
	outDir := filepath.Join(home, ".fairpeer")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("ensure ~/.fairpeer: %w", err)
	}
	data, _ := jsonMarshal(result)
	refPath := filepath.Join(outDir, "reference-style.json")
	writeErr := os.WriteFile(refPath, data, 0o644)
	pptVisionDebugLog("analyzeReferenceImage write reference-style.json: path=%s err=%v", refPath, writeErr)
	return writeErr
}

// ocrTextPrompt transcribes the words in a plain-text image. Used when
// ClassifyReferenceVisual judged the image PLAIN TEXT (A): such an image carries
// no visual design worth replicating, but its words are still useful source
// material — and ppt-auto can't read image bytes directly (refs.go leaves only a
// text <image path> placeholder), so we OCR the words into reference-style.json
// for ppt-auto Step 3 to pick up. Output is the raw transcribed text only.
const ocrTextPrompt = `Transcribe ALL text visible in this image, verbatim. Preserve line breaks and the natural reading order (top-to-bottom, left-to-right). Output ONLY the transcribed text — no headings, no commentary, no markdown formatting. If the image contains no text at all, output exactly: (no text)`

// extractImageText OCRs a plain-text image and writes its words to
// ~/.fairpeer/reference-style.json (Description field), so ppt-auto can read the
// content. This is the PLAIN-TEXT branch of PreparePPTReference for images.
// (Plain-text PDFs are NOT handled here — refs.go already extracts their text
// when the @PDF reference is resolved, so ppt-auto receives the words directly.)
func (a *App) extractImageText(imgPath string) error {
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	imgBytes, err := readImageForVLM(imgPath)
	if err != nil {
		return fmt.Errorf("read image for OCR: %w", err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBytes)
	text, err := callVLMWithTimeout(ctx, dataURL, ocrTextPrompt)
	if err != nil {
		return fmt.Errorf("image OCR: %w", err)
	}
	result := referenceStyleResult{
		Image:       filepath.Base(imgPath),
		Description: "Plain-text reference (no visual design worth replicating). Transcribed content below — use as source material:\n\n" + text,
	}
	home, _ := os.UserHomeDir()
	outDir := filepath.Join(home, ".fairpeer")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("ensure ~/.fairpeer: %w", err)
	}
	data, _ := jsonMarshal(result)
	return os.WriteFile(filepath.Join(outDir, "reference-style.json"), data, 0o644)
}
