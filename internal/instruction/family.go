package instruction

import "strings"

// family.go adds model-family-specific prompt addons. The base DefaultSystemPrompt
// (config.go) is model-agnostic; ForModel (instruction.go) layers on top of it.
//
// Family addons target the specific failure modes observed in testing — they are
// concise, surgical nudges, not full prompt rewrites. Addons exist ONLY for
// families with a measured failure mode; recognising a family without an addon
// is normal and expected.

// vendorPrefixes maps canonical provider prefixes ("vendor/model") to families.
// Prefix matches win over token matches: a provider namespace is the strongest
// identity signal a model ref carries.
var vendorPrefixes = []struct{ prefix, family string }{
	{"qwen/", "qwen"},
	{"deepseek/", "deepseek"},
	{"z.ai/", "glm"},
	{"moonshotai/", "kimi"},
	{"moonshot/", "kimi"},
	{"minimax/", "minimax"},
	{"minimaxi/", "minimax"},
	{"openai/", "gpt"},
	{"anthropic/", "anthropic"},
}

// familyTokens maps whole model-name tokens to families. The ID is split on
// separators (- _ . / :) and a token must match EXACTLY — substring hits inside
// a larger token ("glmw" contains "glm") must not classify. Version suffixes
// sit in their own tokens ("GLM-4.6" → glm, 4, 6) so bare model names classify
// without a provider prefix.
var familyTokens = map[string]string{
	"qwen":     "qwen",
	"qwq":      "qwen",
	"deepseek": "deepseek",
	"glm":      "glm",
	"kimi":     "kimi",
	"minimax":  "minimax",
	"gpt":      "gpt",
	"chatgpt":  "gpt",
	"o1":       "gpt",
	"o3":       "gpt",
	"o4":       "gpt",
	"claude":   "anthropic",
}

// ModelFamily detects the vendor family from a model ID like "openai/gpt-4o",
// "qwen/qwen3-max" or bare "gpt-4o". Two-stage deterministic match (no
// substring Contains): vendor prefix first, then exact whole-token match on
// the split model name. Unknown → "" (caller applies no addon).
func ModelFamily(modelID string) string {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return ""
	}

	for _, vp := range vendorPrefixes {
		if strings.HasPrefix(id, vp.prefix) {
			return vp.family
		}
	}

	for _, tok := range strings.FieldsFunc(id, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/' || r == ':'
	}) {
		if family, ok := familyTokens[tok]; ok {
			return family
		}
		// Version-digit suffixes fuse into one token in some catalog names
		// ("qwen3-max" → token "qwen3"): accept key + digit-run only — a
		// letter suffix ("glmw") is a different name and must not classify.
		for key, family := range familyTokens {
			if len(tok) > len(key) && strings.HasPrefix(tok, key) && tok[len(key)] >= '0' && tok[len(key)] <= '9' {
				return family
			}
		}
	}
	return ""
}

// FamilyAddon returns a model-family-specific prompt nudge, or "" when the
// family is unknown or needs no special handling. These addons are appended
// after the base system prompt + thinking/serial addons (see ForModel).
//
// Each addon targets observed failure modes:
//   - qwen: tool-call format drift (mixed prose + tool args)
//   - glm: parallel tool calls sometimes drop; serial is safer
//   - deepseek: weak instruction following on multi-step; needs explicit sequencing
//   - kimi: over-elaboration; needs conciseness guard
func FamilyAddon(family string) string {
	switch family {
	case "qwen":
		return QwenAddon
	case "glm":
		return GLMAddon
	case "deepseek":
		return DeepSeekAddon
	case "kimi":
		return KimiAddon
	default:
		return ""
	}
}

const QwenAddon = `When you decide to call a tool, output ONLY the tool call — do not mix prose with tool arguments in the same response. Each tool call must have well-formed JSON arguments matching the tool's schema.`

const GLMAddon = `Prefer calling one tool per message when the tools have data dependencies. If multiple independent read-only calls are safe to batch, you may, but if a tool call fails or returns unexpected output, switch to sequential calls so each result is visible before the next decision.`

const DeepSeekAddon = `Break multi-step tasks into explicit, sequential steps: complete one step, verify its result, then proceed to the next. Do not attempt to plan multiple file edits in your head without reading each result first. After each edit, confirm the change succeeded before moving on.`

const KimiAddon = `Be concise. Answer the question directly without restating it. When editing code, make the minimal change — do not refactor surrounding code unless asked. Keep explanations of your changes to one or two sentences.`
