package netdev

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/zzycxz/fairpeer/internal/netdev/transport"
)

// Discovery (tunnel mode): TCP reachability probing through the SSH chain —
// each probe opens a direct-tcpip channel on the hop's connection (or dials
// direct when via is empty), so the SYN comes from the hop's network position.
// UDP/ICMP and /16-and-up sweeps need the netprobe binary (P3 later phase);
// the tunnel mode covers targeted /24-class checks.

// DiscoverPortProbe is one port's outcome on one host.
type DiscoverPortProbe struct {
	Port   int    `json:"port"`
	Open   bool   `json:"open"`
	Banner string `json:"banner,omitempty"` // first bytes, e.g. "SSH-2.0-..."
}

// DiscoverHostResult is one live host.
type DiscoverHostResult struct {
	IP    string              `json:"ip"`
	Ports []DiscoverPortProbe `json:"ports"`
}

// discoverLimits bound one scan.
const (
	discoverMaxHosts       = 4096 // refuse beyond /20
	discoverDefaultRate    = 50
	discoverProbeTimeout   = 3 * time.Second
	discoverBannerTimeout  = 2 * time.Second
	discoverBannerMaxBytes = 96
)

// DiscoverTCP probes ip:port combinations across a CIDR. via names a
// configured hop ("" = dial directly from this machine). The requested CIDR
// must sit INSIDE the configured [netdev.discovery] scopes — enforced here at
// the dial boundary, not trusted to the caller (guardrail invariant 3).
func (m *Manager) DiscoverTCP(ctx context.Context, via, cidr string, ports []int) ([]DiscoverHostResult, error) {
	if !m.cfg.NetDev.Enabled {
		return nil, fmt.Errorf("[netdev] is disabled")
	}
	if len(ports) == 0 {
		ports = []int{22, 23} // default fingerprint: SSH + Telnet
	}
	target, targetNet, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	hosts, err := expandCIDR(target, targetNet)
	if err != nil {
		return nil, err
	}
	if !m.scopeAllows(targetNet) {
		return nil, fmt.Errorf("CIDR %s is outside the configured discovery scopes — probing is refused (scopes are a never-off guardrail)", cidr)
	}

	dialer, closeHop, err := m.dialerFor(ctx, via)
	if err != nil {
		return nil, err
	}
	defer closeHop()

	rate := m.cfg.NetDev.Discovery.Rate
	if rate <= 0 || rate > 256 {
		rate = discoverDefaultRate
	}

	type job struct{ ip string }
	jobs := make(chan job)
	results := make(chan DiscoverHostResult, rate)
	var wg sync.WaitGroup
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for i := 0; i < rate; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-workerCtx.Done():
					return
				default:
				}
				var open []DiscoverPortProbe
				for _, port := range ports {
					p := probeTCP(ctx, dialer, j.ip, port)
					if p.Open {
						open = append(open, p)
					}
				}
				if len(open) > 0 {
					results <- DiscoverHostResult{IP: j.ip, Ports: open}
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, ip := range hosts {
			select {
			case jobs <- job{ip: ip}:
			case <-workerCtx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	var out []DiscoverHostResult
	for r := range results {
		out = append(out, r)
	}
	return out, ctx.Err()
}

// probeTCP dials ip:port through dialer and grabs a banner when open.
func probeTCP(ctx context.Context, dialer transport.Dialer, ip string, port int) DiscoverPortProbe {
	dctx, cancel := context.WithTimeout(ctx, discoverProbeTimeout)
	defer cancel()
	conn, err := dialer.DialContext(dctx, "tcp", net.JoinHostPort(ip, fmt.Sprint(port)))
	if err != nil {
		return DiscoverPortProbe{Port: port}
	}
	p := DiscoverPortProbe{Port: port, Open: true}
	_ = conn.SetReadDeadline(time.Now().Add(discoverBannerTimeout))
	buf := make([]byte, discoverBannerMaxBytes)
	n, _ := io.ReadFull(conn, buf[:1])
	if n > 0 {
		// Read whatever follows the first byte (banner or nothing).
		rest, _ := conn.Read(buf[1:])
		banner := strings.TrimSpace(string(buf[:1+rest]))
		if isPrintableBanner(banner) {
			p.Banner = banner
		}
	}
	conn.Close()
	return p
}

func isPrintableBanner(s string) bool {
	for _, r := range s {
		if r < 0x20 && r != '\t' {
			return false
		}
	}
	return len(s) > 0
}

// dialerFor returns the first-hop dialer: direct, or a hop's SSH connection
// (probes then travel as direct-tcpip channels through that hop).
func (m *Manager) dialerFor(ctx context.Context, via string) (transport.Dialer, func(), error) {
	cleanup := func() {}
	if strings.TrimSpace(via) == "" {
		return directDialer{timeout: discoverProbeTimeout}, cleanup, nil
	}
	hop, ok := m.cfg.NetDevHopByName(via)
	if !ok {
		return nil, cleanup, fmt.Errorf("hop %q is not configured (hops are human-registered only)", via)
	}
	lookup := m.lookupEntry()
	resolved, err := transport.ResolveHost(lookup, hop.Name, nil)
	if err != nil {
		return nil, cleanup, err
	}
	auth := transport.AuthOptions{
		Password:   secretReader(SecretKindPassword, hop.PasswordEnv),
		Passphrase: secretReader(SecretKindPassphrase, hop.PassphraseEnv),
	}
	client, err := transport.New(transport.Options{
		Host:     resolved,
		Auth:     auth,
		HostKeys: &transport.HostKeyPolicy{Prompt: HostKeyPrompt},
	})
	if err != nil {
		return nil, cleanup, err
	}
	if err := client.Start(ctx); err != nil {
		client.Close()
		return nil, cleanup, err
	}
	sshClient, err := client.SSH()
	if err != nil {
		client.Close()
		return nil, cleanup, err
	}
	cleanup = func() { client.Close() }
	return sshDialer{client: sshClient}, cleanup, nil
}

// directDialer dials straight from this machine (no hop).
type directDialer struct{ timeout time.Duration }

func (d directDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	var nd net.Dialer
	if d.timeout > 0 {
		nd.Timeout = d.timeout
	}
	return nd.DialContext(ctx, network, addr)
}

// sshDialer adapts an established *ssh.Client to the Dialer interface:
// every probe becomes a direct-tcpip channel through the hop's connection.
type sshDialer struct {
	client interface {
		DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	}
}

func (d sshDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.client.DialContext(ctx, network, addr)
}

// scopeAllows reports whether target lies inside one of the configured scopes.
func (m *Manager) scopeAllows(target *net.IPNet) bool {
	for _, s := range m.cfg.NetDev.Discovery.Scopes {
		_, scopeNet, err := net.ParseCIDR(strings.TrimSpace(s))
		if err != nil {
			continue
		}
		if cidrContains(scopeNet, target) {
			return true
		}
	}
	return false
}

func cidrContains(outer, inner *net.IPNet) bool {
	if !outer.Contains(inner.IP) {
		return false
	}
	ob, _ := outer.Mask.Size()
	ib, _ := inner.Mask.Size()
	return ob <= ib
}

// expandCIDR lists usable host IPs (network/broadcast excluded for IPv4).
func expandCIDR(ip net.IP, ipNet *net.IPNet) ([]string, error) {
	var out []string
	for cur := ip.Mask(ipNet.Mask); ipNet.Contains(cur); incIP(cur) {
		out = append(out, cur.String())
		if len(out) > discoverMaxHosts {
			return nil, fmt.Errorf("CIDR too large for tunnel-mode probing (>%d hosts); use netprobe", discoverMaxHosts)
		}
	}
	if len(out) > 2 && strings.Contains(out[0], ".") {
		return out[1 : len(out)-1], nil // drop network + broadcast
	}
	return out, nil
}

func incIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			return
		}
	}
}
