package apispec

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

type inner struct {
	Deep string `json:"deep"`
}

type Embedded struct {
	Promoted string `json:"promoted"`
	Shadowed string `json:"shadowed"`
}

type node struct {
	Name     string  `json:"name"`
	Children []*node `json:"children,omitempty"`
}

type marshaled struct{ v int }

func (marshaled) MarshalJSON() ([]byte, error) { return []byte(`"x"`), nil }

type described struct{ v int }

func (described) MarshalJSON() ([]byte, error) { return []byte(`"x"`), nil }
func (described) OpenAPISchema() Schema        { return Schema{"type": "string", "format": "duration"} }

type renamed struct {
	A int `json:"a"`
}

type kind string

type everything struct {
	Embedded
	Shadowed int                `json:"shadowed"`
	Str      string             `json:"str" doc:"a string" enum:"x,y"`
	Opt      string             `json:"opt,omitempty"`
	Untagged int                // field name as key
	Skip     string             `json:"-"`
	private  string             //nolint:unused
	Ptr      *int               `json:"ptr"`
	PtrOpt   *int               `json:"ptr_opt,omitempty"`
	Slice    []string           `json:"slice"`
	Bytes    []byte             `json:"bytes"`
	Array    [2]int             `json:"array"`
	Map      map[kind]bool      `json:"map"`
	When     time.Time          `json:"when"`
	Raw      json.RawMessage    `json:"raw"`
	Anything any                `json:"anything"`
	AsString int                `json:"as_string,string"`
	Ref      inner              `json:"ref"`
	RefPtr   *inner             `json:"ref_ptr"`
	Anon     struct{ X bool }   `json:"anon"`
	Dur      described          `json:"dur"`
	Renamed  renamed            `json:"renamed"`
	Tree     node               `json:"tree"`
	Floats   map[string]float64 `json:"floats,omitempty"`
	Count    uint               `json:"count"`
	WhenPtr  *time.Time         `json:"when_ptr,omitempty"`
}

