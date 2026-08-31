package netdev

import (
	"strings"
)

// banner.go — F1's passive fingerprint parser (spec §4.2.1). The probe
// already waits for the server's first bytes and sends NOTHING; this file
// just turns those bytes into structured hints. Pure string parsing, no
// protocol writes, table-driven tested — the "到手就解析" fix for the old
// raw-96-byte passthrough.

// BannerInfo is the parsed form of one port banner.
type BannerInfo struct {
	// Kind: ssh | http | ftp | smtp | "" (other/unparsed).
	Kind string `json:"kind,omitempty"`
	// Product is the software token from the banner ("Huawei-1.0",
	// "OpenSSH_9.6", "nginx/1.24.0", "vsftpd 3.0.3").
	Product string `json:"product,omitempty"`
	// Version is the product's version when it splits cleanly.
	Version string `json:"version,omitempty"`
	// VendorHint: huawei | cisco | zte | hillstone | vmware | linux | "".
	VendorHint string `json:"vendor_hint,omitempty"`
	// RoleHint is a topology-role suggestion; only set when the banner
	// strongly implies a class (VMware → server). Network-vendor SSH banners
	// deliberately stay empty — Huawei ships the same SSH line on switches,
	// routers and firewalls, and a wrong icon is worse than none.
	RoleHint string `json:"role_hint,omitempty"`
}

var bannerVendorTokens = []struct {
	token  string
	vendor string
}{
	{"huawei", "huawei"},
	{"cisco", "cisco"},
	{"nx-os", "cisco"},
	{"zte", "zte"},
	{"hillstone", "hillstone"},
	{"h3c", "h3c"},
	{"ruijie", "ruijie"},
	{"vmware", "vmware"},
	{"linux", "linux"},
	{"ubuntu", "linux"},
	{"debian", "linux"},
	{"centos", "linux"},
	{"alinux", "linux"},
	{"openssh", ""},
	{"libssh", ""},
	{"dropbear", ""},
}

// ParseBanner classifies a captured banner. Empty/unprintable input returns
// the zero value — the caller treats absence of hints as absence of data,
// never as a negative result.
func ParseBanner(raw string) BannerInfo {
	var b BannerInfo
	s := strings.TrimSpace(raw)
	if s == "" {
		return b
	}
	switch {
	case strings.HasPrefix(s, "SSH-"):
		parseSSHBanner(s, &b)
	case strings.HasPrefix(s, "HTTP/"):
		parseHTTPBanner(s, &b)
	default:
		parseGreetingBanner(s, &b)
	}
	return b
}

// parseSSHBanner handles `SSH-2.0-<software>[ <comment>]`.
func parseSSHBanner(s string, b *BannerInfo) {
	b.Kind = "ssh"
	// Split off "SSH-2.0-" style prefix, then the first space-separated token.
	rest := s[len("SSH-"):]
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.IndexAny(rest, " \r\n"); i >= 0 {
		rest = rest[:i]
	}
	software := strings.TrimSpace(rest)
	if software == "" {
		return
	}
	// "OpenSSH_9.6p1" / "Huawei-1.0" / "nginx" style split on _ or -.
	product, version := software, ""
	for _, sep := range []byte{'_', '-', '/'} {
		if i := strings.IndexByte(product, sep); i > 0 {
			product, version = product[:i], strings.TrimPrefix(product[i+1:], "v")
			break
		}
	}
	b.Product = software
	b.Version = version
	lower := strings.ToLower(software)
	for _, t := range bannerVendorTokens {
		if strings.Contains(lower, t.token) {
			b.VendorHint = t.vendor
			break
		}
	}
	if b.VendorHint == "vmware" {
		b.RoleHint = RoleServer
	}
}

// parseHTTPBanner handles a captured status line plus any headers that fit
// in the banner window (rare — HTTP servers wait for a request; kept for the
// cases where a proxy or redirect speaks first).
func parseHTTPBanner(s string, b *BannerInfo) {
	b.Kind = "http"
	lines := strings.Split(s, "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if v, ok := strings.CutPrefix(strings.ToLower(ln), "server:"); ok {
			product := strings.TrimSpace(v)
			if product != "" {
				b.Product = product
				if i := strings.IndexAny(product, "/"); i > 0 {
					b.Product, b.Version = product[:i], strings.TrimPrefix(product[i+1:], "v")
				}
				return
			}
		}
	}
}

// parseGreetingBanner handles FTP/SMTP-style `220 …` welcome lines that often
// carry the daemon name ("vsftpd 3.0.3", "Microsoft FTP Service", ESMTP id).
func parseGreetingBanner(s string, b *BannerInfo) {
	first := s
	if i := strings.IndexAny(first, "\r\n"); i >= 0 {
		first = first[:i]
	}
	lower := strings.ToLower(first)
	switch {
	case strings.Contains(lower, "esmtp"), strings.Contains(lower, "smtp"):
		b.Kind = "smtp"
	case strings.Contains(lower, "ftp") || strings.Contains(lower, "filezilla"):
		b.Kind = "ftp"
	default:
		return
	}
	for _, tok := range strings.Fields(first) {
		l := strings.ToLower(tok)
		switch {
		case strings.HasPrefix(l, "vsftpd"), strings.HasPrefix(l, "proftpd"),
			strings.Contains(l, "ftp"):
			b.Product = tok
			if i := strings.IndexAny(tok, "/-_"); i > 0 {
				b.Product, b.Version = tok[:i], strings.TrimPrefix(tok[i+1:], "v")
			}
			return
		}
	}
}
