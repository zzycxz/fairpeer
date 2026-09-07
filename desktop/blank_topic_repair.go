package main

// Blank-topic repair (2026-09-05). Conversations held in a project-scope tab
// that never received a topic ID are invisible in the project tree and
// unopenable from the "Recent sessions" list (the resume flow needs a topic to
// open a tab). Two code paths produced such sessions:
//
//   - restoreOrBuildTabs' foreign-root re-land: a mode-switched tab whose root
//     belongs to another profile re-landed on the profile home with NO topic —
//     buildTabController only auto-assigned topics for the retired "global"
//     scope, so nothing ever backfilled project tabs; and
//   - saveTabSessionMeta, which stamped those tabs' empty TopicID into every
//     session they wrote.
//
// repairBlankTopicTabs runs once at startup, before tab controllers build:
// each blank project tab gets a fresh topic, then every saved topic-less
// session in each profile's partitions is adopted onto its bound tab's topic
// (when a blank tab still pins that session) or a fresh topic of its own,
// titled from its first user message and registered in the owning profile's
// projects index. Idempotent: sessions and tabs that already carry a topic
// pass through untouched.

import (
	"path/filepath"
	"strings"

	"github.com/zzycxz/fairpeer/internal/agent"
	"github.com/zzycxz/fairpeer/internal/config"
)

// repairBlankTopicTabs is the startup entry: assign topics to blank project
// tabs, collect the (session path → topic) bindings they pin, then adopt every
// topic-less saved session across all three profiles' partitions.
func (a *App) repairBlankTopicTabs(tabs []*WorkspaceTab) {
	bound := map[string]string{}
	for _, tab := range tabs {
		if tab == nil || tab.Remote != nil || tab.IsExpertSession {
			continue
		}
		topicID := a.ensureTabTopic(tab)
		if topicID == "" {
			continue
		}
		if path := strings.TrimSpace(tab.SessionPath); path != "" {
			bound[path] = topicID
		}
	}
	for _, key := range allProfileKeys {
		a.adoptTopiclessSessions(key, bound)
	}
}

// ensureTabTopic assigns a fresh topic to a blank project-scope tab and
// registers it under the tab's root in the tab's profile index, so the tab's
// next conversation lands in the project tree. Tabs that already carry a topic
// — or have their own scope model (expert, remote) — pass through with the
// topic they have. Returns the tab's effective topic ID ("" when out of
// scope). The tab update happens under a.mu and only while the tab is still
// registered, so a concurrently closed tab is never resurrected.
func (a *App) ensureTabTopic(tab *WorkspaceTab) string {
	if tab == nil || tab.Remote != nil || tab.IsExpertSession || tab.Scope != "project" {
		return ""
	}
	if id := strings.TrimSpace(tab.TopicID); id != "" {
		return id
	}
	root := normalizeProjectRoot(tab.WorkspaceRoot)
	if root == "" {
		root = normalizeProjectRoot(ensureProfileHomeRoot(tab.profile))
	}
	profile := normalizeProfileName(tab.profile)
	topicID := newTopicID()
	registerTopicInProfileIndex(root, topicID, defaultTopicTitle, profile)
	a.mu.Lock()
	if current := a.tabs[tab.ID]; current == tab {
		tab.WorkspaceRoot = root
		tab.TopicID = topicID
		tab.TopicTitle = topicTitleForTab("project", root, topicID, profile)
		a.saveTabsLocked()
	}
	a.mu.Unlock()
	return topicID
}

// registerTopicInProfileIndex titles a topic and registers it under its root
// in the profile's projects index, creating the project entry (titled 工作台
// for a profile home) when missing. Best-effort: an unwritable sidecar leaves
// the topic unregistered rather than failing the caller.
func registerTopicInProfileIndex(workspaceRoot, topicID, title string, profileKey ...string) {
	root := normalizeProjectRoot(workspaceRoot)
	if root == "" || strings.TrimSpace(topicID) == "" {
		return
	}
	if err := setTopicTitleWithSource(root, topicID, title, topicTitleSourceAuto, profileKey...); err != nil {
		return
	}
	f := loadProjectsFile(profileKey...)
	for i := range f.Projects {
		if normalizeProjectRoot(f.Projects[i].Root) == root {
			f.Projects[i].Topics = prependUniqueString(f.Projects[i].Topics, topicID)
			_ = saveProjectsFile(f, profileKey...)
			return
		}
	}
	homeTitle := ""
	if _, isHome := profileKeyForHomeRoot(root); isHome {
		homeTitle = homeProjectTitle
	}
	f.Projects = append(f.Projects, desktopProject{Root: root, Title: homeTitle, Topics: []string{topicID}})
	_ = saveProjectsFile(f, profileKey...)
}

