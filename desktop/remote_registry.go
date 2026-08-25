package main

// remote_registry.go — the durable record of remote workspaces opened on this
// desktop: kind+target+user+root+title per entry, persisted as JSON in the
// desktop config dir. The project tree reads it to render remote projects (and
// their host-side topics when a link is live); OpenRemoteTopicTab resolves a
// tree click back into a remote tab.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type remoteProjectEntry struct {
	Slug  string    `json:"slug"`
	Ref   RemoteRef `json:"ref"`
	Root  string    `json:"root"`
	Title string    `json:"title,omitempty"`
}

var remoteRegistryMu sync.Mutex

func remoteRegistryPath() string {
	return filepath.Join(desktopConfigDir(), "remote-projects.json")
}

func loadRemoteProjects() []remoteProjectEntry {
	remoteRegistryMu.Lock()
	defer remoteRegistryMu.Unlock()
	b, err := os.ReadFile(remoteRegistryPath())
	if err != nil {
		return nil
	}
	var entries []remoteProjectEntry
	if json.Unmarshal(b, &entries) != nil {
		return nil
	}
	return entries
}

func saveRemoteProjectsLocked(entries []remoteProjectEntry) {
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(remoteRegistryPath()), 0o755)
	tmp := remoteRegistryPath() + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o644) == nil {
		_ = os.Rename(tmp, remoteRegistryPath())
	}
}

// upsertRemoteProject records (or refreshes) a remote workspace in the
// registry, keyed by its slug.
func upsertRemoteProject(ref RemoteRef, root, title string) {
	slug := remoteProjectSlugKey(ref, root)
	remoteRegistryMu.Lock()
	defer remoteRegistryMu.Unlock()
	entries := []remoteProjectEntry{}
	if b, err := os.ReadFile(remoteRegistryPath()); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	for i := range entries {
		if entries[i].Slug == slug {
			entries[i].Ref = ref
			entries[i].Root = root
			if title != "" {
				entries[i].Title = title
			}
			sort.SliceStable(entries, func(a, b int) bool { return entries[a].Slug < entries[b].Slug })
			saveRemoteProjectsLocked(entries)
			return
		}
	}
	if title == "" {
		title = ref.Label
	}
	entries = append(entries, remoteProjectEntry{Slug: slug, Ref: ref, Root: root, Title: title})
	sort.SliceStable(entries, func(a, b int) bool { return entries[a].Slug < entries[b].Slug })
	saveRemoteProjectsLocked(entries)
}

// removeRemoteProject drops a registry entry (tab trash flow can call this).
func removeRemoteProject(ref RemoteRef, root string) {
	slug := remoteProjectSlugKey(ref, root)
	remoteRegistryMu.Lock()
	defer remoteRegistryMu.Unlock()
	entries := []remoteProjectEntry{}
	if b, err := os.ReadFile(remoteRegistryPath()); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	out := entries[:0]
	for _, e := range entries {
		if e.Slug != slug {
			out = append(out, e)
		}
	}
	saveRemoteProjectsLocked(out)
}

// remoteTopicsForRef fetches the host-side session list over a live link and
// folds it into per-topic summaries (turns, last activity, title, newest
// session path). Empty when the link is down — the tree then shows the bare
// project node, matching the offline badge.
type remoteTopicSummary struct {
	TopicID       string
	Title         string
	Turns         int
	LastActivityMs int64
	NewestSession string
}

func remoteTopicsForRef(m *remoteHostManager, ref RemoteRef, root string) []remoteTopicSummary {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	ml := m.links[remoteRefKey(ref)]
	m.mu.Unlock()
	if ml == nil {
		return nil
	}
	ml.mu.Lock()
	dead := ml.dead
	ml.mu.Unlock()
	if dead {
		return nil
	}
	var res struct {
		Sessions []struct {
			Path       string `json:"path"`
			ModTimeMs  int64  `json:"modTimeMs"`
			Turns      int    `json:"turns"`
			TopicID    string `json:"topicId"`
			TopicTitle string `json:"topicTitle"`
		} `json:"sessions"`
	}
	if err := ml.link.call(bootCtxBackground(), "session/list", map[string]string{"cwd": root}, &res); err != nil {
		return nil
	}
	byTopic := map[string]*remoteTopicSummary{}
	var order []string
	for _, s := range res.Sessions {
		tid := strings.TrimSpace(s.TopicID)
		if tid == "" || s.Turns == 0 {
			continue
		}
		sum := byTopic[tid]
		if sum == nil {
			sum = &remoteTopicSummary{TopicID: tid, Title: strings.TrimSpace(s.TopicTitle)}
			byTopic[tid] = sum
			order = append(order, tid)
		}
		sum.Turns += s.Turns
		if s.ModTimeMs > sum.LastActivityMs {
			sum.LastActivityMs = s.ModTimeMs
			sum.NewestSession = s.Path
		}
	}
	out := make([]remoteTopicSummary, 0, len(order))
	for _, tid := range order {
		out = append(out, *byTopic[tid])
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].LastActivityMs > out[b].LastActivityMs })
	return out
}


// bootCtxBackground is a bounded context for registry RPC calls.
func bootCtxBackground() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	_ = cancel // leaks a timer at worst until fire; the call is short-lived
	return ctx
}
