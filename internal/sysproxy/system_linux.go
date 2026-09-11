//go:build linux

package sysproxy

import (
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

// ForURL resolves the GNOME/GTK system proxy via `gsettings get
// org.gnome.system.proxy …` — what the GNOME Settings → Network → Network
// Proxy pane writes. Returns (nil, nil) when gsettings is missing (headless
// boxes, non-GIO desktops), the mode is not 'manual', or no proxy applies for
// target's scheme — in every such case the caller's env-var fallback still
// applies. No CGO.
func ForURL(target *url.URL) (*url.URL, error) {
	if target == nil {
		return nil, nil
	}
	if mode := unquote(gsettingsGet("org.gnome.system.proxy", "mode")); mode != "manual" {
		// 'none', 'auto' (PAC — no static proxy to report), or unreadable.
		return nil, nil
	}

	host := target.Hostname()
	if bypassed(host, ignoreHosts()) {
		return nil, nil
	}

	scheme := strings.ToLower(target.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	switch scheme {
	case "https":
		// HTTPS first; fall back to the HTTP entry, which GNOME setups often
		// leave as the only configured proxy.
		if u := gsettingsProxyURL("https", "443", "http"); u != nil {
			return u, nil
		}
	case "socks", "socks5":
		if u := gsettingsProxyURL("socks", "1080", "socks5"); u != nil {
			return u, nil
		}
	}
	if u := gsettingsProxyURL("http", "80", "http"); u != nil {
		return u, nil
	}
	return nil, nil
}

// gsettingsProxyURL reads host/port under org.gnome.system.proxy.<schema> and
// builds the proxy URL, falling back to fallbackSchema's host when the primary
// schema is unset (GNOME leaves https.host empty when it mirrors http). scheme
// is "http" for HTTP/HTTPS proxies and "socks5" for SOCKS.
func gsettingsProxyURL(schema, defaultPort, urlScheme string) *url.URL {
	host := unquote(gsettingsGet("org.gnome.system.proxy."+schema, "host"))
	port := unquote(gsettingsGet("org.gnome.system.proxy."+schema, "port"))
	if host == "" {
		host = unquote(gsettingsGet("org.gnome.system.proxy."+fallbackSchema(schema), "host"))
	}
	if host == "" {
		return nil
	}
	if _, err := strconv.Atoi(port); err != nil {
		port = defaultPort
	}
	return &url.URL{Scheme: urlScheme, Host: net.JoinHostPort(host, port)}
}

// fallbackSchema names the schema to inherit host from: https inherits http;
// everything else has no fallback.
func fallbackSchema(schema string) string {
	if schema == "https" {
		return "http"
	}
	return schema
}

// ignoreHosts flattens org.gnome.system.proxy ignore-hosts (a GVariant string
// array: "['localhost', '127.0.0.0/8']") into the ';'-joined form bypassed
// parses. Read errors yield an empty string = no exclusions.
func ignoreHosts() string {
	raw := gsettingsGet("org.gnome.system.proxy", "ignore-hosts")
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")
	hosts := strings.Split(raw, ",")
	for i, h := range hosts {
		hosts[i] = unquote(strings.TrimSpace(h))
	}
	return strings.Join(hosts, ";")
}

// gsettingsGet runs `gsettings get <schema> <key>` and returns trimmed stdout;
// "" on any failure so callers treat it as unset. gsettings missing entirely →
// ForURL stays out of the way.
func gsettingsGet(schema, key string) string {
	out, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// unquote strips the GVariant quoting gsettings prints: 'manual' → manual,
// 8080 → 8080.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}
