package main

// provider_templates.go defines the built-in provider presets offered in the
// first-run onboarding wizard and the Settings "add provider" flow. FairPeer
// ships no configured provider by default (config.Default().Providers is empty);
// these templates let the user pick a vendor, paste a key, and get a working
// setup in three steps.
//
// The data mirrors fairpeer.example.toml's commented vendor blocks — keep them
// in sync when a vendor changes its endpoint or model lineup.

// ProviderTemplate is one vendor preset shown in the onboarding wizard. The
// user selects a template, pastes an API key, and the wizard probes the vendor
// endpoint for available models. Fields here prefill the provider config so the
// user doesn't need to know base URLs or env-var names.
type ProviderTemplate struct {
	Name          string   `json:"name"`          // provider name (qwen, deepseek, qwen-coding...)
	DisplayName   string   `json:"displayName"`   // human-readable (通义千问, DeepSeek...)
	Kind          string   `json:"kind"`          // "openai" or "anthropic"
	BaseURL       string   `json:"baseUrl"`       // API root
	APIKeyEnv     string   `json:"apiKeyEnv"`     // env var holding the key
	DefaultModel  string   `json:"defaultModel"`  // recommended default model (vendor-relative, no provider prefix)
	FastModel     string   `json:"fastModel"`      // recommended fast model
	VisionModel   string   `json:"visionModel"`   // recommended vision model ("" = same as default)
	Vision        bool     `json:"vision"`        // provider supports image input
	ContextWindow int      `json:"contextWindow"` // max context tokens
	CodingOnly    bool     `json:"codingOnly"`    // consumes Coding Plan subscription quota
	Aggregator    bool     `json:"aggregator"`    // model-aggregation platform (one key, many vendors)
	Category      string   `json:"category"`      // "direct" or "aggregator"
	DocURL        string   `json:"docUrl"`        // where to get an API key
	Models        []string `json:"models"`        // preset model list (fallback when probe fails)
}

