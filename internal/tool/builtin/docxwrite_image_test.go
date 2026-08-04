package builtin

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes returns a minimal valid PNG (1x1) for image-injection tests, so we
// don't depend on real fixture files.
func pngBytes(t *testing.T, path string) {
	t.Helper()
	// Smallest valid PNG: 8-byte signature + IHDR + IDAT + IEND.
	png := []byte{
		0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A,
		// IHDR chunk (13 data bytes): width=1 height=1 depth=8 colortype=2 ...
		0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE,
		// IDAT chunk (compressed scanline)
		0x00, 0x00, 0x00, 0x0C, 'I', 'D', 'A', 'T',
		0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x00, 0x03, 0x00, 0x01, 0x62, 0x60, 0x60, 0x60,
		// IEND
		0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xAE, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.Fatalf("write png fixture: %v", err)
	}
}

// TestWriteDOCXMultiImageUniqueIDs verifies the central multi-image fix: in a
// document with several images, each <a:blip r:embed="..."/>, <wp:docPr id>,
// and <pic:cNvPr id> must reference a distinct relationship id / drawing id,
// and the [Content_Types].xml must declare each image extension exactly once.
// Before the fix every image hardcoded rId100 + id=1 and emitted a duplicate
// <Default Extension="png"/> — Word would show the first image N times and flag
// the package as corrupt.
func TestWriteDOCXMultiImageUniqueIDs(t *testing.T) {
	dir := t.TempDir()
	img1 := filepath.Join(dir, "logo.png")
	img2 := filepath.Join(dir, "chart.png")
	img3 := filepath.Join(dir, "banner.png")
	pngBytes(t, img1)
	pngBytes(t, img2)
	pngBytes(t, img3)
	out := filepath.Join(dir, "multi.docx")

	if err := writeDOCX(DocInput{Path: out, Sections: []DocSection{
		{Type: "image", ImagePath: img1, ImageAlt: "logo"},
		{Type: "image", ImagePath: img2, ImageAlt: "chart"},
		{Type: "image", ImagePath: img3},
	}}); err != nil {
		t.Fatalf("write: %v", err)
	}

	doc := readPartShared(t, out, "word/document.xml")
	// Each blip embed must be unique: rId100, rId101, rId102.
	for _, want := range []string{`r:embed="rId100"`, `r:embed="rId101"`, `r:embed="rId102"`} {
		if c := strings.Count(doc, want); c != 1 {
			t.Errorf("expected exactly one %s, got %d", want, c)
		}
	}
	// docPr ids must be unique (1000, 1001, 1002).
	for _, want := range []string{`<wp:docPr id="1000"`, `<wp:docPr id="1001"`, `<wp:docPr id="1002"`} {
		if c := strings.Count(doc, want); c != 1 {
			t.Errorf("expected exactly one %q, got %d", want, c)
		}
	}
	// alt text rendered as descr.
	if !strings.Contains(doc, `descr="logo"`) {
		t.Errorf("image_alt 'logo' not rendered as descr attr")
	}
	if !strings.Contains(doc, `descr="chart"`) {
		t.Errorf("image_alt 'chart' not rendered as descr attr")
	}

	// Content_Types must declare png exactly once (was duplicated before).
	ct := readPartShared(t, out, "[Content_Types].xml")
	if c := strings.Count(ct, `<Default Extension="png"`); c != 1 {
		t.Errorf("Content_Types declared png %d times, want 1", c)
	}

	// All three media files must be packaged.
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	media := map[string]bool{}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") {
			media[f.Name] = true
		}
	}
	for _, want := range []string{"word/media/logo.png", "word/media/chart.png", "word/media/banner.png"} {
		if !media[want] {
			t.Errorf("missing media part %s", want)
		}
	}
}

// TestWriteDOCXImageBasenameDedup: two images with the same basename from
// different directories must both be packaged (second gets a _2 suffix), not
// silently collide in word/media/.
func TestWriteDOCXImageBasenameDedup(t *testing.T) {
	dir := t.TempDir()
	subA := filepath.Join(dir, "a")
	subB := filepath.Join(dir, "b")
	if err := os.MkdirAll(subA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(subB, 0o755); err != nil {
		t.Fatal(err)
	}
	imgA := filepath.Join(subA, "logo.png")
	imgB := filepath.Join(subB, "logo.png")
	pngBytes(t, imgA)
	pngBytes(t, imgB)
	out := filepath.Join(dir, "dup.docx")

	if err := writeDOCX(DocInput{Path: out, Sections: []DocSection{
		{Type: "image", ImagePath: imgA},
		{Type: "image", ImagePath: imgB},
	}}); err != nil {
		t.Fatalf("write: %v", err)
	}

	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	media := map[string]bool{}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") {
			media[f.Name] = true
		}
	}
	if !media["word/media/logo.png"] || !media["word/media/logo_2.png"] {
		t.Errorf("expected logo.png + logo_2.png in media, got %v", media)
	}
}

