package linkpeersignal

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// Config is the signal.toml structure (PROTOCOL §10.4).
type Config struct {
	Server  ServerConfig  `toml:"server"`
	Pair    PairConfig    `toml:"pair"`
	Session SessionConfig `toml:"session"`
	STUN    STUNConfig    `toml:"stun"`
	Log     LogConfig     `toml:"log"`
}

type ServerConfig struct {
	Listen      string `toml:"listen"`
	PublicRelay string `toml:"public_relay"`
}

type PairConfig struct {
	CodeTTL             int `toml:"code_ttl"`
	MaxFailPerPair      int `toml:"max_fail_per_pair"`
	MaxFailPerIPPerHour int `toml:"max_fail_per_ip_per_hour"`
	MaxPerDevPerHour    int `toml:"max_per_dev_per_hour"`
	MaxConcurrentPerDev int `toml:"max_concurrent_per_dev"`
	MaxGlobal           int `toml:"max_global"`
}

type SessionConfig struct {
	WSMsgPerSecPerDev int `toml:"ws_msg_per_sec_per_dev"`
	WSMaxMsgBytes     int `toml:"ws_max_msg_bytes"`
	OfferTSSkew       int `toml:"offer_ts_skew"`
	IdleTimeout       int `toml:"idle_timeout"` // seconds; peer unseen this long is reaped
	MaxPeers          int `toml:"max_peers"`    // 在线 WS 硬上限（§11.2⑤，默认 50000）
}

type STUNConfig struct {
	Servers []string `toml:"servers"`
}

type LogConfig struct {
	Level string `toml:"level"`
}

// DefaultConfig returns sane defaults matching the spec (PROTOCOL §10.4).
// These are tuned for the 5000-peer scale (ENGINEERING §10.6).
func DefaultConfig() Config {
	return Config{
		Server: ServerConfig{Listen: "127.0.0.1:8080"},
		Pair: PairConfig{
			CodeTTL: 60, MaxFailPerPair: 5,
			MaxFailPerIPPerHour: 200, MaxPerDevPerHour: 5,
			MaxConcurrentPerDev: 3, MaxGlobal: 50000,
		},
		Session: SessionConfig{
			WSMsgPerSecPerDev: 50, WSMaxMsgBytes: 32768,
			OfferTSSkew: 60, IdleTimeout: 90, MaxPeers: 50000,
		},
		Log: LogConfig{Level: "info"},
	}
}

// LoadConfig decodes a TOML file onto the defaults. Empty path → defaults.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.Pair.CodeTTL <= 0 || c.Pair.MaxFailPerPair <= 0 || c.Pair.MaxGlobal <= 0 {
		return fmt.Errorf("invalid pair config")
	}
	if c.Pair.MaxPerDevPerHour <= 0 || c.Pair.MaxFailPerIPPerHour <= 0 {
		return fmt.Errorf("invalid rate limit config")
	}
	if c.Session.WSMaxMsgBytes <= 0 || c.Session.IdleTimeout <= 0 {
		return fmt.Errorf("invalid session config")
	}
	return nil
}
