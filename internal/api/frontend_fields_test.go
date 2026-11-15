package api

import (
	"encoding/json"
	"maps"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	// const result = await op.api('PUT', v1`/smb/homes`, …) — variable, method, template.
	reAssignedCall = regexp.MustCompile("const (\\w+) = await (?:op\\.)?api\\(\\s*'(GET|POST|PUT|PATCH|DELETE)'\\s*,\\s*v1`([^`]*)`")
	reCatch        = regexp.MustCompile(`\bcatch\b`)
)

// TestFrontendResponseFields catches the frontend reading a field that the
// response doesn't have (#231: after #181 typed the handlers, the homes
// dialog read `result.config` from a flat result, stored undefined, and
// broke the Users & Groups tab). For every `const x = await (op.)api(M,
// v1`…`)`, each `x.field` read before the next `catch` must be a property of
// the operation's success response in the committed spec. Responses that are
// maps or arrays are skipped: their keys aren't in the schema.
func TestFrontendResponseFields(t *testing.T) {
	mux := fullRegistry(t).Mux()
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec openAPIDoc
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}

	files, _ := filepath.Glob("../../static/js/*.js")
	app, _ := filepath.Glob("../../static/*.js")
	files = append(files, app...)

	total := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, m := range reAssignedCall.FindAllStringSubmatchIndex(src, -1) {
			v, method, tmpl := src[m[2]:m[3]], src[m[4]:m[5]], src[m[6]:m[7]]
			path := reInterp.ReplaceAllString(tmpl, "x%2Fy")
			path, _, _ = strings.Cut(path, "?")
			_, pattern := mux.Handler(httptest.NewRequest(method, "/api/v1"+path, nil))
			_, specPath, ok := strings.Cut(pattern, " ")
			if !ok {
				continue // unmatched route: TestFrontendPaths reports it
			}
			fields, checkable := spec.responseFields(specPath, method)
			if !checkable {
				continue
			}

			// The variable's uses: up to the next catch (error handling reads
			// err.*, not the response).
			rest := src[m[1]:]
			if c := reCatch.FindStringIndex(rest); c != nil {
				rest = rest[:c[0]]
			}
			reUse := regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\.(\w+)`)
			for _, u := range reUse.FindAllStringSubmatch(rest, -1) {
				total++
				if !fields[u[1]] {
					line := strings.Count(src[:m[0]], "\n") + 1
					t.Errorf("%s:%d: %s.%s, but %s %s responds with %v", filepath.Base(f), line, v, u[1], method, specPath, slices.Sorted(maps.Keys(fields)))
				}
			}
		}
	}
	if total == 0 {
		t.Fatal("no response field reads found — did the call pattern change?")
	}
}

type openAPIDoc struct {
	Paths      map[string]map[string]openAPIOp `json:"paths"`
	Components struct {
		Schemas map[string]openAPISchema `json:"schemas"`
	} `json:"components"`
}

type openAPIOp struct {
	Responses map[string]struct {
		Content map[string]struct {
			Schema openAPISchema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

type openAPISchema struct {
	Ref                  string                   `json:"$ref"`
	Type                 any                      `json:"type"`
	Properties           map[string]openAPISchema `json:"properties"`
	AdditionalProperties any                      `json:"additionalProperties"`
	AllOf                []openAPISchema          `json:"allOf"`
}

// responseFields returns the property names of the operation's 2xx JSON
// response, and false when the response is not an object with fixed
// properties (no body, a map, or an array).
func (d *openAPIDoc) responseFields(path, method string) (map[string]bool, bool) {
	op, ok := d.Paths[path][strings.ToLower(method)]
	if !ok {
		return nil, false
	}
	for code, r := range op.Responses {
		if !strings.HasPrefix(code, "2") {
			continue
		}
		c, ok := r.Content["application/json"]
		if !ok {
			return nil, false
		}
		fields := map[string]bool{}
		if !d.collect(c.Schema, fields) {
			return nil, false
		}
		return fields, true
	}
	return nil, false
}

func (d *openAPIDoc) collect(s openAPISchema, into map[string]bool) bool {
	for s.Ref != "" {
		s = d.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties != false {
		return false // map: keys are data
	}
	if typ, _ := json.Marshal(s.Type); strings.Contains(string(typ), "array") {
		return false
	}
	for name := range s.Properties {
		into[name] = true
	}
	for _, sub := range s.AllOf {
		if !d.collect(sub, into) {
			return false
		}
	}
	return len(into) > 0
}
