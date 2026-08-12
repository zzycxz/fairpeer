package mobilebridge

// Config is the [mobilebridge] section of fairpeer.toml. The desktop
// integration layer (desktop/app.go) fills this from fairpeer's config load
// and passes it to Bridge — mobilebridge itself never reads TOML, keeping it
// independent of fairpeer's config internals. See FAIRPEER_SPEC §5.
type Config struct {
	Enabled         bool
	SignalURL       string   // e.g. "wss://signal.example.com"
	STUNServers     []string // e.g. ["stun:signal.example.com:3478"]
	TURNEnabled     bool     // opt-in relay (default off, pure-P2P)
	TURNServers     []string
	UPnP            bool // probe router for port mapping
	ReadOnlyDefault bool // new peers default to read-only
	RequireApproval bool // mobile submits require desktop approval
	AllowFileDrop   bool // allow phone→desktop file delivery
	AllowHighRisk   bool // allow triggering shell/exec via mobile
	MaxConnections  int  // concurrent C connections
	LogLevel        string
	AutoConfirm     bool // 联调：收到 exchange 立即自动确认（不等用户点允许）
}

// DefaultConfig matches FAIRPEER_SPEC §5 defaults. The SignalURL placeholder
// is overridden by the QR code's relay field at pairing time per device.
func DefaultConfig() Config {
	return Config{
		SignalURL:      "wss://signal.linkpeer.app",
		STUNServers:    []string{"stun:signal.linkpeer.app:3478"},
		UPnP:           true,
		AllowFileDrop:  true,
		MaxConnections: 4,
		LogLevel:       "info",
	}
}

// PerConnPermissions is what one connected C may do, derived from Config
// defaults and per-device overrides (set via the Wails MobileBridgeSetReadOnly
// binding). command_router enforces these on every inbound command.
type PerConnPermissions struct {
	ReadOnly        bool
	RequireApproval bool
	AllowFileDrop   bool
	AllowHighRisk   bool
}

// DefaultPermissions snapshots the config-level defaults for a new peer.
func (c Config) DefaultPermissions() PerConnPermissions {
	return PerConnPermissions{
		ReadOnly:        c.ReadOnlyDefault,
		RequireApproval: c.RequireApproval,
		AllowFileDrop:   c.AllowFileDrop,
		AllowHighRisk:   c.AllowHighRisk,
	}
}
