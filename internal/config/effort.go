package config

import (
	"fmt"
	"strings"
)

const (
	ReasoningProtocolAuto    = "auto"
	ReasoningProtocolOpenAI  = "openai"
	ReasoningProtocolMiniMax = "minimax"
	ReasoningProtocolNone    = "none"
)

// Canonical effort levels. All providers use this unified vocabulary.
// Provider-specific translation happens at the wire layer (openai.go, anthropic.go).
const (
	EffortAuto   = "auto"
	EffortLow    = "low"
	EffortMedium = "medium"
	EffortHigh   = "high"
)

// EffortCapability describes the abstract effort levels a provider/model can set
// through the /effort command.
type EffortCapability struct {
	Supported bool
	Levels    []string
	Default   string
}

// UnifiedEffortCapability returns the standard effort capability for any provider
// that supports reasoning. This is the Claude Code-style approach: a simple
// low/medium/high vocabulary that works across all providers.
func UnifiedEffortCapability() EffortCapability {
	return EffortCapability{
		Supported: true,
		Levels:    []string{EffortAuto, EffortLow, EffortMedium, EffortHigh},
		Default:   EffortAuto,
	}
}

// EffortCapabilityForEntry returns the user-facing /effort levels for a resolved
// provider entry. Provider implementations still decide how a stored effort is
// serialized into requests.
func EffortCapabilityForEntry(e *ProviderEntry) EffortCapability {
	if explicitReasoningProtocol(e) == ReasoningProtocolNone {
		return EffortCapability{}
	}
	// All providers that support reasoning use the unified low/medium/high levels.
	return UnifiedEffortCapability()
}

// NormalizeEffort maps a user-supplied /effort level into the value stored in
// config. Empty means auto/provider default. All providers use the unified
// low/medium/high vocabulary. Legacy values are migrated:
//   - max/xhigh → high
//   - adaptive → high
//   - disabled/off → low
func NormalizeEffort(e *ProviderEntry, raw string) (string, error) {
	level := normalizeEffortLevel(raw)
	if level == "" {
		return "", fmt.Errorf("usage: /effort auto|low|medium|high")
	}
	if level == "auto" {
		return "", nil
	}
	if explicitReasoningProtocol(e) == ReasoningProtocolNone {
		return "", effortNotConfigurableError(e)
	}
	// Migrate legacy values to unified vocabulary
	switch level {
	case "max", "xhigh":
		level = EffortHigh
	case "adaptive":
		level = EffortHigh
	case "disabled", "off":
		level = EffortLow
	}
	// Validate against unified levels
	switch level {
	case EffortLow, EffortMedium, EffortHigh:
		return level, nil
	default:
		return "", fmt.Errorf("usage: /effort auto|low|medium|high")
	}
}

// EffortDisplay returns the selected /effort level, using "auto" for provider
// default.
func EffortDisplay(e *ProviderEntry) string {
	if e == nil || strings.TrimSpace(e.Effort) == "" {
		return "auto"
	}
	return normalizeEffortLevel(e.Effort)
}

// EffectiveEffort resolves the provider-visible effort value. Explicit
// ProviderEntry.Effort wins; otherwise a configured SupportedEfforts list makes
// DefaultEffort (or the first supported level) the runtime default. Empty means
// provider default / omit the provider-specific effort field.
func EffectiveEffort(e *ProviderEntry) string {
	if e == nil {
		return ""
	}
	if effort := normalizeStoredEffort(e.Effort); effort != "" {
		return effort
	}
	supported := normalizedSupportedEfforts(e)
	if len(supported) == 0 {
		return ""
	}
	def := normalizeEffortLevel(e.DefaultEffort)
	if def == "" || !containsString(supported, def) {
		return supported[0]
	}
	return def
}

func normalizeEffortConfig(c *Config) {
	if c == nil {
		return
	}
	for i := range c.Providers {
		normalizeProviderEffortFields(&c.Providers[i])
	}
}

func normalizeProviderEffortFields(e *ProviderEntry) {
	if e == nil {
		return
	}
	e.Effort = normalizeStoredEffort(e.Effort)
	e.ReasoningProtocol = normalizeReasoningProtocol(e.ReasoningProtocol)
	e.DefaultEffort = normalizeEffortLevel(e.DefaultEffort)
	e.SupportedEfforts = normalizedSupportedEfforts(e)
}

func normalizeStoredEffort(raw string) string {
	level := normalizeEffortLevel(raw)
	if level == "auto" || level == "off" {
		return ""
	}
	return level
}

// ReasoningProtocolForEntry resolves the provider request shape for reasoning
// controls. Explicit per-provider config wins. With no URL-based fallback, an
// empty result means the provider uses standard OpenAI-compatible request shape
// unless it declares reasoning_protocol = "openai" explicitly.
func ReasoningProtocolForEntry(e *ProviderEntry) string {
	return explicitReasoningProtocol(e)
}

func explicitReasoningProtocol(e *ProviderEntry) string {
	if e == nil {
		return ""
	}
	protocol := normalizeReasoningProtocol(e.ReasoningProtocol)
	if protocol == ReasoningProtocolAuto {
		return ""
	}
	return protocol
}

// ValidReasoningProtocols lists every accepted reasoning_protocol value —
// used for error messages so typos surface instead of silently degrading to
// auto (the old behaviour swallowed unknown values).
var ValidReasoningProtocols = []string{ReasoningProtocolAuto, ReasoningProtocolOpenAI, ReasoningProtocolMiniMax, ReasoningProtocolNone}

// normalizeReasoningProtocol maps "" and "auto" to "" (endpoint auto-detect).
// A recognized explicit protocol is lowercased and returned verbatim; an
// UNKNOWN value is returned as-is (lowercased) so callers can reject it —
// silent fallback would hide config typos from the user.
func normalizeReasoningProtocol(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", ReasoningProtocolAuto:
		return ""
	case ReasoningProtocolOpenAI, ReasoningProtocolMiniMax, ReasoningProtocolNone:
		return v
	default:
		return v
	}
}

// ReasoningProtocolValid reports whether raw is one of the accepted values.
func ReasoningProtocolValid(raw string) bool {
	v := normalizeReasoningProtocol(raw)
	if v == "" {
		return true
	}
	switch v {
	case ReasoningProtocolOpenAI, ReasoningProtocolMiniMax, ReasoningProtocolNone:
		return true
	}
	return false
}

func effortNotConfigurableError(e *ProviderEntry) error {
	name := ""
	if e != nil {
		name = e.Name
	}
	if name == "" {
		name = "this model"
	}
	return fmt.Errorf("effort is not configurable for %s", name)
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func normalizeEffortLevel(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizedSupportedEfforts(e *ProviderEntry) []string {
	if e == nil || len(e.SupportedEfforts) == 0 {
		return nil
	}
	out := make([]string, 0, len(e.SupportedEfforts))
	seen := map[string]bool{}
	for _, raw := range e.SupportedEfforts {
		level := normalizeEffortLevel(raw)
		if level == "" || level == "auto" || seen[level] {
			continue
		}
		seen[level] = true
		out = append(out, level)
	}
	return out
}
