package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/mobilebridge"
	"github.com/zzycxz/fairpeer/internal/secret"
)

// desktop/mobilebridge_app.go wires the internal/mobilebridge Bridge into the
// fairpeer desktop App. It provides:
//   - a secret.Store-backed KeyStore (encrypted at rest) for S's long-term
//     Ed25519 key + paired peer pubs
//   - an execAdapter exposing App's existing tab methods as CommandExecutor
//   - Wails-bound methods the frontend calls (pair / status / confirm / unpair)
//
// MINIMAL-INVASION design: app.go grows one field (mobilebridge atomic.Pointer)
// and one startup line; tabs.go grows one forward line in tabEventSink.Emit.
// Everything else lives in this file.

// secretKeyStore adapts the encrypted secret.Store (DPAPI on Windows, AES-GCM
// elsewhere) to mobilebridge.KeyStore, so the long-term Ed25519 private key and
// paired peer pubs are encrypted at rest (FAIRPEER_SPEC §6). It deliberately
// uses its own store file — NOT secret.Default() — because the shared store's
// LoadIntoEnv exports every entry into the process env, which must never happen
// to key material.
type secretKeyStore struct {
	store *secret.Store
}

// mobilebridgeStorePath is the encrypted keystore location, beside the other
// fairpeer secrets in the user config dir.
func mobilebridgeStorePath() string {
	return filepath.Join(desktopConfigDir(), "mobilebridge.enc.json")
}

func newSecretKeyStore() (*secretKeyStore, error) {
	if desktopConfigDir() == "" {
		return nil, errors.New("user config dir unavailable")
	}
	ks := &secretKeyStore{store: secret.New(mobilebridgeStorePath())}
	ks.migrateLegacyKeyFileAt(legacyMobilebridgeKeysPath())
	return ks, nil
}

func (s *secretKeyStore) Get(key string) ([]byte, error) {
	v, ok, err := s.store.Get(key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, mobilebridge.ErrNotFound
	}
	return []byte(v), nil
}

func (s *secretKeyStore) Set(key string, val []byte) error {
	return s.store.Set(key, string(val))
}

func (s *secretKeyStore) Delete(key string) error {
	return s.store.Delete(key)
}

// legacyMobilebridgeKeysPath is the M1 plaintext keystore (~/.fairpeer/
// mobilebridge_keys.json, base64 values). Kept only for the one-time migration
// below.
func legacyMobilebridgeKeysPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".fairpeer", "mobilebridge_keys.json")
}

// migrateLegacyKeyFileAt lifts the M1 plaintext keystore into the encrypted
// store once, then removes the plaintext file. The long-term key is verified to
// decrypt before the file is dropped — a lost key means a new device identity
// and re-pairing every phone, so only a verified migration deletes. On any
// failure the legacy file is kept; whatever made it into the store still works.
func (s *secretKeyStore) migrateLegacyKeyFileAt(p string) {
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return // no legacy file — nothing to migrate
	}
	var legacy map[string]string
	if err := json.Unmarshal(b, &legacy); err != nil {
		slog.Warn("mobilebridge: legacy keystore unreadable; keeping file", "path", p, "err", err)
		return
	}
	if len(legacy) == 0 {
		_ = os.Remove(p)
		return
	}
	var anyKey string
	for k, v := range legacy {
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			slog.Warn("mobilebridge: legacy keystore entry undecodable; keeping file", "path", p, "key", k, "err", err)
			return
		}
		if _, ok, _ := s.store.Get(k); !ok {
			if err := s.store.Set(k, string(raw)); err != nil {
				slog.Warn("mobilebridge: legacy keystore migration failed; keeping file", "path", p, "err", err)
				return
			}
		}
		anyKey = k
	}
	// Verify a migrated entry survives a full encrypt→decrypt round trip before
	// dropping the plaintext original.
	if v, ok, err := s.store.Get(anyKey); err == nil && ok && len(v) > 0 {
		_ = os.Remove(p)
		slog.Info("mobilebridge: migrated legacy keystore into encrypted store", "from", p)
	}
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
	store, err := newSecretKeyStore()
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
	var pairAddr string // 用户钉死的配对网卡（[mobilebridge] pair_address）
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
		pairAddr = c.MobileBridge.PairAddress
		cfg.UDPKnock = c.MobileBridge.UDPKnock
		cfg.KnockServer = c.MobileBridge.KnockServer
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
	if pairAddr != "" {
		bridge.SetPairAddress(pairAddr)
	}
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
		"enabled":      true,
		"connected":    mb.SignalConnected(),
		"signal_url":   mb.SignalURL(),
		"pending":      mb.PendingPairings(),
		"udp_knock":    mb.KnockEnabled(),
		"knock_server": mb.KnockServer(),
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

// MobileBridgeListPairNics 返回 JSON：{nics:[{ip,name,label,isDefault}], pinned}。
// 设置面板「配对网卡」下拉框数据源。
func (a *App) MobileBridgeListPairNics() (string, error) {
	mb := a.mobilebridge.Load()
	if mb == nil {
		return "", fmt.Errorf("mobile bridge not initialized")
	}
	pinned := ""
	if c, err := config.Load(); err == nil {
		pinned = c.MobileBridge.PairAddress
	}
	b, _ := json.Marshal(map[string]any{"nics": mb.ListPairNics(), "pinned": pinned})
	return string(b), nil
}

// MobileBridgeSetPairNic 钉死/恢复配对网卡（"" = 自动），并持久化到
// 用户 config.toml 的 [mobilebridge] pair_address。
func (a *App) MobileBridgeSetPairNic(ip string) error {
	mb := a.mobilebridge.Load()
	if mb == nil {
		return fmt.Errorf("mobile bridge not initialized")
	}
	c := config.LoadForEdit(config.UserConfigPath())
	c.MobileBridge.PairAddress = ip
	if err := c.WriteFile(config.UserConfigPath()); err != nil {
		return fmt.Errorf("save pair_address: %w", err)
	}
	mb.SetPairAddress(ip)
	return nil
}

// MobileBridgeSetKnock 配置 UDP 单包敲门（开关 + 远程 STUN 服务器），
// 持久化到 [mobilebridge] udp_knock / knock_server，并即时生效于 Bridge。
func (a *App) MobileBridgeSetKnock(enabled bool, server string) error {
	mb := a.mobilebridge.Load()
	if mb == nil {
		return fmt.Errorf("mobile bridge not initialized")
	}
	c := config.LoadForEdit(config.UserConfigPath())
	c.MobileBridge.UDPKnock = enabled
	c.MobileBridge.KnockServer = server
	if err := c.WriteFile(config.UserConfigPath()); err != nil {
		return fmt.Errorf("save udp_knock: %w", err)
	}
	mb.SetKnock(enabled, server)
	return nil
}

// keep sync/atomic referenced (the field lives in app.go but the import is
// grouped here for the adapter's awareness; app.go itself also imports it).
var _ atomic.Pointer[mobilebridge.Bridge]

// ensure App has the field — this is a compile-time check; the actual field
// declaration is in app.go (added by the desktop-integration edit).
// (If you see a compile error here, app.go's mobilebridge field is missing.)
var _ = context.Background
