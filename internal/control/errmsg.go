package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/zzycxz/fairpeer/internal/i18n"
	"github.com/zzycxz/fairpeer/internal/provider"
)

// explainError maps a provider HTTP failure to an actionable, localized message
// so the turn-done error the UI shows is never a bare status code or silent
// failure. Unknown errors (and nil) pass through unchanged.
func explainError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *provider.APIError
	if errors.As(err, &apiErr) {
		msg := i18n.M.ProviderStatusMessage(apiErr.Status)
		// Relay-drop 400: the gateway lost its upstream, the request was fine —
		// "request body was rejected" would point the user at a nonexistent bug.
		if provider.IsTransientGatewayBody(apiErr.Body) {
			msg = i18n.M.ProviderErrGatewayTransient
		}
		if msg == "" {
			return err
		}
		if reason := requestErrorReason(apiErr); reason != "" {
			return fmt.Errorf("%s\n%s", msg, reason)
		}
		return errors.New(msg)
	}
	var authErr *provider.AuthError
	if errors.As(err, &authErr) {
		msg := i18n.M.ProviderStatusMessage(authErr.Status)
		if msg == "" {
			return err
		}
		if authErr.KeyEnv != "" {
			return fmt.Errorf("%s (%s)", msg, authErr.KeyEnv)
		}
		return errors.New(msg)
	}
	return err
}

// requestErrorReason returns the provider's verbatim reason for request-shaped
// 4xx (400/422) — the localized line names the category, the body names the
// actual cause (context-length exceeded, unpaired tool_calls). Empty otherwise.
func requestErrorReason(e *provider.APIError) string {
	if e.Status != 400 && e.Status != 422 {
		return ""
	}
	return providerBodyReason(e.Body)
}

// providerBodyReason pulls the human reason from an OpenAI/Anthropic-shaped error
// body ({"error":{"message":…}}), falling back to the trimmed raw body. Relays
// that stash the real cause in "param" (xiaomimimo: message "Request failed",
// param "Connection prematurely closed BEFORE response") get it appended.
func providerBodyReason(body string) string {
	if body == "" {
		return ""
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Param   string `json:"param"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &parsed) == nil && parsed.Error.Message != "" {
		reason := parsed.Error.Message
		if parsed.Error.Param != "" && !strings.Contains(reason, parsed.Error.Param) {
			reason += " (" + parsed.Error.Param + ")"
		}
		return clampRunes(reason, 800)
	}
	return clampRunes(body, 800)
}

func clampRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
