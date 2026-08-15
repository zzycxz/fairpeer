package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPPTReferenceAttachment verifies the SubmitToTab trigger condition: an
// attachment is only routed to PreparePPTReference when BOTH (a) it's an
// image/PDF attachment token AND (b) the message shows PPT intent. This guards
// against two failure modes: burning a VLM call on every pasted screenshot
// (no PPT intent), and missing a real reference (intent present, attachment
// missed by the parser).
func TestPPTReferenceAttachment(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantPath string
		wantOK   bool
	}{
		{"image + ppt intent", "照这张图做个PPT @.fairpeer/attachments/shot.png", ".fairpeer/attachments/shot.png", true},
		{"pdf + ppt intent (演示文稿)", "把这个PDF转成演示文稿 @.fairpeer/attachments/doc.pdf", ".fairpeer/attachments/doc.pdf", true},
		{"slide keyword (english)", "make slides @.fairpeer/attachments/x.jpg", ".fairpeer/attachments/x.jpg", true},
		{"幻灯 keyword", "做几页幻灯 @.fairpeer/attachments/a.webp", ".fairpeer/attachments/a.webp", true},
		{"ppt intent but no attachment", "做个PPT关于AI发展", "", false},
		{"attachment but NO ppt intent → must skip (avoid VLM burn)", "看这张图里写了啥 @.fairpeer/attachments/shot.png", "", false},
		{"attachment but non-image/pdf ext → skip", "做个PPT @.fairpeer/attachments/notes.txt", "", false},
		{"multiple attachments, pick the image one", "做PPT @.fairpeer/attachments/a.txt 和 @.fairpeer/attachments/b.png", ".fairpeer/attachments/b.png", true},
		{"chinese comma delimiter after token", "做PPT@.fairpeer/attachments/x.png，谢谢", ".fairpeer/attachments/x.png", true},
		{"token at end of input (no trailing space)", "做个PPT 照这张 @.fairpeer/attachments/end.png", ".fairpeer/attachments/end.png", true},
		{"empty input", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := pptReferenceAttachment(c.input)
			if got != c.wantPath || ok != c.wantOK {
				t.Errorf("pptReferenceAttachment(%q) = (%q, %v), want (%q, %v)",
					c.input, got, ok, c.wantPath, c.wantOK)
			}
		})
	}
}

// TestHasPPTIntent sanity-checks the intent keyword net — broad enough to catch
// real PPT requests, doesn't match ordinary text.
func TestHasPPTIntent(t *testing.T) {
	yes := []string{"做个ppt", "PPT", "幻灯片", "演示文稿", "make slides", "slide deck", "PPTX"}
	for _, s := range yes {
		if !hasPPTIntent(lowerForTest(s)) {
			t.Errorf("hasPPTIntent(%q) = false, want true", s)
		}
	}
	no := []string{"写个文档", "翻译这段", "总结一下", "看这张图", "做个表格"}
	for _, s := range no {
		if hasPPTIntent(lowerForTest(s)) {
			t.Errorf("hasPPTIntent(%q) = true, want false", s)
		}
	}
}

// lowerForTest mirrors what pptReferenceAttachment does (strings.ToLower on the
// whole input) so the test exercises the same normalization.
func lowerForTest(s string) string {
	// strings.ToLower is what the real code uses; replicate without re-importing
	// strings here to keep the test focused (hasPPTIntent takes a pre-lowered str).
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// TestLocalPathReference verifies the second reference form: an ABSOLUTE local
// path pasted as text (e.g. 把 C:\Users\me\Desktop\shot.png 转成PPT). Guards three
// failure modes: existing paths must route (previously this form bypassed the
// gate entirely), non-existent paths must surface as `missing` (so the caller
// warns instead of silently dropping the reference), and URLs / relative paths
// must NOT match (false positives would burn VLM calls on ordinary messages).
func TestLocalPathReference(t *testing.T) {
	tmp := t.TempDir()
	existing := tmp + string(filepath.Separator) + "shot.png"
	if err := os.WriteFile(existing, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pdf := tmp + string(filepath.Separator) + "deck.pdf"
	if err := os.WriteFile(pdf, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		input       string
		wantFound   string
		wantMissing string
		wantOK      bool
	}{
		{"existing png path", "把 " + existing + " 转成PPT", existing, "", true},
		{"existing pdf path, quoted", `转成幻灯 "` + pdf + `" 谢谢`, pdf, "", true},
		{"missing path → missing, not ok", "把 " + tmp + string(filepath.Separator) + "nope.png 做成PPT", "", tmp + string(filepath.Separator) + "nope.png", false},
		{"URL with image ext → skip (no :// tokens)", "看这个 https://example.com/a.png 做PPT", "", "", false},
		{"relative path → skip", "把 shot.png 做成PPT", "", "", false},
		{"bare extension word → skip", "做个 shots.png 主题的PPT", "", "", false},
		{"no path at all", "做个PPT关于AI", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, miss, ok := localPathReference(c.input)
			if got != c.wantFound || miss != c.wantMissing || ok != c.wantOK {
				t.Errorf("localPathReference(%q) = (%q, %q, %v), want (%q, %q, %v)",
					c.input, got, miss, ok, c.wantFound, c.wantMissing, c.wantOK)
			}
		})
	}
}

// TestClearStaleReferenceFilesIn verifies the existence invariant's core: both
// leftovers from a previous task (reference-style.json + pdf-pages/) are
// removed, and clearing an already-clean home is a harmless no-op.
func TestClearStaleReferenceFilesIn(t *testing.T) {
	home := t.TempDir()
	fp := filepath.Join(home, ".fairpeer", "reference-style.json")
	pd := filepath.Join(home, ".fairpeer", "pdf-pages", "page-1.json")
	if err := os.MkdirAll(filepath.Dir(pd), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{fp, pd} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clearStaleReferenceFilesIn(home)
	for _, p := range []string{fp, pd} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists after clear (err=%v)", p, err)
		}
	}
	// Idempotent: a second clear on the clean home must not error or panic.
	clearStaleReferenceFilesIn(home)
}
