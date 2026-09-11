package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/zzycxz/fairpeer/internal/tool"
)

// Bug 3 regression: apply_patch and move_file are multi-path file writers whose
// zero-value init registrations carry roots==nil and therefore bypassed the
// workspace boundary on both assembly paths until they were bound. These tests
// prove the bound instances refuse escapes, and the structural test below keeps
// any future roots-bearing writer from silently re-adding the hole.

func confinedTool(t *testing.T, name, root string) tool.Tool {
	t.Helper()
	for _, tl := range ConfineWriters([]string{root}) {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("%s missing from ConfineWriters", name)
	return nil
}

func TestApplyPatchConfinement(t *testing.T) {
	root := t.TempDir()
	ap := confinedTool(t, "apply_patch", root)

	out := filepath.ToSlash(filepath.Join(t.TempDir(), "escape.txt"))
	patch := "*** Begin Patch\n*** Add File: " + out + "\n+evil\n*** End Patch\n"
	args, _ := json.Marshal(map[string]string{"patchText": patch})
	if _, err := ap.Execute(context.Background(), args); err == nil {
		t.Error("apply_patch outside root should error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("escaping apply_patch must not create the file")
	}

	in := filepath.ToSlash(filepath.Join(root, "ok.txt"))
	patch = "*** Begin Patch\n*** Add File: " + in + "\n+ok\n*** End Patch\n"
	args, _ = json.Marshal(map[string]string{"patchText": patch})
	if _, err := ap.Execute(context.Background(), args); err != nil {
		t.Fatalf("apply_patch inside root failed: %v", err)
	}
}

func TestMoveFileConfinement(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := confinedTool(t, "move_file", root)

	out := filepath.Join(t.TempDir(), "moved.txt")
	args, _ := json.Marshal(map[string]string{"source_path": src, "destination_path": out})
	if _, err := mf.Execute(context.Background(), args); err == nil {
		t.Error("move_file to outside root should error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("escaping move must not create the destination")
	}
	if _, err := os.Stat(src); err != nil {
		t.Error("source must survive a refused move")
	}

	// A move fully inside the root still works.
	inside := filepath.Join(root, "dst.txt")
	args, _ = json.Marshal(map[string]string{"source_path": src, "destination_path": inside})
	if _, err := mf.Execute(context.Background(), args); err != nil {
		t.Fatalf("move inside root failed: %v", err)
	}
}

// TestAllRootsBearingWritersAreConfined is the structural guard: every
// registered writer whose concrete type carries a `roots` field participates in
// the confinement contract, so it must come back from ConfineWriters and
// Workspace.Tools with non-empty roots. A new writer added without either
// binding keeps its roots==nil zero value and escapes the workspace boundary —
// exactly how apply_patch/move_file slipped through.
func TestAllRootsBearingWritersAreConfined(t *testing.T) {
	root := t.TempDir()

	boundRoots := func(ts []tool.Tool) map[string]int {
		out := map[string]int{}
		for _, tl := range ts {
			v := reflect.ValueOf(tl)
			for v.Kind() == reflect.Pointer {
				v = v.Elem()
			}
			if v.Kind() != reflect.Struct {
				continue
			}
			f := v.FieldByName("roots")
			if !f.IsValid() {
				continue
			}
			out[tl.Name()] = f.Len()
		}
		return out
	}

	confined := boundRoots(ConfineWriters([]string{root}))
	wsTools := boundRoots(Workspace{Dir: root}.Tools())

	// DocumentTools()/RAGTools() writers are runtime-assembled (not
	// init-builtins), so tool.Builtins() never lists them — cover them
	// explicitly (NEW-32: the structural test was blind to exactly these).
	extra := DocumentTools([]string{root})
	for _, tl := range extra {
		if tl.ReadOnly() {
			continue
		}
		name := tl.Name()
		if n := confined[name]; n == 0 {
			t.Errorf("runtime-assembled writer %q missing from ConfineWriters", name)
		}
	}

	for _, tl := range tool.Builtins() {
		if tl.ReadOnly() {
			continue
		}
		v := reflect.ValueOf(tl)
		for v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			continue
		}
		if _, ok := v.Type().FieldByName("roots"); !ok {
			continue
		}
		name := tl.Name()
		if n := confined[name]; n == 0 {
			t.Errorf("writer %q carries a roots field but ConfineWriters returns no bound copy — its init-registered zero value bypasses the workspace boundary", name)
		}
		if n := wsTools[name]; n == 0 {
			t.Errorf("writer %q carries a roots field but Workspace.Tools returns no bound copy — the desktop path uses the unconfined zero value", name)
		}
	}
}
