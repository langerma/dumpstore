//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestOpenAPISpec fetches the served spec (#181) and validates the live
// response of every parameterless GET operation that declares a JSON
// schema against it. Typed handlers can't drift from their schema, but
// hand-declared response types and nullability can — this catches both on
// real hosts.
func TestOpenAPISpec(t *testing.T) {
	login(t)
	raw := apiOK(t, "GET", "/api/v1/openapi.json", nil)
	var spec struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				In       string `json:"in"`
				Required bool   `json:"required"`
			} `json:"parameters"`
			Responses map[string]struct {
				Content map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	if spec.OpenAPI != "3.1.0" {
		t.Fatalf("openapi = %q", spec.OpenAPI)
	}
	ops := 0
	for _, methods := range spec.Paths {
		ops += len(methods)
	}
	if ops < 110 {
		t.Fatalf("only %d operations in spec", ops)
	}

	v := validator{components: spec.Components.Schemas}
	var checked []string
	paths := make([]string, 0, len(spec.Paths))
	for p := range spec.Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		op, ok := spec.Paths[path]["get"]
		if !ok || !strings.HasPrefix(path, "/api/") || strings.Contains(path, "{") || path == "/api/v1/events" {
			continue
		}
		if slices.ContainsFunc(op.Parameters, func(p struct {
			In       string `json:"in"`
			Required bool   `json:"required"`
		}) bool {
			return p.Required
		}) {
			continue
		}
		content, ok := op.Responses["200"].Content["application/json"]
		if !ok || len(content.Schema) == 0 {
			continue // untyped (Phase 2 of #181) or not JSON
		}
		t.Run(op.OperationID, func(t *testing.T) {
			body := apiOK(t, "GET", path, nil)
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.UseNumber()
			var doc any
			if err := dec.Decode(&doc); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			if errs := v.validate("$", content.Schema, doc); len(errs) > 0 {
				t.Errorf("GET %s does not match its schema:\n  %s", path, strings.Join(errs, "\n  "))
			}
		})
		checked = append(checked, path)
	}
	if len(checked) < 20 {
		t.Errorf("validated only %d endpoints: %v", len(checked), checked)
	}
}

// validator checks JSON values against the JSON Schema subset the
// generator (internal/apispec) emits.
type validator struct {
	components map[string]map[string]any
}

func (v validator) validate(at string, s map[string]any, x any) []string {
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		target, ok := v.components[name]
		if !ok {
			return []string{fmt.Sprintf("%s: unresolved %s", at, ref)}
		}
		return v.validate(at, target, x)
	}
	if alts, ok := s["anyOf"].([]any); ok {
		var all []string
		for _, a := range alts {
			errs := v.validate(at, a.(map[string]any), x)
			if len(errs) == 0 {
				return nil
			}
			all = append(all, errs...)
		}
		return append([]string{at + ": matches no anyOf branch"}, all...)
	}
	var errs []string
	if parts, ok := s["allOf"].([]any); ok {
		for _, p := range parts {
			errs = append(errs, v.validate(at, p.(map[string]any), x)...)
		}
	}
	if enum, ok := s["enum"].([]any); ok && x != nil {
		if !slices.ContainsFunc(enum, func(e any) bool { return e == x }) {
			errs = append(errs, fmt.Sprintf("%s: %v not in enum %v", at, x, enum))
		}
	}

	var types []string
	switch t := s["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, e := range t {
			types = append(types, e.(string))
		}
	default:
		return errs // {} — anything goes
	}
	got := jsonType(x)
	if !slices.Contains(types, got) && !(got == "integer" && slices.Contains(types, "number")) {
		return append(errs, fmt.Sprintf("%s: got %s, want %v", at, got, types))
	}

	switch val := x.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, ok := val[r.(string)]; !ok {
					errs = append(errs, fmt.Sprintf("%s: missing required %q", at, r))
				}
			}
		}
		for k, item := range val {
			if ps, ok := props[k].(map[string]any); ok {
				errs = append(errs, v.validate(at+"."+k, ps, item)...)
			} else if ap, ok := s["additionalProperties"].(map[string]any); ok {
				errs = append(errs, v.validate(at+"["+k+"]", ap, item)...)
			} else if props != nil {
				errs = append(errs, fmt.Sprintf("%s: undocumented property %q", at, k))
			}
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, item := range val {
				errs = append(errs, v.validate(fmt.Sprintf("%s[%d]", at, i), items, item)...)
			}
		}
	case json.Number:
		if minV, ok := s["minimum"].(float64); ok {
			if f, _ := val.Float64(); f < minV {
				errs = append(errs, fmt.Sprintf("%s: %s below minimum %v", at, val, minV))
			}
		}
	}
	return errs
}

func jsonType(x any) string {
	switch val := x.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if _, err := val.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", x)
}

// TestSchemaValidatorRejects guards the drift check itself: a validator
// that accepts everything would make TestOpenAPISpec meaningless.
func TestSchemaValidatorRejects(t *testing.T) {
	v := validator{components: map[string]map[string]any{
		"Pool": {"type": "object", "required": []any{"name"}, "properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"size": map[string]any{"type": "integer", "minimum": float64(0)},
		}},
	}}
	ref := map[string]any{"$ref": "#/components/schemas/Pool"}
	parse := func(s string) any {
		dec := json.NewDecoder(strings.NewReader(s))
		dec.UseNumber()
		var x any
		if err := dec.Decode(&x); err != nil {
			t.Fatal(err)
		}
		return x
	}
	if errs := v.validate("$", ref, parse(`{"name":"tank","size":1}`)); len(errs) != 0 {
		t.Fatalf("valid value rejected: %v", errs)
	}
	for _, bad := range []string{
		`{"size":1}`,                   // missing required
		`{"name":3}`,                   // wrong type
		`{"name":"tank","size":-1}`,    // below minimum
		`{"name":"tank","extra":true}`, // undocumented property
		`null`,                         // not nullable
	} {
		if errs := v.validate("$", ref, parse(bad)); len(errs) == 0 {
			t.Errorf("accepted %s", bad)
		}
	}
	if errs := v.validate("$", map[string]any{"$ref": "#/components/schemas/Nope"}, parse(`{}`)); len(errs) == 0 {
		t.Error("accepted an unresolved $ref")
	}
}
