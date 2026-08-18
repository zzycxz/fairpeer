package netdev

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/config"
	"github.com/zzycxz/fairpeer/internal/netdev/transport"
)

// testManager wires a Manager against the simulator with an isolated audit
// file and a stubbed secret store.
func testManager(t *testing.T, sim *simDevice) (*Manager, string) {
	t.Helper()
	host, portStr, _ := net.SplitHostPort(sim.addr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	cfg := config.Default()
	cfg.NetDev = config.NetDevConfig{
		Enabled: true,
		Devices: []config.NetDevDevice{{
			Name: "sw1", Vendor: "huawei", OS: "vrp8",
			Address: host, Port: port, Username: "admin",
			PasswordEnv: "TEST_ENV",
		}, {
			// Points at a closed port: a refused (pre-connection) command must
			// never even try to dial it.
			Name: "dead", Vendor: "huawei", OS: "vrp8",
			Address: "127.0.0.1", Port: 1, Username: "admin",
			PasswordEnv: "TEST_ENV",
		}},
	}

	orig := secretGetter
	secretGetter = func(kind, name string) (string, bool, error) {
		if name == "TEST_ENV" {
			return sim.password, true, nil
		}
		return "", false, nil
	}
	t.Cleanup(func() { secretGetter = orig })

	// TOFU auto-accept with an isolated managed file (strict mode is the
	// production default; tests exercise the prompt path).
	origPrompt := HostKeyPrompt
	HostKeyPrompt = func(ctx context.Context, q transport.HostKeyQuestion) (bool, error) { return true, nil }
	t.Cleanup(func() { HostKeyPrompt = origPrompt })

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	SetAuditPath(auditPath)
	t.Cleanup(func() { SetAuditPath("") })

	m := NewManager(cfg)
	t.Cleanup(m.Close)
	return m, auditPath
}

func readAudit(t *testing.T, path string) []Audit {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []Audit
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e Audit
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("bad audit line: %v", err)
		}
		out = append(out, e)
	}
	return out
}

func TestExecReadCommand(t *testing.T) {
	sim := startSimDevice(t)
	m, auditPath := testManager(t, sim)

	res := m.Exec(context.Background(), "sw1", "display version")
	if res.Refused {
		t.Fatalf("read command refused: %s", res.Refusal)
	}
	if res.IsError || !strings.Contains(res.Output, "Version 8.180") {
		t.Fatalf("unexpected result: %+v", res)
	}

	entries := readAudit(t, auditPath)
	if len(entries) == 0 {
		t.Fatal("no audit entry written")
	}
	last := entries[len(entries)-1]
	if last.Device != "sw1" || last.Command != "display version" || last.Class != "read" || last.Status != AuditOK {
		t.Fatalf("audit entry = %+v", last)
	}
}

func TestExecRefusesWriteDangerousUnknown(t *testing.T) {
	sim := startSimDevice(t)
	m, auditPath := testManager(t, sim)

	cases := []struct {
		cmd, class string
	}{
		{"undo stp enable", "write"},
		{"system-view", "write"},
		{"reboot", "dangerous"},
		{"mystery-verb arg", "unknown"},
	}
	for _, c := range cases {
		res := m.Exec(context.Background(), "sw1", c.cmd)
		if !res.Refused {
			t.Errorf("%q not refused", c.cmd)
		}
		if res.Class != c.class {
			t.Errorf("%q class = %s, want %s", c.cmd, res.Class, c.class)
		}
		if res.Output != "" {
			t.Errorf("%q produced output despite refusal", c.cmd)
		}
	}
	// The refusals never connected (device "dead" has a closed port): a
	// refusal there must be the classifier message, not a dial error.
	res := m.Exec(context.Background(), "dead", "undo stp enable")
	if !res.Refused || !strings.Contains(res.Refusal, "structurally read-only") {
		t.Fatalf("refusal should precede dialing, got: %+v", res)
	}

	entries := readAudit(t, auditPath)
	if len(entries) != len(cases)+1 {
		t.Fatalf("audit entries = %d, want %d", len(entries), len(cases)+1)
	}
	for _, e := range entries {
		if e.Status != AuditRefused {
			t.Fatalf("audit status = %s, want refused (%+v)", e.Status, e)
		}
	}
}

func TestExecUnknownDeviceRefused(t *testing.T) {
	sim := startSimDevice(t)
	m, _ := testManager(t, sim)

	res := m.Exec(context.Background(), "not-in-inventory", "display version")
	if !res.Refused || !strings.Contains(res.Refusal, "not in the user-global netdev inventory") {
		t.Fatalf("unknown device not refused: %+v", res)
	}
}

// TestExecSealAcrossSession: the seal holds on a live session too — after a
// successful read, a write on the same cached session is still refused, and
// the session stays usable afterwards.
func TestExecSealAcrossSession(t *testing.T) {
	sim := startSimDevice(t)
	m, _ := testManager(t, sim)

	if res := m.Exec(context.Background(), "sw1", "display version"); res.Refused {
		t.Fatalf("setup read refused: %s", res.Refusal)
	}
	if res := m.Exec(context.Background(), "sw1", "delete vrpcfg.zip"); !res.Refused {
		t.Fatal("dangerous command executed on live session")
	}
	// Session still usable: a read command runs (unknown to the sim, so the
	// device flags an error — but it is NOT refused, i.e. the session works).
	if res := m.Exec(context.Background(), "sw1", "display clock"); res.Refused {
		t.Fatalf("session unusable after refusal: %+v", res)
	}
}
