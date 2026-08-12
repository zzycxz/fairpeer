package linkpeersignal

import (
	"log/slog"
	"os"
	"strings"
)

// Audit emits ONLY metadata to a JSON slog.Logger: devId (truncated), event
// type, timestamps, error codes. It NEVER logs pair codes, public keys, SDP,
// ICE candidates, or any business content. Log retention is 7 days (operational
// concern, handled by log rotation outside the process — SIGNAL_SPEC §13.7).
type Audit struct {
	log *slog.Logger
}

func NewAudit(level string) *Audit {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	return &Audit{log: slog.New(h)}
}

// truncDev shortens a devId to "first4...last3" so logs can't reconstruct
// the full identifier. devId is already a public-key hash, but minimizing
// exposure is defense-in-depth.
func truncDev(dev string) string {
	if len(dev) <= 7 {
		return dev
	}
	return dev[:4] + "..." + dev[len(dev)-3:]
}

// Generic leveled helpers (used by main for startup/shutdown messages).
func (a *Audit) Info(msg string, args ...any)  { a.log.Info(msg, args...) }
func (a *Audit) Warn(msg string, args ...any)  { a.log.Warn(msg, args...) }

func (a *Audit) PairRegister(dev, ip string) {
	a.log.Info("pair_register", "dev", truncDev(dev), "ip", ip)
}
func (a *Audit) PairExchange(dev, ip string, ok bool) {
	a.log.Info("pair_exchange", "dev", truncDev(dev), "ip", ip, "ok", ok)
}
func (a *Audit) WSConnect(dev, ip string) {
	a.log.Info("ws_connect", "dev", truncDev(dev), "ip", ip)
}
func (a *Audit) WSDisconnect(dev string) {
	a.log.Info("ws_disconnect", "dev", truncDev(dev))
}
func (a *Audit) RateLimit(dim, key string) {
	a.log.Warn("rate_limited", "dim", dim, "key", truncDev(key))
}
func (a *Audit) Error(evt, dev string, err error) {
	a.log.Error("error", "evt", evt, "dev", truncDev(dev), "err", err.Error())
}
