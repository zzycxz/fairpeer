package agent

import (
	"strings"
)

// overflowPatterns match the way OpenAI-compatible gateways and Anthropic word
// a rejected-too-large request. The set mirrors dsh's isContextWindowExceeded
// classifier (packages/llm/llm/src/error.ts): each vendor/gateway spells the
// rejection differently, and the recovery (force-compact + retry the SAME
// request once) is strictly better than failing the turn, so matching a bit
// broadly is cheap compared to missing one.
var overflowPatterns = []string{
	"context length",                // OpenAI: "This model's maximum context length is ..."
	"context_length_exceeded",      // OpenAI error code
	"maximum context",              // short form across gateways
	"prompt is too long",           // Anthropic / several gateways
	"input length and `max_tokens`",// vLLM wording
	"input tokens exceed",          // paraphrase used by relay gateways
	"exceeds the context window",   // paraphrase
	"exceeds maximum number of tokens", // paraphrase
	"reduce the length",            // Anthropic: "…reduce the length of the messages or system prompt"
	"context window",               // generic last resort (substring of several above)
}

// isContextOverflowError reports whether err is (or wraps) a provider rejection
// caused by the request exceeding the model's context window.
func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range overflowPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
