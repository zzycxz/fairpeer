//go:build darwin

package sysproxy

import (
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

// ForURL resolves the macOS system proxy by parsing `scutil --proxy` — the
// same dictionary the Network settings pane writes (System Settings →
// Network → Proxies). Returns nil when the system is set to direct, the host
// is on the exceptions list, or no proxy applies for target's scheme; callers
// then fall back to env/direct. No CGO: scutil ships with every macOS install.
func ForURL(target *url.URL) (*url.URL, error) {
	if target == nil {
		return nil, nil
	}
	out, err := exec.Command("scutil", "--proxy").Output()
	if err != nil {
		return nil, nil // unreadable system config — env/direct fallback applies
	}
	kv, exceptions := parseScutilDict(string(out))
	if bypassed(target.Hostname(), strings.Join(exceptions, ";")) {
		return nil, nil
	}

	scheme := strings.ToLower(target.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	switch scheme {
	case "https":
		// HTTPS first; fall back to the HTTP entry, which some setups configure
		// as the only explicit proxy while leaving HTTPS fields empty.
		if u := scutilProxyURL(kv, "HTTPSProxy", "HTTPSEnable", "443", "http"); u != nil {
			return u, nil
		}
	case "socks", "socks5":
		if u := scutilProxyURL(kv, "SOCKSProxy", "SOCKSEnable", "1080", "socks5"); u != nil {
			return u, nil
		}
	}
	if u := scutilProxyURL(kv, "HTTPProxy", "HTTPEnable", "80", "http"); u != nil {
		return u, nil
	}
	return nil, nil
}

// parseScutilDict flattens `scutil --proxy` output into key/value pairs plus
// the ExceptionsList array. The output is a NextStep-style plist dictionary:
//
//	<dictionary> {
//	  HTTPEnable : 1
//	  HTTPPort : 7890
//	  HTTPProxy : 127.0.0.1
//	  ExceptionsList : <array> {
//	    0 : 127.0.0.1
//	    1 : localhost
//	  }
//	}
//
// Array entries ("0 : 127.0.0.1") belong to the most recent array key, so the
// parser tracks that with inArray until the closing brace.
func parseScutilDict(out string) (kv map[string]string, exceptions []string) {
	kv = map[string]string{}
	inArray := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "<") || trimmed == "}" {
			inArray = false
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if inArray {
			exceptions = append(exceptions, strings.Trim(v, `"'`))
			continue
		}
		if v == "<array> {" {
			inArray = true
			continue
		}
		kv[k] = strings.Trim(v, `"'`)
	}
	return kv, exceptions
}

// scutilProxyURL builds the proxy URL for an enable/host/port triple from the
// scutil dictionary, or nil when disabled or unusable. scheme is "http" for
// HTTP/HTTPS proxies (the standard http://… proxy form) and "socks5" for SOCKS.
func scutilProxyURL(kv map[string]string, hostKey, enableKey, defaultPort, scheme string) *url.URL {
	if kv[enableKey] != "1" {
		return nil
	}
	host := kv[hostKey]
	if host == "" {
		return nil
	}
	port := kv[strings.TrimSuffix(hostKey, "Proxy")+"Port"] // HTTPProxy → HTTPPort etc.
	if _, err := strconv.Atoi(port); err != nil {
		port = defaultPort
	}
	return &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
}
