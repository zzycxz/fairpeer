package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zzycxz/fairpeer/internal/provider"
)

// TestResponseFormatAndCacheKeyOnWire (upgrade spec 4-7/4-6) captures the
// request body and asserts: response_format json_schema is present exactly
// when ResponseSchema is set (with the caller's schema name), prompt_cache_key
// passes through clamped, and neither leaks into a plain request.
func TestResponseFormatAndCacheKeyOnWire(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	p, err := New(provider.Config{Name: "t", BaseURL: srv.URL, Model: "t/m", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}

	drain := func(req provider.Request) {
		ch, err := p.Stream(context.Background(), req)
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range ch {
		}
	}

	drain(provider.Request{
		Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
		ResponseSchema: json.RawMessage(`{"type":"object"}`),
		SchemaName:     "verdict",
		CacheKey:       strings.Repeat("k", 80),
	})
	drain(provider.Request{
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
	})

	if len(bodies) != 2 {
		t.Fatalf("captured %d bodies, want 2", len(bodies))
	}
	var with struct {
		ResponseFormat struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string          `json:"name"`
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		} `json:"response_format"`
		PromptCacheKey string `json:"prompt_cache_key"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &with); err != nil {
		t.Fatalf("body[0]: %v", err)
	}
	if with.ResponseFormat.Type != "json_schema" || with.ResponseFormat.JSONSchema.Name != "verdict" {
		t.Fatalf("response_format = %+v", with.ResponseFormat)
	}
	if len(with.PromptCacheKey) != 64 {
		t.Fatalf("prompt_cache_key len = %d, want clamped 64", len(with.PromptCacheKey))
	}
	if strings.Contains(bodies[1], "response_format") || strings.Contains(bodies[1], "prompt_cache_key") {
		t.Fatalf("plain request leaked optional fields: %s", bodies[1])
	}
}

// TestSchemaStrictCompatible gates json_schema.strict: only schemas meeting
// OpenAI's strict-mode rules (every object closed via additionalProperties:false
// and every property required, recursively) may set it — the API 400s otherwise.
func TestSchemaStrictCompatible(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   bool
	}{
		{"fully strict", `{"type":"object","additionalProperties":false,"required":["a","b"],"properties":{"a":{"type":"string"},"b":{"type":"object","additionalProperties":false,"required":["c"],"properties":{"c":{"type":"number"}}}}}`, true},
		{"closed empty object", `{"type":"object","additionalProperties":false}`, true},
		{"optional property", `{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`, false},
		{"open object", `{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`, false},
		{"missing additionalProperties literal", `{"type":"object","required":[],"properties":{},"additionalProperties":{"type":"string"}}`, false},
		{"open nested object", `{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"object","required":[],"properties":{}}}}`, false},
		{"composition", `{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"anyOf":[{"type":"string"},{"type":"number"}]}}}`, false},
		{"ref", `{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"$ref":"#/$defs/x"}}}`, false},
		{"array items strict", `{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["k"],"properties":{"k":{"type":"string"}}}}}}`, true},
		{"array items open", `{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"array","items":{"type":"object"}}}}`, false},
		{"no required key", `{"type":"object","additionalProperties":false,"properties":{"a":{"type":"string"}}}`, false},
		{"non-object root", `{"type":"string"}`, false},
		{"invalid json", `{`, false},
	}
	for _, tc := range cases {
		if got := schemaStrictCompatible(json.RawMessage(tc.schema)); got != tc.want {
			t.Errorf("%s: schemaStrictCompatible(%s) = %v, want %v", tc.name, tc.schema, got, tc.want)
		}
	}
}

// TestResponseFormatStrictOnWire checks the strict flag reaches the request
// body only for a qualifying schema — and is omitted (not false) otherwise.
func TestResponseFormatStrictOnWire(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	p, err := New(provider.Config{Name: "t", BaseURL: srv.URL, Model: "t/m", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	drain := func(schema string) {
		body = ""
		ch, err := p.Stream(context.Background(), provider.Request{
			Messages:       []provider.Message{{Role: provider.RoleUser, Content: "hi"}},
			ResponseSchema: json.RawMessage(schema),
			SchemaName:     "verdict",
		})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		for range ch {
		}
		if body == "" {
			t.Fatal("no request body captured")
		}
	}

	drain(`{"type":"object","additionalProperties":false,"required":["ok"],"properties":{"ok":{"type":"boolean"}}}`)
	if !strings.Contains(body, `"strict":true`) {
		t.Fatalf("strict-compatible schema should set strict:true: %s", body)
	}

	drain(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`)
	if strings.Contains(body, `"strict"`) {
		t.Fatalf("non-strict schema must omit strict entirely: %s", body)
	}
}
