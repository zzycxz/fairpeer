package netdev

import (
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
)

// The console session has no ssh.Session behind it — Close must be nil-safe
// (it paniced on the first cut), and a bare Session with only a stdin line
// must construct cleanly.
func TestConsoleSessionCloseNilSSH(t *testing.T) {
	s := &Session{drv: nil, stdin: nil, out: &syncBuffer{}}
	if err := s.Close(); err != nil {
		t.Fatalf("Close on a console-shaped session: %v", err)
	}
	if err := s.Close(); err != nil { // idempotent
		t.Fatalf("second Close: %v", err)
	}
}

// Validation-shape checks that live next to the console wiring: the config
// layer owns the full ValidateNetDev; here we pin the console branches.
func TestConsoleConfigValidation(t *testing.T) {
	base := func(mut func(*config.NetDevDevice)) config.NetDevConfig {
		d := config.NetDevDevice{Name: "sw1", Vendor: "huawei", OS: "vrp8", Address: "10.0.0.1", ConsolePort: "COM3"}
		mut(&d)
		return config.NetDevConfig{Enabled: true, Devices: []config.NetDevDevice{d}}
	}
	if err := config.ValidateNetDev(base(func(d *config.NetDevDevice) {})); err != nil {
		t.Fatalf("plain console device should validate: %v", err)
	}
	if err := config.ValidateNetDev(base(func(d *config.NetDevDevice) { d.Address = "" })); err != nil {
		t.Fatalf("console device without an address should validate: %v", err)
	}
	if err := config.ValidateNetDev(base(func(d *config.NetDevDevice) { d.ConsolePort = "COM3; rm -rf /" })); err == nil {
		t.Fatal("console_port with metacharacters must be refused")
	}
	if err := config.ValidateNetDev(base(func(d *config.NetDevDevice) { d.Vendor = "snmp" })); err == nil {
		t.Fatal("console on a vendor without a CLI driver must be refused")
	}
	if err := config.ValidateNetDev(base(func(d *config.NetDevDevice) { d.Kind = "docker" })); err == nil {
		t.Fatal("console on an API-plane kind must be refused")
	}
	if err := config.ValidateNetDev(base(func(d *config.NetDevDevice) { d.ConsoleBaud = 1000000 })); err == nil {
		t.Fatal("console_baud out of range must be refused")
	}
	// Non-console devices still need an address.
	nd := base(func(d *config.NetDevDevice) { d.ConsolePort = ""; d.Address = "" })
	if err := config.ValidateNetDev(nd); err == nil {
		t.Fatal("SSH device without an address must still be refused")
	}
}

// Engagement-envelope validation (评估授权信封): partial envelopes refused,
// full envelopes pass, date format enforced.
func TestAssessmentEnvelopeValidation(t *testing.T) {
	base := func(a config.NetDevAssessment) config.NetDevConfig {
		return config.NetDevConfig{Enabled: true, Devices: []config.NetDevDevice{{Name: "sw1", Vendor: "huawei", OS: "vrp8", Address: "10.0.0.1"}}, Assessment: a}
	}
	if err := config.ValidateNetDev(base(config.NetDevAssessment{EngagementID: "ENG-1", Expires: "2099-01-01", Approver: "张三"})); err != nil {
		t.Fatalf("full envelope should validate: %v", err)
	}
	if err := config.ValidateNetDev(base(config.NetDevAssessment{EngagementID: "ENG-1"})); err == nil {
		t.Fatal("envelope without expiry must be refused")
	}
	if err := config.ValidateNetDev(base(config.NetDevAssessment{Expires: "2099-01-01"})); err == nil {
		t.Fatal("envelope without engagement_id must be refused")
	}
	if err := config.ValidateNetDev(base(config.NetDevAssessment{EngagementID: "ENG-1", Expires: "2099/01/01"})); err == nil {
		t.Fatal("non-YYYY-MM-DD expiry must be refused")
	}
}