// TestWriteDOCXSVGRejected: SVG must surface an explicit error rather than emit
// an unrenderable blip (Word needs a PNG raster fallback + asvg extension that
// we don't build).
func TestWriteDOCXSVGRejected(t *testing.T) {
	dir := t.TempDir()
	svg := filepath.Join(dir, "diagram.svg")
	if err := os.WriteFile(svg, []byte("<svg></svg>"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "withsvg.docx")
	err := writeDOCX(DocInput{Path: out, Sections: []DocSection{
		{Type: "image", ImagePath: svg},
	}})
	if err == nil {
		t.Fatalf("expected SVG rejection error, got nil")
	}
	if !strings.Contains(err.Error(), "SVG") {
		t.Errorf("error should mention SVG, got: %v", err)
	}
}

// TestWriteDOCXMissingImageRejected: a non-existent image path must error
// BEFORE any zip part is written (no half-built corrupt .docx left behind).
func TestWriteDOCXMissingImageRejected(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "broken.docx")
	err := writeDOCX(DocInput{Path: out, Sections: []DocSection{
		{Type: "image", ImagePath: filepath.Join(dir, "nope.png")},
	}})
	if err == nil {
		t.Fatalf("expected missing-image error, got nil")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Errorf("a corrupt partial .docx was left on disk after the error")
	}
}

// TestWriteDOCXAppendPreservesExistingImage: the regression test for the
// append-mode data-loss bug. Before the fix, append reused static rels /
// content-types / numbering and never copied word/media/, so an appended
// chapter made the ORIGINAL image's relationship dangle (rId100 pointing at
// nothing) and the media file vanish — Word flagged the package corrupt. We
// now copy every unchanged part verbatim and continue image rIds past the
// original max. This test writes a doc with an image, appends a text chapter,
// and confirms the original image's media part AND relationship survive.
func TestWriteDOCXAppendPreservesExistingImage(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "logo.png")
	pngBytes(t, img)
	out := filepath.Join(dir, "withimg.docx")

	// First write: image + a paragraph.
	if err := writeDOCX(DocInput{Path: out, Sections: []DocSection{
		{Type: "image", ImagePath: img, ImageAlt: "logo"},
		{Type: "paragraph", Text: "original body"},
	}}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Append a new chapter (no new image).
	if err := writeDOCX(DocInput{Path: out, Append: true, Sections: []DocSection{
		{Type: "heading", Level: 1, Text: "appended chapter"},
		{Type: "paragraph", Text: "new body"},
	}}); err != nil {
		t.Fatalf("append: %v", err)
	}

	// The original media file must still be in the package.
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open zip after append: %v", err)
	}
	defer zr.Close()
	hasMedia := false
	for _, f := range zr.File {
		if f.Name == "word/media/logo.png" {
			hasMedia = true
		}
	}
	if !hasMedia {
		t.Errorf("append dropped the original word/media/logo.png (data loss)")
	}

	// The document.xml.rels must still contain the image relationship pointing
	// at the original rId100/media/logo.png. We appended text only, so no new
	// relationships were added — the original ones must be intact.
	rels := readPartShared(t, out, "word/_rels/document.xml.rels")
	if !strings.Contains(rels, `Target="media/logo.png"`) {
		t.Errorf("append dropped the image relationship; rels:\n%s", rels)
	}
	// The document body keeps both the original and appended text, and the
	// original image's rId100 reference is still valid (points at the rel).
	doc := readPartShared(t, out, "word/document.xml")
	for _, want := range []string{"original body", "appended chapter", "new body", `r:embed="rId100"`} {
		if !strings.Contains(doc, want) {
			t.Errorf("document missing %q after append", want)
		}
	}
}

// TestWriteDOCXAppendAddsNewImageWithoutColliding: appending NEW images must
// assign rIds past the original max (not reuse rId100), so the appended image
// doesn't shadow the original and both render.
func TestWriteDOCXAppendAddsNewImageWithoutColliding(t *testing.T) {
	dir := t.TempDir()
	img1 := filepath.Join(dir, "first.png")
	img2 := filepath.Join(dir, "second.png")
	pngBytes(t, img1)
	pngBytes(t, img2)
	out := filepath.Join(dir, "twoimg.docx")

	// First write: one image (gets rId100).
	if err := writeDOCX(DocInput{Path: out, Sections: []DocSection{
		{Type: "image", ImagePath: img1, ImageAlt: "first"},
	}}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Append: a second image. The original rels has rId1, rId2, rId100; the new
	// image must get rId101 (one past max=100), NOT rId100 (which would shadow
	// the first image).
	if err := writeDOCX(DocInput{Path: out, Append: true, Sections: []DocSection{
		{Type: "image", ImagePath: img2, ImageAlt: "second"},
	}}); err != nil {
		t.Fatalf("append: %v", err)
	}

	doc := readPartShared(t, out, "word/document.xml")
	// Both rId100 (original) and rId101 (appended) present and distinct.
	if c := strings.Count(doc, `r:embed="rId100"`); c != 1 {
		t.Errorf("original image rId100 count = %d, want 1", c)
	}
	if c := strings.Count(doc, `r:embed="rId101"`); c != 1 {
		t.Errorf("appended image rId101 count = %d, want 1", c)
	}
	// Both media parts present.
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	media := map[string]bool{}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") {
			media[f.Name] = true
		}
	}
	if !media["word/media/first.png"] || !media["word/media/second.png"] {
		t.Errorf("missing media after append; have %v", media)
	}
	// rels has both image targets with distinct rIds.
	rels := readPartShared(t, out, "word/_rels/document.xml.rels")
	if !strings.Contains(rels, `Id="rId100"`) || !strings.Contains(rels, `Id="rId101"`) {
		t.Errorf("rels missing rId100/rId101:\n%s", rels)
	}
}