func gen(t *testing.T, typ reflect.Type) (Schema, *generator) {
	t.Helper()
	g := newGenerator(map[reflect.Type]string{reflect.TypeFor[renamed](): "Custom"})
	s, err := g.schema(typ)
	if err != nil {
		t.Fatalf("schema(%s): %v", typ, err)
	}
	return s, g
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestSchemaReflection(t *testing.T) {
	s, g := gen(t, reflect.TypeFor[everything]())
	if got := jsonOf(s); got != `{"$ref":"#/components/schemas/Everything"}` {
		t.Fatalf("top-level ref = %s", got)
	}
	obj := g.components["Everything"]
	props := obj["properties"].(Schema)

	want := map[string]string{
		"promoted":  `{"type":"string"}`,
		"shadowed":  `{"type":"integer"}`, // outer field wins over the embedded one
		"str":       `{"description":"a string","enum":["x","y"],"type":"string"}`,
		"opt":       `{"type":"string"}`,
		"Untagged":  `{"type":"integer"}`,
		"ptr":       `{"type":["integer","null"]}`,
		"ptr_opt":   `{"type":"integer"}`,
		"slice":     `{"items":{"type":"string"},"type":["array","null"]}`,
		"bytes":     `{"contentEncoding":"base64","type":["string","null"]}`,
		"array":     `{"items":{"type":"integer"},"maxItems":2,"minItems":2,"type":"array"}`,
		"map":       `{"additionalProperties":{"type":"boolean"},"type":["object","null"]}`,
		"when":      `{"format":"date-time","type":"string"}`,
		"raw":       `{}`,
		"anything":  `{}`,
		"as_string": `{"type":"string"}`,
		"ref":       `{"$ref":"#/components/schemas/Inner"}`,
		"ref_ptr":   `{"anyOf":[{"$ref":"#/components/schemas/Inner"},{"type":"null"}]}`,
		"anon":      `{"properties":{"X":{"type":"boolean"}},"required":["X"],"type":"object"}`,
		"dur":       `{"format":"duration","type":"string"}`,
		"renamed":   `{"$ref":"#/components/schemas/Custom"}`,
		"tree":      `{"$ref":"#/components/schemas/Node"}`,
		"floats":    `{"additionalProperties":{"type":"number"},"type":"object"}`,
		"count":     `{"minimum":0,"type":"integer"}`,
		"when_ptr":  `{"format":"date-time","type":"string"}`,
	}
	for name, w := range want {
		if got := jsonOf(props[name]); got != w {
			t.Errorf("%s = %s, want %s", name, got, w)
		}
	}
	if len(props) != len(want) {
		var names []string
		for k := range props {
			names = append(names, k)
		}
		t.Errorf("properties = %v, want exactly %d (Skip, private excluded)", names, len(want))
	}

	req := map[string]bool{}
	for _, r := range obj["required"].([]string) {
		req[r] = true
	}
	for _, name := range []string{"opt", "ptr_opt", "floats"} {
		if req[name] {
			t.Errorf("omitempty field %s must not be required", name)
		}
	}
	for _, name := range []string{"str", "ptr", "slice", "Untagged", "promoted"} {
		if !req[name] {
			t.Errorf("field %s must be required", name)
		}
	}

	// Recursive type terminates and refers to itself.
	if got := jsonOf(g.components["Node"]["properties"].(Schema)["children"]); got != `{"items":{"anyOf":[{"$ref":"#/components/schemas/Node"},{"type":"null"}]},"type":"array"}` {
		t.Errorf("recursive children = %s", got)
	}
}

func TestSchemaErrors(t *testing.T) {
	if _, err := newGenerator(nil).schema(reflect.TypeFor[marshaled]()); err == nil ||
		!strings.Contains(err.Error(), "Schemaer") {
		t.Errorf("Marshaler without Schemaer: err = %v", err)
	}
	type wrap struct {
		M marshaled `json:"m"`
	}
	if _, err := newGenerator(nil).schema(reflect.TypeFor[wrap]()); err == nil {
		t.Error("nested Marshaler without Schemaer must fail")
	}

	// Two distinct types with the same component name.
	g := newGenerator(nil)
	if _, err := g.schema(reflect.TypeFor[inner]()); err != nil {
		t.Fatal(err)
	}
	type Inner struct{ Y int }
	if _, err := g.schema(reflect.TypeFor[Inner]()); err == nil || !strings.Contains(err.Error(), "Registry.Names") {
		t.Errorf("name collision: err = %v", err)
	}
}

func TestRouteDerivations(t *testing.T) {
	r := Route{Method: "GET", Path: "/api/acl/{dataset...}", Group: "datasets.acl", Name: "get"}
	if got := r.OperationID(); got != "datasetsAclGet" {
		t.Errorf("OperationID = %q", got)
	}
	if got := r.Tag(); got != "datasets" {
		t.Errorf("Tag = %q", got)
	}
	if got := r.OpenAPIPath(); got != "/api/acl/{dataset}" {
		t.Errorf("OpenAPIPath = %q", got)
	}
}

func TestHandleValidation(t *testing.T) {
	h := http.NotFoundHandler()
	type nameParam struct {
		Name string `path:"name"`
	}
	ok := Route{Method: "GET", Path: "/api/x/{name...}", Group: "x", Name: "get", Summary: "s",
		Params: reflect.TypeFor[nameParam](), Handler: h}

	cases := map[string]func(r *Route){
		"missing group":     func(r *Route) { r.Group = "" },
		"missing summary":   func(r *Route) { r.Summary = "" },
		"missing handler":   func(r *Route) { r.Handler = nil },
		"undeclared param":  func(r *Route) { r.Params = nil },
		"extra declared":    func(r *Route) { r.Path = "/api/x" },
		"non-struct params": func(r *Route) { r.Params = reflect.TypeFor[string]() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := ok
			mutate(&r)
			defer func() {
				if recover() == nil {
					t.Error("Handle did not panic")
				}
			}()
			New(http.NewServeMux()).Handle(r)
		})
	}

	reg := New(http.NewServeMux())
	reg.Handle(ok)
	dup := ok
	dup.Method = "DELETE"
	defer func() {
		if recover() == nil {
			t.Error("duplicate group+name did not panic")
		}
	}()
	reg.Handle(dup)
}

