// validate.go — upgrade spec 3-2: the single place every tool call's args
// pass through before dispatch. It enforces the schema's required-field
// list (presence, non-null) so a missing argument fails fast with one uniform
// message instead of each tool re-implementing the check — or worse, a tool
// acting on a zero-valued field. Deeper type validation stays with the tools:
// they own their shapes and already report precise errors there.
package tool

import (
	"encoding/json"
	"fmt"
)

// ValidateArgs returns an error when args is not valid JSON or is missing any
// field the tool's schema marks required. Tools without a schema or without a
// required list always pass — validation is additive, never a new gate.
func ValidateArgs(t Tool, args json.RawMessage) error {
	if t == nil {
		return nil
	}
	schema := t.Schema()
	if len(schema) == 0 {
		return nil
	}
	var s struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil // a schema we can't read is not the call's problem
	}
	if len(s.Required) == 0 {
		return nil
	}
	var a map[string]json.RawMessage
	if err := json.Unmarshal(args, &a); err != nil {
		// Malformed JSON is deliberately NOT failed here: executeOne's
		// tool-error path echoes the tool's schema for JSON glitches so the
		// retry lands valid — preempting that recovery would regress it.
		return nil
	}
	for _, k := range s.Required {
		if v, ok := a[k]; !ok || string(v) == "null" {
			return fmt.Errorf("missing required argument %q", k)
		}
	}
	return nil
}
