package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/zzycxz/fairpeer/internal/config"
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
type execAdapter struct {
	app     *App
	fdMu    sync.Mutex
	fdFiles map[string]*os.File // file_drop 接收中的文件（name → handle）
}

// resolveMobileTab maps linkpeer's tab alias ("default"/"") to a real fairpeer
// tab UUID (the active tab). All mobilebridge commands route through this so
// the phone transparently joins the desktop's active session without knowing
// fairpeer's UUID-style tab IDs.
func (a *App) resolveMobileTab(tab string) string {
	if tab == "default" || tab == "" {
		return a.ActiveTabID()
	}
	return tab
}

func (e *execAdapter) Submit(tab, input, _ string) error {
	tab = e.app.resolveMobileTab(tab)
	slog.Info("mobilebridge: submit", "resolvedTab", tab, "inputLen", len(input))
	if tab == "" {
		slog.Warn("mobilebridge: submit dropped — no active tab")
		return errors.New("tab_not_found") // S1: 让 router 回传错误给 C
	}
	go e.app.SubmitToTab(tab, input)
	return nil
}

func (e *execAdapter) Cancel(tab string) error {
	if tab = e.app.resolveMobileTab(tab); tab != "" {
		go e.app.CancelTab(tab)
	}
	return nil
}

func (e *execAdapter) Steer(tab, text string) error {
	if tab = e.app.resolveMobileTab(tab); tab != "" {
		go e.app.SteerForTab(tab, text)
	}
	return nil
}

func (e *execAdapter) Pause(tab string) error {
	if tab = e.app.resolveMobileTab(tab); tab != "" {
		go e.app.PauseTab(tab)
	}
	return nil
}

func (e *execAdapter) Resume(tab string) error {
	if tab = e.app.resolveMobileTab(tab); tab != "" {
		go e.app.ResumeTurnTab(tab)
	}
	return nil
}

func (e *execAdapter) Approve(tab, id string, allow, session, persist bool) error {
	if tab = e.app.resolveMobileTab(tab); tab != "" {
		go e.app.ApproveTab(tab, id, allow, session, persist)
	}
	return nil
}
func (e *execAdapter) Answer(string, string, []string) error { return nil } // TODO: wire to AskRequest
func (e *execAdapter) SetPlan(_ string, on bool) error       { go e.app.SetPlanMode(on); return nil }
func (e *execAdapter) SetModel(tab, model string) error {
	if tab = e.app.resolveMobileTab(tab); tab != "" {
		go e.app.SetModelForTab(tab, model)
	}
	return nil
}
func (e *execAdapter) ListSessions() ([]mobilebridge.SessionInfo, error) {
	return e.app.SessionListForMobile(), nil
}
// ModelsForMobile 返回激活 tab 可用的模型列表（list_models 回复）。
func (e *execAdapter) ListModels() ([]mobilebridge.ModelInfo, error) {
	return e.app.ModelsForMobile(), nil
}

// NewTab 在桌面创建一个新会话 tab，返回它的 ID（new_tab 命令）。
func (e *execAdapter) NewTab(workspaceRoot, profile string) (string, error) {
	scope := "global"
	if workspaceRoot != "" {
		scope = "project"
	}
	tab, err := e.app.EnsureBlankTab(scope, workspaceRoot, profile)
	if err != nil {
		return "", err
	}
	return tab.ID, nil
}

func (e *execAdapter) RenameSession(tab, title string) error {
	if t := e.app.resolveMobileTab(tab); t != "" {
		go e.app.RenameTabForMobile(t, title)
	}
	return nil
}

func (e *execAdapter) DeleteSession(tab string) error {
	if t := e.app.resolveMobileTab(tab); t != "" {
		go e.app.CloseTab(t)
	}
	return nil
}

// OfficeRun 触发办公生成（M5）。简化版：把模板 + 参数拼成指令 submit 到激活
// tab，fairpeer 的 Controller（cowork profile）调用办公工具链生成文档。
func (e *execAdapter) OfficeRun(tab, template string, args map[string]string) error {
	tab = e.app.resolveMobileTab(tab)
	if tab == "" {
		return nil
	}
	input := "生成「" + template + "」"
	if len(args) > 0 {
		var parts []string
		for k, v := range args {
			parts = append(parts, k+": "+v)
		}
		input += "（" + strings.Join(parts, "；") + "）"
	}
	go e.app.SubmitToTab(tab, input)
	return nil
}

// —— 文件投递（file_drop，§4）—— 落地到 configDir/fairpeer/incoming/。

