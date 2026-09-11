package config

// project_trust_test.go — G3（CODEX_GAP_AUDIT）三重确认后的修复红测试：
// 未受信任项目根的 [[plugins]] 与 .mcp.json MCP 服务器不得并入配置（克隆
// 仓库不得静默带入可执行服务器）；信任后正常合并；告警写入 ConfigWarnings。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/hook"
)

func writeProjectTrustFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "fairpeer.toml"), []byte(
		"[[plugins]]\nname = \"proj-plugin\"\ncommand = \"evil-plugin\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(
		`{"mcpServers":{"proj-mcp":{"command":"evil-mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUntrustedProjectPluginsAndMCPSkipped(t *testing.T) {
	hook.SetTrustHomeForTest(t.TempDir())
	t.Cleanup(func() { hook.SetTrustHomeForTest("") })

	root := t.TempDir()
	writeProjectTrustFixture(t, root)

	cfg, err := LoadForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.Plugins {
		if strings.HasPrefix(p.Name, "proj-") {
			t.Fatalf("untrusted project plugin %q leaked into config", p.Name)
		}
	}
	found := false
	for _, w := range cfg.UntrustedProjectNotices {
		if strings.Contains(w, "未受信任") && strings.Contains(w, "fairpeer trust") {
			found = true
		}
	}
	if !found {
		t.Fatalf("UntrustedProjectNotices missing trust notice: %v", cfg.UntrustedProjectNotices)
	}
}

func TestTrustedProjectPluginsAndMCPMerge(t *testing.T) {
	hook.SetTrustHomeForTest(t.TempDir())
	t.Cleanup(func() { hook.SetTrustHomeForTest("") })

	root := t.TempDir()
	writeProjectTrustFixture(t, root)
	if err := hook.Trust(root, ""); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, p := range cfg.Plugins {
		names[p.Name] = true
	}
	if !names["proj-plugin"] {
		t.Fatalf("trusted project plugin missing: %v", names)
	}
	if !names["proj-mcp"] {
		t.Fatalf("trusted .mcp.json server missing: %v", names)
	}
	if len(cfg.UntrustedProjectNotices) != 0 {
		t.Fatalf("trusted project must not carry notices: %v", cfg.UntrustedProjectNotices)
	}
}

func TestUntrustRevokesLoad(t *testing.T) {
	hook.SetTrustHomeForTest(t.TempDir())
	t.Cleanup(func() { hook.SetTrustHomeForTest("") })

	root := t.TempDir()
	writeProjectTrustFixture(t, root)
	if err := hook.Trust(root, ""); err != nil {
		t.Fatal(err)
	}
	if err := hook.Untrust(root, ""); err != nil {
		t.Fatal(err)
	}
	if hook.IsTrusted(root, "") {
		t.Fatal("untrust must revoke IsTrusted")
	}
	cfg, err := LoadForRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cfg.Plugins {
		if strings.HasPrefix(p.Name, "proj-") {
			t.Fatalf("revoked project plugin %q leaked into config", p.Name)
		}
	}
}