// adoptTopiclessSessions stamps a topic onto every saved session in the
// profile's partitions that carries no topic_id but did carry conversation
// (ListSessions only returns sessions with ≥1 turn). Each adopted session is
// titled from its first user message, registered under its own workspace root
// (empty roots land on the profile home, matching restoreSessionTopicIndex),
// and re-stamped with the topic so it re-enters the project tree and becomes
// resumable. Sessions bound to a live blank-topic tab reuse that tab's fresh
// topic. Subagent/bot/expert sessions have their own lifecycles and are left
// alone. Startup-only; idempotent by the TopicID check.
func (a *App) adoptTopiclessSessions(profileKey string, bound map[string]string) {
	if key := config.ProfileNameKey(profileKey); key == "" {
		profileKey = config.ProfileDev
	}
	dirs := []string{}
	if d := config.SessionDirFor(profileKey); d != "" {
		dirs = append(dirs, d)
	}
	for _, p := range loadProjectsFile(profileKey).Projects {
		if d := config.ProjectSessionDirFor(p.Root, profileKey); d != "" {
			dirs = append(dirs, d)
		}
	}
	seenDir := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" || seenDir[dir] {
			continue
		}
		seenDir[dir] = true
		infos, err := agent.ListSessions(dir)
		if err != nil {
			continue
		}
		for _, info := range infos {
			adoptOneTopiclessSession(dir, info.Path, profileKey, bound)
		}
	}
}

func adoptOneTopiclessSession(dir, path, profileKey string, bound map[string]string) {
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil || !ok {
		return
	}
	if strings.TrimSpace(meta.TopicID) != "" {
		return
	}
	// Subagent branches, expert-team collaborations, and IM bot sessions each
	// manage their own discovery — adopting them into the project tree would
	// misfile them. Subagents are identified by the subagents/ directory
	// convention (their sidecars historically carry no parent_id) as well as
	// by ParentID when present; trashed sessions are never adoption targets.
	if meta.ParentID != "" || meta.ExpertTeamID != "" || meta.Mode == "bot" || meta.Platform != "" {
		return
	}
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg == "subagents" || seg == sessionTrashDir {
			return
		}
	}
	// Profile attribution: the meta's own stamp is authoritative. The dev
	// scan (over <config>/sessions) also sees flat files one level into the
	// cowork/netdev partitions — those belong to the named profile's own
	// scan, which lands them on the right home and index.
	if p := config.ProfileNameKey(meta.Profile); p != "" {
		if p != config.ProfileNameKey(profileKey) {
			return
		}
		profileKey = p
	}
	root := normalizeProjectRoot(meta.WorkspaceRoot)
	if root == "" {
		root = normalizeProjectRoot(ensureProfileHomeRoot(profileKey))
	}
	topicID := bound[strings.TrimSpace(path)]
	if topicID == "" {
		topicID = newTopicID()
	}
	title := restoredSessionTopicTitle(dir, path, meta)
	if title == "" {
		title = defaultTopicTitle
	}
	registerTopicInProfileIndex(root, topicID, title, profileKey)
	if !meta.CreatedAt.IsZero() {
		_ = setTopicCreatedAt(root, topicID, meta.CreatedAt.UnixMilli(), profileKey)
	}
	meta.Scope = "project"
	meta.WorkspaceRoot = root
	meta.TopicID = topicID
	meta.TopicTitle = title
	if meta.Profile == "" {
		meta.Profile = profileKey
	}
	_ = agent.SaveBranchMetaPreserveUpdated(path, meta)
}