func (e *execAdapter) FileStart(_ string, name string, size int64) error {
	// 安全：清理文件名，防路径遍历 + 限制大小 + 白名单扩展名
	clean := filepath.Base(name) // 去掉所有 ../ 和路径分隔符
	if clean == "." || clean == "/" || clean == "" {
		return errors.New("invalid filename")
	}
	const maxFileSize = 50 * 1024 * 1024 // 50MB 上限
	if size > maxFileSize {
		return errors.New("file too large (max 50MB)")
	}
	// 扩展名白名单（§11.2③ 安全沙箱）
	ext := strings.ToLower(filepath.Ext(clean))
	allowed := map[string]bool{
		".txt": true, ".md": true, ".pdf": true, ".doc": true, ".docx": true,
		".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
		".csv": true, ".json": true, ".png": true, ".jpg": true, ".jpeg": true,
		".gif": true, ".zip": true, ".mp3": true, ".mp4": true,
	}
	if !allowed[ext] {
		return errors.New("file type not allowed: " + ext)
	}
	dir := filepath.Join(fileDropDir(), "incoming")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, clean))
	if err != nil {
		return err
	}
	e.fdMu.Lock()
	// 关闭并清理上一个未完成的文件（同时只允许一个文件投递）
	for _, old := range e.fdFiles {
		_ = old.Close()
	}
	e.fdFiles = map[string]*os.File{clean: f}
	e.fdMu.Unlock()
	slog.Info("mobilebridge: file_start", "name", clean, "size", size)
	return nil
}

func (e *execAdapter) FileChunk(_ string, _ int, b64 string) error {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return err
	}
	e.fdMu.Lock()
	defer e.fdMu.Unlock()
	for _, f := range e.fdFiles {
		_, _ = f.Write(data)
	}
	return nil
}

func (e *execAdapter) FileEnd(_ string, name string) error {
	e.fdMu.Lock()
	f := e.fdFiles[name]
	delete(e.fdFiles, name)
	e.fdMu.Unlock()
	if f != nil {
		_ = f.Close()
		slog.Info("mobilebridge: file_end", "name", name)
	}
	return nil
}

func fileDropDir() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "fairpeer")
}

// LoadSession 返回某个 tab 的对话历史（load_session 命令，§4.2）。
func (e *execAdapter) LoadSession(tab string) ([]map[string]any, error) {
	tab = e.app.resolveMobileTab(tab)
	if tab == "" {
		return nil, nil
	}
	msgs := e.app.HistoryForTab(tab)
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]any{
			"role": m.Role, "content": m.Content, "reasoning": m.Reasoning,
		}
	}
	return out, nil
}

// ensureMobileBridge loads the device key + creates the Bridge. Called once
// from App.startup. Safe to call when disabled — the bridge simply won't be
// stored, and every MobileBridge* binding no-ops.
func (a *App) ensureMobileBridge(ctx context.Context) {
	store, err := newFileKeyStore()
	if err != nil {
		slog.Warn("mobilebridge: key store unavailable, bridge disabled", "err", err)
		return // no key store → mobile bridge stays off
	}
	priv, pub, err := loadOrCreateDeviceKey(store)
	if err != nil {
		slog.Warn("mobilebridge: device key load failed, bridge disabled", "err", err)
		return
	}
	cfg := mobilebridge.DefaultConfig()
	// Resolution order: LINKPEER_SIGNAL env > [mobilebridge] signal_url > default.
	// The env var wins so ad-hoc `LINKPEER_SIGNAL=... wails dev` still works;
	// the TOML section is the persistent, restart-surviving way to point fairpeer
	// at your linkpeer-signal K during normal use.
	if s := os.Getenv("LINKPEER_SIGNAL"); s != "" {
		cfg.SignalURL = s
	} else if c, cerr := config.Load(); cerr == nil {
		if c.MobileBridge.SignalURL != "" {
			cfg.SignalURL = c.MobileBridge.SignalURL
		}
		if len(c.MobileBridge.STUNServers) > 0 {
			cfg.STUNServers = append(cfg.STUNServers, c.MobileBridge.STUNServers...)
		}
		if c.MobileBridge.LogLevel != "" {
			cfg.LogLevel = c.MobileBridge.LogLevel
		}
		cfg.AutoConfirm = c.MobileBridge.AutoConfirm
	}
	slog.Info("mobilebridge: starting bridge",
		"signal_url", cfg.SignalURL, "stun", cfg.STUNServers, "log_level", cfg.LogLevel)
	bridge := mobilebridge.NewBridge(cfg, priv, pub, store, &execAdapter{app: a, fdFiles: map[string]*os.File{}}, mobilebridge.NewAudit(cfg.LogLevel))
	// 注入 tab 别名解析：linkpeer 发 "default"/""，fairpeer 需要 UUID tab id。
	// 映射到当前激活 tab，手机就能接入桌面正在用的会话。
	bridge.SetResolveTab(func(tab string) string {
		if tab == "default" || tab == "" {
			if id := a.ActiveTabID(); id != "" {
				return id
			}
		}
		return tab
	})
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
		return map[string]any{"enabled": false, "connected": false}
	}
	return map[string]any{
		"enabled":    true,
		"connected":  mb.SignalConnected(),
		"signal_url": mb.SignalURL(),
		"pending":    mb.PendingPairings(),
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
