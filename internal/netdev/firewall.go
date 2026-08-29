package netdev

// firewall.go — kind=firewall 的只读 REST 客户端（NETDEV_SPEC_V2 §2.6）。
// v1 vendor：fortinet（FortiOS）。GET 白名单 = /api/v2/monitor/* 系统与防火墙
// 监控端点 + cmdb 的只读 GET；写路径（POST/PUT/DELETE）不存在代码路径。
// 认证：REST API token（secret store，api-token kind）优先，回退设备账密
// （Basic）。自签证书跳过校验（同 redfish 的带外约定）。真机校准点：各端点
// 的 JSON 字段随 FortiOS 版本略有差异——这里只做透传 + 状态码检查。

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const fwBodyCap = 256 * 1024

// fwPaths is the GET whitelist: what → path. Fixed set — no user/tool input
// ever reaches the URL (the whole anti-injection story in one table).
var fwPaths = map[string]string{
	"status":     "/api/v2/monitor/system/status",
	"resource":   "/api/v2/monitor/system/resource",
	"interfaces": "/api/v2/monitor/system/interface",
	"conns":      "/api/v2/monitor/firewall/conn",
	"policies":   "/api/v2/cmdb/firewall/policy",
	"routes":     "/api/v2/monitor/router/ipv4",
}

// FirewallGet answers ONE whitelisted GET against the device's REST API.
func (m *Manager) FirewallGet(ctx context.Context, deviceName, what string) (string, error) {
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return "", fmt.Errorf("device %q is not in the inventory (add it in the 运维 settings)", deviceName)
	}
	if d.Kind != "firewall" || d.Fw == nil {
		return "", fmt.Errorf("device %q is not a kind=firewall target", deviceName)
	}
	return m.sealAPIGet(deviceName, "fw "+what, func() (string, error) {
		path, ok := fwPaths[what]
		if !ok {
			return "", errors.New("what must be status|resource|interfaces|conns|policies|routes")
		}
		if strings.TrimSpace(d.Address) == "" {
			return "", fmt.Errorf("device %q has no address", deviceName)
		}
		client := &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec — self-signed mgmt certs, same call as redfish
			Timeout:   30 * time.Second,
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+d.Address+path, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		// Token auth first; fall back to the device's Basic credentials.
		if strings.TrimSpace(d.Fw.ApiTokenEnv) != "" {
			if tok, ok, _ := secretGetter(SecretKindAPIToken, d.Fw.ApiTokenEnv); ok && tok != "" {
				req.Header.Set("Authorization", "Bearer "+tok)
			}
		}
		if req.Header.Get("Authorization") == "" && d.Username != "" {
			if pwd, ok, _ := secretGetter(SecretKindPassword, d.PasswordEnv); ok {
				req.SetBasicAuth(d.Username, pwd)
			}
		}
		if req.Header.Get("Authorization") == "" {
			return "", fmt.Errorf("device %q: no API token (firewall.api_token_env) nor device credentials", deviceName)
		}
		res, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, fwBodyCap))
		if err != nil {
			return "", err
		}
		if res.StatusCode != http.StatusOK {
			return string(body), fmt.Errorf("fortios %s → HTTP %d", path, res.StatusCode)
		}
		return string(body), nil
	})
}

// ── Agent tool ───────────────────────────────────────────────────────────────

type firewallTool struct{ m *Manager }

func (t *firewallTool) Name() string { return "netdev_firewall" }

func (t *firewallTool) Description() string {
	return "Read-only FortiOS REST queries against a kind=firewall target. " +
		"what=status|resource|interfaces|conns(会话表)|policies(策略只读)|routes. " +
		"GET-only monitor/cmdb endpoints — no policy-write path exists."
}

func (t *firewallTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"device": {"type": "string", "description": "kind=firewall device name from netdev_devices"},
			"what":   {"type": "string", "enum": ["status", "resource", "interfaces", "conns", "policies", "routes"], "description": "which GET"}
		},
		"required": ["device", "what"]
	}`)
}

func (t *firewallTool) ReadOnly() bool { return true }

func (t *firewallTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Device string `json:"device"`
		What   string `json:"what"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if a.Device == "" || a.What == "" {
		return "", errors.New("netdev_firewall: device and what are required")
	}
	return t.m.FirewallGet(ctx, a.Device, a.What)
}
