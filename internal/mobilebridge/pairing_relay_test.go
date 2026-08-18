package mobilebridge

import (
	"strings"
	"testing"
)

func TestHTTPURLScheme(t *testing.T) {
	cases := map[string]string{
		"wss://signal.example.com": "https://signal.example.com",
		"ws://host:8080":           "http://host:8080",
		"http://127.0.0.1:8080":    "http://127.0.0.1:8080",
		"https://a.b/":             "https://a.b/",
	}
	for in, want := range cases {
		if got := httpURL(in); got != want {
			t.Errorf("httpURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLanRelayURLsLoopback(t *testing.T) {
	got := lanRelayURLs("http://127.0.0.1:8080")
	if len(got) == 0 {
		t.Fatal("no relay candidates for loopback signal_url")
	}
	for _, r := range got {
		if strings.Contains(r, "127.0.0.1") {
			t.Errorf("relay candidate still loopback: %s", r)
		}
		if !strings.HasPrefix(r, "http://192.168.") && !strings.HasPrefix(r, "http://10.") &&
			!strings.HasPrefix(r, "http://172.") {
			t.Errorf("relay candidate not a private-LAN http URL: %s", r)
		}
		if !strings.HasSuffix(r, ":8080") {
			t.Errorf("relay candidate lost the port: %s", r)
		}
	}
	t.Logf("candidates: %v", got)
}

func TestLanRelayURLsPublicPassthrough(t *testing.T) {
	got := lanRelayURLs("wss://signal.linkpeer.app")
	if len(got) != 1 || got[0] != "wss://signal.linkpeer.app" {
		t.Errorf("public signal_url must pass through unchanged, got %v", got)
	}
}

func TestParseRoutePrint(t *testing.T) {
	// 样例：中文 Windows `route print -4` 输出片段——含 Clash TUN 的 /1
	// 劫持路由（掩码 128.0.0.0，必须被排除）、fake-ip 出口（非私网，排除）、
	// 两条真实默认路由（WLAN metric 35 优于 以太网 metric 261）、
	// 持久路由段落的重复条目（去重）。
	const sample = `===========================================================================
接口列表
 17...00 1a 2b 3c 4d 5e ......Intel(R) Wi-Fi 6E AX211
===========================================================================

IPv4 路由表
===========================================================================
活动路由:
网络目标        网络掩码          网关       接口   跃点数
          0.0.0.0          0.0.0.0      192.168.1.1   192.168.1.48     35
          0.0.0.0  128.0.0.0     198.18.0.2     198.18.0.1      1
        128.0.0.0  128.0.0.0         在链路上     198.18.0.1    281
          0.0.0.0          0.0.0.0       10.0.0.1     10.0.0.5     261
===========================================================================
永久路由:
  网络地址          网络掩码  网关地址  跃点数
          0.0.0.0          0.0.0.0      192.168.1.1   192.168.1.48     35
`
	es := parseRoutePrint(sample)
	if len(es) != 2 {
		t.Fatalf("want 2 default routes (dedup + TUN/fake-ip excluded), got %d: %+v", len(es), es)
	}
	if es[0].ifaceIP != "192.168.1.48" || es[0].metric != 35 {
		t.Errorf("lowest-metric route must win, got %+v", es[0])
	}
	if es[1].ifaceIP != "10.0.0.5" || es[1].metric != 261 {
		t.Errorf("second route wrong, got %+v", es[1])
	}
	for _, e := range es {
		if e.ifaceIP == "198.18.0.1" {
			t.Errorf("fake-ip TUN egress must be excluded: %+v", e)
		}
	}
}

func TestDefaultLanIPInfoReturnsReason(t *testing.T) {
	ip, reason := defaultLanIPInfo()
	if ip == "" || reason == "" {
		t.Fatalf("default NIC must have a documented reason, got ip=%q reason=%q", ip, reason)
	}
	t.Logf("default=%s reason=%s", ip, reason)
}
