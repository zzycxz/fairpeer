package agent

import (
	"errors"
	"fmt"
	"testing"
)

// P1-A2: the overflow classifier must catch the spellings used by the major
// gateways (mirrors dsh's isContextWindowExceeded set) and stay quiet on the
// errors that merely look similar.
func TestIsContextOverflowError(t *testing.T) {
	yes := []string{
		"openai: 400 This model's maximum context length is 65536 tokens. However, your messages resulted in 90000 tokens",
		"anthropic: 400 prompt is too long: 200000 tokens > 190000 maximum",
		"relay: error code: context_length_exceeded",
		"gateway: input length and `max_tokens` exceed context limit: 131072 > 128000",
		"vendor: request exceeds the context window of this model",
		"vendor: please reduce the length of the messages or system prompt",
		"vendor: input tokens exceed the model's limit of 32768",
	}
	for _, m := range yes {
		if !isContextOverflowError(errors.New(m)) {
			t.Errorf("should classify as overflow: %q", m)
		}
		// wrapped errors too
		if !isContextOverflowError(fmt.Errorf("stream: %w", errors.New(m))) {
			t.Errorf("wrapped should classify: %q", m)
		}
	}
	no := []string{
		"openai: 401 invalid api key",
		"openai: 429 rate limit exceeded",
		"connection reset by peer",
		"openai: 400 invalid request: unknown parameter",
	}
	for _, m := range no {
		if isContextOverflowError(errors.New(m)) {
			t.Errorf("false positive: %q", m)
		}
	}
}
