package netdev

// apitools.go — the kind=docker / kind=k8s agent tools. Thin wrappers over
// Manager.DockerGet / KubeGet (NETDEV_SPEC_V2 §2.2/§2.3): the whitelist IS
// the seal, ReadOnly() is true, and the tool layer accepts the TARGET NAME
// only (no server/context/socket parameters — no escape surface).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// ── netdev_docker ────────────────────────────────────────────────────────────

type dockerTool struct{ m *Manager }

func (t *dockerTool) Name() string { return "netdev_docker" }

func (t *dockerTool) Description() string {
	return "Read-only Docker Engine queries against a kind=docker target (local Docker Desktop or a configured engine). " +
		"what=ping|version|info|ps (containers)|images|inspect(container)|logs(container, tail_n). " +
		"GET-only API — no build/start/stop/exec path exists at all. Use ps for the running set, logs for one container's tail."
}

func (t *dockerTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"device":   {"type": "string", "description": "kind=docker device name from netdev_devices"},
			"what":     {"type": "string", "enum": ["ping", "version", "info", "ps", "images", "inspect", "logs"], "description": "which GET"},
			"container":{"type": "string", "description": "container id/name (for inspect/logs)"},
			"tail_n":   {"type": "integer", "description": "log lines (logs only; default 100, max 1000)"}
		},
		"required": ["device", "what"]
	}`)
}

func (t *dockerTool) ReadOnly() bool { return true }

func (t *dockerTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Device   string `json:"device"`
		What     string `json:"what"`
		Container string `json:"container"`
		TailN    int    `json:"tail_n"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if a.Device == "" || a.What == "" {
		return "", errors.New("netdev_docker: device and what are required")
	}
	return t.m.DockerGet(ctx, a.Device, a.What, strings.TrimSpace(a.Container), a.TailN)
}

// ── netdev_k8s ───────────────────────────────────────────────────────────────

type kubeTool struct{ m *Manager }

func (t *kubeTool) Name() string { return "netdev_k8s" }

func (t *kubeTool) Description() string {
	return "Read-only Kubernetes queries against a kind=k8s target (kubeconfig pinned to one context in the secret store). " +
		"what=version|nodes|pods|pod|podlog|events|deployments; namespace defaults to the context's, must be inside the target's allowlist. " +
		"No apply/scale/delete path exists — changes go through netdev_propose like everything else."
}

func (t *kubeTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"device":    {"type": "string", "description": "kind=k8s device name from netdev_devices"},
			"what":      {"type": "string", "enum": ["version", "nodes", "pods", "pod", "podlog", "events", "deployments"], "description": "which GET"},
			"namespace": {"type": "string", "description": "defaults to the pinned context's namespace"},
			"name":      {"type": "string", "description": "pod name (pod/podlog)"},
			"tail_n":    {"type": "integer", "description": "log lines (podlog only; default 100, max 1000)"}
		},
		"required": ["device", "what"]
	}`)
}

func (t *kubeTool) ReadOnly() bool { return true }

func (t *kubeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Device    string `json:"device"`
		What      string `json:"what"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		TailN     int    `json:"tail_n"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if a.Device == "" || a.What == "" {
		return "", errors.New("netdev_k8s: device and what are required")
	}
	return t.m.KubeGet(ctx, a.Device, a.What, strings.TrimSpace(a.Namespace), strings.TrimSpace(a.Name), a.TailN)
}
