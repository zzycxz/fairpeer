package main

// provider_templates.go defines the ProviderTemplate type — the data shape for
// vendor presets shown in the onboarding wizard and Settings "add provider"
// picker. The actual template DATA lives in default_registry.json (embedded)
// and is managed by registry.go (with models.dev remote updates).
//
// This file previously held a hardcoded `providerTemplates` Go variable; that
// data has been moved to default_registry.json so model/URL updates no longer
// require recompiling Go code.

// ProviderTemplate is one vendor preset shown in the onboarding wizard. The
// user selects a template, pastes an API key, and the wizard probes the vendor
// endpoint for available models. Fields here prefill the provider config so the
// user doesn't need to know base URLs or env-var names.
type ProviderTemplate struct {
	Name          string   `json:"name"`          // provider name (qwen, deepseek, ...)
	DisplayName   string   `json:"displayName"`   // human-readable (通义千问, DeepSeek, ...)
	Kind          string   `json:"kind"`          // "openai" or "anthropic"
	BaseURL       string   `json:"baseUrl"`       // API root
	APIKeyEnv     string   `json:"apiKeyEnv"`     // env var holding the key
	DefaultModel  string   `json:"defaultModel"`  // recommended default model (vendor-relative, no provider prefix)
	FastModel     string   `json:"fastModel"`     // recommended fast model
	VisionModel   string   `json:"visionModel"`   // recommended vision model ("" = same as default)
	Vision        bool     `json:"vision"`        // provider supports image input
	ContextWindow int      `json:"contextWindow"` // max context tokens
	Local         bool     `json:"local"`         // keyless local endpoint (Ollama, llama.cpp): wizard skips the API-key step and fetches installed models live
	CodingOnly    bool     `json:"codingOnly"`    // consumes Coding Plan subscription quota (reserved, v1.0 unused)
	Aggregator    bool     `json:"aggregator"`    // model-aggregation platform (reserved, v1.0 unused)
	Category      string   `json:"category"`      // "direct", "aggregator", or "local"
	DocURL        string   `json:"docUrl"`        // where to get an API key
	Models        []string `json:"models"`        // preset model list (fallback when probe fails)
	// ReasoningModels lists models.dev-flagged reasoning-capable model IDs
	// (per-model data the merge used to discard). Display-only for now — the
	// boot-time behaviour layer (internal/instruction, effort routing) never
	// reads this; see docs/MODEL_ROUTING_SPEC.md for the boundary.
	ReasoningModels []string `json:"reasoningModels,omitempty"`
}
