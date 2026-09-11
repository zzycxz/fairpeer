package main

// notify_long.go sends OS notifications for scheduled-task reminders.
//
// On Windows it fires a long-duration (25s) toast via go-toast, instead of the
// ~7s default that Wails' runtime.SendNotification uses. The user asked for
// reminders that stay on screen long enough to actually read (and persist to
// Action Center), not a 7-second flash that's easy to miss.
//
// We bypass Wails' SendNotification (which hardcodes Duration=Short and
// exposes no override) and call go-toast directly. This reuses the AUMID/COM
// registration that Wails' InitializeNotifications already performed at startup
// (it calls toast.SetAppData with AppID = exe base name), so we must match that
// same AppID here.
//
// Off Windows go-toast's Push is a bind_noop (it renders nothing), so the
// reminder would silently vanish. There we fall back to Wails' cross-platform
// notification (short duration, but visible on macOS/Linux notification
// centers). The in-app "scheduler:notice" event emit is unaffected — it is the
// caller's job and stays primary everywhere.

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// notifyLongDurationToast fires the OS notification for a scheduled-task
// reminder. It is best-effort: errors are swallowed by the caller. ctx is the
// Wails context (a.ctx) needed by the non-Windows path; nil skips the OS
// notification (the in-app emit already fired).
//
// Windows path: a 25-second ("long") toast with an explicit "知道了" dismiss
// button. AppID matches Wails' InitializeNotifications (filepath.Base of the
// executable) so it shares the same registry/COM registration and the toast
// attributes correctly to fairpeer.
//
// The dismiss Action uses Foreground activation so clicking it both closes the
// toast AND routes through Wails' OnNotificationResponse (registered in startup
// → showMainWindow), bringing the window forward. Windows always shows a hover-X
// too, but the labeled button is far more discoverable for "I've seen it, close".
func notifyLongDurationToast(ctx context.Context, title, body string) {
	if goruntime.GOOS == "windows" {
		notifyWindowsLongToast(title, body)
		return
	}
	// go-toast is a no-op off Windows — use Wails' cross-platform notification
	// (short duration is the only loss vs the Windows path). Recover-guarded:
	// Wails' runtime calls panic on a dead/nil context, and this fires from a
	// goroutine so a panic would take the whole app down.
	if ctx == nil {
		return
	}
	defer func() { _ = recover() }()
	_ = runtime.SendNotification(ctx, runtime.NotificationOptions{Title: title, Body: body})
}

// notifyWindowsLongToast fires the long toast via go-toast (Windows only).
func notifyWindowsLongToast(title, body string) {
	n := toast.Notification{
		AppID:               appExeBaseName(),
		Title:               title,
		Body:                body,
		Duration:            toast.Long, // ~25s on screen (vs ~7s Short), then Action Center
		ActivationType:      toast.Foreground,
		ActivationArguments: "default",
		Actions: []toast.Action{
			{
				Type:      toast.Foreground,
				Content:   "知道了",
				Arguments: "dismiss",
			},
		},
	}
	_ = n.Push()
}

// appExeBaseName returns the running executable's base name (e.g. "fairpeer.exe"),
// matching what Wails' InitializeNotifications uses as the toast AppID. Falls
// back to "fairpeer.exe" if the path can't be resolved (keeps the toast working
// rather than failing with an empty AppID, which Windows would suppress).
func appExeBaseName() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Base(exe)
	}
	return "fairpeer.exe"
}
