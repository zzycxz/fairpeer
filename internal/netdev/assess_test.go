package netdev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
)

func assessTestManager(t *testing.T, sim *simDevice) *Manager {
	t.Helper()
	m, _ := testManager(t, sim)
	m.cfg.NetDev.Assessment = config.NetDevAssessment{
		EngagementID: "ASSESS-TEST-1",
		Scopes:       []string{"127.0.0.0/8"},
		Expires:      time.Now().AddDate(0, 0, 7).Format("2006-01-02"),
		Approver:     "tester",
	}
	return m
}

func TestAssessmentGate(t *testing.T) {
	if err := AssessmentActive(config.NetDevConfig{}); err == nil {
		t.Fatal("no envelope accepted")
	}
	expired := config.NetDevConfig{Assessment: config.NetDevAssessment{
		EngagementID: "X", Expires: "2020-01-01"}}
	if err := AssessmentActive(expired); err == nil || err.Error() == "" {
		t.Fatal("expired accepted")
	}
	ok := config.NetDevConfig{Assessment: config.NetDevAssessment{
		EngagementID: "X", Expires: time.Now().AddDate(0, 0, 1).Format("2006-01-02")}}
	if err := AssessmentActive(ok); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
}

func TestWeakCredBasicNoFalsePositive(t *testing.T) {
	sim := startSimDevice(t) // real password "pw" — NOT in the basic set
	m := assessTestManager(t, sim)

	res, err := m.WeakCredCheck(context.Background(), "sw1", WeakTierBasic, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Weak {
		t.Fatal("false positive: basic tier confirmed a non-basic password")
	}
	if res.Attempts > weakBudgetBasic {
		t.Fatalf("budget exceeded: %d", res.Attempts)
	}
}

func TestWeakCredDictionaryFindsWeak(t *testing.T) {
	sim := startSimDevice(t)
	m := assessTestManager(t, sim)

	dict := filepath.Join(t.TempDir(), "dict.txt")
	if err := os.WriteFile(dict, []byte("firstguess\nsecondguess\npw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := m.WeakCredCheck(context.Background(), "sw1", WeakTierDict, dict)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Weak || res.Attempts != 3 {
		t.Fatalf("res = %+v, want weak on attempt 3", res)
	}
}

func TestWeakCredRequiresEngagement(t *testing.T) {
	sim := startSimDevice(t)
	m, _ := testManager(t, sim) // no assessment envelope
	if _, err := m.WeakCredCheck(context.Background(), "sw1", WeakTierBasic, ""); err == nil {
		t.Fatal("weak-cred without engagement accepted")
	}
}

func TestWeakCredDictBudgetCap(t *testing.T) {
	sim := startSimDevice(t)
	m := assessTestManager(t, sim)
	dict := filepath.Join(t.TempDir(), "big.txt")
	var body string
	for i := 0; i < 30; i++ {
		body += "guess" + string(rune('a'+i)) + "\n"
	}
	if err := os.WriteFile(dict, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := m.WeakCredCheck(context.Background(), "sw1", WeakTierDict, dict)
	if err != nil {
		t.Fatal(err)
	}
	if res.Attempts > weakBudgetDictMax {
		t.Fatalf("hard cap exceeded: %d attempts", res.Attempts)
	}
}

// The agent tool surface (netdev_assess) refuses without an engagement
// envelope BEFORE any dial, and the refusal lands as a VISIBLE live event —
// the 操作实况 panel must show the agent being stopped.
func TestAssessToolRefusesWithoutEnvelope(t *testing.T) {
	sim := startSimDevice(t)
	m, _ := testManager(t, sim) // no assessment envelope

	col := &liveCollector{}
	m.SetLiveObserver(col.on)

	tool := &assessTool{m: m}
	out, err := tool.Execute(context.Background(), []byte(`{"device":"sw1"}`))
	if err == nil {
		t.Fatalf("tool must refuse without an envelope; got output %q", out)
	}
	if !strings.Contains(err.Error(), "engagement") {
		t.Fatalf("tool error = %v, want engagement-envelope refusal", err)
	}
	col.waitKind(t, LiveCmdRefused, 1)

	// With the envelope, the tool runs the sealed path end to end and
	// brackets it with live lifecycle events.
	m.cfg.NetDev.Assessment = config.NetDevAssessment{
		EngagementID: "ASSESS-TOOL-TEST",
		Expires:      time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
		Approver:     "tester",
	}
	out, err = tool.Execute(context.Background(), []byte(`{"device":"sw1","tier":"basic"}`))
	if err != nil {
		t.Fatalf("tool with envelope: %v", err)
	}
	if !strings.Contains(out, "no weak credential") {
		t.Fatalf("tool output = %q", out)
	}
	col.waitKind(t, LiveCmdStart, 1)
	col.waitKind(t, LiveCmdEnd, 1)
}

// P2-3 补件：确认的弱口令必须立案（Source 含档位），复查通过同档自动恢复、
// 异档不得误恢复。
func TestWeakCredFindingLifecycle(t *testing.T) {
	findingsDirOverr = filepath.Join(t.TempDir(), "findings")
	t.Cleanup(func() { findingsDirOverr = "" })

	m := &Manager{}
	res := WeakCredResult{Device: "sw9", Tier: "basic", Weak: true, Attempts: 2, Budget: 3,
		Detail: "weak credential confirmed after 2 attempt(s) — change it via a proposal"}
	m.fileWeakCredFinding(res)
	m.fileWeakCredFinding(res) // re-confirm updates the same alert, no pile-up
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Source != "assess:weak-cred:basic:sw9" || fs[0].Status != "active" {
		t.Fatalf("file/dedup = %+v", fs)
	}

	// A dictionary-tier pass must NOT resolve the basic-tier alert.
	m.resolveWeakCredFinding("sw9", "dictionary")
	fs, _ = ListFindings()
	if fs[0].Status != "active" {
		t.Fatal("cross-tier pass resolved the alert")
	}
	// Same-tier pass resolves it.
	m.resolveWeakCredFinding("sw9", "basic")
	fs, _ = ListFindings()
	if fs[0].Status != "resolved" {
		t.Fatal("same-tier pass did not resolve the alert")
	}
}

// An unreachable device must NOT auto-resolve a CONFIRMED weak-cred finding:
// dialAuth used to fold transport failures into (false, nil) — the same shape
// as a clean rejection — so a full-budget "pass" that never reached the device
// cleared the alert (复核通过 lied). The verdict is unknown; the finding stays.
func TestWeakCredUnreachableDoesNotResolveFinding(t *testing.T) {
	findingsDirOverr = filepath.Join(t.TempDir(), "findings")
	t.Cleanup(func() { findingsDirOverr = "" })

	cfg := config.Default()
	cfg.NetDev = config.NetDevConfig{
		Enabled: true,
		Assessment: config.NetDevAssessment{
			EngagementID: "ASSESS-UNREACH-1",
			Scopes:       []string{"127.0.0.0/8"},
			Expires:      time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
			Approver:     "tester",
		},
		Devices: []config.NetDevDevice{{
			Name: "dead9", Vendor: "linux", Address: "127.0.0.1", Port: 1, Username: "root", PasswordEnv: "TEST_ENV",
		}},
	}
	m := NewManager(cfg)
	t.Cleanup(m.Close)

	// A previously CONFIRMED basic-tier alert for that device.
	m.fileWeakCredFinding(WeakCredResult{Device: "dead9", Tier: WeakTierBasic, Weak: true, Attempts: 1, Budget: weakBudgetBasic, Detail: "weak credential confirmed"})

	res, err := m.WeakCredCheck(context.Background(), "dead9", WeakTierBasic, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Weak {
		t.Fatal("unreachable device reported a weak credential")
	}
	if !strings.Contains(res.Detail, "unreachable") || !strings.Contains(res.Detail, "unknown") {
		t.Fatalf("detail = %q — must state the verdict is unknown", res.Detail)
	}
	fs, _ := ListFindings()
	if len(fs) != 1 || fs[0].Status != "active" {
		t.Fatalf("finding auto-resolved by an unreachable re-check: %+v", fs)
	}
}
