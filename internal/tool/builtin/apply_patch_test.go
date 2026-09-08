package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePatch_AddFile(t *testing.T) {
	patch := `*** Begin Patch
*** Add File: hello.go
+package main
+
+func main() {
+	fmt.Println("hello")
+}
*** End Patch`
	hunks, err := parsePatch(patch)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if hunks[0].typ != hunkAdd {
		t.Fatalf("expected add, got %d", hunks[0].typ)
	}
	if hunks[0].path != "hello.go" {
		t.Fatalf("path=%q, want hello.go", hunks[0].path)
	}
	if !strings.Contains(hunks[0].contents, "package main") {
		t.Fatal("contents missing package main")
	}
}

func TestParsePatch_UpdateFile(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: main.go
@@ func main
 func main() {
-	fmt.Println("old")
+	fmt.Println("new")
 }
*** End Patch`
	hunks, err := parsePatch(patch)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if hunks[0].typ != hunkUpdate {
		t.Fatalf("expected update, got %d", hunks[0].typ)
	}
	if len(hunks[0].chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(hunks[0].chunks))
	}
	chunk := hunks[0].chunks[0]
	if len(chunk.oldLines) != 3 {
		t.Fatalf("expected 3 old lines, got %d", len(chunk.oldLines))
	}
	if len(chunk.newLines) != 3 {
		t.Fatalf("expected 3 new lines, got %d", len(chunk.newLines))
	}
	if chunk.oldLines[1] != "\tfmt.Println(\"old\")" {
		t.Fatalf("old line 1=%q", chunk.oldLines[1])
	}
	if chunk.newLines[1] != "\tfmt.Println(\"new\")" {
		t.Fatalf("new line 1=%q", chunk.newLines[1])
	}
}

func TestParsePatch_DeleteFile(t *testing.T) {
	patch := `*** Begin Patch
*** Delete File: old.go
*** End Patch`
	hunks, err := parsePatch(patch)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if hunks[0].typ != hunkDelete {
		t.Fatalf("expected delete, got %d", hunks[0].typ)
	}
}

func TestParsePatch_MultipleFiles(t *testing.T) {
	patch := `*** Begin Patch
*** Add File: a.go
+package a
*** Update File: b.go
@@
-old
+new
*** Delete File: c.go
*** End Patch`
	hunks, err := parsePatch(patch)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(hunks) != 3 {
		t.Fatalf("expected 3 hunks, got %d", len(hunks))
	}
	if hunks[0].typ != hunkAdd || hunks[1].typ != hunkUpdate || hunks[2].typ != hunkDelete {
		t.Fatalf("wrong types: %d %d %d", hunks[0].typ, hunks[1].typ, hunks[2].typ)
	}
}

func TestParsePatch_EmptyPatch(t *testing.T) {
	patch := `*** Begin Patch
*** End Patch`
	_, err := parsePatch(patch)
	if err == nil {
		t.Fatal("expected error for empty patch")
	}
}

func TestParsePatch_MissingMarkers(t *testing.T) {
	_, err := parsePatch("no markers here")
	if err == nil {
		t.Fatal("expected error for missing markers")
	}
}

func TestDeriveNewContent_SimpleReplace(t *testing.T) {
	original := "line1\nline2\nline3\n"
	chunks := []updateChunk{
		{oldLines: []string{"line2"}, newLines: []string{"LINE2"}},
	}
	result, err := deriveNewContent(original, chunks)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(result, "LINE2") {
		t.Fatalf("result missing LINE2: %q", result)
	}
	if strings.Contains(result, "line2") {
		t.Fatalf("result still has line2: %q", result)
	}
}

func TestDeriveNewContent_AddLines(t *testing.T) {
	original := "line1\nline3\n"
	chunks := []updateChunk{
		{oldLines: []string{"line3"}, newLines: []string{"line2", "line3"}},
	}
	result, err := deriveNewContent(original, chunks)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(result, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
}

func TestDeriveNewContent_RemoveLines(t *testing.T) {
	original := "line1\nline2\nline3\n"
	chunks := []updateChunk{
		{oldLines: []string{"line1", "line2"}, newLines: []string{"line1"}},
	}
	result, err := deriveNewContent(original, chunks)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(result, "line2") {
		t.Fatalf("result still has line2: %q", result)
	}
}

// TestDeriveNewContent_PreservesCRLFEndings pins the A3 fix: a CRLF source
// edited by an LF patch (the common case — patches are copied from LF diffs)
// must come out fully CRLF, not mixed. Before the fix, the TrimSpace fallback
// matched stripped lines and the patched line leaked a bare \n, flipping the
// whole file in git.
func TestDeriveNewContent_PreservesCRLFEndings(t *testing.T) {
	original := "line1\r\nline2\r\nline3\r\n"
	chunks := []updateChunk{
		{oldLines: []string{"line2"}, newLines: []string{"LINE2"}},
	}
	result, err := deriveNewContent(original, chunks)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := "line1\r\nLINE2\r\nline3\r\n"
	if result != want {
		t.Fatalf("CRLF not preserved:\nwant %q\n got %q", want, result)
	}
	// No bare \n should remain once CRLF pairs are removed.
	if strings.Contains(strings.ReplaceAll(result, "\r\n", ""), "\n") {
		t.Fatalf("a bare \\n leaked into a CRLF file: %q", result)
	}
}

// TestDeriveNewContent_PreservesLFEndings is the LF regression guard: the
// line-ending fix must not change behaviour for plain-LF sources.
func TestDeriveNewContent_PreservesLFEndings(t *testing.T) {
	original := "line1\nline2\nline3\n"
	chunks := []updateChunk{
		{oldLines: []string{"line2"}, newLines: []string{"LINE2"}},
	}
	result, err := deriveNewContent(original, chunks)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := "line1\nLINE2\nline3\n"
	if result != want {
		t.Fatalf("LF changed unexpectedly:\nwant %q\n got %q", want, result)
	}
}

func TestSeekSequence_Exact(t *testing.T) {
	lines := []string{"a", "b", "c", "d"}
	pattern := []string{"b", "c"}
	idx := seekSequence(lines, pattern, 0)
	if idx != 1 {
		t.Fatalf("expected 1, got %d", idx)
	}
}

func TestSeekSequence_Trimmed(t *testing.T) {
	lines := []string{"  a  ", "b", "c"}
	pattern := []string{"a", "b"}
	idx := seekSequence(lines, pattern, 0)
	if idx != 0 {
		t.Fatalf("expected 0, got %d", idx)
	}
}

func TestSeekSequence_NotFound(t *testing.T) {
	lines := []string{"a", "b"}
	pattern := []string{"x"}
	idx := seekSequence(lines, pattern, 0)
	if idx != -1 {
		t.Fatalf("expected -1, got %d", idx)
	}
}

func TestApplyPatch_Integration(t *testing.T) {
	// Create a temp directory with a file.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	original := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply a patch that changes "hello" to "world" and adds a new file.
	patch := `*** Begin Patch
*** Update File: ` + filePath + `
@@ func main
 func main() {
-	fmt.Println("hello")
+	fmt.Println("world")
 }
*** Add File: ` + filepath.Join(dir, "helper.go") + `
+package main
+
+func helper() {}
*** End Patch`

	a := applyPatch{workDir: dir}
	result, err := a.Execute(context.TODO(), mustJSON(patch))
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(result, "2 files changed") {
		t.Fatalf("result=%q", result)
	}

	// Verify main.go was updated.
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "world") {
		t.Fatalf("main.go not updated: %s", content)
	}

	// Verify helper.go was created.
	helperPath := filepath.Join(dir, "helper.go")
	helperContent, err := os.ReadFile(helperPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(helperContent), "func helper()") {
		t.Fatalf("helper.go wrong content: %s", helperContent)
	}
}

func TestApplyPatch_DeleteIntegration(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "delete_me.go")
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := `*** Begin Patch
*** Delete File: ` + filePath + `
*** End Patch`

	a := applyPatch{workDir: dir}
	result, err := a.Execute(context.TODO(), mustJSON(patch))
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(result, "1 files changed") {
		t.Fatalf("result=%q", result)
	}

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatal("file should have been deleted")
	}
}

func TestApplyPatch_EmptyPatchText(t *testing.T) {
	a := applyPatch{}
	_, err := a.Execute(context.TODO(), mustJSON(""))
	if err == nil {
		t.Fatal("expected error for empty patchText")
	}
}

// TestApplyPatch_UpdateMoveToSamePathRejected guards the audit case: an Update
// File with a Move to the SAME path used to write the new content to movePath
// and then os.Remove the (identical) source — deleting what was just written
// while reporting success. It must now be rejected in validation, in both
// Execute and PreviewFiles, with the file left untouched.
func TestApplyPatch_UpdateMoveToSamePathRejected(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "same.txt")
	original := "line1\nline2\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := `*** Begin Patch
*** Update File: ` + filePath + `
*** Move to: ` + filePath + `
@@
 line1
-line2
+line2 changed
*** End Patch`

	a := applyPatch{workDir: dir}
	if _, err := a.Execute(context.TODO(), mustJSON(patch)); err == nil {
		t.Fatal("update+move to the same path must be rejected")
	} else if !strings.Contains(err.Error(), "move to itself") {
		t.Fatalf("error should explain the self-move, got: %v", err)
	}
	if _, err := a.PreviewFiles(mustJSON(patch)); err == nil {
		t.Fatal("PreviewFiles must reject a self-move too")
	}

	// The file must be untouched (the old flow reported success AND deleted it).
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Fatalf("file must be unchanged, got: %q", content)
	}
}

// TestApplyPatch_RollbackRestoresPreexistingMoveDest guards the rollback path:
// when a move lands on a PRE-EXISTING destination and a later change fails,
// rollback must restore the destination's original content — not os.Remove it
// (which destroyed a file the patch never owned).
func TestApplyPatch_RollbackRestoresPreexistingMoveDest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.go")
	dst := filepath.Join(dir, "dst.go")
	victim := filepath.Join(dir, "victim.go")
	srcBody := "package main\n\nfunc src() {}\n"
	dstBody := "package main\n\nfunc destOriginal() {}\n"
	victimBody := "package main\n\nfunc victim() {\n\tok()\n}\n"
	for p, body := range map[string]string{src: srcBody, dst: dstBody, victim: victimBody} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	patch := `*** Begin Patch
*** Update File: ` + src + `
*** Move to: ` + dst + `
@@
-func src() {}
+func src() { renamed() }
*** Update File: ` + victim + `
@@
-func victim() {
+func victim() {
 	ok()
-}
+this is not valid go ]]
*** End Patch`

	a := applyPatch{workDir: dir}
	_, err := a.Execute(context.TODO(), mustJSON(patch))
	if err == nil {
		t.Fatal("patch with a syntax-broken final hunk must fail")
	}

	// src must be restored (move rolled back).
	content, rerr := os.ReadFile(src)
	if rerr != nil {
		t.Fatalf("src must exist after rollback: %v", rerr)
	}
	if !strings.Contains(string(content), "func src()") {
		t.Fatalf("src not restored: %q", content)
	}
	// dst must still hold ITS original content — not be deleted, not hold the
	// moved content.
	content, rerr = os.ReadFile(dst)
	if rerr != nil {
		t.Fatalf("pre-existing move destination must survive rollback: %v", rerr)
	}
	if string(content) != dstBody {
		t.Fatalf("destination must be restored to its original content, got: %q", content)
	}
}

// TestApplyPatch_RollbackRestoresOverwrittenAdd guards TOOL-4: an add that
// OVERWRITES a pre-existing file, followed by a failing later change, must
// roll back to the ORIGINAL content — the old rollback os.Remove'd the file,
// destroying data the patch never owned (the move-destination rule, applied
// to adds).
func TestApplyPatch_RollbackRestoresOverwrittenAdd(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.go")
	original := "package main\n\nfunc userOriginal() {}\n"
	if err := os.WriteFile(existing, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "victim.go")
	if err := os.WriteFile(victim, []byte("package main\n\nfunc victim() {\n\tok()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patch := `*** Begin Patch
*** Add File: ` + existing + `
+package main
+
+func replaced() {}
*** Update File: ` + victim + `
@@
-func victim() {
+func victim() {
 	ok()
-}
+this is not valid go ]]
*** End Patch`

	a := applyPatch{workDir: dir}
	_, err := a.Execute(context.TODO(), mustJSON(patch))
	if err == nil {
		t.Fatal("patch with a syntax-broken final hunk must fail")
	}

	content, rerr := os.ReadFile(existing)
	if rerr != nil {
		t.Fatalf("overwritten file must survive rollback: %v", rerr)
	}
	if string(content) != original {
		t.Fatalf("original content must be restored, got: %q", content)
	}
}

func mustJSON(patchText string) []byte {
	b, _ := json.Marshal(map[string]string{"patchText": patchText})
	return b
}
