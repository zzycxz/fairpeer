package knowledge

// knowledge_test.go — BLUETEAM 批1 最小切片红测试（SKILL_ORCHESTRATION_SPEC
// §3.5-D / §10.12）：内置三表 schema 校验、user-knowledge 同 id 覆盖优先、
// 释放与未知 id 语义。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 遍历所有内置 id：schema 校验必须全绿（坏数据在 CI 拦，不进引擎）。
func TestShippedKnowledgeValidates(t *testing.T) {
	ids := IDs()
	if len(ids) < 3 {
		t.Fatalf("expected at least 3 shipped knowledge files, got %v", ids)
	}
	for _, id := range ids {
		b, err := Load(id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if err := Validate(id, b); err != nil {
			t.Fatalf("validate %s: %v", id, err)
		}
	}
}

// schema 真的会拒：缺必需字段/坏类型的红样例。
func TestValidateRejectsBadSamples(t *testing.T) {
	if err := Validate("segment-priors", []byte("version: 1\n")); err == nil {
		t.Fatal("segment-priors without priors must fail schema")
	}
	if err := Validate("credential-spots", []byte("version: 1\nspots:\n  - id: x\n")); err == nil {
		t.Fatal("spot without check/positive must fail schema")
	}
	if err := Validate("host-risk-checks", []byte("version: 1\nchecks:\n  - id: x\n    check: {linux: \"ls\"}\n    positive: y\n")); err == nil {
		t.Fatal("check without fallback (攻防同条目) must fail schema")
	}
}

// 覆盖优先级：user-knowledge 同 id 覆盖内置；删掉覆盖后回落在内置。
func TestUserKnowledgeOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	SetStateDir(dir)
	t.Cleanup(func() { SetStateDir("") })

	// 先释放内置并读到基准内容。
	base, err := Load("segment-priors")
	if err != nil {
		t.Fatalf("baseline load: %v", err)
	}
	userDir := filepath.Join(dir, "user-knowledge")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	override := "version: 99\nsource: user\ngateway_candidates: ['.254']\nsample_points: ['.254']\nmin_alive: 1\nttl_map: {\"64\": \"x\"}\nrole_signals: []\n"
	if err := os.WriteFile(filepath.Join(userDir, "segment-priors.yaml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load("segment-priors")
	if err != nil {
		t.Fatalf("override load: %v", err)
	}
	if string(got) != override || string(got) == string(base) {
		t.Fatal("user override must win over the released builtin")
	}
	// 覆盖内容同样要过 schema。
	if err := Validate("segment-priors", got); err != nil {
		t.Fatalf("override must still validate: %v", err)
	}
	// 移除覆盖 → 回落内置。
	if err := os.Remove(filepath.Join(userDir, "segment-priors.yaml")); err != nil {
		t.Fatal(err)
	}
	after, err := Load("segment-priors")
	if err != nil || string(after) != string(base) {
		t.Fatal("removing the override must fall back to the builtin")
	}
}

// 释放幂等：EnsureReleased 两次内容不变；未知 id 报错并教路径。
func TestReleaseIdempotentAndUnknownID(t *testing.T) {
	dir := t.TempDir()
	SetStateDir(dir)
	t.Cleanup(func() { SetStateDir("") })
	d1, err := EnsureReleased()
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if d2, err := EnsureReleased(); err != nil || d2 != d1 {
		t.Fatalf("second release must be idempotent (same dir, no error): %v", err)
	}
	_, err = Load("no-such-table")
	if err == nil || !strings.Contains(err.Error(), "user-knowledge") {
		t.Fatalf("unknown id must error naming the override path: %v", err)
	}
}

// SaveUser（批 3 知识反哺通道）：接受/拒绝/覆盖三路径。
func TestSaveUser(t *testing.T) {
	dir := t.TempDir()
	SetStateDir(dir)
	t.Cleanup(func() { SetStateDir("") })

	// unknown id 拒收（纯内存判定，无落盘副作用——user-knowledge 目录不得被创建）。
	if err := SaveUser("no-such-table", []byte("version: 1\n")); err == nil {
		t.Fatal("unknown id must be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "user-knowledge")); !os.IsNotExist(err) {
		t.Fatalf("refused save must not create user-knowledge dir: %v", err)
	}
	// 校验不过整份拒收。
	if err := SaveUser("segment-priors", []byte("version: 1\n")); err == nil {
		t.Fatal("invalid YAML must be refused")
	}
	// 有效覆盖落盘，且 Load 立即可见（user 覆盖优先）。
	valid, err := embedded.ReadFile("data/segment-priors.yaml")
	if err != nil {
		t.Fatal(err)
	}
	slightly := strings.Replace(string(valid), "min_alive: 2", "min_alive: 3", 1)
	if err := SaveUser("segment-priors", []byte(slightly)); err != nil {
		t.Fatalf("valid override refused: %v", err)
	}
	got, err := Load("segment-priors")
	if err != nil || !strings.Contains(string(got), "min_alive: 3") {
		t.Fatalf("user override must win on Load: %v %q", err, got)
	}
	// 覆盖路径：第二次 SaveUser 同 id 原地替换。
	slightly2 := strings.Replace(string(valid), "min_alive: 2", "min_alive: 4", 1)
	if err := SaveUser("segment-priors", []byte(slightly2)); err != nil {
		t.Fatalf("overwrite refused: %v", err)
	}
	got2, _ := Load("segment-priors")
	if !strings.Contains(string(got2), "min_alive: 4") {
		t.Fatalf("second save must overwrite: %q", got2)
	}
}
