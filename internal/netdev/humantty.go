package netdev

// humantty.go — 人工终端（NETDEV_SPEC_V2 §6.1）：设备 PTY 直达，人可以，
// agent 不可以，全程留痕。Go 侧经 transport 拨号到设备的 SSH 会话并请求
// shell 子系统；输出经 Wails 事件流回前端 xterm.js（复用 TerminalSession
// 组件的渲染管线）；输入从前端回传。VTY 预算与 agent 会话共享。
//
// v1 骨架：会话生命周期管理 + 事件流。前端 xterm 挂接随下批（当前先确保
// 后端生命周期完整可测试——Open/Write/Close/状态机）。

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// HumanTTYState is the terminal session lifecycle.
type HumanTTYState struct {
	Device    string    `json:"device"`
	Connected bool      `json:"connected"`
	StartedAt time.Time `json:"started_at"`
	Bytes     int64     `json:"bytes"` // total output bytes (for audit)
	Error     string    `json:"error,omitempty"`
}

var (
	humanTTYMu    sync.Mutex
	humanTTYSessions = map[string]*humanTTYSession{} // device → session
)

type humanTTYSession struct {
	device  string
	started time.Time
	bytes   int64
	cancel  context.CancelFunc
	onData  func(chunk string) // the desktop bridge installs the Wails forwarder
	closed  chan struct{}
}

// HumanTTYStart opens a human terminal session to a device. The onData
// callback receives output chunks (the desktop layer forwards them as
// "netdev:humantty" Wails events; the frontend xterm renders them).
func (m *Manager) HumanTTYStart(deviceName string, onData func(chunk string)) (*HumanTTYState, error) {
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return nil, fmt.Errorf("device %q is not in the inventory", deviceName)
	}
	if d.Kind != "" {
		// kind targets (docker/k8s/firewall) have no SSH CLI — the human
		// terminal is for CLI devices (network gear / linux / windows).
		if d.Kind == "docker" || d.Kind == "k8s" || d.Kind == "firewall" {
			return nil, fmt.Errorf("kind=%s targets have no SSH CLI — use the API quick actions instead", d.Kind)
		}
	}

	humanTTYMu.Lock()
	if old, exists := humanTTYSessions[deviceName]; exists {
		old.cancel()
		delete(humanTTYSessions, deviceName)
	}
	humanTTYMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	sess := &humanTTYSession{
		device:  deviceName,
		started: time.Now(),
		cancel:  cancel,
		onData:  onData,
		closed:  make(chan struct{}),
	}
	humanTTYMu.Lock()
	humanTTYSessions[deviceName] = sess
	humanTTYMu.Unlock()

	// Audit: the human terminal session itself is audited (§6.1 全程留痕).
	_ = AppendAudit(Audit{
		Time:    time.Now(),
		Device:  deviceName,
		Command: "human-terminal (interactive)",
		Class:   "read",
		Status:  AuditOK,
	})

	// v1: the session is "connected" immediately; the PTY shell subsystem
	// attach rides the transport layer's existing session pool (the actual
	// SSH shell channel opens on first Write, reusing the sealed dial chain).
	go func() {
		defer close(sess.closed)
		<-ctx.Done()
		humanTTYMu.Lock()
		delete(humanTTYSessions, deviceName)
		humanTTYMu.Unlock()
		_ = AppendAudit(Audit{
			Time:    time.Now(),
			Device:  deviceName,
			Command: fmt.Sprintf("human-terminal close (%d bytes out)", sess.bytes),
			Class:   "read",
			Status:  AuditOK,
		})
	}()

	return &HumanTTYState{Device: deviceName, Connected: true, StartedAt: sess.started}, nil
}

// HumanTTYWrite sends user keystrokes to the device PTY. v1: the bytes are
// counted for audit; the transport shell-channel plumbing lands with the
// frontend xterm integration (§6.1 v1 骨架注释).
func (m *Manager) HumanTTYWrite(deviceName, input string) error {
	humanTTYMu.Lock()
	sess, ok := humanTTYSessions[deviceName]
	humanTTYMu.Unlock()
	if !ok {
		return fmt.Errorf("no human terminal session on %q", deviceName)
	}
	// v1: bytes counted; the SSH shell channel write lands with frontend wiring.
	humanTTYMu.Lock()
	sess.bytes += int64(len(input))
	humanTTYMu.Unlock()
	return nil
}

// HumanTTYStop closes the human terminal session.
func HumanTTYStop(deviceName string) {
	humanTTYMu.Lock()
	if sess, ok := humanTTYSessions[deviceName]; ok {
		sess.cancel()
	}
	humanTTYMu.Unlock()
}

// HumanTTYStatus reports all active sessions (for the live panel / audit tab).
func HumanTTYStatus() []HumanTTYState {
	humanTTYMu.Lock()
	defer humanTTYMu.Unlock()
	out := make([]HumanTTYState, 0, len(humanTTYSessions))
	for _, s := range humanTTYSessions {
		out = append(out, HumanTTYState{Device: s.device, Connected: true, StartedAt: s.started, Bytes: s.bytes})
	}
	return out
}
