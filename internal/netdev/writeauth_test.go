package netdev

// writeauth_test.go — WRITE_AUTHZ_SPEC §12 后端红测试。三明治的完整 E2E
// （真拨号 pre/post）留给靶场批次；这里钉死全部闸门语义：三档分发、
// 危险/未知硬底线、headless confirm 拒绝、TOML 放宽拦截、写预算、
// 两锁合成、台账与 diff 摘要。

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

func writeAuthTestEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	SetWriteLocksPath(filepath.Join(dir, "write-locks.json"))
	SetOpStepsDir(filepath.Join(dir, "opsteps"))
	SetBackupsDir(filepath.Join(dir, "backups"))
	t.Cleanup(func() {
		SetWriteLocksPath("")
		SetOpStepsDir("")
		SetBackupsDir("")
	})
}

// newWriteAuthManager builds a Manager around one cisco device whose group
// tier is set by groupTier ("" = no group entry → global default applies).
func newWriteAuthManager(t *testing.T, groupTier string, device config.NetDevDevice, groups ...config.NetDevGroup) *Manager {
	t.Helper()
	writeAuthTestEnv(t)
	cfg := &config.Config{}
	cfg.NetDev.Devices = []config.NetDevDevice{device}
	if len(groups) > 0 {
		cfg.NetDev.Groups = groups
	} else if groupTier != "" {
		cfg.NetDev.Groups = []config.NetDevGroup{{Name: device.Group, Write: groupTier}}
	}
	return NewManager(cfg)
}

func labDevice() config.NetDevDevice {
	return config.NetDevDevice{Name: "sw1", Vendor: "cisco", OS: "ios", Group: "lab"}
}

// ── 配置层：解析、tighten-only、三层解析（§4.1） ─────────────────────────────

func TestWriteTierParseAndTightenOnlyValidation(t *testing.T) {
	if _, err := config.ParseNetDevWriteTier("yolo"); err == nil {
		t.Fatal("invalid tier must error")
	}
	for _, s := range []string{"", "sealed", "confirm", "auto"} {
		if _, err := config.ParseNetDevWriteTier(s); err != nil {
			t.Fatalf("tier %q: %v", s, err)
		}
	}

	nd := config.NetDevConfig{Enabled: true}
	nd.Devices = []config.NetDevDevice{{Name: "sw1", Vendor: "cisco", OS: "ios", Address: "192.0.2.1", Group: "lab", WriteOverride: "auto"}}
	nd.Groups = []config.NetDevGroup{{Name: "lab", Write: "sealed"}}
	if err := config.ValidateNetDev(nd); err == nil || !strings.Contains(err.Error(), "wider") {
		t.Fatalf("auto override on sealed group must be rejected, got: %v", err)
	}

	// Tightening is legal.
	nd.Groups[0].Write = "auto"
	nd.Devices[0].WriteOverride = "sealed"
	if err := config.ValidateNetDev(nd); err != nil {
		t.Fatalf("tightening override must validate: %v", err)
	}
}

func TestWriteTierResolutionLayering(t *testing.T) {
	nd := config.NetDevConfig{}
	nd.Groups = []config.NetDevGroup{{Name: "lab", Write: "auto"}}
	d := config.NetDevDevice{Name: "sw1", Group: "lab"}

	if got := nd.NetDevWriteTierFor(d); got != config.NetDevWriteAuto {
		t.Fatalf("group auto → auto, got %s", got)
	}
	d.WriteOverride = "confirm"
	if got := nd.NetDevWriteTierFor(d); got != config.NetDevWriteConfirm {
		t.Fatalf("device tighten confirm → confirm, got %s", got)
	}
	// A wider override is clamped by the resolver too (defense in depth).
	d.WriteOverride = "auto"
	nd.Groups[0].Write = "sealed"
	if got := nd.NetDevWriteTierFor(d); got != config.NetDevWriteSealed {
		t.Fatalf("wider override clamped to group sealed, got %s", got)
	}
	// Empty everything → sealed (zero value is the safe default).
	if got := (config.NetDevConfig{}).NetDevWriteTierFor(config.NetDevDevice{Name: "x"}); got != config.NetDevWriteSealed {
		t.Fatalf("zero config → sealed, got %s", got)
	}
}

