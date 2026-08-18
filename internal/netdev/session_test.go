package netdev

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/zzycxz/fairpeer/internal/netdev/driver"
	"github.com/zzycxz/fairpeer/internal/netdev/transport"
)

// simDevice is an in-process SSH server emulating a Huawei-VRP-like CLI:
// echoes commands, prints "<SimSW>" prompts, pages long output behind a
// "---- More ----" prompt, answers with error lines on unknown commands, and
// can emit GBK bytes on demand to exercise the decoder.
type simDevice struct {
	addr     string
	password string
}

func startSimDevice(t *testing.T) *simDevice {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sim := &simDevice{password: "pw"}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == sim.password {
				return nil, nil
			}
			return nil, errors.New("bad password")
		},
	}
	cfg.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sim.addr = listener.Addr().String()
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveSimConn(conn, cfg)
		}
	}()
	return sim
}

func serveSimConn(conn net.Conn, cfg *ssh.ServerConfig) {
	sconn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			continue
		}
		go serveSimSession(ch, chReqs)
	}
	_ = sconn.Close()
}

const simPrompt = "<SimSW>"

func serveSimSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "shell", "pty-req":
			if req.WantReply {
				req.Reply(true, nil)
			}
			if req.Type == "shell" {
				go runSimShell(ch)
				return
			}
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func runSimShell(ch ssh.Channel) {
	defer ch.Close()
	write := func(s string) { _, _ = ch.Write([]byte(s)) }
	write("Welcome to the simulated VRP.\n")
	write(simPrompt)

	line := make([]byte, 0, 256)
	buf := make([]byte, 256)
	for {
		n, err := ch.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				if b == '\n' || b == '\r' {
					cmd := strings.TrimSpace(string(line))
					line = line[:0]
					if cmd != "" {
						write(cmd + "\n") // CLI echo of the completed line
						simDispatch(ch, cmd)
					}
					write(simPrompt)
					continue
				}
				line = append(line, b)
			}
		}
		if err != nil {
			return
		}
	}
}

func simDispatch(ch ssh.Channel, cmd string) {
	switch {
	case cmd == "screen-length 0 temporary" || cmd == "terminal length 0" || cmd == "terminal width 511":
		// acknowledged silently; caller prints the next prompt.
	case cmd == "display version":
		ch.Write([]byte("\nHuawei Versatile Routing Platform Software\nVRP (R) Software, Version 8.180 (S5735 V200R019C10)\nCopyright (C) 2000-2019 Huawei Technologies Co., Ltd.\n"))
	case cmd == "display interface brief":
		ch.Write([]byte("\nPHY: Physical\n*down: administratively down\n(l): loopback\n(s): spoofing\nInUti/OutUti: input utility/output utility\n"))
	case cmd == "display gbk":
		// GBK-encoded Chinese output.
		gbk, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("\n接口当前状态: UP\n线路协议状态: UP\n"))
		ch.Write(gbk)
	case cmd == "display long":
		// 60 lines in chunks of 24 behind a More prompt. The marker's bytes
		// stay in the stream (as on real devices whose ANSI erase sequences
		// vanish under strip, leaving the text); cleanOutput scrubs the line.
		const total = 60
		written := 0
		for written < total {
			chunk := total - written
			if chunk > 24 {
				chunk = 24
			}
			var b strings.Builder
			for i := 0; i < chunk; i++ {
				fmt.Fprintf(&b, "line %03d payload data for the paging test\n", written+i+1)
			}
			ch.Write([]byte(b.String()))
			written += chunk
			if written < total {
				ch.Write([]byte("  ---- More ----"))
				one := make([]byte, 1)
				for {
					n, err := ch.Read(one)
					if err != nil {
						return
					}
					if n == 1 && one[0] == ' ' {
						break
					}
				}
				ch.Write([]byte("\r\n"))
			}
		}
	default:
		ch.Write([]byte("\nError: Unrecognized command found at '^' position.\n"))
	}
}

// simConnection dials the simulator with auto-accept host keys and a client
// ready for session use.
func simConnection(t *testing.T, sim *simDevice) *transport.Client {
	t.Helper()
	host, portStr, err := net.SplitHostPort(sim.addr)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	policy := &transport.HostKeyPolicy{
		SystemKnownHosts: []string{filepath.Join(t.TempDir(), "none")},
		ManagedPath:      filepath.Join(t.TempDir(), "known_hosts"),
		Prompt:           func(ctx context.Context, q transport.HostKeyQuestion) (bool, error) { return true, nil },
	}
	c, err := transport.New(transport.Options{
		Host:        transport.ResolvedHost{HostName: host, Port: port, User: "admin"},
		Auth:        transport.AuthOptions{Password: func() (string, error) { return sim.password, nil }},
		HostKeys:    policy,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestSessionReadCommand(t *testing.T) {
	sim := startSimDevice(t)
	c := simConnection(t, sim)
	drv, _ := driver.For("huawei", "vrp8")

	s, err := OpenSession(context.Background(), c, drv, "auto")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer s.Close()

	res, err := s.Run(context.Background(), "display version")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("read command flagged as error: %q", res.Output)
	}
	for _, want := range []string{"Huawei Versatile Routing Platform", "Version 8.180"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output missing %q: %q", want, res.Output)
		}
	}
	if strings.Contains(res.Output, simPrompt) {
		t.Fatalf("prompt leaked into cleaned output: %q", res.Output)
	}
	if strings.Contains(res.Output, "display version") {
		t.Fatalf("echo leaked into cleaned output: %q", res.Output)
	}
}

func TestSessionPaging(t *testing.T) {
	sim := startSimDevice(t)
	c := simConnection(t, sim)
	drv, _ := driver.For("huawei", "vrp8")
	s, err := OpenSession(context.Background(), c, drv, "auto")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer s.Close()

	res, err := s.Run(context.Background(), "display long")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Output, "line 001") || !strings.Contains(res.Output, "line 060") {
		t.Fatalf("paged output truncated: has first=%v last=%v",
			strings.Contains(res.Output, "line 001"), strings.Contains(res.Output, "line 060"))
	}
	if strings.Contains(res.Output, "More") {
		t.Fatalf("pager marker leaked: %q", res.Output)
	}
}

func TestSessionErrorDetection(t *testing.T) {
	sim := startSimDevice(t)
	c := simConnection(t, sim)
	drv, _ := driver.For("huawei", "vrp8")
	s, err := OpenSession(context.Background(), c, drv, "auto")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer s.Close()

	res, err := s.Run(context.Background(), "display bogus")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("error output not flagged: %q", res.Output)
	}
	if !strings.Contains(res.Output, "Unrecognized") {
		t.Fatalf("error text lost: %q", res.Output)
	}
}

func TestSessionGBKDecode(t *testing.T) {
	sim := startSimDevice(t)
	c := simConnection(t, sim)
	drv, _ := driver.For("huawei", "vrp8")
	s, err := OpenSession(context.Background(), c, drv, "gbk")
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer s.Close()

	res, err := s.Run(context.Background(), "display gbk")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Output, "接口当前状态") {
		t.Fatalf("GBK decode failed: %q", res.Output)
	}
}
