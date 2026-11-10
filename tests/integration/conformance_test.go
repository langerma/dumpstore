//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	dsapi "dumpstore/internal/api"
	"dumpstore/internal/apispec"
	"dumpstore/internal/auth"
)

// Every JSON response the suite receives is recorded and, after all tests,
// checked against the served OpenAPI spec (#181): the body must match the
// schema documented for its operation and status, and every 4xx status
// must be documented explicitly. This covers writes and error responses,
// not only the parameterless GETs TestOpenAPISpec samples.
//
// DUMPSTORE_RECORD=<file> additionally writes the recordings as NDJSON
// (route pattern, status, body) for offline comparison between builds.

type recording struct {
	Method  string          `json:"method"`
	Path    string          `json:"path"`
	Pattern string          `json:"pattern"`
	Status  int             `json:"status"`
	Body    json.RawMessage `json:"body"`
}

var (
	recMu      sync.Mutex
	recordings []recording
)

// recorder captures JSON responses passing through the shared client.
type recorder struct{ next http.RoundTripper }

func (rt recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := rt.next.RoundTrip(req)
	if err != nil || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		return resp, err
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(b))
	if json.Valid(b) {
		recMu.Lock()
		recordings = append(recordings, recording{Method: req.Method, Path: req.URL.EscapedPath() + query(req.URL), Status: resp.StatusCode, Body: b})
		recMu.Unlock()
	}
	return resp, nil
}

func query(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	return "?" + u.RawQuery
}

// routeMux resolves requests to route patterns exactly as the server does.
func routeMux() (*http.ServeMux, map[string]apispec.Route) {
	mux := http.NewServeMux()
	reg := dsapi.NewRegistry(mux)
	auth.RegisterRoutes(reg, &auth.Config{}, auth.NewSessionStore(time.Hour), auth.NewRateLimiter())
	(&dsapi.Handler{}).RegisterRoutes(reg)
	routes := map[string]apispec.Route{}
	for _, r := range reg.Routes() {
		routes[r.Method+" "+r.Path] = r
	}
	return mux, routes
}

// checkConformance validates all recordings; it returns the violations.
func checkConformance() []string {
	spec, err := fetchSpec()
	if err != nil {
		return []string{"fetch spec: " + err.Error()}
	}
	mux, routes := routeMux()
	v := validator{components: spec.Components.Schemas}

	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	var dump *json.Encoder
	if f := os.Getenv("DUMPSTORE_RECORD"); f != "" {
		fh, err := os.Create(f)
		if err != nil {
			return []string{"DUMPSTORE_RECORD: " + err.Error()}
		}
		defer fh.Close()
		dump = json.NewEncoder(fh)
	}

	recMu.Lock()
	defer recMu.Unlock()
	for _, rec := range recordings {
		req, err := http.NewRequest(rec.Method, "http://x"+rec.Path, nil)
		if err != nil {
			add(fmt.Sprintf("%s %s: %v", rec.Method, rec.Path, err))
			continue
		}
		_, pattern := mux.Handler(req)
		rec.Pattern = pattern
		if dump != nil {
			_ = dump.Encode(rec)
		}
		route, ok := routes[pattern]
		if !ok {
			continue // not an API route (e.g. 404 from the file server)
		}
		op, ok := spec.ops[route.OperationID()]
		if !ok {
			add(fmt.Sprintf("%s: operation %s missing from the served spec", pattern, route.OperationID()))
			continue
		}
		code := fmt.Sprint(rec.Status)
		resp, documented := op.Responses[code]
		if !documented {
			if rec.Status >= 400 && rec.Status < 500 {
				add(fmt.Sprintf("%s: status %d not documented (add it to fails(...))", pattern, rec.Status))
				continue
			}
			resp, documented = op.Responses["default"]
		}
		if !documented {
			add(fmt.Sprintf("%s: status %d not documented", pattern, rec.Status))
			continue
		}
		schema := resp.schema(spec)
		if schema == nil {
			continue // no body documented (e.g. 204)
		}
		dec := json.NewDecoder(bytes.NewReader(rec.Body))
		dec.UseNumber()
		var doc any
		if err := dec.Decode(&doc); err != nil {
			add(fmt.Sprintf("%s %d: %v", pattern, rec.Status, err))
			continue
		}
		for _, e := range v.validate("$", schema, doc) {
			add(fmt.Sprintf("%s %d: %s", pattern, rec.Status, e))
		}
	}
	sort.Strings(out)
	return out
}

type specResponse struct {
	Ref     string `json:"$ref"`
	Content map[string]struct {
		Schema map[string]any `json:"schema"`
	} `json:"content"`
}

// schema returns the JSON body schema, following a components.responses $ref.
func (r specResponse) schema(s *servedSpec) map[string]any {
	if r.Ref != "" {
		r = s.Components.Responses[strings.TrimPrefix(r.Ref, "#/components/responses/")]
	}
	if c, ok := r.Content["application/json"]; ok {
		return c.Schema
	}
	return nil
}

type specOperation struct {
	OperationID string                  `json:"operationId"`
	Responses   map[string]specResponse `json:"responses"`
}

type servedSpec struct {
	Paths      map[string]map[string]specOperation `json:"paths"`
	Components struct {
		Schemas   map[string]map[string]any `json:"schemas"`
		Responses map[string]specResponse   `json:"responses"`
	} `json:"components"`
	ops map[string]specOperation
}

func fetchSpec() (*servedSpec, error) {
	form := url.Values{"username": {adminUser}, "password": {adminPass}}
	if resp, err := client.PostForm(baseURL+"/auth/login", form); err == nil {
		resp.Body.Close()
	}
	resp, err := client.Get(baseURL + "/api/v1/openapi.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var s servedSpec
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	s.ops = map[string]specOperation{}
	for _, methods := range s.Paths {
		for _, op := range methods {
			s.ops[op.OperationID] = op
		}
	}
	return &s, nil
}
