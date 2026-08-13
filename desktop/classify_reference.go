package main

// classify_reference.go — VLM gatekeeper for the PPT vision pipeline.
//
// Per ppt-vision-enhancement-spec §六.五: before running the full vision flow
// (AnalyzeReferenceImage / AnalyzePDFPages), ask the VLM ONE lightweight question
// — is this reference PLAIN TEXT or VISUALLY DESIGNED? Plain text → just extract
// words as material (normal topic-driven ppt-auto); visually designed → run the
// vision flow. The VLM (not file extension / size / page-count rules) is the
// judge. This is what the user means by "一切都应该是 VLM 去判断".
//
// Why a separate lightweight call: the full 4-section extraction
// (pdfPageVLMPrompt) is expensive and only worth it when the reference actually
// carries visual design. A pure-text screenshot or a body-text PDF should NOT
// trigger layout/color/font-size extraction — it should just donate its words as
// source material. So we ask a cheap A/B question first, then route.

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zzycxz/fairpeer/internal/docconv"
	"github.com/zzycxz/fairpeer/internal/proc"
	runtimepkg "github.com/zzycxz/fairpeer/internal/runtime"
	"github.com/zzycxz/fairpeer/internal/tool/builtin"
)

// classifyVisualPrompt asks the VLM for a single A/B verdict on whether the
// reference is plain text or visually designed. Kept short on purpose — this is
// the cheap gate before the expensive 4-section extraction.
const classifyVisualPrompt = `Look at this image. Decide which category it falls into:

(A) PLAIN TEXT — only words and numbers, with NO visual styling, layout, or design. Examples: a plain text note, a body-text-only document page, a code listing, a raw data dump.
(B) VISUALLY DESIGNED — has visual design elements such as: styled/large titles, a color scheme, multi-column layout, charts/diagrams, cards/boxes, icons, decorative shapes, or a slide-like layout.

Answer with ONLY "A" or "B" on the FIRST line, then ONE short sentence explaining why (e.g. "B - has a large blue title and a 2-column card layout"). Do not output anything else.`

// ReferenceClassification is the result of the VLM gatekeeper check.
type ReferenceClassification struct {
	IsVisual bool   `json:"is_visual"` // true = B (run vision flow), false = A (plain text → extract words as material)
	Verdict  string `json:"verdict"`   // "A" or "B"
	Reason   string `json:"reason"`    // the VLM's one-line explanation
}

// ClassifyReferenceVisual asks the VLM whether filePath (image or PDF) is plain
// text or visually designed. For a PDF, page 1 is rendered to an image first via
// pdf_to_page_images.py (--max-pages 1); for an image, it's read as-is. The image
// goes to the VLM PNG-lossless. This is the gate that decides whether to run the
// full vision pipeline or just harvest text as material.
func (a *App) ClassifyReferenceVisual(filePath string) (*ReferenceClassification, error) {
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	imgPath, cleanup, err := singleImageForVLM(ctx, filePath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	imgBytes, err := os.ReadFile(imgPath)
	if err != nil {
		return nil, fmt.Errorf("read image for classify: %w", err)
	}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBytes)

	resp, err := builtin.CallVLM(ctx, dataURL, classifyVisualPrompt)
	if err != nil {
		return nil, fmt.Errorf("classify VLM: %w", err)
	}
	return parseClassification(resp), nil
}

// singleImageForVLM returns a path to a single image the VLM can look at, plus a
// cleanup func (nil-op for plain images). For PDFs it renders page 1 via
// pdf_to_page_images.py; for images it returns the file itself.
func singleImageForVLM(ctx context.Context, filePath string) (imgPath string, cleanup func(), err error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".pdf" {
		return filePath, func() {}, nil
	}

	// PDF: render page 1 only (cheap — we just need a glance to classify).
	script := docconv.FindScript("pdf_to_page_images.py")
	if script == "" {
		return "", nil, fmt.Errorf("pdf_to_page_images.py not found")
	}
	tmp, err := os.MkdirTemp("", "classify-pdf-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }

	pyCmd, pyPrefix, _ := runtimepkg.ResolvePython()
	if pyCmd == "" {
		pyCmd = "python3"
		if runtime.GOOS == "windows" {
			pyCmd = "python"
		}
	}
	args := append(append([]string{}, pyPrefix...), script, filePath, tmp, "--max-pages", "1", "--scale", "2")
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(rctx, pyCmd, args...)
	proc.HideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("render pdf page 1: %w: %s", err, stderr.String())
	}
	page1 := filepath.Join(tmp, "page-1.png")
	if _, statErr := os.Stat(page1); statErr != nil {
		cleanup()
		return "", nil, fmt.Errorf("page-1.png not produced after render")
	}
	return page1, cleanup, nil
}

// PrepareResult is what PreparePPTReference returns to its caller (the frontend /
// cowork PPT request handler): whether the reference is visual, and if the vision
// flow ran, whether it hit any per-step error (best-effort — a VLM hiccup on one
// step shouldn't sink the whole request).
type PrepareResult struct {
	IsVisual    bool   `json:"is_visual"`              // true = vision flow ran, false = plain text
	Verdict     string `json:"verdict"`                // "A" (plain) or "B" (visual)
	Reason      string `json:"reason"`                 // VLM's one-line classify reason
	PDFPages    int    `json:"pdf_pages,omitempty"`    // pages processed when input was a PDF
	VisionError string `json:"vision_error,omitempty"` // non-fatal error from the vision step, if any
}

