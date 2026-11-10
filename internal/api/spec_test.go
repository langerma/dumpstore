package api

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"dumpstore/internal/apispec"
	"dumpstore/internal/auth"
)

var update = flag.Bool("update", false, "rewrite api/openapi.json (make openapi)")

const specFile = "../../api/openapi.json"

// fullRegistry registers every route main registers (API + auth) on a
// fresh mux. Handlers are never called except where a test says so.
func fullRegistry(t *testing.T) *apispec.Registry {
	t.Helper()
	reg := NewRegistry(http.NewServeMux())
	auth.RegisterRoutes(reg, &auth.Config{}, auth.NewSessionStore(time.Hour), auth.NewRateLimiter())
	(&Handler{}).RegisterRoutes(reg)
	return reg
}

// TestOpenAPISpec keeps the committed api/openapi.json identical to what the
// registry generates. Regenerate with `make openapi`.
func TestOpenAPISpec(t *testing.T) {
	got, err := fullRegistry(t).Spec()
	if err != nil {
		t.Fatalf("Spec: %v", err)
	}
	if *update {
		if err := os.WriteFile(specFile, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(specFile)
	if err != nil {
		t.Fatalf("%v — run `make openapi`", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("api/openapi.json is stale — run `make openapi` and commit the result")
	}
}

// untypedResponses are JSON routes whose success body has no schema yet
// (map[string]any responses). Phase 2 of #181 burns this list down to zero;
// a route may only leave it, never join it.
var untypedResponses = []string{
	"DELETE /api/acl/{dataset...}",
	"DELETE /api/datasets/{name...}",
	"DELETE /api/groups/{name}",
	"DELETE /api/iscsi-targets",
	"DELETE /api/pools/{pool}/devices/{device...}",
	"DELETE /api/scrub-schedule/{pool}",
	"DELETE /api/scrub/{pool}",
	"DELETE /api/smb-share/{dataset...}",
	"DELETE /api/smb-users/{name}",
	"DELETE /api/smb/homes",
	"DELETE /api/smb/timemachine/{sharename}",
	"DELETE /api/snapshots/{snapshot...}",
	"DELETE /api/users/{name}",
	"DELETE /api/users/{name}/sshkeys",
	"GET /api/chown/{dataset...}",
	"GET /api/iscsi/status",
	"GET /api/schema",
	"GET /api/smb-users",
	"GET /api/smb/homes",
	"GET /api/smb/status",
	"GET /api/snapshots/diff",
	"GET /api/users/{name}/sshkeys",
	"GET /api/userspace/{name...}",
	"GET /api/version",
	"PATCH /api/datasets/{name...}",
	"PATCH /api/tls/config",
	"POST /api/acl/{dataset...}",
	"POST /api/auth/change-password",
	"POST /api/auth/change-username",
	"POST /api/auto-snapshot/release",
	"POST /api/auto-snapshot/takeover",
	"POST /api/chown/{dataset...}",
	"POST /api/datasets",
	"POST /api/datasets/rename",
	"POST /api/groups",
	"POST /api/iscsi-targets",
	"POST /api/iscsi/init",
	"POST /api/pools",
	"POST /api/pools/import",
	"POST /api/pools/{pool}/cache",
	"POST /api/pools/{pool}/export",
	"POST /api/pools/{pool}/log",
	"POST /api/pools/{pool}/offline",
	"POST /api/pools/{pool}/online",
	"POST /api/pools/{pool}/replace",
	"POST /api/pools/{pool}/spare",
	"POST /api/pools/{pool}/vdevs",
	"POST /api/replication/{id}/run",
	"POST /api/rewrite/{name...}",
	"POST /api/scrub/{pool}",
	"POST /api/services/{service}/{action}",
	"POST /api/smb-share/{dataset...}",
	"POST /api/smb-users/{name}",
	"POST /api/smb/homes",
	"POST /api/smb/init",
	"POST /api/smb/timemachine",
	"POST /api/smb/wsdd",
	"POST /api/snapshots",
	"POST /api/snapshots/clone",
	"POST /api/snapshots/delete-batch",
	"POST /api/snapshots/send",
	"POST /api/tls/acme/issue",
	"POST /api/tls/acme/renew",
	"POST /api/tls/gencert",
	"POST /api/userquota/{name...}",
	"POST /api/users",
	"POST /api/users/{name}/sshkeys",
	"PUT /api/auto-snapshot/{name...}",
	"PUT /api/groups/{name}",
	"PUT /api/scrub-schedule/{pool}",
	"PUT /api/users/{name}",
}

// untypedBodies are routes whose request body is still an anonymous struct,
// documented as a generic JSON object. Phase 2 of #181 burns this down.
var untypedBodies = []string{
	"DELETE /api/users/{name}/sshkeys",
	"PATCH /api/tls/config",
	"POST /api/acl/{dataset...}",
	"POST /api/auth/change-password",
	"POST /api/auth/change-username",
	"POST /api/chown/{dataset...}",
	"POST /api/datasets",
	"POST /api/datasets/rename",
	"POST /api/groups",
	"POST /api/iscsi-targets",
	"POST /api/pools",
	"POST /api/pools/import",
	"POST /api/pools/{pool}/cache",
	"POST /api/pools/{pool}/log",
	"POST /api/pools/{pool}/offline",
	"POST /api/pools/{pool}/online",
	"POST /api/pools/{pool}/replace",
	"POST /api/pools/{pool}/spare",
	"POST /api/pools/{pool}/vdevs",
	"POST /api/rewrite/{name...}",
	"POST /api/smb-share/{dataset...}",
	"POST /api/smb-users/{name}",
	"POST /api/smb/homes",
	"POST /api/smb/timemachine",
	"POST /api/snapshots",
	"POST /api/snapshots/clone",
	"POST /api/snapshots/delete-batch",
	"POST /api/snapshots/send",
	"POST /api/tls/acme/issue",
	"POST /api/tls/gencert",
	"POST /api/userquota/{name...}",
	"POST /api/users",
	"POST /api/users/{name}/sshkeys",
	"PUT /api/groups/{name}",
	"PUT /api/scrub-schedule/{pool}",
	"PUT /api/users/{name}",
}

// TestRouteTypingBurnDown pins exactly which routes still lack typed
// schemas, so typing can only improve.
func TestRouteTypingBurnDown(t *testing.T) {
	var gotResp, gotBody []string
	for _, r := range fullRegistry(t).Routes() {
		key := r.Method + " " + r.Path
		if r.Untyped() {
			gotResp = append(gotResp, key)
		}
		if r.Request == reflect.TypeFor[jsonObject]() {
			gotBody = append(gotBody, key)
		}
	}
	slices.Sort(gotResp)
	slices.Sort(gotBody)
	check := func(what string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s changed; update the list in spec_test.go (only removals are expected):\n%s",
				what, "\t\""+strings.Join(got, "\",\n\t\"")+"\",")
		}
	}
	check("untypedResponses", gotResp, untypedResponses)
	check("untypedBodies", gotBody, untypedBodies)
}

// TestSpecPathsResolve serves every route's pattern and requests its
// OpenAPI path with percent-encoded sample values: each must reach the same
// pattern and decode the parameters intact. Wildcard ({x...}) parameters
// get a value with '/' and '@', like dataset and snapshot names.
func TestSpecPathsResolve(t *testing.T) {
	for _, r := range fullRegistry(t).Routes() {
		t.Run(r.Method+" "+r.Path, func(t *testing.T) {
			pattern := r.Method + " " + r.Path
			want := map[string]string{}
			path := r.OpenAPIPath()
			for _, seg := range strings.Split(r.Path, "/") {
				if !strings.HasPrefix(seg, "{") {
					continue
				}
				name := strings.TrimSuffix(strings.Trim(seg, "{}"), "...")
				val := "tank"
				if strings.HasSuffix(seg, "...}") {
					val = "tank/a b/c@snap-1"
				}
				want[name] = val
				path = strings.Replace(path, "{"+name+"}", url.PathEscape(val), 1)
			}

			got := map[string]string{}
			var matched string
			mux := http.NewServeMux()
			for _, other := range fullRegistry(t).Routes() {
				p := other.Method + " " + other.Path
				mux.HandleFunc(p, func(_ http.ResponseWriter, req *http.Request) {
					matched = p
					for name := range want {
						got[name] = req.PathValue(name)
					}
				})
			}
			mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(r.Method, path, nil))
			if matched != pattern {
				t.Fatalf("%s %s matched %q, want %q", r.Method, path, matched, pattern)
			}
			for name, v := range want {
				if got[name] != v {
					t.Errorf("{%s} = %q, want %q", name, got[name], v)
				}
			}
		})
	}
}

