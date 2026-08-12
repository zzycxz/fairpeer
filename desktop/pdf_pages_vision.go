package main

// pdf_pages_vision.go — render a PDF's pages to images (via pdf_to_page_images.py)
// and ask the VLM to describe each page's CONTENT + LAYOUT + FORMAT + DESIGN, so
// ppt-auto can redraw a similar deck page-by-page.
//
// This is the "PDF (esp. scanned / image-style) → ppt-auto" bridge — the missing
// piece for "give a PDF, redraw each page as a slide". It implements exactly what
// the user described: split the PDF into one image per page, recognize each page
// with the VLM (not just text — also format, layout, design), and hand the
// per-page descriptions to ppt-auto to redraw.
//
// Pipeline:
//   pdf_to_page_images.py (fitz) renders each page → page-N.png
//   → each PNG sent to builtin.CallVLM with a 4-section prompt
//   → ~/.fairpeer/pdf-pages/page-N.json (one per page)
//   → ppt-auto reads these and redraws each page
//
// Design notes:
//   - VLM (not OCR) is the recognizer, so layout/format/design survive, not just
//     text. OCR already exists (ocr_pdf.py) but only yields flat text.
//   - PNG bytes are sent as-is (data:image/png, no JPEG re-encode, no downscale):
//     pdf_to_page_images.py already renders at scale=2, and PNG preserves the
//     sharp text/solid-color regions that JPEG smears — same lesson as the
//     image_understand format-preservation fix.
//   - Mirrors ppt_template_vision.go's CallVLM + write-JSON pattern; the only new
//     piece is per-page rendering via the python script.
//   - Per-page VLM failures are recorded in that page's JSON (error field) rather
//     than aborting the whole PDF — one bad page shouldn't sink the rest.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

// pdfPageResult is one page's VLM description, written to page-{N}.json.
type pdfPageResult struct {
	Page       int    `json:"page"`                  // 1-based page number
	Image      string `json:"image"`                 // rendered PNG filename (page-N.png)
	TotalPages int    `json:"total_pages,omitempty"` // total in the source PDF
	Desc       string `json:"description,omitempty"` // VLM's 4-section markdown
	Error      string `json:"error,omitempty"`       // per-page VLM/read error (other pages still process)
}

// AnalyzePDFPages is the desktop entry point: render each page of pdfPath to a
// PNG (via pdf_to_page_images.py) and ask the VLM to describe it, writing one
// JSON per page to ~/.fairpeer/pdf-pages/page-{N}.json. Returns the number of
// pages processed. Intended to run async (call in a goroutine, like
// analyzeTemplateStyleAsync); per-page failures land in the JSON, not as errors.
func (a *App) AnalyzePDFPages(pdfPath string) (int, error) {
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	return analyzePDFPages(ctx, pdfPath)
}

func analyzePDFPages(ctx context.Context, pdfPath string) (int, error) {
	// 1. Locate the renderer script — same search path as ocr_pdf.py (both sit at
	//    the project root and ship together).
	script := docconv.FindScript("pdf_to_page_images.py")
	if script == "" {
		return 0, fmt.Errorf("pdf_to_page_images.py not found")
	}

	home, _ := os.UserHomeDir()
	outDir := filepath.Join(home, ".fairpeer", "pdf-pages")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, fmt.Errorf("create pdf-pages dir: %w", err)
	}

	// 2. Render pages via the python script (fitz). ResolvePython prefers a bundled
	//    interpreter if present (uv); fall back to python3 / python. 10-min cap
	//    mirrors rag's PDF OCR path.
	pyCmd, pyPrefix, _ := runtimepkg.ResolvePython()
	if pyCmd == "" {
		pyCmd = "python3"
		if runtime.GOOS == "windows" {
			pyCmd = "python"
		}
	}
	args := append(append([]string{}, pyPrefix...), script, pdfPath, outDir, "--scale", "2")
	rctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(rctx, pyCmd, args...)
	proc.HideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("render pdf pages: %w: %s", err, stderr.String())
	}

	// 3. Parse the LAST stdout line as JSON {"pages": [...], "total_pages": N}.
	pages, total, err := parseRenderOutput(stdout.Bytes())
	if err != nil {
		return 0, fmt.Errorf("parse render output: %w (stdout=%q)", err, strings.TrimSpace(stdout.String()))
	}

	// 4. Send each page PNG to the VLM and write one JSON per page. Per-page
	//    isolation: a VLM error on page 3 still yields page-4.json.
	prompt := pdfPageVLMPrompt
	for i, pagePath := range pages {
		result := pdfPageResult{Page: i + 1, Image: filepath.Base(pagePath), TotalPages: total}
		if imgBytes, rerr := os.ReadFile(pagePath); rerr == nil {
			// PNG bytes as-is — no JPEG re-encode, no downscale (scale=2 already
			// applied at render time; PNG keeps text/edges sharp for the VLM).
			dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imgBytes)
			if resp, verr := builtin.CallVLM(ctx, dataURL, prompt); verr == nil {
				result.Desc = resp
			} else {
				result.Error = verr.Error()
			}
		} else {
			result.Error = fmt.Sprintf("read png: %v", rerr)
		}
		data, _ := jsonMarshal(result)
		_ = os.WriteFile(filepath.Join(outDir, fmt.Sprintf("page-%d.json", i+1)), data, 0o644)
	}
	return len(pages), nil
}

// parseRenderOutput extracts the JSON object from the last non-empty stdout line.
// pdf_to_page_images.py prints progress to stderr and the result JSON as the final
// stdout line, so we scan from the end for a line starting with '{'.
func parseRenderOutput(stdout []byte) (pages []string, total int, err error) {
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var res struct {
			Pages []string `json:"pages"`
			Total int      `json:"total_pages"`
			Err   string   `json:"error"`
		}
		if jerr := json.Unmarshal([]byte(line), &res); jerr != nil {
			continue
		}
		if res.Err != "" {
			return nil, 0, fmt.Errorf("%s", res.Err)
		}
		return res.Pages, res.Total, nil
	}
	return nil, 0, fmt.Errorf("no JSON result line in render output")
}

// pdfPageVLMPrompt asks the VLM for a 4-section description of ONE PDF page,
// structured so ppt-auto can redraw a similar slide. Text is verbatim (precise);
// layout/format/design are approximate (VLM can't see exact pixels) — which
// matches "redraw a similar page", not "pixel-perfect clone".
const pdfPageVLMPrompt = `You are analyzing ONE page of a document to help recreate it as an editable PowerPoint slide. Describe it in exactly 4 markdown sections:
## 1 CONTENT
Transcribe ALL text verbatim — title, headings, body paragraphs, every bullet point, captions, data labels, table cells. Do NOT summarize; write the exact text on the page.
## 2 LAYOUT
The spatial arrangement: where the title sits (top-center / top-left / etc.), the body region, single vs multi-column, paragraphs vs bullet list, any table (give its rows and columns), image regions (position: top-left / center / right / full-bleed, and rough size share of the page).
## 3 FORMAT
Relative font sizing (which text is largest, what looks like body size relative to it), weight cues (bold title?), bullet style (• / - / 1.), text alignment per block (left / center / right).
## 4 DESIGN
Background (light / dark, hue name), accent colors (hue names, e.g. "deep blue", "orange accent"), overall style (formal / casual / minimal / illustrated / corporate), any logo or decorative elements.

Text (section 1) must be exact. Positions and colors (sections 2-4) can be approximate — you cannot see exact pixels. Output ONLY the 4 sections, no preamble, no closing remarks.`
