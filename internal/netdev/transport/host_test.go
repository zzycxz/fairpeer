package transport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in         string
		user, host string
		port       int
	}{
		{"host.example", "", "host.example", 0},
		{"user@host.example", "user", "host.example", 0},
		{"user@host.example:2222", "user", "host.example", 2222},
		{"host.example:22", "", "host.example", 22},
		{"[::1]", "", "::1", 0},
		{"[::1]:2200", "", "::1", 2200},
		{"2001:db8::1", "", "2001:db8::1", 0},
	}
	for _, c := range cases {
		user, host, port, err := ParseTarget(c.in)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", c.in, err)
		}
		if user != c.user || host != c.host || port != c.port {
			t.Fatalf("ParseTarget(%q) = %q,%q,%d; want %q,%q,%d",
				c.in, user, host, port, c.user, c.host, c.port)
		}
	}
	for _, bad := range []string{"", "@host", "user@", "host:0", "host:99999", "[::1", "host:abc"} {
		if _, _, _, err := ParseTarget(bad); err == nil {
			t.Fatalf("ParseTarget(%q) unexpectedly succeeded", bad)
		}
	}
}

func testLookup(table map[string]HostEntry) LookupEntry {
	return func(name string) (HostEntry, bool) {
		e, ok := table[name]
		return e, ok
	}
}

func TestResolveHostEntryPrecedence(t *testing.T) {
	lookup := testLookup(map[string]HostEntry{
		"core": {
			Name: "core", Host: "10.0.0.1", User: "netops", Port: 2222,
			IdentityFile: `~/.ssh/id_core`, ProxyJump: "l1,none,l2",
		},
	})
	r, err := ResolveHost(lookup, "core", nil)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if r.HostName != "10.0.0.1" || r.User != "netops" || r.Port != 2222 {
		t.Fatalf("resolved = %+v", r)
	}
	if len(r.ProxyJump) != 2 || r.ProxyJump[0] != "l1" || r.ProxyJump[1] != "l2" {
		t.Fatalf("jump chain = %v, want [l1 l2] (none dropped)", r.ProxyJump)
	}
	// IdentityFile home expansion happens at auth time, but the field is kept.
	if r.IdentityFile == "" {
		t.Fatalf("identity file lost")
	}
}

func TestResolveHostAdHocDefaults(t *testing.T) {
	r, err := ResolveHost(nil, "admin@10.9.9.9:2022", nil)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	if r.User != "admin" || r.HostName != "10.9.9.9" || r.Port != 2022 {
		t.Fatalf("resolved = %+v", r)
	}
	if r.ProxyJump != nil {
		t.Fatalf("ad-hoc target has no jump chain")
	}
}

func TestResolveJumpHostsClearsNestedChain(t *testing.T) {
	lookup := testLookup(map[string]HostEntry{
		"l2": {Name: "l2", Host: "10.2.0.1", ProxyJump: "l1"},
	})
	hops, err := ResolveJumpHosts(lookup, []string{"l1", "l2"}, nil)
	if err != nil {
		t.Fatalf("ResolveJumpHosts: %v", err)
	}
	if len(hops) != 2 {
		t.Fatalf("hops = %d, want 2", len(hops))
	}
	if hops[1].ProxyJump != nil {
		t.Fatalf("nested ProxyJump must be cleared: %v", hops[1].ProxyJump)
	}
}

// writeSSHConfig writes an isolated ssh config and returns a source with the
// OpenSSH executable path disabled so the embedded parser is exercised.
func writeSSHConfig(t *testing.T, dir, contents string) *SSHConfigSource {
	t.Helper()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := LoadSSHConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	src.resolveOpenSSH = nil // force the embedded parser fallback
	return src
}

func TestSSHConfigParserEffective(t *testing.T) {
	dir := t.TempDir()
	src := writeSSHConfig(t, dir, `
Host core-sw
  HostName 10.0.0.1
  User netops
  Port 2222
  IdentityFile ~/.ssh/id_core

Host *
  User fallback
`)
	eff := src.Effective("core-sw")
	if eff.HostName != "10.0.0.1" || eff.User != "netops" || eff.Port != 2222 {
		t.Fatalf("effective = %+v", eff)
	}
	if len(eff.IdentityFiles) != 1 {
		t.Fatalf("identity files = %v", eff.IdentityFiles)
	}
	// Wildcard-only fallback for an undeclared host.
	eff = src.Effective("other")
	if eff.User != "fallback" {
		t.Fatalf("wildcard user = %q, want fallback", eff.User)
	}
	if eff.HostName != "other" {
		t.Fatalf("hostname defaults to alias, got %q", eff.HostName)
	}
}

func TestSSHConfigAliasDiscoveryWithInclude(t *testing.T) {
	dir := t.TempDir()
	// OpenSSH resolves relative Include paths against ~/.ssh, so the test uses
	// an absolute path to stay independent of the real home directory.
	included := filepath.Join(dir, "extra")
	if err := os.WriteFile(included, []byte("Host from-include\n  HostName 10.9.9.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := writeSSHConfig(t, dir, "Include "+included+"\nHost direct\n  HostName 10.0.0.2\n")
	aliases := src.Aliases()
	want := map[string]bool{"direct": true, "from-include": true}
	if len(aliases) != len(want) {
		t.Fatalf("aliases = %+v, want both direct and included", aliases)
	}
	for _, a := range aliases {
		if !want[a.Alias] {
			t.Fatalf("unexpected alias %q", a.Alias)
		}
	}
	if src.HasAlias("from-include") != true {
		t.Fatalf("HasAlias(from-include) = false, want true")
	}
	if src.HasAlias("nope") {
		t.Fatalf("HasAlias(nope) = true")
	}
}

func TestSSHConfigLayeringUnderEntry(t *testing.T) {
	dir := t.TempDir()
	src := writeSSHConfig(t, dir, "Host sw\n  User sshuser\n  Port 2022\n")
	lookup := testLookup(map[string]HostEntry{
		"core": {Name: "core", Host: "sw", UseSSHConfig: true},
	})
	r, err := ResolveHost(lookup, "core", src)
	if err != nil {
		t.Fatalf("ResolveHost: %v", err)
	}
	// Host stays as the alias lookup key but the network target comes from
	// ssh_config; unset fields are filled.
	if r.HostName != "sw" {
		t.Fatalf("hostname = %q, want alias kept as lookup key", r.HostName)
	}
	if r.User != "sshuser" || r.Port != 2022 {
		t.Fatalf("layered fields = %q/%d, want sshuser/2022", r.User, r.Port)
	}
}
