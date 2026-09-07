package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zzycxz/fairpeer/internal/agent"
	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/provider"
)

// SCENARIO_SPEC G2-1：sa_* / subagents/ 会话不进用户"最近会话"与回收站。
func TestListSessionsSkipsSubagentTranscripts(t *testing.T) {
	// 夹具必须落在 knownSessionDirs 会枚举到的分区里：TestMain 沙箱化的
	// AppData 下的全局会话分区。
	dir := config.SessionDir()
	os.MkdirAll(dir, 0o755)
	mk := func(rel, firstUser string) string {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		s := agent.NewSession("")
		s.Add(provider.Message{Role: provider.RoleUser, Content: firstUser})
		if err := s.Save(p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	mk("conv-1/conv-1.jsonl", "正常对话")
	mk("sa_20260905_000001_x/sa_20260905_000001_x.jsonl", "子代理任务")
	mk("subagents/sa_y/sa_y.jsonl", "子代理任务2")

	a := &App{}
	a.tabs = map[string]*WorkspaceTab{}
	got := a.listSessions("")
	if len(got) != 1 {
		t.Fatalf("listSessions = %d 条（期望 1，只留正常对话）", len(got))
	}
	if filepath.Base(got[0].Path) != "conv-1.jsonl" {
		t.Fatalf("留下的不是正常对话: %v", got[0].Path)
	}
}

func TestIsSubagentSession(t *testing.T) {
	cases := map[string]bool{
		filepath.Join("s", "subagents", "sa_x.jsonl"): true,
		filepath.Join("s", "sa_20260905_x.jsonl"):     true,
		filepath.Join("s", "20260905-1/20260905-1.jsonl"): false,
		"conv.jsonl":          false,
		"sample_subagents.md": false,
	}
	for path, want := range cases {
		if got := isSubagentSession(path); got != want {
			t.Errorf("isSubagentSession(%q) = %v, want %v", path, got, want)
		}
	}
}