func TestSpecDocument(t *testing.T) {
	type params struct {
		Name  string `path:"name" doc:"dataset name"`
		After int    `query:"after"`
		Must  string `query:"must" required:"true"`
	}
	type body struct {
		Label string `json:"label"`
	}
	type errBody struct {
		Error string `json:"error"`
	}
	reg := New(http.NewServeMux())
	reg.Error = reflect.TypeFor[errBody]()
	reg.Tags = map[string]string{"things": "Things", "metrics": "Metrics"}
	h := http.NotFoundHandler()
	reg.Handle(Route{Method: "GET", Path: "/api/things/{name...}", Group: "things", Name: "get", Summary: "Get",
		Params: reflect.TypeFor[params](), Response: reflect.TypeFor[[]inner](), Handler: h})
	reg.Handle(Route{Method: "POST", Path: "/api/things", Group: "things", Name: "create", Summary: "Create",
		Request: reflect.TypeFor[body](), Status: 202, Handler: h})
	reg.Handle(Route{Method: "GET", Path: "/metrics", Group: "metrics", Name: "scrape", Summary: "Scrape",
		ResponseType: Text, Public: true, Internal: true, Handler: h})

	raw, err := reg.Spec()
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := reg.Spec(); string(again) != string(raw) {
		t.Error("Spec() is not deterministic")
	}
	delete(reg.Tags, "metrics")
	if _, err := reg.Spec(); err == nil || !strings.Contains(err.Error(), `tag "metrics"`) {
		t.Errorf("undescribed tag: err = %v", err)
	}
	reg.Tags["metrics"] = "Metrics"
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	paths := doc["paths"].(map[string]any)

	get := paths["/api/things/{name}"].(map[string]any)["get"].(map[string]any)
	if get["operationId"] != "thingsGet" || get["x-fern-sdk-group-name"] != "things" || get["x-fern-sdk-method-name"] != "get" ||
		get["x-forge-group-name"] != "things" || get["x-forge-method-name"] != "get" {
		t.Errorf("get metadata: %v", get)
	}
	if got := jsonOf(get["parameters"]); got != `[{"description":"dataset name","in":"path","name":"name","required":true,"schema":{"description":"dataset name","type":"string"}},{"in":"query","name":"after","required":false,"schema":{"type":"integer"}},{"in":"query","name":"must","required":true,"schema":{"type":"string"}}]` {
		t.Errorf("parameters = %s", got)
	}
	resp := get["responses"].(map[string]any)
	if got := jsonOf(resp["200"].(map[string]any)["content"]); got != `{"application/json":{"schema":{"items":{"$ref":"#/components/schemas/Inner"},"type":["array","null"]}}}` {
		t.Errorf("200 content = %s", got)
	}
	if _, ok := resp["409"]; ok {
		t.Error("GET must not document write-only 409")
	}

	post := paths["/api/things"].(map[string]any)["post"].(map[string]any)
	pr := post["responses"].(map[string]any)
	for _, code := range []string{"202", "400", "401", "409", "default"} {
		if _, ok := pr[code]; !ok {
			t.Errorf("POST missing response %s", code)
		}
	}
	if got := jsonOf(post["parameters"]); got != `[{"$ref":"#/components/parameters/OperationID"}]` {
		t.Errorf("POST parameters = %s", got)
	}
	if _, ok := pr["202"].(map[string]any)["headers"].(map[string]any)[OperationIDHeader]; !ok {
		t.Error("write success response must document the operation ID header")
	}
	if got := jsonOf(post["requestBody"]); got != `{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Body"}}},"required":true}` {
		t.Errorf("requestBody = %s", got)
	}
	if got := jsonOf(doc["components"].(map[string]any)["schemas"].(map[string]any)["Body"]); got != `{"properties":{"label":{"type":"string"}},"required":["label"],"type":"object"}` {
		t.Errorf("Body component = %s", got)
	}

	m := paths["/metrics"].(map[string]any)["get"].(map[string]any)
	if got := jsonOf(m["security"]); got != `[]` {
		t.Errorf("public security = %s", got)
	}
	if m["x-forge-skip"] != true || m["x-fern-ignore"] != true {
		t.Error("internal flag missing")
	}
	if _, ok := m["responses"].(map[string]any)["401"]; ok {
		t.Error("public route must not document 401")
	}
	if got := jsonOf(m["responses"].(map[string]any)["200"].(map[string]any)["content"]); got != `{"text/plain":{"schema":{"type":"string"}}}` {
		t.Errorf("text content = %s", got)
	}
	errSchema := doc["components"].(map[string]any)["responses"].(map[string]any)["Error"]
	if !strings.Contains(jsonOf(errSchema), `#/components/schemas/ErrBody`) {
		t.Errorf("error response = %s", jsonOf(errSchema))
	}
}

type homesShare struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only"`
}

type homes struct {
	Enabled bool `json:"enabled"`
	*homesShare
}

func TestEmbeddedPointerFieldsOptional(t *testing.T) {
	_, g := gen(t, reflect.TypeFor[homes]())
	obj := g.components["Homes"]
	if got := jsonOf(obj["required"]); got != `["enabled"]` {
		t.Errorf("required = %s; fields of a nil-able embedded pointer must be optional", got)
	}
	if got := jsonOf(obj["properties"]); got != `{"enabled":{"type":"boolean"},"path":{"type":"string"},"read_only":{"type":"boolean"}}` {
		t.Errorf("properties = %s", got)
	}
}

func TestRouteErrors(t *testing.T) {
	reg := New(http.NewServeMux())
	reg.Tags = map[string]string{"x": "X"}
	reg.Handle(Route{Method: "GET", Path: "/api/x", Group: "x", Name: "get", Summary: "s",
		Errors: []int{404, 409}, Handler: http.NotFoundHandler()})
	raw, err := reg.Spec()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	resp := doc["paths"].(map[string]any)["/api/x"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)
	for _, code := range []string{"404", "409"} {
		if got := jsonOf(resp[code]); got != `{"$ref":"#/components/responses/Error"}` {
			t.Errorf("%s = %s", code, got)
		}
	}

	bad := New(http.NewServeMux())
	bad.Tags = reg.Tags
	bad.Handle(Route{Method: "GET", Path: "/api/x", Group: "x", Name: "get", Summary: "s",
		Errors: []int{500}, Handler: http.NotFoundHandler()})
	if _, err := bad.Spec(); err == nil {
		t.Error("Errors with a non-4xx status must fail")
	}
}
