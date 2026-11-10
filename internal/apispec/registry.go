// Package apispec is dumpstore's route registry and OpenAPI 3.1 generator
// (#181). Every HTTP route is registered together with its metadata — SDK
// group and method name, summary, parameter/request/response types — so the
// published spec (GET /api/v1/openapi.json, committed as api/openapi.json)
// is derived from the same registrations the mux serves and cannot drift
// from them.
package apispec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Version is the API contract version reported in info.version. It is
// independent of the build version so the committed spec stays stable;
// bump it when the API surface changes.
const Version = "0.1.0"

// Content types for Route.RequestType / Route.ResponseType.
const (
	JSON  = "application/json"
	Form  = "application/x-www-form-urlencoded"
	Text  = "text/plain"
	HTML  = "text/html"
	Event = "text/event-stream"
)

// OperationIDHeader is the write-operation header handled by the API's
// Operations middleware (#184). Documented on every write.
const OperationIDHeader = "X-Operation-ID"

// Route is one HTTP operation: what the mux serves and what the spec says.
type Route struct {
	Method string // GET, POST, …
	Path   string // Go ServeMux pattern path, e.g. /api/v1/datasets/{name}

	Group       string // SDK group (x-fern-sdk-group-name), e.g. "datasets.acl"
	Name        string // SDK method (x-fern-sdk-method-name), e.g. "get"
	Summary     string
	Description string

	// Params is a struct type whose fields carry path:"x" / query:"x" tags
	// (plus optional doc:"…", and required:"true" for a mandatory query
	// parameter). Every {x} in Path must be declared.
	Params reflect.Type
	// Request is the request body type (nil: no body).
	Request reflect.Type
	// RequestOptional marks the body as optional (requestBody.required: false).
	RequestOptional bool
	// Response is the success body type (nil: undocumented — see Untyped).
	Response reflect.Type

	Status       int    // success status; 0 means 200
	Errors       []int  // documented 4xx statuses (500 is the default response)
	RequestType  string // request content type; "" means JSON
	ResponseType string // success content type; "" means JSON

	Public   bool // no authentication required (security: [])
	Internal bool // x-forge-internal: not part of generated SDKs

	Handler http.Handler
}

// Untyped reports whether a JSON route still lacks a response type.
func (r Route) Untyped() bool {
	if r.Status == http.StatusNoContent || r.Status >= 300 && r.Status < 400 {
		return false
	}
	return r.Response == nil && (r.ResponseType == "" || r.ResponseType == JSON)
}

// OperationID derives the operationId from the SDK group and method:
// "datasets.acl" + "get" → "datasetsAclGet".
func (r Route) OperationID() string {
	parts := append(strings.Split(r.Group, "."), r.Name)
	var b strings.Builder
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i > 0 {
			rs := []rune(p)
			rs[0] = unicode.ToUpper(rs[0])
			p = string(rs)
		}
		b.WriteString(p)
	}
	return b.String()
}

// Tag is the docs section: the first segment of the SDK group.
func (r Route) Tag() string {
	t, _, _ := strings.Cut(r.Group, ".")
	return t
}

var pathParamRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(\.\.\.)?\}`)

// OpenAPIPath converts the Go pattern path to an OpenAPI path template.
func (r Route) OpenAPIPath() string {
	return pathParamRe.ReplaceAllString(r.Path, "{$1}")
}

func (r Route) isWrite() bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return strings.HasPrefix(r.Path, "/api/")
}

// Registry registers routes on a ServeMux and records them for the spec.
type Registry struct {
	mux    *http.ServeMux
	routes []Route
	ids    map[string]bool

	// Error is the error response body type, shared by every operation.
	Error reflect.Type
	// TaskStep is the op-log step type; registered as a component so SDKs
	// get it even where a response is still untyped.
	TaskStep reflect.Type
	// Tags describes the docs sections (first segment of each SDK group).
	// Every tag in use must be described.
	Tags map[string]string
	// Names overrides components.schemas names (default: the Go type name,
	// first letter upper-cased). These become public SDK type names, so
	// generic Go names (Result, Task, …) are renamed here, in the API layer,
	// rather than in the domain packages.
	Names map[reflect.Type]string
}

// New returns a Registry serving on mux.
func New(mux *http.ServeMux) *Registry {
	return &Registry{mux: mux, ids: map[string]bool{}}
}

// Mux returns the underlying mux, for handlers outside the API surface
// (static files).
func (reg *Registry) Mux() *http.ServeMux { return reg.mux }

// Routes returns the registered routes in registration order.
func (reg *Registry) Routes() []Route { return slices.Clone(reg.routes) }

// Handle validates r's metadata, registers it on the mux and records it.
// Metadata errors are programming errors and panic at startup (and in the
// unit tests that build the full registry).
func (reg *Registry) Handle(r Route) {
	if err := r.validate(); err != nil {
		panic(fmt.Sprintf("apispec: %s %s: %v", r.Method, r.Path, err))
	}
	id := r.OperationID()
	if reg.ids[id] {
		panic(fmt.Sprintf("apispec: %s %s: duplicate operation %s.%s", r.Method, r.Path, r.Group, r.Name))
	}
	reg.ids[id] = true
	reg.mux.Handle(r.Method+" "+r.Path, r.Handler)
	reg.routes = append(reg.routes, r)
}

func (r Route) validate() error {
	switch {
	case r.Method == "" || r.Path == "":
		return fmt.Errorf("method and path required")
	case r.Group == "" || r.Name == "":
		return fmt.Errorf("SDK group and method name required")
	case r.Summary == "":
		return fmt.Errorf("summary required")
	case r.Handler == nil:
		return fmt.Errorf("handler required")
	}
	want := map[string]bool{}
	for _, m := range pathParamRe.FindAllStringSubmatch(r.Path, -1) {
		want[m[1]] = true
	}
	got := map[string]bool{}
	if r.Params != nil {
		if r.Params.Kind() != reflect.Struct {
			return fmt.Errorf("params must be a struct, got %s", r.Params)
		}
		for _, f := range reflect.VisibleFields(r.Params) {
			if p := f.Tag.Get("path"); p != "" {
				got[p] = true
			}
		}
	}
	for p := range want {
		if !got[p] {
			return fmt.Errorf("path parameter {%s} not declared in Params", p)
		}
	}
	for p := range got {
		if !want[p] {
			return fmt.Errorf("Params declares path parameter %q not in the path", p)
		}
	}
	return nil
}

// Spec renders the OpenAPI 3.1 document: deterministic, indented JSON with
// a trailing newline (the committed api/openapi.json is byte-compared).
func (reg *Registry) Spec() ([]byte, error) {
	g := newGenerator(reg.Names)
	errSchema := Schema{"type": "object"}
	if reg.Error != nil {
		s, err := g.schema(reg.Error)
		if err != nil {
			return nil, fmt.Errorf("error type: %w", err)
		}
		errSchema = s
	}
	if reg.TaskStep != nil {
		if _, err := g.schema(reg.TaskStep); err != nil {
			return nil, fmt.Errorf("task step type: %w", err)
		}
	}

	paths := map[string]Schema{}
	tags := map[string]bool{}
	for _, r := range reg.routes {
		op, err := reg.operation(g, r)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", r.Method, r.Path, err)
		}
		p := r.OpenAPIPath()
		if paths[p] == nil {
			paths[p] = Schema{}
		}
		paths[p][strings.ToLower(r.Method)] = op
		tags[r.Tag()] = true
	}
	var tagList []any
	for _, t := range slices.Sorted(maps.Keys(tags)) {
		desc := reg.Tags[t]
		if desc == "" {
			return nil, fmt.Errorf("tag %q has no description in Registry.Tags", t)
		}
		tagList = append(tagList, Schema{"name": t, "description": desc})
	}

	doc := Schema{
		"openapi": "3.1.0",
		"info": Schema{
			"title":   "dumpstore API",
			"version": Version,
			"description": "HTTP API of dumpstore, a web UI for ZFS and file sharing. " +
				"Generated from the server's route registry (internal/apispec). " +
				"Dataset, snapshot and device names contain '/' and '@' — percent-encode them in path parameters. " +
				"Every write accepts an optional " + OperationIDHeader + " header; its progress steps are published on the ansible.progress SSE topic and the result can be fetched from GET /api/v1/operations/{id}.",
			"license": Schema{"name": "BSD-2-Clause", "identifier": "BSD-2-Clause"},
		},
		"servers":  []any{Schema{"url": "/"}},
		"security": []any{Schema{"session": []any{}}, Schema{"proxyUser": []any{}}},
		"tags":     tagList,
		"paths":    paths,
		"components": Schema{
			"schemas": g.components,
			"responses": Schema{
				"Error": Schema{
					"description": "Error",
					"content":     Schema{JSON: Schema{"schema": errSchema}},
				},
			},
			"parameters": Schema{
				"OperationID": Schema{
					"name":        OperationIDHeader,
					"in":          "header",
					"required":    false,
					"description": "Client-chosen ID for this write (8–64 characters of [A-Za-z0-9-]). Generated by the server when absent; echoed in the response header. Reusing an ID returns 409 with duplicate_operation: true.",
					"schema":      Schema{"type": "string", "pattern": "^[A-Za-z0-9-]{8,64}$"},
				},
			},
			"securitySchemes": Schema{
				"session": Schema{
					"type": "apiKey", "in": "cookie", "name": "dumpstore_session",
					"description": "Session cookie set by POST /auth/login.",
				},
				"proxyUser": Schema{
					"type": "apiKey", "in": "header", "name": "X-Remote-User",
					"description": "Authenticated user asserted by a reverse proxy; honoured only from the configured trusted proxy CIDRs.",
				},
			},
		},
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (reg *Registry) operation(g *generator, r Route) (Schema, error) {
	op := Schema{
		"operationId":            r.OperationID(),
		"summary":                r.Summary,
		"tags":                   []any{r.Tag()},
		"x-fern-sdk-group-name":  r.Group,
		"x-fern-sdk-method-name": r.Name,
	}
	if r.Description != "" {
		op["description"] = r.Description
	}
	if r.Internal {
		op["x-forge-internal"] = true
	}
	if r.Public {
		op["security"] = []any{}
	}

	var params []any
	if r.Params != nil {
		for _, sf := range reflect.VisibleFields(r.Params) {
			if sf.Anonymous {
				continue
			}
			in, name := "path", sf.Tag.Get("path")
			if name == "" {
				in, name = "query", sf.Tag.Get("query")
			}
			if name == "" {
				continue
			}
			s, err := g.schema(sf.Type)
			if err != nil {
				return nil, fmt.Errorf("param %s: %w", name, err)
			}
			// Path parameters are always required; a query parameter only
			// with required:"true".
			required := in == "path" || sf.Tag.Get("required") == "true"
			p := Schema{"name": name, "in": in, "required": required, "schema": nonNull(annotate(s, sf.Tag))}
			if d := sf.Tag.Get("doc"); d != "" {
				p["description"] = d
			}
			params = append(params, p)
		}
	}
	if r.isWrite() {
		params = append(params, Schema{"$ref": "#/components/parameters/OperationID"})
	}
	if len(params) > 0 {
		op["parameters"] = params
	}

	if r.Request != nil {
		s, err := g.schema(r.Request)
		if err != nil {
			return nil, fmt.Errorf("request: %w", err)
		}
		op["requestBody"] = Schema{
			"required": !r.RequestOptional,
			"content":  Schema{orDefault(r.RequestType, JSON): Schema{"schema": nonNull(s)}},
		}
	}

	status := r.Status
	if status == 0 {
		status = http.StatusOK
	}
	ct := orDefault(r.ResponseType, JSON)
	var body Schema
	switch {
	case r.Response != nil:
		s, err := g.schema(r.Response)
		if err != nil {
			return nil, fmt.Errorf("response: %w", err)
		}
		body = s
	case ct == JSON:
		body = Schema{}
	default:
		body = Schema{"type": "string"}
	}
	ok := Schema{"description": http.StatusText(status)}
	headers := Schema{}
	switch {
	case status == http.StatusNoContent:
	case status >= 300 && status < 400:
		headers["Location"] = Schema{"schema": Schema{"type": "string"}}
	default:
		ok["content"] = Schema{ct: Schema{"schema": body}}
	}
	if r.isWrite() {
		headers[OperationIDHeader] = Schema{
			"description": "ID of this operation.",
			"schema":      Schema{"type": "string"},
		}
	}
	if len(headers) > 0 {
		ok["headers"] = headers
	}
	errRef := Schema{"$ref": "#/components/responses/Error"}
	responses := Schema{fmt.Sprint(status): ok, "default": errRef}
	if !r.Public {
		responses["401"] = errRef
	}
	if r.isWrite() {
		responses["400"] = errRef
		responses["409"] = errRef
	}
	for _, code := range r.Errors {
		if code < 400 || code >= 500 {
			return nil, fmt.Errorf("Errors lists %d; only 4xx statuses are documented explicitly", code)
		}
		responses[fmt.Sprint(code)] = errRef
	}
	op["responses"] = responses
	return op, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
