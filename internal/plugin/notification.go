// Server-initiated MCP notifications (upgrade spec 3-7④ / FAIRPEER_CODEX_GAP_SPEC
// Spec-6). Until now all three transports dropped everything that carried a
// method and no id; tools/list_changed in particular meant fairpeer kept serving
// a stale tool list until the next launch. Transports now surface notifications
// to the Host, which honours the list_changed family and logs the rest.
package plugin

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// notificationSink is the optional transport capability: the transport calls fn
// for every server-initiated notification (method, no id). Progress
// notifications stay token-routed per call; elicitation stays a request.
// Transports without the capability keep the old drop-with-debug-log behaviour.
type notificationSink interface {
	setNotificationHandler(fn func(method string, params json.RawMessage))
}

// parseServerNotification extracts method + params from a JSON-RPC frame. ok is
// false for messages that carry an id (responses, server requests) or don't
// parse — those keep their existing routing.
func parseServerNotification(raw []byte) (method string, params json.RawMessage, ok bool) {
	var env struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.Method == "" || len(env.ID) > 0 {
		return "", nil, false
	}
	return env.Method, env.Params, true
}

// attachNotificationHandler wires a client's transport notifications into the
// host router. Called wherever a client joins h.clients.
func (h *Host) attachNotificationHandler(c *Client) {
	c.setNotificationHandler(func(method string, params json.RawMessage) {
		h.handleServerNotification(c, method, params)
	})
}

// setNotificationHandler forwards to the transport when it supports the
// capability (all three first-party transports do).
func (c *Client) setNotificationHandler(fn func(method string, params json.RawMessage)) {
	if sink, ok := c.t.(notificationSink); ok {
		sink.setNotificationHandler(fn)
	}
}

// handleServerNotification routes one surfaced notification. The list_changed
// family triggers a refetch (tools swap through ToolsRefreshed, prompts and
// resources through the existing fetch helpers); everything else is logged.
func (h *Host) handleServerNotification(c *Client, method string, params json.RawMessage) {
	switch method {
	case "notifications/tools/list_changed":
		slog.Info("mcp: server tool list changed", "server", c.name)
		go h.refreshClientTools(c)
	case "notifications/prompts/list_changed":
		if c.hasPrompts {
			slog.Info("mcp: prompt list changed", "server", c.name)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), defaultStartTimeout)
				defer cancel()
				h.fetchPrompts(ctx, c, nil)
			}()
		}
	case "notifications/resources/list_changed":
		if c.hasResources {
			slog.Info("mcp: resource list changed", "server", c.name)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), defaultStartTimeout)
				defer cancel()
				h.fetchResources(ctx, c, nil)
			}()
		}
	case "notifications/resources/updated":
		uri := ""
		if params != nil {
			var p struct {
				URI string `json:"uri"`
			}
			if json.Unmarshal(params, &p) == nil {
				uri = p.URI
			}
		}
		slog.Info("mcp: resource updated", "server", c.name, "uri", uri)
	default:
		slog.Debug("mcp: server notification", "server", c.name, "method", method)
	}
}

// refreshClientTools re-runs tools/list for one server after its
// tools/list_changed notification. Debounced per client (servers may emit the
// notification in bursts); on success it updates the status counts, refreshes
// the persisted handshake cache, and hands the fresh list to ToolsRefreshed so
// the registry owner can swap the server's namespace live.
func (h *Host) refreshClientTools(c *Client) {
	c.refreshMu.Lock()
	if c.refreshing {
		c.refreshMu.Unlock()
		return
	}
	c.refreshing = true
	c.refreshMu.Unlock()
	defer func() {
		c.refreshMu.Lock()
		c.refreshing = false
		c.refreshMu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ts, err := c.listTools(ctx)
	if err != nil {
		slog.Warn("mcp: tool list refresh failed", "server", c.name, "err", err)
		return
	}

	h.mu.Lock()
	c.toolCount = len(ts)
	h.mu.Unlock()

	// Keep the cross-launch cache honest so the next start doesn't resurrect
	// the pre-notification tool list from disk.
	_ = SaveCachedSchema(c.name, CachedSchema{
		SpecHash:     SpecFingerprint(c.spec),
		Capabilities: map[string]bool{"prompts": c.hasPrompts, "resources": c.hasResources},
		Tools:        cacheableToolsOf(ts),
	})

	if h.ToolsRefreshed != nil {
		h.ToolsRefreshed(c.name, ts)
	}
	slog.Info("mcp: tool list refreshed", "server", c.name, "tools", len(ts))
}
