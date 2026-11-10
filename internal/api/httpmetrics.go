package api

import (
	"context"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meter resolves through the global provider; instruments created before
// internal/otel.Init installs the SDK are delegated once it does.
var meter = otel.Meter("dumpstore")

// Exposed on /metrics as http_requests_total and http_request_duration_seconds
// (the Prometheus exporter appends the _total and unit suffixes).
var (
	httpRequests, _ = meter.Int64Counter("http_requests",
		metric.WithDescription("Total HTTP requests by method, path, and status code."))
	httpDuration, _ = meter.Float64Histogram("http_request_duration",
		metric.WithDescription("HTTP request latency histogram."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))
)

// RecordHTTP records a completed HTTP request. Called from the RequestLogger middleware.
// Only API paths (/api/... and /metrics) are recorded; static file requests are ignored.
func RecordHTTP(method, path string, status int, d time.Duration) {
	p := normalizePath(path)
	if p == "" {
		return
	}
	ctx := context.Background()
	httpRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("path", p),
		attribute.String("status", strconv.Itoa(status)),
	))
	httpDuration.Record(ctx, d.Seconds(), metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("path", p),
	))
}

// normalizePath returns the normalised path for metric recording, collapsing
// variable segments to keep cardinality low. Returns "" for non-API paths
// (static files, favicons, images) which should not be recorded.
//
//	/api/datasets/tank/data  → /api/datasets/{name}
//	/api/snapshots/tank@s    → /api/snapshots/{name}
//	/api/pools               → /api/pools  (no variable segment)
//	/metrics                 → /metrics
//	/app.js, /favicon.ico, … → ""  (skipped)
func normalizePath(p string) string {
	if p == "/metrics" {
		return p
	}
	if !strings.HasPrefix(p, "/api/") {
		return ""
	}
	// Strip /api/ and split on first remaining slash.
	resource, sub, hasMore := strings.Cut(strings.TrimPrefix(p, "/api/"), "/")
	if hasMore && sub != "" {
		return "/api/" + resource + "/{name}"
	}
	return p
}
