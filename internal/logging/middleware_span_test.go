package logging

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestRequestSpanNamedAfterRoute asserts the middleware chain from main.go
// (otelhttp → RequestLogger → mux) produces spans named "METHOD /pattern",
// not the bare method name.
func TestRequestSpanNamedAfterRoute(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/pools", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/datasets/{name...}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := otelhttp.NewHandler(RequestLogger(mux), "dumpstore",
		otelhttp.WithTracerProvider(tp))

	for _, path := range []string{"/api/pools", "/api/datasets/tank/data"} {
		req := httptest.NewRequest("GET", path, nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(spans))
	}
	want := []string{"GET /api/pools", "GET /api/datasets/{name...}"}
	for i, s := range spans {
		if s.Name() != want[i] {
			t.Errorf("span %d: name %q, want %q", i, s.Name(), want[i])
		}
	}
}

// TestShortCircuitSpanNames covers requests that never reach a mux route
// (#140): an auth middleware 401 and an unmatched path must get fixed,
// low-cardinality names — never the raw URL path.
func TestShortCircuitSpanNames(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/pools", func(w http.ResponseWriter, r *http.Request) {})
	// Stand-in for auth.Middleware: rejects /api/secret before the mux runs.
	authGate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})

	handler := otelhttp.NewHandler(RequestLogger(authGate), "dumpstore",
		otelhttp.WithTracerProvider(tp))

	for _, path := range []string{"/api/secret", "/api/nope/tank"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(spans))
	}
	want := []struct{ name, reason string }{
		{"GET (unauthenticated)", "auth"},
		{"GET (unmatched)", "unmatched"},
	}
	for i, s := range spans {
		if s.Name() != want[i].name {
			t.Errorf("span %d: name %q, want %q", i, s.Name(), want[i].name)
		}
		var got string
		for _, kv := range s.Attributes() {
			if kv.Key == "dumpstore.short_circuit" {
				got = kv.Value.AsString()
			}
		}
		if got != want[i].reason {
			t.Errorf("span %d: dumpstore.short_circuit %q, want %q", i, got, want[i].reason)
		}
	}
}
