package netdev

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

// The observer fires after every successful save (SaveRollingFinding rides on
// SaveFinding, so it is covered), never on failure, and a panicking observer
// must not break the save path.
func TestFindingObserverFiresOnSave(t *testing.T) {
	dir := t.TempDir()
	oldDir, oldLookup := findingsDirOverr, loadConfigForProject
	defer func() {
		findingsDirOverr, loadConfigForProject = oldDir, oldLookup
		SetFindingObserver(nil)
	}()
	findingsDirOverr = dir
	loadConfigForProject = func() *config.Config { return nil }

	var mu sync.Mutex
	got := map[string]bool{}
	SetFindingObserver(func(f *Finding) {
		mu.Lock()
		got[f.Source] = true
		mu.Unlock()
	})

	f := &Finding{Title: "t", Severity: SeverityWarning, Source: "vulnscan",
		Evidence: []Evidence{{Device: "sw1", Command: "x", Output: "y"}}}
	if err := SaveFinding(f); err != nil {
		t.Fatalf("save: %v", err)
	}
	if f.ID == "" {
		t.Fatal("save did not stamp an ID")
	}

	// Rolling save (same Source) must also reach the observer.
	r := &Finding{Title: "r", Severity: SeverityInfo, Source: "cve:sweep",
		Evidence: []Evidence{{Device: "(cve-feed)", Command: "cve match", Output: "1"}}}
	if err := SaveRollingFinding(r); err != nil {
		t.Fatalf("rolling save: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := got["vulnscan"] && got["cve:sweep"]
		mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("observer did not see both saves: %v", got)
}

// A failing write (FindingsDir is a FILE, so MkdirAll fails) must not fire
// the observer.
func TestFindingObserverSilentOnFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := dir + "/blocker"
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil { // occupies the dir path
		t.Fatalf("setup: %v", err)
	}
	oldDir, oldLookup := findingsDirOverr, loadConfigForProject
	defer func() {
		findingsDirOverr, loadConfigForProject = oldDir, oldLookup
		SetFindingObserver(nil)
	}()
	findingsDirOverr = blocker
	loadConfigForProject = func() *config.Config { return nil }

	fired := false
	SetFindingObserver(func(f *Finding) { fired = true })

	f := &Finding{Title: "t", Severity: SeverityWarning,
		Evidence: []Evidence{{Device: "sw1", Command: "x", Output: "y"}}}
	if err := SaveFinding(f); err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	time.Sleep(100 * time.Millisecond)
	if fired {
		t.Fatal("observer fired for a failed save")
	}
}

// A panicking observer must never break the save path.
func TestFindingObserverPanicContained(t *testing.T) {
	dir := t.TempDir()
	oldDir, oldLookup := findingsDirOverr, loadConfigForProject
	defer func() {
		findingsDirOverr, loadConfigForProject = oldDir, oldLookup
		SetFindingObserver(nil)
	}()
	findingsDirOverr = dir
	loadConfigForProject = func() *config.Config { return nil }

	SetFindingObserver(func(f *Finding) { panic("observer bug") })

	f := &Finding{Title: "t", Severity: SeverityWarning,
		Evidence: []Evidence{{Device: "sw1", Command: "x", Output: "y"}}}
	if err := SaveFinding(f); err != nil {
		t.Fatalf("save broke under panicking observer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, f.ID+".json")); err != nil {
		t.Fatalf("finding file missing: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the recovered goroutine settle
}