// ── §12.5：TOML 放宽未经确认 → sealed 降级 + 告警 ────────────────────────────

func TestUnconfirmedRelaxationClampedToSealed(t *testing.T) {
	m := newWriteAuthManager(t, "auto", labDevice())
	if got := m.EffectiveWriteTier(labDevice()); got != config.NetDevWriteSealed {
		t.Fatalf("unconfirmed auto must clamp to sealed, got %s", got)
	}
	w := m.WriteTierWarnings()
	if len(w) != 1 || !strings.Contains(w[0], "sw1") {
		t.Fatalf("expected one clamp warning naming the device, got %v", w)
	}
	if len(m.WriteTierWarnings()) != 0 {
		t.Fatal("warnings should clear after read")
	}
	// Clamping persists after the warning fired once.
	if got := m.EffectiveWriteTier(labDevice()); got != config.NetDevWriteSealed {
		t.Fatalf("clamp persists, got %s", got)
	}
	// The human confirms → the configured tier takes effect.
	if err := m.ConfirmWriteTier("sw1", "auto", "test"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if got := m.EffectiveWriteTier(labDevice()); got != config.NetDevWriteAuto {
		t.Fatalf("confirmed auto → auto, got %s", got)
	}
	// Tightening in config beats a wider confirmed baseline.
	if err := m.ConfirmWriteTier("sw1", "auto", "test"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	m.cfg.NetDev.Groups[0].Write = "sealed"
	if got := m.EffectiveWriteTier(labDevice()); got != config.NetDevWriteSealed {
		t.Fatalf("configured narrower than confirmed → sealed, got %s", got)
	}
}

// ── §12.2：sealed 档写命令 → 拒绝并路由提案（任意对话模式） ─────────────────

func TestSealedWriteRoutesToProposal(t *testing.T) {
	m := newWriteAuthManager(t, "", labDevice()) // no group tier → sealed
	r := m.Exec(t.Context(), "sw1", "copy running-config tftp:")
	if !r.Refused {
		t.Fatalf("sealed write must be refused, got %+v", r)
	}
	if !strings.Contains(r.Refusal, "proposal") {
		t.Fatalf("refusal should route to the proposal pipeline: %s", r.Refusal)
	}
}

// ── §12.7：dangerous / unknown 在 auto 档也恒拒（硬底线） ────────────────────

func TestDangerousAndUnknownRefusedEvenOnAuto(t *testing.T) {
	m := newWriteAuthManager(t, "auto", labDevice())
	if err := m.ConfirmWriteTier("sw1", "auto", "test"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	r := m.Exec(t.Context(), "sw1", "reload")
	if !r.Refused || r.Class != "dangerous" {
		t.Fatalf("dangerous must refuse on auto tier: %+v", r)
	}
	r = m.Exec(t.Context(), "sw1", "zzz-unknown-command arg")
	if !r.Refused || r.Class != "unknown" {
		t.Fatalf("unknown must refuse on auto tier: %+v", r)
	}
}

// ── §12.9：confirm 档 headless 拒绝（Manager 通道，不依赖 permission） ───────

func TestConfirmTierHeadlessRefused(t *testing.T) {
	m := newWriteAuthManager(t, "confirm", labDevice())
	if err := m.ConfirmWriteTier("sw1", "confirm", "test"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	r := m.Exec(t.Context(), "sw1", "copy running-config tftp:")
	if !r.Refused {
		t.Fatal("confirm tier without an approver must refuse (headless)")
	}
	if !strings.Contains(r.Refusal, "审批") {
		t.Fatalf("refusal should explain the approval requirement: %s", r.Refusal)
	}
	// An interactive approver that declines also refuses — and the Manager
	// channel never consults the permission layer, so full-access cannot skip.
	m.SetWriteApprover(func(device, command string) (bool, string) {
		return false, "用户点了拒绝"
	})
	r = m.Exec(t.Context(), "sw1", "copy running-config tftp:")
	if !r.Refused || !strings.Contains(r.Refusal, "拒绝") {
		t.Fatalf("declined approval must refuse: %+v", r)
	}
}

// ── §12.8（预算部分）：写预算独立计数、TurnBegin 重置 ────────────────────────

func TestWriteBudgetExhaustion(t *testing.T) {
	m := newWriteAuthManager(t, "auto", labDevice())
	m.cfg.NetDev.Write.TurnWriteBudget = 1
	if err := m.ConfirmWriteTier("sw1", "auto", "test"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	m.waMu.Lock()
	m.turnWrites = 1 // simulate one sandwich already spent this turn
	m.waMu.Unlock()
	r := m.Exec(t.Context(), "sw1", "copy running-config tftp:")
	if !r.Refused || !strings.Contains(r.Refusal, "write budget exhausted") {
		t.Fatalf("exhausted write budget must refuse before the sandwich: %+v", r)
	}
	m.TurnBegin()
	if !m.writeBudgetLeft() {
		t.Fatal("TurnBegin must reset the write budget")
	}
}

// ── 两锁合成（§2.3）：仅 auto 档写调用按 writer 走模式回退 ───────────────────

func TestExecReadOnlyCallSynthesis(t *testing.T) {
	m := newWriteAuthManager(t, "auto", labDevice())
	if err := m.ConfirmWriteTier("sw1", "auto", "test"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	et := &execTool{m: m}
	writeArgs := []byte(`{"device":"sw1","command":"copy running-config tftp:"}`)
	readArgs := []byte(`{"device":"sw1","command":"show version"}`)

	if et.ReadOnlyCall(writeArgs) {
		t.Fatal("auto-tier write must be a writer call (mode applies)")
	}
	if !et.ReadOnlyCall(readArgs) {
		t.Fatal("read command stays read-only")
	}

	// Confirm tier: the card belongs to the Manager channel — keep this layer
	// "read" to avoid double cards.
	m2 := newWriteAuthManager(t, "confirm", labDevice())
	if err := m2.ConfirmWriteTier("sw1", "confirm", "test"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if !(&execTool{m: m2}).ReadOnlyCall(writeArgs) {
		t.Fatal("confirm-tier write must stay read at the permission layer (Manager owns the card)")
	}

	// Sealed: refused inside Exec regardless of what permission says.
	m3 := newWriteAuthManager(t, "", labDevice())
	if !(&execTool{m: m3}).ReadOnlyCall(writeArgs) {
		t.Fatal("sealed write stays read at this layer (Exec refuses it)")
	}
}

// ── §12.10（台账部分）：OpStep 落库与读取、diff 摘要 ─────────────────────────

func TestOpStepLedgerAndSummarizeDiff(t *testing.T) {
	m := newWriteAuthManager(t, "auto", labDevice())
	m.appendOpStep(context.Background(), OpStep{At: "2026-09-07T10:00:00Z", Actor: "agent", Device: "sw1",
		Command: "sysname CORE-1", Status: "ok", PreID: "sw1@1", PostID: "sw1@2",
		DiffSummary: "+1/-1 行", RollbackTo: "sw1@1"})
	steps := ListOpSteps("sw1", 10)
	if len(steps) != 1 {
		t.Fatalf("expected 1 ledger row, got %d", len(steps))
	}
	if steps[0].RollbackTo != "sw1@1" || steps[0].PreID != "sw1@1" {
		t.Fatalf("ledger row must carry the rollback pointer: %+v", steps[0])
	}
	if len(ListOpSteps("other", 10)) != 0 {
		t.Fatal("device filter broken")
	}

	sum := summarizeDiff("--- a\n+++ b\n-old name\n+sysname CORE-1\n+extra\n")
	if !strings.Contains(sum, "+2/-1 行") || !strings.Contains(sum, "sysname CORE-1") {
		t.Fatalf("diff summary wrong: %s", sum)
	}
	if got := summarizeDiff("no changes here"); !strings.Contains(got, "无文本差异") {
		t.Fatalf("empty diff should say so: %s", got)
	}
}
