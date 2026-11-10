package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dumpstore/internal/api"
)

// TestTraceRequest pins which requests get a server span. The SSE stream
// must be excluded at its real route — the filter once kept matching the
// pre-/api/v1 path after the move (#181).
func TestTraceRequest(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/events?topics=pool.query": false,
		"/api/v1/datasets":                 true,
		"/api/v1/datasets/tank%2Fdata/acl": true,
		"/auth/login":                      true,
		"/login":                           false,
		"/metrics":                         false,
		"/app.js":                          false,
	} {
		if got := traceRequest(httptest.NewRequest("GET", path, nil)); got != want {
			t.Errorf("traceRequest(%s) = %v, want %v", path, got, want)
		}
	}

	// The excluded path must be the SSE route the server actually registers.
	mux := http.NewServeMux()
	(&api.Handler{}).RegisterRoutes(api.NewRegistry(mux))
	if _, pattern := mux.Handler(httptest.NewRequest("GET", "/api/v1/events", nil)); pattern != "GET /api/v1/events" {
		t.Errorf("SSE route is %q; update traceRequest", pattern)
	}
}
