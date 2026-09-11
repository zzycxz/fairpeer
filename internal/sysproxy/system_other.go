//go:build !windows && !darwin && !linux

package sysproxy

import "net/url"

// ForURL has no OS proxy source on platforms without a system-proxy
// implementation (darwin and linux are covered by system_darwin.go /
// system_linux.go); env/direct handling stays with the caller. The unused
// list/bypass helpers keep one cross-platform file.
func ForURL(*url.URL) (*url.URL, error) {
	_ = parseProxyList
	_ = bypassed
	return nil, nil
}
