// schema_strict.go — gating for OpenAI structured outputs' strict mode.
//
// json_schema.strict=true makes the API guarantee the reply conforms to the
// schema, but it rejects schemas that don't meet strict mode's own rules with
// a 400. We can only opt in when the schema qualifies on its own, so this
// checker walks the schema and verifies the two hard requirements — every
// object is closed (additionalProperties:false) and lists all of its
// properties in required — recursing into every subschema position. Anything
// the checker can't fully verify (composition, $ref, patterns) disqualifies:
// omitting strict is always safe, sending it wrongly fails the whole request.
package openai

import "encoding/json"

// strictDisqualifying lists schema keywords whose conformance this checker
// can't prove. Their mere presence keeps strict off.
var strictDisqualifying = []string{
	"allOf", "anyOf", "oneOf", "not", "if", "then", "else", "contains",
	"propertyNames", "$ref", "patternProperties", "unevaluatedProperties",
	"unevaluatedItems",
}

// schemaStrictCompatible reports whether raw qualifies for strict mode.
func schemaStrictCompatible(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false // strict mode requires an object schema at the root
	}
	// The root itself must be an object schema: a plain "type" of object, or
	// properties present (draft-style shorthand). Strict output is always a
	// single JSON object, so a string/array root can't comply.
	rootType, typeIsString := m["type"].(string)
	_, hasProps := m["properties"]
	if !hasProps && !(typeIsString && rootType == "object") {
		return false
	}
	return strictSchemaNode(m)
}

// strictSchemaNode checks one schema node (a JSON object). Boolean schemas
// carry no requirements and pass.
func strictSchemaNode(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return true
	}
	for _, key := range strictDisqualifying {
		if _, ok := m[key]; ok {
			return false
		}
	}
	props, hasProps := m["properties"].(map[string]any)
	if hasProps || schemaTypeIsObject(m) {
		if m["additionalProperties"] != false {
			return false // open object
		}
		if hasProps && !allPropertiesRequired(m, props) {
			return false // optional property present
		}
	}
	for _, sub := range props {
		if !strictSchemaNode(sub) {
			return false
		}
	}
	switch items := m["items"].(type) {
	case map[string]any:
		if !strictSchemaNode(items) {
			return false
		}
	case []any:
		for _, sub := range items {
			if !strictSchemaNode(sub) {
				return false
			}
		}
	}
	if arr, ok := m["prefixItems"].([]any); ok {
		for _, sub := range arr {
			if !strictSchemaNode(sub) {
				return false
			}
		}
	}
	// additionalProperties may be a schema (values allowed but constrained)
	// rather than the literal false — the object check above already rejected
	// that case for objects, so this only fires for non-object parents.
	if ap, ok := m["additionalProperties"].(map[string]any); ok {
		if !strictSchemaNode(ap) {
			return false
		}
	}
	for _, key := range []string{"$defs", "definitions"} {
		defs, ok := m[key].(map[string]any)
		if !ok {
			continue
		}
		for _, sub := range defs {
			if !strictSchemaNode(sub) {
				return false
			}
		}
	}
	return true
}

func schemaTypeIsObject(m map[string]any) bool {
	switch t := m["type"].(type) {
	case string:
		return t == "object"
	case []any: // multi-typed schemas: ["object","null"] and friends
		for _, e := range t {
			if s, ok := e.(string); ok && s == "object" {
				return true
			}
		}
	}
	return false
}

func allPropertiesRequired(m, props map[string]any) bool {
	req, ok := m["required"].([]any)
	if !ok {
		return false
	}
	seen := make(map[string]bool, len(req))
	for _, r := range req {
		if s, ok := r.(string); ok {
			seen[s] = true
		}
	}
	for name := range props {
		if !seen[name] {
			return false
		}
	}
	return true
}
