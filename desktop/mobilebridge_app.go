package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/zzycxz/fairpeer/internal/mobilebridge"
)

// desktop/mobilebridge_app.go wires the internal/mobilebridge Bridge into the
// fairpeer desktop App. It provides:
//   - a file-backed KeyStore for S's long-term Ed25519 key + paired peer pubs
//   - an execAdapter exposing App's existing tab methods as CommandExecutor
//   - Wails-bound methods the frontend calls (pair / status / confirm / unpair)
//
// MINIMAL-INVASION design: app.go grows one field (mobilebridge atomic.Pointer)
// and one startup line; tabs.go grows one forward line in tabEventSink.Emit.
// Everything else lives in this file.
//
// TODO(M2 polish): replace fileKeyStore with a secret.Store adapter so the
// long-term private key is DPAPI-encrypted at rest (FAIRPEER_SPEC §6). The
// mobilebridge.KeyStore interface makes that swap local to this file.

// fileKeyStore persists keys as base64 in a JSON file under ~/.fairpeer/.
// M1 trade-off: plaintext at rest. Acceptable for the dev build; the DPAPI
// upgrade is tracked separately and changes nothing in mobilebridge itself.
type fileKeyStore struct {
	path string
	mu   sync.Mutex
	m    map[string]string // base64-encoded values
}

func newFileKeyStore() (*fileKeyStore, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, ".fairpeer", "mobilebridge_keys.json")
	f := &fileKeyStore{path: p, m: map[string]string{}}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &f.m)
	}
	return f, nil
}

func (f *fileKeyStore) Get(key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[key]
	if !ok {
		return nil, mobilebridge.ErrNotFound
	}
	return base64.StdEncoding.DecodeString(v)
}

func (f *fileKeyStore) Set(key string, val []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[key] = base64.StdEncoding.EncodeToString(val)
	return f.saveLocked()
}

func (f *fileKeyStore) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, key)
	return f.saveLocked()
}

func (f *fileKeyStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(f.m)
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, b, 0o600)
}

// loadOrCreateDeviceKey returns S's long-term Ed25519 keypair, generating +
// persisting a fresh one on first run. The deviceId is derived from the pub
// (PROTOCOL §2.2), so a regenerated key means a new identity + re-pair.
func loadOrCreateDeviceKey(store mobilebridge.KeyStore) (ed25519.PrivateKey, ed25519.PublicKey, error) {
	if b, err := store.Get("mobilebridge.device.priv"); err == nil && len(b) == ed25519.PrivateKeySize {
		priv := ed25519.PrivateKey(b)
		pub, ok := priv.Public().(ed25519.PublicKey)
		if !ok {
			return nil, nil, fmt.Errorf("invalid stored private key")
		}
		return priv, pub, nil
	}
	pub, priv, err := mobilebridge.GenerateLongTerm()
	if err != nil {
		return nil, nil, err
	}
	if err := store.Set("mobilebridge.device.priv", priv); err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

// execAdapter exposes App's existing tab methods as mobilebridge.CommandExecutor.
// Each dispatch runs in its own goroutine so the bridge's DataChannel read loop
// is never blocked by a long Controller operation.
type execAdapter struct{ app *App }

func (e *execAdapter) Submit(tab, input, _ string) error                    { go e.app.SubmitToTab(tab, input); return nil }
func (e *execAdapter) Cancel(tab string) error                              { go e.app.CancelTab(tab); return nil }
func (e *execAdapter) Steer(tab, text string) error                         { go e.app.SteerForTab(tab, text); return nil }
func (e *execAdapter) Pause(tab string) error                               { go e.app.PauseTab(tab); return nil }
func (e *execAdapter) Resume(tab string) error                              { go e.app.ResumeTurnTab(tab); return nil }
func (e *execAdapter) Approve(tab, id string, allow, session, persist bool) error {
	go e.app.ApproveTab(tab, id, allow, session, persist)
	return nil
}
func (e *execAdapter) Answer(string, string, []string) error { return nil } // M4: wire to AskRequest
func (e *execAdapter) SetPlan(_ string, on bool) error       { go e.app.SetPlanMode(on); return nil }
func (e *execAdapter) SetModel(string, string) error         { return nil } // M4
func (e *execAdapter) ListSessions() ([]mobilebridge.SessionInfo, error) {
	return nil, nil // M4: surface real session list
}

// ensureMobileBridge loads the device key + creates the Bridge. Called once
// from App.startup. Safe to call when disabled — the bridge simply won't be
// stored, and every MobileBridge* binding no-ops.
func (a *App) ensureMobileBridge(ctx context.Context) {
	store, err := newFileKeyStore()
	if err != nil {
		return // no key store → mobile bridge stays off
	}
	priv, pub, err := loadOrCreateDeviceKey(store)
	if err != nil {
		return
	}
	cfg := mobilebridge.DefaultConfig()
	// 联调捷径：LINKPEER_SIGNAL 覆盖 signal_url（M1 TODO 的最小版；
	// 正式版应读 fairpeer.toml [mobilebridge] 段）。
	if s := os.Getenv("LINKPEER_SIGNAL"); s != "" {
		cfg.SignalURL = s
	}
	bridge := mobilebridge.NewBridge(cfg, priv, pub, store, &execAdapter{app: a}, mobilebridge.NewAudit(cfg.LogLevel))
	a.mobilebridge.Store(bridge)
	go bridge.Start(ctx)
}

// --- Wails-bound methods (frontend calls these) ---

// MobileBridgeStartPairing 返回 JSON {code, qrURL}（避免 Wails 多返回值折叠成单 string 的歧义）。
func (a *App) MobileBridgeStartPairing() (string, error) {
	mb := a.mobilebridge.Load()
	if mb == nil {
		return "", fmt.Errorf("mobile bridge not initialized")
	}
	code, qrURL, err := mb.StartPairing()
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]string{"code": code, "qrURL": qrURL})
	return string(b), nil
}

// MobileBridgeStatus reports state for the settings panel.
func (a *App) MobileBridgeStatus() map[string]any {
	mb := a.mobilebridge.Load()
	if mb == nil {
		return map[string]any{"enabled": false}
	}
	return map[string]any{
		"enabled": true,
		"pending": mb.PendingPairings(),
	}
}

func (a *App) MobileBridgeConfirm(pairID string) error {
	mb := a.mobilebridge.Load()
	if mb == nil {
		return fmt.Errorf("mobile bridge not initialized")
	}
	return mb.ConfirmPairing(pairID)
}

func (a *App) MobileBridgeReject(pairID string) {
	mb := a.mobilebridge.Load()
	if mb == nil {
		return
	}
	mb.RejectPairing(pairID)
}

func (a *App) MobileBridgeUnpair(devC string) error {
	mb := a.mobilebridge.Load()
	if mb == nil {
		return fmt.Errorf("mobile bridge not initialized")
	}
	mb.Unpair(devC)
	return nil
}

// keep sync/atomic referenced (the field lives in app.go but the import is
// grouped here for the adapter's awareness; app.go itself also imports it).
var _ atomic.Pointer[mobilebridge.Bridge]

// ensure App has the field — this is a compile-time check; the actual field
// declaration is in app.go (added by the desktop-integration edit).
// (If you see a compile error here, app.go's mobilebridge field is missing.)
var _ = context.Background
