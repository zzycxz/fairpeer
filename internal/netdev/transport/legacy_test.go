package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// newLegacySSHTestServer is newSSHTestServer pinned to ONLY the legacy
// algorithms (aes128-cbc cipher + dh-group1-sha1 KEX) that x/crypto implements
// but does not offer by default — the shape of an ancient Cisco IOS 12.x /
// early VRP5 box. A default-options client cannot negotiate with it at all,
// which is exactly the failure the per-device legacy_algo knob exists for.
func newLegacySSHTestServer(t *testing.T, password string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		Config: ssh.Config{
			Ciphers:      []string{ssh.InsecureCipherAES128CBC},
			KeyExchanges: []string{ssh.InsecureKeyExchangeDH1SHA1},
		},
		PasswordCallback: func(conn ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == password {
				return nil, nil
			}
			return nil, errors.New("invalid password")
		},
	}
	cfg.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go serveSSH(listener, cfg, password)
	return listener.Addr().String()
}

// serveSSH mirrors the accept loop of newSSHTestServer for session channels
// enough for a single Exec("echo hi").
func serveSSH(listener net.Listener, cfg *ssh.ServerConfig, password string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
		if err != nil {
			continue
		}
		go func() {
			defer sconn.Close()
			for newChan := range chans {
				if newChan.ChannelType() != "session" {
					newChan.Reject(ssh.UnknownChannelType, "unsupported")
					continue
				}
				ch, chReqs, err := newChan.Accept()
				if err != nil {
					continue
				}
				go func() {
					defer ch.Close()
					for req := range chReqs {
						if req.Type != "exec" {
							if req.WantReply {
								_ = req.Reply(false, nil)
							}
							continue
						}
						if req.WantReply {
							_ = req.Reply(true, nil)
						}
						_, _ = ch.Write([]byte("hi\n"))
						_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						// Return (not continue): the deferred ch.Close() is what
						// unblocks the client's Exec — staying in the loop hangs it.
						return
					}
				}()
			}
		}()
		go ssh.DiscardRequests(reqs)
	}
}

// TestLegacyAlgoConnectsToLegacyOnlyServer proves the per-device opt-in
// actually negotiates with a legacy-only peer: without the knob the handshake
// fails with the actionable hint; with it, connect + exec succeed.
func TestLegacyAlgoConnectsToLegacyOnlyServer(t *testing.T) {
	addr := newLegacySSHTestServer(t, "pw")
	host, port := splitHostPort(t, addr)

	mk := func(legacy bool) *Client {
		c, err := New(Options{
			Host:     ResolvedHost{HostName: host, Port: port, User: "u", LegacyAlgo: legacy},
			Auth:     AuthOptions{Password: func() (string, error) { return "pw", nil }},
			HostKeys: acceptAllPolicy(t),
		})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	// Without the knob: handshake fails, and the error carries the remediation
	// hint instead of the raw x/crypto "no common algorithm" string.
	bare := mk(false)
	if err := bare.Start(context.Background()); err == nil {
		bare.Close()
		t.Fatal("default-algo client connected to a legacy-only server — the modern offer must not include aes128-cbc/dh-group1")
	} else if !strings.Contains(err.Error(), "legacy_algo") {
		t.Fatalf("default-algo error lacks the legacy_algo hint: %v", err)
	}

	// With the knob: full connect + exec.
	c := mk(true)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("legacy dial: %v", err)
	}
	defer c.Close()
	if got := c.Status().Status; got != StatusConnected {
		t.Fatalf("status = %v, want connected", got)
	}
	res, err := c.Exec(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(string(res.Stdout), "hi") {
		t.Fatalf("stdout = %q, want hi", res.Stdout)
	}
}
