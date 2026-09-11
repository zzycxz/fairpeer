package config

import (
	"strings"
	"testing"
)

// NEW-12: the four [agent] resilience knobs must survive a render round-trip
// (any SaveTo rewrite used to drop keys the renderer didn't know).
func TestRenderTOMLKeepsResilienceKeys(t *testing.T) {
	c := Default()
	c.Agent.StreamRecoveries = 5
	c.Agent.RetryMaxAttempts = 7
	c.Agent.RetryBackoffMaxSec = 20
	c.Agent.RetryMode = "always"

	out := RenderTOMLForScope(c, RenderScopeFull)
	for _, key := range []string{"stream_recoveries", "retry_max_attempts", "retry_backoff_max_sec", "retry_mode"} {
		if !strings.Contains(out, key) {
			t.Errorf("render output lost %q", key)
		}
	}
}
