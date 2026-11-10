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
	"strings"
	"testing"
	"time"

	"dumpstore/internal/ansible"
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

// TestNoUntypedRoutes: every JSON route documents named request and
// response types (#181 Phase 2). Only the spec document itself is a
// generic object.
func TestNoUntypedRoutes(t *testing.T) {
	for _, r := range fullRegistry(t).Routes() {
		key := r.Method + " " + r.Path
		if r.Untyped() {
			t.Errorf("%s has no response type — register it with handle() and a typed handler", key)
		}
		generic := func(t reflect.Type) bool {
			return t != nil && t.Kind() == reflect.Map && t.Elem().Kind() == reflect.Interface
		}
		if generic(r.Request) {
			t.Errorf("%s documents its request body as a generic object — use a named request type", key)
		}
		if generic(r.Response) && key != "GET /api/v1/openapi.json" {
			t.Errorf("%s documents its response as a generic object — use a named response type", key)
		}
	}
}

// TestSpecPathsResolve serves every route's pattern and requests its
// OpenAPI path with percent-encoded sample values: each must reach the same
// pattern and decode the parameters intact. Every parameter gets a value
// with '/', '@' and a space, like dataset and snapshot names.
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
				// Every parameter is one segment (#181 Phase 3): names with
				// '/', '@' and spaces must arrive intact when percent-encoded.
				val := "tank/a b/c@snap-1"
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
	mux.Handle(pattern, typed(func(_ context.Context, r Req) (Resp, error) { return fn(r) }, 0))
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
		{"/api/v1/datasets/bad%20name/properties", `{"error":"invalid dataset name"}`},
		{"/api/v1/datasets/bad%20name/acl", `{"error":"invalid dataset name"}`},
		{"/api/v1/datasets/bad%20name/auto-snapshot", `{"error":"invalid dataset name"}`},
	} {
		rec := httptest.NewRecorder()
		reg.Mux().ServeHTTP(rec, httptest.NewRequest("GET", c.target, nil))
		if rec.Code != 400 || rec.Body.String() != c.body+"\n" {
			t.Errorf("%s: %d %q", c.target, rec.Code, rec.Body.String())
		}
	}
}

func TestTypedAdapterStatusAndOpErrors(t *testing.T) {
	serve := func(status int, fn func(context.Context, struct{}) (map[string]int, error)) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		mux.Handle("POST /x", withRemoteAddr(typed(fn, status)))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", nil)
		req.RemoteAddr = "192.0.2.7:4242"
		mux.ServeHTTP(rec, req)
		return rec
	}
	ok := func(context.Context, struct{}) (map[string]int, error) { return map[string]int{"a": 1}, nil }

	for _, status := range []int{http.StatusCreated, http.StatusAccepted} {
		rec := serve(status, ok)
		if rec.Code != status || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != "{\"a\":1}\n" {
			t.Errorf("status %d: %d %q %q", status, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
	}
	if rec := serve(http.StatusNoContent, ok); rec.Code != 204 || rec.Body.Len() != 0 {
		t.Errorf("204: %d %q", rec.Code, rec.Body.String())
	}

	steps := []ansible.TaskStep{{Name: "zfs destroy", Status: "failed", Msg: "busy"}}
	rec := serve(0, func(context.Context, struct{}) (map[string]int, error) {
		return nil, opError(errors.New("1 of 1 step(s) failed"), steps)
	})
	if rec.Code != 500 || rec.Body.String() != `{"error":"1 of 1 step(s) failed","tasks":[{"name":"zfs destroy","status":"failed","msg":"busy"}]}`+"\n" {
		t.Errorf("opError: %d %q", rec.Code, rec.Body.String())
	}
	if rec := serve(0, func(context.Context, struct{}) (map[string]int, error) {
		return nil, playbookError(errors.New("no output"), nil)
	}); rec.Code != 500 || rec.Body.String() != "{\"error\":\"no output\"}\n" {
		t.Errorf("playbookError(nil): %d %q", rec.Code, rec.Body.String())
	}

	var addr string
	serve(0, func(ctx context.Context, _ struct{}) (map[string]int, error) {
		addr = remoteAddr(ctx)
		return nil, nil
	})
	if addr != "192.0.2.7:4242" {
		t.Errorf("remote addr in context = %q", addr)
	}
}

// TestPreV1PathsGone: the /api/v1 cut is a hard cut (#181 Phase 3) — the
// old paths must not resolve to any route.
func TestPreV1PathsGone(t *testing.T) {
	mux := fullRegistry(t).Mux()
	for _, p := range []string{
		"GET /api/pools", "GET /api/datasets", "GET /api/sysinfo", "GET /api/poolstatus",
		"GET /api/dataset-props/tank", "POST /api/datasets/rename", "GET /api/smb-shares",
		"DELETE /api/iscsi-targets", "GET /api/whoami", "GET /api/events",
	} {
		method, path, _ := strings.Cut(p, " ")
		if _, pattern := mux.Handler(httptest.NewRequest(method, path, nil)); pattern != "" {
			t.Errorf("%s still resolves to %q", p, pattern)
		}
	}
}