// PreparePPTReference is the unified entry point the PPT request handler calls
// when the user gives a reference file (image or PDF) with "make a PPT" intent.
// It classifies the reference with the VLM, then routes:
//
//   - PLAIN TEXT (A) → skip the vision flow entirely. ppt-auto treats the file as
//     ordinary source material via its normal extract_content / doc_read path.
//     Returns IsVisual=false so the caller knows to run the topic-driven flow.
//
//   - VISUALLY DESIGNED (B) → run the vision flow so ppt-auto can redraw a similar
//     slide: image → AnalyzeReferenceImage (writes reference-style.json);
//     PDF → AnalyzePDFPages (writes page-N.json).
//
// Vision-step errors are recorded in VisionError, not returned as a fatal error —
// the caller can still proceed (degrade to topic-driven) if the vision step hiccups.
func (a *App) PreparePPTReference(filePath string) (*PrepareResult, error) {
	cls, err := a.ClassifyReferenceVisual(filePath)
	if err != nil {
		return nil, err
	}
	result := &PrepareResult{
		IsVisual: cls.IsVisual,
		Verdict:  cls.Verdict,
		Reason:   cls.Reason,
	}
	if !cls.IsVisual {
		// Plain text: no vision flow. ppt-auto handles the file as source material.
		return result, nil
	}

	// Visually designed → run the vision flow for this file type.
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".pdf" {
		n, verr := a.AnalyzePDFPages(filePath)
		result.PDFPages = n
		if verr != nil {
			result.VisionError = verr.Error()
		}
	} else {
		if verr := a.AnalyzeReferenceImage(filePath); verr != nil {
			result.VisionError = verr.Error()
		}
	}
	return result, nil
}

// parseClassification pulls the A/B verdict out of the VLM response. Tolerant of
// leading whitespace / a leading code fence; defaults to "plain text" (A, the
// cheaper path) if it can't find a clear A/B so we never accidentally run the
// expensive vision flow on an ambiguous answer.
func parseClassification(resp string) *ReferenceClassification {
	for _, raw := range strings.Split(strings.TrimSpace(resp), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "`"))
		if line == "" {
			continue
		}
		first := strings.ToUpper(line[:1])
		if first == "A" || first == "B" {
			// Rest of this line (or the next) is the reason.
			reason := strings.TrimSpace(strings.TrimPrefix(line, first))
			reason = strings.TrimLeft(reason, " -—:.)")
			if reason == "" {
				reason = strings.TrimSpace(resp)
			}
			return &ReferenceClassification{
				IsVisual: first == "B",
				Verdict:  first,
				Reason:   reason,
			}
		}
	}
	// Ambiguous — default to plain text (A) so we don't burn the vision pipeline
	// on an uncertain answer.
	return &ReferenceClassification{IsVisual: false, Verdict: "A?", Reason: strings.TrimSpace(resp)}
}

// pptIntentKeywords marks user input as PPT-making intent. Kept broad on purpose:
// the VLM classify step (PreparePPTReference → ClassifyReferenceVisual) is the
// real gatekeeper that decides vision-flow vs plain-text, so a loose keyword net
// here just avoids burning a VLM call on every screenshot a user pastes for
// non-PPT reasons.
var pptIntentKeywords = []string{"ppt", "幻灯", "演示文稿", "slides", "slide"}

func hasPPTIntent(lowered string) bool {
	for _, kw := range pptIntentKeywords {
		if strings.Contains(lowered, kw) {
			return true
		}
	}
	return false
}

// referenceAttachmentExts are attachment extensions that can be a visual reference
// for PPT generation (images + PDF).
var referenceAttachmentExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true,
	".pdf": true,
}

// pptReferenceAttachment scans input for an @.fairpeer/attachments/<file> token
// whose extension is an image/PDF, AND whose text shows PPT intent. Returns the
// attachment path (relative, without the leading @) and true when both match —
// the signal that PreparePPTReference should run before the message reaches the
// model, so reference-style.json / page-N.json is ready when ppt-auto reads it.
func pptReferenceAttachment(input string) (string, bool) {
	if !hasPPTIntent(strings.ToLower(input)) {
		return "", false
	}
	const prefix = "@.fairpeer/attachments/"
	search := input
	for {
		idx := strings.Index(search, prefix)
		if idx < 0 {
			return "", false
		}
		rest := search[idx+len(prefix):]
		end := len(rest)
		for i, r := range rest {
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '@' || r == ',' || r == '，' {
				end = i
				break
			}
		}
		name := rest[:end]
		search = rest[end:] // advance for the next iteration
		if name == "" {
			continue
		}
		if referenceAttachmentExts[strings.ToLower(filepath.Ext(name))] {
			return ".fairpeer/attachments/" + name, true
		}
	}
}
