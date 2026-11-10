package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// api('GET', v1`/datasets/${name}/acl`) / op.api(...) — method and template.
	reAPICall = regexp.MustCompile("api\\(\\s*'(GET|POST|PUT|PATCH|DELETE)'\\s*,\\s*v1`([^`]*)`")
	// Any v1`...` template (EventSource, variables, ...).
	reV1      = regexp.MustCompile("\\bv1`([^`]*)`")
	reInterp  = regexp.MustCompile(`\$\{[^}]*\}`)
	reOldPath = regexp.MustCompile(`['"\x60]/api/`)
)

// TestFrontendPaths keeps static/js in sync with the server's routes
// (#181 Phase 3): every v1`…` template must resolve to a registered route —
// with its HTTP method where the call site states one — and no hand-written
// '/api/…' path may remain. The UI has no test suite; this catches stale or
// mistyped paths at `go test` time.
func TestFrontendPaths(t *testing.T) {
	mux := fullRegistry(t).Mux()
	files, _ := filepath.Glob("../../static/js/*.js")
	app, _ := filepath.Glob("../../static/*.js")
	files = append(files, app...)
	if len(files) == 0 {
		t.Fatal("no frontend files found")
	}

	resolve := func(method, tmpl string) string {
		path := reInterp.ReplaceAllString(tmpl, "x%2Fy")
		path, _, _ = strings.Cut(path, "?")
		_, pattern := mux.Handler(httptest.NewRequest(method, "/api/v1"+path, nil))
		return pattern
	}

	total := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		name := filepath.Base(f)

		for i, line := range strings.Split(src, "\n") {
			// The v1 helper's own bare prefix ('/api/v1') is not a path.
			if reOldPath.MatchString(line) && !strings.Contains(line, "/api/v1/openapi.json") &&
				!strings.Contains(line, "'/api/v1'") &&
				!strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("%s:%d: hand-written API path — use v1`…`: %s", name, i+1, strings.TrimSpace(line))
			}
		}

		checked := map[int]bool{}
		for _, m := range reAPICall.FindAllStringSubmatchIndex(src, -1) {
			method, tmpl := src[m[2]:m[3]], src[m[4]:m[5]]
			checked[m[4]] = true
			total++
			if p := resolve(method, tmpl); !strings.HasPrefix(p, method+" /api/v1/") {
				t.Errorf("%s: %s v1`%s` does not match a route (got %q)", name, method, tmpl, p)
			}
		}
		for _, m := range reV1.FindAllStringSubmatchIndex(src, -1) {
			if checked[m[2]] {
				continue
			}
			tmpl := src[m[2]:m[3]]
			total++
			ok := false
			for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
				if strings.HasPrefix(resolve(method, tmpl), method+" /api/v1/") {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%s: v1`%s` does not match any route", name, tmpl)
			}
		}
	}
	if total < 100 {
		t.Errorf("only %d v1`…` paths found in static/ — is the frontend migrated?", total)
	}
}
