package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// newSSHTestServer starts an in-process SSH server with password auth. When
// forwardTCP is true it also serves direct-tcpip channels (jump-host duty):
// each forwarded channel is bridged to the requested address dialed from this
// process. Returns the server's public host key and its address.
func newSSHTestServer(t *testing.T, password string, forwardTCP bool) (hostKey ssh.PublicKey, addr string) {
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

	handleChannel := func(newChan ssh.NewChannel) {
		switch newChan.ChannelType() {
		case "session":
			ch, reqs, err := newChan.Accept()
			if err != nil {
				return
			}
			go func() {
				defer ch.Close()
				for req := range reqs {
					if req.Type != "exec" {
						if req.WantReply {
							req.Reply(false, nil)
						}
						continue
					}
					var payload struct{ Command string }
					if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
						if req.WantReply {
							req.Reply(false, nil)
						}
						continue
					}
					if req.WantReply {
						req.Reply(true, nil)
					}
					_, _ = io.WriteString(ch, "ran "+payload.Command)
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					return
				}
			}()
		case "direct-tcpip":
			if !forwardTCP {
				_ = newChan.Reject(ssh.Prohibited, "forwarding disabled")
				return
			}
			var payload struct {
				Addr  string
				Port  uint32
				OAddr string
				OPort uint32
			}
			if err := ssh.Unmarshal(newChan.ExtraData(), &payload); err != nil {
				_ = newChan.Reject(ssh.ConnectionFailed, "bad payload")
				return
			}
			upstream, err := net.Dial("tcp", net.JoinHostPort(payload.Addr, strconv.Itoa(int(payload.Port))))
			if err != nil {
				_ = newChan.Reject(ssh.ConnectionFailed, err.Error())
				return
			}
			ch, _, err := newChan.Accept()
			if err != nil {
				upstream.Close()
				return
			}
			go func() {
				defer ch.Close()
				defer upstream.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(upstream, ch); done <- struct{}{} }()
				go func() { _, _ = io.Copy(ch, upstream); done <- struct{}{} }()
				<-done
			}()
		default:
			_ = newChan.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					conn.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for newChan := range chans {
					handleChannel(newChan)
				}
				_ = sconn.Close()
			}()
		}
	}()
	return signer.PublicKey(), listener.Addr().String()
}

// splitHostPort splits "127.0.0.1:port" for test dial targets.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

// acceptAllPolicy auto-accepts every host key via TOFU with isolated files.
func acceptAllPolicy(t *testing.T) *HostKeyPolicy {
	t.Helper()
	return isolatedKeyPolicy(t, func(ctx context.Context, q HostKeyQuestion) (bool, error) {
		return true, nil
	})
}

func testDialTimeout() time.Duration { return 5 * time.Second }
