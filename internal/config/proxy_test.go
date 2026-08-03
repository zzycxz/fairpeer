package config

import "testing"

func TestDirectProxyHostsFromNoProxyProviders(t *testing.T) {
	t.Skip("No no_proxy providers currently exist")
	spec := Default().NetworkProxySpec()
	hasDirectHost := false
	for _, h := range spec.DirectHosts {
		if h == "api.example.com" {
			hasDirectHost = true
		}
	}
	if !hasDirectHost {
		t.Errorf("a no_proxy provider's host should land in DirectHosts, got %v", spec.DirectHosts)
	}
}

func TestExplicitProxyOverridesProviderNoProxy(t *testing.T) {
	// An explicit custom proxy (e.g. a mandatory corporate proxy) must apply to
	// every provider, including no_proxy ones like test-provider, so it isn't unreachable
	// behind the proxy (#3635).
	c := Default()
	c.Network.ProxyMode = "custom"
	spec := c.NetworkProxySpec()
	for _, h := range spec.DirectHosts {
		if h == "token-plan.example.com" {
			t.Fatalf("custom proxy must not force test-provider direct; DirectHosts = %v", spec.DirectHosts)
		}
	}
}
