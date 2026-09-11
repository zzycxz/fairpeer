package netdev

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// 批 1.5④：轮次案例开卷——BeginCaseRun 开/续当日同入口案例，Finish 把
// 新增/仍在/已恢复 diff 钉进时间线。
func TestCaseRunScope(t *testing.T) {
	dir := t.TempDir()
	oldS := netdevStateDirOverr
	oldF := findingsDirOverr
	defer func() { netdevStateDirOverr = oldS; findingsDirOverr = oldF }()
	netdevStateDirOverr = dir
	findingsDirOverr = dir
	// casesDirOverr 不覆盖：CasesDir() = <stateDir>/cases，与 findings 子目录天然隔离。

	// Run start: one active finding already on the books.
	mustSave := func(title string) *Finding {
		f := &Finding{Title: title, Severity: SeverityWarning,
			Evidence: []Evidence{{Device: "sw1", Command: "x", Output: "y"}}}
		if err := SaveFinding(f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	f1 := mustSave("基线：旧告警")

	scope := BeginCaseRun("入口=网段 目标=10.30.2.10 收敛所在段")
	if scope == nil || scope.caseID == "" {
		t.Fatal("scope must open a case")
	}
	if scope.entry != "网段" {
		t.Fatalf("entry sniff: %q", scope.entry)
	}

	// During the run: f1 resolves, a new finding lands.
	f1.Status = "resolved"
	now := time.Now()
	f1.ResolvedAt = &now
	if err := SaveFinding(f1); err != nil {
		t.Fatal(err)
	}
	mustSave("本轮新增发现")

	scope.Finish("Top 风险：1 条……续跑指引：无")

	cases, err := ListCases()
	if err != nil || len(cases) != 1 {
		t.Fatalf("exactly one case expected: %v %d", err, len(cases))
	}
	c := cases[0]
	if !strings.Contains(c.Title, "入口=网段") {
		t.Fatalf("case title must carry the entry form: %q", c.Title)
	}
	var sawOpen, sawClose bool
	for _, e := range c.Entries {
		if strings.Contains(e.Text, "轮次开卷") {
			sawOpen = true
		}
		if strings.Contains(e.Text, "轮次收尾") {
			sawClose = true
			for _, want := range []string{"新增 1", "已恢复 1"} {
				if !strings.Contains(e.Text, want) {
					t.Errorf("close entry missing %q: %q", want, e.Text)
				}
			}
		}
	}
	if !sawOpen || !sawClose {
		t.Fatalf("case entries: open=%v close=%v (%+v)", sawOpen, sawClose, c.Entries)
	}

	// Second round same day continues the same case.
	f1.Status = "active"
	f1.ResolvedAt = nil
	_ = SaveFinding(f1)
	scope2 := BeginCaseRun("入口=网段 目标=10.30.2.11")
	if scope2.caseID != scope.caseID {
		t.Fatalf("same-day same-entry run must continue the case: %s vs %s", scope2.caseID, scope.caseID)
	}
}

// 审查补钉：「仍在」计数、不同入口各自开卷、入口嗅探取文本最早命中。
func TestCaseRunScopeCountsAndEntries(t *testing.T) {
	dir := t.TempDir()
	oldS := netdevStateDirOverr
	oldF := findingsDirOverr
	defer func() { netdevStateDirOverr = oldS; findingsDirOverr = oldF }()
	netdevStateDirOverr = dir
	findingsDirOverr = dir

	mustSave := func(title string) *Finding {
		f := &Finding{Title: title, Severity: SeverityWarning,
			Evidence: []Evidence{{Device: "sw1", Command: "x", Output: "y"}}}
		if err := SaveFinding(f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	mustSave("存量发现") // 整轮都不动 → 计「仍在」

	scope := BeginCaseRun("先 入口=网段 收敛，再 入口=清单 逐台")
	if scope.entry != "网段" {
		t.Fatalf("earliest-marker entry sniff: %q", scope.entry)
	}
	mustSave("本轮新增")
	scope.Finish("")

	cases, err := ListCases()
	if err != nil {
		t.Fatal(err)
	}
	var closeEntry string
	for _, c := range cases {
		for _, e := range c.Entries {
			if strings.Contains(e.Text, "轮次收尾") {
				closeEntry = e.Text
			}
		}
	}
	for _, want := range []string{"新增 1", "仍在 1"} {
		if !strings.Contains(closeEntry, want) {
			t.Errorf("close entry missing %q: %q", want, closeEntry)
		}
	}
	if strings.Contains(closeEntry, "已恢复") && !strings.Contains(closeEntry, "已恢复 0") {
		t.Errorf("resolved must be 0: %q", closeEntry)
	}

	// 不同入口 → 另开一册（当日同入口才续卷）。
	before := len(cases)
	scope2 := BeginCaseRun("入口=主机 目标=10.0.0.5")
	cases2, _ := ListCases()
	if len(cases2) != before+1 {
		t.Fatalf("different entry must open a NEW case: %d → %d", before, len(cases2))
	}
	if scope2.caseID == scope.caseID {
		t.Fatal("主机 entry must not continue the 网段 case")
	}
}

// 审查补钉：未标注入口、rune 截断（防 U+FFFD）。
func TestCaseRunScopeEdgeCases(t *testing.T) {
	dir := t.TempDir()
	oldS := netdevStateDirOverr
	oldF := findingsDirOverr
	defer func() { netdevStateDirOverr = oldS; findingsDirOverr = oldF }()
	netdevStateDirOverr = dir
	findingsDirOverr = dir

	if got := caseRunEntry("看一下 sw1 的状态"); got != "未标注" {
		t.Fatalf("no-marker task must be 未标注, got %q", got)
	}

	scope := BeginCaseRun("入口=清单 全网核查")
	if scope.caseID == "" {
		t.Fatal("case must open")
	}
	// 80 rune 截断：长中文摘要不产生非法 UTF-8（字节切片会把汉字切碎）。
	long := strings.Repeat("长", 200)
	scope.Finish(long)
	cases, _ := ListCases()
	for _, c := range cases {
		if c.ID != scope.caseID {
			continue
		}
		for _, e := range c.Entries {
			if strings.Contains(e.Text, "轮次收尾") {
				if !utf8.ValidString(e.Text) {
					t.Fatalf("digest must stay valid UTF-8: %q", e.Text)
				}
				runes := []rune(e.Text)
				if len(runes) > 200 {
					t.Fatalf("digest not truncated: %d runes", len(runes))
				}
			}
		}
	}
}
