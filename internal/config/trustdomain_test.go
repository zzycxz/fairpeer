package config

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestTrustDomainConfigParse(t *testing.T) {
	src := `
[trustdomain]
enabled = true
signal_url = "wss://signal.example.com/knock"
domain_id = "abc123"
data_dir = "/tmp/td-test"
bootstrap_peers = ["10.0.0.21:7123", "10.0.0.22:7123"]
admins = ["T4AIJ9eeNHDlcVT3Ob1wgDcLbPpB0yeJBEkXq9Z8X0g="]
quorum_m = 3
attest_interval_sec = 30
checkpoint_every_blocks = 20
tick_interval_sec = 2
`
	var c Config
	if err := toml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	td := c.TrustDomain
	if !td.Enabled || td.SignalURL != "wss://signal.example.com/knock" || td.DomainID != "abc123" {
		t.Fatalf("core fields: %+v", td)
	}
	if len(td.BootstrapPeers) != 2 || td.QuorumM != 3 {
		t.Fatalf("lists/quorum: %+v", td)
	}
	if err := td.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if got := td.AttestIntervalOrDefault(); got != 30 {
		t.Fatalf("attest interval = %d, want 30", got)
	}
	if got := td.TickIntervalOrDefault(); got != 2 {
		t.Fatalf("tick interval = %d, want 2", got)
	}
	if td.DataDirOrDefault() != "/tmp/td-test" {
		t.Fatalf("explicit data_dir not honored: %s", td.DataDirOrDefault())
	}
}

func TestTrustDomainConfigValidateRejectsBadAdminKey(t *testing.T) {
	td := TrustDomainConfig{Enabled: true, Admins: []string{"not-base64!!!"}}
	err := td.Validate()
	if err == nil || !strings.Contains(err.Error(), "admin key") {
		t.Fatalf("bad admin key accepted: %v", err)
	}

	// A disabled section may hold anything (users pre-stage config).
	td.Enabled = false
	if err := td.Validate(); err != nil {
		t.Fatalf("disabled section must not validate: %v", err)
	}
}

func TestTrustDomainDefaults(t *testing.T) {
	td := TrustDomainConfig{}
	if got := td.AttestIntervalOrDefault(); got != 60 {
		t.Fatalf("default attest interval = %d, want 60", got)
	}
	if got := td.TickIntervalOrDefault(); got != 5 {
		t.Fatalf("default tick interval = %d, want 5", got)
	}
	if dir := td.DataDirOrDefault(); dir == "" || !strings.HasSuffix(dir, "trustdomain") {
		t.Fatalf("default data dir = %q, want .../trustdomain", dir)
	}
}
