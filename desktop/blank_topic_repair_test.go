package main

// Tests for blank_topic_repair.go: restored blank project tabs get a topic,
// topic-less sessions are adopted into the project tree (reusing a bound
// tab's topic), and subagent/bot/expert sessions pass through untouched.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/agent"
	"github.com/zzycxz/fairpeer/internal/config"
)

// writeOrphanSession writes a turns>0 session with scope/root/profile but NO
// topic — the on-disk shape saveTabSessionMeta produced for blank-topic tabs.
func writeOrphanSession(t *testing.T, dir, name, workspaceRoot, profile string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	body := `{"role":"user","content":"帮我查查呼池的告警"}` + "\n" +
		`{"role":"assistant","content":"已定位告警"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := agent.BranchMeta{
		CreatedAt:     time.Now().Add(-time.Hour),
		UpdatedAt:     time.Now(),
		Scope:         "project",
		WorkspaceRoot: workspaceRoot,
		Profile:       profile,
		CachedTurns:   1,
		CachedPreview: "帮我查查呼池的告警",
	}
	if err := agent.SaveBranchMetaPreserveUpdated(path, meta); err != nil {
		t.Fatalf("save orphan meta: %v", err)
	}
	return path
}

func netdevHomeTopicIDs(t *testing.T) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, p := range loadProjectsFile(config.ProfileNetDev).Projects {
		if normalizeProjectRoot(p.Root) == normalizeProjectRoot(profileHomeRoot(config.ProfileNetDev)) {
			for _, id := range p.Topics {
				ids[id] = true
			}
		}
	}
	return ids
}

// The user-reported case end to end: a restored blank-topic netdev home tab
// pins a topic-less session — repair assigns the tab a topic, adopts the
// session onto it, and the topic becomes findable/listable.
func TestRepairBlankTopicTabAdoptsBoundSession(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDirFor(config.ProfileNetDev)
	orphan := writeOrphanSession(t, dir, "20260904-083332-319b.jsonl", profileHomeRoot(config.ProfileNetDev), config.ProfileNetDev)

	app := NewApp()
	tab := &WorkspaceTab{
		ID:            "tab_blank",
		Scope:         "project",
		WorkspaceRoot: profileHomeRoot(config.ProfileNetDev),
		SessionPath:   orphan,
		profile:       config.ProfileNetDev,
	}
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.activeTabID = tab.ID
	app.mu.Unlock()

	app.repairBlankTopicTabs([]*WorkspaceTab{tab})

	if strings.TrimSpace(tab.TopicID) == "" {
		t.Fatalf("blank tab still has no topic after repair")
	}
	meta, ok, err := agent.LoadBranchMeta(orphan)
	if err != nil || !ok {
		t.Fatalf("load adopted meta: %v (ok=%v)", err, ok)
	}
	if meta.TopicID != tab.TopicID {
		t.Fatalf("adopted session topic = %q, want the bound tab's %q", meta.TopicID, tab.TopicID)
	}
	if !netdevHomeTopicIDs(t)[tab.TopicID] {
		t.Fatalf("topic %q missing from netdev home index", tab.TopicID)
	}
	if title := loadTopicTitle(profileHomeRoot(config.ProfileNetDev), tab.TopicID, config.ProfileNetDev); strings.TrimSpace(title) == "" {
		t.Fatalf("adopted topic has no title")
	}
	if got, _ := app.findKnownTopicSession(tab.TopicID); got != orphan {
		t.Fatalf("findKnownTopicSession = %q, want %q", got, orphan)
	}
	nodes := app.ListProjectTree(config.ProfileNetDev)
	if findTreeTopic(nodes, tab.TopicID) == nil {
		t.Fatalf("adopted topic not rendered in netdev tree: %#v", nodes)
	}
}

// An orphan with no live tab (its tab is long gone) gets a fresh topic and
// lands on the profile home when its meta carries no root.
func TestAdoptTopiclessSessionWithoutBoundTab(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDirFor(config.ProfileNetDev)
	orphan := writeOrphanSession(t, dir, "20260902-231622-dc23.jsonl", "", config.ProfileNetDev)

	NewApp().repairBlankTopicTabs(nil)

	meta, ok, err := agent.LoadBranchMeta(orphan)
	if err != nil || !ok {
		t.Fatalf("load adopted meta: %v (ok=%v)", err, ok)
	}
	if strings.TrimSpace(meta.TopicID) == "" {
		t.Fatalf("orphan was not adopted (no topic stamped)")
	}
	if meta.WorkspaceRoot != normalizeProjectRoot(profileHomeRoot(config.ProfileNetDev)) {
		t.Fatalf("rootless orphan adopted onto %q, want netdev home", meta.WorkspaceRoot)
	}
	if !netdevHomeTopicIDs(t)[meta.TopicID] {
		t.Fatalf("adopted topic %q missing from index", meta.TopicID)
	}
}

// Subagent branches, bot sessions, and expert collaborations keep their own
// discovery models — adoption must not file them into the project tree.
func TestAdoptSkipsSubagentBotAndExpertSessions(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDirFor(config.ProfileCowork)
	write := func(name string, mutate func(m *agent.BranchMeta)) string {
		path := writeOrphanSession(t, dir, name, profileHomeRoot(config.ProfileCowork), config.ProfileCowork)
		m, _, _ := agent.LoadBranchMeta(path)
		mutate(&m)
		if err := agent.SaveBranchMetaPreserveUpdated(path, m); err != nil {
			t.Fatalf("rewrite meta %s: %v", name, err)
		}
		return path
	}
	subagent := write("sa_branch.jsonl", func(m *agent.BranchMeta) { m.ParentID = "20260904-185841-eee6" })
	bot := write("bot_session.jsonl", func(m *agent.BranchMeta) { m.Mode = "bot"; m.Platform = "wechat" })
	expert := write("expert_session.jsonl", func(m *agent.BranchMeta) { m.ExpertTeamID = "team_1" })
	// Legacy subagent sidecar: lives in the subagents/ dir with NO parent_id —
	// the path convention must exclude it too.
	legacySubagent := writeOrphanSession(t, filepath.Join(dir, "subagents"), "sa_legacy.jsonl", profileHomeRoot(config.ProfileCowork), "")

	NewApp().repairBlankTopicTabs(nil)

	for name, path := range map[string]string{"subagent": subagent, "bot": bot, "expert": expert, "legacy-subagent": legacySubagent} {
		meta, _, _ := agent.LoadBranchMeta(path)
		if strings.TrimSpace(meta.TopicID) != "" {
			t.Fatalf("%s session was adopted (topic %q) — must be skipped", name, meta.TopicID)
		}
	}
}

// Running repair twice must not churn topics: everything already carries one.
func TestRepairIdempotent(t *testing.T) {
	isolateDesktopUserDirs(t)

	dir := config.SessionDirFor(config.ProfileNetDev)
	orphan := writeOrphanSession(t, dir, "20260904-083332-319b.jsonl", profileHomeRoot(config.ProfileNetDev), config.ProfileNetDev)

	app := NewApp()
	tab := &WorkspaceTab{ID: "tab_blank", Scope: "project", WorkspaceRoot: profileHomeRoot(config.ProfileNetDev), SessionPath: orphan, profile: config.ProfileNetDev}
	app.mu.Lock()
	app.tabs[tab.ID] = tab
	app.tabOrder = []string{tab.ID}
	app.mu.Unlock()

	app.repairBlankTopicTabs([]*WorkspaceTab{tab})
	first := tab.TopicID
	meta1, _, _ := agent.LoadBranchMeta(orphan)
	app.repairBlankTopicTabs([]*WorkspaceTab{tab})
	meta2, _, _ := agent.LoadBranchMeta(orphan)

	if tab.TopicID != first {
		t.Fatalf("tab topic churned: %q → %q", first, tab.TopicID)
	}
	if meta2.TopicID != meta1.TopicID {
		t.Fatalf("session topic churned: %q → %q", meta1.TopicID, meta2.TopicID)
	}
}

// Expert and remote tabs (and non-project scopes) keep their own topic model:
// ensureTabTopic leaves them untouched.
func TestEnsureTabTopicSkipsExpertAndRemoteTabs(t *testing.T) {
	isolateDesktopUserDirs(t)

	app := NewApp()
	expert := &WorkspaceTab{ID: "tab_expert", Scope: "expert", IsExpertSession: true, profile: config.ProfileCowork}
	remote := &WorkspaceTab{ID: "tab_remote", Scope: "project", WorkspaceRoot: t.TempDir(), profile: config.ProfileDev}
	remote.Remote = &RemoteRef{Kind: "ssh", Target: "host"}
	if got := app.ensureTabTopic(expert); got != "" || expert.TopicID != "" {
		t.Fatalf("expert tab got a topic: %q", got)
	}
	if got := app.ensureTabTopic(remote); got != "" || remote.TopicID != "" {
		t.Fatalf("remote tab got a topic: %q", got)
	}
}

func findTreeTopic(nodes []ProjectNode, topicID string) *ProjectNode {
	for i := range nodes {
		if nodes[i].Kind == "topic" && nodes[i].TopicID == topicID {
			return &nodes[i]
		}
		if hit := findTreeTopic(nodes[i].Children, topicID); hit != nil {
			return hit
		}
	}
	return nil
}
