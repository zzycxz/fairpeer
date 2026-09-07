package main

// trustdomain_app_test.go pins the GUI enable flow (settings 信任域 tab
// onboarding): TrustDomainInit writes identity + genesis, flips
// [trustdomain] enabled, and the status view turns into a joined
// single-admin board; a second init reuses the on-disk ledger instead of
// forking a new domain. TrustDomainSetEnabled only writes the flag — the
// join path stays CLI (multi-party admission).

import (
	"context"
	"testing"
	"time"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev"
)

func TestTrustDomainInitCreatesBootstrapDomain(t *testing.T) {
	isolateDesktopUserDirs(t)
	// SharedRemoteNode is process-global; start clean and leave it clean.
	netdev.ResetSharedRemoteNode()
	t.Cleanup(netdev.ResetSharedRemoteNode)

	a := &App{}
	if v := a.TrustDomainStatus(); v.Enabled {
		t.Fatal("trust domain should start disabled")
	}

	domainID, err := a.TrustDomainInit()
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(domainID) < 16 {
		t.Fatalf("suspiciously short domain id %q", domainID)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.TrustDomain.Enabled {
		t.Fatal("[trustdomain] enabled not persisted")
	}

	view := a.TrustDomainStatus()
	if !view.Enabled || !view.Joined {
		t.Fatalf("status after init: enabled=%v joined=%v detail=%s", view.Enabled, view.Joined, view.Detail)
	}
	if view.Height != 0 {
		t.Fatalf("genesis-only chain height = %d, want 0 (genesis is height 0)", view.Height)
	}
	if len(view.Members) != 1 || view.Members[0].Role != "admin" {
		t.Fatalf("bootstrap domain members = %+v, want one admin", view.Members)
	}

	again, err := a.TrustDomainInit()
	if err != nil {
		t.Fatalf("re-init: %v", err)
	}
	if again != domainID {
		t.Fatalf("re-init forked the domain: %q vs %q", again, domainID)
	}
}

// TestTrustDomainEnableSkipsControllerRebuild pins the applyConfigOnly rule:
// with a live ctx and NO tabs, applyConfigChange's rebuild() step would fail
// ("no active tab" — and in production "unknown model" on setups without a
// default model, poisoning the topbar 启动错误 banner). Enabling the domain
// must succeed regardless: it is model-independent infrastructure.
func TestTrustDomainEnableSkipsControllerRebuild(t *testing.T) {
	isolateDesktopUserDirs(t)
	netdev.ResetSharedRemoteNode()
	t.Cleanup(netdev.ResetSharedRemoteNode)

	a := &App{ctx: context.Background()} // live ctx + empty tab map: rebuild would error
	if _, err := a.TrustDomainInit(); err != nil {
		t.Fatalf("init should not depend on a bootable controller: %v", err)
	}
	if err := a.TrustDomainSetEnabled(false); err != nil {
		t.Fatalf("set enabled should not depend on a bootable controller: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.TrustDomain.Enabled {
		t.Fatal("SetEnabled(false) did not persist")
	}
}

// TestTrustDomainResetBootstrapsFreshDomain pins the hard-rollback path:
// reset (init --force) discards the ledger — new domain ID, back to a
// genesis-only chain, still enabled — while a plain re-init keeps reusing
// the same domain.
func TestTrustDomainResetBootstrapsFreshDomain(t *testing.T) {
	isolateDesktopUserDirs(t)
	netdev.ResetSharedRemoteNode()
	t.Cleanup(netdev.ResetSharedRemoteNode)

	a := &App{}
	first, err := a.TrustDomainInit()
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	again, err := a.TrustDomainInit()
	if err != nil || again != first {
		t.Fatalf("re-init must reuse the domain: %q vs %q (err=%v)", again, first, err)
	}
	// 域 ID = 创世块哈希，而创世块带秒级时间戳：同一秒内 init/reset 会得到
	// 相同哈希。隔到下一秒，两个域才可区分（生产行为不受影响——账本确实重建了）。
	time.Sleep(1100 * time.Millisecond)
	reset, err := a.TrustDomainReset()
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if reset == first {
		t.Fatalf("reset must produce a fresh domain, got the same id %q", reset)
	}
	view := a.TrustDomainStatus()
	if !view.Enabled || !view.Joined || view.Height != 0 {
		t.Fatalf("after reset: enabled=%v joined=%v height=%d, want true/true/0", view.Enabled, view.Joined, view.Height)
	}
	if len(view.Members) != 1 || view.Members[0].Role != "admin" {
		t.Fatalf("after reset members = %+v, want one admin", view.Members)
	}
}

func TestTrustDomainSetEnabledLeavesLedgerUntouched(t *testing.T) {
	isolateDesktopUserDirs(t)
	netdev.ResetSharedRemoteNode()
	t.Cleanup(netdev.ResetSharedRemoteNode)

	a := &App{}
	if err := a.TrustDomainSetEnabled(true); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.TrustDomain.Enabled {
		t.Fatal("enabled flag not persisted")
	}
	view := a.TrustDomainStatus()
	if !view.Enabled || view.Joined {
		t.Fatalf("want enabled-but-not-joined, got enabled=%v joined=%v", view.Enabled, view.Joined)
	}
}
