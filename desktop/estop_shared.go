package main

// estop_shared.go — platform-neutral parts of the emergency-stop feature
// (config resolution + frontend notice), shared by the Win32 RegisterHotKey
// implementation and the unix polling implementation.

import (
	"runtime"
	"strings"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/zzycxz/fairpeer/internal/config"
)

const defaultEStopHotkey = "Ctrl+Shift+Pause"

// darwinEStopHotkey — the trailing G exists only to satisfy the shared hotkey
// parser (it requires a main key); darwin detection matches the chord alone.
// A 3-modifier chord is deliberate: macOS keyboards have no Pause, and the
// Windows default would degenerate into "hold Ctrl+Shift ~500 ms" — a
// false-cancel trap.
const darwinEStopHotkey = "Ctrl+Alt+Cmd+G"

// estopHotkeyString returns the configured combo, defaulting to
// Ctrl+Shift+Pause (Ctrl+Alt+Cmd+G on macOS). Empty means disabled.
func (a *App) estopHotkeyString() string {
	cfg, err := config.Load()
	if err != nil {
		return defaultEStopHotkey
	}
	if cfg.Cowork.EStopHotkey == "off" || cfg.Cowork.EStopHotkey == "disabled" {
		return ""
	}
	if strings.TrimSpace(cfg.Cowork.EStopHotkey) == "" {
		if runtime.GOOS == "darwin" {
			return darwinEStopHotkey
		}
		return defaultEStopHotkey
	}
	return cfg.Cowork.EStopHotkey
}

// emitEStopNotice pushes a stop-confirmation toast to the frontend. The
// frontend renders this as a prominent red banner so the user sees the kill
// landed.
func (a *App) emitEStopNotice() {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "estop:fired", map[string]string{
		"message": "已紧急停止 AI 操作",
		"detail":  "全局热键触发的紧急停止已生效，进行中的任务被中断。",
	})
}
