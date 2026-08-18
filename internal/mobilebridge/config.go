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

	// UDPKnock 单包敲门（M3 NAT 穿透辅助，默认关）：ICE 建连前 S 从 ICE
	// 同一 UDP socket 向 C 的 srflx 公网映射发敲门包，提前打开 S 侧 NAT，
	// 让 C 的 connectivity check 能进来。对 cone NAT 有效；双对称 NAT 无解
	// （PROTOCOL §7）。KnockServer 是敲门依赖的远程 STUN 服务器——两端
	// 靠它学到各自公网映射（srflx 候选），没有它敲门无目标可敲。
	UDPKnock    bool
	KnockServer string // e.g. "stun:stun.example.com:3478"；空 = 不追加
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