// providerTemplates is the single source of truth for the onboarding vendor
// grid. 11 direct vendors + 7 aggregation platforms.
var providerTemplates = []ProviderTemplate{
	// ── Direct vendors (11) ──────────────────────────────────────────────
	{
		Name: "qwen", DisplayName: "通义千问 (Qwen)", Kind: "openai",
		BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", APIKeyEnv: "DASHSCOPE_API_KEY",
		DefaultModel: "qwen3.8-max", FastModel: "qwen3.7-flash", VisionModel: "qwen3.7-plus",
		Vision: true, ContextWindow: 1000000, Category: "direct",
		DocURL: "https://bailian.console.aliyun.com/?apiKey=1",
		Models: []string{"qwen3.8-max", "qwen3.7-plus", "qwen3.7-flash"},
	},
	{
		Name: "deepseek", DisplayName: "DeepSeek", Kind: "openai",
		BaseURL: "https://api.deepseek.com", APIKeyEnv: "DEEPSEEK_API_KEY",
		DefaultModel: "deepseek-v4-pro", FastModel: "deepseek-v4-flash", VisionModel: "deepseek-v4-pro",
		Vision: false, ContextWindow: 1000000, Category: "direct",
		DocURL: "https://platform.deepseek.com/api_keys",
		Models: []string{"deepseek-v4-pro", "deepseek-v4-flash"},
	},
	{
		Name: "volcengine", DisplayName: "火山引擎 (Doubao)", Kind: "openai",
		BaseURL: "https://ark.cn-beijing.volces.com/api/v3", APIKeyEnv: "VOLCENGINE_API_KEY",
		DefaultModel: "doubao-seed-evolving", FastModel: "doubao-seed-2.1-turbo", VisionModel: "doubao-seed-2.1-turbo",
		Vision: true, ContextWindow: 1000000, Category: "direct",
		DocURL: "https://console.volcengine.com/ark/region:ark+cn-beijing/apiKey",
		Models: []string{"doubao-seed-evolving", "doubao-seed-2.1-turbo"},
	},
	{
		Name: "zhipu", DisplayName: "智谱 AI (GLM)", Kind: "openai",
		BaseURL: "https://open.bigmodel.cn/api/paas/v4", APIKeyEnv: "ZHIPU_API_KEY",
		DefaultModel: "glm-5.2", FastModel: "glm-4-flash", VisionModel: "glm-5v-turbo",
		Vision: true, ContextWindow: 128000, Category: "direct",
		DocURL: "https://open.bigmodel.cn/usercenter/apikeys",
		Models: []string{"glm-5.2", "glm-5v-turbo", "glm-4-flash"},
	},
	{
		Name: "minimax", DisplayName: "MiniMax", Kind: "openai",
		BaseURL: "https://api.minimaxi.com/v1", APIKeyEnv: "MINIMAX_API_KEY",
		DefaultModel: "MiniMax-M3", FastModel: "MiniMax-M2.7", VisionModel: "MiniMax-M3",
		Vision: true, ContextWindow: 1000000, Category: "direct",
		DocURL: "https://platform.minimaxi.com/user-center/basic-information/interface-key",
		Models: []string{"MiniMax-M3", "MiniMax-M2.7"},
	},
	{
		Name: "moonshot", DisplayName: "Moonshot (Kimi)", Kind: "openai",
		BaseURL: "https://api.moonshot.cn/v1", APIKeyEnv: "MOONSHOT_API_KEY",
		DefaultModel: "kimi-k3", FastModel: "kimi-k2.6", VisionModel: "kimi-k3",
		Vision: true, ContextWindow: 1000000, Category: "direct",
		DocURL: "https://platform.moonshot.cn/console/api-keys",
		Models: []string{"kimi-k3", "kimi-k2.6"},
	},
	{
		Name: "mimo", DisplayName: "MiMo (小米)", Kind: "openai",
		BaseURL: "https://api.xiaomimimo.com/v1", APIKeyEnv: "MIMO_API_KEY",
		DefaultModel: "mimo-v2.5-pro", FastModel: "mimo-v2.5", VisionModel: "mimo-v2.5",
		Vision: true, ContextWindow: 1000000, Category: "direct",
		DocURL: "https://xiaomimimo.com/console/api-keys",
		Models: []string{"mimo-v2.5-pro", "mimo-v2.5"},
	},
	{
		Name: "stepfun", DisplayName: "阶跃星辰 (StepFun)", Kind: "openai",
		BaseURL: "https://api.stepfun.com/v1", APIKeyEnv: "STEPFUN_API_KEY",
		DefaultModel: "step-3.7-flash", FastModel: "step-3.5-flash", VisionModel: "step-3.7-flash",
		Vision: true, ContextWindow: 256000, Category: "direct",
		DocURL: "https://platform.stepfun.com/interface-key",
		Models: []string{"step-3.7-flash", "step-3.5-flash"},
	},
	{
		Name: "xfyun", DisplayName: "讯飞 MaaS (iFlytek)", Kind: "openai",
		BaseURL: "https://spark-api-open.xf-yun.com/v1", APIKeyEnv: "XFYUN_API_KEY",
		DefaultModel: "glm-5.2", FastModel: "qwen3.6-35b-a3b", VisionModel: "qwen3.5-397b-a17b",
		Vision: true, ContextWindow: 128000, Category: "direct",
		DocURL: "https://console.xfyun.cn/services/bm4",
		Models: []string{"glm-5.2", "qwen3.5-397b-a17b", "qwen3.6-35b-a3b"},
	},
	{
		Name: "anthropic", DisplayName: "Anthropic (Claude)", Kind: "anthropic",
		BaseURL: "https://api.anthropic.com", APIKeyEnv: "ANTHROPIC_API_KEY",
		DefaultModel: "claude-sonnet-5", FastModel: "claude-haiku-4-5", VisionModel: "claude-sonnet-5",
		Vision: true, ContextWindow: 1000000, Category: "direct",
		DocURL: "https://console.anthropic.com/settings/keys",
		Models: []string{"claude-sonnet-5", "claude-haiku-4-5"},
	},
	{
		Name: "openai", DisplayName: "OpenAI (GPT)", Kind: "openai",
		BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY",
		DefaultModel: "gpt-5.6-terra", FastModel: "gpt-5.6-luna", VisionModel: "gpt-5.6-terra",
		Vision: true, ContextWindow: 1050000, Category: "direct",
		DocURL: "https://platform.openai.com/api-keys",
		Models: []string{"gpt-5.6-terra", "gpt-5.6-luna"},
	},

	// ── Coding Plan aggregators (7) ─────────────────────────────────────
	{
		Name: "qwen-coding", DisplayName: "通义 Coding Plan", Kind: "openai",
		BaseURL: "https://coding.dashscope.aliyuncs.com/v1", APIKeyEnv: "QWEN_CODING_API_KEY",
		DefaultModel: "qwen3.7-plus", VisionModel: "qwen3.7-plus",
		Vision: true, ContextWindow: 1000000, CodingOnly: true, Aggregator: true, Category: "aggregator",
		DocURL: "https://bailian.console.aliyun.com/?apiKey=1",
		Models: []string{"qwen3.7-plus", "qwen3.6-plus", "kimi-k2.5", "glm-5", "minimax-m2.5", "qwen3-coder-plus", "glm-4.7"},
	},
	{
		Name: "zhipu-coding", DisplayName: "智谱 z.ai", Kind: "openai",
		BaseURL: "https://api.z.ai/v1", APIKeyEnv: "ZHIPU_CODING_API_KEY",
		DefaultModel: "glm-5.2", VisionModel: "glm-5.2",
		Vision: true, ContextWindow: 1000000, CodingOnly: true, Aggregator: true, Category: "aggregator",
		DocURL: "https://z.ai/manage/apikey",
		Models: []string{"glm-5.2", "glm-5.1", "glm-5"},
	},
	{
		Name: "volcengine-coding", DisplayName: "火山 Coding Plan", Kind: "openai",
		BaseURL: "https://ark.cn-beijing.volces.com/api/v3", APIKeyEnv: "VOLCENGINE_API_KEY",
		DefaultModel: "doubao-seed-evolving", VisionModel: "doubao-seed-evolving",
		Vision: true, ContextWindow: 1000000, CodingOnly: true, Aggregator: true, Category: "aggregator",
		DocURL: "https://console.volcengine.com/ark/region:ark+cn-beijing/apiKey",
		Models: []string{"doubao-seed-evolving", "doubao-seed-2.1-turbo", "deepseek-v4-pro", "kimi-k2.6"},
	},
	{
		Name: "baidu-coding", DisplayName: "百度千帆 Token Plan", Kind: "openai",
		BaseURL: "https://qianfan.baidubce.com/v2/tokenplan/personal", APIKeyEnv: "BAIDU_API_KEY",
		DefaultModel: "glm-5.2", VisionModel: "glm-5.2",
		Vision: true, ContextWindow: 1000000, CodingOnly: true, Aggregator: true, Category: "aggregator",
		DocURL: "https://console.bce.baidu.com/qianfan/ais/console/applicationConsole/application",
		Models: []string{"ernie-5.1", "glm-5.2", "kimi-k2.6", "deepseek-v4-pro"},
	},
	{
		Name: "tencent-coding", DisplayName: "腾讯云 TokenHub", Kind: "openai",
		BaseURL: "https://api.lkeap.cloud.tencent.com/v1", APIKeyEnv: "TENCENT_API_KEY",
		DefaultModel: "deepseek-v4-pro", VisionModel: "deepseek-v4-pro",
		Vision: true, ContextWindow: 1000000, CodingOnly: true, Aggregator: true, Category: "aggregator",
		DocURL: "https://console.cloud.tencent.com/lkeap/api-key",
		Models: []string{"deepseek-v4-pro", "glm-5.2", "kimi-k2.6", "minimax-m2.7"},
	},
	{
		Name: "stepfun-coding", DisplayName: "阶跃 Step Plan", Kind: "openai",
		BaseURL: "https://api.stepfun.com/v1", APIKeyEnv: "STEPFUN_API_KEY",
		DefaultModel: "step-3.7-flash", VisionModel: "step-3.7-flash",
		Vision: true, ContextWindow: 256000, CodingOnly: true, Aggregator: false, Category: "aggregator",
		DocURL: "https://platform.stepfun.com/interface-key",
		Models: []string{"step-3.7-flash", "step-3.5-flash"},
	},
	{
		Name: "xfyun-coding", DisplayName: "讯飞 MaaS Coding", Kind: "openai",
		BaseURL: "https://spark-api-open.xf-yun.com/v1", APIKeyEnv: "XFYUN_API_KEY",
		DefaultModel: "astron-code-latest", VisionModel: "astron-code-latest",
		Vision: true, ContextWindow: 128000, CodingOnly: true, Aggregator: true, Category: "aggregator",
		DocURL: "https://console.xfyun.cn/services/bm4",
		Models: []string{"astron-code-latest"},
	},
}

// GetProviderTemplates returns the built-in vendor presets for the onboarding
// wizard and the Settings "add provider" picker. The slice is a defensive copy
// so callers can't mutate the package-level table.
func (a *App) GetProviderTemplates() []ProviderTemplate {
	out := make([]ProviderTemplate, len(providerTemplates))
	copy(out, providerTemplates)
	return out
}
