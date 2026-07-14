package logging

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"

	"dumpstore/internal/api"
)

// newReqID returns a random 16-character hex string for request correlation.
func newReqID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RequestLogger wraps a handler and emits one logfmt line per request.
// It generates a unique req_id, stores it in the request context, and includes
// it on the request log line so downstream slog calls can be correlated.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newReqID()
		}
		r = r.WithContext(api.WithReqID(r.Context(), id))
		w.Header().Set("X-Request-ID", id)

		// When otelhttp wraps this middleware, cross-reference the span with
		// the journald req_id. No-op span otherwise.
		span := trace.SpanFromContext(r.Context())
		if span.IsRecording() {
			span.SetAttributes(attribute.String("req_id", id))
		}

		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		elapsed := time.Since(start)

		// The mux sets r.Pattern in place during dispatch, so the matched route
		// is only known now: rename the span from the generic operation to the
		// low-cardinality "METHOD /route". ServeMux patterns are registered
		// method-qualified ("GET /api/pools"), so strip the method before
		// composing — semconv http.route is the path template alone.
		if span.IsRecording() && r.Pattern != "" {
			route := r.Pattern
			if _, after, ok := strings.Cut(route, " "); ok {
				route = after
			}
			span.SetName(r.Method + " " + route)
			span.SetAttributes(semconv.HTTPRoute(route))
		}

		level := slog.LevelInfo
		if rw.status >= 500 {
			level = slog.LevelError
		} else if rw.status >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(r.Context(), level, "request",
			"req_id", id,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", elapsed.Milliseconds(),
			"remote", r.RemoteAddr,
		)
		api.RecordHTTP(r.Method, r.URL.Path, rw.status, elapsed)
	})
}

// statusRecorder captures the HTTP status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher by forwarding to the underlying ResponseWriter
// if it supports flushing. Required for SSE streaming to work through this middleware.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
