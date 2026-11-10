package logging

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricRoute(t *testing.T) {
	mux := http.NewServeMux()
	var got string
	record := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	mux.HandleFunc("GET /api/v1/datasets/{name}", record)
	mux.HandleFunc("GET /metrics", record)
	mux.Handle("/", http.HandlerFunc(record))
	for _, c := range []struct {
		path   string
		status int
		want   string
	}{
		{"/api/v1/datasets/tank%2Fdata", 200, "/api/v1/datasets/{name}"},
		{"/metrics", 200, "/metrics"},
		{"/app.js", 200, ""},
	} {
		r := httptest.NewRequest("GET", c.path, nil)
		mux.ServeHTTP(httptest.NewRecorder(), r)
		if got = metricRoute(r, c.status); got != c.want {
			t.Errorf("%s: metricRoute = %q, want %q", c.path, got, c.want)
		}
	}
	// Requests that never reached the mux (auth middleware, no route).
	for status, want := range map[int]string{401: "(unauthenticated)", 403: "(unauthenticated)", 404: "(unmatched)"} {
		if got := metricRoute(httptest.NewRequest("GET", "/api/v1/nope/x", nil), status); got != want {
			t.Errorf("unmatched %d: %q, want %q", status, got, want)
		}
	}
	if got := metricRoute(httptest.NewRequest("GET", "/favicon.ico", nil), 404); got != "" {
		t.Errorf("unmatched static: %q, want \"\"", got)
	}
}