// ---- typed adapter ----

type adapterReq struct {
	Name  string `path:"name"`
	Kind  string `query:"kind"`
	After uint   `query:"after"`
	Force bool   `query:"force"`
	N     int    `query:"n"`
	Body  struct {
		Label string `json:"label"`
	} `body:""`
}

func serveTyped[Req, Resp any](fn func(r Req) (Resp, error), pattern, method, target, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	mux.Handle(pattern, typed(func(_ context.Context, r Req) (Resp, error) { return fn(r) }))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func TestTypedAdapterBinds(t *testing.T) {
	var got adapterReq
	rec := serveTyped(func(r adapterReq) (map[string]string, error) {
		got = r
		return map[string]string{"ok": "yes"}, nil
	}, "POST /x/{name...}", "POST", "/x/tank%2Fdata?kind=group&after=3&force=true&n=-2", `{"label":"L"}`)

	if rec.Code != 200 || rec.Body.String() != "{\"ok\":\"yes\"}\n" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	if got.Name != "tank/data" || got.Kind != "group" || got.After != 3 || !got.Force || got.N != -2 || got.Body.Label != "L" {
		t.Errorf("bound %+v", got)
	}
}

func TestTypedAdapterErrors(t *testing.T) {
	type q struct {
		After uint `query:"after"`
		Force bool `query:"force"`
		N     int  `query:"n"`
	}
	ok := func(q) (struct{}, error) { return struct{}{}, nil }
	for target, wantBody := range map[string]string{
		"/x?after=-1":  `{"error":"after must be a non-negative integer"}`,
		"/x?force=yes": `{"error":"force must be true or false"}`,
		"/x?n=1.5":     `{"error":"n must be an integer"}`,
	} {
		rec := serveTyped(ok, "GET /x", "GET", target, "")
		if rec.Code != 400 || rec.Body.String() != wantBody+"\n" {
			t.Errorf("%s: %d %q", target, rec.Code, rec.Body.String())
		}
	}

	type b struct {
		Body struct {
			A int `json:"a"`
		} `body:""`
	}
	rec := serveTyped(func(b) (struct{}, error) { return struct{}{}, nil }, "POST /x", "POST", "/x", `{"a":1}{}`)
	if rec.Code != 400 || !strings.HasPrefix(rec.Body.String(), `{"error":"invalid request body: `) {
		t.Errorf("bad body: %d %q", rec.Code, rec.Body.String())
	}

	rec = serveTyped(func(struct{}) (struct{}, error) {
		return struct{}{}, errStatus(http.StatusNotFound, errors.New("gone"))
	}, "GET /x", "GET", "/x", "")
	if rec.Code != 404 || rec.Body.String() != "{\"error\":\"gone\"}\n" {
		t.Errorf("httpError: %d %q", rec.Code, rec.Body.String())
	}

	rec = serveTyped(func(struct{}) (struct{}, error) {
		return struct{}{}, fmt.Errorf("wrapped: %w", errStatus(http.StatusConflict, errors.New("busy")))
	}, "GET /x", "GET", "/x", "")
	if rec.Code != 409 || rec.Body.String() != "{\"error\":\"busy\"}\n" {
		t.Errorf("wrapped httpError: %d %q", rec.Code, rec.Body.String())
	}

	rec = serveTyped(func(struct{}) (struct{}, error) {
		return struct{}{}, errors.New("boom")
	}, "GET /x", "GET", "/x", "")
	if rec.Code != 500 || rec.Body.String() != "{\"error\":\"boom\"}\n" {
		t.Errorf("plain error: %d %q", rec.Code, rec.Body.String())
	}
}

// TestConvertedHandlerParity spot-checks that handlers moved onto the typed
// adapter answer exactly as before on their validation paths.
func TestConvertedHandlerParity(t *testing.T) {
	reg := fullRegistry(t)
	for _, c := range []struct{ target, body string }{
		{"/api/dataset-props/bad%20name", `{"error":"invalid dataset name"}`},
		{"/api/acl/bad%20name", `{"error":"invalid dataset name"}`},
		{"/api/auto-snapshot/bad%20name", `{"error":"invalid dataset name"}`},
	} {
		rec := httptest.NewRecorder()
		reg.Mux().ServeHTTP(rec, httptest.NewRequest("GET", c.target, nil))
		if rec.Code != 400 || rec.Body.String() != c.body+"\n" {
			t.Errorf("%s: %d %q", c.target, rec.Code, rec.Body.String())
		}
	}
}
