package netdev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Case lifecycle: save → list (newest first) → report content → bundle file.
func TestCaseLifecycleAndBundle(t *testing.T) {
	old := casesDirOverr
	oldState := netdevStateDirOverr
	defer func() { casesDirOverr = old; netdevStateDirOverr = oldState }()
	dir := t.TempDir()
	casesDirOverr = dir
	netdevStateDirOverr = dir

	c := &IncidentCase{
		Title:   "支付服务入侵排查",
		Devices: []string{"vm-1"},
		IOCs:    []CaseIOC{{Value: "10.6.6.6", Type: "ip"}},
		Entries: []CaseEntry{{Time: time.Now(), Kind: "finding", Device: "vm-1", Text: "新增可疑账户"}},
	}
	if err := SaveCase(c); err != nil {
		t.Fatal(err)
	}
	if c.ID == "" {
		t.Fatal("id must be generated")
	}
	list, err := ListCases()
	if err != nil || len(list) != 1 || list[0].Title != c.Title {
		t.Fatalf("list: %v %+v", err, list)
	}
	rep := CaseReport(c)
	if !strings.Contains(rep, "10.6.6.6") || !strings.Contains(rep, "新增可疑账户") {
		t.Fatalf("report content: %s", rep)
	}
	// empty-title refuses
	if err := SaveCase(&IncidentCase{}); err == nil {
		t.Fatal("empty title must refuse")
	}
	// bundle writes a file with report + sections
	path, err := CaseBundle(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "支付服务入侵排查") {
		t.Fatalf("bundle: %v %s", err, path)
	}
	if !strings.HasPrefix(filepath.Base(path), "case-"+c.ID) {
		t.Fatalf("bundle name: %s", path)
	}
	// delete round-trip
	if err := DeleteCase(c.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = ListCases(); len(list) != 0 {
		t.Fatalf("delete: %+v", list)
	}
}
